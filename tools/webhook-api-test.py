#!/usr/bin/env python3
"""End-to-end test suite for the ECG Hub webhook REST API — stdlib only.

Drives /api/v1/webhooks with an API key (X-API-Key), exactly as an external
machine client would, and checks every documented behaviour: authentication,
permissions, CRUD, validation, signed delivery, delivery history, resend,
per-user isolation and the SSRF guard.

It runs its own receiver so deliveries can be inspected: /hook answers 200,
/fail answers 500. The receiver binds 0.0.0.0 and is advertised to the backend
as --receiver-host (default host.docker.internal), because the backend runs in
Docker and its SSRF guard refuses loopback targets.

Usage:
    # mint a key from credentials (needs apikey.manage), run everything
    python3 webhook-api-test.py --username admin --password secret

    # or use an existing key
    python3 webhook-api-test.py --api-key ecghub_xxx

    # full ingest loop: upload an ECG and follow the event to the receiver
    python3 webhook-api-test.py --api-key ecghub_xxx --ingest-file sample.xml

    # per-user isolation needs a second key owned by a different user
    python3 webhook-api-test.py --api-key ecghub_aaa --other-api-key ecghub_bbb

Exit code is 0 only when every check passed. Known open failures on main:
#40 (malformed id → 500), #41 (test delivery not logged), #42 (unknown vendor
accepted) — six checks in total.
"""

import argparse
import hashlib
import hmac
import json
import queue
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer

GREEN, RED, YELLOW, CYAN, DIM, RESET = (
    "\033[32m", "\033[31m", "\033[33m", "\033[36m", "\033[2m", "\033[0m",
)

# Event types the API is expected to offer (models.AllWebhookEvents).
EXPECTED_EVENTS = {
    "ecg.ingested", "ecg.unidentified", "ecg.quarantined",
    "ecg.duplicate", "hl7.exhausted", "hl7.rejected",
}

ARGS = None
RECEIVED = queue.Queue()   # every request the receiver got, newest last
RESULTS = []               # (ok, name, detail)
CREATED = []               # webhook ids to clean up


# ─────────────────────────── result reporting ────────────────────────────

def check(name, ok, detail=""):
    RESULTS.append((ok, name, detail))
    mark = f"{GREEN}PASS{RESET}" if ok else f"{RED}FAIL{RESET}"
    line = f"  [{mark}] {name}"
    if detail and not ok:
        line += f"\n         {DIM}{detail}{RESET}"
    print(line)
    return ok


def section(title):
    print(f"\n{CYAN}── {title}{RESET}")


# ────────────────────────────── HTTP client ──────────────────────────────

def call(method, path, *, key=None, body=None, headers=None, form=None):
    """Call the API. Returns (status, parsed_body_or_text, raw_text)."""
    url = ARGS.base_url.rstrip("/") + path
    data = None
    hdrs = dict(headers or {})
    if form is not None:
        boundary, data = encode_multipart(form)
        hdrs["Content-Type"] = f"multipart/form-data; boundary={boundary}"
    elif body is not None:
        data = json.dumps(body).encode()
        hdrs["Content-Type"] = "application/json"
    if key:
        hdrs.setdefault("X-API-Key", key)

    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8", "replace")
            status = resp.status
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", "replace")
        status = e.code
    except (urllib.error.URLError, TimeoutError) as e:
        return 0, {"error": str(e)}, str(e)
    try:
        return status, json.loads(raw) if raw else None, raw
    except json.JSONDecodeError:
        return status, raw, raw


def encode_multipart(fields):
    """Minimal multipart/form-data encoder: {name: (filename, bytes)}."""
    boundary = "----ecghubtest" + uuid.uuid4().hex
    out = b""
    for name, (filename, content) in fields.items():
        out += f"--{boundary}\r\n".encode()
        out += (f'Content-Disposition: form-data; name="{name}"; '
                f'filename="{filename}"\r\n').encode()
        out += b"Content-Type: application/octet-stream\r\n\r\n"
        out += content + b"\r\n"
    out += f"--{boundary}--\r\n".encode()
    return boundary, out


def mint_api_key(username, password):
    """Log in and create an API key. Requires apikey.manage."""
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor()
    )
    base = ARGS.base_url.rstrip("/")

    login = urllib.request.Request(
        base + "/api/v1/auth/login", method="POST",
        data=json.dumps({"username": username, "password": password}).encode(),
        headers={"Content-Type": "application/json"},
    )
    try:
        opener.open(login, timeout=30).read()
    except urllib.error.HTTPError as e:
        sys.exit(f"login failed: HTTP {e.code} {e.read().decode()[:200]}")

    create = urllib.request.Request(
        base + "/api/grpc.api.v1.APIKeyService/CreateApiKey", method="POST",
        data=json.dumps({"name": f"webhook-api-test {int(time.time())}"}).encode(),
        headers={"Content-Type": "application/json"},
    )
    try:
        payload = json.loads(opener.open(create, timeout=30).read())
    except urllib.error.HTTPError as e:
        sys.exit(f"API key creation failed: HTTP {e.code} {e.read().decode()[:200]}")
    return payload["plaintext"]


# ──────────────────────────────── receiver ───────────────────────────────

class Receiver(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        RECEIVED.put({
            "path": self.path,
            "headers": {k.lower(): v for k, v in self.headers.items()},
            "body": self.rfile.read(length),
        })
        code = 500 if self.path.startswith("/fail") else 200
        self.send_response(code)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, *_):
        pass


def start_receiver(port):
    server = HTTPServer(("0.0.0.0", port), Receiver)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def wait_delivery(timeout=15, path=None, webhook_id=None):
    """Return the next matching request, or None.

    Filtering on webhook_id matters: any other webhook configured on this
    server may point at the same receiver and would otherwise be mistaken for
    the delivery under test.
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            got = RECEIVED.get(timeout=max(0.1, deadline - time.time()))
        except queue.Empty:
            return None
        if path is not None and got["path"] != path:
            continue
        if webhook_id is not None and got["headers"].get("x-ecg-hub-webhook-id") != webhook_id:
            continue
        return got
    return None


def drain():
    while not RECEIVED.empty():
        RECEIVED.get_nowait()


def hook_url(path="/hook"):
    return f"http://{ARGS.receiver_host}:{ARGS.receiver_port}{path}"


def create_hook(**overrides):
    """Create a webhook and register it for cleanup. Returns the body."""
    body = {"name": "api-test", "url": hook_url()}
    body.update(overrides)
    status, payload, _ = call("POST", "/api/v1/webhooks", key=ARGS.api_key, body=body)
    if status == 201 and isinstance(payload, dict):
        CREATED.append(payload["id"])
    return status, payload


# ───────────────────────────────── tests ─────────────────────────────────

def test_authentication():
    section("Authentication")
    status, _, _ = call("GET", "/api/v1/webhooks")
    check("no credentials → 401", status == 401, f"got {status}")

    status, _, _ = call("GET", "/api/v1/webhooks", key="ecghub_" + "0" * 40)
    check("unknown API key → 401", status == 401, f"got {status}")

    status, payload, _ = call("GET", "/api/v1/webhooks", key=ARGS.api_key)
    check("X-API-Key → 200", status == 200 and isinstance(payload, list),
          f"got {status} {payload}")

    status, _, _ = call("GET", "/api/v1/webhooks",
                        headers={"Authorization": f"Bearer {ARGS.api_key}"})
    check("Authorization: Bearer <key> → 200", status == 200, f"got {status}")


def test_options():
    section("Filter options")
    status, payload, _ = call("GET", "/api/v1/webhooks/options", key=ARGS.api_key)
    if not check("GET /webhooks/options → 200", status == 200, f"got {status}"):
        return
    events = set(payload.get("events", []))
    check("options list every known event type", events == EXPECTED_EVENTS,
          f"missing={EXPECTED_EVENTS - events} extra={events - EXPECTED_EVENTS}")
    vendors = payload.get("vendors", [])
    check("options list the loaded vendor modules", len(vendors) > 0,
          "no vendors returned — no ingestion module loaded?")


def test_validation():
    section("Create — validation")
    cases = [
        ("empty name rejected", {"name": "", "url": hook_url()}),
        ("over-long name rejected", {"name": "a" * 101, "url": hook_url()}),
        ("non-http scheme rejected", {"name": "x", "url": "ftp://example.com"}),
        ("javascript: URL rejected", {"name": "x", "url": "javascript:alert(1)"}),
        ("URL without host rejected", {"name": "x", "url": "http://"}),
        ("unknown event rejected",
         {"name": "x", "url": hook_url(), "events": ["nope"]}),
    ]
    for name, body in cases:
        status, payload, _ = call("POST", "/api/v1/webhooks", key=ARGS.api_key, body=body)
        if status == 201:
            CREATED.append(payload["id"])
        check(name, status == 400, f"got {status} {payload}")

    status, _, _ = call("POST", "/api/v1/webhooks", key=ARGS.api_key,
                        headers={"Content-Type": "application/json"},
                        body=None)
    check("empty body rejected", status == 400, f"got {status}")

    # A vendor filter that matches no module silently disables every delivery,
    # so a typo is as damaging as an unknown event — which IS rejected. Issue #42.
    status, payload = create_hook(name="vendor-typo", vendors=["philpis"])
    check("unknown vendor rejected", status == 400,
          f"got {status} — webhook accepted a vendor no module provides")


def test_crud():
    section("CRUD")
    status, hook = create_hook(name="crud", secret="s3cr3t",
                               auth_header="Bearer tok123",
                               events=["ecg.ingested"], vendors=[])
    if not check("create → 201", status == 201, f"got {status} {hook}"):
        return None
    check("create defaults to enabled", hook.get("enabled") is True, str(hook))
    check("create reports has_secret / has_auth_header",
          hook.get("has_secret") and hook.get("has_auth_header"), str(hook))

    status, payload, raw = call("GET", "/api/v1/webhooks", key=ARGS.api_key)
    ids = [h["id"] for h in payload] if isinstance(payload, list) else []
    check("list contains the new webhook", hook["id"] in ids, f"got {ids}")
    leaked = [f for f in ("s3cr3t", "tok123", "secret_encrypted", "auth_header_encrypted",
                          "user_id") if f in raw]
    check("list never returns secret material", not leaked, f"leaked: {leaked}")

    # Tri-state: absent = keep, "" = clear.
    base = {"name": "crud-renamed", "url": hook_url(), "events": [], "vendors": []}
    status, payload, _ = call("PUT", f"/api/v1/webhooks/{hook['id']}",
                              key=ARGS.api_key, body=base)
    check("update renames the webhook",
          status == 200 and payload.get("name") == "crud-renamed", f"got {status} {payload}")
    check("update without secret keeps the stored secret",
          payload.get("has_secret") is True, str(payload))

    status, payload, _ = call("PUT", f"/api/v1/webhooks/{hook['id']}",
                              key=ARGS.api_key, body={**base, "secret": ""})
    check('update with secret "" clears it', payload.get("has_secret") is False, str(payload))

    status, payload, _ = call("PUT", "/api/v1/webhooks/" + str(uuid.uuid4()),
                              key=ARGS.api_key, body=base)
    check("update of an unknown id → 404", status == 404, f"got {status} {payload}")

    # A malformed id is a client error, not a server error: a 500 here also
    # lands in the admin "recent errors" panel as if the server had broken. Issue #40.
    for method, path, label in (
        ("PUT", "/api/v1/webhooks/not-a-uuid", "update"),
        ("DELETE", "/api/v1/webhooks/not-a-uuid", "delete"),
        ("POST", "/api/v1/webhooks/not-a-uuid/test", "test"),
        ("GET", "/api/v1/webhooks/not-a-uuid/deliveries", "deliveries"),
    ):
        status, payload, _ = call(method, path, key=ARGS.api_key,
                                  body=base if method == "PUT" else None)
        check(f"{label} with a malformed id → 404, not 500", status == 404,
              f"got {status} {payload}")
    return hook


def test_delivery():
    section("Signed delivery")
    secret = "test-signing-secret"
    status, hook = create_hook(name="delivery", secret=secret,
                               auth_header="Bearer tok123")
    if not check("create webhook for delivery", status == 201, str(hook)):
        return None

    drain()
    status, payload, _ = call("POST", f"/api/v1/webhooks/{hook['id']}/test", key=ARGS.api_key)
    check("POST /test → 200 ok",
          status == 200 and payload.get("ok") is True and payload.get("status_code") == 200,
          f"got {status} {payload}")

    got = wait_delivery(webhook_id=hook["id"])
    if not check("receiver got the test delivery", got is not None,
                 "nothing arrived — is --receiver-host reachable from the backend?"):
        return hook

    hdrs, body = got["headers"], got["body"]
    check("X-ECG-Hub-Event: test", hdrs.get("x-ecg-hub-event") == "test", str(hdrs))
    check("X-ECG-Hub-Webhook-ID matches",
          hdrs.get("x-ecg-hub-webhook-id") == hook["id"], str(hdrs))
    check("X-ECG-Hub-Timestamp present", bool(hdrs.get("x-ecg-hub-timestamp")), str(hdrs))
    check("Authorization header forwarded",
          hdrs.get("authorization") == "Bearer tok123", str(hdrs))

    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    check("HMAC-SHA256 signature valid",
          hmac.compare_digest(hdrs.get("x-ecg-hub-signature", ""), expected),
          f"got {hdrs.get('x-ecg-hub-signature')} want {expected}")

    parsed = json.loads(body)
    check("payload event/webhook_id consistent",
          parsed.get("event") == "test" and parsed.get("webhook_id") == hook["id"],
          str(parsed))

    status, payload, _ = call("GET", "/api/v1/webhooks", key=ARGS.api_key)
    row = next((h for h in payload if h["id"] == hook["id"]), {})
    check("last_status_code recorded on the webhook", row.get("last_status_code") == 200,
          str(row))

    # A test delivery is a delivery: it must show up in the history the UI reads,
    # and it must be resendable like any other. Issue #41.
    status, deliveries, _ = call("GET", f"/api/v1/webhooks/{hook['id']}/deliveries",
                                 key=ARGS.api_key)
    check("test delivery appears in the delivery history",
          status == 200 and isinstance(deliveries, list) and len(deliveries) == 1,
          f"got {status} {deliveries}")

    # Unsigned webhook: no signature header at all.
    status, plain = create_hook(name="unsigned")
    drain()
    call("POST", f"/api/v1/webhooks/{plain['id']}/test", key=ARGS.api_key)
    got = wait_delivery(webhook_id=plain["id"])
    check("no secret → no signature header",
          got is not None and "x-ecg-hub-signature" not in got["headers"],
          str(got["headers"]) if got else "no delivery")
    return hook


def test_failure_paths():
    section("Failure paths")
    status, hook = create_hook(name="failing", url=hook_url("/fail"))
    drain()
    status, payload, _ = call("POST", f"/api/v1/webhooks/{hook['id']}/test", key=ARGS.api_key)
    check("receiver 500 reported as failure",
          payload.get("ok") is False and payload.get("status_code") == 500,
          f"got {payload}")
    check("failed delivery still reached the receiver", wait_delivery(5) is not None)

    section("SSRF guard")
    for label, url in (
        ("loopback target blocked", "http://127.0.0.1:80/"),
        ("cloud metadata target blocked", "http://169.254.169.254/latest/meta-data/"),
    ):
        _, hook = create_hook(name=label, url=url)
        _, payload, _ = call("POST", f"/api/v1/webhooks/{hook['id']}/test", key=ARGS.api_key)
        check(label,
              payload.get("ok") is False and "SSRF guard" in payload.get("error", ""),
              f"got {payload}")


def test_isolation():
    section("Per-user isolation")
    if not ARGS.other_api_key:
        print(f"  {DIM}skipped — pass --other-api-key <key of another user>{RESET}")
        return
    status, hook = create_hook(name="isolation")
    if status != 201:
        check("create webhook for isolation test", False, str(hook))
        return
    other = ARGS.other_api_key
    for label, method, path in (
        ("read deliveries", "GET", f"/api/v1/webhooks/{hook['id']}/deliveries"),
        ("update", "PUT", f"/api/v1/webhooks/{hook['id']}"),
        ("delete", "DELETE", f"/api/v1/webhooks/{hook['id']}"),
        ("test", "POST", f"/api/v1/webhooks/{hook['id']}/test"),
    ):
        body = {"name": "hijack", "url": "http://example.com"} if method == "PUT" else None
        status, payload, _ = call(method, path, key=other, body=body)
        check(f"another user cannot {label} my webhook", status == 404,
              f"got {status} {payload}")

    status, payload, _ = call("GET", "/api/v1/webhooks", key=other)
    ids = [h["id"] for h in payload] if isinstance(payload, list) else []
    check("another user's list excludes my webhook", hook["id"] not in ids, f"got {ids}")


def test_ingest_loop():
    """Upload an ECG and follow the real event through to the receiver."""
    section("Ingest → webhook → history → resend")
    if not ARGS.ingest_file:
        print(f"  {DIM}skipped — pass --ingest-file <ecg file>{RESET}")
        return
    secret = "ingest-secret"
    status, hook = create_hook(name="ingest-loop", secret=secret, events=[], vendors=[])
    if not check("create webhook for the ingest loop", status == 201, str(hook)):
        return

    with open(ARGS.ingest_file, "rb") as f:
        content = f.read()
    # Ingestion deduplicates on the file's SHA-256, so an unmodified re-upload
    # yields ecg.duplicate instead of ecg.ingested. A trailing XML comment is
    # ignored by every parser and makes each run a genuinely new file.
    run_id = uuid.uuid4().hex
    if content.lstrip().startswith(b"<"):
        content += f"\n<!-- ecg-hub webhook-api-test {run_id} -->\n".encode()
    filename = f"webhook-api-test-{run_id[:8]}.xml"
    drain()
    status, payload, _ = call("POST", "/api/v1/uploads", key=ARGS.api_key,
                              form={"files": (filename, content)})
    if not check("upload accepted", status == 200 and payload.get("queued") == 1,
                 f"got {status} {payload}"):
        return

    got = wait_delivery(60, path="/hook", webhook_id=hook["id"])
    if not check("ingestion fired the webhook", got is not None,
                 "no ecg.ingested delivery within 60s"):
        return
    parsed = json.loads(got["body"])
    if not check("event is ecg.ingested", parsed.get("event") == "ecg.ingested",
                 f"got {parsed.get('event')} — the file was already ingested?"):
        return
    check("payload carries ecg_id / patient_id / vendor",
          all(parsed.get("data", {}).get(k) for k in ("ecg_id", "patient_id", "vendor")),
          str(parsed.get("data")))
    check("payload carries pull-back links",
          bool(parsed.get("links", {}).get("ecg_metadata")), str(parsed.get("links")))

    expected = "sha256=" + hmac.new(secret.encode(), got["body"], hashlib.sha256).hexdigest()
    check("ingest delivery is signed",
          hmac.compare_digest(got["headers"].get("x-ecg-hub-signature", ""), expected))

    # The link in the payload must be usable with the same API key.
    link = parsed.get("links", {}).get("ecg_metadata", "")
    if "/api/v1" in link:
        status, _, _ = call("GET", link[link.index("/api/v1"):], key=ARGS.api_key)
        check("ecg_metadata link is fetchable with the API key", status == 200, f"got {status}")

    status, deliveries, _ = call("GET", f"/api/v1/webhooks/{hook['id']}/deliveries",
                                 key=ARGS.api_key)
    check("delivery history records the ingest event",
          status == 200 and len(deliveries) >= 1 and deliveries[0]["event"] == "ecg.ingested",
          f"got {status} {deliveries}")
    if not deliveries:
        return
    delivery_id = deliveries[0]["id"]

    drain()
    status, payload, _ = call(
        "POST", f"/api/v1/webhooks/{hook['id']}/deliveries/{delivery_id}/resend",
        key=ARGS.api_key)
    check("resend → 200 ok", status == 200 and payload.get("ok") is True,
          f"got {status} {payload}")
    replay = wait_delivery(15, path="/hook", webhook_id=hook["id"])
    check("resend replays the exact stored payload",
          replay is not None and json.loads(replay["body"]) == parsed,
          "payload differs from the original" if replay else "no delivery")

    status, after, _ = call("GET", f"/api/v1/webhooks/{hook['id']}/deliveries",
                            key=ARGS.api_key)
    check("resend is logged as a new delivery", len(after) == len(deliveries) + 1,
          f"before={len(deliveries)} after={len(after)}")

    status, _, _ = call(
        "POST", f"/api/v1/webhooks/{hook['id']}/deliveries/{uuid.uuid4()}/resend",
        key=ARGS.api_key)
    check("resend of an unknown delivery → 404", status == 404, f"got {status}")

    # A delivery belongs to one webhook: replaying it through another must fail.
    _, second = create_hook(name="ingest-loop-second")
    status, _, _ = call(
        "POST", f"/api/v1/webhooks/{second['id']}/deliveries/{delivery_id}/resend",
        key=ARGS.api_key)
    check("resend through a different webhook → 404", status == 404, f"got {status}")


def test_deletion():
    section("Deletion")
    status, hook = create_hook(name="to-delete")
    if status != 201:
        return
    status, _, _ = call("DELETE", f"/api/v1/webhooks/{hook['id']}", key=ARGS.api_key)
    check("delete → 204", status == 204, f"got {status}")
    CREATED.remove(hook["id"])
    status, _, _ = call("DELETE", f"/api/v1/webhooks/{hook['id']}", key=ARGS.api_key)
    check("delete again → 404", status == 404, f"got {status}")
    status, _, _ = call("POST", f"/api/v1/webhooks/{hook['id']}/test", key=ARGS.api_key)
    check("test after delete → 404", status == 404, f"got {status}")


def cleanup():
    if ARGS.keep:
        print(f"\n{YELLOW}--keep: {len(CREATED)} webhook(s) left behind{RESET}")
        return
    for hook_id in list(CREATED):
        call("DELETE", f"/api/v1/webhooks/{hook_id}", key=ARGS.api_key)
    print(f"\n{DIM}cleaned up {len(CREATED)} webhook(s){RESET}")


def main():
    global ARGS
    parser = argparse.ArgumentParser(description="Test the ECG Hub webhook REST API")
    parser.add_argument("--base-url", default="http://localhost")
    parser.add_argument("--api-key", help="ecghub_… key of a user with webhook.manage")
    parser.add_argument("--username", help="mint a key instead of passing one")
    parser.add_argument("--password")
    parser.add_argument("--other-api-key",
                        help="key of a DIFFERENT user, enables the isolation tests")
    parser.add_argument("--receiver-host", default="host.docker.internal",
                        help="how the backend reaches this machine (default: %(default)s)")
    parser.add_argument("--receiver-port", type=int, default=9998)
    parser.add_argument("--ingest-file",
                        help="ECG file to upload for the full ingest→webhook loop "
                             "(each run ingests one new ECG into the database)")
    parser.add_argument("--keep", action="store_true",
                        help="do not delete the webhooks created by the run")
    ARGS = parser.parse_args()

    if not ARGS.api_key:
        if not (ARGS.username and ARGS.password):
            parser.error("pass --api-key, or --username and --password")
        ARGS.api_key = mint_api_key(ARGS.username, ARGS.password)
        print(f"{DIM}minted API key {ARGS.api_key[:20]}…{RESET}")

    start_receiver(ARGS.receiver_port)
    print(f"{DIM}receiver on 0.0.0.0:{ARGS.receiver_port}, "
          f"advertised as {hook_url()}{RESET}")

    try:
        test_authentication()
        test_options()
        test_validation()
        test_crud()
        test_delivery()
        test_failure_paths()
        test_isolation()
        test_ingest_loop()
        test_deletion()
    finally:
        cleanup()

    failed = [r for r in RESULTS if not r[0]]
    print(f"\n{'─' * 60}")
    print(f"{len(RESULTS) - len(failed)}/{len(RESULTS)} checks passed")
    if failed:
        print(f"{RED}failures:{RESET}")
        for _, name, detail in failed:
            print(f"  • {name}" + (f" — {detail}" if detail else ""))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
