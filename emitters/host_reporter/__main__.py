"""Entry point for the host-reporter (WP-N).

Usage:
    python -m emitters.host_reporter

Environment variables:
    AGENTOS_LIVENESS_URL   — POST target (default http://localhost:8080/api/host/liveness)
    AGENTOS_HOST           — hostname override
    AGENTOS_TENANT         — tenant slug (default: personal)
    AGENTOS_POLL_S         — poll interval in seconds (default: 10)
"""

from __future__ import annotations

import signal
import sys

from .reporter import HostReporter


def main() -> None:
    reporter = HostReporter()

    def _signal_handler(sig, frame):  # type: ignore[no-untyped-def]
        reporter.stop()

    signal.signal(signal.SIGTERM, _signal_handler)
    signal.signal(signal.SIGINT, _signal_handler)

    try:
        reporter.run_forever()
    finally:
        reporter.close()


if __name__ == "__main__":
    main()
