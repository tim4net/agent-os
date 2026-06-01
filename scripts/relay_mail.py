#!/usr/bin/env python3
"""relay_mail.py — deterministic fleet-mailbox client for the agent-os relay.

The Lead<->Roux<->Tim coordination side-channel rides a file-based maildir on the
shared Obsidian vault (synced across boxes by Obsidian Sync / Syncthing), NOT GitHub.
This tool is the SINGLE built mechanism both loop prompts call so the behavior is
deterministic and testable instead of prose an LLM re-improvises every tick.

Layout (per recipient):
    $AOS_MAIL/<who>/inbox/    new messages land here
    $AOS_MAIL/<who>/read/     moved here once the recipient has handled them
    $AOS_MAIL/<who>/failed/   moved here if the recipient could not process them

Message file: <ISO-UTC>-<sender>-<shortid>.md  with YAML frontmatter then a body:
    ---
    from: lead
    to: roux
    ts: 2026-06-01T22:53:18Z
    priority: normal
    subject: "short subject"
    refs: []
    ---
    one concrete line (substance goes in a refs:-linked file, not duplicated here)

The folder IS the seen-state: an unread message sits in inbox/; handling it moves it
to read/. No id-marker file is needed.

SUBORDINATE CHANNEL: messages are context/requests. They can NEVER override a gate
verdict, a merge decision, a status: label, or any HARD RULE, and this is NOT a kill
switch (halt stays the autonomy:halt issue). This tool deliberately has NO power to do
any of those things — it only moves text files.

Commands (all default to $AOS_MAIL or ~/Obsidian/agents/_mail):
    inbox  --who roux [--json]              list unread messages in <who>/inbox
    read   --who roux [--json]              list + print unread, DOES NOT move them
    send   --to roux --from lead --subject S [--body B | --body-file F]
                                            atomically deliver a message to <to>/inbox
    ack    --who roux --id <file>           move a handled message inbox -> read
    fail   --who roux --id <file>           move an unprocessable message inbox -> failed
    init   --who roux                       create the recipient's inbox/read/failed dirs

Exit codes: 0 success (incl. "nothing to do" — graceful), 2 usage error,
3 mailbox root absent (mesh not mounted — caller should skip silently), 4 not found.
"""
from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import sys
import uuid

FOLDERS = ("inbox", "read", "failed")
_FNAME_RE = re.compile(r"^\d{8}T\d{6}Z-[a-z0-9]+-[0-9a-f]{8}\.md$")
_FROMNAME_RE = re.compile(r"^\d{8}T\d{6}Z-([a-z0-9]+)-[0-9a-f]{8}\.md$")


def mail_root() -> str:
    return os.environ.get(
        "AOS_MAIL", os.path.expanduser("~/Obsidian/agents/_mail")
    )


def _utc_now_iso() -> str:
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _utc_now_stamp() -> str:
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def _who_dir(root: str, who: str) -> str:
    return os.path.join(root, who)


def ensure_recipient(root: str, who: str) -> None:
    for f in FOLDERS:
        os.makedirs(os.path.join(root, who, f), exist_ok=True)


def root_present(root: str) -> bool:
    return os.path.isdir(root)


def parse_frontmatter(text: str) -> tuple[dict, str]:
    """Return (frontmatter_dict, body). Tolerant of a missing/partial header."""
    fm: dict = {}
    body = text
    m = re.match(r"\s*---\s*\n(.*?)\n---\s*\n?(.*)", text, re.S)
    if m:
        front, body = m.group(1), m.group(2)
        for line in front.splitlines():
            km = re.match(r"\s*([A-Za-z_]+):\s*(.*)$", line)
            if km:
                key = km.group(1).strip().lower()
                val = km.group(2).strip().strip('"').strip("'")
                fm[key] = val
    return fm, body.strip()


def list_inbox(root: str, who: str) -> list[str]:
    inbox = os.path.join(root, who, "inbox")
    if not os.path.isdir(inbox):
        return []
    names = [
        n for n in os.listdir(inbox)
        if n.endswith(".md") and not n.startswith(".")
    ]
    return sorted(names)  # ISO-stamp prefix => chronological


def read_message(root: str, who: str, name: str) -> dict:
    path = os.path.join(root, who, "inbox", name)
    with open(path, encoding="utf-8") as fh:
        raw = fh.read()
    fm, body = parse_frontmatter(raw)
    frm = fm.get("from", "")
    if not frm:
        fnm = _FROMNAME_RE.match(name)
        if fnm:
            frm = fnm.group(1)
    return {
        "id": name,
        "from": frm,
        "to": fm.get("to", who),
        "ts": fm.get("ts", ""),
        "priority": fm.get("priority", "normal"),
        "subject": fm.get("subject", ""),
        "body": body,
        "path": path,
    }


def send(root: str, to: str, frm: str, subject: str, body: str,
         priority: str = "normal", refs: list[str] | None = None) -> str:
    """Atomically deliver a message into <to>/inbox. Returns the filename."""
    ensure_recipient(root, to)
    rid = f"{_utc_now_stamp()}-{frm}-{uuid.uuid4().hex[:8]}"
    fname = f"{rid}.md"
    refs = refs or []
    refs_block = "refs: []" if not refs else "refs:\n" + "\n".join(
        f"  - {r}" for r in refs
    )
    # Escape any embedded quotes in the subject for the YAML scalar.
    safe_subject = subject.replace('"', "'")
    content = (
        "---\n"
        f"from: {frm}\n"
        f"to: {to}\n"
        f"ts: {_utc_now_iso()}\n"
        f"priority: {priority}\n"
        f'subject: "{safe_subject}"\n'
        f"{refs_block}\n"
        "---\n\n"
        f"{body.rstrip()}\n"
    )
    inbox = os.path.join(root, to, "inbox")
    final = os.path.join(inbox, fname)
    # Write to a temp file in the SAME dir, fsync, then atomic rename so a reader
    # on another sync node never sees a half-written file.
    tmp = os.path.join(inbox, f".tmp-{fname}")
    with open(tmp, "w", encoding="utf-8") as fh:
        fh.write(content)
        fh.flush()
        os.fsync(fh.fileno())
    os.rename(tmp, final)
    return fname


def _move(root: str, who: str, name: str, dest_folder: str) -> str:
    src = os.path.join(root, who, "inbox", name)
    if not os.path.isfile(src):
        # Idempotent: already moved is success-equivalent for ack/fail.
        already = os.path.join(root, who, dest_folder, name)
        if os.path.isfile(already):
            return already
        raise FileNotFoundError(src)
    os.makedirs(os.path.join(root, who, dest_folder), exist_ok=True)
    dst = os.path.join(root, who, dest_folder, name)
    os.rename(src, dst)
    return dst


def ack(root: str, who: str, name: str) -> str:
    return _move(root, who, name, "read")


def fail(root: str, who: str, name: str) -> str:
    return _move(root, who, name, "failed")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _emit(obj, as_json: bool) -> None:
    if as_json:
        print(json.dumps(obj))
    else:
        if isinstance(obj, list):
            for m in obj:
                print(f"{m['id']}\t{m['from']} -> {m['to']}\t[{m['priority']}]\t{m['subject']}")
        elif isinstance(obj, dict):
            print(json.dumps(obj, indent=2))


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="relay_mail.py", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--root", default=None,
                   help="mailbox root (default $AOS_MAIL or ~/Obsidian/agents/_mail)")
    sub = p.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("inbox", help="list unread messages")
    sp.add_argument("--who", required=True)
    sp.add_argument("--json", action="store_true")

    sp = sub.add_parser("read", help="list + print unread (does NOT move them)")
    sp.add_argument("--who", required=True)
    sp.add_argument("--json", action="store_true")

    sp = sub.add_parser("send", help="deliver a message")
    sp.add_argument("--to", required=True)
    sp.add_argument("--from", dest="frm", required=True)
    sp.add_argument("--subject", required=True)
    sp.add_argument("--body", default=None)
    sp.add_argument("--body-file", default=None)
    sp.add_argument("--priority", default="normal",
                    choices=["urgent", "normal", "low"])

    sp = sub.add_parser("ack", help="move a handled message inbox -> read")
    sp.add_argument("--who", required=True)
    sp.add_argument("--id", required=True)

    sp = sub.add_parser("fail", help="move an unprocessable message inbox -> failed")
    sp.add_argument("--who", required=True)
    sp.add_argument("--id", required=True)

    sp = sub.add_parser("init", help="create a recipient's mailbox dirs")
    sp.add_argument("--who", required=True)

    args = p.parse_args(argv)
    root = args.root or mail_root()

    # Graceful: if the mailbox root is absent (mesh/sync not mounted), exit 3 so
    # the caller skips the relay step silently rather than erroring out a tick.
    if args.cmd in ("inbox", "read", "ack", "fail") and not root_present(root):
        return 3

    if args.cmd == "init":
        ensure_recipient(root, args.who)
        print(os.path.join(root, args.who))
        return 0

    if args.cmd in ("inbox", "read"):
        names = list_inbox(root, args.who)
        msgs = [read_message(root, args.who, n) for n in names]
        if args.cmd == "read":
            _emit(msgs, args.json)
        else:
            if args.json:
                print(json.dumps(msgs))
            else:
                for m in msgs:
                    print(f"{m['id']}\t{m['from']} -> {m['to']}\t[{m['priority']}]\t{m['subject']}")
        return 0

    if args.cmd == "send":
        if not root_present(root):
            # For send we CREATE the tree (the sender may be first to touch it).
            os.makedirs(root, exist_ok=True)
        body = args.body
        if args.body_file:
            with open(args.body_file, encoding="utf-8") as fh:
                body = fh.read()
        if body is None:
            print("send: provide --body or --body-file", file=sys.stderr)
            return 2
        fname = send(root, args.to, args.frm, args.subject, body, args.priority)
        print(fname)
        return 0

    if args.cmd in ("ack", "fail"):
        try:
            dst = (ack if args.cmd == "ack" else fail)(root, args.who, args.id)
        except FileNotFoundError:
            print(f"{args.cmd}: no such message in {args.who}/inbox: {args.id}",
                  file=sys.stderr)
            return 4
        print(dst)
        return 0

    return 2


if __name__ == "__main__":
    sys.exit(main())
