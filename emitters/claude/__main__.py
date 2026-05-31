"""Runnable entry point for the Claude Code work-event emitter.

Usage:
    python -m emitters.claude -p "fix the auth bug" --cwd /path/to/repo
    python -m emitters.claude -p "explain this" --dry-run
    python -m emitters.claude -p "refactor X" --model claude-opus-4-8

Wraps ``claude -p`` in a bounded lifecycle: session.start → claude run → session.end.

Required env:
    AGENTOS_INGEST_KEY  — per-tenant ingest key (unless --dry-run)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys

from emitters.claude import ClaudeEmitter


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
        emitter = ClaudeEmitter(
            endpoint="http://dry-run",
            ingest_key="dry-run-key",
            cwd=args.cwd,
            client=client,
        )

        # In dry-run mode we simulate a claude run without actually calling claude.
        # Instead we emit the lifecycle events and show what would be sent.
        import uuid
        session_id = str(uuid.uuid4())

        print(
            "[agentos.claude] dry-run mode — simulating bounded lifecycle",
            file=sys.stderr,
        )
        print(f"[agentos.claude] session_id: {session_id}", file=sys.stderr)
        print(
            f"[agentos.claude] prompt: {prompt[:100]}{'...' if len(prompt) > 100 else ''}",
            file=sys.stderr,
        )

        await emitter.emit_start(
            session_id=session_id,
            title=args.title or prompt[:80],
            project_hint=args.project_hint,
            external_ref=args.external_ref,
        )

        print("[agentos.claude] simulated claude run...", file=sys.stderr)

        await emitter.emit_end(
            session_id=session_id,
            status="done",
            cost_usd=0.05,
            payload={"duration_s": 1.5, "dry_run": True},
        )

        print("\n[agentos.claude] events that would be posted:", file=sys.stderr)
        for ev in posted:
            print(json.dumps(ev, indent=2), file=sys.stderr)

        await client.aclose()
        return

    # Live mode — actually run claude
    if not ingest_key:
        print(
            "ERROR: AGENTOS_INGEST_KEY is required in live mode. "
            "Use --dry-run to print events without posting.",
            file=sys.stderr,
        )
        sys.exit(1)

    emitter = ClaudeEmitter(
        endpoint=endpoint,
        ingest_key=ingest_key,
        cwd=args.cwd,
    )

    try:
        result = await emitter.run_claude(
            prompt,
            title=args.title,
            project_hint=args.project_hint,
            external_ref=args.external_ref,
            cwd=args.cwd,
            model=args.model,
        )
        if result.exit_code != 0:
            sys.exit(result.exit_code)
    except Exception as exc:
        print(f"[agentos.claude] error: {exc}", file=sys.stderr)
        sys.exit(1)
    finally:
        await emitter.close()


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Claude Code work-event emitter (Agent OS)",
    )
    parser.add_argument(
        "-p", "--prompt", required=True,
        help="Prompt to send to Claude",
    )
    parser.add_argument(
        "--title", default=None,
        help="Session title (defaults to first 80 chars of prompt)",
    )
    parser.add_argument(
        "--cwd", default=None,
        help="Working directory for the Claude invocation",
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
        "--model", default=None,
        help="Claude model to use (e.g. claude-opus-4-8)",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Print events to stderr instead of posting",
    )
    args = parser.parse_args()

    try:
        asyncio.run(_run(args))
    except KeyboardInterrupt:
        print("\n[agentos.claude] interrupted", file=sys.stderr)
        sys.exit(130)


if __name__ == "__main__":
    main()
