"""Antigravity (agy) work-event emitter for Agent OS.

Bounded emitter: wraps ``agy -p`` invocations (non-interactive/print mode)
and emits session.start → session.end events.

Fallback: if ``agy`` doesn't provide structured JSON output, the emitter
parses stdout/stderr and tail's the session store.

Usage:
    python -m emitters.antigravity --prompt "write tests" --cwd /path/to/repo
    python -m emitters.antigravity -p "explain this" --dry-run

Environment variables:
  AGENTOS_ENDPOINT    — ingestion URL  (default http://localhost:8080/api/events/work)
  AGENTOS_INGEST_KEY  — per-tenant ingest key (REQUIRED unless --dry-run)
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
import socket
import sys
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Optional

from emitters._shared import (
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
    _detect_branch,
    _detect_sha,
    post_event,
)

try:
    import httpx
except ImportError:  # pragma: no cover
    raise ImportError("httpx is required. Install with: pip install httpx")

__all__ = ["AntigravityEmitter", "AntigravityResult"]
logger = logging.getLogger("agentos.antigravity")

HARNESS_ANTIGRAVITY = "antigravity"
AGY_SETTINGS_DIR = os.path.expanduser("~/.gemini/antigravity-cli")


@dataclass
class AntigravityResult:
    """Parsed result from an ``agy -p`` invocation."""

    exit_code: int
    stdout: str
    stderr: str
    duration_s: float
    output_json: Optional[dict[str, Any]] = None


class AntigravityEmitter:
    """Bounded emitter for Antigravity ``agy -p`` invocations.

    Bounded liveness — no heartbeats (contract §4: bounded emitters
    cannot heartbeat). Emits session.start before launching, session.end
    after the process exits.
    """

    DEFAULT_ENDPOINT = "http://localhost:8080/api/events/work"

    def __init__(
        self,
        *,
        endpoint: str | None = None,
        ingest_key: str | None = None,
        cwd: str | None = None,
        tenant: str | None = None,
        agy_bin: str = "agy",
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self.endpoint = endpoint or os.environ.get(
            "AGENTOS_ENDPOINT", self.DEFAULT_ENDPOINT
        )
        self.ingest_key = ingest_key or os.environ.get("AGENTOS_INGEST_KEY")
        self.cwd = cwd or os.getcwd()
        self.tenant = tenant
        self.agy_bin = agy_bin
        self._host = socket.gethostname()
        self._pid = os.getpid()
        self._client = client

    def _build_base_event(
        self, session_id: str, kind: str
    ) -> WorkEvent:
        ev = WorkEvent(
            harness=HARNESS_ANTIGRAVITY,
            session_id=session_id,
            host=self._host,
            kind=kind,
            cwd=self.cwd,
            tenant=self.tenant,
            branch=_detect_branch(self.cwd),
            sha=_detect_sha(self.cwd),
        )
        return ev

    async def _post(self, event: WorkEvent) -> int:
        if self.ingest_key is None:
            raise _PostError(
                0,
                "no ingest key configured",
                cause="AGENTOS_INGEST_KEY not set",
            )
        return await post_event(
            event,
            endpoint=self.endpoint,
            ingest_key=self.ingest_key,
            client=self._client,
        )

    async def emit_start(
        self,
        *,
        session_id: str,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
    ) -> int:
        ev = self._build_base_event(session_id, Kind.SESSION_START.value)
        ev.status = Status.RUNNING.value
        ev.liveness_mode = LivenessMode.BOUNDED.value
        ev.pid = self._pid
        if title:
            ev.title = title
        if project_hint:
            ev.project_hint = project_hint
        if external_ref:
            ev.external_ref = external_ref
        return await self._post(ev)

    async def emit_end(
        self,
        *,
        session_id: str,
        status: str,
        cost_usd: float | None = None,
        title: str | None = None,
        payload: dict[str, Any] | None = None,
    ) -> int:
        if status not in (
            Status.DONE.value, Status.FAILED.value, Status.CANCELLED.value
        ):
            raise ValueError(
                f"session.end requires terminal status, got {status!r}"
            )
        ev = self._build_base_event(session_id, Kind.SESSION_END.value)
        ev.status = status
        ev.liveness_mode = LivenessMode.BOUNDED.value
        if cost_usd is not None:
            ev.cost_usd = cost_usd
        if title:
            ev.title = title
        if payload:
            ev.payload = payload
        return await self._post(ev)

    async def run_agy(
        self,
        prompt: str,
        *,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
        cwd: str | None = None,
    ) -> AntigravityResult:
        """Run ``agy -p`` as a subprocess and emit bounded lifecycle events.

        Emits session.start before launching, session.end after the process
        exits. Returns the parsed result.
        """
        work_cwd = cwd or self.cwd
        session_id = str(uuid.uuid4())
        effective_title = title or prompt[:80]
        work_dir = os.path.basename(work_cwd) if work_cwd else None
        effective_project = project_hint or work_dir

        await self.emit_start(
            session_id=session_id,
            title=effective_title,
            project_hint=effective_project,
            external_ref=external_ref,
        )

        start_time = time.monotonic()
        try:
            result = await self._exec_agy(prompt, cwd=work_cwd)
        except Exception as exc:
            duration = time.monotonic() - start_time
            try:
                await self.emit_end(
                    session_id=session_id,
                    status=Status.FAILED.value,
                    payload={"error": str(exc), "duration_s": round(duration, 2)},
                )
            except _PostError as post_err:
                logger.error(
                    "failed to emit session.end(failed) for session %s: %s",
                    session_id, post_err,
                )
            raise

        if result.exit_code == 0:
            terminal_status = Status.DONE.value
        elif result.exit_code in {-signal.SIGINT, -signal.SIGTERM, 130, 143}:
            terminal_status = Status.CANCELLED.value
        else:
            terminal_status = Status.FAILED.value

        payload: dict[str, Any] = {
            "duration_s": round(result.duration_s, 2),
        }
        if result.output_json:
            payload["agy_output"] = {
                k: result.output_json[k]
                for k in result.output_json
                if k in ("type", "model", "usage")
            }

        try:
            await self.emit_end(
                session_id=session_id,
                status=terminal_status,
                cost_usd=None,  # agy doesn't reliably report cost
                title=effective_title,
                payload=payload,
            )
        except _PostError as post_err:
            logger.error(
                "failed to emit session.end(%s) for session %s: %s",
                terminal_status, session_id, post_err,
            )
            raise

        return result

    async def _exec_agy(
        self,
        prompt: str,
        *,
        cwd: str | None = None,
    ) -> AntigravityResult:
        """Execute ``agy -p`` as a subprocess and return parsed result."""
        cmd = [self.agy_bin, "-p", prompt]

        logger.debug("agy command: %s", " ".join(cmd))

        start = time.monotonic()

        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=cwd or self.cwd,
        )

        stdout_bytes, stderr_bytes = await proc.communicate()
        stdout = stdout_bytes.decode("utf-8", errors="replace")
        stderr = stderr_bytes.decode("utf-8", errors="replace")
        duration = time.monotonic() - start

        output_json: dict[str, Any] | None = None
        try:
            parsed = json.loads(stdout)
            if isinstance(parsed, dict):
                output_json = parsed
        except (json.JSONDecodeError, TypeError):
            pass

        return AntigravityResult(
            exit_code=proc.returncode or 0,
            stdout=stdout,
            stderr=stderr,
            duration_s=duration,
            output_json=output_json,
        )

    async def close(self) -> None:
        if self._client is not None:
            await self._client.aclose()
