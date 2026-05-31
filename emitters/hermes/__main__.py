"""Runnable entry point for the Hermes work-event emitter.

Usage:
    python -m emitters.hermes [--title TITLE] [--dry-run]

Wraps a supervised() session around the current Hermes invocation.
When AGENTOS_ENDPOINT is live, events are POSTed; with --dry-run,
events are printed to stderr instead.

Required env:
    AGENTOS_INGEST_KEY  — per-tenant ingest key (unless --dry-run)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys

from emitters.hermes.emitter import HermesEmitter, WorkEvent, Kind


def _dry_post(event: WorkEvent) -> None:
    """Print the event JSON to stderr (dry-run mode)."""
    print(json.dumps(event.to_dict(), indent=2), file=sys.stderr)


async def _run(args: argparse.Namespace) -> None:
    ingest_key = os.environ.get("AGENTOS_INGEST_KEY")
    endpoint = os.environ.get("AGENTOS_ENDPOINT")
    title = args.title
    hb_s = args.heartbeat_s

    if args.dry_run:
        # Dry-run: no HTTP, just emit events to stderr via callbacks
        # We build a minimal emitter for its event construction but
        # bypass the POST by using a MockTransport that captures events.
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
        emitter = HermesEmitter(
            endpoint="http://dry-run",
            ingest_key="dry-run-key",
            heartbeat_s=hb_s,
            client=client,
        )
        try:
            async with emitter.supervised(title=title):
                # The supervised block is the "Hermes work" placeholder.
                # In a real Hermes hook, this would be replaced by the
                # delegate's actual work coroutine. For the entry point
                # demo, we just sleep briefly to show heartbeats firing.
                print(
                    "[agentos.hermes] supervised session active — "
                    "Ctrl+C to cancel, or let work finish",
                    file=sys.stderr,
                )
                await asyncio.sleep(1)  # placeholder for real work
        except asyncio.CancelledError:
            print(
                "[agentos.hermes] session cancelled by user",
                file=sys.stderr,
            )
            raise
        finally:
            # Print all captured events
            for ev in posted:
                print(json.dumps(ev, indent=2), file=sys.stderr)
            await client.aclose()
        return

    # Live mode — POST to the real endpoint
    emitter = HermesEmitter(
        endpoint=endpoint,
        ingest_key=ingest_key,
        heartbeat_s=hb_s,
    )
    try:
        async with emitter.supervised(title=title):
            # Placeholder for real Hermes work.  In production this
            # entry point would be invoked by the Hermes scheduler
            # with the delegate's coroutine wired in.
            print(
                "[agentos.hermes] supervised session active — "
                "Ctrl+C to cancel, or let work finish",
                file=sys.stderr,
            )
            await asyncio.sleep(0)  # placeholder — yields control
    except asyncio.CancelledError:
        print(
            "[agentos.hermes] session cancelled",
            file=sys.stderr,
        )
        raise
    finally:
        await emitter.close()


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Hermes work-event emitter (Agent OS)",
    )
    parser.add_argument(
        "--title", default=None,
        help="Session title (e.g. the work-unit name)",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Print events to stderr instead of POSTing",
    )
    parser.add_argument(
        "--heartbeat-s", type=int, default=None,
        help="Heartbeat interval in seconds (default: 60)",
    )
    args = parser.parse_args()

    if not args.dry_run and not os.environ.get("AGENTOS_INGEST_KEY"):
        parser.error(
            "AGENTOS_INGEST_KEY is required in live mode. "
            "Use --dry-run to print events without posting."
        )

    hb_s = args.heartbeat_s or int(
        os.environ.get("AGENTOS_HEARTBEAT_S", "60")
    )
    args.heartbeat_s = hb_s

    try:
        asyncio.run(_run(args))
    except KeyboardInterrupt:
        print("\n[agentos.hermes] interrupted", file=sys.stderr)
        sys.exit(130)


if __name__ == "__main__":
    main()
