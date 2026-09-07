#!/usr/bin/env python3
"""Minimal WeKnora workspace webhook receiver (stdlib only).

Verifies HMAC-SHA256(secret, timestamp + "." + raw_body) and returns 200.
Do not re-serialize JSON before verifying. Download files asynchronously
using data.download.ticket — never block the request on file I/O.

Usage:
  set WEKNORA_WEBHOOK_SECRET=your-shared-secret
  python scripts/webhook-receiver-example.py

Configure the workspace callback URL to:
  http://127.0.0.1:8088/hooks/weknora
(production must use https://your-receiver.example/hooks/weknora)
"""

from __future__ import annotations

from http.server import BaseHTTPRequestHandler, HTTPServer
import hashlib
import hmac
import os
import time

SECRET = os.environ.get("WEKNORA_WEBHOOK_SECRET", "").encode()
WINDOW = 300
LISTEN = ("127.0.0.1", 8088)


class Handler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        if self.path.rstrip("/") != "/hooks/weknora":
            self.send_error(404)
            return
        if not SECRET:
            self.send_error(500, "WEKNORA_WEBHOOK_SECRET not set")
            return
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        ts = self.headers.get("X-WeKnora-Timestamp", "")
        sig = self.headers.get("X-WeKnora-Signature", "")
        try:
            unix = int(ts)
        except ValueError:
            self.send_error(401)
            return
        if abs(time.time() - unix) > WINDOW:
            self.send_error(401)
            return
        expected = "sha256=" + hmac.new(
            SECRET, f"{unix}.".encode() + raw, hashlib.sha256
        ).hexdigest()
        if len(sig) != len(expected) or not hmac.compare_digest(sig, expected):
            self.send_error(401)
            return
        # Enqueue for async processing; do not download files here.
        event_type = self.headers.get("X-WeKnora-Event", "")
        delivery = self.headers.get("X-WeKnora-Delivery", "")
        print(f"ok type={event_type} delivery={delivery} bytes={len(raw)}")
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, fmt: str, *args) -> None:
        return


if __name__ == "__main__":
    if not SECRET:
        raise SystemExit("set WEKNORA_WEBHOOK_SECRET before starting")
    print(f"listening on http://{LISTEN[0]}:{LISTEN[1]}/hooks/weknora")
    HTTPServer(LISTEN, Handler).serve_forever()
