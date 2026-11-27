import unittest
from io import BytesIO

from pptx import Presentation
from pptx.util import Inches

from docreader.parser.parser import Parser


def _deck(rows):
    prs = Presentation()
    slide = prs.slides.add_slide(prs.slide_layouts[5])
    slide.shapes.title.text = "Pricing"
    table = slide.shapes.add_table(
        len(rows), len(rows[0]), Inches(0.5), Inches(1.5), Inches(8), Inches(2)
    ).table
    for r, row in enumerate(rows):
        for c, value in enumerate(row):
            table.cell(r, c).text = value
    buf = BytesIO()
    prs.save(buf)
    return buf.getvalue()


class PPTXTableCellTests(unittest.TestCase):
    def test_pipe_and_multiline_cells_stay_in_one_column_and_row(self):
        data = _deck(
            [
                ["Plan", "Price | Currency", "Notes"],
                ["Basic", "10 | USD", "line one\nline two"],
            ]
        )
        content = Parser().parse_file("deck.pptx", "pptx", data).content
        self.assertIn(
            "| Plan | Price \\| Currency | Notes |\n| --- | --- | --- |\n"
            "| Basic | 10 \\| USD | line one<br />line two |",
            content,
        )
        self.assertIn("# Pricing", content)

    def test_plain_table_is_unchanged(self):
        content = Parser().parse_file(
            "deck.pptx", "pptx", _deck([["A", "B"], ["x", "2"]])
        ).content
        self.assertIn("| A | B |\n| --- | --- |\n| x | 2 |", content)


if __name__ == "__main__":
    unittest.main()
