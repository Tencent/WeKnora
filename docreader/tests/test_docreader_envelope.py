"""Real gRPC message budgets are distinct from raw uploaded file bytes."""
import hashlib
import os
import unittest
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from unittest.mock import patch

import grpc

from docreader import main
from docreader.models.document import Document
from docreader.parser.stream import ParsedDocumentStream
from docreader.proto import docreader_pb2 as pb, docreader_pb2_grpc as rpc
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner


class _MemoryParser:
    def __init__(self):
        self.calls = 0
        self.reply_bytes = 0

    def parse_file(self, file_name, file_type, content, **kwargs):
        self.calls += 1
        text = "x" * self.reply_bytes if self.reply_bytes else hashlib.sha256(content).hexdigest()
        return Document(content=text)

    def parse_file_stream(self, file_name, file_type, content, **kwargs):
        document = self.parse_file(file_name, file_type, content, **kwargs)
        return ParsedDocumentStream(document=document, image_count=0)


class DocReaderEnvelopeTest(unittest.TestCase):
    def setUp(self):
        self.limit = 1 << 20
        self.config_patch = patch.object(main, "CONFIG", replace(main.CONFIG, grpc_max_file_size_mb=self.limit))
        self.config_patch.start()
        self.addCleanup(self.config_patch.stop)
        self.pool = ThreadPoolExecutor(max_workers=2)
        self.addCleanup(self.pool.shutdown)
        self.service = main.DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        self.addCleanup(self.service.close)
        self.parser = _MemoryParser()
        self.service.parser = self.parser
        # Before the fix, the server applies the raw file cap as a wire cap.
        wire_limit = getattr(main.CONFIG, "grpc_max_message_size_bytes", self.limit)
        self.server = grpc.server(self.pool, options=[
            ("grpc.max_send_message_length", wire_limit),
            ("grpc.max_receive_message_length", wire_limit),
        ])
        rpc.add_DocReaderServicer_to_server(self.service, self.server)
        port = self.server.add_insecure_port("127.0.0.1:0")
        self.server.start()
        self.addCleanup(lambda: self.server.stop(0).wait())
        self.channel = grpc.insecure_channel(f"127.0.0.1:{port}", options=[
            ("grpc.max_send_message_length", self.limit + (1 << 20)),
            ("grpc.max_receive_message_length", self.limit + (1 << 20)),
        ])
        self.addCleanup(self.channel.close)
        self.stub = rpc.DocReaderStub(self.channel)

    def request(self, size, metadata=0):
        return pb.ReadRequest(file_content=b"a" * size, file_name="report.txt", file_type="txt",
                              config=pb.ReadConfig(parser_engine="builtin", parser_engine_overrides={
                                  "metadata": "m" * metadata,
                              }))

    def read(self, request, stream):
        if stream:
            frames = list(self.stub.ReadStream(request, timeout=5))
            self.assertEqual(len(frames), 1)
            return frames[0].meta
        return self.stub.Read(request, timeout=5)

    def test_exact_file_cap_and_metadata_survive_both_rpcs(self):
        for stream in (False, True):
            for size, metadata in ((self.limit, 0), (self.limit - 512, 4096)):
                with self.subTest(stream=stream, size=size):
                    request = self.request(size, metadata)
                    result = self.read(request, stream)
                    self.assertFalse(result.error)
                    self.assertEqual(result.markdown_content, hashlib.sha256(request.file_content).hexdigest())

    def test_response_at_raw_file_cap_survives_protobuf_framing(self):
        self.parser.reply_bytes = self.limit
        for stream in (False, True):
            with self.subTest(stream=stream):
                result = self.read(self.request(16), stream)
                self.assertFalse(result.error)
                self.assertEqual(result.markdown_content, "x" * self.limit)

    def test_raw_file_over_cap_is_rejected_before_parsing(self):
        for stream in (False, True):
            with self.subTest(stream=stream):
                result = self.read(self.request(self.limit + 1), stream)
                self.assertIn("exceeds", result.error)
                self.assertFalse(result.markdown_content)
                self.assertEqual(self.parser.calls, 0)

    def test_stream_fallback_without_parser_streaming_preserves_raw_cap(self):
        self.parser.parse_file_stream = None
        result = self.read(self.request(self.limit + 1), stream=True)
        self.assertIn("exceeds", result.error)
        self.assertEqual(self.parser.calls, 0)

    def test_wire_budget_still_rejects_oversized_metadata(self):
        for stream in (False, True):
            with self.subTest(stream=stream), self.assertRaises(grpc.RpcError) as caught:
                self.read(self.request(16, 3 << 20), stream)
            self.assertEqual(caught.exception.code(), grpc.StatusCode.RESOURCE_EXHAUSTED)
        self.assertEqual(self.parser.calls, 0)


class DocReaderEnvelopeStartupTest(unittest.TestCase):
    def test_main_server_uses_envelope_budget_in_both_directions(self):
        limit = 1 << 20
        cfg = replace(main.CONFIG, grpc_max_file_size_mb=limit, grpc_port=0)
        service = main.DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        parser = _MemoryParser()
        service.parser = parser
        real_server = grpc.server
        pools = []
        port = None

        def create_server(pool, **kwargs):
            nonlocal port
            pools.append(pool)
            server = real_server(pool, **kwargs)
            bind = server.add_insecure_port

            def bind_local(_address):
                nonlocal port
                port = bind("127.0.0.1:0")
                return port

            def probe():
                with grpc.insecure_channel(f"127.0.0.1:{port}", options=[
                    ("grpc.max_send_message_length", 2 * limit),
                    ("grpc.max_receive_message_length", 2 * limit),
                ]) as channel:
                    stub = rpc.DocReaderStub(channel)
                    req = pb.ReadRequest(file_content=b"a" * limit, file_name="report.txt", file_type="txt")
                    result = stub.Read(req, timeout=5)
                    self.assertFalse(result.error)
                    self.assertEqual(result.markdown_content, hashlib.sha256(req.file_content).hexdigest())
                    parser.reply_bytes = limit
                    frames = list(stub.ReadStream(req, timeout=5))
                    self.assertEqual(frames[0].meta.markdown_content, "x" * limit)

            server.add_insecure_port = bind_local
            server.wait_for_termination = probe
            return server

        try:
            with patch.object(main, "CONFIG", cfg), \
                    patch.dict(os.environ, {"GRPC_AUTH_TOKEN": ""}), \
                    patch.object(main.grpc, "server", create_server), \
                    patch.object(main, "DocReaderServicer", return_value=service), \
                    patch.object(main, "load_tls_credentials", return_value=None), \
                    patch.object(main.signal, "signal"), patch.object(main.config, "print_config"):
                main.main()
        finally:
            for pool in pools:
                pool.shutdown()
            service.close()
