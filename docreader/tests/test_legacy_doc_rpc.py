from concurrent import futures
from dataclasses import replace
from pathlib import Path
import unittest
import subprocess
import sys
import threading
from unittest.mock import Mock, patch

import grpc
from docreader.parser.legacy_doc import DOCX_MIME
from docreader.proto import docreader_pb2 as pb, docreader_pb2_grpc as rpc

FIXTURES = Path(__file__).parent / "fixtures"

class LegacyDocRPCTest(unittest.TestCase):
    def test_success_and_sanitized_failures(self):
        # Other legacy tests install a fake docreader.parser in sys.modules.
        # Exercise the real service in a fresh interpreter under full discovery.
        if __name__ != "__main__":
            result = subprocess.run([sys.executable, "-m", "docreader.tests.test_legacy_doc_rpc"], cwd=Path(__file__).resolve().parents[2], capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            return
        from docreader.main import CONFIG, DocReaderServicer
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=3))
        rpc.add_DocReaderServicer_to_server(object.__new__(DocReaderServicer), server)
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        try:
            with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                client = rpc.DocReaderStub(channel)
                req = pb.NormalizeLegacyDocRequest(file_content=(FIXTURES / "legacy_preview.doc").read_bytes(), file_name="original.DOC")
                with patch("docreader.parser.legacy_doc.LegacyDocConverter.normalize", return_value=(FIXTURES / "legacy_preview.docx").read_bytes()) as convert:
                    response = client.NormalizeLegacyDoc(req, timeout=2)
                    self.assertEqual(response.content_type, DOCX_MIME)
                    self.assertEqual(response.file_name, "preview.docx")
                    self.assertGreater(len(response.file_content), 0)
                    # The transport deadline can be rounded; verify the service cap here.
                    self.assertGreater(convert.call_args.args[1], 0)
                    self.assertLessEqual(convert.call_args.args[1], 25)
                    # Check exact deadline propagation without a transport clock.
                    for remaining, expected in [(2, 2), (50, 25), (None, 25), (0, 0)]:
                        with self.subTest(remaining=remaining):
                            context = Mock(spec=grpc.ServicerContext)
                            context.time_remaining.return_value = remaining
                            context.is_active.return_value = True
                            object.__new__(DocReaderServicer).NormalizeLegacyDoc(req, context)
                            self.assertEqual(convert.call_args.args[1], expected)
                with patch("docreader.parser.legacy_doc.LegacyDocConverter.normalize", side_effect=RuntimeError("/private/path secret command output")):
                    with self.assertRaises(grpc.RpcError) as raised:
                        client.NormalizeLegacyDoc(req, timeout=2)
                    self.assertEqual(raised.exception.code(), grpc.StatusCode.FAILED_PRECONDITION)
                    self.assertEqual(raised.exception.details(), "Legacy Word preview unavailable")
                entered, release = threading.Event(), threading.Event()
                def slow_convert(*args):
                    entered.set()
                    release.wait(3)
                    return (FIXTURES / "legacy_preview.docx").read_bytes()
                with patch("docreader.parser.legacy_doc.LegacyDocConverter.normalize", side_effect=slow_convert):
                    first = client.NormalizeLegacyDoc.future(req, timeout=5)
                    self.assertTrue(entered.wait(2))
                    try:
                        with self.assertRaises(grpc.RpcError) as raised:
                            client.NormalizeLegacyDoc(req, timeout=0.5)
                        self.assertEqual(raised.exception.code(), grpc.StatusCode.RESOURCE_EXHAUSTED)
                    finally:
                        release.set()
                        first.result()
                with patch("docreader.main.CONFIG", replace(CONFIG, grpc_max_workers=1)):
                    with self.assertRaises(grpc.RpcError) as raised:
                        client.NormalizeLegacyDoc(req, timeout=2)
                    self.assertEqual(raised.exception.code(), grpc.StatusCode.FAILED_PRECONDITION)
                req.file_name = "test.ppt"
                with self.assertRaises(grpc.RpcError) as raised:
                    client.NormalizeLegacyDoc(req, timeout=2)
                self.assertEqual(raised.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)
        finally:
            server.stop(0).wait()

if __name__ == "__main__":
    unittest.main()
