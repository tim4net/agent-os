"""Tests for the host-reporter emitter (WP-N)."""

from __future__ import annotations

import json
import os
from unittest.mock import patch
from typing import Any

import httpx
import pytest

from emitters.host_reporter.reporter import HostReporter, LivenessReport


# ---------------------------------------------------------------------------
# LivenessReport unit tests
# ---------------------------------------------------------------------------

class TestLivenessReport:
    """Unit tests for the LivenessReport dataclass."""

    def test_to_dict_minimal(self) -> None:
        report = LivenessReport(host="zbook", pid=12345, alive=True)
        d = report.to_dict()
        assert d["host"] == "zbook"
        assert d["pid"] == 12345
        assert d["alive"] is True
        # Optional fields should NOT be present when empty
        assert "session_id" not in d
        assert "harness" not in d
        assert "cwd" not in d
        assert "tenant" not in d

    def test_to_dict_full(self) -> None:
        report = LivenessReport(
            host="zbook",
            pid=12345,
            alive=False,
            session_id="sess-1",
            harness="claude",
            cwd="/work/agent-os",
            tenant="personal",
        )
        d = report.to_dict()
        assert d["host"] == "zbook"
        assert d["pid"] == 12345
        assert d["alive"] is False
        assert d["session_id"] == "sess-1"
        assert d["harness"] == "claude"
        assert d["cwd"] == "/work/agent-os"
        assert d["tenant"] == "personal"

    def test_to_dict_partial(self) -> None:
        report = LivenessReport(host="zbook", pid=12345, alive=True, cwd="/work")
        d = report.to_dict()
        assert d["cwd"] == "/work"
        assert "session_id" not in d
        assert "harness" not in d

    def test_to_dict_empty_strings_excluded(self) -> None:
        """Empty string fields should not be included in the dict."""
        report = LivenessReport(
            host="zbook",
            pid=12345,
            alive=True,
            session_id="",
            harness="",
            cwd="",
            tenant="",
        )
        d = report.to_dict()
        assert "session_id" not in d
        assert "harness" not in d
        assert "cwd" not in d
        assert "tenant" not in d

    def test_to_dict_json_serializable(self) -> None:
        """The output of to_dict must be JSON-serializable."""
        report = LivenessReport(host="zbook", pid=12345, alive=True, tenant="personal")
        d = report.to_dict()
        json_str = json.dumps(d)
        parsed = json.loads(json_str)
        assert parsed["host"] == "zbook"
        assert parsed["pid"] == 12345
        assert parsed["alive"] is True


# ---------------------------------------------------------------------------
# HostReporter integration tests (with mock transport)
# ---------------------------------------------------------------------------

class TestHostReporter:
    """Tests for the HostReporter class."""

    def _make_reporter(
        self,
        transport: Any,
        host: str = "testhost",
        tenant: str = "personal",
        poll_s: float = 0.1,
    ) -> HostReporter:
        """Create a reporter with a mock transport for testing."""
        client = httpx.Client(transport=transport)
        return HostReporter(
            endpoint="http://localhost:9999/api/host/liveness",
            host=host,
            tenant=tenant,
            poll_s=poll_s,
            client=client,
        )

    def test_report_alive_success(self) -> None:
        """POST alive=true report returns 200."""
        transport = httpx.MockTransport(
            lambda request: httpx.Response(200, json={"id": 1, "alive": True})
        )
        reporter = self._make_reporter(transport)
        report = LivenessReport(host="testhost", pid=12345, alive=True)
        status = reporter.report(report)
        assert status == 200

    def test_report_dead_success(self) -> None:
        """POST alive=false report returns 200."""
        transport = httpx.MockTransport(
            lambda request: httpx.Response(200, json={"id": 1, "alive": False})
        )
        reporter = self._make_reporter(transport)
        report = LivenessReport(host="testhost", pid=12345, alive=False)
        status = reporter.report(report)
        assert status == 200

    def test_report_server_error(self) -> None:
        """POST to a 500 server returns 500."""
        transport = httpx.MockTransport(
            lambda request: httpx.Response(500, text="internal error")
        )
        reporter = self._make_reporter(transport)
        report = LivenessReport(host="testhost", pid=12345, alive=True)
        status = reporter.report(report)
        assert status == 500

    def test_report_connect_error(self) -> None:
        """POST to unreachable server returns -1."""
        transport = httpx.MockTransport(
            lambda request: (_ for _ in ()).throw(httpx.ConnectError("connection refused"))
        )
        reporter = self._make_reporter(transport)
        report = LivenessReport(host="testhost", pid=12345, alive=True)
        status = reporter.report(report)
        assert status == -1

    def test_report_sends_correct_json(self) -> None:
        """Verify the POST body matches the expected JSON shape."""
        received_body: dict[str, Any] = {}

        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal received_body
            received_body = json.loads(request.content)
            return httpx.Response(200, json={"id": 1})

        transport = httpx.MockTransport(handler)
        reporter = self._make_reporter(transport)

        report = LivenessReport(
            host="myhost",
            pid=99999,
            alive=True,
            session_id="sess-abc",
            harness="claude",
            cwd="/work/repo",
            tenant="dayjob",
        )
        reporter.report(report)

        assert received_body["host"] == "myhost"
        assert received_body["pid"] == 99999
        assert received_body["alive"] is True
        assert received_body["session_id"] == "sess-abc"
        assert received_body["harness"] == "claude"
        assert received_body["cwd"] == "/work/repo"
        assert received_body["tenant"] == "dayjob"

    def test_discover_processes_no_proc(self) -> None:
        """discover_processes returns empty list on non-Linux or permission denied."""
        reporter = self._make_reporter(
            httpx.MockTransport(lambda r: httpx.Response(200)),
            poll_s=0,
        )
        # On any system, if /proc doesn't exist or isn't readable,
        # discover_processes should return an empty list, not raise.
        procs = reporter.discover_processes()
        assert isinstance(procs, list)

    def test_stop_and_close(self) -> None:
        """stop() and close() should not raise."""
        transport = httpx.MockTransport(
            lambda request: httpx.Response(200)
        )
        reporter = self._make_reporter(transport)
        reporter.stop()
        reporter.close()
        assert reporter._running is False

    def test_report_with_minimal_fields(self) -> None:
        """A report with only required fields sends a minimal JSON body."""
        received_body: dict[str, Any] = {}

        def handler(request: httpx.Request) -> httpx.Response:
            nonlocal received_body
            received_body = json.loads(request.content)
            return httpx.Response(200)

        transport = httpx.MockTransport(handler)
        reporter = self._make_reporter(transport)
        report = LivenessReport(host="h", pid=1, alive=True)
        reporter.report(report)

        # Should only have required fields
        assert set(received_body.keys()) == {"host", "pid", "alive"}

    def test_gone_pid_crash_detection(self) -> None:
        """When a previously-seen PID disappears, poll_once sends alive=false.

        Simulates two poll cycles:
          1. First poll discovers PID 123 → POST alive=true
          2. Second poll discovers nothing → POST alive=false for PID 123
        """
        posts: list[dict[str, Any]] = []

        def handler(request: httpx.Request) -> httpx.Response:
            body = json.loads(request.content)
            posts.append(body)
            return httpx.Response(200, json={"id": 1})

        transport = httpx.MockTransport(handler)
        reporter = self._make_reporter(transport)

        # Monkeypatch discover_processes: first call returns PID 123,
        # second call returns empty (process gone).
        call_count = 0
        original_discover = reporter.discover_processes

        def fake_discover() -> list[tuple[int, str]]:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return [(123, "/work")]
            return []

        reporter.discover_processes = fake_discover  # type: ignore[assignment]

        # First poll: PID 123 discovered → alive=true
        reporter.poll_once()
        # Second poll: PID 123 gone → alive=false
        reporter.poll_once()

        # We expect at least 2 POSTs: one alive=true, one alive=false
        assert len(posts) >= 2, f"expected >= 2 POSTs, got {len(posts)}"

        first = posts[0]
        assert first["pid"] == 123
        assert first["alive"] is True

        # Find the gone-PID post (should be the second POST)
        gone_posts = [p for p in posts if p["pid"] == 123 and p["alive"] is False]
        assert len(gone_posts) == 1, (
            f"expected exactly 1 alive=false POST for pid 123, got {len(gone_posts)}"
        )
        assert gone_posts[0]["pid"] == 123

    def test_gone_pid_retries_on_post_failure(self) -> None:
        """When alive=false POST fails, the pid is NOT removed and is retried.

        Simulates three poll cycles:
          1. First poll discovers PID 123 → POST alive=true (success)
          2. Second poll: PID 123 gone → POST alive=false (500 error) → pid stays pending
          3. Third poll: PID 123 still gone → POST alive=false (success) → pid removed
        """
        posts: list[dict[str, Any]] = []
        attempt = 0

        def handler(request: httpx.Request) -> httpx.Response:
            body = json.loads(request.content)
            posts.append(body)
            nonlocal attempt
            attempt += 1
            # First alive=true POST succeeds, first alive=false POST fails (500),
            # second alive=false POST succeeds
            if body.get("alive") is False and len([p for p in posts if not p.get("alive", True)]) == 1:
                return httpx.Response(500, text="server error")
            return httpx.Response(200, json={"id": 1})

        transport = httpx.MockTransport(handler)
        reporter = self._make_reporter(transport)

        call_count = 0

        def fake_discover() -> list[tuple[int, str]]:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return [(123, "/work")]
            return []

        reporter.discover_processes = fake_discover  # type: ignore[assignment]

        # Poll 1: PID 123 discovered → alive=true
        reporter.poll_once()
        assert 123 in reporter._seen_pids, "PID 123 should be cached after alive=true POST"

        # Poll 2: PID 123 gone → alive=false (server returns 500)
        reporter.poll_once()
        assert 123 in reporter._seen_pids, "PID 123 should remain cached after failed alive=false POST"

        # Poll 3: PID 123 still gone → alive=false (server returns 200)
        reporter.poll_once()
        assert 123 not in reporter._seen_pids, "PID 123 should be removed after successful alive=false POST"

        # Should have exactly 1 alive=true and 2 alive=false POSTs
        alive_true = [p for p in posts if p.get("alive") is True]
        alive_false = [p for p in posts if p.get("alive") is False]
        assert len(alive_true) == 1, f"expected 1 alive=true POST, got {len(alive_true)}"
        assert len(alive_false) == 2, f"expected 2 alive=false POSTs (retry), got {len(alive_false)}"

    def test_proc_scan_failure_no_false_death(self) -> None:
        """When /proc scan fails, poll_once sends ZERO alive=false POSTs.

        A transient scan failure must not fabricate death for healthy
        processes. Simulates three poll cycles:
          1. First poll discovers PID 123 → POST alive=true (success)
          2. Second poll: discover_processes returns None (scan error)
             → no POSTs at all, PID 123 stays cached
          3. Third poll: discover_processes returns empty (genuine no-procs)
             → POST alive=false for PID 123
        """
        posts: list[dict[str, Any]] = []

        def handler(request: httpx.Request) -> httpx.Response:
            body = json.loads(request.content)
            posts.append(body)
            return httpx.Response(200, json={"id": 1})

        transport = httpx.MockTransport(handler)
        reporter = self._make_reporter(transport)

        call_count = 0

        def fake_discover() -> list[tuple[int, str]] | None:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return [(123, "/work")]
            if call_count == 2:
                # Simulate /proc scan failure — returns None
                return None
            # Third call: genuinely no processes
            return []

        reporter.discover_processes = fake_discover  # type: ignore[assignment]

        # Poll 1: PID 123 discovered → alive=true
        reporter.poll_once()
        assert 123 in reporter._seen_pids
        alive_after_1 = [p for p in posts if p.get("alive") is True]
        assert len(alive_after_1) == 1

        # Poll 2: scan failure → NO POSTs, PID stays cached
        reporter.poll_once()
        assert 123 in reporter._seen_pids, (
            "PID 123 must remain cached when scan fails"
        )
        # No new POSTs during the failed scan cycle
        assert len(posts) == 1, (
            f"expected 0 POSTs during scan failure, but got {len(posts) - 1} new"
        )

        # Poll 3: genuine empty → alive=false for PID 123
        reporter.poll_once()
        assert 123 not in reporter._seen_pids, (
            "PID 123 should be removed after genuine gone detection"
        )
        alive_false = [p for p in posts if p.get("alive") is False]
        assert len(alive_false) == 1, (
            f"expected 1 alive=false POST, got {len(alive_false)}"
        )

    def test_discover_processes_real_os_error_returns_none(self) -> None:
        """When os.listdir on /proc raises OSError, discover_processes returns None.

        This exercises the REAL except path in discover_processes (not a
        monkeypatched return value). Reverting the except block from
        `return None` to `return []` causes this test to FAIL.
        """
        reporter = self._make_reporter(
            httpx.MockTransport(lambda r: httpx.Response(200)),
            poll_s=0,
        )
        # Monkeypatch os.listdir to raise OSError, simulating an unreadable /proc.
        def fake_listdir(path: str):
            raise OSError("[Errno 13] Permission denied: '/proc'")

        with patch.object(os, "listdir", fake_listdir):
            result = reporter.discover_processes()
        # The real except path must return None (not []).
        assert result is None, (
            f"expected None on os.listdir failure, got {result!r}"
        )

    def test_os_error_during_scan_preserves_cached_pids(self) -> None:
        """When /proc scan fails mid-run, poll_once sends ZERO alive=false POSTs
        and all cached PIDs remain.

        This drives the full poll_once path with a real os.listdir failure
        (not just a None return from discover_processes). The pid should
        stay cached and no alive=false should be POSTed.

        Mutation guard: reverting `except Exception: ... return None` to
        `return []` causes alive=false to be POSTed (test goes RED).
        """
        posts: list[dict[str, Any]] = []

        def handler(request: httpx.Request) -> httpx.Response:
            body = json.loads(request.content)
            posts.append(body)
            return httpx.Response(200, json={"id": 1})

        transport = httpx.MockTransport(handler)
        reporter = self._make_reporter(transport)

        # Save the original discover_processes before monkeypatching.
        original_discover = reporter.discover_processes

        call_count = 0

        def fake_discover() -> list[tuple[int, str]] | None:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                # First call: discover PID 123 normally
                return [(123, "/work")]
            if call_count == 2:
                # Second call: trigger REAL os.listdir failure on the
                # ORIGINAL discover_processes method (not this monkeypatch).
                def raise_on_listdir(path: str):
                    raise OSError("[Errno 13] Permission denied: '/proc'")

                with patch.object(os, "listdir", raise_on_listdir):
                    return original_discover()
            return []

        reporter.discover_processes = fake_discover  # type: ignore[assignment]

        # Poll 1: PID 123 discovered → alive=true
        reporter.poll_once()
        assert 123 in reporter._seen_pids, "PID 123 should be cached"
        alive_true = [p for p in posts if p.get("alive") is True]
        assert len(alive_true) == 1, f"expected 1 alive=true POST, got {len(alive_true)}"

        pre_fail_count = len(posts)

        # Poll 2: real os.listdir failure → discover_processes returns None
        # → poll_once skips gone-pid sweep → ZERO new POSTs
        reporter.poll_once()
        assert 123 in reporter._seen_pids, (
            "PID 123 must remain cached after /proc scan failure"
        )
        # No new POSTs during the failed scan cycle
        assert len(posts) == pre_fail_count, (
            f"expected 0 POSTs during scan failure, got {len(posts) - pre_fail_count} new"
        )
