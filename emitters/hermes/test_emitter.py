"""Unit tests for the Hermes work-event emitter."""

import asyncio
import json
import os
import uuid
from typing import Any
from unittest import mock

import httpx
import pytest

from emitters.hermes.emitter import (
    HermesEmitter,
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
    _rfc3339_now,
    _detect_branch,
    _detect_sha,
)


# ---------------------------------------------------------------------------
# MockTransport helpers (Finding 2: real HTTP round-trip, not MagicMock)
# ---------------------------------------------------------------------------

def _mock_handler_201(
    captured: list[dict[str, Any]],
    captured_headers: list[dict[str, str]] | None = None,
):
    """Return a MockTransport handler that captures requests and returns 201."""
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


def _mock_handler_status(status_code: int, reason: str = "test"):
    """Return a MockTransport handler that always returns the given status."""
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            status_code, text=reason, request=request,
        )
    return handler


def _make_mock_client(handler) -> httpx.AsyncClient:
    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


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
        assert ts.endswith("Z")
        from datetime import datetime
        datetime.fromisoformat(ts.replace("Z", "+00:00"))

    def test_detect_branch_in_non_git_dir(self, tmp_path: object) -> None:
        result = _detect_branch(str(tmp_path))
        assert result is None

    def test_detect_sha_in_non_git_dir(self, tmp_path: object) -> None:
        result = _detect_sha(str(tmp_path))
        assert result is None

    def test_detect_branch_in_git_dir(self, tmp_path: object) -> None:
        """Finding 6: branch/sha are detected when in a git repo."""
        import subprocess
        subprocess.run(["git", "init"], cwd=str(tmp_path), check=True,
                       capture_output=True)
        subprocess.run(["git", "checkout", "-b", "my-feature"],
                       cwd=str(tmp_path), check=True, capture_output=True)
        # Need at least one commit for rev-parse to work
        readme = tmp_path / "README.md"  # type: ignore
        readme.write_text("init")
        subprocess.run(["git", "add", "."], cwd=str(tmp_path), check=True,
                       capture_output=True)
        subprocess.run(["git", "-c", "user.name=test", "-c",
                        "user.email=t@t", "commit", "-m", "init"],
                       cwd=str(tmp_path), check=True, capture_output=True)

        branch = _detect_branch(str(tmp_path))
        assert branch == "my-feature"

        sha = _detect_sha(str(tmp_path))
        assert sha is not None and len(sha) >= 7

    def test_detect_branch_in_detached_head_returns_none(self, tmp_path: object) -> None:
        """Regression: detached-HEAD must return None, not the literal 'HEAD'."""
        import subprocess
        subprocess.run(["git", "init"], cwd=str(tmp_path), check=True,
                       capture_output=True)
        readme = tmp_path / "README.md"  # type: ignore
        readme.write_text("init")
        subprocess.run(["git", "add", "."], cwd=str(tmp_path), check=True,
                       capture_output=True)
        subprocess.run(["git", "-c", "user.name=test", "-c",
                        "user.email=t@t", "commit", "-m", "init"],
                       cwd=str(tmp_path), check=True, capture_output=True)
        # Detach HEAD by checking out the commit SHA
        sha_out = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=str(tmp_path),
            check=True, capture_output=True, text=True,
        )
        subprocess.run(["git", "checkout", sha_out.stdout.strip()],
                       cwd=str(tmp_path), check=True, capture_output=True)
        branch = _detect_branch(str(tmp_path))
        assert branch is None, f"Expected None in detached HEAD, got {branch!r}"


# ---------------------------------------------------------------------------
# Emitter tests (using httpx.MockTransport — Finding 2)
# ---------------------------------------------------------------------------

class TestHermesEmitter:
    """Test emitter lifecycle with MockTransport (real HTTP round-trip)."""

    def _make_emitter(self, **kwargs) -> HermesEmitter:
        posted: list[dict] = []
        hdrs: list[dict] = []
        handler = _mock_handler_201(posted, hdrs)
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            **kwargs,
        )
        # Stash for test access
        em._test_posted = posted  # type: ignore[attr-defined]
        em._test_headers = hdrs  # type: ignore[attr-defined]
        return em

    def test_requires_ingest_key(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
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
        status = await em.start(title="test session", project_hint="agent-os")

        assert status == 201
        assert len(em._test_posted) == 1  # type: ignore[attr-defined]
        body = em._test_posted[0]  # type: ignore[attr-defined]
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
        uuid.UUID(body["event_id"])
        await em.close()

    @pytest.mark.asyncio
    async def test_start_sends_correct_headers(self) -> None:
        em = self._make_emitter()
        await em.start()

        hdrs = em._test_headers[0]  # type: ignore[attr-defined]
        assert hdrs.get("content-type") == "application/json"
        assert hdrs.get("x-agentos-ingest-key") == "test-key"
        # Idempotency-Key should match event_id
        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert hdrs.get("idempotency-key") == body["event_id"]
        await em.close()

    @pytest.mark.asyncio
    async def test_heartbeat_emits_running(self) -> None:
        em = self._make_emitter()
        await em.heartbeat()

        assert len(em._test_posted) == 1  # type: ignore[attr-defined]
        assert em._test_posted[0]["kind"] == "session.heartbeat"  # type: ignore[attr-defined]
        assert em._test_posted[0]["status"] == "running"  # type: ignore[attr-defined]
        assert em._test_posted[0]["liveness_mode"] == "supervised"  # type: ignore[attr-defined]
        assert em._test_posted[0]["pid"] == os.getpid()  # type: ignore[attr-defined]
        await em.close()

    @pytest.mark.asyncio
    async def test_end_emits_terminal_status_done(self) -> None:
        em = self._make_emitter()
        await em.end("done")

        assert em._test_posted[0]["kind"] == "session.end"  # type: ignore[attr-defined]
        assert em._test_posted[0]["status"] == "done"  # type: ignore[attr-defined]
        await em.close()

    @pytest.mark.asyncio
    async def test_end_emits_terminal_status_failed(self) -> None:
        em = self._make_emitter()
        await em.end("failed")

        assert em._test_posted[0]["status"] == "failed"  # type: ignore[attr-defined]
        await em.close()

    @pytest.mark.asyncio
    async def test_end_rejects_non_terminal_status(self) -> None:
        em = self._make_emitter()
        with pytest.raises(ValueError, match="terminal status"):
            await em.end("running")
        await em.close()

    @pytest.mark.asyncio
    async def test_end_with_cost(self) -> None:
        em = self._make_emitter()
        await em.end("done", cost_usd=0.1234)

        assert em._test_posted[0]["cost_usd"] == 0.1234  # type: ignore[attr-defined]
        await em.close()

    @pytest.mark.asyncio
    async def test_end_with_telemetry_writes_payload_telemetry(self) -> None:
        """Path B: emitter self-reports token usage at payload.telemetry (contract §5)."""
        em = self._make_emitter()
        await em.end(
            "done",
            telemetry={
                "model": "claude-opus-4-8",
                "context_window": 200000,
                "tokens_used": 142378,
                "turns": 7,
            },
        )

        posted = em._test_posted[0]  # type: ignore[attr-defined]
        tele = posted["payload"]["telemetry"]
        assert tele["model"] == "claude-opus-4-8"
        assert tele["context_window"] == 200000
        assert tele["tokens_used"] == 142378
        assert tele["turns"] == 7
        await em.close()

    @pytest.mark.asyncio
    async def test_end_without_telemetry_omits_it(self) -> None:
        """No telemetry arg -> no telemetry sub-block (never fabricate — contract §5/F10)."""
        em = self._make_emitter()
        await em.end("done")

        posted = em._test_posted[0]  # type: ignore[attr-defined]
        # payload may be absent entirely, or present without a telemetry key
        assert "telemetry" not in posted.get("payload", {})
        await em.close()

    @pytest.mark.asyncio
    async def test_end_telemetry_drops_none_fields(self) -> None:
        """Only provided fields are sent; None values are stripped (never fabricated)."""
        em = self._make_emitter()
        await em.end(
            "done",
            telemetry={"tokens_used": 5000, "model": None, "turns": None},
        )

        tele = em._test_posted[0]["payload"]["telemetry"]  # type: ignore[attr-defined]
        assert tele == {"tokens_used": 5000}
        await em.close()

    @pytest.mark.asyncio
    async def test_end_empty_telemetry_omits_block(self) -> None:
        """An all-None / empty telemetry dict produces no telemetry sub-block."""
        em = self._make_emitter()
        await em.end("done", telemetry={"model": None, "tokens_used": None})

        posted = em._test_posted[0]  # type: ignore[attr-defined]
        assert "telemetry" not in posted.get("payload", {})
        await em.close()

    @pytest.mark.asyncio
    async def test_end_with_cancelled(self) -> None:
        em = self._make_emitter()
        await em.end("cancelled")

        assert em._test_posted[0]["status"] == "cancelled"  # type: ignore[attr-defined]
        await em.close()

    @pytest.mark.asyncio
    async def test_event_id_fresh_per_event(self) -> None:
        """Each event must have a unique event_id (idempotency-safe on retry)."""
        em = self._make_emitter()
        await em.start()
        await em.heartbeat()
        await em.end("done")

        event_ids = [e["event_id"] for e in em._test_posted]  # type: ignore[attr-defined]
        assert len(event_ids) == 3
        assert len(set(event_ids)) == 3, "each event_id must be unique"
        await em.close()

    @pytest.mark.asyncio
    async def test_session_id_stable_across_events(self) -> None:
        """session_id must be the same across all events for a session."""
        em = self._make_emitter()
        await em.start()
        await em.heartbeat()
        await em.end("done")

        session_ids = [e["session_id"] for e in em._test_posted]  # type: ignore[attr-defined]
        assert len(set(session_ids)) == 1, "session_id must be stable"
        await em.close()

    @pytest.mark.asyncio
    async def test_idempotency_key_matches_event_id(self) -> None:
        """Idempotency-Key header must equal the body's event_id."""
        em = self._make_emitter()
        await em.start()

        body = em._test_posted[0]  # type: ignore[attr-defined]
        hdrs = em._test_headers[0]  # type: ignore[attr-defined]
        assert hdrs.get("idempotency-key") == body["event_id"]
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_context_success(self) -> None:
        """supervised() emits start → end(done) on clean exit."""
        em = self._make_emitter()

        async with em.supervised(title="ctx test"):
            pass  # no error

        kinds = [p["kind"] for p in em._test_posted]  # type: ignore[attr-defined]
        assert "session.start" in kinds
        assert "session.end" in kinds
        end_event = [p for p in em._test_posted if p["kind"] == "session.end"][0]  # type: ignore[attr-defined]
        assert end_event["status"] == "done"
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_record_reports_telemetry_on_end(self) -> None:
        """Path B: s.record(...) during the block self-reports usage on session.end."""
        em = self._make_emitter()

        async with em.supervised(title="telemetry test") as s:
            s.record(tokens_used=88000, model="claude-opus-4-8")
            s.record(turns=12)  # later call merges, latest-wins per key

        end_event = [p for p in em._test_posted if p["kind"] == "session.end"][0]  # type: ignore[attr-defined]
        tele = end_event["payload"]["telemetry"]
        assert tele["tokens_used"] == 88000
        assert tele["model"] == "claude-opus-4-8"
        assert tele["turns"] == 12
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_record_ignores_none(self) -> None:
        """record() ignores None values so a metric is never fabricated."""
        em = self._make_emitter()

        async with em.supervised(title="none test") as s:
            s.record(tokens_used=4242, model=None)

        end_event = [p for p in em._test_posted if p["kind"] == "session.end"][0]  # type: ignore[attr-defined]
        assert end_event["payload"]["telemetry"] == {"tokens_used": 4242}
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_no_record_omits_telemetry(self) -> None:
        """No record() call -> no telemetry sub-block on end."""
        em = self._make_emitter()

        async with em.supervised(title="bare test"):
            pass

        end_event = [p for p in em._test_posted if p["kind"] == "session.end"][0]  # type: ignore[attr-defined]
        assert "telemetry" not in end_event.get("payload", {})
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_context_failure(self) -> None:
        """supervised() emits end(failed) when an exception occurs."""
        em = self._make_emitter()

        with pytest.raises(RuntimeError, match="boom"):
            async with em.supervised(title="fail test"):
                raise RuntimeError("boom")

        end_event = [p for p in em._test_posted if p["kind"] == "session.end"][0]  # type: ignore[attr-defined]
        assert end_event["status"] == "failed"
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_context_cancellation_emits_cancelled(self) -> None:
        """Finding 5: CancelledError → end('cancelled'), re-raised."""
        em = self._make_emitter()

        async def _work_that_gets_cancelled():
            async with em.supervised(title="cancel test"):
                await asyncio.sleep(10)  # will be cancelled

        task = asyncio.create_task(_work_that_gets_cancelled())
        # Let the start emit, then cancel
        await asyncio.sleep(0.01)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task

        end_events = [p for p in em._test_posted if p["kind"] == "session.end"]  # type: ignore[attr-defined]
        assert len(end_events) == 1
        assert end_events[0]["status"] == "cancelled"
        await em.close()

    # -----------------------------------------------------------------------
    # Finding 3: Real heartbeat loop test (not tautological)
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_heartbeat_loop_fires_real_heartbeats(self) -> None:
        """_heartbeat_loop actually runs and emits ≥2 heartbeats when
        given a short interval, and stops after supervised exit."""
        posted: list[dict] = []
        handler = _mock_handler_201(posted)
        client = _make_mock_client(handler)
        # heartbeat_s=0.01 → ~10 heartbeats per 100ms
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            heartbeat_s=0.01,
            client=client,
        )

        async with em.supervised(title="heartbeat loop test"):
            # Sleep long enough for ≥2 heartbeats to fire (0.05s >> 2×0.01s)
            await asyncio.sleep(0.05)

        hb_events = [p for p in posted if p["kind"] == "session.heartbeat"]
        assert len(hb_events) >= 2, (
            f"expected ≥2 loop-emitted heartbeats, got {len(hb_events)}"
        )
        # Each heartbeat must have correct shape
        for ev in hb_events:
            assert ev["status"] == "running"
            assert ev["liveness_mode"] == "supervised"
            assert ev["pid"] == os.getpid()
            uuid.UUID(ev["event_id"])  # must be valid UUID

        # After supervised exit, no more heartbeats should fire
        last_count = len(hb_events)
        await asyncio.sleep(0.03)  # wait — would produce more if loop still running
        new_hb = [p for p in posted if p["kind"] == "session.heartbeat"]
        assert len(new_hb) == last_count, (
            "heartbeats must stop after session.end"
        )

        # session.start and session.end should also be present
        kinds = [p["kind"] for p in posted]
        assert "session.start" in kinds
        assert "session.end" in kinds
        await em.close()

    # -----------------------------------------------------------------------
    # Finding 2 (body shape via MockTransport) — start event deep assertion
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_start_body_shape_survives_real_round_trip(self) -> None:
        """The exact emitted body shape survives a real HTTP round-trip
        through MockTransport — no MagicMock hand-stamping."""
        posted: list[dict] = []
        hdrs: list[dict] = []
        handler = _mock_handler_201(posted, hdrs)
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
        )
        await em.start(
            title="shape test",
            project_hint="agent-os",
            external_ref="SC-42",
        )
        await em.close()

        assert len(posted) == 1
        body = posted[0]
        # Exact contract §2 shape assertion
        assert isinstance(body, dict)
        # Required fields
        assert body["schema"] == "agentos.work_event/v1"
        uuid.UUID(body["event_id"])  # valid UUID
        assert body["harness"] == "hermes"
        uuid.UUID(body["session_id"])  # valid UUID
        assert isinstance(body["host"], str) and len(body["host"]) > 0
        assert body["kind"] == "session.start"
        assert isinstance(body["ts"], str) and body["ts"].endswith("Z")
        # Conditional session fields
        assert body["status"] == "running"
        assert body["liveness_mode"] == "supervised"
        assert body["pid"] == os.getpid()
        # Correlation hints
        assert body["title"] == "shape test"
        assert body["project_hint"] == "agent-os"
        assert body["external_ref"] == "SC-42"
        assert "cwd" in body
        # Headers asserted by MockTransport
        assert hdrs[0]["x-agentos-ingest-key"] == "test-key"
        assert hdrs[0]["idempotency-key"] == body["event_id"]

    # -----------------------------------------------------------------------
    # Finding 4: HTTP failure handling
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_start_4xx_raises_post_error(self) -> None:
        """Non-retryable 4xx from start() raises _PostError."""
        handler = _mock_handler_status(422, "invalid payload")
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
        )
        with pytest.raises(_PostError) as exc_info:
            await em.start()
        assert exc_info.value.status_code == 422
        await em.close()

    @pytest.mark.asyncio
    async def test_end_4xx_raises_post_error(self) -> None:
        """session.end rejection (4xx) raises _PostError — caller must handle."""
        handler = _mock_handler_status(403, "bad key")
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
        )
        with pytest.raises(_PostError) as exc_info:
            await em.end("done")
        assert exc_info.value.status_code == 403
        await em.close()

    @pytest.mark.asyncio
    async def test_5xx_retries_then_raises(self) -> None:
        """5xx triggers bounded retry then raises _PostError."""
        call_count = 0
        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal call_count
            call_count += 1
            return httpx.Response(500, text="internal error", request=request)
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=3,
            retry_backoff_s=0.001,  # fast for tests
        )
        with pytest.raises(_PostError):
            await em.start()
        # Should have tried MAX_RETRIES times
        assert call_count == 3
        await em.close()

    @pytest.mark.asyncio
    async def test_connect_error_retries_then_raises(self) -> None:
        """Connection error triggers bounded retry then raises _PostError."""
        em = HermesEmitter(
            endpoint="http://127.0.0.1:1/api/events/work",
            ingest_key="test-key",
            max_retries=2,
            retry_backoff_s=0.001,
        )
        with pytest.raises(_PostError) as exc_info:
            await em.start()
        assert exc_info.value.status_code == 0
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_context_surfaces_start_failure(self) -> None:
        """If start() fails (rejected), supervised() raises."""
        handler = _mock_handler_status(422, "bad event")
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
        )
        with pytest.raises(_PostError):
            async with em.supervised(title="will fail"):
                pass
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_context_surfaces_end_failure(self) -> None:
        """If end() fails on exit, supervised() raises _PostError."""
        # Start succeeds, end fails
        call_count = 0
        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return httpx.Response(201, json={"id": "x", "accepted": True}, request=request)
            return httpx.Response(500, text="server down", request=request)
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
            retry_backoff_s=0.001,
        )
        with pytest.raises(_PostError):
            async with em.supervised(title="end fails"):
                pass
        await em.close()

    @pytest.mark.asyncio
    async def test_heartbeat_swallows_post_error(self) -> None:
        """Heartbeat delivery failure is swallowed (best-effort)."""
        handler = _mock_handler_status(500, "server down")
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
            retry_backoff_s=0.001,
        )
        # Should not raise — heartbeats are best-effort
        status = await em.heartbeat()
        assert status == -1
        await em.close()

    # -----------------------------------------------------------------------
    # Finding 1 (tick 3): __aexit__ must NOT mask original exceptions
    # when the terminal session.end POST fails.
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_supervised_work_raises_plus_end_fails_preserves_original(
        self,
    ) -> None:
        """When work raises RuntimeError AND end POST fails,
        the original RuntimeError propagates (not _PostError)."""
        call_count = 0

        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal call_count
            call_count += 1
            # Start succeeds, everything else fails
            if call_count == 1:
                return httpx.Response(201, json={"id": "x", "accepted": True},
                                     request=request)
            return httpx.Response(422, text="rejected", request=request)

        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
        )
        with pytest.raises(RuntimeError, match="boom"):
            async with em.supervised(title="compound fail"):
                raise RuntimeError("boom")
        await em.close()

    @pytest.mark.asyncio
    async def test_supervised_cancelled_plus_end_fails_preserves_cancelled(
        self,
    ) -> None:
        """When task is cancelled AND end POST fails,
        CancelledError still propagates (not _PostError)."""
        call_count = 0

        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return httpx.Response(201, json={"id": "x", "accepted": True},
                                     request=request)
            return httpx.Response(503, text="unavailable", request=request)

        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            client=client,
            max_retries=1,
            retry_backoff_s=0.001,
        )

        async def _work_that_gets_cancelled():
            async with em.supervised(title="cancel+end-fail"):
                await asyncio.sleep(10)

        task = asyncio.create_task(_work_that_gets_cancelled())
        await asyncio.sleep(0.01)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        await em.close()

    # -----------------------------------------------------------------------
    # Minor 2: external_ref/project_hint carried onto heartbeat/end
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_correlation_hints_carried_to_heartbeat_and_end(self) -> None:
        """project_hint and external_ref from start() appear on heartbeat/end."""
        posted: list[dict] = []
        handler = _mock_handler_201(posted)
        client = _make_mock_client(handler)
        em = HermesEmitter(
            endpoint="http://test/api/events/work",
            ingest_key="test-key",
            heartbeat_s=0.01,
            client=client,
        )

        async with em.supervised(
            title="correlation test",
            project_hint="agent-os",
            external_ref="SC-42",
        ):
            await asyncio.sleep(0.02)  # let ≥1 heartbeat fire

        for ev in posted:
            assert ev.get("project_hint") == "agent-os", (
                f"{ev['kind']} missing project_hint"
            )
            assert ev.get("external_ref") == "SC-42", (
                f"{ev['kind']} missing external_ref"
            )
            assert ev.get("title") == "correlation test", (
                f"{ev['kind']} missing title"
            )
        await em.close()

    # -----------------------------------------------------------------------
    # Finding 2 (tick 3): TransportError cause is retained in _PostError
    # -----------------------------------------------------------------------

    @pytest.mark.asyncio
    async def test_post_error_retains_transport_cause(self) -> None:
        """After exhausting retries on TransportError, _PostError.cause
        carries repr of the original transport error."""
        em = HermesEmitter(
            endpoint="http://127.0.0.1:1/api/events/work",
            ingest_key="test-key",
            max_retries=2,
            retry_backoff_s=0.001,
        )
        with pytest.raises(_PostError) as exc_info:
            await em.start()
        # ConnectError sets last_status=0 (connection refused)
        assert exc_info.value.status_code == 0
        assert exc_info.value.cause is not None
        # Cause should contain information about the transport error
        assert "error" in exc_info.value.cause.lower()
        await em.close()


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


# ---------------------------------------------------------------------------
# Finding 3 (tick 3): __main__.py entry point tests
# ---------------------------------------------------------------------------

class TestEntryPoint:
    """Tests for the __main__.py entry point."""

    def test_dry_run_captures_start_and_end(self) -> None:
        """--dry-run captures session.start and session.end events."""
        from emitters.hermes.__main__ import _run
        import argparse
        import io

        args = argparse.Namespace(
            title="dry-run test",
            dry_run=True,
            heartbeat_s=0,
        )
        captured = io.StringIO()
        with mock.patch("sys.stderr", captured):
            asyncio.run(_run(args))  # type: ignore[arg-type]

        output = captured.getvalue()
        assert "session.start" in output, "dry-run must emit session.start to stderr"
        assert "session.end" in output, "dry-run must emit session.end to stderr"
        assert "done" in output, "dry-run end event must have status=done"

    def test_live_mode_exits_without_ingest_key(self) -> None:
        """Live mode without AGENTOS_INGEST_KEY calls parser.error."""
        import os
        from unittest import mock
        from emitters.hermes.__main__ import main

        env = {"AGENTOS_INGEST_KEY": "", "AGENTOS_ENDPOINT": "http://test"}
        with mock.patch.dict(os.environ, env, clear=True), \
             mock.patch("sys.argv", ["emitters.hermes"]):
            with pytest.raises(SystemExit):
                main()
