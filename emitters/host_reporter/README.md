"""Host process reporter for Agent OS bounded-session liveness.

Runs per tailnet host and POSTs process-liveness reports to the Agent OS
server at /api/host/liveness. The server uses these reports to determine
bounded-session liveness per contract §4: a bounded process without a live
report is presumed crashed (stale).

Reports are keyed by (host, pid)/cwd so that a remote bounded Claude crash
is detected within a poll cycle — not the 6h backstop.

Usage:
    python -m emitters.host_reporter

Environment variables:
    AGENTOS_LIVENESS_URL   — POST target (default http://localhost:8080/api/host/liveness)
    AGENTOS_HOST           — hostname override (default: socket.gethostname())
    AGENTOS_TENANT         — tenant slug (default: personal)
    AGENTOS_POLL_S         — poll interval in seconds (default: 10)
    AGENTOS_PID_DIRS       — colon-separated list of dirs to scan for PIDs (optional)
"""
