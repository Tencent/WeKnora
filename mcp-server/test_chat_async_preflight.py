"""Exercise the async chat tools against a real, controlled HTTP backend."""

import asyncio
import contextvars
import json
import threading
import unittest
from contextlib import ExitStack
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock

import requests

import weknora_mcp_server as srv

KB_ID = "11111111-1111-1111-1111-111111111111"
SHARED_KB_ID = "22222222-2222-2222-2222-222222222222"
AGENT_ID = "33333333-3333-3333-3333-333333333333"
REQUEST_CONTEXT = contextvars.ContextVar("preflight_test_context", default=None)


class ChatAsyncPreflightTest(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        patches = ExitStack()
        self.addCleanup(patches.close)
        self.routes = {
            "/api/v1/knowledge-bases": {"data": [{"id": KB_ID, "name": "Docs"}]},
            "/api/v1/shared-knowledge-bases": {
                "data": [{"knowledge_base": {"id": SHARED_KB_ID, "name": "Shared"}}]
            },
            "/api/v1/agents": {"data": [{"id": AGENT_ID, "name": "Research"}]},
            f"/api/v1/agents/{AGENT_ID}": {
                "data": {"config": {"kb_selection_mode": "all"}}
            },
        }
        self.http_calls = []
        self.metadata_calls = []
        self.sessions = []
        self.hold_path = None
        self.metadata_started = threading.Event()
        self.release_metadata = threading.Event()
        case = self

        class Handler(BaseHTTPRequestHandler):
            def setup(self):
                super().setup()
                self.connection.settimeout(3)

            def log_message(self, *_args):
                pass

            def do_GET(self):
                case.http_calls.append(("GET", self.path, None))
                if self.path == case.hold_path:
                    case.metadata_started.set()
                    if not case.release_metadata.wait(5):
                        self.send_error(504, "test metadata release deadline")
                        return
                value = case.routes.get(self.path)
                status = 200
                if isinstance(value, tuple):
                    status, value = value
                if value is None:
                    self.send_error(404)
                    return
                body = json.dumps(value).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                payload = json.loads(
                    self.rfile.read(int(self.headers["Content-Length"]))
                )
                case.http_calls.append(("POST", self.path, payload))
                body = (
                    'data: {"response_type":"answer","content":"done"}\n\n'
                    'data: {"response_type":"complete"}\n\n'
                ).encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = False
        # Non-daemon request threads are joined by server_close().
        self.server_thread = threading.Thread(
            target=self.server.serve_forever, kwargs={"poll_interval": 0.01}
        )
        self.server_thread.start()
        self.addCleanup(self.cleanup_backend)
        self.client = srv.WeKnoraClient(
            f"http://127.0.0.1:{self.server.server_port}/api/v1", "test-key"
        )
        new_session = self.client._new_session

        def track_session():
            session = new_session()
            session.trust_env = False
            self.sessions.append(session)
            return session

        patches.enter_context(
            mock.patch.object(self.client, "_new_session", side_effect=track_session)
        )
        request = self.client._request

        def track_request(method, endpoint, **kwargs):
            self.metadata_calls.append(
                (endpoint, threading.get_ident(), REQUEST_CONTEXT.get())
            )
            return request(method, endpoint, **kwargs)

        patches.enter_context(
            mock.patch.object(self.client, "_request", side_effect=track_request)
        )
        patches.enter_context(mock.patch.object(srv, "client", self.client))

    def cleanup_backend(self):
        self.release_metadata.set()
        self.server.shutdown()
        self.server.server_close()
        self.server_thread.join(3)
        self.assertFalse(self.server_thread.is_alive())
        for session in self.sessions:
            session.close()

    def assert_metadata_off_loop(self):
        self.assertTrue(self.metadata_calls)
        for endpoint, thread_id, context in self.metadata_calls:
            with self.subTest(endpoint=endpoint):
                self.assertNotEqual(thread_id, threading.get_ident())
                self.assertEqual(context, "request-context")

    async def asyncSetUp(self):
        self.context_token = REQUEST_CONTEXT.set("request-context")

    async def asyncTearDown(self):
        # Python 3.10 runs synchronous cleanups outside the async task's Context.
        REQUEST_CONTEXT.reset(self.context_token)

    async def test_chat_resolves_owned_and_shared_names_off_loop(self):
        result = await srv.chat("session", "question", ["Docs", "Shared"])
        self.assertEqual(result["answer"], "done")
        self.assertEqual(result["session_id"], "session")
        self.assertEqual(
            [path for path, *_ in self.metadata_calls],
            ["/knowledge-bases", "/knowledge-bases", "/shared-knowledge-bases"],
        )
        self.assertEqual(
            self.http_calls[-1],
            (
                "POST",
                "/api/v1/knowledge-chat/session",
                {
                    "query": "question",
                    "channel": "api",
                    "knowledge_base_ids": [KB_ID, SHARED_KB_ID],
                },
            ),
        )
        self.assert_metadata_off_loop()

    async def test_agent_resolves_agent_and_kb_names_off_loop(self):
        result = await srv.agent_chat(
            "session", "question", "Research", ["Docs"], web_search_enabled=True
        )
        self.assertEqual(result["answer"], "done")
        self.assertEqual(
            [path for path, *_ in self.metadata_calls],
            ["/agents", "/knowledge-bases"],
        )
        self.assertEqual(
            self.http_calls[-1][2],
            {
                "query": "question",
                "agent_id": AGENT_ID,
                "channel": "api",
                "knowledge_base_ids": [KB_ID],
                "web_search_enabled": True,
            },
        )
        self.assert_metadata_off_loop()

    async def test_agent_configuration_preflight_runs_off_loop(self):
        result = await srv.agent_chat("session", "question", AGENT_ID)
        self.assertEqual(result["answer"], "done")
        self.assertEqual(
            [path for path, *_ in self.metadata_calls], [f"/agents/{AGENT_ID}"]
        )
        self.assert_metadata_off_loop()

    async def test_missing_kbs_keep_ordered_hint_and_skip_chat(self):
        self.routes[f"/api/v1/agents/{AGENT_ID}"] = {
            "data": {"config": {"kb_selection_mode": "none"}}
        }
        with self.assertRaises(ValueError) as raised:
            await srv.agent_chat("session", "question", AGENT_ID)
        self.assertEqual(
            str(raised.exception),
            f"Agent '{AGENT_ID}' has kb_selection_mode='none' with no built-in "
            "knowledge bases. You must provide knowledge_base_ids. "
            f"Available knowledge bases: [Docs ({KB_ID}), Shared ({SHARED_KB_ID})]",
        )
        self.assertEqual(
            [path for path, *_ in self.metadata_calls],
            [f"/agents/{AGENT_ID}", "/knowledge-bases", "/shared-knowledge-bases"],
        )
        self.assertTrue(all(method == "GET" for method, *_ in self.http_calls))
        self.assert_metadata_off_loop()

    async def test_failed_agent_preflight_remains_nonfatal(self):
        self.routes[f"/api/v1/agents/{AGENT_ID}"] = (503, {"error": "offline"})
        result = await srv.agent_chat("session", "question", AGENT_ID)
        self.assertEqual(result["answer"], "done")
        self.assertEqual(self.http_calls[-1][0], "POST")
        self.assert_metadata_off_loop()

    async def test_unknown_kb_still_fails_before_chat(self):
        with self.assertRaisesRegex(ValueError, "Knowledge base 'Missing' not found"):
            await srv.chat("session", "question", ["Missing"])
        self.assertTrue(all(method == "GET" for method, *_ in self.http_calls))
        self.assert_metadata_off_loop()

    async def test_kb_request_error_still_propagates(self):
        self.routes["/api/v1/knowledge-bases"] = (503, {"error": "offline"})
        with self.assertRaises(requests.HTTPError) as raised:
            await srv.chat("session", "question", ["Docs"])
        self.assertEqual(raised.exception.response.status_code, 503)
        self.assertEqual(len(self.http_calls), 1)
        self.assert_metadata_off_loop()

    async def test_uuid_chat_keeps_no_metadata_fast_path(self):
        result = await srv.chat("session", "question", [KB_ID])
        self.assertEqual(result["answer"], "done")
        self.assertEqual(self.metadata_calls, [])
        self.assertEqual(len(self.http_calls), 1)
        self.assertEqual(self.http_calls[0][2]["knowledge_base_ids"], [KB_ID])

    async def test_held_metadata_response_allows_event_loop_progress(self):
        self.hold_path = "/api/v1/knowledge-bases"
        loop = asyncio.get_running_loop()
        heartbeat = threading.Event()
        observed = []

        def observe_progress():
            try:
                started = self.metadata_started.wait(3)
                if started:
                    loop.call_soon_threadsafe(heartbeat.set)
                observed.append(started and heartbeat.wait(2))
            finally:
                self.release_metadata.set()

        observer = threading.Thread(target=observe_progress)
        observer.start()
        try:
            result = await srv.chat("session", "question", ["Docs"])
        finally:
            self.release_metadata.set()
            observer.join(3)
        self.assertFalse(observer.is_alive())
        self.assertEqual(result["answer"], "done")
        self.assertEqual(observed, [True])
        self.assert_metadata_off_loop()


if __name__ == "__main__":
    unittest.main()
