"""Per-image source identity across actual PDF parsing and RPC serialization."""

import base64
import hashlib
import io
import json
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch

import grpc
from PIL import Image
from pypdf import PdfReader, PdfWriter

from docreader.main import DocReaderServicer
from docreader.proto import docreader_pb2_grpc
from docreader.proto.docreader_pb2 import ReadConfig, ReadRequest
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner, _scanned_pdf
from docreader.tests.test_pdf_embedded_images import _build_smask_pdf
from docreader.tests.test_source_locator_pdf import pdf_fixture


def mixed_image_pdf(include_scan=True):
    native = PdfReader(io.BytesIO(pdf_fixture([{
        "lines": [(f"Original technical report paragraph {index}: preserve native figures.", 50, 750 - index * 25)
                  for index in range(15)]
    }])))
    figure = PdfReader(io.BytesIO(_build_smask_pdf()))
    writer = PdfWriter()
    writer.add_page(native.pages[0])
    # pypdf 7 refuses to rewrite the contents of a page that no writer owns
    # (`PageObject.replace_contents()` is gone there), so the figure is merged
    # onto the copy the writer holds instead of onto the reader's page.
    writer.pages[0].merge_page(figure.pages[0])
    if include_scan:
        scanned = PdfReader(io.BytesIO(_scanned_pdf(1)))
        writer.add_page(scanned.pages[0])
    output = io.BytesIO()
    writer.write(output)
    return output.getvalue()


class PDFImageSourceTest(unittest.TestCase):
    def read_both(self, content, force_scanned=False, file_name="mixed-figure.pdf",
                  expected_refs=()):
        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        executor = ThreadPoolExecutor(max_workers=2)
        server = grpc.server(executor)
        docreader_pb2_grpc.add_DocReaderServicer_to_server(service, server)
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        config = ReadConfig(parser_engine="builtin", parser_engine_overrides={
            "pdf_force_scanned": str(force_scanned).lower(),
        })
        request = ReadRequest(file_content=content, file_name=file_name, file_type="pdf", config=config)
        try:
            with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                client = docreader_pb2_grpc.DocReaderStub(channel)
                unary = client.Read(request, timeout=10)
                frames = list(client.ReadStream(request, timeout=10))
        finally:
            server.stop(0).wait()
            executor.shutdown(wait=True)
            service.close()
        self.assertFalse(unary.error)
        self.assertFalse(frames[0].meta.error)
        self.assertEqual(unary.markdown_content, frames[0].meta.markdown_content)
        self.assertEqual(list(unary.source_blocks), list(frames[0].meta.source_blocks))
        unary_images = {image.original_ref: hashlib.sha256(image.image_data).hexdigest() for image in unary.image_refs}
        streamed_images = {frame.image.original_ref: hashlib.sha256(frame.image.image_data).hexdigest()
                           for frame in frames[1:]}
        self.assertEqual(unary_images, streamed_images)
        if expected_refs:
            for ref in expected_refs:
                self.assertIn(f"]({ref})", unary.markdown_content)
            self.assertEqual(set(unary_images), set(expected_refs))
        return [(unary.metadata, unary_images), (frames[0].meta.metadata, streamed_images)]

    def test_mixed_pdf_preserves_native_figure_identity(self):
        expected = {
            "images/mixed-figure_p1_img1.jpg": "pdf_text_layer",
            "images/mixed-figure_page_2.jpg": "scanned_pdf",
        }
        for metadata, images in self.read_both(mixed_image_pdf()):
            self.assertEqual(metadata["scanned_page_count"], "1")
            self.assertEqual(metadata["embedded_image_count"], "1")
            self.assertEqual(metadata["image_source_type"], "scanned_pdf")
            self.assertEqual(json.loads(metadata.get("image_source_types", "{}")), expected)
            self.assertEqual(set(images), set(expected))

    def test_pdf_filename_parentheses_keep_images_linked(self):
        pdf = mixed_image_pdf()
        for file_name, stem in (
            ("xxx（2024年05月09日-2028年05月8日).pdf", "xxx（2024年05月09日-2028年05月8日%29"),
            ("draft (final).pdf", "draft%20%28final%29"),
            ("draft(.pdf", "draft%28"),
            ("报告（终稿）.pdf", "报告（终稿）"),
        ):
            with self.subTest(file_name=file_name, force_scanned=False):
                expected = {
                    f"images/{stem}_p1_img1.jpg": "pdf_text_layer",
                    f"images/{stem}_page_2.jpg": "scanned_pdf",
                }
                for metadata, _ in self.read_both(pdf, file_name=file_name, expected_refs=expected):
                    self.assertEqual(json.loads(metadata["image_source_types"]), expected)

            with self.subTest(file_name=file_name, force_scanned=True):
                expected = [f"images/{stem}_page_{page}.jpg" for page in (1, 2)]
                for metadata, _ in self.read_both(
                    pdf, force_scanned=True, file_name=file_name, expected_refs=expected
                ):
                    self.assertEqual(metadata["image_source_type"], "scanned_pdf")

    def test_native_only_pdf_marks_its_figure_as_native(self):
        for metadata, images in self.read_both(mixed_image_pdf(include_scan=False)):
            self.assertEqual(metadata["scanned_page_count"], "0")
            self.assertEqual(json.loads(metadata.get("image_source_types", "{}")), {
                reference: "pdf_text_layer" for reference in images
            })
            self.assertEqual(len(images), 1)

    def test_forced_scan_keeps_every_page_on_the_scan_path(self):
        for metadata, images in self.read_both(mixed_image_pdf(), force_scanned=True):
            self.assertEqual(metadata["scanned_page_count"], "2")
            source_types = json.loads(metadata.get("image_source_types", "{}"))
            self.assertEqual(len(images), 2)
            self.assertTrue(all(source_types.get(reference, metadata["image_source_type"]) == "scanned_pdf"
                                for reference in images))

    def test_native_vector_clips_receive_native_source_metadata(self):
        image = io.BytesIO()
        Image.new("RGB", (80, 80), "blue").save(image, format="JPEG")
        reference = "images/mixed-figure_p1_vec1.jpg"
        clip = (reference, base64.b64encode(image.getvalue()).decode(), 200, "Figure 1: flow diagram")
        with patch("docreader.parser.pdf_parser._extract_vector_figure_clips", return_value=[clip]):
            results = self.read_both(mixed_image_pdf(include_scan=False))
        for metadata, images in results:
            self.assertEqual(metadata["vector_figure_count"], "1")
            self.assertEqual(len(images), 2)
            self.assertIn(reference, images)
            self.assertEqual(json.loads(metadata.get("image_source_types", "{}")), {
                name: "pdf_text_layer" for name in images
            })


if __name__ == "__main__":
    unittest.main()
