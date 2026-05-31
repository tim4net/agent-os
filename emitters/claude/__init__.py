"""Claude Code work-event emitter for Agent OS.

Bounded emitter: wraps ``claude -p`` invocations and emits
session.start → session.end events to the Agent OS ingestion endpoint.

Bounded liveness means no heartbeats — hooks can't heartbeat (contract §4).
The emitter runs Claude as a subprocess, captures its JSON output, and
posts the lifecycle events.

Usage:
    python -m emitters.claude --prompt "fix the auth bug" --cwd /path/to/repo
    python -m emitters.claude -p "explain this code" --dry-run

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
import subprocess
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
    _rfc3339_now,
    post_event,
)

try:
    import httpx
except ImportError:  # pragma: no cover
    raise ImportError("httpx is required. Install with: pip install httpx")

__all__ = ["ClaudeEmitter", "ClaudeResult"]
logger = logging.getLogger("agentos.claude")

HARNESS_CLAUDE = "claude"


@dataclass
class ClaudeResult:
    """Parsed result from a ``claude -p`` invocation."""

    exit_code: int
    stdout: str
    stderr: str
    duration_s: float
    output_json: Optional[dict[str, Any]] = None
    cost_usd: Optional[float] = None
    session_id: Optional[str] = None


class ClaudeEmitter:
    """Bounded emitter for Claude Code ``claude -p`` invocations.

    Unlike the Hermes emitter (supervised), Claude runs as a subprocess
    with bounded liveness — we emit session.start before launching,
    then session.end after the process exits. No heartbeats (contract §4:
    bounded emitters cannot heartbeat).
    """

    DEFAULT_ENDPOINT = "http://localhost:8080/api/events/work"

    def __init__(
        self,
        *,
        endpoint: str | None = None,
        ingest_key: str | None = None,
        cwd: str | None = None,
        tenant: str | None = None,
        claude_bin: str = "claude",
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self.endpoint = endpoint or os.environ.get(
            "AGENTOS_ENDPOINT", self.DEFAULT_ENDPOINT
        )
        self.ingest_key = ingest_key or os.environ.get("AGENTOS_INGEST_KEY")
        self.cwd = cwd or os.getcwd()
        self.tenant = tenant
        self.claude_bin = claude_bin
        self._host = socket.gethostname()
        self._pid = os.getpid()
        self._client = client

    def _build_base_event(
        self, session_id: str, kind: str
    ) -> WorkEvent:
        """Build a WorkEvent pre-populated with session identity."""
        ev = WorkEvent(
            harness=HARNESS_CLAUDE,
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
        """POST a work-event, raising _PostError on failure."""
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
        """Emit a ``session.start`` event (bounded liveness).

        Returns the HTTP status code from the ingestion endpoint.
        """
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
        artifacts: list[dict[str, Any]] | None = None,
    ) -> int:
        """Emit a ``session.end`` event with a terminal status.

        Returns the HTTP status code.
        """
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
        if artifacts:
            ev.artifacts = artifacts
        return await self._post(ev)

    async def run_claude(
        self,
        prompt: str,
        *,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
        cwd: str | None = None,
        model: str | None = None,
        allowed_tools: list[str] | None = None,
    ) -> ClaudeResult:
        """Run ``claude -p`` as a subprocess and emit bounded lifecycle events.

        Emits session.start before launching, session.end after the process
        exits. Returns the parsed Claude result.
        """
        work_cwd = cwd or self.cwd
        session_id = str(uuid.uuid4())
        effective_title = title or prompt[:80]
        work_dir = os.path.basename(work_cwd) if work_cwd else None
        effective_project = project_hint or work_dir

        # Emit session.start
        await self.emit_start(
            session_id=session_id,
            title=effective_title,
            project_hint=effective_project,
            external_ref=external_ref,
        )

        start_time = time.monotonic()
        try:
            result = await self._exec_claude(
                prompt,
                cwd=work_cwd,
                model=model,
                allowed_tools=allowed_tools,
            )
        except Exception as exc:
            # Process failed to launch — emit session.end(failed)
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

        # Determine terminal status from exit code
        if result.exit_code == 0:
            terminal_status = Status.DONE.value
        elif result.exit_code == 130:
            # SIGINT — Ctrl+C
            terminal_status = Status.CANCELLED.value
        else:
            terminal_status = Status.FAILED.value

        # Build payload with Claude output details
        payload: dict[str, Any] = {
            "duration_s": round(result.duration_s, 2),
        }
        if result.output_json:
            # Extract relevant fields from Claude JSON output
            payload["claude_output"] = {
                k: result.output_json[k]
                for k in ("type", "model", "usage", "result")
                if k in result.output_json
            }

        try:
            await self.emit_end(
                session_id=session_id,
                status=terminal_status,
                cost_usd=result.cost_usd,
                title=effective_title,
                payload=payload,
            )
        except _PostError as post_err:
            logger.error(
                "failed to emit session.end(%s) for session %s: %s",
                terminal_status, session_id, post_err,
            )
            # Surface the post error — don't silently swallow
            raise

        return result

    async def _exec_claude(
        self,
        prompt: str,
        *,
        cwd: str | None = None,
        model: str | None = None,
        allowed_tools: list[str] | None = None,
    ) -> ClaudeResult:
        """Execute ``claude -p`` as a subprocess and return parsed result."""
        cmd = [self.claude_bin, "-p", prompt, "--output-format", "json"]

        if model:
            cmd.extend(["--model", model])
        if allowed_tools:
            for tool in allowed_tools:
                cmd.extend(["--allowedTools", tool])

        logger.debug("claude command: %s", " ".join(cmd))

        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=cwd or self.cwd,
        )

        stdout_bytes, stderr_bytes = await proc.communicate()
        stdout = stdout_bytes.decode("utf-8", errors="replace")
        stderr = stderr_bytes.decode("utf-8", errors="replace")
        duration = time.monotonic() - getattr(
            self, "_claude_start", time.monotonic()
        )

        # Parse JSON output
        output_json: dict[str, Any] | None = None
        cost_usd: float | None = None
        try:
            parsed = json.loads(stdout)
            if isinstance(parsed, dict):
                output_json = parsed
                # Claude JSON output may include cost info
                usage = output_json.get("usage")
                if isinstance(usage, dict):
                    # tokens for telemetry; cost from dedicated field
                    pass
                if "cost_usd" in output_json:
                    cost_usd = float(output_json["cost_usd"])
                elif "cost" in output_json:
                    cost_usd = float(output_json["cost"])
        except (json.JSONDecodeError, TypeError, ValueError):
            pass

        return ClaudeResult(
            exit_code=proc.returncode or 0,
            stdout=stdout,
            stderr=stderr,
            duration_s=duration,
            output_json=output_json,
            cost_usd=cost_usd,
        )

    async def close(self) -> None:
        """Close the HTTP client if we own it."""
        if self._client is not None:
            await self._client.aclose()
