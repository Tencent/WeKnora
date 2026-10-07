"""Actual PDFium parsing through source blocks and the protobuf JSON boundary."""
import json
import unittest
from unittest.mock import patch

from docreader.parser.pdf_parser import PDFParser
from docreader.source_wire import source_blocks_to_proto
from docreader.tests.test_pdf_embedded_images import _pdf_from_objects


def pdf_fixture(pages):
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        ("<< /Type /Pages /Count %d /Kids [%s] >>" % (
            len(pages), " ".join(f"{4+i*2} 0 R" for i in range(len(pages)))
        )).encode(),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ]
    for i, page in enumerate(pages):
        stream = "\n".join(f"BT /F1 14 Tf {x} {y} Td ({text}) Tj ET" for text, x, y in page["lines"]).encode()
        objects.extend([
            (f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Rotate {page.get('rotation', 0)} "
             f"{page.get('crop', '')} /Resources << /Font << /F1 3 0 R >> >> /Contents {5+i*2} 0 R >>").encode(),
            f"<< /Length {len(stream)} >>\nstream\n".encode() + stream + b"\nendstream",
        ])
    return _pdf_from_objects(objects)


class NativePDFSourceTest(unittest.TestCase):
    def parse(self, pages):
        with patch("docreader.parser.pdf_parser.SOURCE_BOXES", True):
            doc = PDFParser(file_name="source-regression.pdf", file_type="pdf", pdf_force_scanned=False).parse_into_text(pdf_fixture(pages))
        self.assertFalse(doc.metadata.get("scanned_page_count"))
        return doc

    def test_page_and_region_survive_actual_parser_and_wire(self):
        doc = self.parse([
            {"lines": [("First page has independent original evidence.", 60, 700)]},
            {"lines": [("Second page upper paragraph is unrelated.", 60, 700),
                       ("Second page lower evidence: the limit is 1.5 mm.", 60, 400)]},
        ])
        at = doc.content.index("Second page lower evidence")
        source = [b for b in doc.source_blocks if b["start"] <= at < b["end"]]
        self.assertEqual(len(source), 1)
        loc = source[0]["locator"]
        self.assertEqual(loc["page"], 2)
        self.assertEqual(loc["mapping"], "exact")
        self.assertGreater(loc["bbox"][1], .45)
        self.assertLess(loc["bbox"][3], .55)
        self.assertIn("1.5 mm", doc.content[source[0]["start"]:source[0]["end"]])
        self.assertIn(loc, [json.loads(b.locator_json) for b in source_blocks_to_proto(doc)])

    def test_cropbox_and_rotation_use_displayed_page_coordinates(self):
        for rotation in (0, 90, 180, 270):
            with self.subTest(rotation=rotation):
                doc = self.parse([{"rotation": rotation, "crop": "/CropBox [100 100 500 700]",
                                  "lines": [("Cropped and rotated source evidence.", 150, 600)]}])
                loc = next(b["locator"] for b in doc.source_blocks if b["locator"].get("bbox"))
                x0, y0, x1, y1 = loc["bbox"]
                self.assertTrue(0 <= x0 < x1 <= 1 and 0 <= y0 < y1 <= 1)
                if rotation == 0: self.assertLess(y1, .2)
                elif rotation == 90: self.assertGreater(x0, .8)
                elif rotation == 180: self.assertGreater(y0, .8)
                else: self.assertLess(x1, .2)

    def test_identical_paragraphs_keep_distinct_vertical_regions(self):
        text = "Repeated paragraph with identical original evidence."
        doc = self.parse([{"lines": [(text, 60, 700), (text, 60, 400)]}])
        boxed = [b for b in doc.source_blocks if b["locator"].get("bbox")]
        self.assertEqual(len(boxed), 2)
        self.assertNotEqual(boxed[0]["locator"]["source_id"], boxed[1]["locator"]["source_id"])
        self.assertLess(boxed[0]["locator"]["bbox"][1], .2)
        self.assertGreater(boxed[1]["locator"]["bbox"][1], .45)
        self.assertIn(text.rstrip('.'), doc.content[boxed[1]["start"]:boxed[1]["end"]])


if __name__ == "__main__":
    unittest.main()
