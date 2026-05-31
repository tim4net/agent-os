"""Unit tests for shared work-event types."""

import asyncio
import json
import uuid
from typing import Any

import httpx
import pytest

from emitters._shared.types import (
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
    _rfc3339_now,
    _detect_branch,
    _detect_sha,
    post_event,
)


# ---------------------------------------------------------------------------
# MockTransport helpers
# ---------------------------------------------------------------------------

def _mock_handler_201(captured: list[dict[str, Any]]):
    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        captured.append(body)
        return httpx.Response(
            201, json={"id": "test-uuid", "accepted": True},
            request=request,
        )
    return handler


def _make_mock_client(handler) -> httpx.AsyncClient:
    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


# ---------------------------------------------------------------------------
# Enum tests
# ---------------------------------------------------------------------------

class TestEnums:
    def test_kind_values(self) -> None:
        assert Kind.SESSION_START.value == "session.start"
        assert Kind.SESSION_HEARTBEAT.value == "session.heartbeat"
        assert Kind.SESSION_END.value == "session.end"
        assert Kind.ARTIFACT_CREATED.value == "artifact.created"
        assert Kind.SERVER_STARTED.value == "server.started"
        assert Kind.SERVER_STOPPED.value == "server.stopped"
        assert Kind.NOTE.value == "note"

    def test_status_values(self) -> None:
        assert Status.RUNNING.value == "running"
        assert Status.DONE.value == "done"
        assert Status.FAILED.value == "failed"
        assert Status.CANCELLED.value == "cancelled"
        assert Status.UNKNOWN.value == "unknown"

    def test_liveness_mode_values(self) -> None:
        assert LivenessMode.SUPERVISED.value == "supervised"
        assert LivenessMode.BOUNDED.value == "bounded"


# ---------------------------------------------------------------------------
# WorkEvent serialization tests (contract §2)
# ---------------------------------------------------------------------------

class TestWorkEvent:
    def test_minimal_event_has_required_fields(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        assert d["schema"] == "agentos.work_event/v1"
        assert "event_id" in d
        assert d["harness"] == "generic"
        assert "session_id" in d
        assert "host" in d
        assert d["kind"] == "session.start"
        assert "ts" in d

    def test_event_id_is_uuid(self) -> None:
        ev = WorkEvent()
        uuid.UUID(ev.event_id)

    def test_harness_is_settable(self) -> None:
        ev = WorkEvent(harness="claude")
        assert ev.to_dict()["harness"] == "claude"

    def test_status_omitted_when_none(self) -> None:
        ev = WorkEvent()
        assert "status" not in ev.to_dict()

    def test_status_included_when_set(self) -> None:
        ev = WorkEvent(status="running")
        assert ev.to_dict()["status"] == "running"

    def test_liveness_mode_included_when_set(self) -> None:
        ev = WorkEvent(liveness_mode="supervised", pid=12345)
        d = ev.to_dict()
        assert d["liveness_mode"] == "supervised"
        assert d["pid"] == 12345

    def test_pid_omitted_when_none(self) -> None:
        ev = WorkEvent()
        assert "pid" not in ev.to_dict()

    def test_correlation_hints_omitted_when_none(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        for key in ("project_hint", "external_ref", "branch", "sha", "cwd",
                     "tenant", "title", "cost_usd"):
            assert key not in d, f"{key} should be omitted when None"

    def test_correlation_hints_included_when_set(self) -> None:
        ev = WorkEvent(
            project_hint="agent-os",
            external_ref="SC-42",
            branch="wp-d/issue-13",
            sha="abc1234",
            cwd="/home/tim/work/agent-os",
            tenant="personal",
            title="WP-D emitters",
            cost_usd=0.05,
        )
        d = ev.to_dict()
        assert d["project_hint"] == "agent-os"
        assert d["external_ref"] == "SC-42"
        assert d["branch"] == "wp-d/issue-13"
        assert d["cost_usd"] == 0.05

    def test_artifacts_omitted_when_empty(self) -> None:
        ev = WorkEvent()
        assert "artifacts" not in ev.to_dict()

    def test_artifacts_included_when_present(self) -> None:
        ev = WorkEvent(artifacts=[{"type": "image", "path": "/data/x.png"}])
        assert len(ev.to_dict()["artifacts"]) == 1

    def test_payload_omitted_when_empty(self) -> None:
        ev = WorkEvent()
        assert "payload" not in ev.to_dict()

    def test_payload_included_when_set(self) -> None:
        ev = WorkEvent(payload={"telemetry": {"model": "gpt-4"}})
        assert ev.to_dict()["payload"]["telemetry"]["model"] == "gpt-4"

    def test_json_round_trip(self) -> None:
        ev = WorkEvent(
            harness="claude",
            status="running",
            liveness_mode="supervised",
            pid=12345,
        )
        d = ev.to_dict()
        parsed = json.loads(json.dumps(d))
        assert parsed["schema"] == "agentos.work_event/v1"
        assert parsed["harness"] == "claude"
        assert parsed["pid"] == 12345


# ---------------------------------------------------------------------------
# Helper function tests
# ---------------------------------------------------------------------------

class TestHelpers:
    def test_rfc3339_now_format(self) -> None:
        ts = _rfc3339_now()
        assert ts.endswith("Z")
        from datetime import datetime
        datetime.fromisoformat(ts.replace("Z", "+00:00"))

    def test_detect_branch_in_non_git_dir(self, tmp_path: object) -> None:
        assert _detect_branch(str(tmp_path)) is None

    def test_detect_sha_in_non_git_dir(self, tmp_path: object) -> None:
        assert _detect_sha(str(tmp_path)) is None


# ---------------------------------------------------------------------------
# post_event tests (real HTTP round-trip via MockTransport)
# ---------------------------------------------------------------------------

class TestPostEvent:
    @pytest.mark.asyncio
    async def test_post_returns_201(self) -> None:
        captured: list[dict] = []
        handler = _mock_handler_201(captured)
        client = _make_mock_client(handler)

        ev = WorkEvent(harness="test")
        status = await post_event(
            ev, endpoint="http://test", ingest_key="key", client=client,
        )

        assert status == 201
        assert len(captured) == 1
        assert captured[0]["harness"] == "test"
        await client.aclose()

    @pytest.mark.asyncio
    async def test_post_4xx_raises(self) -> None:
        def handler(request: httpx.Request) -> httpx.Response:
            return httpx.Response(422, text="bad event", request=request)
        client = _make_mock_client(handler)

        ev = WorkEvent()
        with pytest.raises(_PostError) as exc_info:
            await post_event(
                ev, endpoint="http://test", ingest_key="key",
                client=client, max_retries=1,
            )
        assert exc_info.value.status_code == 422
        await client.aclose()

    @pytest.mark.asyncio
    async def test_post_5xx_retries(self) -> None:
        call_count = 0
        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal call_count
            call_count += 1
            return httpx.Response(500, text="server error", request=request)
        client = _make_mock_client(handler)

        ev = WorkEvent()
        with pytest.raises(_PostError):
            await post_event(
                ev, endpoint="http://test", ingest_key="key",
                client=client, max_retries=3, retry_backoff_s=0.001,
            )
        assert call_count == 3
        await client.aclose()

    @pytest.mark.asyncio
    async def test_post_retains_transport_cause(self) -> None:
        ev = WorkEvent()
        with pytest.raises(_PostError) as exc_info:
            await post_event(
                ev,
                endpoint="http://127.0.0.1:1/api/events/work",
                ingest_key="key",
                max_retries=1,
                retry_backoff_s=0.001,
            )
        assert exc_info.value.cause is not None
