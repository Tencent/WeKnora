import io
import unittest

import openpyxl

from docreader.parser.excel_parser import ExcelParser


class XlsxMergedLiteralTests(unittest.TestCase):
    def test_merged_literal_text_survives_excel_parsing(self):
        workbook = openpyxl.Workbook()
        sheet = workbook.active
        for column, text in (("A", "=1+2"), ("B", "#N/A")):
            cell = sheet[f"{column}1"]
            cell.value = text
            cell.data_type = "s"
            sheet.merge_cells(f"{column}1:{column}2")

        buffer = io.BytesIO()
        workbook.save(buffer)
        workbook.close()
        document = ExcelParser(
            file_name="literal-text.xlsx", xlsx_percent_as_text=False
        ).parse_into_text(buffer.getvalue())

        self.assertEqual(
            [chunk.content for chunk in document.chunks],
            ["A: =1+2,B: #N/A\n", "A: =1+2,B: #N/A\n"],
        )
