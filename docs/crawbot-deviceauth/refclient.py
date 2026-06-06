#!/usr/bin/env python3
"""
OpenClaw device-auth reference client — proves the device-pairing handshake that
the AgentOS Go harness must replicate. Mirrors the gateway's compiled protocol:

  - device identity = Ed25519 keypair; deviceId = sha256(raw 32-byte pubkey) hex
  - connect.challenge -> nonce
  - sign v3 payload: "v3|deviceId|clientId|clientMode|role|scopes_csv|signedAtMs|token|nonce|platform|deviceFamily"
  - connect with auth.deviceToken + device{id,publicKey(rawb64url),signature(b64url),signedAt,nonce}
  - talk.session.create (requires operator.write)

Usage:
  python3 refclient.py gen-identity              # prints {deviceId, publicKeyRawB64Url, privateKeyPem}
  python3 refclient.py connect <url> <devicetoken>  # full handshake + talk.session.create
Identity is read from /tmp/oc-refclient/identity.json
"""
import asyncio, base64, hashlib, json, sys, time, ssl
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization

IDENT_PATH = "/tmp/oc-refclient/identity.json"
ED25519_SPKI_PREFIX = bytes.fromhex("302a300506032b6570032100")


def b64url(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).decode().rstrip("=")


def gen_identity():
    sk = Ed25519PrivateKey.generate()
    pk = sk.public_key()
    raw_pub = pk.public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
    priv_pem = sk.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    ).decode()
    device_id = hashlib.sha256(raw_pub).hexdigest()
    ident = {
        "deviceId": device_id,
        "publicKeyRawB64Url": b64url(raw_pub),
        "privateKeyPem": priv_pem,
    }
    with open(IDENT_PATH, "w") as f:
        json.dump(ident, f, indent=2)
    print(json.dumps({k: v for k, v in ident.items() if k != "privateKeyPem"}, indent=2))
    print("privateKeyPem written to", IDENT_PATH)


def load_identity():
    with open(IDENT_PATH) as f:
        return json.load(f)


def sign_payload(priv_pem: str, payload: str) -> str:
    sk = serialization.load_pem_private_key(priv_pem.encode(), password=None)
    return b64url(sk.sign(payload.encode("utf-8")))


def build_v3_payload(device_id, client_id, client_mode, role, scopes, signed_at_ms, token, nonce, platform, device_family):
    scopes_csv = ",".join(scopes)
    norm = lambda s: (s or "").strip().lower()
    return "|".join([
        "v3", device_id, client_id, client_mode, role, scopes_csv,
        str(signed_at_ms), token or "", nonce, norm(platform), norm(device_family),
    ])


async def connect(url, device_token):
    import websockets
    ident = load_identity()
    client_id = "gateway-client"
    client_mode = "backend"
    role = "operator"
    scopes = ["operator.read", "operator.write"]
    platform = "linux"
    device_family = ""

    ssl_ctx = None
    if url.startswith("wss://"):
        ssl_ctx = ssl.create_default_context()
        ssl_ctx.check_hostname = False
        ssl_ctx.verify_mode = ssl.CERT_NONE

    async with websockets.connect(url, ssl=ssl_ctx, open_timeout=15, max_size=8*1024*1024) as ws:
        # 1. Wait briefly for connect.challenge to obtain nonce
        nonce = ""
        try:
            while True:
                raw = await asyncio.wait_for(ws.recv(), timeout=3)
                msg = json.loads(raw)
                if msg.get("type") == "event" and msg.get("event") == "connect.challenge":
                    nonce = (msg.get("payload") or {}).get("nonce", "")
                    print("[refclient] got challenge nonce:", nonce[:16], "...")
                    break
        except asyncio.TimeoutError:
            print("[refclient] no challenge within 3s (nonce stays empty)")

        # 2. Build + sign v3 payload, send connect
        signed_at = int(time.time() * 1000)
        payload = build_v3_payload(
            ident["deviceId"], client_id, client_mode, role, scopes,
            signed_at, device_token, nonce, platform, device_family,
        )
        signature = sign_payload(ident["privateKeyPem"], payload)
        connect_params = {
            "minProtocol": 4,
            "maxProtocol": 4,
            "client": {"id": client_id, "version": "0.6.0", "platform": platform, "mode": client_mode},
            "role": role,
            "scopes": scopes,
            "caps": ["tool-events"],
            "auth": {"deviceToken": device_token},
            "device": {
                "id": ident["deviceId"],
                "publicKey": ident["publicKeyRawB64Url"],
                "signature": signature,
                "signedAt": signed_at,
                "nonce": nonce,
            },
        }
        await ws.send(json.dumps({"type": "req", "id": "c1", "method": "connect", "params": connect_params}))

        async def wait_res(req_id, timeout=15):
            while True:
                raw = await asyncio.wait_for(ws.recv(), timeout=timeout)
                msg = json.loads(raw)
                if msg.get("type") == "res" and msg.get("id") == req_id:
                    return msg
                # ignore events while waiting

        cres = await wait_res("c1")
        print("[refclient] connect res:", json.dumps(cres)[:300])
        if not cres.get("ok", False) or cres.get("error"):
            print("RESULT: CONNECT_FAILED")
            return

        # 3. chat.send — text chat, requires operator.write
        import uuid
        session_key = f"aos-{int(time.time()*1000)}"
        run_id = str(uuid.uuid4())
        await ws.send(json.dumps({
            "type": "req", "id": "m1", "method": "chat.send",
            "params": {
                "sessionKey": session_key,
                "message": "Reply with exactly: PONG",
                "idempotencyKey": run_id,
            },
        }))
        sres = await wait_res("m1", timeout=30)
        print("[refclient] chat.send res:", json.dumps(sres)[:400])
        if not (sres.get("ok", False) and not sres.get("error")):
            print("RESULT: CHAT_SEND_FAILED —", (sres.get("error") or {}).get("message"))
            return
        print("RESULT: SUCCESS — operator.write granted, chat.send accepted")

        # 4. Drain streaming events briefly to show a reply arrives
        deadline = time.time() + 40
        collected = ""
        seen_events = {}
        while time.time() < deadline:
            try:
                raw = await asyncio.wait_for(ws.recv(), timeout=5)
            except asyncio.TimeoutError:
                continue
            msg = json.loads(raw)
            if msg.get("type") == "event":
                ev = msg.get("event", "")
                pl = msg.get("payload") or {}
                seen_events[ev] = seen_events.get(ev, 0) + 1
                if ev in ("chat", "agent"):
                    print(f"[raw] event={ev} payload={json.dumps(pl)[:500]}")
        print("[refclient] event histogram:", json.dumps(seen_events))
        print("[refclient] final collected reply:", repr(collected[:200]))


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "gen-identity":
        gen_identity()
    elif cmd == "connect":
        asyncio.run(connect(sys.argv[2], sys.argv[3]))
    else:
        print(__doc__)
        sys.exit(1)
