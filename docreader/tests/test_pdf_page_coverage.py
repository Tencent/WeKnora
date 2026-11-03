"""Actual PDFium routing of image bounds against visible page boxes."""

import unittest

from docreader.parser.pdf_parser import PDFParser, _page_image_area_ratio


def _pdf(*, page_box="/MediaBox [0 0 200 200]", commands="", form=False):
    def stream(data, extra=""):
        return (
            f"<< /Length {len(data)} {extra} >>\nstream\n".encode()
            + data
            + b"\nendstream"
        )

    content = (
        commands + "\nBT /F1 12 Tf 120 150 Td (Visible text on the page) Tj ET"
    ).encode()
    form_ref = "/Fm 7 0 R" if form else ""
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        f"<< /Type /Page /Parent 2 0 R {page_box} /Resources << /Font << /F1 5 0 R >> /XObject << /Im1 6 0 R {form_ref} >> >> /Contents 4 0 R >>".encode(),
        stream(content),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        stream(
            b"\x00\x00\x00",
            "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8",
        ),
    ]
    if form:
        objects.append(
            stream(
                b"q 1 0 0 1 0 0 cm /Im1 Do Q",
                "/Type /XObject /Subtype /Form /BBox [0 0 1 1] /Resources << /XObject << /Im1 6 0 R >> >>",
            )
        )
    out = bytearray(b"%PDF-1.4\n")
    offsets = [0]
    for i, obj in enumerate(objects, 1):
        offsets.append(len(out))
        out += f"{i} 0 obj\n".encode() + obj + b"\nendobj\n"
    xref = len(out)
    out += f"xref\n0 {len(offsets)}\n0000000000 65535 f \n".encode()
    for offset in offsets[1:]:
        out += f"{offset:010} 00000 n \n".encode()
    out += f"trailer\n<< /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode()
    return bytes(out)


def _image(width, height, x, y):
    return f"q {width} 0 0 {height} {x} {y} cm /Im1 Do Q"


class PDFPageCoverageTest(unittest.TestCase):
    def _ratio(self, data):
        import pypdfium2 as pdfium
        from pypdfium2 import raw

        pdf = pdfium.PdfDocument(data)
        try:
            page = pdf[0]
            try:
                return _page_image_area_ratio(page, raw)
            finally:
                page.close()
        finally:
            pdf.close()

    def test_off_page_image_does_not_hide_native_text(self):
        data = _pdf(commands=_image(1000, 1000, 1000, 1000))
        self.assertEqual(self._ratio(data), 0.0)
        doc = PDFParser(file_name="native.pdf").parse_into_text(data)
        self.assertEqual(doc.metadata["text_page_count"], 1)
        self.assertEqual(doc.metadata["scanned_page_count"], 0)
        self.assertIn("Visible text", doc.content)
        stream = PDFParser(file_name="native.pdf").parse_stream(data)
        try:
            self.assertEqual(stream.document.metadata["text_page_count"], 1)
            self.assertEqual(stream.document.metadata["scanned_page_count"], 0)
            self.assertIn("Visible text", stream.document.content)
        finally:
            stream.close()

    def test_cropbox_is_offset_and_image_outside_is_ignored(self):
        box = "/MediaBox [0 0 400 400] /CropBox [100 100 300 300]"
        self.assertEqual(
            self._ratio(_pdf(page_box=box, commands=_image(90, 90, 0, 0))), 0.0
        )
        self.assertEqual(
            self._ratio(_pdf(page_box=box, commands=_image(200, 200, 100, 100))), 1.0
        )
        self.assertAlmostEqual(
            self._ratio(_pdf(page_box=box, commands=_image(200, 200, 200, 100))), 0.5
        )

    def test_rotation_keeps_unrotated_page_coordinates(self):
        for rotation in (90, 180, 270):
            with self.subTest(rotation=rotation):
                box = f"/MediaBox [0 0 400 200] /Rotate {rotation}"
                self.assertAlmostEqual(
                    self._ratio(_pdf(page_box=box, commands=_image(100, 100, 350, 0))),
                    0.0625,
                )

    def test_text_only_page_has_zero_image_area(self):
        self.assertEqual(self._ratio(_pdf()), 0.0)

    def test_nested_form_keeps_existing_local_coordinate_fallback(self):
        box = "/MediaBox [0 0 400 400] /CropBox [100 100 300 300]"
        data = _pdf(
            page_box=box, commands="q 200 0 0 200 100 100 cm /Fm Do Q", form=True
        )
        # PDFium returns the child's bounds in form-local coordinates; without
        # the parent transform, clipping them to the page would change behavior.
        self.assertAlmostEqual(self._ratio(data), 1 / 40000)

    def test_full_page_image_keeps_scan_routing(self):
        data = _pdf(commands=_image(200, 200, 0, 0))
        doc = PDFParser(file_name="scan.pdf").parse_into_text(data)
        self.assertEqual(doc.metadata["scanned_page_count"], 1)
        self.assertEqual(doc.metadata["text_page_count"], 0)

    def test_missing_page_box_preserves_legacy_estimate(self):
        from types import SimpleNamespace
        from unittest.mock import Mock

        image = SimpleNamespace(
            type=3, level=0, get_bounds=lambda: (500, 500, 600, 600)
        )
        page = SimpleNamespace(
            get_bbox=Mock(side_effect=ValueError("no page box")),
            get_size=lambda: (200, 200),
            get_objects=lambda: iter([image]),
        )
        self.assertEqual(
            _page_image_area_ratio(page, SimpleNamespace(FPDF_PAGEOBJ_IMAGE=3)), 0.25
        )
