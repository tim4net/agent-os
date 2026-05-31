"""Runnable entry point for the supervisor wrapper.

Usage:
    python -m emitters.supervisor -- claude -p "fix the bug"
    python -m emitters.supervisor --harness antigravity -- agy -p "write tests"
    python -m emitters.supervisor --title "my task" -- ls -la
    python -m emitters.supervisor --dry-run -- echo hello

Wraps any command with supervised liveness: session.start → heartbeats → session.end.

Required env:
    AGENTOS_INGEST_KEY  — per-tenant ingest key (unless --dry-run)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys

from emitters.supervisor import SupervisedEmitter


async def _run(args: argparse.Namespace) -> None:
    ingest_key = os.environ.get("AGENTOS_INGEST_KEY")
    endpoint = os.environ.get("AGENTOS_ENDPOINT")
    cmd = args.command

    # argparse.REMAINDER keeps the literal "--"; strip it
    if cmd and cmd[0] == "--":
        cmd = cmd[1:]

    if not cmd:
        print("ERROR: must specify a command to supervise", file=sys.stderr)
        sys.exit(1)

    if args.dry_run:
        import httpx

        posted: list[dict] = []

        def _handler(request: httpx.Request) -> httpx.Response:
            body = json.loads(request.content)
            posted.append(body)
            return httpx.Response(
                201, json={"id": "dry-run", "accepted": True},
                request=request,
            )

        transport = httpx.MockTransport(_handler)
        client = httpx.AsyncClient(transport=transport)
        emitter = SupervisedEmitter(
            endpoint="http://dry-run",
            ingest_key="dry-run-key",
            harness=args.harness,
            cwd=args.cwd,
            heartbeat_s=0.01,  # fast for demo
            client=client,
        )

        title = args.title or " ".join(cmd[:3])
        print(
            f"[agentos.supervisor] dry-run mode — supervising: {' '.join(cmd)}",
            file=sys.stderr,
        )

        exit_code = await emitter.run_supervised(
            cmd,
            title=title,
            project_hint=args.project_hint,
            external_ref=args.external_ref,
            cwd=args.cwd,
        )

        print("\n[agentos.supervisor] events posted:", file=sys.stderr)
        for ev in posted:
            print(json.dumps(ev, indent=2), file=sys.stderr)

        await client.aclose()
        return

    # Live mode
    if not ingest_key:
        print(
            "ERROR: AGENTOS_INGEST_KEY is required in live mode. "
            "Use --dry-run to print events without posting.",
            file=sys.stderr,
        )
        sys.exit(1)

    emitter = SupervisedEmitter(
        endpoint=endpoint,
        ingest_key=ingest_key,
        harness=args.harness,
        cwd=args.cwd,
    )

    try:
        exit_code = await emitter.run_supervised(
            cmd,
            title=args.title,
            project_hint=args.project_hint,
            external_ref=args.external_ref,
            cwd=args.cwd,
        )
        sys.exit(exit_code)
    except Exception as exc:
        print(f"[agentos.supervisor] error: {exc}", file=sys.stderr)
        sys.exit(1)
    finally:
        await emitter.close()


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Supervised work-event emitter wrapper (Agent OS)",
    )
    parser.add_argument(
        "command", nargs=argparse.REMAINDER,
        help="Command to supervise (after --)",
    )
    parser.add_argument(
        "--harness", default="generic",
        choices=["claude", "antigravity", "codex", "generic"],
        help="Harness name for emitted events (default: generic)",
    )
    parser.add_argument(
        "--title", default=None,
        help="Session title",
    )
    parser.add_argument(
        "--cwd", default=None,
        help="Working directory",
    )
    parser.add_argument(
        "--project-hint", default=None,
        help="Project hint for correlation",
    )
    parser.add_argument(
        "--external-ref", default=None,
        help="External reference (SC-<n> or #<n>)",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Print events to stderr instead of posting",
    )
    args = parser.parse_args()

    try:
        asyncio.run(_run(args))
    except KeyboardInterrupt:
        print("\n[agentos.supervisor] interrupted", file=sys.stderr)
        sys.exit(130)


if __name__ == "__main__":
    main()
