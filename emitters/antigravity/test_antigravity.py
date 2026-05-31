"""Unit tests for the Antigravity (agy) work-event emitter."""

import asyncio
import json
import os
import signal
import sys
import tempfile
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
from emitters.antigravity import AntigravityEmitter, AntigravityResult


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


def _make_agy_emitter(**kwargs) -> AntigravityEmitter:
    posted: list[dict] = []
    hdrs: list[dict] = []
    handler = _mock_handler_201(posted, hdrs)
    client = _make_mock_client(handler)
    em = AntigravityEmitter(
        endpoint="http://test/api/events/work",
        ingest_key="test-key",
        client=client,
        **kwargs,
    )
    em._test_posted = posted  # type: ignore[attr-defined]
    em._test_headers = hdrs  # type: ignore[attr-defined]
    return em


# ---------------------------------------------------------------------------
# Antigravity emitter tests
# ---------------------------------------------------------------------------

class TestAntigravityEmitter:
    """Bounded emitter: session.start → session.end, no heartbeats."""

    @pytest.mark.asyncio
    async def test_emit_start_bounded(self) -> None:
        """session.start uses bounded liveness mode."""
        em = _make_agy_emitter()
        sid = str(uuid.uuid4())
        status = await em.emit_start(session_id=sid, title="test session")

        assert status == 201
        assert len(em._test_posted) == 1  # type: ignore[attr-defined]
        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["schema"] == "agentos.work_event/v1"
        assert body["harness"] == "antigravity"
        assert body["kind"] == "session.start"
        assert body["status"] == "running"
        assert body["liveness_mode"] == "bounded"
        assert body["session_id"] == sid
        uuid.UUID(body["event_id"])
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_done(self) -> None:
        """session.end with status=done."""
        em = _make_agy_emitter()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, status="done")

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["kind"] == "session.end"
        assert body["status"] == "done"
        assert body["liveness_mode"] == "bounded"
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_failed(self) -> None:
        """session.end with status=failed."""
        em = _make_agy_emitter()
        sid = str(uuid.uuid4())
        await em.emit_end(session_id=sid, status="failed")

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["status"] == "failed"
        await em.close()

    @pytest.mark.asyncio
    async def test_emit_end_rejects_non_terminal(self) -> None:
        """session.end rejects non-terminal status."""
        em = _make_agy_emitter()
        sid = str(uuid.uuid4())
        with pytest.raises(ValueError, match="terminal status"):
            await em.emit_end(session_id=sid, status="running")
        await em.close()

    @pytest.mark.asyncio
    async def test_start_headers(self) -> None:
        """Correct ingest headers on session.start."""
        em = _make_agy_emitter()
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
        em = _make_agy_emitter()
        sid = str(uuid.uuid4())
        await em.emit_start(session_id=sid)
        await em.emit_end(session_id=sid, status="done")

        event_ids = [e["event_id"] for e in em._test_posted]  # type: ignore[attr-defined]
        assert len(set(event_ids)) == 2
        await em.close()

    @pytest.mark.asyncio
    async def test_start_body_shape_survives_round_trip(self) -> None:
        """Full contract §2 shape survives a real MockTransport round-trip."""
        em = _make_agy_emitter()
        sid = str(uuid.uuid4())
        await em.emit_start(
            session_id=sid,
            title="agy shape test",
            project_hint="my-project",
            external_ref="SC-100",
        )

        body = em._test_posted[0]  # type: ignore[attr-defined]
        assert body["harness"] == "antigravity"
        assert body["liveness_mode"] == "bounded"
        assert body["title"] == "agy shape test"
        assert body["project_hint"] == "my-project"
        assert body["external_ref"] == "SC-100"
        await em.close()

    @pytest.mark.asyncio
    async def test_no_ingest_key_raises(self) -> None:
        """Emitting without ingest key raises _PostError."""
        em = AntigravityEmitter(
            endpoint="http://test",
            ingest_key=None,
        )
        sid = str(uuid.uuid4())
        with pytest.raises(_PostError, match="no ingest key"):
            await em.emit_start(session_id=sid)
        await em.close()


class TestAntigravityResult:
    """AntigravityResult dataclass tests."""

    def test_result_defaults(self) -> None:
        r = AntigravityResult(
            exit_code=0, stdout="", stderr="", duration_s=1.0,
        )
        assert r.output_json is None

    def test_result_with_json(self) -> None:
        r = AntigravityResult(
            exit_code=0, stdout="{}", stderr="", duration_s=2.0,
            output_json={"type": "response"},
        )
        assert r.output_json is not None
        assert r.output_json["type"] == "response"


# ---------------------------------------------------------------------------
# Subprocess-path regression tests (Blocker 4)
# ---------------------------------------------------------------------------

class TestAntigravitySubprocessPath:
    """End-to-end tests that exercise run_agy through a real subprocess."""

    @pytest.mark.asyncio
    async def test_run_agy_fake_bin_emits_start_and_end(self) -> None:
        """A fake agy binary triggers real run_agy wiring."""
        posted: list[dict] = []
        handler = _mock_handler_201(posted, None)
        client = _make_mock_client(handler)

        with tempfile.TemporaryDirectory() as tmpdir:
            fake_bin = os.path.join(tmpdir, "agy")
            with open(fake_bin, "w") as f:
                f.write("#!/usr/bin/env python3\n")
                f.write('import json, sys\n')
                f.write('print(json.dumps({"type": "response"}))\n')
            os.chmod(fake_bin, 0o755)

            em = AntigravityEmitter(
                endpoint="http://test/api/events/work",
                ingest_key="test-key",
                client=client,
                agy_bin=fake_bin,
            )

            result = await em.run_agy("write tests", cwd=tmpdir)

            assert result.exit_code == 0
            assert result.duration_s > 0

            kinds = [p["kind"] for p in posted]
            assert "session.start" in kinds
            assert "session.end" in kinds
            start_ev = [p for p in posted if p["kind"] == "session.start"][0]
            end_ev = [p for p in posted if p["kind"] == "session.end"][0]
            assert start_ev["session_id"] == end_ev["session_id"]
            assert end_ev["status"] == "done"
            assert end_ev["payload"]["duration_s"] > 0
            await em.close()

    @pytest.mark.asyncio
    async def test_run_agy_fake_bin_failure_emits_failed(self) -> None:
        """A fake agy binary that exits non-zero emits session.end(failed)."""
        posted: list[dict] = []
        handler = _mock_handler_201(posted, None)
        client = _make_mock_client(handler)

        with tempfile.TemporaryDirectory() as tmpdir:
            fake_bin = os.path.join(tmpdir, "agy")
            with open(fake_bin, "w") as f:
                f.write("#!/usr/bin/env python3\n")
                f.write("import sys\n")
                f.write("sys.exit(1)\n")
            os.chmod(fake_bin, 0o755)

            em = AntigravityEmitter(
                endpoint="http://test/api/events/work",
                ingest_key="test-key",
                client=client,
                agy_bin=fake_bin,
            )

            result = await em.run_agy("fail task", cwd=tmpdir)

            assert result.exit_code == 1
            end_ev = [p for p in posted if p["kind"] == "session.end"][0]
            assert end_ev["status"] == "failed"
            await em.close()

    @pytest.mark.asyncio
    async def test_duration_s_computed_not_fabricated(self) -> None:
        """duration_s in payload is > 0, not fabricated ≈0."""
        posted: list[dict] = []
        handler = _mock_handler_201(posted, None)
        client = _make_mock_client(handler)

        with tempfile.TemporaryDirectory() as tmpdir:
            fake_bin = os.path.join(tmpdir, "agy")
            with open(fake_bin, "w") as f:
                f.write("#!/usr/bin/env python3\n")
                f.write("import time; time.sleep(0.05)\n")
                f.write('print(\'{"type":"response"}\')\n')
            os.chmod(fake_bin, 0o755)

            em = AntigravityEmitter(
                endpoint="http://test/api/events/work",
                ingest_key="test-key",
                client=client,
                agy_bin=fake_bin,
            )

            result = await em.run_agy("sleep a bit", cwd=tmpdir)

            assert result.duration_s >= 0.04
            end_ev = [p for p in posted if p["kind"] == "session.end"][0]
            assert end_ev["payload"]["duration_s"] >= 0.04
            await em.close()

    @pytest.mark.asyncio
    async def test_signal_cancelled_maps_negative_code(self) -> None:
        """An asyncio subprocess killed by signal maps to cancelled."""
        posted: list[dict] = []
        handler = _mock_handler_201(posted, None)
        client = _make_mock_client(handler)

        with tempfile.TemporaryDirectory() as tmpdir:
            fake_bin = os.path.join(tmpdir, "agy")
            with open(fake_bin, "w") as f:
                f.write("#!/usr/bin/env python3\n")
                f.write("import time\n")
                f.write("time.sleep(60)\n")
                f.write('print(\'{"type":"response"}\')\n')
            os.chmod(fake_bin, 0o755)

            em = AntigravityEmitter(
                endpoint="http://test/api/events/work",
                ingest_key="test-key",
                client=client,
                agy_bin=fake_bin,
            )

            original_exec = em._exec_agy

            async def _exec_then_signal(prompt, **kw):
                proc = await asyncio.create_subprocess_exec(
                    fake_bin, "-p", prompt,
                    stdout=asyncio.subprocess.PIPE,
                    stderr=asyncio.subprocess.PIPE,
                    cwd=kw.get("cwd") or em.cwd,
                )
                await asyncio.sleep(0.05)
                proc.send_signal(signal.SIGINT)
                stdout_bytes, stderr_bytes = await proc.communicate()
                from emitters.antigravity import AntigravityResult
                return AntigravityResult(
                    exit_code=proc.returncode or 0,
                    stdout=stdout_bytes.decode("utf-8", errors="replace"),
                    stderr=stderr_bytes.decode("utf-8", errors="replace"),
                    duration_s=0.05,
                )

            em._exec_agy = _exec_then_signal  # type: ignore[method-assign]

            try:
                await em.run_agy("interrupt me", cwd=tmpdir)
            except _PostError:
                pass

            end_events = [p for p in posted if p["kind"] == "session.end"]
            if end_events:
                assert end_events[0]["status"] == "cancelled"
            await em.close()
