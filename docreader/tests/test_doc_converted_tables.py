"""Converted DOC tables get the same vertical-merge handling as DOCX uploads."""

from io import BytesIO
from pathlib import Path
import unittest
from unittest.mock import patch

from docx import Document as WordDocument

from docreader.parser.doc_parser import DocParser
from docreader.parser.parser import Parser


def _converted_table(*, merged: bool) -> bytes:
    document = WordDocument()
    table = document.add_table(rows=4, cols=3)
    for cell, text in zip(table.rows[0].cells, ("Item", "Method", "Control")):
        cell.text = text
    for index in range(1, 4):
        table.cell(index, 0).text = f"item-{index}"
        table.cell(index, 1).text = "shared method"
        table.cell(index, 2).text = "same"
    if merged:
        table.cell(1, 1).merge(table.cell(3, 1)).text = "shared method"
    output = BytesIO()
    document.save(output)
    return output.getvalue()


class ConvertedDOCTableTest(unittest.TestCase):
    def check_table(self, file_type: str, *, merged: bool):
        # Only the external LibreOffice conversion boundary is substituted.
        # Its DOCX result is a real package; all downstream parsers are real.
        legacy = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1synthetic conversion input"
        paths = []

        def convert(path):
            self.assertEqual(Path(path).read_bytes(), legacy)
            paths.append(path)
            return _converted_table(merged=merged)

        with patch.object(DocParser, "_try_convert_doc_to_docx", side_effect=convert):
            with self.assertLogs("docreader.parser.chain_parser", level="INFO") as logs:
                document = Parser().parse_file(
                    f"table.{file_type}", file_type, legacy, parser_engine="builtin"
                )

        self.assertEqual(len(paths), 1)
        self.assertFalse(Path(paths[0]).exists())
        self.assertTrue(
            any("MarkitdownParser succeeded" in line for line in logs.output)
        )
        self.assertEqual(
            [line for line in document.content.splitlines() if line.startswith("|")],
            [
                "| Item | Method | Control |",
                "| --- | --- | --- |",
                "| item-1 | shared method | same |",
                "| item-2 | shared method | same |",
                "| item-3 | shared method | same |",
            ],
        )

    def test_doc_conversion_keeps_merged_value_in_each_row(self):
        self.check_table("doc", merged=True)

    def test_legacy_doc_renamed_docx_keeps_merged_value_in_each_row(self):
        self.check_table("docx", merged=True)

    def test_equal_unmerged_cells_still_occupy_separate_rows(self):
        self.check_table("doc", merged=False)


if __name__ == "__main__":
    unittest.main()
