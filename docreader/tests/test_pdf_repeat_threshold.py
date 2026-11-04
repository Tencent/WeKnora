"""The repeated-image threshold is a minimum page share, not a rounded-down count."""
import unittest
from dataclasses import replace
from unittest.mock import patch

from docreader.parser import pdf_parser
from docreader.parser.pdf_parser import PDFParser, _select_embedded_images
from docreader.tests.test_pdf_embedded_images import _pdf_from_objects


def _meta(n):
    return [{"page": i, "width": 160, "height": 160, "area_ratio": 0.08, "hash": "same"} for i in range(n)]


def _sample_pdf(count=2, scanned=False, blank=False):
    # One embedded 160x160 image, displayed on selected pages. All bytes and
    # page text are synthetic; shared object reproduces an identical image.
    pixels = bytes((20, 40, 200)) * (160 * 160)
    page_ids = [5 + 2 * i for i in range(5)]
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        ("<< /Type /Pages /Count 5 /Kids [%s] >>" % " ".join(f"{i} 0 R" for i in page_ids)).encode(),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        (b"<< /Type /XObject /Subtype /Image /Width 160 /Height 160 "
         b"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Length %d >>\nstream\n" % len(pixels)
         + pixels + b"\nendstream"),
    ]
    for page_index, page_id in enumerate(page_ids):
        contents = b""
        if scanned and page_index >= 3:
            contents = b"q 600 0 0 800 0 0 cm /Im0 Do Q\n"
        elif not blank or page_index < 2:
            contents = (b"BT /F1 12 Tf 50 750 Td (This page describes technical setup "
                        b"and operating details for all users.) Tj ET\n")
            if page_index < count:
                contents += b"q 200 0 0 200 50 450 cm /Im0 Do Q\n"
        objects.extend([
            (f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Contents {page_id + 1} 0 R "
             "/Resources << /Font << /F1 3 0 R >> /XObject << /Im0 4 0 R >> >> >>").encode(),
            b"<< /Length %d >>\nstream\n" % len(contents) + contents + b"endstream",
        ])
    return _pdf_from_objects(objects)


class RepeatedImageThresholdTest(unittest.TestCase):
    def test_minimum_two_and_odd_page_boundaries(self):
        for pages, appearances, expected in ((2,1,1),(2,2,0),(3,1,1),(3,2,0),(5,2,2),(5,3,0),(7,3,3),(7,4,0)):
            with self.subTest(pages=pages, appearances=appearances):
                self.assertEqual(len(_select_embedded_images(_meta(appearances), pages)), expected)

    def test_zero_one_and_decimal_fraction_boundaries(self):
        for pages, count, frac, expected in ((5,1,0,1),(5,2,0,0),(5,4,1,4),(5,5,1,0),
                                             (100,6,.07,6),(100,7,.07,0),(25,6,.28,6),(25,7,.28,0)):
            with self.subTest(pages=pages, count=count, frac=frac):
                self.assertEqual(len(_select_embedded_images(_meta(count), pages, repeat_frac=frac)), expected)

    def test_same_page_duplicates_do_not_count_as_cross_page(self):
        items = _meta(2)
        items[1]["page"] = 0
        self.assertEqual(_select_embedded_images(items, 5), [0])

    def test_real_five_page_pdf_keeps_two_but_drops_three(self):
        for count, expected in ((2,2),(3,0)):
            with self.subTest(count=count):
                doc = PDFParser(file_name="manual.pdf").parse(_sample_pdf(count))
                self.assertEqual(doc.metadata["text_page_count"], 5)
                self.assertEqual(doc.metadata["scanned_page_count"], 0)
                self.assertEqual(doc.metadata["embedded_image_count"], expected)
                self.assertEqual(len(doc.images), expected)

    def test_scanned_pages_excluded_and_blank_pages_still_count(self):
        with patch.object(pdf_parser, "CONFIG", replace(pdf_parser.CONFIG, pdf_render_parallelism=1)):
            mixed = PDFParser(file_name="mixed.pdf").parse(_sample_pdf(scanned=True))
        self.assertEqual(mixed.metadata["text_page_count"], 3)
        self.assertEqual(mixed.metadata["scanned_page_count"], 2)
        self.assertEqual(mixed.metadata["embedded_image_count"], 0)
        blank = PDFParser(file_name="blank.pdf").parse(_sample_pdf(blank=True))
        self.assertEqual(blank.metadata["text_page_count"], 5)
        self.assertEqual(blank.metadata["embedded_image_count"], 2)


if __name__ == "__main__":
    unittest.main()
