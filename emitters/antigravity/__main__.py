"""Runnable entry point for the Antigravity work-event emitter.

Usage:
    python -m emitters.antigravity -p "write tests" --cwd /path/to/repo
    python -m emitters.antigravity -p "explain this" --dry-run

Wraps ``agy -p`` in a bounded lifecycle: session.start → agy run → session.end.

Required env:
    AGENTOS_INGEST_KEY  — per-tenant ingest key (unless --dry-run)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys

from emitters.antigravity import AntigravityEmitter


async def _run(args: argparse.Namespace) -> None:
    ingest_key = os.environ.get("AGENTOS_INGEST_KEY")
    endpoint = os.environ.get("AGENTOS_ENDPOINT")
    prompt = args.prompt

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
        emitter = AntigravityEmitter(
            endpoint="http://dry-run",
            ingest_key="dry-run-key",
            cwd=args.cwd,
            client=client,
        )

        import uuid
        session_id = str(uuid.uuid4())

        print(
            "[agentos.antigravity] dry-run mode — simulating bounded lifecycle",
            file=sys.stderr,
        )
        print(f"[agentos.antigravity] session_id: {session_id}", file=sys.stderr)

        await emitter.emit_start(
            session_id=session_id,
            title=args.title or prompt[:80],
            project_hint=args.project_hint,
            external_ref=args.external_ref,
        )

        print("[agentos.antigravity] simulated agy run...", file=sys.stderr)

        await emitter.emit_end(
            session_id=session_id,
            status="done",
            payload={"duration_s": 1.2, "dry_run": True},
        )

        print("\n[agentos.antigravity] events that would be posted:", file=sys.stderr)
        for ev in posted:
            print(json.dumps(ev, indent=2), file=sys.stderr)

        await client.aclose()
        return

    if not ingest_key:
        print(
            "ERROR: AGENTOS_INGEST_KEY is required in live mode. "
            "Use --dry-run to print events without posting.",
            file=sys.stderr,
        )
        sys.exit(1)

    emitter = AntigravityEmitter(
        endpoint=endpoint,
        ingest_key=ingest_key,
        cwd=args.cwd,
    )

    try:
        result = await emitter.run_agy(
            prompt,
            title=args.title,
            project_hint=args.project_hint,
            external_ref=args.external_ref,
            cwd=args.cwd,
        )
        if result.exit_code != 0:
            sys.exit(result.exit_code)
    except Exception as exc:
        print(f"[agentos.antigravity] error: {exc}", file=sys.stderr)
        sys.exit(1)
    finally:
        await emitter.close()


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Antigravity work-event emitter (Agent OS)",
    )
    parser.add_argument(
        "-p", "--prompt", required=True,
        help="Prompt to send to Antigravity",
    )
    parser.add_argument(
        "--title", default=None,
        help="Session title (defaults to first 80 chars of prompt)",
    )
    parser.add_argument(
        "--cwd", default=None,
        help="Working directory for the agy invocation",
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
        print("\n[agentos.antigravity] interrupted", file=sys.stderr)
        sys.exit(130)


if __name__ == "__main__":
    main()
