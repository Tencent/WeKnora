"""Real PDF regression coverage for Paddle's optional native-only probe."""
import io
import unittest
from unittest.mock import patch

import docreader.parser.pdf_parser as pdf_parser
from docreader.parser.pdf_parser import PDFParser
from docreader.tests.test_pdf_embedded_images import _pdf_from_objects


def native_fixture(*, scanned=False, blank=False):
    # An actual text table and a 120x90 RGB figure; optional second page is
    # image-dominated despite carrying an invisible, nonempty OCR text layer.
    lines = [] if blank else [
        ("Quarterly native report", 60, 740),
        ("Region", 60, 690), ("Sales", 220, 690), ("Margin", 380, 690),
        ("North", 60, 660), ("100", 220, 660), ("25", 380, 660),
        ("South", 60, 630), ("200", 220, 630), ("30", 380, 630),
        ("Original evidence must survive without OCR.", 60, 580),
    ]
    stream = b"\n".join(
        f"BT /F1 14 Tf {x} {y} Td ({text}) Tj ET".encode()
        for text, x, y in lines
    )
    if not blank:
        stream += b"\nq 240 0 0 180 60 300 cm /Im0 Do Q"
    image = bytes((120, 60, 180)) * (120 * 90)
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [4 0 R 7 0 R] /Count 2 >>" if scanned else
        b"<< /Type /Pages /Kids [4 0 R] /Count 1 >>",
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] "
        b"/Resources << /Font << /F1 3 0 R >> /XObject << /Im0 6 0 R >> >> /Contents 5 0 R >>",
        f"<< /Length {len(stream)} >>\nstream\n".encode() + stream + b"\nendstream",
        f"<< /Type /XObject /Subtype /Image /Width 120 /Height 90 /ColorSpace /DeviceRGB "
        f"/BitsPerComponent 8 /Length {len(image)} >>\nstream\n".encode() + image + b"\nendstream",
    ]
    if scanned:
        scan = (b"q 600 0 0 800 0 0 cm /Im0 Do Q\n"
                b"BT /F1 14 Tf 3 Tr 60 700 Td (Hidden OCR text is not a native page) Tj ET")
        objects.extend([
            b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] "
            b"/Resources << /Font << /F1 3 0 R >> /XObject << /Im0 6 0 R >> >> /Contents 8 0 R >>",
            f"<< /Length {len(scan)} >>\nstream\n".encode() + scan + b"\nendstream",
        ])
    return _pdf_from_objects(objects)


class NativeOnlyPDFTest(unittest.TestCase):
    def parse(self, content, *, stream=False, **overrides):
        parser = PDFParser(file_name="report.pdf", file_type="pdf", pdf_native_only=True, **overrides)
        if stream:
            with parser.parse_stream(content) as result:
                self.assertEqual(list(result.images), [])
                self.assertEqual(result.image_count, len(result.document.images))
                return result.document
        return parser.parse_into_text(content)

    def test_preserves_native_table_figure_and_exact_source_blocks(self):
        content = native_fixture()
        baseline = PDFParser(file_name="report.pdf", pdf_force_scanned=False).parse_into_text(content)
        self.assertTrue(baseline.images)
        self.assertTrue(baseline.source_blocks)
        for stream in (False, True):
            with self.subTest(stream=stream):
                actual = self.parse(content, stream=stream, pdf_force_scanned=False)
                self.assertEqual(actual.metadata["pdf_native_only"], "accepted")
                self.assertEqual(actual.content, baseline.content)
                self.assertEqual(actual.images, baseline.images)
                self.assertEqual(actual.source_blocks, baseline.source_blocks)
                for token in ("Region", "Sales", "Margin", "North", "100", "South", "200"):
                    self.assertIn(token, actual.content)
                self.assertRegex(actual.content, r"North\s+100\s+25")
                self.assertRegex(actual.content, r"South\s+200\s+30")
                for ref in actual.images:
                    self.assertIn(ref, actual.content)
                for block in actual.source_blocks:
                    self.assertTrue(0 <= block["start"] < block["end"] <= len(actual.content))

    def test_mixed_hidden_ocr_page_is_rejected_without_rendering(self):
        for stream in (False, True):
            with self.subTest(stream=stream), patch.object(pdf_parser, "_render_scanned_pages") as render, \
                    patch.object(pdf_parser, "_write_pdf_temp_file") as temp:
                doc = self.parse(native_fixture(scanned=True), stream=stream, pdf_force_scanned=False)
                self.assertEqual(doc.metadata["pdf_native_only"], "requires_ocr")
                self.assertEqual(doc.images, {})
                self.assertEqual(doc.source_blocks, [])
                render.assert_not_called()
                temp.assert_not_called()

    def test_force_scanned_and_invalid_content_do_not_render(self):
        for stream in (False, True):
            for content, force in ((native_fixture(), True), (b"not a PDF", False)):
                with self.subTest(stream=stream, force=force), \
                        patch.object(pdf_parser.PDFScannedParser, "parse_into_text") as render, \
                        patch.object(pdf_parser.PDFScannedParser, "parse_stream") as render_stream:
                    doc = self.parse(content, stream=stream, pdf_force_scanned=force)
                    self.assertEqual(doc.metadata["pdf_native_only"], "requires_ocr")
                    render.assert_not_called()
                    render_stream.assert_not_called()

    def test_global_force_scanned_is_respected(self):
        with patch.object(pdf_parser, "FORCE_SCANNED_PDF", True):
            self.assertEqual(self.parse(native_fixture()).metadata["pdf_native_only"], "requires_ocr")

    def test_blank_document_is_not_accepted(self):
        self.assertEqual(self.parse(native_fixture(blank=True), pdf_force_scanned=False).metadata["pdf_native_only"],
                         "requires_ocr")

    def test_password_protected_pdf_is_not_accepted(self):
        from pypdf import PdfReader, PdfWriter

        writer = PdfWriter()
        writer.append(PdfReader(io.BytesIO(native_fixture())))
        writer.encrypt("private-test-password")
        encrypted = io.BytesIO()
        writer.write(encrypted)
        with patch.object(pdf_parser, "_render_scanned_pages") as render:
            doc = self.parse(encrypted.getvalue(), pdf_force_scanned=False)
            self.assertEqual(doc.metadata["pdf_native_only"], "requires_ocr")
            render.assert_not_called()

    def test_native_probe_survives_unary_and_stream_rpc(self):
        from docreader.main import DocReaderServicer
        from docreader.proto.docreader_pb2 import ReadConfig, ReadRequest, ReadResponse, ReadStreamResponse
        from docreader.tests.test_docreader_stream import _NoopPreprocessRunner

        config = ReadConfig(parser_engine="builtin", parser_engine_overrides={
            "pdf_native_only": "true", "pdf_force_scanned": "false",
        })
        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        try:
            request = ReadRequest(file_content=native_fixture(), file_name="report.pdf", file_type="pdf", config=config)
            unary = ReadResponse.FromString(service.Read(request, None).SerializeToString())
            frames = [ReadStreamResponse.FromString(f.SerializeToString()) for f in service.ReadStream(request, None)]
            self.assertFalse(unary.error)
            self.assertEqual(unary.metadata["pdf_native_only"], "accepted")
            self.assertTrue(unary.image_refs)
            self.assertTrue(unary.source_blocks)
            self.assertEqual(frames[0].meta.markdown_content, unary.markdown_content)
            self.assertEqual(list(frames[0].meta.source_blocks), list(unary.source_blocks))
            self.assertEqual(dict(frames[0].meta.metadata), dict(unary.metadata))
            self.assertEqual([f.image for f in frames[1:]], list(unary.image_refs))
            request.file_content = native_fixture(scanned=True)
            with patch.object(pdf_parser, "_render_scanned_pages") as render:
                rejected = service.Read(request, None)
                stream_rejected = list(service.ReadStream(request, None))
                self.assertFalse(rejected.error)
                self.assertEqual(rejected.metadata["pdf_native_only"], "requires_ocr")
                self.assertFalse(rejected.markdown_content)
                self.assertFalse(stream_rejected[0].meta.error)
                self.assertEqual(stream_rejected[0].meta.metadata["pdf_native_only"], "requires_ocr")
                self.assertEqual(len(stream_rejected), 1)
                self.assertEqual(stream_rejected[0].meta.image_count, 0)
                self.assertFalse(rejected.image_refs)
                render.assert_not_called()
            # An ordinary empty parse still fails: the rejection marker is
            # specific to the native-only probe, not a general empty success.
            request.config.parser_engine_overrides["pdf_native_only"] = "false"
            request.file_content = native_fixture(blank=True)
            self.assertTrue(service.Read(request, None).error)
            self.assertTrue(list(service.ReadStream(request, None))[0].meta.error)
        finally:
            service.close()
