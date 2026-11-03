"""Only remove repeated PDF page-edge occurrences, not equal body lines."""
import unittest

from docreader.parser.pdf_parser import PDFParser, _strip_repeating_lines
from docreader.tests.test_pdf_text_only_figure import _pdf


class RepeatingEdgesTest(unittest.TestCase):
    def test_identical_body_line_survives_real_multipage_parse(self):
        pages = []
        for i in range(4):
            lines = ["SAFETY INSTRUCTIONS", f"Operating step page {i + 1}"]
            if i == 0:
                lines.append("SAFETY INSTRUCTIONS")
            lines.append(f"Last body unique {i + 1}")
            pages.append((lines, False))
        doc = PDFParser(file_name="manual.pdf").parse(_pdf(pages))
        self.assertEqual(doc.content.count("SAFETY INSTRUCTIONS"), 1)
        self.assertLess(doc.content.index("Operating step page 1"), doc.content.index("SAFETY INSTRUCTIONS"))
        self.assertLess(doc.content.index("SAFETY INSTRUCTIONS"), doc.content.index("Last body unique 1"))
        block = next(b for b in doc.source_blocks if "SAFETY INSTRUCTIONS" in doc.content[b["start"]:b["end"]])
        self.assertEqual(block["locator"]["page"], 1)
        for i in range(4):
            self.assertIn(f"Operating step page {i + 1}", doc.content)

    def test_threshold_is_at_least_sixty_percent(self):
        classes = ["text"] * 4
        texts = ["repeat\nx1", "repeat\nx2", "other\nx3", "different\nx4"]
        self.assertEqual(_strip_repeating_lines(texts, classes), texts)
        texts = ["repeat\nx1", "repeat\nx2", "repeat\nx3", "different\nx4"]
        self.assertEqual(_strip_repeating_lines(texts, classes), ["x1", "x2", "x3", "different\nx4"])
        texts = ["repeat\nx1", "repeat\nx2", "repeat\nx3", "different\nx4", "another\nx5"]
        self.assertEqual(_strip_repeating_lines(texts, ["text"] * 5), ["x1", "x2", "x3", "different\nx4", "another\nx5"])

    def test_first_and_last_position_do_not_cross_contaminate(self):
        texts = ["repeat\na", "repeat\nb", "c\nrepeat", "d\nrepeat"]
        self.assertEqual(_strip_repeating_lines(texts, ["text"] * 4), texts)

    def test_first_last_same_multiline_header_and_variable_page_numbers(self):
        texts = [f"SHARED\nsecond header\nbody {i}\nSHARED" for i in range(4)]
        self.assertEqual(_strip_repeating_lines(texts, ["text"] * 4), [f"second header\nbody {i}" for i in range(4)])
        texts = [f"SHARED\nbody {i}\nPage {i}" for i in range(1, 5)]
        self.assertEqual(_strip_repeating_lines(texts, ["text"] * 4), [f"body {i}\nPage {i}" for i in range(1, 5)])

    def test_short_single_empty_and_scanned_pages_are_preserved(self):
        texts = ["SHARED\nbody 1", "SHARED\nbody 2", "SHARED\nbody 3"]
        self.assertEqual(_strip_repeating_lines(texts, ["text"] * 3), texts)
        two_line_pages = ["HEADER\nFOOTER"] * 4
        self.assertEqual(_strip_repeating_lines(two_line_pages, ["text"] * 4), two_line_pages)
        texts = ["SHARED", "SHARED\nbody 2", "SHARED\nbody 3", "SHARED\nbody 4", ""]
        result = _strip_repeating_lines(texts, ["text"] * 5)
        self.assertEqual(result[0], "SHARED")
        self.assertEqual(result[-1], "")
        scanned = ["SHARED\nbody 1", "SHARED\nbody 2", "SHARED\nbody 3", "SHARED\nbody 4", "SHARED\nimage"]
        self.assertEqual(_strip_repeating_lines(scanned, ["text"] * 4 + ["scanned"])[-1], scanned[-1])


if __name__ == "__main__":
    unittest.main()
