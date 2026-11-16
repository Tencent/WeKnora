"""A table inside a table cell is read as that cell's text."""

import unittest

from docreader.parser.html_parser import HTMLParser


def _markdown(html: str) -> str:
    return HTMLParser(file_name="nested.html", file_type="html").parse_into_text(
        html.encode()
    ).content


class HTMLNestedTableTest(unittest.TestCase):
    def test_nested_table_rows_stay_in_the_outer_cell(self):
        # markdownify wrote the inner table's pipes and separator line into
        # the outer cell, which gave the Contact row twelve columns
        markdown = _markdown(
            "<table><tr><th>Field</th><th>Value</th></tr>"
            "<tr><td>Name</td><td>Ann</td></tr>"
            "<tr><td>Contact</td><td><table>"
            "<tr><td>Phone</td><td>123</td></tr>"
            "<tr><td>Mail</td><td>a@x.com</td></tr>"
            "</table></td></tr>"
            "<tr><td>City</td><td>Paris</td></tr></table>"
        )

        self.assertEqual(
            markdown,
            "| Field | Value |\n| --- | --- |\n| Name | Ann |\n"
            "| Contact | Phone 123 Mail a@x.com |\n| City | Paris |",
        )

    def test_indented_nested_table_adds_no_extra_spaces(self):
        markdown = _markdown(
            "<table>\n  <tr><th>Field</th><th>Value</th></tr>\n  <tr>\n"
            "    <td>Contact</td>\n    <td>\n      <table>\n        <tbody>\n"
            "          <tr>\n            <td>Phone</td>\n            <td>123</td>\n"
            "          </tr>\n          <tr>\n            <td>Mail</td>\n"
            "            <td>a@x.com</td>\n          </tr>\n        </tbody>\n"
            "      </table>\n    </td>\n  </tr>\n</table>"
        )

        self.assertEqual(
            markdown,
            "| Field | Value |\n| --- | --- |\n| Contact | Phone 123 Mail a@x.com |",
        )

    def test_nested_table_cell_keeps_its_link(self):
        markdown = _markdown(
            "<table><tr><th>Docs</th></tr><tr><td><table><tr>"
            '<td>Guide</td><td><a href="https://example.com/g">open</a></td>'
            "</tr></table></td></tr></table>"
        )

        self.assertEqual(
            markdown,
            "| Docs |\n| --- |\n| Guide [open](https://example.com/g) |",
        )

    def test_pipe_in_a_nested_table_cell_is_escaped_once(self):
        markdown = _markdown(
            "<table><tr><th>Key</th><th>Value</th></tr><tr><td>Flags</td>"
            "<td><table><tr><td>a|b</td></tr></table></td></tr></table>"
        )

        self.assertEqual(
            markdown, "| Key | Value |\n| --- | --- |\n| Flags | a\\|b |"
        )

    def test_table_nested_two_levels_deep(self):
        markdown = _markdown(
            "<table><tr><th>Outer</th></tr><tr><td><table><tr><td>mid</td>"
            "<td><table><tr><td>deep</td></tr></table></td></tr></table>"
            "</td></tr></table>"
        )

        self.assertEqual(markdown, "| Outer |\n| --- |\n| mid deep |")


if __name__ == "__main__":
    unittest.main()
