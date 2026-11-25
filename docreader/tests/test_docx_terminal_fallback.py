"""The last DOCX fallback must retain the original document's reading order."""

import unittest
from io import BytesIO
from unittest.mock import patch

from docx import Document
from docx.oxml import OxmlElement

from docreader.parser.parser import Parser


def parse_fallback(document):
    data = BytesIO()
    document.save(data)
    # Exercise the final fallback through the actual public parser chain.
    with patch(
        "docreader.parser.markitdown_parser.StdMarkitdownParser.parse_into_text",
        side_effect=RuntimeError("converter unavailable"),
    ), patch(
        "docreader.parser.docx_parser.Docx.__call__",
        side_effect=RuntimeError("worker unavailable"),
    ):
        return Parser().parse_file("report.docx", "docx", data.getvalue())


class TerminalDocxFallbackTests(unittest.TestCase):
    def test_paragraphs_and_tables_remain_in_body_order(self):
        doc = Document()
        doc.add_paragraph("Before first table")
        doc.add_table(rows=1, cols=1).cell(0, 0).text = "First table value"
        doc.add_paragraph("Between the tables")
        doc.add_table(rows=1, cols=1).cell(0, 0).text = "Second table value"
        doc.add_paragraph("After second table")
        result = parse_fallback(doc)
        values = [
            "Before first table",
            "First table value",
            "Between the tables",
            "Second table value",
            "After second table",
        ]
        for value in values:
            self.assertIn(value, result.content)
        self.assertEqual(sorted(values, key=result.content.index), values)
        for value in values:
            self.assertEqual(result.content.count(value), 1)

    def test_content_controls_and_nested_cell_text_survive(self):
        doc = Document()
        doc.add_paragraph("Before control")
        table = doc.add_table(rows=1, cols=2)
        table.cell(0, 0).text = "Outer value"
        cell = table.cell(0, 1)
        cell.text = "Before nested"
        cell.add_table(rows=1, cols=1).cell(0, 0).text = "Nested payload"
        cell.add_paragraph("After nested")
        control = OxmlElement("w:sdt")
        body = OxmlElement("w:sdtContent")
        control.append(body)
        table._tbl.addprevious(control)
        body.append(table._tbl)
        doc.add_paragraph("After control")
        result = parse_fallback(doc)
        values = [
            "Before control",
            "Outer value",
            "Before nested",
            "Nested payload",
            "After nested",
            "After control",
        ]
        for value in values:
            self.assertIn(value, result.content)
        self.assertEqual(sorted(values, key=result.content.index), values)
        self.assertIn("| Outer value |", result.content)

    def test_plain_and_empty_documents_remain_usable(self):
        doc = Document()
        doc.add_paragraph("First paragraph")
        doc.add_paragraph("")
        doc.add_paragraph("Last paragraph")
        self.assertEqual(
            parse_fallback(doc).content, "First paragraph\n\nLast paragraph"
        )
        self.assertEqual(parse_fallback(Document()).content, "")
        empty_table = Document()
        empty_table.add_table(rows=2, cols=2)
        self.assertEqual(parse_fallback(empty_table).content, "")

    def test_successful_first_parser_is_not_replaced(self):
        doc = Document()
        doc.add_paragraph("Successful normal conversion")
        data = BytesIO()
        doc.save(data)
        with patch("docreader.parser.docx_parser.Docx.__call__") as fallback:
            result = Parser().parse_file("normal.docx", "docx", data.getvalue())
        fallback.assert_not_called()
        self.assertIn("Successful normal conversion", result.content)
