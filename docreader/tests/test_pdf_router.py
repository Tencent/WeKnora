import io
import unittest

from PIL import Image

from docreader.parser.pdf_parser import (
    PDFParser,
    _classify_page,
    _group_lines,
    _point_in_boxes,
    _segments_to_markdown,
    _select_embedded_images,
    _split_columns,
    _strip_repeating_lines,
)


def _char(ch, x0, x1, y0, y1):
    return {"x0": x0, "x1": x1, "y0": y0, "y1": y1, "ch": ch}


def _line(text, h):
    return {"text": text, "h": h}


def _make_image_only_pdf(num_pages: int = 2) -> bytes:
    buf = io.BytesIO()
    pages = [Image.new("RGB", (64, 64), color) for color in ("white", "black")]
    pages = (pages * ((num_pages // 2) + 1))[:num_pages]
    pages[0].save(buf, format="PDF", save_all=True, append_images=pages[1:])
    return buf.getvalue()


class ClassifyPageTest(unittest.TestCase):
    def test_full_page_image_is_scanned_even_with_text(self):
        # Scanned newspaper: image covers the page, embedded OCR text exists.
        self.assertEqual(_classify_page(2.0, 1620), "scanned")
        # Scanned UN doc with garbled OCR text layer (ratio ~1.0).
        self.assertEqual(_classify_page(1.0, 1500), "scanned")

    def test_native_text_page_is_text(self):
        self.assertEqual(_classify_page(0.01, 673), "text")
        self.assertEqual(_classify_page(0.0, 1200), "text")

    def test_sparse_text_with_image_is_scanned(self):
        self.assertEqual(_classify_page(0.3, 2), "scanned")

    def test_blank_page_is_text(self):
        # No image, no text -> not rendered as an image.
        self.assertEqual(_classify_page(0.0, 0), "text")


class StripRepeatingLinesTest(unittest.TestCase):
    def test_removes_repeated_header_footer(self):
        header = "ACME CONFIDENTIAL"
        texts = [f"{header}\nbody page {i}\npage {i} footer" for i in range(6)]
        # Make the footer identical across pages so it is detected.
        texts = [f"{header}\nbody page {i}\nshared footer" for i in range(6)]
        classes = ["text"] * 6
        cleaned = _strip_repeating_lines(texts, classes)
        for page in cleaned:
            self.assertNotIn(header, page)
            self.assertNotIn("shared footer", page)
        self.assertIn("body page 0", cleaned[0])

    def test_keeps_lines_when_too_few_pages(self):
        texts = ["HEADER\nbody"] * 2
        classes = ["text"] * 2
        self.assertEqual(_strip_repeating_lines(texts, classes), texts)


class SelectEmbeddedImagesTest(unittest.TestCase):
    def _fig(self, page, h="fig", w=200, ht=200, area=0.2):
        return {"page": page, "width": w, "height": ht, "area_ratio": area, "hash": h}

    def test_keeps_real_figure(self):
        meta = [self._fig(0, "a")]
        self.assertEqual(_select_embedded_images(meta, 1), [0])

    def test_drops_tiny_and_small_images(self):
        meta = [
            self._fig(0, "tiny_area", area=0.001),  # too small a share
            self._fig(0, "tiny_px", w=20, ht=20),  # too few pixels
        ]
        self.assertEqual(_select_embedded_images(meta, 1), [])

    def test_drops_repeated_logo_watermark(self):
        # Same hash on 5 of 6 text pages -> running logo/watermark.
        meta = [self._fig(p, "logo") for p in range(5)]
        meta.append(self._fig(5, "unique"))
        kept = _select_embedded_images(meta, 6)
        kept_hashes = {meta[i]["hash"] for i in kept}
        self.assertNotIn("logo", kept_hashes)
        self.assertIn("unique", kept_hashes)

    def test_dedups_identical_image_on_same_page(self):
        meta = [self._fig(0, "dup"), self._fig(0, "dup")]
        self.assertEqual(len(_select_embedded_images(meta, 1)), 1)

    def test_respects_max_images_cap(self):
        meta = [self._fig(i, f"h{i}") for i in range(10)]
        self.assertEqual(len(_select_embedded_images(meta, 10, max_images=3)), 3)


class ReadingOrderTest(unittest.TestCase):
    def test_single_column_stays_single(self):
        # One column of glyphs at x~100, no full-height gutter.
        chars = [_char("a", 100, 110, 700 - i * 12, 712 - i * 12) for i in range(5)]
        cols = _split_columns(chars, scale=12.0, width=600.0)
        self.assertEqual(len(cols), 1)

    def test_two_columns_split_left_to_right(self):
        # Left column x~50-150, right column x~400-500, wide empty gutter between.
        left = [_char("L", 50, 150, 700 - i * 12, 712 - i * 12) for i in range(4)]
        right = [_char("R", 400, 500, 700 - i * 12, 712 - i * 12) for i in range(4)]
        cols = _split_columns(left + right, scale=12.0, width=600.0)
        self.assertEqual(len(cols), 2)
        # Reading order: left column before right column.
        self.assertEqual(cols[0][0]["ch"], "L")
        self.assertEqual(cols[1][0]["ch"], "R")

    def test_group_lines_orders_by_y_then_x(self):
        # Two visual lines; within a line glyphs given out of x-order.
        chars = [
            _char("B", 120, 130, 700, 712),
            _char("A", 100, 110, 700, 712),  # same line, left of B
            _char("C", 100, 110, 680, 692),  # next line down
        ]
        lines = _group_lines(chars)
        self.assertEqual([ln["text"] for ln in lines], ["AB", "C"])


class HeadingDetectionTest(unittest.TestCase):
    def test_promotes_large_line_to_heading(self):
        lines = [_line("Big Title", 24.0)] + [_line(f"body {i}", 10.0) for i in range(6)]
        md = _segments_to_markdown(lines)
        self.assertTrue(md.startswith("# Big Title"))
        self.assertIn("\nbody 0", md)

    def test_does_not_promote_when_sizes_uniform(self):
        lines = [_line(f"line {i}", 10.0) for i in range(6)]
        md = _segments_to_markdown(lines)
        self.assertNotIn("#", md)

    def test_skips_sentence_like_long_lines(self):
        # Large but ends with a period and is long -> body text, not a heading.
        lines = [_line("x" * 90 + ".", 30.0)] + [_line("y", 10.0) for _ in range(6)]
        md = _segments_to_markdown(lines)
        self.assertFalse(md.startswith("#"))


class HiddenTextFilterTest(unittest.TestCase):
    def test_point_in_boxes(self):
        boxes = [(0.0, 0.0, 10.0, 10.0)]
        self.assertTrue(_point_in_boxes(5.0, 5.0, boxes))
        self.assertFalse(_point_in_boxes(20.0, 5.0, boxes))


class PDFRouterIntegrationTest(unittest.TestCase):
    def test_image_only_pdf_routes_to_scanned(self):
        pdf_bytes = _make_image_only_pdf(2)
        doc = PDFParser(file_name="imgonly.pdf", file_type="pdf").parse_into_text(
            pdf_bytes
        )

        self.assertEqual(doc.metadata["image_source_type"], "scanned_pdf")
        self.assertEqual(doc.metadata["page_count"], 2)
        self.assertEqual(doc.metadata["scanned_page_count"], 2)
        self.assertEqual(len(doc.images), 2)
        self.assertIn("images/imgonly_page_1.jpg", doc.images)
        self.assertIn("![imgonly_page_1.jpg](images/imgonly_page_1.jpg)", doc.content)
        # JPEG magic bytes after decoding.
        import base64

        self.assertTrue(
            base64.b64decode(doc.images["images/imgonly_page_1.jpg"]).startswith(
                b"\xff\xd8"
            )
        )

    def test_malformed_pdf_raises_after_fallback(self):
        # Routing fails to open the PDF, falls back to full rendering which also
        # fails on garbage input; the error surfaces to the caller.
        with self.assertRaises(Exception):
            PDFParser(file_name="broken.pdf", file_type="pdf").parse_into_text(
                b"not a pdf"
            )


if __name__ == "__main__":
    unittest.main()
