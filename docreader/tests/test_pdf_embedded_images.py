"""Embedded-figure extraction from native PDF pages.

Focus: images whose visible content lives in a /SMask (soft transparency
mask) while the base RGB plane is black. ``get_bitmap()`` alone decodes only
the base plane, producing an all-black JPEG; the decode path must render the
mask in and composite over white. Reproduces the all-black figures extracted
from a production PDF exported by a plotting tool.
"""

import base64
import io
import unittest

import pypdfium2 as pdfium
import pypdfium2.raw as pdfium_raw
from PIL import Image

from docreader.parser.pdf_parser import (
    _decode_embedded_image_pil,
    _extract_embedded_images,
)

PAGE_W, PAGE_H = 612, 792
IMG_W, IMG_H = 120, 90


def _build_smask_pdf() -> bytes:
    """Hand-built PDF: one black RGB image shaped by an SMask circle.

    The base plane is entirely black; the SMask is opaque (255) inside a
    filled circle and transparent (0) elsewhere, so a viewer shows a black
    circle on the white page — and a decoder that drops the mask shows pure
    black.
    """
    base = b"\x00\x00\x00" * (IMG_W * IMG_H)
    cx, cy, radius = IMG_W // 2, IMG_H // 2, 30
    mask = bytearray(IMG_W * IMG_H)
    for y in range(IMG_H):
        for x in range(IMG_W):
            if (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius:
                mask[y * IMG_W + x] = 255
    contents = b"q 480 0 0 360 66 216 cm /Im0 Do Q\n"
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        (
            b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] "
            b"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>"
            % (PAGE_W, PAGE_H)
        ),
        b"<< /Length %d >>\nstream\n" % len(contents) + contents + b"endstream",
        (
            b"<< /Type /XObject /Subtype /Image /Width %d /Height %d "
            b"/ColorSpace /DeviceRGB /BitsPerComponent 8 /SMask 6 0 R "
            b"/Length %d >>\nstream\n" % (IMG_W, IMG_H, len(base))
            + base
            + b"\nendstream"
        ),
        (
            b"<< /Type /XObject /Subtype /Image /Width %d /Height %d "
            b"/ColorSpace /DeviceGray /BitsPerComponent 8 /Length %d >>\nstream\n"
            % (IMG_W, IMG_H, len(mask))
            + bytes(mask)
            + b"\nendstream"
        ),
    ]
    out = bytearray(b"%PDF-1.7\n")
    offsets = []
    for i, body in enumerate(objects, start=1):
        offsets.append(len(out))
        out += b"%d 0 obj\n" % i + body + b"\nendobj\n"
    xref_pos = len(out)
    out += b"xref\n0 %d\n" % (len(objects) + 1)
    out += b"0000000000 65535 f \n"
    for off in offsets:
        out += b"%010d 00000 n \n" % off
    out += b"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (
        len(objects) + 1,
        xref_pos,
    )
    return bytes(out)


def _first_image_object(pdf):
    for page in pdf:
        for obj in page.get_objects():
            if obj.type == pdfium_raw.FPDF_PAGEOBJ_IMAGE:
                return obj
    raise AssertionError("test PDF has no image object")


class DecodeEmbeddedImageTest(unittest.TestCase):
    def test_smask_image_composites_over_white(self):
        pdf = pdfium.PdfDocument(_build_smask_pdf())
        try:
            pil = _decode_embedded_image_pil(_first_image_object(pdf))
        finally:
            pdf.close()
        self.assertEqual(pil.mode, "RGB")
        # Transparent corners become white page background, not black.
        self.assertGreater(pil.getpixel((2, 2))[0], 200)
        self.assertGreater(pil.getpixel((IMG_W - 3, IMG_H - 3))[0], 200)
        # Opaque circle center keeps the base-plane colour (black).
        self.assertLess(pil.getpixel((IMG_W // 2, IMG_H // 2))[0], 40)

    def test_opaque_image_without_mask_unchanged(self):
        buf = io.BytesIO()
        Image.new("RGB", (100, 80), (10, 200, 30)).save(buf, format="PDF")
        pdf = pdfium.PdfDocument(buf.getvalue())
        try:
            pil = _decode_embedded_image_pil(_first_image_object(pdf))
        finally:
            pdf.close()
        self.assertEqual(pil.mode, "RGB")
        self.assertEqual(pil.getpixel((50, 40))[1], 200)


class ExtractEmbeddedImagesTest(unittest.TestCase):
    def test_smask_figure_is_not_all_black(self):
        pdf = pdfium.PdfDocument(_build_smask_pdf())
        try:
            result = _extract_embedded_images(
                pdf, ["text"], pdfium_raw, "smask_doc", 85
            )
        finally:
            pdf.close()
        self.assertIn(0, result)
        ref_path, b64_jpeg, _y_top = result[0][0]
        self.assertTrue(ref_path.startswith("images/smask_doc_p1_img1"))
        pil = Image.open(io.BytesIO(base64.b64decode(b64_jpeg))).convert("RGB")
        total = bright = 0
        for x in range(0, pil.width, 4):
            for y in range(0, pil.height, 4):
                total += 1
                if max(pil.getpixel((x, y))) >= 200:
                    bright += 1
        # Dropping the mask would make this ~0: the whole figure is black.
        self.assertGreater(bright / total, 0.5)


if __name__ == "__main__":
    unittest.main()
