"""A pipe inside a table cell stays in that cell; GFM escapes it as ``\\|``."""

import unittest

from docreader.parser.html_parser import HTMLParser
from docreader.parser.markdown_parser import MarkdownParser, MarkdownTableUtil


class TableCellPipeTest(unittest.TestCase):
    def test_format_table_keeps_an_escaped_pipe_in_its_cell(self):
        raw = "|Pattern|Matches|\n|---|---|\n|`a\\|b`|a or b|\n|x \\| y|literal|"

        formatted = MarkdownTableUtil().format_table(raw)

        self.assertEqual(
            formatted,
            "| Pattern | Matches |\n| --- | --- |\n"
            "| `a\\|b` | a or b |\n| x \\| y | literal |",
        )

    def test_format_table_ends_a_cell_at_a_pipe_after_an_escaped_backslash(self):
        # "\\\\" is an escaped backslash, so the pipe after it is a delimiter
        formatted = MarkdownTableUtil().format_table("|a\\\\|b|\n|---|---|")

        self.assertEqual(formatted, "| a\\\\ | b |\n| --- | --- |")

    def test_markdown_upload_keeps_an_escaped_pipe(self):
        content = "| Type | Notes |\n| --- | --- |\n| int \\| None | optional |\n"

        document = MarkdownParser(
            file_name="types.md", file_type="md"
        ).parse_into_text(content.encode())

        self.assertIn("| int \\| None | optional |", document.content)

    def test_html_upload_escapes_a_pipe_in_a_cell(self):
        html = (
            "<table><tr><th>Expr</th><th>Means</th></tr>"
            "<tr><td>a|b</td><td>run <code>ls|wc</code></td></tr></table>"
        )

        document = HTMLParser(
            file_name="pipes.html", file_type="html"
        ).parse_into_text(html.encode())

        self.assertIn("| Expr | Means |", document.content)
        self.assertIn("| a\\|b | run `ls\\|wc` |", document.content)

    def test_html_comment_in_a_cell_stays_hidden(self):
        html = (
            "<table><tr><th>Name</th></tr>"
            "<tr><td>pen<!-- sku|42 --></td></tr></table>"
        )

        document = HTMLParser(
            file_name="comment.html", file_type="html"
        ).parse_into_text(html.encode())

        self.assertIn("| pen |", document.content)
        self.assertNotIn("sku", document.content)


if __name__ == "__main__":
    unittest.main()
