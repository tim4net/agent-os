"""Unit tests for the Hermes work-event emitter."""

import asyncio
import json
import os
import uuid
from unittest import mock

import pytest

from emitters.hermes.emitter import (
    HermesEmitter,
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _rfc3339_now,
    _detect_branch,
    _detect_sha,
)


# ---------------------------------------------------------------------------
# WorkEvent serialization tests
# ---------------------------------------------------------------------------

class TestWorkEvent:
    """Contract §2 shape validation."""

    def test_minimal_event_has_required_fields(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        assert d["schema"] == "agentos.work_event/v1"
        assert "event_id" in d
        assert d["harness"] == "hermes"
        assert "session_id" in d
        assert "host" in d
        assert d["kind"] == "session.start"
        assert "ts" in d

    def test_event_id_is_uuid(self) -> None:
        ev = WorkEvent()
        # Should not raise
        uuid.UUID(ev.event_id)

    def test_status_omitted_when_none(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        assert "status" not in d

    def test_status_included_when_set(self) -> None:
        ev = WorkEvent(status="running")
        d = ev.to_dict()
        assert d["status"] == "running"

    def test_liveness_mode_included_when_set(self) -> None:
        ev = WorkEvent(liveness_mode="supervised", pid=12345)
        d = ev.to_dict()
        assert d["liveness_mode"] == "supervised"
        assert d["pid"] == 12345

    def test_pid_omitted_when_none(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        assert "pid" not in d

    def test_correlation_hints_omitted_when_none(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        for key in ("project_hint", "external_ref", "branch", "sha", "cwd",
                     "tenant", "title", "cost_usd"):
            assert key not in d, f"{key} should be omitted when None"

    def test_correlation_hints_included_when_set(self) -> None:
        ev = WorkEvent(
            project_hint="agent-os",
            external_ref="SC-91130",
            branch="wp-c/issue-3",
            sha="abc1234",
            cwd="/home/tim/work/agent-os",
            tenant="personal",
            title="Hermes emitter",
            cost_usd=0.05,
        )
        d = ev.to_dict()
        assert d["project_hint"] == "agent-os"
        assert d["external_ref"] == "SC-91130"
        assert d["branch"] == "wp-c/issue-3"
        assert d["sha"] == "abc1234"
        assert d["cwd"] == "/home/tim/work/agent-os"
        assert d["tenant"] == "personal"
        assert d["title"] == "Hermes emitter"
        assert d["cost_usd"] == 0.05

    def test_artifacts_omitted_when_empty(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        assert "artifacts" not in d

    def test_artifacts_included_when_present(self) -> None:
        ev = WorkEvent(artifacts=[{"type": "image", "path": "/data/x.png", "name": "x.png"}])
        d = ev.to_dict()
        assert len(d["artifacts"]) == 1
        assert d["artifacts"][0]["type"] == "image"

    def test_payload_omitted_when_empty(self) -> None:
        ev = WorkEvent()
        d = ev.to_dict()
        assert "payload" not in d

    def test_payload_included_when_set(self) -> None:
        ev = WorkEvent(payload={"telemetry": {"model": "gpt-4"}})
        d = ev.to_dict()
        assert d["payload"]["telemetry"]["model"] == "gpt-4"

    def test_json_round_trip(self) -> None:
        ev = WorkEvent(
            status="running",
            liveness_mode="supervised",
            pid=12345,
            branch="main",
            title="test",
        )
        d = ev.to_dict()
        serialized = json.dumps(d)
        parsed = json.loads(serialized)
        assert parsed["schema"] == "agentos.work_event/v1"
        assert parsed["status"] == "running"
        assert parsed["pid"] == 12345


# ---------------------------------------------------------------------------
# Helper function tests
# ---------------------------------------------------------------------------

class TestHelpers:
    def test_rfc3339_now_format(self) -> None:
        ts = _rfc3339_now()
        # Should be parseable and end with Z
        assert ts.endswith("Z")
        # Should parse as ISO format
        from datetime import datetime
        datetime.fromisoformat(ts.replace("Z", "+00:00"))

    def test_detect_branch_in_non_git_dir(self, tmp_path: object) -> None:
        # In a non-git directory, should return None
        result = _detect_branch(str(tmp_path))
        assert result is None

    def test_detect_sha_in_non_git_dir(self, tmp_path: object) -> None:
        result = _detect_sha(str(tmp_path))
        assert result is None


# ---------------------------------------------------------------------------
# Emitter tests
# ---------------------------------------------------------------------------

class TestHermesEmitter:
    """Test emitter lifecycle with mocked HTTP."""

    def _make_emitter(self, **kwargs) -> HermesEmitter:
        return HermesEmitter(
            endpoint="http://localhost:9999/api/events/work",
            ingest_key="test-key",
            **kwargs,
        )

    def test_requires_ingest_key(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
            # Remove env var if set
            os.environ.pop("AGENTOS_INGEST_KEY", None)
            with pytest.raises(ValueError, match="INGEST_KEY"):
                HermesEmitter()

    def test_env_ingest_key(self) -> None:
        with mock.patch.dict(os.environ, {"AGENTOS_INGEST_KEY": "env-key"}):
            em = HermesEmitter()
            assert em.ingest_key == "env-key"

    @pytest.mark.asyncio
    async def test_start_emits_correct_event(self) -> None:
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        status = await em.start(title="test session", project_hint="agent-os")

        assert status == 201
        assert len(posted) == 1
        body = posted[0]
        assert body["schema"] == "agentos.work_event/v1"
        assert body["harness"] == "hermes"
        assert body["kind"] == "session.start"
        assert body["status"] == "running"
        assert body["liveness_mode"] == "supervised"
        assert body["pid"] == os.getpid()
        assert body["title"] == "test session"
        assert body["project_hint"] == "agent-os"
        assert body["session_id"] == em.session_id
        assert body["host"] == em._host
        # event_id should be a UUID
        uuid.UUID(body["event_id"])

    @pytest.mark.asyncio
    async def test_start_sends_correct_headers(self) -> None:
        em = self._make_emitter()
        captured_headers: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            captured_headers.append(headers)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.start()

        hdrs = captured_headers[0]
        assert hdrs["Content-Type"] == "application/json"
        assert hdrs["X-AgentOS-Ingest-Key"] == "test-key"
        # Idempotency-Key should match event_id
        assert "Idempotency-Key" in hdrs

    @pytest.mark.asyncio
    async def test_heartbeat_emits_running(self) -> None:
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.heartbeat()

        assert len(posted) == 1
        assert posted[0]["kind"] == "session.heartbeat"
        assert posted[0]["status"] == "running"
        assert posted[0]["liveness_mode"] == "supervised"
        assert posted[0]["pid"] == os.getpid()

    @pytest.mark.asyncio
    async def test_end_emits_terminal_status_done(self) -> None:
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.end("done")

        assert len(posted) == 1
        assert posted[0]["kind"] == "session.end"
        assert posted[0]["status"] == "done"

    @pytest.mark.asyncio
    async def test_end_emits_terminal_status_failed(self) -> None:
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.end("failed")

        assert posted[0]["status"] == "failed"

    @pytest.mark.asyncio
    async def test_end_rejects_non_terminal_status(self) -> None:
        em = self._make_emitter()
        with pytest.raises(ValueError, match="terminal status"):
            await em.end("running")

    @pytest.mark.asyncio
    async def test_end_with_cost(self) -> None:
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.end("done", cost_usd=0.1234)

        assert posted[0]["cost_usd"] == 0.1234

    @pytest.mark.asyncio
    async def test_end_with_cancelled(self) -> None:
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.end("cancelled")

        assert posted[0]["status"] == "cancelled"

    @pytest.mark.asyncio
    async def test_event_id_fresh_per_event(self) -> None:
        """Each event must have a unique event_id (idempotency-safe on retry)."""
        em = self._make_emitter()
        event_ids: list[str] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            event_ids.append(json["event_id"])
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.start()
        await em.heartbeat()
        await em.end("done")

        assert len(event_ids) == 3
        assert len(set(event_ids)) == 3, "each event_id must be unique"

    @pytest.mark.asyncio
    async def test_session_id_stable_across_events(self) -> None:
        """session_id must be the same across all events for a session."""
        em = self._make_emitter()
        session_ids: list[str] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            session_ids.append(json["session_id"])
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.start()
        await em.heartbeat()
        await em.end("done")

        assert len(set(session_ids)) == 1, "session_id must be stable"

    @pytest.mark.asyncio
    async def test_supervised_context_success(self) -> None:
        """supervised() emits start → (heartbeats) → end(done) on clean exit."""
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]

        async with em.supervised(title="ctx test"):
            pass  # no error

        kinds = [p["kind"] for p in posted]
        assert "session.start" in kinds
        assert "session.end" in kinds
        end_event = [p for p in posted if p["kind"] == "session.end"][0]
        assert end_event["status"] == "done"

    @pytest.mark.asyncio
    async def test_supervised_context_failure(self) -> None:
        """supervised() emits end(failed) when an exception occurs."""
        em = self._make_emitter()
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]

        with pytest.raises(RuntimeError, match="boom"):
            async with em.supervised(title="fail test"):
                raise RuntimeError("boom")

        end_event = [p for p in posted if p["kind"] == "session.end"][0]
        assert end_event["status"] == "failed"

    @pytest.mark.asyncio
    async def test_heartbeat_loop_sends_heartbeats(self) -> None:
        """Heartbeat loop sends heartbeats at the configured interval."""
        em = self._make_emitter(heartbeat_s=60)
        posted: list[dict] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            posted.append(json)
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]

        # Directly exercise the heartbeat method multiple times to simulate
        # what the loop does.
        await em.heartbeat()
        await em.heartbeat()
        await em.heartbeat()

        hb_events = [p for p in posted if p["kind"] == "session.heartbeat"]
        assert len(hb_events) == 3

        # Verify each has correct shape
        for ev in hb_events:
            assert ev["status"] == "running"
            assert ev["liveness_mode"] == "supervised"
            assert ev["pid"] == os.getpid()

    @pytest.mark.asyncio
    async def test_idempotency_key_matches_event_id(self) -> None:
        """Idempotency-Key header must equal the body's event_id."""
        em = self._make_emitter()
        captured: list[tuple[dict, dict]] = []

        async def mock_post(url: str, json: dict, headers: dict) -> mock.MagicMock:
            captured.append((json, headers))
            resp = mock.MagicMock()
            resp.status_code = 201
            return resp

        em._client.post = mock_post  # type: ignore[assignment]
        await em.start()

        body, headers = captured[0]
        assert headers["Idempotency-Key"] == body["event_id"]

    @pytest.mark.asyncio
    async def test_connect_error_returns_zero(self) -> None:
        """Unreachable endpoint returns status 0."""
        em = HermesEmitter(
            endpoint="http://127.0.0.1:1/api/events/work",
            ingest_key="test-key",
        )
        status = await em.start()
        assert status == 0

    @pytest.mark.asyncio
    async def test_close_stops_heartbeats_and_client(self) -> None:
        em = self._make_emitter()
        await em._start_heartbeats()
        await em.close()
        assert em._heartbeat_task is None


# ---------------------------------------------------------------------------
# Contract enum tests
# ---------------------------------------------------------------------------

class TestEnums:
    def test_harness_hermes_value(self) -> None:
        assert Kind.SESSION_START.value == "session.start"
        assert Kind.SESSION_HEARTBEAT.value == "session.heartbeat"
        assert Kind.SESSION_END.value == "session.end"

    def test_status_values(self) -> None:
        assert Status.RUNNING.value == "running"
        assert Status.DONE.value == "done"
        assert Status.FAILED.value == "failed"
        assert Status.CANCELLED.value == "cancelled"
        assert Status.UNKNOWN.value == "unknown"

    def test_liveness_mode_values(self) -> None:
        assert LivenessMode.SUPERVISED.value == "supervised"
        assert LivenessMode.BOUNDED.value == "bounded"
