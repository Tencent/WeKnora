"""Percentage display rounding through the actual XLSX import path."""
import io
import unittest
from unittest.mock import patch

import openpyxl

from docreader.parser.excel_parser import ExcelParser


class PercentageRoundingTests(unittest.TestCase):
    def test_rounds_percentage_ties_away_from_zero(self):
        cases = [
            (0.125, "0%", "13%"),
            (-0.125, "0%", "-13%"),
            (0.00125, "0.00%", "0.13%"),
            (-0.00125, "0.00%;(0.00%)", "(0.13%)"),
            (12.345, "#,##0%", "1,235%"),
            (0.124, "0%", "12%"),
            (0.126, "0%", "13%"),
            (0, "0.00%", "0.00%"),
        ]
        for value, number_format, expected in cases:
            with self.subTest(value=value, number_format=number_format):
                wb = openpyxl.Workbook()
                wb.active.append(["rate", value])
                wb.active["B1"].number_format = number_format
                output = io.BytesIO()
                wb.save(output)
                doc = ExcelParser(xlsx_percent_as_text=True).parse(output.getvalue())
                self.assertIn(f"B: {expected}\n", doc.content)
                raw = ExcelParser(xlsx_percent_as_text=False).parse(output.getvalue())
                self.assertIn(f"B: {value}\n", raw.content)

    def test_row_hint_uses_same_rounding(self):
        wb = openpyxl.Workbook()
        wb.active.append([0.5, 0.00125])
        wb.active["A1"].number_format = "0.00%"
        output = io.BytesIO()
        wb.save(output)
        with patch.dict("os.environ", {"DOCREADER_XLSX_PCT_ROW_HINT_MIN": "1"}):
            doc = ExcelParser(xlsx_percent_as_text=True).parse(output.getvalue())
        self.assertIn("A: 50.00%,B: 0.13%\n", doc.content)
