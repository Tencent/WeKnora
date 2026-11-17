"""Caseless glyph matching must retain original source coordinates."""
import unittest

from docreader.parser.source_locator import (
    PageGeometry,
    locate_lines,
    normalize_with_positions,
    page_blocks,
)


class SourceCasefoldTests(unittest.TestCase):
    def test_caseless_regions_preserve_original_ranges(self):
        pairs = (
            ("ΤΕΛΟΣ ΚΕΙΜΕΝΟΥ", "τελος κειμενου"),
            ("Straße evidence", "STRASSE EVIDENCE"),
            ("Straße Café evidence", "STRASSE CAFE\u0301 EVIDENCE"),
        )
        for left, right in pairs:
            for page, glyph in ((left, right), (right, left)):
                with self.subTest(page=page, glyph=glyph):
                    blocks = page_blocks(
                        page, 7, 2,
                        [{"text": glyph, "bbox": (10, 70, 90, 80)}],
                        PageGeometry((0, 0, 100, 100)),
                    )
                    self.assertEqual(len(blocks), 1)
                    self.assertEqual(blocks[0]["start"], 7)
                    self.assertEqual(blocks[0]["end"], 7 + len(page))
                    self.assertEqual(
                        blocks[0]["locator"].get("bbox"), [0.1, 0.2, 0.9, 0.3]
                    )

    def test_expansion_retains_each_original_character_position(self):
        projection, positions = normalize_with_positions("Straße Café")
        self.assertEqual(projection, "strassecafe\u0301")
        self.assertEqual(positions, [0, 1, 2, 3, 4, 4, 5, 7, 8, 9, 10, 10])

    def test_expansion_does_not_shift_subsequent_line(self):
        page = "Straße evidence\nΤΕΛΟΣ ΚΕΙΜΕΝΟΥ"
        self.assertEqual(
            locate_lines(page, ["STRASSE EVIDENCE", "τελος κειμενου"]),
            [0, page.index("\n") + 1],
        )

    def test_casefold_preserves_accent_and_numeric_distinctions(self):
        for source, glyph in (
            ("Straße Café evidence", "STRASSE CAFE EVIDENCE"),
            ("Straße limit 15 mm", "STRASSE LIMIT 1.5 MM"),
        ):
            with self.subTest(source=source):
                self.assertEqual(locate_lines(source, [glyph]), [None])

    def test_short_casefold_equivalent_labels_remain_ambiguous(self):
        self.assertEqual(locate_lines("Straße\nSTRASSE", ["strasse"]), [None])


if __name__ == "__main__":
    unittest.main()
