import io
import unittest

from PIL import Image

from docreader.parser.pdf_parser import (
    PDFParser,
    _classify_page,
    _filter_reading_columns,
    _group_lines,
    _is_artifact_column,
    _chars_to_layout_markdown,
    _join_line_glyphs,
    _looks_like_numeric_table_columns,
    _merge_orphan_punctuation_lines,
    _page_chars,
    _point_in_boxes,
    _postprocess_pdf_text,
    _segments_to_markdown,
    _select_embedded_images,
    _should_prefer_plain,
    _split_columns,
    _strip_repeating_lines,
    _tail_looks_like_numeric_table,
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
            _char("B", 110, 120, 700, 712),  # adjacent to A (no word-sized gap)
            _char("A", 100, 110, 700, 712),
            _char("C", 100, 110, 680, 692),  # next line down
        ]
        lines = _group_lines(chars)
        self.assertEqual([ln["text"] for ln in lines], ["AB", "C"])

    def test_join_line_glyphs_inserts_word_spaces(self):
        # Wide gap between "copy" and "of" mimics positioned OCR / text layers.
        chars = [
            _char("c", 0, 4, 0, 10),
            _char("f", 10, 14, 0, 10),
        ]
        self.assertEqual(_join_line_glyphs(chars), "c f")

    def test_join_line_glyphs_keeps_adjacent_letters(self):
        chars = [_char("A", 100, 110, 700, 712), _char("B", 110, 120, 700, 712)]
        self.assertEqual(_join_line_glyphs(chars), "AB")

    def test_join_line_glyphs_keeps_kerning_gaps_inside_numbers(self):
        # Right-aligned PDF table cells often add a small gap between digit
        # glyphs.  It is not a word boundary.
        chars = [
            _char("1", 0, 4, 0, 10),
            _char("2", 7, 11, 0, 10),
            _char("4", 14, 18, 0, 10),
        ]
        self.assertEqual(_join_line_glyphs(chars), "124")

    def test_join_line_glyphs_keeps_large_numeric_cell_gap(self):
        chars = [
            _char("1", 0, 4, 0, 10),
            _char("2", 20, 24, 0, 10),
        ]
        self.assertEqual(_join_line_glyphs(chars), "1 2")


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

    def test_page_chars_reports_filtered_off_page_glyphs(self):
        class FakeTextPage:
            chars = [
                ("visible", (10.0, 10.0, 20.0, 20.0)),
                ("hidden", (-20.0, 10.0, -10.0, 20.0)),
            ]

            def count_chars(self):
                return len(self.chars)

            def get_charbox(self, index):
                return self.chars[index][1]

            def get_text_range(self, index, length=1):
                return self.chars[index][0][0]

        class FakePage:
            def get_size(self):
                return 100.0, 100.0

            def get_objects(self):
                return []

        class FakeRaw:
            FPDF_PAGEOBJ_TEXT = 1

        chars, width, filtered = _page_chars(
            FakeTextPage(), FakePage(), FakeRaw(), return_filter_info=True
        )
        self.assertEqual(width, 100.0)
        self.assertEqual(len(chars), 1)
        self.assertTrue(filtered)


class MarginColumnFilterTest(unittest.TestCase):
    @staticmethod
    def _table_chars(rows):
        chars = []
        for i, (label, value) in enumerate(rows):
            y = 700 - i * 40
            for j, ch in enumerate(label):
                chars.append(_char(ch, 50 + j * 8, 56 + j * 8, y, y + 10))
            if value is not None:
                for j, ch in enumerate(value):
                    chars.append(_char(ch, 500 + j * 8, 506 + j * 8, y, y + 10))
        for j, ch in enumerate("ITEM"):
            chars.append(_char(ch, 50 + j * 8, 56 + j * 8, 740, 750))
        for j, ch in enumerate("QUANTITY"):
            chars.append(_char(ch, 500 + j * 8, 506 + j * 8, 740, 750))
        return chars

    def test_drops_narrow_vertical_margin_column(self):
        # Mimics arXiv sidebar: narrow x span, one glyph per line.
        margin = [
            _char(c, 20, 28, 500 - i * 14, 512 - i * 14)
            for i, c in enumerate("0202luJ22")
        ]
        body = [
            _char("L", 160, 170, 700, 712),
            _char("a", 170, 180, 700, 712),
            _char("n", 180, 190, 700, 712),
        ]
        cols = _filter_reading_columns(margin + body, scale=10.0, width=612.0)
        self.assertEqual(len(cols), 1)
        self.assertEqual(cols[0][0]["ch"], "L")

    def test_keeps_real_two_column_layout(self):
        left = [_char("L", 50, 150, 700 - i * 12, 712 - i * 12) for i in range(4)]
        right = [_char("R", 400, 500, 700 - i * 12, 712 - i * 12) for i in range(4)]
        cols = _filter_reading_columns(left + right, scale=12.0, width=600.0)
        self.assertEqual(len(cols), 2)

    def test_numeric_table_hint_requires_aligned_value_column(self):
        # A narrow quantity column aligned with labels is a table, not a
        # margin watermark.  The hint is consumed before artifact filtering.
        chars = []
        for i, label in enumerate(("ITEM", "Aster", "Willow")):
            y = 700 - i * 40
            for j, ch in enumerate(label):
                chars.append(_char(ch, 50 + j * 8, 56 + j * 8, y, y + 10))
        for i, value in enumerate(("QUANTITY", "124", "237")):
            y = 700 - i * 40
            for j, ch in enumerate(value):
                chars.append(_char(ch, 500 + j * 8, 506 + j * 8, y, y + 10))
        self.assertTrue(
            _looks_like_numeric_table_columns(chars, scale=10.0, width=600.0)
        )

    def test_numeric_table_keeps_rows_and_drops_unrelated_artifact_column(self):
        chars = self._table_chars([("Aster", "124"), ("Willow", "237")])
        chars.extend(
            _char(c, 20, 28, 500 - i * 14, 512 - i * 14)
            for i, c in enumerate("0202luJ22")
        )
        text = _chars_to_layout_markdown(chars, scale=10.0, width=600.0)
        self.assertIn("Aster 124", text)
        self.assertIn("Willow 237", text)
        self.assertNotIn("0202luJ22", text)

    def test_numeric_table_hint_does_not_match_two_text_columns(self):
        left = [_char("L", 50, 150, 700 - i * 12, 712 - i * 12) for i in range(4)]
        right = [_char("R", 400, 500, 700 - i * 12, 712 - i * 12) for i in range(4)]
        self.assertFalse(
            _looks_like_numeric_table_columns(left + right, scale=12.0, width=600.0)
        )

    def test_layout_keeps_repeated_values_and_empty_cells_in_rows(self):
        repeated = _chars_to_layout_markdown(
            self._table_chars(
                [("Aster", "1"), ("Willow", "1")]
            ),
            scale=10.0,
            width=600.0,
        )
        self.assertIn("Aster 1", repeated)
        self.assertIn("Willow 1", repeated)

        with_empty = _chars_to_layout_markdown(
            self._table_chars(
                [("Aster", None), ("Willow", "237")]
            ),
            scale=10.0,
            width=600.0,
        )
        self.assertIn("Aster", with_empty)
        self.assertIn("Willow 237", with_empty)


class PunctuationMergeTest(unittest.TestCase):
    def test_merges_orphan_periods(self):
        lines = [
            _line("Figure 1 2", 10.0),
            _line(". .", 10.0),
            _line("Next", 10.0),
        ]
        merged = _merge_orphan_punctuation_lines(lines)
        self.assertEqual([ln["text"] for ln in merged], ["Figure 1 2..", "Next"])


class PdfTextSanitizeTest(unittest.TestCase):
    def test_removes_fffe_placeholder(self):
        from docreader.parser.pdf_parser import _postprocess_pdf_text

        raw = "multi\ufffelayer and non\ufffetrivial"
        out = _postprocess_pdf_text(raw)
        self.assertEqual(out, "multilayer and nontrivial")

    def test_strips_chart_axis_run(self):
        from docreader.parser.pdf_parser import _postprocess_pdf_text

        raw = (
            "Deep convolutional neural networks have led to breakthroughs.\n"
            "0 1 2 3 4 5 6 0\n"
            "10\n"
            "20\n"
            "iter. (1e4)\n"
            "training error (%)\n"
            "56-layer\n"
            "20-layer\n"
            "Figure 1. Training error on CIFAR-10.\n"
        )
        out = _postprocess_pdf_text(raw)
        self.assertIn("breakthroughs", out)
        self.assertNotIn("56-layer", out)
        self.assertIn("Figure 1.", out)

    def test_strips_diagram_labels_above_caption(self):
        from docreader.parser.pdf_parser import _postprocess_pdf_text

        raw = (
            "Paragraph before.\n"
            "identity\n"
            "weight layer\n"
            "relu\n"
            "Figure 2. Residual learning block.\n"
            "Paragraph after.\n"
        )
        out = _postprocess_pdf_text(raw)
        self.assertIn("Paragraph before.", out)
        self.assertIn("Figure 2.", out)
        self.assertIn("Paragraph after.", out)
        self.assertNotIn("identity", out)
        self.assertNotIn("weight layer", out)

    def test_strips_arxiv_header_line(self):
        from docreader.parser.pdf_parser import _postprocess_pdf_text

        raw = "Body text.\n1\narXiv:1512.03385v1 [cs.CV] 10 Dec 2015\nMore body."
        out = _postprocess_pdf_text(raw)
        self.assertNotIn("arXiv:", out)
        self.assertIn("Body text.", out)

    def test_preserves_numeric_body_run_without_chart_context(self):
        raw = "Quantity\n124\n237\n68\n91\nEnd of inventory."
        out = _postprocess_pdf_text(raw)
        for value in ("124", "237", "68", "91"):
            self.assertIn(value, out)

    def test_preserves_numeric_body_run_with_chart_context_when_header_is_present(self):
        raw = "Quantity\n124\n237\n68\n91\ntraining error"
        out = _postprocess_pdf_text(raw)
        for value in ("124", "237", "68", "91"):
            self.assertIn(value, out)

    def test_numeric_header_and_rows_are_a_table_tail(self):
        self.assertTrue(_tail_looks_like_numeric_table(["Body", "Quantity", "124", "237"]))

    def test_preserves_repeated_single_digit_values(self):
        from docreader.parser.pdf_parser import _postprocess_pdf_text

        raw = "ITEM QUANTITY\nAster 1\nWillow 1\nHazel 1"
        out = _postprocess_pdf_text(raw)
        self.assertIn("Aster 1", out)
        self.assertIn("Willow 1", out)
        self.assertIn("Hazel 1", out)

    def test_keeps_ambiguous_middle_page_number(self):
        from docreader.parser.pdf_parser import _postprocess_pdf_text

        out = _postprocess_pdf_text("Body\n124\nMore body\n2")
        self.assertIn("124", out)
        # An unlabelled edge number is ambiguous too, so retain it.  The
        # existing arXiv-header regression covers the explicit cleanup case.
        self.assertIn("\n2", out)

    def test_keeps_numeric_table_before_figure_caption(self):
        raw = "Header\nAster 124\nWillow 237\nFigure 1. Chart\nAfter"
        out = _postprocess_pdf_text(raw)
        self.assertIn("Aster 124", out)
        self.assertIn("Willow 237", out)
        self.assertIn("Figure 1. Chart", out)


class PlainWellFormedTest(unittest.TestCase):
    def test_academic_plain_skips_layout(self):
        from docreader.parser.pdf_parser import _plain_is_well_formed

        plain = (
            "Recent work [DL15, MBXS17] shows progress on NLP tasks "
            "with pre-trained models."
        )
        self.assertTrue(_plain_is_well_formed(plain))

    def test_glued_scan_plain_needs_layout(self):
        from docreader.parser.pdf_parser import _plain_is_well_formed

        self.assertFalse(_plain_is_well_formed("Thisisadigitalcopyofabook"))


class LayoutQualityFallbackTest(unittest.TestCase):
    def test_prefers_plain_when_many_single_char_lines(self):
        plain = "Language Models are Few-Shot Learners\nTom Brown"
        layout = "0\n2\n0\n2\nl\nu\nJ\nLan ua e Models"
        self.assertTrue(_should_prefer_plain(plain, layout))

    def test_keeps_good_layout(self):
        plain = "Hello world"
        layout = "Hello world"
        self.assertFalse(_should_prefer_plain(plain, layout))

    def test_prefers_plain_when_layout_splits_numeric_table_columns(self):
        plain = "ITEM QUANTITY\nAster 124\nWillow 237"
        layout = "ITEM\nAster\nWillow\nQUANTITY\n1 24\n237"
        self.assertTrue(_should_prefer_plain(plain, layout))

    def test_prefers_plain_when_layout_substitutes_a_numeric_value(self):
        plain = "ITEM QUANTITY\nAster 124\nWillow 237"
        layout = "ITEM QUANTITY\nAster 124\nWillow 999"
        self.assertTrue(_should_prefer_plain(plain, layout))

    def test_does_not_prefer_plain_numeric_rows_without_table_hint(self):
        plain = "Method 2020\nDataset 2021"
        layout = "Method\nDataset\n2020\n2021"
        self.assertFalse(
            _should_prefer_plain(plain, layout, allow_structured_rows=False)
        )


class ResNetPaperFigureTest(unittest.TestCase):
    """Regression: ResNet PDF (arXiv:1512.03385) vector figures and captions."""

    def test_resnet_figures_and_captions(self):
        import os

        from docreader.parser.pdf_parser import PDFParser

        for path in (
            os.path.join(
                os.path.dirname(__file__),
                "..",
                "..",
                "testdata",
                "rag_test",
                "pdf_en",
                "resnet.pdf",
            ),
            "/tmp/resnet.pdf",
        ):
            if os.path.isfile(path):
                break
        else:
            self.skipTest("resnet.pdf not available")

        with open(path, "rb") as f:
            doc = PDFParser(file_name="resnet.pdf", file_type="pdf").parse_into_text(
                f.read()
            )
        self.assertGreater(doc.metadata.get("vector_figure_count", 0), 0)
        self.assertIn("![", doc.content)
        self.assertIn("Figure 2. Residual learning", doc.content)
        self.assertNotIn("arXiv:", doc.content)
        fig2 = doc.content.find("Figure 2. Residual learning")
        before = doc.content[max(0, fig2 - 120) : fig2]
        self.assertIn("![", before)
        self.assertNotIn("identity", before)


class Gpt3PaperLayoutTest(unittest.TestCase):
    """Regression: arXiv GPT-3 paper title page must not be one-glyph-per-line."""

    def test_gpt3_page0_title_and_authors(self):
        import os

        import pypdfium2 as pdfium
        import pypdfium2.raw as pdfium_r

        from docreader.parser.pdf_parser import PDFParser, _extract_layout_text

        pdf_path = os.path.join(
            os.path.dirname(__file__),
            "..",
            "..",
            "testdata",
            "rag_test",
            "pdf_en",
            "gpt3.pdf",
        )
        if not os.path.isfile(pdf_path):
            self.skipTest("gpt3.pdf not in testdata")
        with open(pdf_path, "rb") as f:
            content = f.read()
        with pdfium.PdfDocument(content) as pdf:
            page = pdf[0]
            try:
                layout = _extract_layout_text(page, pdfium_r)
            finally:
                page.close()
        # Margin sidebar must not appear as one-glyph-per-line prefix.
        self.assertNotRegex(layout[:300], r"^0\n2\n0\n2")
        self.assertIn("Few-Shot Learners", layout)

        doc = PDFParser(file_name="gpt3.pdf", file_type="pdf").parse_into_text(content)
        self.assertIn("Language Models are Few-Shot Learners", doc.content)
        self.assertIn("Tom B. Brown", doc.content[:1200])
        self.assertIn("[DL15, MBXS17, PNZtY18]", doc.content)
        self.assertIn("task-specific architectures), and more recently", doc.content)
        self.assertNotIn("k ifi hi d l", doc.content)


class ScanEnglishDictLayoutTest(unittest.TestCase):
    """Regression: Google Books-style PDFs lose spaces without gap inference."""

    def test_scan_en_dict_page0_has_word_spaces(self):
        import os

        import pypdfium2 as pdfium
        import pypdfium2.raw as pdfium_r

        from docreader.parser.pdf_parser import _extract_layout_text

        pdf_path = os.path.join(
            os.path.dirname(__file__),
            "..",
            "..",
            "testdata",
            "rag_test",
            "pdf_scan",
            "scan_en_dict.pdf",
        )
        if not os.path.isfile(pdf_path):
            self.skipTest("scan_en_dict.pdf not in testdata")
        with open(pdf_path, "rb") as f:
            pdf = pdfium.PdfDocument(f.read())
        try:
            text = _extract_layout_text(pdf[0], pdfium_r)
        finally:
            pdf.close()
        self.assertIn("This is a digital copy of a book", text)
        self.assertNotIn("Thisisadigitalcopyofabook", text)


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


class ForceScannedTest(unittest.TestCase):
    """Tests for the force-scanned PDF parsing mode."""

    def test_default_text_pdf_is_not_force_scanned(self):
        """Without any override, a text PDF should NOT use scanned mode."""
        parser = PDFParser(file_name="normal.pdf", file_type="pdf")
        self.assertFalse(parser._force_scanned)

    def test_force_scanned_via_kwarg_true(self):
        """Per-upload override pdf_force_scanned=true enables scanned mode."""
        parser = PDFParser(
            file_name="test.pdf", file_type="pdf", pdf_force_scanned="true"
        )
        self.assertTrue(parser._force_scanned)

    def test_force_scanned_via_kwarg_false(self):
        """Per-upload override pdf_force_scanned=false disables scanned mode."""
        parser = PDFParser(
            file_name="test.pdf", file_type="pdf", pdf_force_scanned="false"
        )
        self.assertFalse(parser._force_scanned)

    def test_force_scanned_via_kwarg_overrides_env(self, monkeypatch=None):
        """Per-upload override takes priority over global env var."""
        import docreader.parser.pdf_parser as pdf_mod

        old = pdf_mod.FORCE_SCANNED_PDF
        try:
            pdf_mod.FORCE_SCANNED_PDF = True
            # Explicit false override should win over env=true
            parser = PDFParser(
                file_name="test.pdf", file_type="pdf", pdf_force_scanned="false"
            )
            self.assertFalse(parser._force_scanned)
        finally:
            pdf_mod.FORCE_SCANNED_PDF = old

    def test_force_scanned_via_env(self):
        """Global env variable enables force scanned when no override."""
        import docreader.parser.pdf_parser as pdf_mod

        old = pdf_mod.FORCE_SCANNED_PDF
        try:
            pdf_mod.FORCE_SCANNED_PDF = True
            parser = PDFParser(file_name="test.pdf", file_type="pdf")
            self.assertTrue(parser._force_scanned)
        finally:
            pdf_mod.FORCE_SCANNED_PDF = old

    def test_force_scanned_output_is_scanned_pdf(self):
        """Force-scanned mode produces scanned_pdf metadata and image refs."""
        pdf_bytes = _make_image_only_pdf(2)
        parser = PDFParser(
            file_name="forced.pdf", file_type="pdf", pdf_force_scanned="true"
        )
        doc = parser.parse_into_text(pdf_bytes)
        self.assertEqual(doc.metadata.get("image_source_type"), "scanned_pdf")
        self.assertEqual(doc.metadata.get("page_count"), 2)
        self.assertEqual(doc.metadata.get("scanned_page_count"), 2)
        self.assertEqual(doc.metadata.get("text_page_count"), 0)
        self.assertEqual(doc.metadata.get("embedded_image_count"), 0)
        self.assertEqual(doc.metadata.get("vector_figure_count"), 0)
        self.assertIn("![forced_page_1.jpg]", doc.content)
        self.assertIn("![forced_page_2.jpg]", doc.content)
        self.assertEqual(len(doc.images), 2)


if __name__ == "__main__":
    unittest.main()
