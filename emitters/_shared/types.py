"""Shared work-event types per contract v1.1.

Every emitter (hermes, claude, antigravity, supervisor) imports from here.
This is the single source of truth for:
  - Frozen enums (contract §4)
  - WorkEvent builder (contract §2)
  - HTTP posting logic with retry (contract §1)
  - Git detection helpers
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import socket
import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional

try:
    import httpx
except ImportError:  # pragma: no cover — runtime dep
    raise ImportError(
        "httpx is required. Install with: pip install httpx"
    )

__all__ = [
    "Kind",
    "Status",
    "LivenessMode",
    "WorkEvent",
    "_PostError",
    "_rfc3339_now",
    "_detect_branch",
    "_detect_sha",
    "post_event",
]

logger = logging.getLogger("agentos.shared")

# ---------------------------------------------------------------------------
# Frozen enums (contract §4)
# ---------------------------------------------------------------------------

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
# Work-event builder (contract §2)
# ---------------------------------------------------------------------------

@dataclass
class WorkEvent:
    """Builds a single work-event JSON body per contract §2.

    The ``harness`` field is NOT hardcoded — each emitter sets it.
    """

    schema_version: str = "agentos.work_event/v1"
    event_id: str = field(default_factory=lambda: str(uuid.uuid4()))
    harness: str = "generic"  # set by each emitter
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
            branch = result.stdout.strip()
            return None if branch == "HEAD" else branch
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
# HTTP posting (contract §1)
# ---------------------------------------------------------------------------

class _PostError(Exception):
    """Raised when a POST to the ingestion endpoint fails after retries."""
    def __init__(self, status_code: int, body_summary: str,
                 cause: str | None = None) -> None:
        self.status_code = status_code
        self.body_summary = body_summary
        self.cause = cause
        detail = f"{body_summary}"
        if cause:
            detail += f" (cause: {cause})"
        super().__init__(
            f"ingestion endpoint returned {status_code}: {detail}"
        )


async def post_event(
    event: WorkEvent,
    *,
    endpoint: str,
    ingest_key: str,
    client: httpx.AsyncClient | None = None,
    max_retries: int = 3,
    retry_backoff_s: float = 1.0,
) -> int:
    """POST a work-event to the ingestion endpoint.

    Checks status code; retries on 5xx and connection errors with bounded
    backoff.  Raises ``_PostError`` on persistent non-2xx.

    Returns the HTTP status code on success (2xx).
    """
    if client is None:
        client = httpx.AsyncClient(timeout=10)
        _owns_client = True
    else:
        _owns_client = False

    body_json = json.dumps(event.to_dict())
    headers = {
        "Content-Type": "application/json",
        "X-AgentOS-Ingest-Key": ingest_key,
        "Idempotency-Key": event.event_id,
    }

    last_status: int = -1
    last_cause: str | None = None
    try:
        for attempt in range(max_retries):
            try:
                resp = await client.post(
                    endpoint,
                    content=body_json,
                    headers=headers,
                )
                last_status = resp.status_code
                if 200 <= resp.status_code < 300:
                    return resp.status_code
                reason = resp.text[:200] if resp.text else "(empty body)"
                logger.warning(
                    "POST %s → %d (attempt %d/%d): %s",
                    event.kind, resp.status_code,
                    attempt + 1, max_retries, reason,
                )
                if resp.status_code >= 500:
                    await asyncio.sleep(retry_backoff_s * (attempt + 1))
                    continue
                else:
                    raise _PostError(resp.status_code, reason)
            except _PostError:
                raise
            except (httpx.ConnectError, httpx.TransportError) as exc:
                last_cause = repr(exc)
                logger.warning(
                    "POST %s: transport error (attempt %d/%d): %s",
                    event.kind, attempt + 1, max_retries, last_cause,
                )
                last_status = 0
                await asyncio.sleep(retry_backoff_s * (attempt + 1))
                continue
    finally:
        if _owns_client:
            try:
                await client.aclose()
            except Exception:
                pass  # prevent masking in-flight exceptions

    raise _PostError(last_status, "exhausted retries", cause=last_cause)
