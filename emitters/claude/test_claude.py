"""Unit tests for the Claude Code work-event emitter."""

import asyncio
import json
import os
import uuid
from typing import Any
from unittest import mock

import httpx
import pytest

from emitters._shared import (
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
)
from emitters.claude import ClaudeEmitter, ClaudeResult


# ---------------------------------------------------------------------------
# MockTransport helpers
# ---------------------------------------------------------------------------

def _mock_handler_201(
    captured: list[dict[str, Any]],
    captured_headers: list[dict[str, str]] | None = None,
):
    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        captured.append(body)
        if captured_headers is not None:
            captured_headers.append(dict(request.headers))
        return httpx.Response(
            201, json={"id": "test-uuid", "accepted": True},
            request=request,
        )
    return handler


def _make_mock_client(handler) -> httpx.AsyncClient:
    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


def _make_claude_emitter(**kwargs) -> ClaudeEmitter:
    posted: list[dict] = []
    hdrs: list[dict] = []
    handler = _mock_handler_201(posted, hdrs)
    client = _make_mock_client(handler)
    em = ClaudeEmitter(
        endpoint="http://test/api/events/work",
        ingest_key="test-key",
        client=client,
        **kwargs,
    )
    em._test_posted = posted  # type: ignore[attr-defined]
    em._test_headers = hdrs  # type: ignore[attr-defined]
    return em


# ---------------------------------------------------------------------------
# Claude emitter tests
# ---------------------------------------------------------------------------

class TestClaudeEmitter:
    """Bounded emitter: session.start → session.end, no heartbeats."""

    @pytest.mark.asyncio
    async def test_emit_start_bounded(self) -> None:
        """session.start uses bounded liveness mode."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        status = await em.emit_start(session_id=sid, title="test session")

        assert status == 201
        assert len(em._test_posted) == 1  # type: ignore[attr-defined]
        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["schema"] == "agentos.work_event/v1"
        assert body["harness"] == "claude"
        assert body["kind"] == "session.start"
        assert body["status"] == "running"
        assert body["liveness_mode"] == "bounded"
        assert body["session_id"] == sid
        uuid.UUID(body["event_id"])
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_done(self) -> None:
        """session.end with status=done."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, status="done", cost_usd=0.05)

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["kind"] == "session.end"
        assert body["status"] == "done"
        assert body["liveness_mode"] == "bounded"
        assert body["cost_usd"] == 0.05
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_failed(self) -> None:
        """session.end with status=failed."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, status="failed")

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["status"] == "failed"
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_rejects_non_terminal(self) -> None:
        """session.end rejects non-terminal status."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        with pytest.raises(ValueError, match="terminal status"):
            await em.emit_end(session_id=sid, status="running")
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_start_headers(self) -> None:
        """session.start sends correct ingest headers."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        await em.emit_start(session_id=sid)

        hdrs = em._test_headers[0]  # type: ignore[attr-defined]
        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert hdrs.get("content-type") == "application/json"
        assert hdrs.get("x-agentos-ingest-key") == "test-key"
        assert hdrs.get("idempotency-key") == body["event_id"]
        await em.close()

    @pytest.mark.asyncio
    async def test_event_id_fresh_per_event(self) -> None:
        """Each event gets a unique event_id."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        await em.emit_start(session_id=sid)
        await em.emit_end(session_id=sid, status="done")

        event_ids = [e["event_id"] for e in em._test_posted]  # type: ignore[attr-defined]
        assert len(set(event_ids)) == 2
        await em.close()

    @pytest.mark.asyncio
    async def test_start_body_shape_survives_round_trip(self) -> None:
        """Full contract §2 shape survives a real MockTransport round-trip."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        await em.emit_start(
            session_id=sid,
            title="claude shape test",
            project_hint="agent-os",
            external_ref="SC-42",
        )

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["harness"] == "claude"
        assert body["liveness_mode"] == "bounded"
        assert body["title"] == "claude shape test"
        assert body["project_hint"] == "agent-os"
        assert body["external_ref"] == "SC-42"
        assert "cwd" in body
        await em.close()

    @pytest.mark.asyncio
    async def test_no_ingest_key_raises(self) -> None:
        """Emitting without ingest key raises _PostError."""
        em = ClaudeEmitter(
            endpoint="http://test",
            ingest_key=None,
        )
        sid = str(uuid.uuid4())
        with pytest.raises(_PostError, match="no ingest key"):
            await em.emit_start(session_id=sid)
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_with_payload_and_artifacts(self) -> None:
        """session.end can carry payload and artifacts."""
        em = _make_claude_emitter()
        sid = str(uuid.uuid4())
        await em.emit_end(
            session_id=sid,
            status="done",
            payload={"duration_s": 12.5, "model": "claude-opus-4-8"},
            artifacts=[{"type": "code", "path": "/data/x.py", "name": "x.py"}],
        )

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["payload"]["duration_s"] == 12.5
        assert len(body["artifacts"]) == 1
        assert body["artifacts"][0]["type"] == "code"
        await em.close()


class TestClaudeResult:
    """ClaudeResult dataclass tests."""

    def test_result_defaults(self) -> None:
        r = ClaudeResult(
            exit_code=0, stdout="", stderr="", duration_s=1.0,
        )
        assert r.output_json is None
        assert r.cost_usd is None

    def test_result_with_json(self) -> None:
        r = ClaudeResult(
            exit_code=0, stdout="{}", stderr="", duration_s=2.0,
            output_json={"result": "ok"},
            cost_usd=0.01,
        )
        assert r.output_json == {"result": "ok"}
        assert r.cost_usd == 0.01
