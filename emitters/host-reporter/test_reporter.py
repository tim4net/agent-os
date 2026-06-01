"""Tests for the host-reporter emitter (WP-N)."""

from __future__ import annotations

import json
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
