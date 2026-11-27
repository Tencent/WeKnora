"""Per-image source identity across actual PDF parsing and RPC serialization."""

import base64
import io
import json
import unittest
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from unittest.mock import patch

import grpc
from PIL import Image
from pypdf import PdfReader, PdfWriter

from docreader.config import CONFIG
from docreader.main import DocReaderServicer
from docreader.proto import docreader_pb2_grpc
from docreader.proto.docreader_pb2 import ReadConfig, ReadRequest
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner, _scanned_pdf
from docreader.tests.test_pdf_embedded_images import _build_smask_pdf, _pdf_from_objects
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


def _signature_widget_pdf(rotation=0):
    """A raster signature appearance attached to a widget with an inherited field type."""
    text = b"BT /F1 14 Tf 150 600 Td (Native contract text remains searchable.) Tj ET"
    appearance = b"q 100 0 0 40 0 0 cm /Im0 Do Q"
    # Blue top half and red bottom half make it possible to check the actual crop.
    pixels = bytes((20, 60, 200)) * 4 + bytes((200, 40, 20)) * 4
    return _pdf_from_objects([
        b"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [13 0 R 10 0 R] >> >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] "
        b"/CropBox [100 100 500 700] /Rotate %d /Annots [6 0 R 10 0 R 11 0 R] "
        b"/Resources << /Font << /F1 4 0 R >> >> /Contents 12 0 R >>" % rotation,
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        b"<< /FT /Sig /T (Signature) /Parent 13 0 R /Kids [6 0 R 11 0 R] /V 9 0 R >>",
        b"<< /Type /Annot /Subtype /Widget /Parent 5 0 R /P 3 0 R "
        b"/Rect [180 200 280 240] /F 4 /AP << /N 7 0 R >> >>",
        b"<< /Type /XObject /Subtype /Form /BBox [0 0 100 40] "
        b"/Resources << /XObject << /Im0 8 0 R >> >> /Length %d >>\nstream\n"
        % len(appearance) + appearance + b"\nendstream",
        b"<< /Type /XObject /Subtype /Image /Width 4 /Height 2 /ColorSpace /DeviceRGB "
        b"/BitsPerComponent 8 /Length %d >>\nstream\n" % len(pixels)
        + pixels + b"\nendstream",
        b"<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached "
        b"/ByteRange [0 0 0 0] /Contents <00> >>",
        b"<< /Type /Annot /Subtype /Widget /FT /Tx /T (Other field) /P 3 0 R "
        b"/Rect [180 400 280 440] /F 4 /AP << /N 7 0 R >> >>",
        b"<< /Type /Annot /Subtype /Widget /Parent 5 0 R /P 3 0 R "
        b"/Rect [180 300 280 340] /F 2 /AP << /N 7 0 R >> >>",
        b"<< /Length %d >>\nstream\n" % len(text) + text + b"\nendstream",
        b"<< /T (Contract) /Kids [5 0 R] >>",
    ])


class PDFImageSourceTest(unittest.TestCase):
    def read_both(self, content, force_scanned=False, verify=None):
        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        executor = ThreadPoolExecutor(max_workers=2)
        server = grpc.server(executor)
        docreader_pb2_grpc.add_DocReaderServicer_to_server(service, server)
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        config = ReadConfig(parser_engine_overrides={
            "pdf_force_scanned": str(force_scanned).lower(),
        })
        request = ReadRequest(file_content=content, file_name="mixed-figure.pdf", file_type="pdf", config=config)
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
        unary_images = {image.original_ref: image.image_data for image in unary.image_refs}
        streamed_images = {frame.image.original_ref: frame.image.image_data
                           for frame in frames[1:]}
        self.assertEqual(unary_images, streamed_images)
        self.assertEqual(frames[0].meta.image_count, len(unary.image_refs))
        if verify is not None:
            verify(unary.markdown_content, unary.source_blocks)
        return [(unary.metadata, unary_images), (frames[0].meta.metadata, streamed_images)]

    def test_native_pdf_exposes_signature_appearance(self):
        reference = "images/mixed-figure_p1_sig1.jpg"
        markdown = f"![mixed-figure_p1_sig1.jpg]({reference})"

        def verify(content, blocks):
            self.assertIn("Native contract text remains searchable.", content)
            self.assertIn(markdown, content)
            signature = next(block for block in blocks if content[block.start:block.end] == markdown)
            self.assertEqual(json.loads(signature.locator_json)["page"], 1)

        for rotation in (0, 90):
            with self.subTest(rotation=rotation):
                results = self.read_both(_signature_widget_pdf(rotation), verify=verify)
                for metadata, images in results:
                    self.assertEqual(metadata["scanned_page_count"], "0")
                    self.assertEqual(set(images), {reference})
                    self.assertEqual(json.loads(metadata["image_source_types"]), {
                        reference: "pdf_text_layer",
                    })
                    with Image.open(io.BytesIO(images[reference])) as image:
                        w, h = image.size
                        if rotation == 0:
                            self.assertGreater(w, 2 * h)
                            blue, red = (w // 2, h // 4), (w // 2, 3 * h // 4)
                        else:
                            self.assertGreater(h, 2 * w)
                            blue, red = (3 * w // 4, h // 2), (w // 4, h // 2)
                        blue_pixel = image.getpixel(blue)
                        red_pixel = image.getpixel(red)
                        self.assertGreater(blue_pixel[2] - blue_pixel[0], 100)
                        self.assertGreater(red_pixel[0] - red_pixel[2], 100)

    def test_forced_scan_renders_signature_appearance(self):
        reference = "images/mixed-figure_page_1.jpg"
        single_page = _signature_widget_pdf()
        writer = PdfWriter(clone_from=io.BytesIO(single_page))
        writer.add_blank_page(width=400, height=600)
        multi_page = io.BytesIO()
        writer.write(multi_page)

        for page_count, content in ((1, single_page), (2, multi_page.getvalue())):
            with self.subTest(page_count=page_count):
                config = replace(CONFIG, pdf_render_parallelism=2)
                with patch("docreader.parser.pdf_parser.CONFIG", config):
                    results = self.read_both(content, force_scanned=True)
                for metadata, images in results:
                    self.assertEqual(metadata["scanned_page_count"], str(page_count))
                    self.assertEqual(len(images), page_count)
                    with Image.open(io.BytesIO(images[reference])) as image:
                        # Displayed page starts at x=100 and its top is y=700.
                        w, h = image.size
                        blue = image.getpixel((round(w * 130 / 400), round(h * 470 / 600)))
                        red = image.getpixel((round(w * 130 / 400), round(h * 490 / 600)))
                        self.assertGreater(blue[2] - blue[0], 100)
                        self.assertGreater(red[0] - red[2], 100)

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
