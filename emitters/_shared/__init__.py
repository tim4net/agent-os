"""Shared work-event contract types for all Agent OS emitters.

Frozen per docs/work-event-contract.md v1.1. All emitters import from
here — do NOT duplicate enum/WorkEvent definitions per emitter package.
"""

from emitters._shared.types import (
    Kind,
    LivenessMode,
    Status,
    WorkEvent,
    _PostError,
    _rfc3339_now,
    _detect_branch,
    _detect_sha,
    post_event,
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
