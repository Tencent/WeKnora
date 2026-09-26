from __future__ import annotations

import base64
import http.client
import json
import os
import socket
import subprocess
import sys
import threading
import time
import unittest
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from fixture import plugin  # noqa: E402
from weknora_plugin import ErrorCode, Host, PluginError  # noqa: E402
from weknora_plugin import protocol as p  # noqa: E402
from weknora_plugin.types import Cursor, FetchInput, from_wire, parse_time, to_wire  # noqa: E402

ENVELOPE = {"context": {"tenantId": 7, "locale": "zh-CN", "requestId": "r1"}}


class Client:
    def __init__(self, conn_factory):
        self.conn_factory = conn_factory

    def request(self, method, path, body=None, headers=None):
        conn = self.conn_factory()
        raw = b"" if body is None else (body if isinstance(body, bytes) else json.dumps(body).encode())
        conn.request(method, path, body=raw if method != "GET" else None, headers=headers or {})
        resp = conn.getresponse()
        data = resp.read()
        conn.close()
        return resp.status, resp.getheader("Content-Type"), data

    def call(self, path, input=None, config=None, **kw):
        env = dict(ENVELOPE)
        if input is not None:
            env["input"] = input
        if config is not None:
            env["config"] = config
        status, _, data = self.request("POST", path, env, **kw)
        return status, json.loads(data)

    def stream(self, path, input=None, config=None):
        env = dict(ENVELOPE, input=input or {}, config=config or {})
        status, ctype, data = self.request("POST", path, env)
        if ctype != p.NDJSON_CONTENT_TYPE:
            return status, json.loads(data)
        return status, [json.loads(line) for line in data.splitlines() if line]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = plugin.test_server()
        host, port = cls.server.server_address[:2]
        cls.c = Client(lambda: http.client.HTTPConnection(host, port, timeout=10))

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()

    def test_manifest_and_health(self):
        status, _, data = self.c.request("GET", "/v1/manifest")
        self.assertEqual(status, 200)
        self.assertEqual(
            json.loads(data),
            {
                "id": "acme.fixture",
                "version": "1.0.0",
                "apiVersion": "weknora.plugin/v1",
                "contributes": {
                    "webSearch": ["echo"],
                    "connectors": ["notes"],
                    "parsers": ["upper"],
                    "chunkers": ["paragraphs"],
                    "webhooks": ["inbox"],
                    "options": ["projects"],
                    "mcpServers": ["issues"],
                    "ui": ["request"],
                    "events": ["handler"],
                },
            },
        )
        self.assertEqual(json.loads(self.c.request("GET", "/v1/health")[2]), {"status": "ok"})

    def test_web_search(self):
        status, body = self.c.call("/v1/websearch/echo/search", {"query": "hi", "maxResults": 3})
        self.assertEqual(status, 200)
        self.assertEqual(
            body["output"]["results"],
            [{"title": "hi", "url": "https://example.com/?tenant=7", "snippet": "zh-CN"}],
        )

    def test_errors(self):
        status, body = self.c.call("/v1/websearch/echo/search", {"query": "down"})
        self.assertEqual((status, body["error"]), (503, {"code": "unavailable", "message": "upstream is down", "retryable": True}))
        status, body = self.c.call("/v1/websearch/echo/search", {"query": "crash"})
        self.assertEqual((status, body["error"]["code"], body["error"]["message"]), (500, "internal", "boom"))
        status, body = self.c.call("/v1/websearch/nope/search", {"query": "x"})
        self.assertEqual((status, body["error"]["code"]), (404, "not_found"))
        status, body = self.c.call("/v1/no-such-endpoint")
        self.assertEqual((status, body["error"]["code"]), (404, "not_found"))
        status, _, data = self.c.request("POST", "/v1/config/validate", b"{not json")
        self.assertEqual((status, json.loads(data)["error"]["code"]), (400, "bad_request"))

    def test_config_validate(self):
        status, body = self.c.call("/v1/config/validate", config={"tenant": {"region": "eu"}})
        self.assertEqual((status, body), (200, {"output": {}}))
        status, body = self.c.call("/v1/config/validate", config={"tenant": {"region": "mars"}})
        self.assertEqual(status, 400)
        self.assertEqual(body["error"]["details"], {"fields": {"region": "no such region"}})

    def test_parser(self):
        doc = base64.b64encode(b"hello").decode()
        status, body = self.c.call("/v1/parsers/upper/parse", {"fileName": "a.txt", "fileType": "txt", "content": doc})
        self.assertEqual(status, 200)
        out = body["output"]
        self.assertTrue(out["markdown"].startswith("HELLO"))
        self.assertEqual(out["images"], [{"originalRef": "img/dot.png", "data": base64.b64encode(b"\x89PNG").decode(), "mimeType": "image/png"}])
        self.assertEqual(out["metadata"], {"fileType": "txt"})

    def test_chunker(self):
        status, body = self.c.call("/v1/chunkers/paragraphs/split", {"text": "第一段\n\nsecond", "chunkSize": 300})
        self.assertEqual(status, 200)
        self.assertEqual(
            body["output"]["chunks"],
            [{"start": 0, "end": 3, "contextHeader": "size 300"}, {"start": 5, "end": 11, "contextHeader": "size 300"}],
        )
        status, body = self.c.call("/v1/chunkers/paragraphs/split", {"text": "out of range"})
        self.assertEqual(body["error"]["code"], "internal")

    def test_events(self):
        from fixture import seen_events

        ev = {"id": "e1", "type": "knowledge.ingested", "occurredAt": "2026-09-26T10:51:18.5Z", "attempt": 2, "data": {"knowledgeId": "k1"}}
        self.assertEqual(self.c.call("/v1/events", ev), (200, {"output": {}}))
        self.assertIn(("e1", "knowledge.ingested", 2, 7, "k1"), seen_events)
        status, body = self.c.call("/v1/events", dict(ev, type="knowledge.failed"))
        self.assertEqual((status, body["error"]["retryable"]), (503, True))

    def test_options(self):
        inp = {"field": "project", "scope": "tenant", "query": "x"}
        status, body = self.c.call("/v1/options/projects", inp)
        self.assertEqual((status, body["error"]["code"]), (400, "invalid_config"))
        status, body = self.c.call("/v1/options/projects", inp, {"tenant": {"token": "t"}})
        self.assertEqual(body["output"]["options"], [{"value": "p1", "label": "Project x"}, {"value": "p2", "label": "Other"}])

    def test_tools(self):
        def send(msg, config=None):
            status, body = self.c.call("/v1/mcp/issues", dict(msg, jsonrpc="2.0"), config)
            self.assertEqual(status, 200)
            return body["output"]

        init = send({"id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18"}})
        self.assertEqual((init["id"], init["result"]["capabilities"]), (1, {"tools": {}}))
        tools = send({"id": 2, "method": "tools/list"})["result"]["tools"]
        self.assertEqual([t["name"] for t in tools], ["search"])
        self.assertEqual(tools[0]["annotations"], {"readOnlyHint": True})
        out = send({"id": 3, "method": "tools/call", "params": {"name": "search", "arguments": {"q": "login"}}})
        self.assertEqual(out["result"]["structuredContent"], {"q": "login", "tenant": 7})
        self.assertEqual(json.loads(out["result"]["content"][0]["text"]), {"q": "login", "tenant": 7})
        out = send({"id": 4, "method": "tools/call", "params": {"name": "search", "arguments": {"q": "text"}}})
        self.assertEqual(out["result"], {"content": [{"type": "text", "text": "plain"}]})
        out = send({"id": 5, "method": "tools/call", "params": {"name": "search", "arguments": {"q": "boom"}}})
        self.assertEqual(out["result"], {"content": [{"type": "text", "text": "token expired"}], "isError": True})
        self.assertEqual(send({"id": 6, "method": "tools/call", "params": {"name": "nope"}})["error"]["code"], -32602)
        self.assertEqual(send({"id": 7, "method": "resources/list"})["result"], {"resources": []})
        self.assertEqual(send({"id": 8, "method": "completion/complete"})["error"]["code"], -32601)
        self.assertNotIn("error", send({"method": "notifications/initialized"}))
        self.assertEqual(self.c.call("/v1/mcp/nope", {"jsonrpc": "2.0", "method": "ping"})[0], 404)

    def test_webhooks(self):
        body = base64.b64encode(b'{"a": 1}').decode()
        req = {"method": "POST", "path": "/issue", "headers": {"X-Signature": "ok"}, "body": body}
        status, out = self.c.call("/v1/webhooks/inbox", req)
        self.assertEqual(status, 200)
        self.assertEqual(out["output"]["contentType"], "application/json")
        self.assertEqual(json.loads(base64.b64decode(out["output"]["body"])), {"path": "/issue", "got": {"a": 1}, "tenant": 7})
        status, out = self.c.call("/v1/webhooks/inbox", dict(req, headers={}))
        self.assertEqual((out["output"]["status"], base64.b64decode(out["output"]["body"])), (401, b"bad signature"))
        self.assertEqual(self.c.call("/v1/webhooks/nope", req)[0], 404)

    def test_ui_requests(self):
        req = {"mount": "pages/links", "method": "PUT", "path": "/links", "body": {"a": 1}, "role": "admin"}
        status, body = self.c.call("/v1/ui/request", req)
        self.assertEqual(status, 200)
        self.assertEqual(body["output"], {"status": 200, "body": {**req}})
        status, body = self.c.call("/v1/ui/request", dict(req, path="/missing"))
        self.assertEqual(body["output"], {"status": 404, "body": {"error": "no such thing"}})

    def test_connector_unary(self):
        cfg = {"instance": {"credentials": {"token": "ok"}, "settings": {}, "resourceIds": []}}
        self.assertEqual(self.c.call("/v1/connectors/notes/validate", config=cfg), (200, {"output": {}}))
        bad = {"instance": {"credentials": {"token": "bad"}}}
        status, body = self.c.call("/v1/connectors/notes/validate", config=bad)
        self.assertEqual((status, body["error"]["code"]), (401, "unauthorized"))
        status, body = self.c.call("/v1/connectors/notes/list-resources", {}, cfg)
        self.assertEqual(body["output"], {"resources": [{"externalId": "inbox", "name": "Inbox", "hasChildren": True}]})
        status, body = self.c.call("/v1/connectors/notes/resolve-ancestors", {"resourceIds": ["x"]}, cfg)
        self.assertEqual(body["output"], {"ancestors": []})

    def test_fetch_streams(self):
        status, events = self.c.stream("/v1/connectors/notes/fetch", {"mode": "incremental", "cursor": {"state": {"after": 2}}})
        self.assertEqual(status, 200)
        self.assertEqual([e["type"] for e in events], ["item", "item", "item", "checkpoint", "progress", "end"])
        self.assertEqual(events[0]["data"]["externalId"], "n2")
        self.assertEqual(base64.b64decode(events[0]["data"]["content"]), b"# Note 2")
        self.assertEqual(events[-1]["data"], {"state": {"after": 5}})

    def test_fetch_failures(self):
        status, body = self.c.stream("/v1/connectors/notes/fetch", config={"instance": {"settings": {"fail": "before"}}})
        self.assertEqual((status, body["error"]["code"]), (401, "unauthorized"))
        status, events = self.c.stream("/v1/connectors/notes/fetch", config={"instance": {"settings": {"fail": "during"}}})
        self.assertEqual([e["type"] for e in events], ["item", "error"])
        self.assertEqual(events[1]["error"], {"code": "rate_limited", "message": "slow down", "retryable": True, "details": {"retryAfter": 7}})


class BindTest(unittest.TestCase):
    def test_no_reverse_dns_on_bind(self):
        # getfqdn can stall startup for seconds where reverse DNS is slow.
        from unittest import mock

        with mock.patch("socket.getfqdn", side_effect=AssertionError("getfqdn called")):
            server = plugin.test_server()
        server.shutdown()
        server.server_close()


class WireTest(unittest.TestCase):
    def test_times(self):
        t = parse_time("2026-09-25T16:22:58.135616789Z")
        self.assertEqual(t, datetime(2026, 9, 25, 16, 22, 58, 135616, tzinfo=timezone.utc))
        self.assertEqual(to_wire(Cursor(last_sync_time=t)), {"lastSyncTime": "2026-09-25T16:22:58.135616Z"})
        # Go trims trailing zeros, so any number of digits arrives.
        for raw, micros in (("2026-09-26T10:51:18.63664+08:00", 636640), ("2026-09-26T10:51:18.5Z", 500000)):
            self.assertEqual(parse_time(raw).microsecond, micros)
        naive = datetime(2026, 1, 2, 3, 4, 5)
        self.assertEqual(to_wire(naive), "2026-01-02T03:04:05Z")

    def test_from_wire(self):
        inp = from_wire(FetchInput, {"mode": "incremental", "cursor": {"lastSyncTime": "2026-01-02T03:04:05Z", "state": {"a": 1}}, "unknown": 1})
        self.assertEqual(inp.cursor.state, {"a": 1})
        self.assertEqual(inp.cursor.last_sync_time.year, 2026)
        self.assertEqual(inp.resource_ids, [])


class SignatureTest(unittest.TestCase):
    def test_sign_and_verify(self):
        now = 1_700_000_000
        sig = p.sign(b"s3cret", now, b"{}")
        p.verify_signature(b"s3cret", str(now), sig, b"{}", now=now)
        with self.assertRaisesRegex(ValueError, "bad signature"):
            p.verify_signature(b"other", str(now), sig, b"{}", now=now)
        with self.assertRaisesRegex(ValueError, "clock skew"):
            p.verify_signature(b"s3cret", str(now), sig, b"{}", now=now + 600)

    def test_matches_go(self):
        # pluginapi.Sign([]byte("k"), 1, []byte("b")) in Go.
        import hashlib
        import hmac

        self.assertEqual(p.sign(b"k", 1, b"b"), hmac.new(b"k", b"1.b", hashlib.sha256).hexdigest())


class FakeHostAPI(BaseHTTPRequestHandler):
    store: dict = {}

    def _reply(self, status, body=None):
        raw = b"" if body is None else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _authorized(self):
        if self.headers.get("Authorization") != "Bearer tok":
            self._reply(401, {"error": {"code": "unauthorized", "message": "bad token"}})
            return False
        return True

    def do_GET(self):
        if not self._authorized():
            return
        u = urlparse(self.path)
        q = {k: v[0] for k, v in parse_qs(u.query).items()}
        if u.path == p.HOST_KV_LIST_PATH:
            keys = sorted(k for k in self.store if k.startswith(q.get("prefix", "")) and k > q.get("after", ""))
            page = keys[:2]
            self._reply(200, {"entries": [{"key": k, "value": self.store[k], "updatedAt": "2026-01-01T00:00:00Z"} for k in page], "next": page[-1] if len(keys) > 2 else ""})
        elif q["key"] in self.store:
            self._reply(200, {"key": q["key"], "value": self.store[q["key"]], "updatedAt": "2026-01-01T00:00:00Z"})
        else:
            self._reply(404, {"error": {"code": "not_found", "message": "no key"}})

    def do_PUT(self):
        if not self._authorized():
            return
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.store[body["key"]] = body["value"]
        self._reply(200, {"key": body["key"], "value": body["value"]})

    def do_DELETE(self):
        if not self._authorized():
            return
        self.store.pop(parse_qs(urlparse(self.path).query)["key"][0], None)
        self._reply(204)

    def log_message(self, *args):
        pass


class HostTest(unittest.TestCase):
    def test_kv(self):
        srv = HTTPServer(("127.0.0.1", 0), FakeHostAPI)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        url = f"http://127.0.0.1:{srv.server_address[1]}"
        try:
            h = Host(url, "tok")
            self.assertIsNone(h.kv_get("missing"))
            self.assertEqual(h.kv_get("missing", 5), 5)
            h.kv_put("a", {"n": 1}, ttl=60)
            h.kv_put("b", 2)
            h.kv_put("c", [3])
            self.assertEqual(h.kv_get("a"), {"n": 1})
            self.assertEqual([e.key for e in h.kv_items()], ["a", "b", "c"])
            h.kv_delete("a")
            self.assertIsNone(h.kv_entry("a"))
            with self.assertRaises(PluginError) as ctx:
                Host(url, "wrong").kv_get("b")
            self.assertEqual(ctx.exception.code, ErrorCode.UNAUTHORIZED)
        finally:
            srv.shutdown()
            srv.server_close()
        with self.assertRaises(PluginError) as ctx:
            Host(url, "tok", timeout=2).kv_get("b")
        self.assertEqual(ctx.exception.code, ErrorCode.UNAVAILABLE)


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("plugin", timeout=10)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect(self.path)


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@unittest.skipUnless(hasattr(socket, "AF_UNIX"), "needs unix sockets")
class ServeTest(unittest.TestCase):
    def _start(self, env):
        proc = subprocess.Popen(
            [sys.executable, os.path.join(HERE, "fixture.py")],
            env={**{"PATH": os.environ.get("PATH", "")}, **env},
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        def stop():
            proc.terminate()
            proc.wait(10)
            proc.stdout.close()
            proc.stderr.close()

        self.addCleanup(stop)
        return proc

    def test_host_mode(self):
        import tempfile

        d = tempfile.mkdtemp()
        sock = os.path.join(d, "p.sock")
        proc = self._start({p.ENV_SOCKET: sock, p.ENV_TOKEN: "tok"})
        self.assertEqual(proc.stdout.readline().strip(), f"WEKNORA_PLUGIN|1|unix|{sock}")
        c = Client(lambda: UnixConnection(sock))
        self.assertEqual(c.request("GET", "/v1/health")[0], 401)
        status, _, _ = c.request("GET", "/v1/health", headers={"Authorization": "Bearer tok"})
        self.assertEqual(status, 200)
        proc.terminate()
        self.assertEqual(proc.wait(10), 0)
        self.assertFalse(os.path.exists(sock), "the socket is removed on shutdown")

    def test_remote_mode(self):
        port = _free_port()
        self._start({p.ENV_ADDR: f"127.0.0.1:{port}", p.ENV_SECRET: "s3cret"})
        c = Client(lambda: http.client.HTTPConnection("127.0.0.1", port, timeout=10))
        for _ in range(100):
            try:
                c.request("GET", "/v1/health")
                break
            except OSError:
                time.sleep(0.05)
        body = json.dumps(dict(ENVELOPE, input={"query": "q"})).encode()
        ts = int(time.time())
        status, _, _ = c.request("POST", "/v1/websearch/echo/search", body, {p.TIMESTAMP_HEADER: str(ts), p.SIGNATURE_HEADER: p.sign(b"other", ts, body)})
        self.assertEqual(status, 401)
        status, _, data = c.request("POST", "/v1/websearch/echo/search", body, {p.TIMESTAMP_HEADER: str(ts), p.SIGNATURE_HEADER: p.sign(b"s3cret", ts, body)})
        self.assertEqual(status, 200, data)

    def test_shutdown_drains_calls_in_flight(self):
        port = _free_port()
        proc = self._start({p.ENV_ADDR: f"127.0.0.1:{port}", p.ENV_SECRET: "s3cret"})
        c = Client(lambda: http.client.HTTPConnection("127.0.0.1", port, timeout=10))
        for _ in range(100):
            try:
                c.request("GET", "/v1/health")
                break
            except OSError:
                time.sleep(0.05)
        body = json.dumps(dict(ENVELOPE, input={"query": "slow"})).encode()
        ts = int(time.time())
        headers = {p.TIMESTAMP_HEADER: str(ts), p.SIGNATURE_HEADER: p.sign(b"s3cret", ts, body)}
        result = {}
        t = threading.Thread(target=lambda: result.update(r=c.request("POST", "/v1/websearch/echo/search", body, headers)))
        t.start()
        time.sleep(0.2)
        proc.terminate()
        t.join(10)
        self.assertEqual(result["r"][0], 200)
        self.assertEqual(proc.wait(10), 0)

    def test_needs_a_secret(self):
        proc = self._start({})
        self.assertNotEqual(proc.wait(10), 0)
        self.assertIn(p.ENV_SECRET, proc.stderr.read())


if __name__ == "__main__":
    unittest.main()
