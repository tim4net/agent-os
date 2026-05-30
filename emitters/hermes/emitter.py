"""Hermes work-event emitter for Agent OS.

Supervised emitter: wraps a Hermes delegate/session lifecycle and emits
work-events to POST /api/events/work per the frozen contract v1.1.

Lifecycle:
  1. session.start   — emitted when the supervised session begins
  2. session.heartbeat — emitted every ~60 s while the session runs
  3. session.end     — emitted on completion, cancellation, or failure

Usage as a library:
    emitter = HermesEmitter(
        endpoint="http://localhost:8080/api/events/work",
        ingest_key=os.environ["AGENTOS_INGEST_KEY"],
    )
    await emitter.start(title="fix auth bug")
    try:
        await do_work()
        await emitter.end("done")
    except Exception:
        await emitter.end("failed")

Usage as a supervisor (heartbeats run in the background):
    async with emitter.supervised(title="fix auth bug"):
        await do_work()          # heartbeats sent every 60s
    # session.end emitted automatically with "done"

Entry point (run as Hermes hook):
    python -m emitters.hermes

Environment variables:
  AGENTOS_ENDPOINT    — ingestion URL  (default http://localhost:8080/api/events/work)
  AGENTOS_INGEST_KEY  — per-tenant ingest key (REQUIRED)
  AGENTOS_HEARTBEAT_S — heartbeat interval in seconds (default 60)
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import socket
import sys
import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any, Optional

try:
    import httpx
except ImportError:  # pragma: no cover — runtime dep
    raise ImportError(
        "httpx is required. Install with: pip install httpx"
    )

__all__ = [
    "Harness",
    "Kind",
    "Status",
    "LivenessMode",
    "HermesEmitter",
    "WorkEvent",
]

logger = logging.getLogger("agentos.hermes")

# ---------------------------------------------------------------------------
# Frozen enums (contract §4)
# ---------------------------------------------------------------------------

class Harness(str, Enum):
    HERMES = "hermes"


class Kind(str, Enum):
    SESSION_START = "session.start"
    SESSION_HEARTBEAT = "session.heartbeat"
    SESSION_END = "session.end"
    ARTIFACT_CREATED = "artifact.created"
    SERVER_STARTED = "server.started"
    SERVER_STOPPED = "server.stopped"
    NOTE = "note"


class Status(str, Enum):
    RUNNING = "running"
    DONE = "done"
    FAILED = "failed"
    CANCELLED = "cancelled"
    UNKNOWN = "unknown"


class LivenessMode(str, Enum):
    SUPERVISED = "supervised"
    BOUNDED = "bounded"


# ---------------------------------------------------------------------------
# Work-event builder
# ---------------------------------------------------------------------------

@dataclass
class WorkEvent:
    """Builds a single work-event JSON body per contract §2."""

    schema_version: str = "agentos.work_event/v1"
    event_id: str = field(default_factory=lambda: str(uuid.uuid4()))
    harness: str = Harness.HERMES.value
    session_id: str = field(default_factory=lambda: str(uuid.uuid4()))
    host: str = field(default_factory=socket.gethostname)
    kind: str = Kind.SESSION_START.value
    ts: str = field(default_factory=lambda: _rfc3339_now())
    status: Optional[str] = None
    liveness_mode: Optional[str] = None
    pid: Optional[int] = None
    # Correlation hints (all optional)
    project_hint: Optional[str] = None
    external_ref: Optional[str] = None
    branch: Optional[str] = None
    sha: Optional[str] = None
    cwd: Optional[str] = None
    tenant: Optional[str] = None
    title: Optional[str] = None
    cost_usd: Optional[float] = None
    payload: dict[str, Any] = field(default_factory=dict)
    artifacts: list[dict[str, Any]] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        """Serialize to the contract §2 JSON shape."""
        d: dict[str, Any] = {
            "schema": self.schema_version,
            "event_id": self.event_id,
            "harness": self.harness,
            "session_id": self.session_id,
            "host": self.host,
            "kind": self.kind,
            "ts": self.ts,
        }
        # Conditional fields — only include when not None
        if self.status is not None:
            d["status"] = self.status
        if self.liveness_mode is not None:
            d["liveness_mode"] = self.liveness_mode
        if self.pid is not None:
            d["pid"] = self.pid
        # Optional correlation hints
        for opt_key in (
            "project_hint", "external_ref", "branch", "sha", "cwd",
            "tenant", "title", "cost_usd",
        ):
            val = getattr(self, opt_key)
            if val is not None:
                d[opt_key] = val
        if self.artifacts:
            d["artifacts"] = self.artifacts
        if self.payload:
            d["payload"] = self.payload
        return d


def _rfc3339_now() -> str:
    """Return current time as RFC3339 UTC string."""
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _detect_branch(cwd: str | None = None) -> str | None:
    """Try to detect the current git branch from CWD. Returns None if unknown."""
    try:
        import subprocess
        result = subprocess.run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True, text=True, timeout=5,
            cwd=cwd or os.getcwd(),
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except Exception:
        pass
    return None


def _detect_sha(cwd: str | None = None) -> str | None:
    """Try to detect the current git SHA (short). Returns None if unknown."""
    try:
        import subprocess
        result = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True, text=True, timeout=5,
            cwd=cwd or os.getcwd(),
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except Exception:
        pass
    return None


# ---------------------------------------------------------------------------
# Emitter
# ---------------------------------------------------------------------------

class _PostError(Exception):
    """Raised when a POST to the ingestion endpoint fails after retries."""
    def __init__(self, status_code: int, body_summary: str) -> None:
        self.status_code = status_code
        self.body_summary = body_summary
        super().__init__(
            f"ingestion endpoint returned {status_code}: {body_summary}"
        )


class HermesEmitter:
    """Thin supervised emitter for Hermes delegate/session lifecycle.

    Posts work-events to the Agent OS ingestion endpoint.
    Heartbeats are sent in the background when used as a supervisor.
    """

    DEFAULT_ENDPOINT = "http://localhost:8080/api/events/work"
    DEFAULT_HEARTBEAT_S = 60
    MAX_RETRIES = 3
    RETRY_BACKOFF_S = 1.0

    def __init__(
        self,
        *,
        endpoint: str | None = None,
        ingest_key: str | None = None,
        heartbeat_s: float | int | None = None,
        session_id: str | None = None,
        cwd: str | None = None,
        tenant: str | None = None,
        client: httpx.AsyncClient | None = None,
        max_retries: int | None = None,
        retry_backoff_s: float | None = None,
    ) -> None:
        self.endpoint = endpoint or os.environ.get(
            "AGENTOS_ENDPOINT", self.DEFAULT_ENDPOINT
        )
        self.ingest_key = ingest_key or os.environ.get("AGENTOS_INGEST_KEY")
        if not self.ingest_key:
            raise ValueError(
                "INGEST_KEY is required. Set AGENTOS_INGEST_KEY env or pass ingest_key."
            )
        self.heartbeat_s = float(heartbeat_s) if heartbeat_s is not None else float(
            os.environ.get("AGENTOS_HEARTBEAT_S", self.DEFAULT_HEARTBEAT_S)
        )
        self.max_retries = max_retries if max_retries is not None else self.MAX_RETRIES
        self.retry_backoff_s = retry_backoff_s if retry_backoff_s is not None else self.RETRY_BACKOFF_S
        self.session_id = session_id or str(uuid.uuid4())
        self.cwd = cwd or os.getcwd()
        self.tenant = tenant
        self._pid = os.getpid()
        self._host = socket.gethostname()
        self._branch: str | None = None
        self._sha: str | None = None
        self._heartbeat_task: asyncio.Task | None = None
        self._started = False
        self._ended = False
        # Allow injection for testing (e.g. MockTransport clients)
        self._client = client or httpx.AsyncClient(timeout=10)

    # --- internal helpers ---

    def _base_event(self, kind: str) -> WorkEvent:
        """Build a WorkEvent pre-populated with session identity."""
        ev = WorkEvent(
            harness=Harness.HERMES.value,
            session_id=self.session_id,
            host=self._host,
            kind=kind,
            cwd=self.cwd,
            tenant=self.tenant,
        )
        return ev

    async def _post(self, event: WorkEvent) -> int:
        """POST a work-event to the ingestion endpoint.

        Checks status code; logs warnings on non-2xx; retries on 5xx
        and connection errors with bounded backoff.  Raises ``_PostError``
        on persistent non-2xx so callers can surface delivery failures.

        Returns the HTTP status code on success (2xx).
        """
        body = event.to_dict()
        body_json = json.dumps(body)
        headers = {
            "Content-Type": "application/json",
            "X-AgentOS-Ingest-Key": self.ingest_key,
            "Idempotency-Key": event.event_id,
        }

        last_status: int = -1
        for attempt in range(self.max_retries):
            try:
                resp = await self._client.post(
                    self.endpoint,
                    content=body_json,
                    headers=headers,
                )
                last_status = resp.status_code
                if 200 <= resp.status_code < 300:
                    return resp.status_code
                # Non-2xx — log and decide whether to retry
                reason = resp.text[:200] if resp.text else "(empty body)"
                logger.warning(
                    "agentos.hermes POST %s → %d (attempt %d/%d): %s",
                    event.kind, resp.status_code,
                    attempt + 1, self.max_retries, reason,
                )
                if resp.status_code >= 500:
                    # Server error — retry with backoff
                    await asyncio.sleep(self.retry_backoff_s * (attempt + 1))
                    continue
                else:
                    # 4xx client error — not retryable, surface immediately
                    raise _PostError(resp.status_code, reason)
            except httpx.ConnectError:
                logger.warning(
                    "agentos.hermes POST %s: connection error (attempt %d/%d)",
                    event.kind, attempt + 1, self.max_retries,
                )
                last_status = 0
                await asyncio.sleep(self.retry_backoff_s * (attempt + 1))
                continue
            except _PostError:
                raise  # re-raise the 4xx we just raised
            except Exception as exc:
                logger.warning(
                    "agentos.hermes POST %s: unexpected error (attempt %d/%d): %s",
                    event.kind, attempt + 1, self.max_retries, exc,
                )
                last_status = -1
                await asyncio.sleep(self.retry_backoff_s * (attempt + 1))
                continue

        # Exhausted retries — raise so callers know delivery failed
        raise _PostError(last_status, "exhausted retries")

    def _detect_git(self) -> None:
        """Detect branch and SHA once (idempotent)."""
        if self._branch is None:
            self._branch = _detect_branch(self.cwd)
        if self._sha is None:
            self._sha = _detect_sha(self.cwd)

    # --- public API ---

    async def start(
        self,
        *,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
        liveness_mode: str = LivenessMode.SUPERVISED.value,
    ) -> int:
        """Emit a ``session.start`` event.

        Returns the HTTP status code from the ingestion endpoint.
        Raises ``_PostError`` if the endpoint rejects or is unreachable
        after retries.
        """
        self._detect_git()
        ev = self._base_event(Kind.SESSION_START.value)
        ev.status = Status.RUNNING.value
        ev.liveness_mode = liveness_mode
        ev.pid = self._pid
        ev.title = title
        ev.project_hint = project_hint
        ev.external_ref = external_ref
        ev.branch = self._branch
        ev.sha = self._sha
        self._started = True
        return await self._post(ev)

    async def heartbeat(self) -> int:
        """Emit a single ``session.heartbeat`` event.

        Returns the HTTP status code.  Swallows delivery errors (heartbeats
        are best-effort liveness probes).
        """
        ev = self._base_event(Kind.SESSION_HEARTBEAT.value)
        ev.status = Status.RUNNING.value
        ev.liveness_mode = LivenessMode.SUPERVISED.value
        ev.pid = self._pid
        ev.branch = self._branch
        ev.sha = self._sha
        try:
            return await self._post(ev)
        except _PostError:
            # Heartbeats are best-effort — log and swallow
            logger.warning(
                "agentos.hermes heartbeat delivery failed for session %s",
                self.session_id,
            )
            return -1

    async def end(
        self,
        status: str = Status.DONE.value,
        *,
        cost_usd: float | None = None,
        title: str | None = None,
    ) -> int:
        """Emit a ``session.end`` event with a terminal status.

        Returns the HTTP status code.
        Raises ``_PostError`` if the endpoint rejects or is unreachable
        after retries — the caller MUST handle this.
        """
        if status not in (Status.DONE.value, Status.FAILED.value, Status.CANCELLED.value):
            raise ValueError(
                f"session.end requires terminal status, got {status!r}"
            )
        ev = self._base_event(Kind.SESSION_END.value)
        ev.status = status
        ev.liveness_mode = LivenessMode.SUPERVISED.value
        ev.pid = self._pid
        ev.branch = self._branch
        ev.sha = self._sha
        if cost_usd is not None:
            ev.cost_usd = cost_usd
        if title is not None:
            ev.title = title
        self._ended = True
        await self._stop_heartbeats()
        return await self._post(ev)

    # --- heartbeat loop (supervised liveness) ---

    async def _heartbeat_loop(self) -> None:
        """Background task: emit heartbeats every ``heartbeat_s`` seconds."""
        while not self._ended:
            await asyncio.sleep(self.heartbeat_s)
            if self._ended:
                break
            await self.heartbeat()

    async def _start_heartbeats(self) -> None:
        """Start the heartbeat background task."""
        if self._heartbeat_task is None or self._heartbeat_task.done():
            self._heartbeat_task = asyncio.create_task(
                self._heartbeat_loop(), name="hermes-heartbeat"
            )

    async def _stop_heartbeats(self) -> None:
        """Cancel the heartbeat background task if running."""
        if self._heartbeat_task is not None and not self._heartbeat_task.done():
            self._heartbeat_task.cancel()
            try:
                await self._heartbeat_task
            except asyncio.CancelledError:
                pass
            self._heartbeat_task = None

    # --- context manager (supervised session) ---

    def supervised(
        self,
        *,
        title: str | None = None,
        project_hint: str | None = None,
        external_ref: str | None = None,
        cost_usd: float | None = None,
    ) -> _SupervisedSession:
        """Return an async context manager that wraps a supervised session.

        Emits session.start on entry, starts heartbeats, emits session.end
        on exit (done, cancelled, or failed based on exit conditions).

        Example::

            async with emitter.supervised(title="fix auth bug"):
                await do_work()
        """
        return _SupervisedSession(
            emitter=self,
            title=title,
            project_hint=project_hint,
            external_ref=external_ref,
            cost_usd=cost_usd,
        )

    async def close(self) -> None:
        """Close the HTTP client."""
        await self._stop_heartbeats()
        await self._client.aclose()


@dataclass
class _SupervisedSession:
    """Async context manager for a supervised Hermes session."""

    emitter: HermesEmitter
    title: str | None = None
    project_hint: str | None = None
    external_ref: str | None = None
    cost_usd: float | None = None

    async def __aenter__(self) -> _SupervisedSession:
        await self.emitter.start(
            title=self.title,
            project_hint=self.project_hint,
            external_ref=self.external_ref,
        )
        await self.emitter._start_heartbeats()
        return self

    async def __aexit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> bool:
        if exc_type is not None and issubclass(exc_type, asyncio.CancelledError):
            # CancelledError → emit end("cancelled") and re-raise
            await self.emitter.end(Status.CANCELLED.value, cost_usd=self.cost_usd)
            return False  # re-raise CancelledError
        if exc_type is not None:
            await self.emitter.end(Status.FAILED.value, cost_usd=self.cost_usd)
        else:
            await self.emitter.end(Status.DONE.value, cost_usd=self.cost_usd)
        # Always re-raise exceptions — the emitter recorded the failure but
        # the caller should see it.
        return False
