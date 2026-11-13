import unittest
from io import BytesIO
from unittest.mock import patch

import openpyxl
import pandas as pd

from docreader.parser.excel_parser import ExcelParser


class ExcelWorkbookLifetimeTest(unittest.TestCase):
    def test_closes_real_workbook_after_success_or_sheet_failure(self):
        workbook = openpyxl.Workbook()
        workbook.active["A1"] = "first"
        workbook.create_sheet("Second")["A1"] = "second"
        stream = BytesIO()
        workbook.save(stream)
        workbook.close()
        original_close = pd.ExcelFile.close
        original_parse = pd.ExcelFile.parse

        for fail in (False, True):
            with self.subTest(fail=fail):
                closed = []

                def tracked_close(excel, closed=closed):
                    closed.append(excel.sheet_names)
                    return original_close(excel)

                def parse(excel, *args, fail=fail, **kwargs):
                    if fail and kwargs["sheet_name"] == "Second":
                        raise ValueError("cannot read second sheet")
                    return original_parse(excel, *args, **kwargs)

                with (
                    patch.object(pd.ExcelFile, "close", tracked_close),
                    patch.object(pd.ExcelFile, "parse", parse),
                ):
                    parser = ExcelParser(file_name="notes.xlsx", file_type="xlsx")
                    if fail:
                        with self.assertRaisesRegex(
                            ValueError, "cannot read second sheet"
                        ):
                            parser.parse_into_text(stream.getvalue())
                    else:
                        self.assertIn(
                            "second", parser.parse_into_text(stream.getvalue()).content
                        )
                self.assertEqual(closed, [["Sheet", "Second"]])
