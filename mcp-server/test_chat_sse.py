"""Regression tests for the SSE consumer shared by the two chat tools."""

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock

import requests

import weknora_mcp_server as srv


class ChatSSETest(unittest.TestCase):
    def setUp(self):
        self.client = srv.WeKnoraClient("http://example.test/api/v1", "test-key")
        self.addCleanup(self.client.session.close)

    def consume(self, lines, *, agent=False):
        response = mock.MagicMock()
        response.iter_lines.return_value = iter(lines)
        self.response = response
        with mock.patch.object(self.client.session, "post", return_value=response):
            if agent:
                return self.client.agent_chat("session-1", "question", "agent-1")
            return self.client.chat("session-1", "question")

    def test_multiline_answers_and_references_in_both_chat_paths(self):
        lines = [
            b'data: {"response_type":"answer",',
            'data: "content":"你好"}'.encode(),
            b"",
            b'data: {"response_type":"references",',
            b'data: "knowledge_references":[{"id":"chunk-1"}]}',
            b"",
            b'data: {"response_type":"complete"}',
            b"",
        ]
        for agent in (False, True):
            with self.subTest(agent=agent):
                result = self.consume(lines, agent=agent)
                self.assertEqual(result["answer"], "你好")
                self.assertEqual(result["references"], [{"id": "chunk-1"}])
                self.assertEqual(result["session_id"], "session-1")

    def test_dispatches_once_per_frame_not_once_per_data_line(self):
        result = self.consume(
            [
                b'data: {"response_type":"answer","content":"not a frame"}',
                b'data: {"response_type":"answer","content":"still same frame"}',
                b"",
                b'data: {"response_type":"answer","content":"valid frame"}',
                b"",
                b'data: {"response_type":"complete"}',
                b"",
            ]
        )
        self.assertEqual(result["answer"], "valid frame")

    def test_ignores_metadata_comments_and_empty_frames(self):
        result = self.consume(
            [
                "",
                ": heartbeat",
                "event: message",
                "id: 42",
                "retry: 1000",
                "",
                "data:",
                "",
                'data:{"response_type":"answer",',
                ": another heartbeat",
                'data: "content":"hello"}',
                "",
                'data: {"response_type":"complete"}',
                "",
            ]
        )
        self.assertEqual(result["answer"], "hello")

    def test_multiline_error_propagates_and_closes_response(self):
        with self.assertRaisesRegex(requests.RequestException, "upstream failed"):
            self.consume(
                [
                    b'data: {"response_type":"error",',
                    b'data: "content":"upstream failed"}',
                    b"",
                ]
            )
        self.response.__exit__.assert_called_once()

    def test_multiline_complete_stops_before_later_events(self):
        result = self.consume(
            [
                b'data: {"response_type":"answer","content":"done"}',
                b"",
                b'data: {"response_type":',
                b'data: "complete"}',
                b"",
                b'data: {"response_type":"error","content":"must not read"}',
                b"",
            ]
        )
        self.assertEqual(result["answer"], "done")
        self.response.__exit__.assert_called_once()

    def test_eof_before_complete_fails_in_both_chat_paths(self):
        cases = [
            [],
            [b'data: {"response_type":"answer","content":"partial"}', b""],
            [b'data: {"response_type":"answer","content":"unfinished"}'],
            [b'data: {"response_type":"complete"}'],
        ]
        for agent in (False, True):
            for lines in cases:
                with self.subTest(agent=agent, lines=lines):
                    with self.assertRaisesRegex(requests.RequestException, "before complete"):
                        self.consume(lines, agent=agent)
                    self.response.__exit__.assert_called_once()

    def test_crlf_multiline_frame_with_fragmented_utf8(self):
        wire = (
            'data: {"response_type":"answer",\r\n'
            'data: "content":"你好"}\r\n\r\n'
            'data: {"response_type":"complete"}\r\n\r\n'
        ).encode()
        # Exercise requests' real line iterator, with a UTF-8 code point split
        # across transport chunks rather than pre-decoded lines.
        split = wire.index("你".encode()) + 1
        response = requests.Response()
        response.status_code = 200
        response._content_consumed = True
        with mock.patch.object(
            response, "iter_content", return_value=iter([wire[:split], wire[split:]])
        ), mock.patch.object(self.client.session, "post", return_value=response):
            result = self.client.chat("session-1", "question")
        self.assertEqual(result["answer"], "你好")

    def test_rejects_oversized_event_and_closes_response(self):
        with mock.patch.object(srv, "MAX_SSE_EVENT_BYTES", 64, create=True):
            with self.assertRaisesRegex(requests.RequestException, "SSE event exceeds"):
                self.consume([b"data: " + b"x" * 32] * 3)
        self.response.__exit__.assert_called_once()

    def test_event_size_limit_resets_after_each_frame(self):
        payload = json.dumps({"response_type": "answer", "content": "hello"})
        with mock.patch.object(
            srv, "MAX_SSE_EVENT_BYTES", len(payload.encode()) + 1, create=True
        ):
            result = self.consume(
                ["data: " + payload, ""] * 3
                + ['data: {"response_type":"complete"}', ""]
            )
        self.assertEqual(result["answer"], "hello" * 3)

    def check_http_error_closes_in_both_chat_paths(self, status):
        for agent in (False, True):
            with self.subTest(status=status, agent=agent):
                response = requests.Response()
                response.status_code = status
                response.url = "http://example.test/chat"
                response.raw = mock.MagicMock()
                with mock.patch.object(
                    self.client.session, "post", return_value=response
                ), mock.patch.object(
                    response, "close", wraps=response.close
                ) as close, mock.patch.object(
                    response, "iter_lines"
                ) as iter_lines:
                    with self.assertRaises(requests.HTTPError) as raised:
                        if agent:
                            self.client.agent_chat("session-1", "question", "agent-1")
                        else:
                            self.client.chat("session-1", "question")
                self.assertIs(raised.exception.response, response)
                close.assert_called_once_with()
                response.raw.close.assert_called_once_with()
                response.raw.release_conn.assert_called_once_with()
                iter_lines.assert_not_called()

    def test_bad_request_closes_streaming_response(self):
        self.check_http_error_closes_in_both_chat_paths(400)

    def test_service_unavailable_closes_streaming_response(self):
        self.check_http_error_closes_in_both_chat_paths(503)

    def test_real_http_error_closes_streaming_response_in_both_chat_paths(self):
        class ErrorHandler(BaseHTTPRequestHandler):
            def do_POST(self):
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                self.send_response(int(self.path.split("/")[1]))
                body = b"backend error\n"
                self.send_header("Content-Type", "text/plain")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, format, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), ErrorHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 5)
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        port = server.server_address[1]

        for status in (400, 503):
            for agent in (False, True):
                with self.subTest(status=status, agent=agent):
                    client = srv.WeKnoraClient(
                        f"http://127.0.0.1:{port}/{status}", "test-key"
                    )
                    client.session.trust_env = False
                    self.addCleanup(client.session.close)
                    with self.assertRaises(requests.HTTPError) as raised:
                        if agent:
                            client.agent_chat("session-1", "question", "agent-1")
                        else:
                            client.chat("session-1", "question")
                    response = raised.exception.response
                    self.addCleanup(response.close)
                    self.assertEqual(response.status_code, status)
                    self.assertTrue(response.raw.closed)

    def test_post_connection_error_is_preserved_in_both_chat_paths(self):
        for agent in (False, True):
            with self.subTest(agent=agent):
                error = requests.ConnectionError("connection failed")
                with mock.patch.object(self.client.session, "post", side_effect=error):
                    with self.assertRaises(requests.ConnectionError) as raised:
                        if agent:
                            self.client.agent_chat("session-1", "question", "agent-1")
                        else:
                            self.client.chat("session-1", "question")
                self.assertIs(raised.exception, error)

    def test_stream_read_error_closes_response_in_both_chat_paths(self):
        for agent in (False, True):
            with self.subTest(agent=agent):
                response = requests.Response()
                response.status_code = 200
                response.raw = mock.MagicMock()
                error = requests.ConnectionError("stream read failed")
                with mock.patch.object(
                    self.client.session, "post", return_value=response
                ), mock.patch.object(
                    response, "iter_lines", side_effect=error
                ), mock.patch.object(
                    response, "close", wraps=response.close
                ) as close:
                    with self.assertRaises(requests.ConnectionError) as raised:
                        if agent:
                            self.client.agent_chat("session-1", "question", "agent-1")
                        else:
                            self.client.chat("session-1", "question")
                self.assertIs(raised.exception, error)
                close.assert_called_once_with()
                response.raw.close.assert_called_once_with()
                response.raw.release_conn.assert_called_once_with()


    def consume_wire(self, wire, *, agent=False, chunks=None):
        response = requests.Response()
        response.status_code = 200
        response._content_consumed = True
        with mock.patch.object(
            response, "iter_content", return_value=iter(chunks or [wire])
        ), mock.patch.object(
            self.client.session, "post", return_value=response
        ), mock.patch.object(response, "close", wraps=response.close) as close:
            self.wire_close = close
            if agent:
                return self.client.agent_chat("session-1", "question", "agent-1")
            return self.client.chat("session-1", "question")

    def test_initial_bom_answer_with_fragmented_transport_in_both_chat_paths(self):
        wire = (
            '\ufeffdata: {"response_type":"answer","content":"你好"}\r\n\r\n'
            'data: {"response_type":"references",'
            '"knowledge_references":[{"id":"chunk-1"}]}\r\n\r\n'
            'data: {"response_type":"complete"}\r\n\r\n'
        ).encode()
        split = wire.index("你".encode()) + 1
        chunks = [wire[:1], wire[1:2], wire[2:split], wire[split:]]
        for agent in (False, True):
            with self.subTest(agent=agent):
                result = self.consume_wire(wire, agent=agent, chunks=chunks)
                self.assertEqual(result["answer"], "你好")
                self.assertEqual(result["references"], [{"id": "chunk-1"}])
                self.assertEqual(result["session_id"], "session-1")
                self.wire_close.assert_called_once_with()

    def test_initial_bom_references_in_both_chat_paths(self):
        wire = (
            '\ufeffdata: {"response_type":"references",'
            '"knowledge_references":[{"id":"first-reference"}]}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        for agent in (False, True):
            with self.subTest(agent=agent):
                result = self.consume_wire(wire, agent=agent)
                self.assertEqual(result["answer"], "")
                self.assertEqual(result["references"], [{"id": "first-reference"}])

    def test_initial_bom_error_propagates_and_closes_in_both_chat_paths(self):
        wire = (
            '\ufeffdata: {"response_type":"error","content":"upstream failed"}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        for agent in (False, True):
            with self.subTest(agent=agent):
                with self.assertRaisesRegex(requests.RequestException, "upstream failed"):
                    self.consume_wire(wire, agent=agent)
                self.wire_close.assert_called_once_with()

    def test_initial_bom_complete_stops_before_later_events_in_both_chat_paths(self):
        wire = (
            '\ufeffdata: {"response_type":"complete"}\n\n'
            'data: {"response_type":"answer","content":"must not read"}\n\n'
        ).encode()
        for agent in (False, True):
            with self.subTest(agent=agent):
                result = self.consume_wire(wire, agent=agent)
                self.assertEqual(result["answer"], "")
                self.wire_close.assert_called_once_with()

    def test_only_one_initial_bom_is_ignored(self):
        wire = (
            '\ufeff\ufeffdata: {"response_type":"answer","content":"unknown field"}\n\n'
            'data: {"response_type":"answer","content":"valid"}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        for agent in (False, True):
            with self.subTest(agent=agent):
                self.assertEqual(self.consume_wire(wire, agent=agent)["answer"], "valid")

    def test_later_bom_field_and_literal_content_keep_their_meaning(self):
        wire = (
            ': heartbeat\n'
            '\ufeffdata: {"response_type":"answer","content":"unknown field"}\n\n'
            'data: {"response_type":"answer","content":"left\ufeffright"}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        for agent in (False, True):
            with self.subTest(agent=agent):
                result = self.consume_wire(wire, agent=agent)
                self.assertEqual(result["answer"], "left\ufeffright")

    def test_plain_and_bom_metadata_streams_remain_healthy(self):
        data = (
            'data: {"response_type":"answer","content":"plain"}\n\n'
            'data: {"response_type":"complete"}\n\n'
        )
        for prefix in ("", "\ufeffevent: message\n"):
            for agent in (False, True):
                with self.subTest(prefix=prefix, agent=agent):
                    result = self.consume_wire((prefix + data).encode(), agent=agent)
                    self.assertEqual(result["answer"], "plain")
                    self.assertEqual(result["session_id"], "session-1")
                    self.wire_close.assert_called_once_with()


    def test_real_http_initial_bom_stream_in_both_chat_paths(self):
        wire = (
            '\ufeffdata: {"response_type":"answer","content":"你好"}\n\n'
            'data: {"response_type":"references",'
            '"knowledge_references":[{"id":"wire-reference"}]}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        paths = []

        class SSEHandler(BaseHTTPRequestHandler):
            def do_POST(self):
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                paths.append(self.path)
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream; charset=utf-8")
                self.send_header("Content-Length", str(len(wire)))
                self.end_headers()
                self.wfile.write(wire)

            def log_message(self, format, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), SSEHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 5)
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        port = server.server_address[1]
        client = srv.WeKnoraClient(f"http://127.0.0.1:{port}/api/v1", "test-key")
        client.session.trust_env = False
        self.addCleanup(client.session.close)
        for agent in (False, True):
            with self.subTest(agent=agent):
                if agent:
                    result = client.agent_chat("session-1", "question", "agent-1")
                else:
                    result = client.chat("session-1", "question")
                self.assertEqual(result["answer"], "你好")
                self.assertEqual(result["references"], [{"id": "wire-reference"}])
                self.assertEqual(result["session_id"], "session-1")
        self.assertEqual(
            paths,
            ["/api/v1/knowledge-chat/session-1", "/api/v1/agent-chat/session-1"],
        )


if __name__ == "__main__":
    unittest.main()
