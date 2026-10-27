"""Linux regression: large PDF requests release their buffers while idle."""

import ctypes
import gc
import io
import logging
import multiprocessing
import os
import sys
import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch

import grpc
from PIL import Image

from docreader.proto import docreader_pb2 as pb
from docreader.proto import docreader_pb2_grpc as rpc


def _anonymous_bytes():
    with open("/proc/self/status") as status:
        return next(
            int(line.split()[1]) * 1024
            for line in status
            if line.startswith("RssAnon:")
        )


def _serve(control):
    os.environ["DOCREADER_PDF_RENDER_PARALLELISM"] = "1"
    os.environ["GRPC_AUTH_TOKEN"] = ""
    os.environ["GRPC_TLS_ENABLED"] = "false"
    from docreader import main

    logging.disable(logging.CRITICAL)
    real_server = grpc.server

    def create_server(pool, **kwargs):
        kwargs["options"] = [("grpc.max_receive_message_length", 80 * 1024 * 1024)]
        server = real_server(pool, **kwargs)
        add_port = server.add_insecure_port
        port = None

        def bind(_address):
            nonlocal port
            port = add_port("127.0.0.1:0")
            return port

        def wait():
            # No auto-GC while idle: the completed request must reclaim itself.
            gc.collect()
            gc.disable()
            control.send(port)
            try:
                while control.recv() == "memory":
                    control.send(_anonymous_bytes())
            finally:
                server.stop(0).wait()
                pool.shutdown()
                control.close()

        server.add_insecure_port = bind
        server.wait_for_termination = wait
        return server

    with patch.object(main.grpc, "server", create_server):
        with patch.object(main.config, "print_config"):
            main.main()


def _scanned_pdf(padding_bytes=0):
    image = io.BytesIO()
    with Image.new("RGB", (64, 64), "black") as page:
        page.save(image, format="JPEG")
    jpeg = image.getvalue()
    drawing = b"q 200 0 0 200 0 0 cm /Im0 Do Q"
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "
        b"/Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>",
        b"<< /Type /XObject /Subtype /Image /Width 64 /Height 64 "
        b"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length "
        + str(len(jpeg)).encode() + b" >>\nstream\n" + jpeg + b"\nendstream",
        b"<< /Length " + str(len(drawing)).encode()
        + b" >>\nstream\n" + drawing + b"\nendstream",
    ]
    # An unreferenced stream gives the request a large payload without making
    # rendering slow or requiring a checked-in binary fixture.
    output = io.BytesIO()
    output.write(b"%PDF-1.7\n")
    offsets = []
    for number, obj in enumerate(objects, 1):
        offsets.append(output.tell())
        output.write(f"{number} 0 obj\n".encode() + obj + b"\nendobj\n")
    offsets.append(output.tell())
    output.write(f"6 0 obj\n<< /Length {padding_bytes} >>\nstream\n".encode())
    output.write(b"A" * padding_bytes)
    output.write(b"\nendstream\nendobj\n")
    xref = output.tell()
    output.write(b"xref\n0 7\n0000000000 65535 f \n")
    for offset in offsets:
        output.write(f"{offset:010d} 00000 n \n".encode())
    output.write(
        f"trailer\n<< /Size 7 /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode()
    )
    return output.getvalue()


@unittest.skipUnless(
    sys.platform.startswith("linux") and hasattr(ctypes.CDLL(None), "malloc_trim"),
    "anonymous-memory regression requires Linux and glibc",
)
class DocReaderMemoryTest(unittest.TestCase):
    def test_large_pdf_buffers_are_released_after_rpc(self):
        ctx = multiprocessing.get_context("spawn")
        control, child_control = ctx.Pipe()
        process = ctx.Process(target=_serve, args=(child_control,))
        process.start()
        child_control.close()
        channel = None
        try:
            self.assertTrue(control.poll(30), "docreader did not start")
            port = control.recv()
            channel = grpc.insecure_channel(
                f"127.0.0.1:{port}",
                options=[("grpc.max_send_message_length", 80 * 1024 * 1024)],
            )
            grpc.channel_ready_future(channel).result(timeout=10)
            stub = rpc.DocReaderStub(channel)
            small = pb.ReadRequest(
                file_content=_scanned_pdf(), file_name="scan.pdf", file_type="pdf"
            )
            self.assertEqual(len(list(stub.ReadStream(small, timeout=10))), 2)

            def memory():
                control.send("memory")
                self.assertTrue(control.poll(5), "memory probe did not respond")
                return control.recv()

            baseline = memory()

            def assert_reclaimed(label):
                # EOF can arrive before the worker finishes its cleanup.
                deadline = time.monotonic() + 5
                current = memory()
                while current - baseline > 32 * 1024 * 1024 and time.monotonic() < deadline:
                    time.sleep(0.02)
                    current = memory()
                print(
                    f"{label}: idle delta={(current-baseline)/1048576:.1f} MiB "
                    "after a 64 MiB request", flush=True,
                )
                self.assertLess(current - baseline, 32 * 1024 * 1024)

            large = pb.ReadRequest(
                file_content=_scanned_pdf(64 * 1024 * 1024),
                file_name="scan.pdf",
                file_type="pdf",
            )
            for method in ("ReadStream", "Read", "ReadStream", "invalid PDF"):
                with self.subTest(method=method):
                    if method == "Read":
                        result = stub.Read(large, timeout=20)
                        self.assertFalse(result.error)
                        self.assertEqual(len(result.image_refs), 1)
                    else:
                        request = large
                        if method == "invalid PDF":
                            request = pb.ReadRequest(
                                file_content=b"not a PDF" + b"A" * (64 * 1024 * 1024),
                                file_name="broken.pdf", file_type="pdf",
                            )
                        frames = list(stub.ReadStream(request, timeout=20))
                        if method == "invalid PDF":
                            self.assertTrue(frames[0].meta.error)
                        else:
                            self.assertEqual(len(frames), 2)
                            self.assertEqual(frames[0].meta.image_count, 1)
                            self.assertTrue(frames[1].image.image_data.startswith(b"\xff\xd8"))
                    assert_reclaimed(method)
            with ThreadPoolExecutor(max_workers=2) as clients:
                calls = [
                    clients.submit(lambda: list(stub.ReadStream(large, timeout=20)))
                    for _ in range(2)
                ]
                for call in calls:
                    self.assertEqual(len(call.result()), 2)
            assert_reclaimed("concurrent ReadStream")
            self.assertEqual(len(list(stub.ReadStream(small, timeout=10))), 2)
        finally:
            if channel is not None:
                channel.close()
            if process.is_alive():
                control.send("stop")
                process.join(timeout=5)
            if process.is_alive():
                process.terminate()
                process.join(timeout=5)
            control.close()


if __name__ == "__main__":
    unittest.main()
