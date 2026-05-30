"""Hermes work-event emitter for Agent OS.

Thin out-of-process emitter. Posts session lifecycle events
(session.start, session.heartbeat, session.end) to the Agent OS
ingestion endpoint. Supervised liveness: heartbeat loop outlives
the Hermes work.
"""

__version__ = "0.1.0"

from emitters.hermes.emitter import (
    Harness,
    HermesEmitter,
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
)

__all__ = [
    "Harness",
    "Kind",
    "Status",
    "LivenessMode",
    "HermesEmitter",
    "WorkEvent",
    "_PostError",
]
