import unittest
from io import BytesIO

from docx import Document

from docreader.parser.parser import Parser


def _docx(rows):
    doc = Document()
    table = doc.add_table(rows=len(rows), cols=len(rows[0]))
    for r, row in enumerate(rows):
        for c, value in enumerate(row):
            table.cell(r, c).text = value
    buf = BytesIO()
    doc.save(buf)
    return buf.getvalue()


class DOCXTableCellTests(unittest.TestCase):
    def test_pipe_in_cell_stays_in_one_column(self):
        content = Parser().parse_file(
            "table.docx",
            "docx",
            _docx(
                [
                    ["Plan", "Price | Currency", "Notes"],
                    ["Basic", "10 | USD", "x"],
                ]
            ),
        ).content
        self.assertIn("| Plan | Price \\| Currency | Notes |", content)
        self.assertIn("| Basic | 10 \\| USD | x |", content)

    def test_plain_table_is_unchanged(self):
        content = Parser().parse_file(
            "table.docx", "docx", _docx([["A", "B"], ["x", "2"]])
        ).content
        self.assertIn("| A | B |\n| --- | --- |\n| x | 2 |", content)


if __name__ == "__main__":
    unittest.main()
