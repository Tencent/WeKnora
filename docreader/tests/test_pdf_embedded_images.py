"""Embedded-figure extraction from native PDF pages.

Focus: images whose visible content lives in a /SMask (soft transparency
mask) while the base RGB plane is black. ``get_bitmap()`` alone decodes only
the base plane, producing an all-black JPEG; the decode path must render the
mask in and composite over white. Reproduces the all-black figures extracted
from a production PDF exported by a plotting tool.
"""

import base64
import gc
import io
import unittest
from unittest.mock import patch

import pypdfium2 as pdfium
import pypdfium2.raw as pdfium_raw
from PIL import Image

from docreader.parser.pdf_parser import (
    _decode_embedded_image_pil,
    _extract_embedded_images,
)

PAGE_W, PAGE_H = 612, 792
IMG_W, IMG_H = 120, 90


def _pdf_from_objects(objects: list) -> bytes:
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


def _page_objects(image_obj: bytes, extra_objs: list | None = None) -> list:
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
        image_obj,
    ]
    if extra_objs:
        objects.extend(extra_objs)
    return objects


def _circle_mask() -> bytes:
    cx, cy, radius = IMG_W // 2, IMG_H // 2, 30
    mask = bytearray(IMG_W * IMG_H)
    for y in range(IMG_H):
        for x in range(IMG_W):
            if (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius:
                mask[y * IMG_W + x] = 255
    return bytes(mask)


def _build_smask_pdf(base_rgb=(0, 0, 0)) -> bytes:
    """Hand-built PDF: one RGB image shaped by an SMask circle.

    The SMask is opaque (255) inside a filled circle and transparent (0)
    elsewhere. A decoder that drops the mask shows the raw base plane only.
    """
    r, g, b = base_rgb
    base = bytes((r, g, b)) * (IMG_W * IMG_H)
    mask = _circle_mask()
    image = (
        b"<< /Type /XObject /Subtype /Image /Width %d /Height %d "
        b"/ColorSpace /DeviceRGB /BitsPerComponent 8 /SMask 6 0 R "
        b"/Length %d >>\nstream\n" % (IMG_W, IMG_H, len(base))
        + base
        + b"\nendstream"
    )
    mask_obj = (
        b"<< /Type /XObject /Subtype /Image /Width %d /Height %d "
        b"/ColorSpace /DeviceGray /BitsPerComponent 8 /Length %d >>\nstream\n"
        % (IMG_W, IMG_H, len(mask))
        + mask
        + b"\nendstream"
    )
    return _pdf_from_objects(_page_objects(image, [mask_obj]))


def _build_opaque_gray_pdf(level: int = 128) -> bytes:
    data = bytes((level,)) * (IMG_W * IMG_H)
    image = (
        b"<< /Type /XObject /Subtype /Image /Width %d /Height %d "
        b"/ColorSpace /DeviceGray /BitsPerComponent 8 /Length %d >>\nstream\n"
        % (IMG_W, IMG_H, len(data))
        + data
        + b"\nendstream"
    )
    return _pdf_from_objects(_page_objects(image))


def _first_image_object(pdf):
    for page in pdf:
        for obj in page.get_objects():
            if obj.type == pdfium_raw.FPDF_PAGEOBJ_IMAGE:
                return obj
    raise AssertionError("test PDF has no image object")


def _raw_decode(obj):
    bitmap = obj.get_bitmap()
    try:
        pil = bitmap.to_pil()
        if pil.mode in ("RGB", "L"):
            return pil.copy()
        return pil.convert("RGB")
    finally:
        bitmap.close()


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

    def test_colored_smask_keeps_base_color(self):
        pdf = pdfium.PdfDocument(_build_smask_pdf(base_rgb=(200, 30, 40)))
        try:
            pil = _decode_embedded_image_pil(_first_image_object(pdf))
        finally:
            pdf.close()
        self.assertEqual(pil.mode, "RGB")
        self.assertGreater(pil.getpixel((2, 2))[0], 200)
        center = pil.getpixel((IMG_W // 2, IMG_H // 2))
        self.assertGreater(center[0], 150)
        self.assertLess(center[1], 80)
        self.assertLess(center[2], 80)

    def test_opaque_image_matches_raw_pixels(self):
        buf = io.BytesIO()
        Image.new("RGB", (100, 80), (10, 200, 30)).save(buf, format="PDF")
        pdf = pdfium.PdfDocument(buf.getvalue())
        try:
            obj = _first_image_object(pdf)
            raw = _raw_decode(obj)
            pil = _decode_embedded_image_pil(obj)
        finally:
            pdf.close()
        self.assertEqual(pil.size, raw.size)
        self.assertEqual(pil.mode, raw.mode)
        self.assertEqual(pil.tobytes(), raw.tobytes())

    def test_opaque_gray_fallback_keeps_l_mode(self):
        # render=True typically returns BGRA even for DeviceGray; the raw
        # fallback must keep L so grayscale hashes/JPEGs stay single-channel.
        pdf = pdfium.PdfDocument(_build_opaque_gray_pdf(128))
        try:
            obj = _first_image_object(pdf)
            raw = _raw_decode(obj)
            self.assertEqual(raw.mode, "L")
            original = obj.get_bitmap

            def _boom(render=False, **kwargs):
                if render:
                    raise RuntimeError("render unavailable")
                return original(render=render, **kwargs)

            with patch.object(obj, "get_bitmap", side_effect=_boom):
                with self.assertLogs("docreader.parser.pdf_parser", level="WARNING"):
                    pil = _decode_embedded_image_pil(obj)
        finally:
            pdf.close()
        self.assertEqual(pil.mode, "L")
        self.assertEqual(pil.getpixel((IMG_W // 2, IMG_H // 2)), 128)
        self.assertEqual(pil.tobytes(), raw.tobytes())

    def test_decoded_image_survives_bitmap_gc(self):
        buf = io.BytesIO()
        Image.new("RGB", (100, 80), (10, 200, 30)).save(buf, format="PDF")
        pdf = pdfium.PdfDocument(buf.getvalue())
        try:
            pil = _decode_embedded_image_pil(_first_image_object(pdf))
        finally:
            pdf.close()
        gc.collect()
        self.assertEqual(pil.getpixel((50, 40)), (10, 200, 30))

    def test_render_failure_falls_back_to_raw(self):
        buf = io.BytesIO()
        Image.new("RGB", (100, 80), (10, 200, 30)).save(buf, format="PDF")
        pdf = pdfium.PdfDocument(buf.getvalue())
        try:
            obj = _first_image_object(pdf)
            raw = _raw_decode(obj)
            original = obj.get_bitmap

            def _boom(render=False, **kwargs):
                if render:
                    raise RuntimeError("render unavailable")
                return original(render=render, **kwargs)

            with patch.object(obj, "get_bitmap", side_effect=_boom):
                with self.assertLogs(
                    "docreader.parser.pdf_parser", level="WARNING"
                ) as logs:
                    pil = _decode_embedded_image_pil(obj)
        finally:
            pdf.close()
        self.assertTrue(any("falling back to raw decode" in m for m in logs.output))
        self.assertEqual(pil.mode, raw.mode)
        self.assertEqual(pil.tobytes(), raw.tobytes())


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
