"""Plugin: register contributions, then serve the protocol."""

from __future__ import annotations

import contextlib
import hmac
import json
import logging
import os
import re
import signal
import socket
import socketserver
import sys
import threading
import time
import traceback
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any, Callable, Dict, Iterator, List, Optional, Tuple

from . import protocol as p
from .host import Host
from .protocol import ErrorCode, PluginError
from .types import (
    ConnectorConfig,
    Cursor,
    FetchInput,
    FetchedItem,
    ParseInput,
    ParseOutput,
    SearchInput,
    SearchResult,
    UIRequest,
    UIResponse,
    from_wire,
    parse_time,
    to_wire,
)


class Call:
    """One request's caller and configuration. Configuration secrets are
    already decrypted; do not keep them beyond the call."""

    def __init__(self, envelope: dict) -> None:
        ctx = envelope.get("context") or {}
        cfg = envelope.get("config") or {}
        self.tenant_id: int = int(ctx.get("tenantId") or 0)
        self.user_id: str = ctx.get("userId") or ""
        self.locale: str = ctx.get("locale") or ""
        self.request_id: str = ctx.get("requestId") or ""
        #: When WeKnora stops waiting; give up by then.
        self.deadline: Optional[datetime] = parse_time(ctx.get("deadline"))
        self._host = ctx.get("host") or {}
        #: Platform-wide configuration (config.system).
        self.system: Dict[str, Any] = cfg.get("system") or {}
        #: Workspace configuration (config.tenant).
        self.tenant: Dict[str, Any] = cfg.get("tenant") or {}
        #: The integration instance's configuration, e.g. a data source's.
        self.instance: Dict[str, Any] = cfg.get("instance") or {}

    def time_left(self) -> Optional[float]:
        """Seconds until the deadline, or None without one."""
        if self.deadline is None:
            return None
        return (self.deadline - datetime.now(timezone.utc)).total_seconds()

    def host(self) -> Optional[Host]:
        """The Host API client of this call, or None when the plugin was
        granted no Host API scopes."""
        if not self._host.get("url") or not self._host.get("token"):
            return None
        return Host(self._host["url"], self._host["token"])


class StreamClosed(Exception):
    """WeKnora stopped listening to a stream: stop fetching and return."""


class Stream:
    """A streaming answer: items, checkpoints and progress as NDJSON lines,
    each sent at once. Safe to use from several threads."""

    def __init__(self, handler: "_Handler") -> None:
        self._h = handler
        self._lock = threading.Lock()
        self._started = False
        self._closed = False

    def _write(self, event: dict) -> None:
        with self._lock:
            if self._closed:
                raise StreamClosed("stream already ended")
            try:
                if not self._started:
                    self._h.send_response(200)
                    self._h.send_header("Content-Type", p.NDJSON_CONTENT_TYPE)
                    self._h.send_header("Transfer-Encoding", "chunked")
                    self._h.end_headers()
                    self._started = True
                line = json.dumps(event, separators=(",", ":")).encode() + b"\n"
                self._h.wfile.write(b"%x\r\n%s\r\n" % (len(line), line))
                self._h.wfile.flush()
            except OSError as e:
                self._closed = True
                raise StreamClosed(str(e)) from None

    def item(self, item: FetchedItem) -> None:
        """Emits one fetched item."""
        self._write({"type": "item", "data": to_wire(item)})

    def checkpoint(self, cursor: Cursor) -> None:
        """Emits a resumable cursor; it must be a complete snapshot."""
        self._write({"type": "checkpoint", "data": to_wire(cursor)})

    def progress(self, message: str) -> None:
        """Reports progress for display."""
        self._write({"type": "progress", "message": message})

    def log(self, level: str, message: str) -> None:
        """Forwards a line to WeKnora's logs: debug, info, warn or error."""
        self._write({"type": "log", "level": level, "message": message})

    def _finish(self) -> None:
        try:
            self._h.wfile.write(b"0\r\n\r\n")
            self._h.wfile.flush()
        except OSError:
            pass
        self._closed = True

    def _end(self, cursor: Optional[Cursor]) -> None:
        try:
            self._write({"type": "end", "data": to_wire(cursor or Cursor())})
        except StreamClosed:
            return
        with self._lock:
            self._finish()

    def _fail(self, err: PluginError) -> None:
        with self._lock:
            if self._closed:
                return
            started = self._started
        if not started:
            self._h._send_error(err)
            self._closed = True
            return
        try:
            self._write({"type": "error", "error": err.to_wire()})
        except StreamClosed:
            return
        with self._lock:
            self._finish()


WebSearchFunc = Callable[[Call, SearchInput], List[SearchResult]]
ParserFunc = Callable[[Call, ParseInput], ParseOutput]
ConfigValidator = Callable[[Call], None]
UIHandler = Callable[[Call, UIRequest], Any]

_ROUTE = re.compile(r"^/v1/(websearch|connectors|parsers)/([^/]+)/([a-z-]+)$")


class Plugin:
    """A plugin under construction: register contributions, then serve.

    ``id`` and ``version`` must match plugin.yaml."""

    def __init__(self, id: str, version: str, logger: Optional[logging.Logger] = None) -> None:
        self.id = id
        self.version = version
        self._web_search: Dict[str, WebSearchFunc] = {}
        self._connectors: Dict[str, Any] = {}
        self._parsers: Dict[str, ParserFunc] = {}
        self._validate: Optional[ConfigValidator] = None
        self._ui: Optional[UIHandler] = None
        #: Seconds serve() waits for calls in flight after SIGTERM.
        self.shutdown_timeout = 60.0
        if logger is None:
            logger = logging.getLogger("weknora_plugin")
            if not logger.handlers:
                h = logging.StreamHandler(sys.stderr)
                h.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
                logger.addHandler(h)
                logger.setLevel(logging.INFO)
        #: Writes to stderr, which the plugin host forwards to WeKnora's
        #: logs. Keep stdout for the handshake.
        self.logger = logger

    # Registration. Each works as a call or as a decorator.

    def web_search(self, id: str, fn: Optional[WebSearchFunc] = None) -> Any:
        """Registers web search provider id: fn(call, SearchInput) returns
        a list of SearchResult."""
        return self._register(self._web_search, id, fn)

    def parser(self, id: str, fn: Optional[ParserFunc] = None) -> Any:
        """Registers parser id: fn(call, ParseInput) returns a ParseOutput
        (or the Markdown as a str)."""
        return self._register(self._parsers, id, fn)

    def connector(self, id: str, connector: Any = None) -> Any:
        """Registers connector id: an object with validate(call, cfg),
        list_resources(call, cfg, parent_id) and fetch(call, cfg, input,
        stream), and optionally resolve_ancestors(call, cfg, resource_ids).
        As a decorator on a class it registers an instance."""
        if connector is not None:
            self._connectors[id] = connector() if isinstance(connector, type) else connector
            return connector

        def register(c: Any) -> Any:
            self._connectors[id] = c() if isinstance(c, type) else c
            return c

        return register

    def ui(self, fn: UIHandler) -> UIHandler:
        """Registers the handler behind the plugin's pages: fn(call,
        UIRequest) returns a UIResponse, or any JSON value for a 200."""
        self._ui = fn
        return fn

    def config_validator(self, fn: ConfigValidator) -> ConfigValidator:
        """Registers the check behind POST /v1/config/validate: raise
        invalid_config(...) to point at fields."""
        self._validate = fn
        return fn

    @staticmethod
    def _register(table: dict, id: str, fn: Any) -> Any:
        if fn is not None:
            table[id] = fn
            return fn

        def register(f: Any) -> Any:
            table[id] = f
            return f

        return register

    def manifest(self) -> dict:
        """What the plugin reports at GET /v1/manifest."""
        contributes = {}
        for point, table in (
            ("webSearch", self._web_search),
            ("connectors", self._connectors),
            ("parsers", self._parsers),
        ):
            if table:
                contributes[point] = sorted(table)
        if self._ui is not None:
            contributes["ui"] = ["request"]
        return {"id": self.id, "version": self.version, "apiVersion": p.API_VERSION, "contributes": contributes}

    # Dispatch.

    def _dispatch(self, h: "_Handler", method: str, path: str, body: bytes) -> None:
        if method == "GET" and path == "/v1/manifest":
            h._send_json(200, self.manifest())
            return
        if method == "GET" and path == "/v1/health":
            h._send_json(200, {"status": "ok"})
            return
        if method == "POST" and path == "/v1/config/validate":
            self._unary(h, body, lambda call, _: self._validate(call) if self._validate else None, empty=True)
            return
        if method == "POST" and path == "/v1/ui/request":
            if self._ui is None:
                h._send_error(PluginError(ErrorCode.NOT_FOUND, "this plugin's pages make no requests"))
            else:
                self._unary(h, body, lambda call, raw: _ui_output(self._ui(call, from_wire(UIRequest, raw))))
            return
        m = _ROUTE.match(path)
        if method == "POST" and m:
            point, cid, action = m.groups()
            if self._route(h, point, cid, action, body):
                return
        h._send_error(PluginError(ErrorCode.NOT_FOUND, f"no endpoint {method} {path}"))

    def _route(self, h: "_Handler", point: str, cid: str, action: str, body: bytes) -> bool:
        if point == "websearch" and action == "search":
            fn = self._web_search.get(cid)
            if fn is None:
                h._send_error(PluginError(ErrorCode.NOT_FOUND, f"no web search provider {cid!r}"))
            else:
                self._unary(h, body, lambda call, raw: {"results": list(fn(call, from_wire(SearchInput, raw)) or [])})
            return True
        if point == "parsers" and action == "parse":
            fn = self._parsers.get(cid)
            if fn is None:
                h._send_error(PluginError(ErrorCode.NOT_FOUND, f"no parser {cid!r}"))
            else:
                self._unary(h, body, lambda call, raw: _parse_output(fn(call, from_wire(ParseInput, raw))))
            return True
        if point == "connectors" and action in ("validate", "list-resources", "resolve-ancestors", "fetch"):
            c = self._connectors.get(cid)
            if c is None:
                h._send_error(PluginError(ErrorCode.NOT_FOUND, f"no connector {cid!r}"))
            elif action == "fetch":
                self._fetch(h, c, body)
            else:
                self._unary(h, body, lambda call, raw: _connector_call(c, action, call, raw), empty=action == "validate")
            return True
        return False

    def _unary(self, h: "_Handler", body: bytes, fn: Callable[[Call, Any], Any], empty: bool = False) -> None:
        try:
            env = _envelope(body)
            out = fn(Call(env), env.get("input"))
            h._send_json(200, {"output": {} if empty else to_wire(out)})
        except Exception as e:  # noqa: BLE001 - every failure becomes a protocol error
            h._send_error(self._as_error(e))

    def _fetch(self, h: "_Handler", c: Any, body: bytes) -> None:
        try:
            env = _envelope(body)
            call = Call(env)
            cfg = _connector_config(call)
            inp = from_wire(FetchInput, env.get("input"))
        except Exception as e:  # noqa: BLE001
            h._send_error(self._as_error(e))
            return
        s = Stream(h)
        try:
            cursor = c.fetch(call, cfg, inp, s)
        except StreamClosed:
            return
        except Exception as e:  # noqa: BLE001
            s._fail(self._as_error(e))
            return
        s._end(cursor)

    def _as_error(self, e: Exception) -> PluginError:
        if isinstance(e, PluginError):
            return e
        self.logger.error("plugin call failed: %s", "".join(traceback.format_exception(type(e), e, e.__traceback__)))
        return PluginError(ErrorCode.INTERNAL, str(e) or type(e).__name__)

    # Serving.

    def serve(self) -> None:
        """Runs the plugin until SIGTERM or SIGINT, then drains calls in
        flight. Started by the plugin host it listens where the host says
        and prints the handshake; otherwise it serves as a remote plugin on
        WEKNORA_PLUGIN_ADDR, checking signatures with WEKNORA_PLUGIN_SECRET."""
        server, handshake = self._listen()
        stop = threading.Event()

        def on_signal(*_: Any) -> None:
            stop.set()

        for sig in (signal.SIGTERM, signal.SIGINT):
            try:
                signal.signal(sig, on_signal)
            except ValueError:  # not the main thread
                pass
        t = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.2}, daemon=True)
        t.start()
        if handshake:
            # The host waits for this exact line; everything else on stdout
            # is treated as log output.
            print(handshake, flush=True)
        else:
            self.logger.info("plugin serving on %s", _describe(server))
        stop.wait()
        server.shutdown()
        server.drain(self.shutdown_timeout)
        server.server_close()

    def _listen(self) -> Tuple["_Server", Optional[str]]:
        sock_path = os.environ.get(p.ENV_SOCKET, "")
        if sock_path:
            network = os.environ.get(p.ENV_NETWORK) or "unix"
            token = os.environ.get(p.ENV_TOKEN, "")
            if not token:
                raise RuntimeError(f"{p.ENV_SOCKET} is set without {p.ENV_TOKEN}")
            auth = _bearer(token)
            if network == "unix":
                try:
                    os.remove(sock_path)
                except FileNotFoundError:
                    pass
                server: _Server = _UnixServer(sock_path, self, auth)
                return server, p.handshake_line("unix", sock_path)
            server = _TCPServer(_split_addr(sock_path), self, auth)
            host, port = server.server_address[:2]
            return server, p.handshake_line("tcp", f"{host}:{port}")
        secret = os.environ.get(p.ENV_SECRET, "")
        if not secret:
            raise RuntimeError(f"a remote plugin needs {p.ENV_SECRET} (the secret shown when it was registered)")
        addr = os.environ.get(p.ENV_ADDR) or ":8080"
        return _TCPServer(_split_addr(addr), self, _signed(secret.encode())), None

    def test_server(self, auth: Optional[Callable[["_Handler", bytes], Optional[str]]] = None) -> "_Server":
        """Serves the plugin on a free loopback port without authentication
        (or with the given check), for tests. Call shutdown() when done."""
        server = _TCPServer(("127.0.0.1", 0), self, auth or (lambda h, b: None))
        threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.1}, daemon=True).start()
        return server


def _envelope(body: bytes) -> dict:
    try:
        env = json.loads(body or b"{}")
    except ValueError as e:
        raise PluginError(ErrorCode.BAD_REQUEST, f"decode envelope: {e}") from None
    if not isinstance(env, dict):
        raise PluginError(ErrorCode.BAD_REQUEST, "decode envelope: not an object")
    return env


def _connector_config(call: Call) -> ConnectorConfig:
    try:
        return from_wire(ConnectorConfig, call.instance)
    except (TypeError, ValueError) as e:
        raise PluginError(ErrorCode.BAD_REQUEST, f"decode connector config: {e}") from None


def _connector_call(c: Any, action: str, call: Call, raw: Any) -> Any:
    cfg = _connector_config(call)
    raw = raw or {}
    if action == "validate":
        c.validate(call, cfg)
        return None
    if action == "list-resources":
        return {"resources": list(c.list_resources(call, cfg, raw.get("parentId") or "") or [])}
    resolve = getattr(c, "resolve_ancestors", None)
    if resolve is None:
        return {"ancestors": []}
    return {"ancestors": list(resolve(call, cfg, list(raw.get("resourceIds") or [])) or [])}


def _ui_output(out: Any) -> Any:
    if not isinstance(out, UIResponse):
        out = UIResponse(body=out)
    wire: dict = {"status": out.status or 200}
    if out.body is not None:
        wire["body"] = to_wire(out.body)
    return wire


def _parse_output(out: Any) -> Any:
    if isinstance(out, str):
        return ParseOutput(markdown=out)
    return out


# Authentication: a check returns an error message, or None to let the
# request through.


def _bearer(token: str) -> Callable[["_Handler", bytes], Optional[str]]:
    want = "Bearer " + token

    def check(h: "_Handler", _: bytes) -> Optional[str]:
        if hmac.compare_digest(h.headers.get("Authorization", ""), want):
            return None
        return "missing or wrong host token"

    return check


def _signed(secret: bytes) -> Callable[["_Handler", bytes], Optional[str]]:
    def check(h: "_Handler", body: bytes) -> Optional[str]:
        try:
            p.verify_signature(
                secret, h.headers.get(p.TIMESTAMP_HEADER, ""), h.headers.get(p.SIGNATURE_HEADER, ""), body
            )
        except ValueError as e:
            return str(e)
        return None

    return check


class _Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server: "_Server"

    def _handle(self) -> None:
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length > 0 else b""
        path = self.path.split("?", 1)[0]
        problem = self.server.auth(self, body)
        if problem is not None:
            self._send_error(PluginError(ErrorCode.UNAUTHORIZED, problem))
            return
        with self.server.in_flight():
            self.server.plugin._dispatch(self, self.command, path, body)

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = _handle

    def _send_json(self, status: int, value: Any) -> None:
        raw = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _send_error(self, err: PluginError) -> None:
        self._send_json(err.code.http_status, {"error": err.to_wire()})

    def address_string(self) -> str:
        return str(self.client_address[0]) if self.client_address else "host"

    def log_message(self, format: str, *args: Any) -> None:
        pass


class _Server(socketserver.ThreadingMixIn, HTTPServer):
    daemon_threads = True

    def __init__(self, address: Any, plugin: Plugin, auth: Callable[[_Handler, bytes], Optional[str]]) -> None:
        self.plugin = plugin
        self.auth = auth
        self._active = 0
        self._active_lock = threading.Condition()
        super().__init__(address, _Handler)

    @contextlib.contextmanager
    def in_flight(self) -> Iterator[None]:
        """Counts a call being answered; idle keep-alive connections do not
        hold up a shutdown."""
        with self._active_lock:
            self._active += 1
        try:
            yield
        finally:
            with self._active_lock:
                self._active -= 1
                self._active_lock.notify_all()

    def handle_error(self, request: Any, client_address: Any) -> None:
        # A caller that went away mid-answer is routine.
        self.plugin.logger.debug("connection error", exc_info=True)

    def drain(self, timeout: float) -> None:
        """Waits for calls in flight, up to timeout seconds."""
        end = time.monotonic() + timeout
        with self._active_lock:
            while self._active and time.monotonic() < end:
                self._active_lock.wait(end - time.monotonic())


class _TCPServer(_Server):
    address_family = socket.AF_INET

    def __init__(self, address: Tuple[str, int], *args: Any) -> None:
        if ":" in address[0]:
            self.address_family = socket.AF_INET6
        super().__init__(address, *args)

    def server_bind(self) -> None:
        # HTTPServer.server_bind looks the host up with getfqdn, a reverse
        # DNS query that can stall startup for seconds; nothing needs it.
        socketserver.TCPServer.server_bind(self)
        self.server_name, self.server_port = str(self.server_address[0]), int(self.server_address[1])


class _UnixServer(_Server):
    address_family = getattr(socket, "AF_UNIX", socket.AF_INET)

    def server_bind(self) -> None:
        socketserver.TCPServer.server_bind(self)
        self.server_name, self.server_port = "plugin", 0

    def server_close(self) -> None:
        super().server_close()
        try:
            os.remove(self.server_address)
        except (OSError, TypeError):
            pass


def _split_addr(addr: str) -> Tuple[str, int]:
    host, _, port = addr.rpartition(":")
    host = host.strip("[]")
    return (host or "0.0.0.0", int(port))


def _describe(server: _Server) -> str:
    addr = server.server_address
    return f"{addr[0]}:{addr[1]}" if isinstance(addr, tuple) else str(addr)
