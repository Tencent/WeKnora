"""A table's ``<tfoot>`` rows stay rows of that table, after its body rows."""

import unittest

from docreader.parser.html_parser import HTMLParser


def _markdown(html: str) -> str:
    return HTMLParser(file_name="footer.html", file_type="html").parse_into_text(
        html.encode()
    ).content


class HTMLTableFooterTest(unittest.TestCase):
    def test_footer_rows_follow_the_body_rows(self):
        # markdownify wrote an empty header row and a second separator line
        # before the footer, in the middle of the table
        markdown = _markdown(
            "<table><thead><tr><th>Item</th><th>Qty</th></tr></thead>"
            "<tbody><tr><td>pen</td><td>2</td></tr></tbody>"
            "<tfoot><tr><td>Total</td><td>2</td></tr></tfoot></table>"
        )

        self.assertEqual(
            markdown, "| Item | Qty |\n| --- | --- |\n| pen | 2 |\n| Total | 2 |"
        )

    def test_a_footer_written_before_the_body_still_comes_last(self):
        markdown = _markdown(
            "<table><thead><tr><th>Item</th><th>Qty</th></tr></thead>"
            "<tfoot><tr><td>Total</td><td>3</td></tr></tfoot>"
            "<tbody><tr><td>pen</td><td>2</td></tr><tr><td>ink</td><td>1</td></tr>"
            "</tbody></table>"
        )

        self.assertEqual(
            markdown,
            "| Item | Qty |\n| --- | --- |\n| pen | 2 |\n| ink | 1 |\n| Total | 3 |",
        )

    def test_a_nested_table_footer_stays_in_the_nested_table(self):
        markdown = _markdown(
            "<table><tr><th>Outer</th></tr><tr><td>"
            "<table><tr><th>Inner</th></tr><tr><td>a</td></tr>"
            "<tfoot><tr><td>inner total</td></tr></tfoot></table>"
            "</td></tr><tfoot><tr><td>outer total</td></tr></tfoot></table>"
        )

        # markdownify flattens the inner table into one cell; its footer row
        # has to stay inside it rather than move after the outer body
        self.assertLess(markdown.index("inner total"), markdown.index("outer total"))
        self.assertTrue(markdown.rstrip().endswith("| outer total |"))


if __name__ == "__main__":
    unittest.main()
