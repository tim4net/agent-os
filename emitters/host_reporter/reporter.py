"""Host process liveness reporter for Agent OS (WP-N).

Per-tailnet-host agent that POSTs process-liveness keyed by (host, pid)/cwd
for bounded-session crash detection (contract §4).

The reporter polls /proc on the local host to discover running agent processes
and reports their liveness to the Agent OS server. When a previously-seen
process disappears (kill, crash, exit), the reporter sends alive=false so the
server can consume this signal for session-state derivation.
"""

from __future__ import annotations

import json
import logging
import os
import socket
import time
from dataclasses import dataclass
from typing import Any

try:
    import httpx
except ImportError:  # pragma: no cover
    raise ImportError("httpx is required. Install with: pip install httpx")

__all__ = [
    "LivenessReport",
    "HostReporter",
]

logger = logging.getLogger("agentos.host_reporter")


@dataclass
class LivenessReport:
    """A single process-liveness report to POST to /api/host/liveness."""

    host: str
    pid: int
    alive: bool
    session_id: str = ""
    harness: str = ""
    cwd: str = ""
    tenant: str = ""

    def to_dict(self) -> dict[str, Any]:
        """Serialize to JSON-POSTable dict."""
        d: dict[str, Any] = {
            "host": self.host,
            "pid": self.pid,
            "alive": self.alive,
        }
        if self.session_id:
            d["session_id"] = self.session_id
        if self.harness:
            d["harness"] = self.harness
        if self.cwd:
            d["cwd"] = self.cwd
        if self.tenant:
            d["tenant"] = self.tenant
        return d


class HostReporter:
    """Reports process liveness to Agent OS.

    Discovers agent processes (by scanning /proc or a configured PID dir)
    and periodically POSTs their status. When a process disappears, sends
    alive=false so the server can consume this signal for session-state derivation.

    Args:
        endpoint: POST target URL for liveness reports.
        host: Hostname to report (defaults to socket.gethostname()).
        tenant: Tenant slug for scoping.
        poll_s: Seconds between poll cycles.
        client: Optional httpx client for testing.
    """

    DEFAULT_ENDPOINT = "http://localhost:8080/api/host/liveness"
    DEFAULT_POLL_S = 10

    def __init__(
        self,
        *,
        endpoint: str | None = None,
        host: str | None = None,
        tenant: str | None = None,
        poll_s: float | None = None,
        client: httpx.Client | None = None,
    ) -> None:
        self.endpoint = endpoint or os.environ.get(
            "AGENTOS_LIVENESS_URL", self.DEFAULT_ENDPOINT
        )
        self.host = host or os.environ.get("AGENTOS_HOST", socket.gethostname())
        self.tenant = tenant or os.environ.get("AGENTOS_TENANT", "personal")
        self.poll_s = float(poll_s) if poll_s is not None else float(
            os.environ.get("AGENTOS_POLL_S", self.DEFAULT_POLL_S)
        )
        self._client = client or httpx.Client(timeout=10)
        self._seen_pids: dict[int, LivenessReport] = {}
        self._running = False

    def report(self, report: LivenessReport) -> int:
        """POST a liveness report to the server.

        Returns the HTTP status code on success, -1 on failure.
        """
        try:
            resp = self._client.post(
                self.endpoint,
                content=json.dumps(report.to_dict()),
                headers={"Content-Type": "application/json"},
            )
            if 200 <= resp.status_code < 300:
                return resp.status_code
            logger.warning(
                "host_reporter POST → %d: %s", resp.status_code, resp.text[:200]
            )
            return resp.status_code
        except (httpx.ConnectError, httpx.TransportError) as exc:
            logger.warning("host_reporter POST failed: %s", exc)
            return -1

    def discover_processes(self) -> list[tuple[int, str]] | None:
        """Discover running agent processes by scanning /proc.

        Returns a list of (pid, cwd) tuples for processes whose cmdline
        contains known agent harness keywords.

        Returns None when the /proc scan itself fails (e.g. unreadable
        directory), so the caller can distinguish "no processes found"
        from "we couldn't tell."
        """
        procs: list[tuple[int, str]] = []
        # Agent harness keywords to look for in process command lines
        harness_patterns = ["claude", "hermes", "antigravity", "codex", "agy"]

        try:
            proc_dir = "/proc"
            for entry in os.listdir(proc_dir):
                if not entry.isdigit():
                    continue
                pid = int(entry)
                try:
                    cmdline_path = os.path.join(proc_dir, entry, "cmdline")
                    cwd_path = os.path.join(proc_dir, entry, "cwd")

                    with open(cmdline_path, "rb") as f:
                        cmdline = f.read().decode("utf-8", errors="replace").lower()

                    # Check if this is an agent process
                    is_agent = any(p in cmdline for p in harness_patterns)
                    if not is_agent:
                        continue

                    # Read cwd (may fail due to permissions)
                    try:
                        real_cwd = os.path.realpath(cwd_path)
                    except OSError:
                        real_cwd = ""

                    procs.append((pid, real_cwd))
                except (PermissionError, FileNotFoundError, ProcessLookupError):
                    continue
        except Exception:
            logger.warning(
                "host_reporter: failed to scan /proc — skipping this cycle"
            )
            return None

        return procs

    def poll_once(self) -> None:
        """Run a single poll cycle: discover processes, report changes."""
        discovered = self.discover_processes()

        # If the scan failed entirely, skip the gone-pid sweep — we cannot
        # distinguish "all processes exited" from "we couldn't read /proc."
        if discovered is None:
            logger.warning(
                "host_reporter: /proc scan failed, skipping gone-pid sweep"
            )
            return

        current_pids: set[int] = set()

        for pid, cwd in discovered:
            current_pids.add(pid)
            if pid not in self._seen_pids:
                # New process — report alive; only cache on success
                report = LivenessReport(
                    host=self.host,
                    pid=pid,
                    alive=True,
                    cwd=cwd,
                    tenant=self.tenant,
                )
                status = self.report(report)
                if 200 <= status < 300:
                    self._seen_pids[pid] = report
            # Existing process — already reported alive, skip unless cwd changed
            else:
                existing = self._seen_pids[pid]
                if existing.cwd != cwd:
                    new_report = LivenessReport(
                        host=existing.host,
                        pid=existing.pid,
                        alive=existing.alive,
                        session_id=existing.session_id,
                        harness=existing.harness,
                        cwd=cwd,
                        tenant=existing.tenant,
                    )
                    status = self.report(new_report)
                    if 200 <= status < 300:
                        self._seen_pids[pid] = new_report

        # Check for processes that disappeared
        gone_pids = set(self._seen_pids.keys()) - current_pids
        for pid in gone_pids:
            report = self._seen_pids[pid]
            report.alive = False
            status = self.report(report)
            if 200 <= status < 300:
                self._seen_pids.pop(pid)

    def run_forever(self) -> None:
        """Run the reporter loop until interrupted."""
        self._running = True
        logger.info(
            "host_reporter starting on %s (poll=%ss, tenant=%s, endpoint=%s)",
            self.host, self.poll_s, self.tenant, self.endpoint,
        )
        while self._running:
            try:
                self.poll_once()
            except Exception as exc:
                logger.error("host_reporter poll error: %s", exc)
            time.sleep(self.poll_s)

    def stop(self) -> None:
        """Signal the reporter to stop."""
        self._running = False

    def close(self) -> None:
        """Close the HTTP client."""
        try:
            self._client.close()
        except Exception:
            pass
