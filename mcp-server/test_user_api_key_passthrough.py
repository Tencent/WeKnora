#!/usr/bin/env python3
"""Per-user API key passthrough tests.

Covers the MCPAuthMiddleware decision tree (credential carriers, strict
mode, malformed rejection), WeKnoraClient outbound header selection, and
contextvars propagation through run_in_executor (the chat/agent_chat path).
"""

import asyncio
import contextvars
import functools
import os
import unittest
from unittest import mock

MCP_SERVER_DIR = os.path.dirname(os.path.abspath(__file__))


def _import_server():
    import sys

    if str(MCP_SERVER_DIR) not in sys.path:
        sys.path.insert(0, str(MCP_SERVER_DIR))
    import weknora_mcp_server as srv

    return srv


def _make_scope(headers: dict) -> dict:
    return {
        "type": "http",
        "headers": [
            (name.encode("latin-1"), value.encode("latin-1"))
            for name, value in headers.items()
        ],
    }


async def _invoke(headers: dict):
    """Run one request through the middleware; return (status, contextvar)."""
    srv = _import_server()
    observed = {}

    async def app(scope, receive, send):
        observed["per_user_key"] = srv._per_user_api_key.get()
        await send({"type": "http.response.start", "status": 200, "headers": []})
        await send({"type": "http.response.body", "body": b""})

    async def receive():
        return {"type": "http.request", "body": b""}

    sent = []

    async def send(message):
        sent.append(message)

    await srv.MCPAuthMiddleware(app)(_make_scope(headers), receive, send)
    status = sent[0]["status"] if sent else None
    return status, observed.get("per_user_key")


class MiddlewareDecisionTreeTest(unittest.TestCase):
    def _run(self, headers: dict):
        srv = _import_server()
        # Reset the ContextVar between tests: each asyncio.run copies the
        # current context, so a leftover value would leak into the next test.
        srv._per_user_api_key.set(None)
        return asyncio.run(_invoke(headers))

    def test_bearer_carries_per_user_key(self):
        status, key = self._run({"Authorization": "Bearer sk-user-a"})
        self.assertEqual(status, 200)
        self.assertEqual(key, "sk-user-a")

    def test_custom_header_carries_per_user_key(self):
        status, key = self._run({"X-WeKnora-Key": "sk-user-b"})
        self.assertEqual(status, 200)
        self.assertEqual(key, "sk-user-b")

    def test_legacy_x_mcp_auth_token_carries_per_user_key(self):
        status, key = self._run({"X-MCP-Auth-Token": "sk-user-c"})
        self.assertEqual(status, 200)
        self.assertEqual(key, "sk-user-c")

    def test_no_credential_falls_back_to_env_identity(self):
        status, key = self._run({})
        self.assertEqual(status, 200)
        self.assertIsNone(key)

    def test_no_credential_rejected_under_strict_mode(self):
        srv = _import_server()
        with mock.patch.dict(os.environ, {"MCP_REQUIRE_USER_KEY": "1"}):
            status, key = self._run({})
        self.assertEqual(status, 401)
        self.assertIsNone(key)

    def test_malformed_control_chars_rejected(self):
        status, key = self._run({"Authorization": "Bearer sk-bad\x01key"})
        self.assertEqual(status, 401)
        self.assertIsNone(key)

    def test_malformed_too_long_rejected(self):
        srv = _import_server()
        status, key = self._run({"X-WeKnora-Key": "x" * (srv.MAX_USER_KEY_LEN + 1)})
        self.assertEqual(status, 401)
        self.assertIsNone(key)

    def test_non_http_scope_passes_through(self):
        srv = _import_server()

        async def scenario():
            seen = {}

            async def app(scope, receive, send):
                seen["type"] = scope.get("type")

            middleware = srv.MCPAuthMiddleware(app)

            async def receive():
                return {}

            async def send(message):
                pass

            await middleware({"type": "lifespan"}, receive, send)
            return seen

        self.assertEqual(asyncio.run(scenario()), {"type": "lifespan"})


class ValidateUserKeyTest(unittest.TestCase):
    def test_valid(self):
        srv = _import_server()
        self.assertEqual(srv._validate_user_key("  sk-ok  "), "sk-ok")

    def test_empty(self):
        srv = _import_server()
        self.assertIsNone(srv._validate_user_key(""))
        self.assertIsNone(srv._validate_user_key("   "))

    def test_too_long(self):
        srv = _import_server()
        self.assertIsNone(srv._validate_user_key("x" * (srv.MAX_USER_KEY_LEN + 1)))

    def test_control_chars(self):
        srv = _import_server()
        self.assertIsNone(srv._validate_user_key("sk-bad\x00"))
        self.assertIsNone(srv._validate_user_key("sk-bad\x7f"))


class OutboundHeaderTest(unittest.TestCase):
    """The effective key must ride on per-request headers, never on the
    thread-local session default headers (which are shared across requests
    on the same worker thread)."""

    def setUp(self):
        self.srv = _import_server()
        self.client = self.srv.WeKnoraClient("http://localhost:8080/api/v1", "env-key")

    def _capture_request(self):
        captured = {}

        def fake_request(session_self, method, url, **kwargs):
            captured["headers"] = dict(kwargs.get("headers") or {})
            captured["session_headers"] = dict(session_self.headers)
            response = mock.Mock()
            response.raise_for_status.return_value = None
            response.json.return_value = {}
            return response

        patcher = mock.patch.object(self.srv.requests.Session, "request", fake_request)
        patcher.start()
        self.addCleanup(patcher.stop)
        return captured

    def test_request_uses_per_user_key_when_set(self):
        captured = self._capture_request()
        token = self.srv._per_user_api_key.set("sk-user-a")
        try:
            self.client._request("GET", "/knowledge-bases")
        finally:
            self.srv._per_user_api_key.reset(token)
        self.assertEqual(captured["headers"]["X-API-Key"], "sk-user-a")
        # Session default headers must stay untouched (env identity).
        self.assertEqual(captured["session_headers"]["X-API-Key"], "env-key")

    def test_request_falls_back_to_env_key(self):
        captured = self._capture_request()
        token = self.srv._per_user_api_key.set(None)
        try:
            self.client._request("GET", "/knowledge-bases")
        finally:
            self.srv._per_user_api_key.reset(token)
        self.assertEqual(captured["headers"]["X-API-Key"], "env-key")

    def test_request_merges_caller_headers(self):
        captured = self._capture_request()
        self.client._request(
            "GET", "/sessions", params={"page": 1}, headers={"X-Custom": "v"}
        )
        self.assertEqual(captured["headers"]["X-API-Key"], "env-key")
        self.assertEqual(captured["headers"]["X-Custom"], "v")


class ExecutorPropagationTest(unittest.TestCase):
    """chat/agent_chat wrap their executor call in copy_context().run so the
    per-user key set by the middleware reaches the worker thread."""

    def test_ctx_run_propagates_contextvar_into_executor(self):
        srv = _import_server()

        async def scenario():
            token = srv._per_user_api_key.set("sk-thread-user")
            try:
                ctx = contextvars.copy_context()
                fn = functools.partial(
                    ctx.run, lambda: srv._per_user_api_key.get()
                )
                return await asyncio.get_running_loop().run_in_executor(None, fn)
            finally:
                srv._per_user_api_key.reset(token)

        self.assertEqual(asyncio.run(scenario()), "sk-thread-user")

    def test_bare_executor_does_not_propagate(self):
        """Negative control documenting why the ctx.run wrapper is required:
        a bare run_in_executor call loses the ContextVar (returns None)."""
        srv = _import_server()

        async def scenario():
            token = srv._per_user_api_key.set("sk-thread-user")
            try:
                return await asyncio.get_running_loop().run_in_executor(
                    None, lambda: srv._per_user_api_key.get()
                )
            finally:
                srv._per_user_api_key.reset(token)

        self.assertIsNone(asyncio.run(scenario()))


class StartupGuidanceTest(unittest.TestCase):
    def test_no_hard_exit_without_shared_token(self):
        srv = _import_server()
        # The old sys.exit gate is gone entirely.
        self.assertFalse(hasattr(srv, "require_network_transport_auth"))
        self.assertFalse(hasattr(srv, "network_transport_auth_token"))

    def test_warn_only_when_no_auth_sources(self):
        srv = _import_server()
        with mock.patch.dict(
            os.environ, {"MCP_REQUIRE_USER_KEY": "", "WEKNORA_API_KEY": ""}
        ):
            with mock.patch.object(srv.logger, "warning") as warn:
                srv.warn_if_no_auth_sources("http")
        warn.assert_called_once()

        with mock.patch.dict(
            os.environ, {"MCP_REQUIRE_USER_KEY": "1", "WEKNORA_API_KEY": ""}
        ):
            with mock.patch.object(srv.logger, "warning") as warn:
                srv.warn_if_no_auth_sources("http")
        warn.assert_not_called()


if __name__ == "__main__":
    unittest.main()
