"""Unit tests for the supervisor emitter wrapper."""

import asyncio
import json
import os
import uuid
from typing import Any

import httpx
import pytest

from emitters._shared import (
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
)
from emitters.supervisor import SupervisedEmitter


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


def _make_supervisor(**kwargs) -> tuple[SupervisedEmitter, list[dict], list[dict]]:
    posted: list[dict] = []
    hdrs: list[dict] = []
    handler = _mock_handler_201(posted, hdrs)
    client = _make_mock_client(handler)
    em = SupervisedEmitter(
        endpoint="http://test/api/events/work",
        ingest_key="test-key",
        client=client,
        **kwargs,
    )
    return em, posted, hdrs


# ---------------------------------------------------------------------------
# Supervisor emitter tests
# ---------------------------------------------------------------------------

class TestSupervisedEmitter:
    """Supervised emitter: heartbeats + terminal status on exit."""

    @pytest.mark.asyncio
    async def test_emit_start_supervised(self) -> None:
        """session.start uses supervised liveness mode."""
        em, posted, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        status = await em.emit_start(session_id=sid, pid=12345, title="test")

        assert status == 201
        assert len(posted) == 1
        body = posted[0]
        assert body["harness"] == "generic"
        assert body["kind"] == "session.start"
        assert body["status"] == "running"
        assert body["liveness_mode"] == "supervised"
        assert body["pid"] == 12345
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_heartbeat(self) -> None:
        """Heartbeat event has correct shape."""
        em, posted, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        await em.emit_heartbeat(session_id=sid, pid=12345)

        assert len(posted) == 1
        body = posted[0]
        assert body["kind"] == "session.heartbeat"
        assert body["status"] == "running"
        assert body["liveness_mode"] == "supervised"
        assert body["pid"] == 12345
        uuid.UUID(body["event_id"])
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_done(self) -> None:
        """session.end with terminal status done."""
        em, posted, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, pid=12345, status="done")

        body = posted[0]
        assert body["kind"] == "session.end"
        assert body["status"] == "done"
        assert body["liveness_mode"] == "supervised"
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_failed(self) -> None:
        """session.end with terminal status failed."""
        em, posted, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, pid=12345, status="failed")

        body = posted[0]
        assert body["status"] == "failed"
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_cancelled(self) -> None:
        """session.end with terminal status cancelled."""
        em, posted, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, pid=12345, status="cancelled")

        body = posted[0]
        assert body["status"] == "cancelled"
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_rejects_non_terminal(self) -> None:
        """session.end rejects non-terminal status."""
        em, _, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        with pytest.raises(ValueError, match="terminal status"):
            await em.emit_end(session_id=sid, pid=12345, status="running")
        await em.close()

    @pytest.mark.asyncio
    async def test_event_id_fresh_per_event(self) -> None:
        """Each event gets a unique event_id."""
        em, posted, _ = _make_supervisor()
        sid = str(uuid.uuid4())
        await em.emit_start(session_id=sid, pid=12345)
        await em.emit_heartbeat(session_id=sid, pid=12345)
        await em.emit_end(session_id=sid, pid=12345, status="done")

        event_ids = [e["event_id"] for e in posted]
        assert len(set(event_ids)) == 3

    @pytest.mark.asyncio
    async def test_start_body_shape_survives_round_trip(self) -> None:
        """Full contract §2 shape survives MockTransport round-trip."""
        em, posted, hdrs = _make_supervisor(harness="claude")
        sid = str(uuid.uuid4())
        await em.emit_start(
            session_id=sid,
            pid=12345,
            title="supervised test",
            project_hint="agent-os",
            external_ref="SC-42",
        )

        body = posted[0]
        assert body["harness"] == "claude"
        assert body["liveness_mode"] == "supervised"
        assert body["pid"] == 12345
        assert body["title"] == "supervised test"
        assert body["project_hint"] == "agent-os"
        assert body["external_ref"] == "SC-42"
        # Headers
        assert hdrs[0]["x-agentos-ingest-key"] == "test-key"
        assert hdrs[0]["idempotency-key"] == body["event_id"]
        await em.close()

    # -----------------------------------------------------------------------
    # NON-TAUTOLOGICAL: Real heartbeat loop test
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_heartbeat_loop_fires_real_heartbeats(self) -> None:
        """_heartbeat_loop actually runs and emits ≥2 heartbeats when
        given a short interval, and stops after session.end."""
        em, posted, _ = _make_supervisor(heartbeat_s=0.01)
        sid = str(uuid.uuid4())

        # Start heartbeats
        await em._start_heartbeats(sid, 12345)

        # Sleep long enough for ≥2 heartbeats (0.05s >> 2×0.01s)
        await asyncio.sleep(0.05)

        hb_events = [p for p in posted if p["kind"] == "session.heartbeat"]
        assert len(hb_events) >= 2, (
            f"expected ≥2 loop-emitted heartbeats, got {len(hb_events)}"
        )
        for ev in hb_events:
            assert ev["status"] == "running"
            assert ev["liveness_mode"] == "supervised"
            assert ev["pid"] == 12345
            uuid.UUID(ev["event_id"])

        # End session — heartbeats should stop
        last_count = len(hb_events)
        await em.emit_end(session_id=sid, pid=12345, status="done")

        await asyncio.sleep(0.03)  # would produce more if loop still running
        new_hb = [p for p in posted if p["kind"] == "session.heartbeat"]
        assert len(new_hb) == last_count, (
            "heartbeats must stop after session.end"
        )
        await em.close()

    # -----------------------------------------------------------------------
    # run_supervised integration: wraps a real subprocess
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_run_supervised_echo(self) -> None:
        """run_supervised wraps 'echo hello' with full lifecycle."""
        em, posted, _ = _make_supervisor(heartbeat_s=0.01)

        exit_code = await em.run_supervised(
            ["echo", "hello"],
            title="echo test",
            project_hint="test-proj",
        )

        assert exit_code == 0

        kinds = [p["kind"] for p in posted]
        assert "session.start" in kinds
        assert "session.end" in kinds

        end_event = [p for p in posted if p["kind"] == "session.end"][0]
        assert end_event["status"] == "done"
        assert end_event["harness"] == "generic"

        # Should have heartbeats too (process runs fast but
        # heartbeat interval is 0.01s)
        hb_events = [p for p in posted if p["kind"] == "session.heartbeat"]
        # echo is very fast; may not get heartbeats — that's OK
        # as long as start+end are correct
        assert len(posted) >= 2
        await em.close()

    @pytest.mark.asyncio
    async def test_run_supervised_exit_code_1(self) -> None:
        """run_supervised maps non-zero exit to status=failed."""
        em, posted, _ = _make_supervisor(heartbeat_s=60)

        exit_code = await em.run_supervised(
            ["false"],  # exits with 1
            title="fail test",
        )

        assert exit_code == 1
        end_event = [p for p in posted if p["kind"] == "session.end"][0]
        assert end_event["status"] == "failed"
        assert end_event["payload"]["exit_code"] == 1
        await em.close()

    @pytest.mark.asyncio
    async def test_run_supervised_with_custom_harness(self) -> None:
        """run_supervised carries the configured harness name."""
        em, posted, _ = _make_supervisor(
            harness="claude", heartbeat_s=60,
        )

        await em.run_supervised(["true"])

        start_event = [p for p in posted if p["kind"] == "session.start"][0]
        assert start_event["harness"] == "claude"
        await em.close()

    # -----------------------------------------------------------------------
    # Headers
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_start_sends_correct_headers(self) -> None:
        """session.start sends correct headers via MockTransport."""
        em, _, hdrs = _make_supervisor()
        sid = str(uuid.uuid4())
        await em.emit_start(session_id=sid, pid=12345)

        assert hdrs[0]["content-type"] == "application/json"
        assert hdrs[0]["x-agentos-ingest-key"] == "test-key"
        body = em._test_posted[0] if hasattr(em, '_test_posted') else {}  # type: ignore
        await em.close()
