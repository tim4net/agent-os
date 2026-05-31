"""Supervisor wrapper for Agent OS work-event emission.

A thin launcher-wrapper that gives any agent **supervised** liveness:
  - Wraps a subprocess (e.g. claude, agy, any command)
  - Emits session.start with supervised liveness
  - Sends 60s heartbeats while the subprocess PID is alive
  - Emits session.end with correct terminal status on exit
  - Maps exit code → done/failed/cancelled

Usage:
    python -m emitters.supervisor -- claude -p "fix the bug"
    python -m emitters.supervisor -- agy -p "write tests" --cwd /project

Environment variables:
  AGENTOS_ENDPOINT    — ingestion URL  (default http://localhost:8080/api/events/work)
  AGENTOS_INGEST_KEY  — per-tenant ingest key (REQUIRED unless --dry-run)
  AGENTOS_HEARTBEAT_S — heartbeat interval in seconds (default 60)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import signal
import socket
import sys
import time
import uuid
from typing import Any, Optional

from emitters._shared import (
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _detect_branch,
    _detect_sha,
    _rfc3339_now,
    post_event,
)

try:
    import httpx
except ImportError:  # pragma: no cover
    raise ImportError("httpx is required. Install with: pip install httpx")

__all__ = ["SupervisedEmitter"]
logger = logging.getLogger("agentos.supervisor")

# Exit code → status mapping
_CANCELLED_CODES = {130}  # SIGINT


class SupervisedEmitter:
    """Supervised emitter: wraps any subprocess with supervised liveness.

    Lifecycle:
      1. Emit session.start (supervised)
      2. Launch subprocess
      3. Heartbeat loop (every ~60s while PID alive)
      4. On subprocess exit: emit session.end with terminal status
    """

    DEFAULT_ENDPOINT = "http://localhost:8080/api/events/work"
    DEFAULT_HEARTBEAT_S = 60

    def __init__(
        self,
        *,
        endpoint: str | None = None,
        ingest_key: str | None = None,
        harness: str = "generic",
        cwd: str | None = None,
        tenant: str | None = None,
        heartbeat_s: float | int | None = None,
        client: httpx.AsyncClient | None = None,
        max_retries: int = 3,
        retry_backoff_s: float = 1.0,
    ) -> None:
        self.endpoint = endpoint or os.environ.get(
            "AGENTOS_ENDPOINT", self.DEFAULT_ENDPOINT
        )
        self.ingest_key = ingest_key or os.environ.get("AGENTOS_INGEST_KEY")
        self.harness = harness
        self.cwd = cwd or os.getcwd()
        self.tenant = tenant
        self.heartbeat_s = float(heartbeat_s) if heartbeat_s is not None else float(
            os.environ.get("AGENTOS_HEARTBEAT_S", self.DEFAULT_HEARTBEAT_S)
        )
        self.max_retries = max_retries
        self.retry_backoff_s = retry_backoff_s
        self._host = socket.gethostname()
        self._client = client
        self._ended = False
        self._heartbeat_task: asyncio.Task | None = None

    def _build_event(
        self, session_id: str, kind: str, pid: int
    ) -> WorkEvent:
        ev = WorkEvent(
            harness=self.harness,
            session_id=session_id,
            host=self._host,
            kind=kind,
            cwd=self.cwd,
            tenant=self.tenant,
            branch=_detect_branch(self.cwd),
            sha=_detect_sha(self.cwd),
            liveness_mode=LivenessMode.SUPERVISED.value,
            pid=pid,
        )
        return ev

    async def _post(self, event: WorkEvent) -> int:
        """POST a work-event, raising on failure."""
        if self.ingest_key is None:
            raise RuntimeError("AGENTOS_INGEST_KEY is required")
        return await post_event(
            event,
            endpoint=self.endpoint,
            ingest_key=self.ingest_key,
            client=self._client,
            max_retries=self.max_retries,
            retry_backoff_s=self.retry_backoff_s,
        )

    async def emit_start(
        self,
        *,
        session_id: str,
        pid: int,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
    ) -> int:
        ev = self._build_event(session_id, Kind.SESSION_START.value, pid)
        ev.status = Status.RUNNING.value
        if title:
            ev.title = title
        if project_hint:
            ev.project_hint = project_hint
        if external_ref:
            ev.external_ref = external_ref
        return await self._post(ev)

    async def emit_heartbeat(self, session_id: str, pid: int) -> int:
        """Emit a single heartbeat (best-effort)."""
        ev = self._build_event(session_id, Kind.SESSION_HEARTBEAT.value, pid)
        ev.status = Status.RUNNING.value
        try:
            return await self._post(ev)
        except Exception:
            logger.warning(
                "heartbeat delivery failed for session %s", session_id
            )
            return -1

    async def emit_end(
        self,
        *,
        session_id: str,
        pid: int,
        status: str,
        cost_usd: float | None = None,
        payload: dict[str, Any] | None = None,
    ) -> int:
        if status not in (
            Status.DONE.value, Status.FAILED.value, Status.CANCELLED.value
        ):
            raise ValueError(
                f"session.end requires terminal status, got {status!r}"
            )
        ev = self._build_event(session_id, Kind.SESSION_END.value, pid)
        ev.status = status
        if cost_usd is not None:
            ev.cost_usd = cost_usd
        if payload:
            ev.payload = payload
        self._ended = True
        await self._stop_heartbeats()
        return await self._post(ev)

    # --- heartbeat loop ---

    async def _heartbeat_loop(
        self, session_id: str, pid: int
    ) -> None:
        """Background: emit heartbeats while PID alive."""
        while not self._ended:
            await asyncio.sleep(self.heartbeat_s)
            if self._ended:
                break
            await self.emit_heartbeat(session_id, pid)

    async def _start_heartbeats(
        self, session_id: str, pid: int
    ) -> None:
        if self._heartbeat_task is None or self._heartbeat_task.done():
            self._heartbeat_task = asyncio.create_task(
                self._heartbeat_loop(session_id, pid),
                name="supervisor-heartbeat",
            )

    async def _stop_heartbeats(self) -> None:
        if (
            self._heartbeat_task is not None
            and not self._heartbeat_task.done()
        ):
            self._heartbeat_task.cancel()
            try:
                await self._heartbeat_task
            except asyncio.CancelledError:
                pass
            self._heartbeat_task = None

    async def close(self) -> None:
        await self._stop_heartbeats()
        if self._client is not None:
            await self._client.aclose()

    # --- run a command under supervision ---

    async def run_supervised(
        self,
        cmd: list[str],
        *,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
        cwd: str | None = None,
    ) -> int:
        """Run a command under supervised liveness.

        Returns the command's exit code.
        """
        work_cwd = cwd or self.cwd
        session_id = str(uuid.uuid4())
        effective_title = title or " ".join(cmd[:3])

        # Launch the subprocess
        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=work_cwd,
        )
        child_pid = proc.pid

        # Emit session.start with the child PID
        await self.emit_start(
            session_id=session_id,
            pid=child_pid,
            title=effective_title,
            project_hint=project_hint or os.path.basename(work_cwd),
            external_ref=external_ref,
        )

        # Start heartbeat loop
        await self._start_heartbeats(session_id, child_pid)

        # Wait for process to exit
        stdout_bytes, stderr_bytes = await proc.communicate()
        exit_code = proc.returncode or 0

        # Determine terminal status
        if exit_code in _CANCELLED_CODES:
            terminal_status = Status.CANCELLED.value
        elif exit_code == 0:
            terminal_status = Status.DONE.value
        else:
            terminal_status = Status.FAILED.value

        # Emit session.end
        try:
            await self.emit_end(
                session_id=session_id,
                pid=child_pid,
                status=terminal_status,
                payload={
                    "exit_code": exit_code,
                    "duration_s": 0,  # not tracked here
                },
            )
        except Exception as post_err:
            logger.error(
                "failed to emit session.end for session %s: %s",
                session_id, post_err,
            )

        return exit_code
