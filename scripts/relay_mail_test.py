#!/usr/bin/env python3
"""Tests for scripts/relay_mail.py — the fleet-mailbox relay client.

Pure-stdlib (unittest), no network, no real vault: every test runs against a fresh
temp mailbox root. Proves the behaviors the loop prompts depend on:
  - send delivers an atomic, well-formed message into <to>/inbox
  - inbox/read list unread without moving them (re-read is stable)
  - ack moves inbox -> read (the "seen" transition); fail -> failed
  - dedupe: an ack'd message no longer appears as unread
  - graceful-absent: commands on a missing root exit 3, never raise
  - frontmatter round-trips; sender is recoverable from filename if header thin
  - no half-written temp file is ever visible as a message
  - ack is idempotent (already-moved is success)

Run: python3 scripts/relay_mail_test.py   (or via unittest discovery)
"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import relay_mail as rm  # noqa: E402

SCRIPT = os.path.join(HERE, "relay_mail.py")


def run_cli(root, *args):
    """Invoke the CLI as a subprocess; return (rc, stdout, stderr)."""
    cmd = [sys.executable, SCRIPT, "--root", root, *args]
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    return p.returncode, p.stdout.strip(), p.stderr.strip()


class RelayMailAPI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="relaymail-")
        self.root = os.path.join(self.tmp, "_mail")
        os.makedirs(self.root)

    def tearDown(self):
        import shutil
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_send_creates_wellformed_message(self):
        fname = rm.send(self.root, "roux", "lead", "hello roux",
                        "Please confirm with MAILBOX-OK.")
        self.assertTrue(fname.endswith(".md"))
        self.assertRegex(fname, rm._FNAME_RE)
        path = os.path.join(self.root, "roux", "inbox", fname)
        self.assertTrue(os.path.isfile(path))
        with open(path) as fh:
            fm, body = rm.parse_frontmatter(fh.read())
        self.assertEqual(fm["from"], "lead")
        self.assertEqual(fm["to"], "roux")
        self.assertEqual(fm["priority"], "normal")
        self.assertEqual(fm["subject"], "hello roux")
        self.assertIn("MAILBOX-OK", body)
        self.assertTrue(fm["ts"].endswith("Z"))

    def test_inbox_lists_then_ack_moves_and_dedupes(self):
        rm.send(self.root, "roux", "lead", "s1", "one")
        names = rm.list_inbox(self.root, "roux")
        self.assertEqual(len(names), 1)
        # read does NOT move it — still unread on a second look
        again = rm.list_inbox(self.root, "roux")
        self.assertEqual(names, again)
        # ack moves inbox -> read
        dst = rm.ack(self.root, "roux", names[0])
        self.assertIn(os.path.join("roux", "read"), dst)
        # now unread is empty (deduped — won't be re-processed)
        self.assertEqual(rm.list_inbox(self.root, "roux"), [])
        self.assertTrue(os.path.isfile(
            os.path.join(self.root, "roux", "read", names[0])))

    def test_fail_moves_to_failed(self):
        rm.send(self.root, "roux", "lead", "s", "bad")
        name = rm.list_inbox(self.root, "roux")[0]
        dst = rm.fail(self.root, "roux", name)
        self.assertIn(os.path.join("roux", "failed"), dst)
        self.assertEqual(rm.list_inbox(self.root, "roux"), [])

    def test_ack_idempotent(self):
        rm.send(self.root, "roux", "lead", "s", "x")
        name = rm.list_inbox(self.root, "roux")[0]
        rm.ack(self.root, "roux", name)
        # second ack must not raise — already-moved is success-equivalent
        dst = rm.ack(self.root, "roux", name)
        self.assertTrue(os.path.isfile(dst))

    def test_chronological_order(self):
        # filenames are ISO-stamped; list must be chronological
        a = rm.send(self.root, "roux", "lead", "a", "1")
        b = rm.send(self.root, "roux", "tim", "b", "2")
        names = rm.list_inbox(self.root, "roux")
        self.assertEqual(names, sorted([a, b]))

    def test_sender_recovered_from_filename_when_header_thin(self):
        # write a message with NO frontmatter; sender comes from the filename
        rm.ensure_recipient(self.root, "roux")
        fname = "20260601T120000Z-lead-deadbeef.md"
        with open(os.path.join(self.root, "roux", "inbox", fname), "w") as fh:
            fh.write("just a body, no frontmatter\n")
        msg = rm.read_message(self.root, "roux", fname)
        self.assertEqual(msg["from"], "lead")
        self.assertIn("just a body", msg["body"])

    def test_no_halfwritten_temp_visible(self):
        rm.send(self.root, "roux", "lead", "s", "x")
        # the .tmp- staging file must be gone after an atomic send
        inbox = os.path.join(self.root, "roux", "inbox")
        leftover = [n for n in os.listdir(inbox) if n.startswith(".tmp-")]
        self.assertEqual(leftover, [])
        # and list_inbox never returns dotfiles
        self.assertTrue(all(not n.startswith(".")
                            for n in rm.list_inbox(self.root, "roux")))

    def test_subject_with_quotes_is_safe(self):
        fname = rm.send(self.root, "roux", "lead", 'say "hi" now', "b")
        with open(os.path.join(self.root, "roux", "inbox", fname)) as fh:
            fm, _ = rm.parse_frontmatter(fh.read())
        # must parse back without breaking the YAML scalar
        self.assertIn("hi", fm["subject"])


class RelayMailCLI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="relaymail-cli-")
        self.root = os.path.join(self.tmp, "_mail")
        os.makedirs(self.root)

    def tearDown(self):
        import shutil
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_cli_roundtrip_send_inbox_ack(self):
        rc, out, err = run_cli(self.root, "send", "--to", "roux",
                               "--from", "lead", "--subject", "ping",
                               "--body", "MAILBOX-OK?")
        self.assertEqual(rc, 0, err)
        fname = out
        self.assertRegex(fname, rm._FNAME_RE)

        rc, out, err = run_cli(self.root, "inbox", "--who", "roux", "--json")
        self.assertEqual(rc, 0, err)
        import json
        msgs = json.loads(out)
        self.assertEqual(len(msgs), 1)
        self.assertEqual(msgs[0]["from"], "lead")

        rc, out, err = run_cli(self.root, "ack", "--who", "roux", "--id", fname)
        self.assertEqual(rc, 0, err)

        rc, out, err = run_cli(self.root, "inbox", "--who", "roux", "--json")
        self.assertEqual(rc, 0, err)
        self.assertEqual(json.loads(out), [])

    def test_cli_graceful_absent_root_exits_3(self):
        missing = os.path.join(self.tmp, "does-not-exist")
        rc, out, err = run_cli(missing, "inbox", "--who", "roux")
        self.assertEqual(rc, 3)  # caller skips silently; no traceback
        self.assertEqual(out, "")

    def test_cli_ack_missing_message_exits_4(self):
        run_cli(self.root, "init", "--who", "roux")
        rc, out, err = run_cli(self.root, "ack", "--who", "roux",
                               "--id", "nope.md")
        self.assertEqual(rc, 4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
