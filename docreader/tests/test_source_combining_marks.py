"""Region provenance must survive composed/decomposed glyph text."""
import unicodedata
import unittest

from docreader.parser.source_locator import PageGeometry, locate_lines, page_blocks


class SourceCombiningMarksTests(unittest.TestCase):
    def test_regions_match_composed_and_decomposed_text(self):
        for phrase in ("Café résumé", "한글 원문", "Ångström evidence", "Evidence ấ\u0323"):
            for page_form, glyph_form in (("NFC", "NFD"), ("NFD", "NFC")):
                with self.subTest(phrase=phrase, page_form=page_form):
                    page = unicodedata.normalize(page_form, phrase)
                    glyph = unicodedata.normalize(glyph_form, phrase)
                    lines = [{"text": glyph, "bbox": (10, 70, 90, 80)}]
                    blocks = page_blocks(page, 7, 2, lines, PageGeometry((0, 0, 100, 100)))
                    self.assertEqual(len(blocks), 1)
                    self.assertEqual(blocks[0]["locator"].get("bbox"), [0.1, 0.2, 0.9, 0.3])
                    self.assertEqual(blocks[0]["start"], 7)
                    self.assertEqual(blocks[0]["end"], 7 + len(page))

    def test_marks_are_not_discarded_to_match_different_words(self):
        self.assertEqual(locate_lines("Cafe evidence", ["Café evidence"]), [None])
        self.assertEqual(locate_lines("Café evidence", ["Cafe evidence"]), [None])
        self.assertEqual(locate_lines("Cafe\u0301 evidence", ["Café evidence"]), [0])
        self.assertEqual(locate_lines("Cafe\u0301 evidence", ["Cafe evidence"]), [None])

    def test_repeated_lines_keep_distinct_offsets(self):
        page = "Cafe\u0301 original evidence\nCafe\u0301 original evidence"
        hits = locate_lines(page, ["Café original evidence"] * 2)
        self.assertEqual(hits, [0, page.index("\n") + 1])

    def test_single_glyph_keeps_existing_short_key_guard(self):
        self.assertEqual(locate_lines("é", ["é"]), [None])
