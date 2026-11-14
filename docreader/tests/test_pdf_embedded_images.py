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


def _build_same_page_figures() -> bytes:
    contents = (
        b"0 0 10 10 re f\n"
        b"q 200 0 0 150 20 20 cm /Bottom Do Q\n"
        b"q 200 0 0 150 20 400 cm /Top Do Q\n"
    )
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        b"/Resources << /XObject << /Bottom 5 0 R /Top 6 0 R >> >> /Contents 4 0 R >>",
        b"<< /Length %d >>\nstream\n" % len(contents) + contents + b"endstream",
    ]
    for color in ((20, 30, 210), (200, 30, 40)):
        pixels = bytes(color) * (400 * 300)
        objects.append(
            b"<< /Type /XObject /Subtype /Image /Width 400 /Height 300 "
            b"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Length %d >>\nstream\n" % len(pixels)
            + pixels + b"\nendstream"
        )
    return _pdf_from_objects(objects)


class _TrackedPDF:
    def __init__(self, pdf):
        self.pdf = pdf
        self.pages = []

    def __getitem__(self, index):
        page = self.pdf[index]
        self.pages.append(page)
        return page


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
    def test_same_page_figures_keep_pixels_references_and_vertical_order(self):
        with pdfium.PdfDocument(_build_same_page_figures()) as pdf:
            result = _extract_embedded_images(pdf, ["text"], pdfium_raw, "figures", 85)
        top, bottom = result[0]
        self.assertTrue(top[0].endswith("_p1_img2.jpg"))
        self.assertTrue(bottom[0].endswith("_p1_img1.jpg"))
        self.assertGreater(top[2], bottom[2])
        for item, channel in ((top, 0), (bottom, 2)):
            with Image.open(io.BytesIO(base64.b64decode(item[1]))) as pil:
                self.assertGreater(pil.getpixel((200, 150))[channel], 150)

    def _two_figures(self):
        buf = io.BytesIO()
        with Image.new("RGB", (400, 300), (10, 120, 230)) as first:
            with Image.new("RGB", (400, 300), (230, 120, 10)) as second:
                first.save(buf, format="PDF", save_all=True, append_images=[second])
        return pdfium.PdfDocument(buf.getvalue())

    def test_candidates_release_pixels_before_decoding_the_next_figure(self):
        decoded = []

        def decode(obj):
            for previous in decoded:
                with self.assertRaises(ValueError):
                    previous.tobytes()
            pil = _decode_embedded_image_pil(obj)
            decoded.append(pil)
            return pil

        with self._two_figures() as pdf:
            with patch("docreader.parser.pdf_parser._decode_embedded_image_pil", side_effect=decode):
                result = _extract_embedded_images(pdf, ["text", "text"], pdfium_raw, "figures", 85)
        self.assertEqual(set(result), {0, 1})
        self.assertEqual(len(decoded), 4)
        for pil in decoded:
            with self.assertRaises(ValueError):
                pil.tobytes()
        for page in result.values():
            with Image.open(io.BytesIO(base64.b64decode(page[0][1]))) as pil:
                self.assertEqual(pil.size, (400, 300))

    def test_filtered_candidates_release_pixels(self):
        decoded = []

        def decode(obj):
            pil = _decode_embedded_image_pil(obj)
            decoded.append(pil)
            return pil

        with self._two_figures() as pdf:
            with patch("docreader.parser.pdf_parser._decode_embedded_image_pil", side_effect=decode):
                with patch("docreader.parser.pdf_parser._select_embedded_images", return_value=[]):
                    result = _extract_embedded_images(pdf, ["text", "text"], pdfium_raw, "filtered", 85)
        self.assertEqual(result, {})
        self.assertEqual(len(decoded), 2)
        for pil in decoded:
            with self.assertRaises(ValueError):
                pil.tobytes()

    def test_hash_failure_releases_pixels_and_page(self):
        decoded = []

        def decode(obj):
            pil = _decode_embedded_image_pil(obj)
            decoded.append(pil)
            return pil

        with self._two_figures() as pdf:
            tracked = _TrackedPDF(pdf)
            with patch("docreader.parser.pdf_parser._decode_embedded_image_pil", side_effect=decode):
                with patch("hashlib.md5", side_effect=RuntimeError("hash failed")):
                    with self.assertRaisesRegex(RuntimeError, "hash failed"):
                        _extract_embedded_images(tracked, ["text", "text"], pdfium_raw, "hash", 85)
            self.assertEqual(len(decoded), 1)
            with self.assertRaises(ValueError):
                decoded[0].tobytes()
            self.assertTrue(tracked.pages)
            self.assertTrue(all(page.raw is None for page in tracked.pages))
            page = pdf[0]
            try:
                self.assertEqual(page.get_size(), (400.0, 300.0))
            finally:
                page.close()

    def test_jpeg_failure_releases_selected_pixels_and_page(self):
        decoded = []

        def decode(obj):
            pil = _decode_embedded_image_pil(obj)
            decoded.append(pil)
            return pil

        with self._two_figures() as pdf:
            tracked = _TrackedPDF(pdf)
            with patch("docreader.parser.pdf_parser._decode_embedded_image_pil", side_effect=decode):
                with patch.object(Image.Image, "save", side_effect=RuntimeError("encode failed")):
                    with self.assertRaisesRegex(RuntimeError, "encode failed"):
                        _extract_embedded_images(tracked, ["text", "text"], pdfium_raw, "encode", 85)
            self.assertEqual(len(decoded), 3)
            for pil in decoded:
                with self.assertRaises(ValueError):
                    pil.tobytes()
            self.assertTrue(tracked.pages)
            self.assertTrue(all(page.raw is None for page in tracked.pages))
            page = pdf[0]
            try:
                self.assertEqual(page.get_size(), (400.0, 300.0))
            finally:
                page.close()

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
