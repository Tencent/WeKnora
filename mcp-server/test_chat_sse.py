"""Regression tests for the SSE consumer shared by the two chat tools."""

import json
import unittest
from unittest import mock

import requests

import weknora_mcp_server as srv


class ChatSSETest(unittest.TestCase):
    def setUp(self):
        self.client = srv.WeKnoraClient("http://example.test/api/v1", "test-key")

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

    def test_discards_unterminated_event_at_eof(self):
        result = self.consume(
            [b'data: {"response_type":"answer","content":"unfinished"}']
        )
        self.assertEqual(result["answer"], "")

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
            result = self.consume(["data: " + payload, ""] * 3)
        self.assertEqual(result["answer"], "hello" * 3)


if __name__ == "__main__":
    unittest.main()
