"""Supported image formats retain their MIME types across DocReader RPCs."""

import unittest
from concurrent.futures import ThreadPoolExecutor
from email import encoders
from email.mime.base import MIMEBase
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from io import BytesIO

import grpc
from PIL import Image

from docreader.main import (
    DocReaderServicer,
    _image_ref_from_streamed_data,
    _mime_for_ref,
)
from docreader.proto import docreader_pb2 as pb
from docreader.proto import docreader_pb2_grpc as rpc
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner

SVG = (
    b'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16">'
    b'<rect width="16" height="16" fill="red"/></svg>'
)


def _raster(format):
    output = BytesIO()
    Image.new("RGB", (8, 8), "red").save(output, format=format)
    return output.getvalue()


def _svg_archive():
    message = MIMEMultipart("related")
    html = MIMEText(
        '<html><body><p>SVG diagram</p><img src="cid:diagram"/></body></html>', "html"
    )
    message.attach(html)
    image = MIMEBase("image", "svg+xml")
    image.set_payload(SVG)
    encoders.encode_base64(image)
    image["Content-ID"] = "<diagram>"
    image["Content-Location"] = "https://example.test/diagram.svg"
    message.attach(image)
    return message.as_bytes()


class ImageRefMimeTests(unittest.TestCase):
    def setUp(self):
        self.pool = ThreadPoolExecutor(max_workers=2)
        self.addCleanup(self.pool.shutdown)
        self.service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        self.addCleanup(self.service.close)
        self.server = grpc.server(self.pool)
        rpc.add_DocReaderServicer_to_server(self.service, self.server)
        port = self.server.add_insecure_port("127.0.0.1:0")
        self.assertGreater(port, 0)
        self.server.start()
        self.addCleanup(lambda: self.server.stop(0).wait())
        self.channel = grpc.insecure_channel(f"127.0.0.1:{port}")
        self.addCleanup(self.channel.close)
        self.stub = rpc.DocReaderStub(self.channel)

    def read(self, name, file_type, content, stream):
        request = pb.ReadRequest(
            file_name=name, file_type=file_type, file_content=content
        )
        if stream:
            frames = list(self.stub.ReadStream(request, timeout=10))
            self.assertTrue(frames[0].HasField("meta"))
            meta = frames[0].meta
            refs = [frame.image for frame in frames[1:] if frame.HasField("image")]
            self.assertEqual(meta.image_count, len(refs))
        else:
            meta = self.stub.Read(request, timeout=10)
            refs = list(meta.image_refs)
        self.assertFalse(meta.error, meta.error)
        self.assertEqual(len(refs), 1)
        self.assertIn(f"({refs[0].original_ref})", meta.markdown_content)
        return refs[0]

    def test_tiff_uploads_keep_their_type_and_bytes(self):
        payload = _raster("TIFF")
        for suffix in ("tiff", "TIFF"):
            for stream in (False, True):
                with self.subTest(suffix=suffix, stream=stream):
                    ref = self.read("scan." + suffix, "tiff", payload, stream)
                    self.assertEqual(ref.image_data, payload)
                    self.assertEqual(ref.mime_type, "image/tiff")

    def test_svg_archives_keep_their_type_and_bytes(self):
        for stream in (False, True):
            with self.subTest(stream=stream):
                ref = self.read("diagram.mhtml", "mhtml", _svg_archive(), stream)
                self.assertEqual(ref.image_data, SVG)
                self.assertEqual(ref.mime_type, "image/svg+xml")

    def test_existing_png_rpc_metadata_is_unchanged(self):
        payload = _raster("PNG")
        for stream in (False, True):
            with self.subTest(stream=stream):
                ref = self.read("diagram.png", "png", payload, stream)
                self.assertEqual(ref.mime_type, "image/png")
                self.assertEqual(ref.image_data, payload)

    def test_raw_streamed_image_builder_uses_the_same_mime_mapping(self):
        for name, payload, mime in (
            ("scan.tif", _raster("TIFF"), "image/tiff"),
            ("diagram.svg", SVG, "image/svg+xml"),
        ):
            with self.subTest(name=name):
                ref = _image_ref_from_streamed_data("images/" + name, payload)
                self.assertEqual(ref.filename, name)
                self.assertEqual(ref.image_data, payload)
                self.assertEqual(ref.mime_type, mime)

    def test_unknown_reference_suffix_keeps_the_binary_fallback(self):
        self.assertEqual(
            _mime_for_ref("images/data.unknown"),
            ("data.unknown", "application/octet-stream"),
        )


if __name__ == "__main__":
    unittest.main()
