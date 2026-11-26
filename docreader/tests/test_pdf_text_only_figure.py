"""Keep ambiguous source text on native text-only PDF pages."""
import io
import json
import unittest
from unittest.mock import patch

from PIL import Image
import pypdfium2 as pdfium
import pypdfium2.raw as raw

from docreader.parser.pdf_parser import PDFParser, _page_is_text_only
from docreader.tests.test_pdf_embedded_images import _pdf_from_objects


def _pdf(pages):
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        ("<< /Type /Pages /Count %d /Kids [%s] >>" % (
            len(pages), " ".join(f"{4 + i * 2} 0 R" for i in range(len(pages)))
        )).encode(),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ]
    for i, (lines, has_line) in enumerate(pages):
        stream = "\n".join(
            f"BT /F1 12 Tf 50 {750 - n * 25} Td ({text}) Tj ET"
            for n, text in enumerate(lines)
        )
        if has_line:
            stream += "\n50 700 m 160 700 l S"
        stream = stream.encode()
        objects.extend([
            (f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] "
             f"/Resources << /Font << /F1 3 0 R >> >> /Contents {5 + i * 2} 0 R >>").encode(),
            f"<< /Length {len(stream)} >>\nstream\n".encode() + stream + b"\nendstream",
        ])
    return _pdf_from_objects(objects)


class TextOnlyFigureTest(unittest.TestCase):
    def test_real_pdf_preserves_text_and_keeps_figure_crops(self):
        content = _pdf([
            (["Product details and identifiers", "Device model AB-123", "Figure 1. System overview"], False),
            (["identity", "weight layer", "Figure 2. Network diagram"], True),
            (["Other business text", "Sensor model ZX-987", "Figure 3. Product drawing"], False),
        ])
        pdf = pdfium.PdfDocument(content)
        try:
            self.assertTrue(_page_is_text_only(pdf[0], raw))
            self.assertFalse(_page_is_text_only(pdf[1], raw))
            self.assertTrue(_page_is_text_only(pdf[2], raw))
        finally:
            pdf.close()
        doc = PDFParser(file_name="sample.pdf").parse(content)
        for value, page in (("Device model AB-123", 1), ("Sensor model ZX-987", 3)):
            self.assertIn(value, doc.content)
            block = next(b for b in doc.source_blocks if value in doc.content[b["start"]:b["end"]])
            self.assertEqual(block["locator"]["page"], page)
        self.assertNotIn("identity", doc.content)
        self.assertNotIn("weight layer", doc.content)
        for n in (1, 2, 3):
            self.assertIn(f"sample_p{n}_fig1.jpg", doc.content)

    def test_font_only_diagram_keeps_text_and_figure_crop(self):
        content = _pdf([(["alpha ---> beta", "Figure 4. ASCII diagram"], False)])
        doc = PDFParser(file_name="ascii.pdf").parse(content)
        self.assertEqual(doc.content.count("alpha"), 1)
        self.assertIn("ascii_p1_fig1.jpg", doc.content)

    def test_unrelated_text_page_skips_extra_object_inspection(self):
        content = _pdf([(["Normal unrelated document text without a chart"], False)])
        with patch("docreader.parser.pdf_parser._page_is_text_only", side_effect=AssertionError):
            doc = PDFParser(file_name="plain.pdf").parse(content)
        self.assertIn("Normal unrelated", doc.content)

    def test_non_text_or_failed_inspection_keeps_existing_behavior(self):
        class Obj:
            type = 999
        class Page:
            def get_objects(self):
                return iter([Obj()])
        class FailingPage:
            def get_objects(self):
                raise RuntimeError("bad")
        self.assertFalse(_page_is_text_only(Page(), raw))
        self.assertFalse(_page_is_text_only(FailingPage(), raw))

    def test_scanned_page_still_uses_scanned_image_path(self):
        buf = io.BytesIO()
        Image.new("RGB", (200, 200), "white").save(buf, format="PDF")
        doc = PDFParser(file_name="scan.pdf").parse(buf.getvalue())
        self.assertTrue(doc.images)
        self.assertIn("scanned_pdf", json.loads(doc.metadata["image_source_types"]).values())


if __name__ == "__main__":
    unittest.main()
