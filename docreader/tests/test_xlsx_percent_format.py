"""Tests for materializing XLSX percentage number formats (opt-in).

Excel keeps a percentage as the underlying fraction plus a display format:
the cell stores ``0.61123366`` and ``0.00%`` renders it as ``61.12%``.
openpyxl and pandas read the fraction and drop the format, so a cell the user
sees as ``61.12%`` reaches the pipeline as ``0.6112`` — a 100x error that a
model will happily copy into a report.

When ``DOCREADER_XLSX_PERCENT_AS_TEXT`` is on (or the per-upload
``parser_engine_overrides`` key ``xlsx_percent_as_text`` is set), percentage
cells are materialized as the literal text the user sees. The switch is off by
default because the value changes from a number into a string, so anything that
sorts or aggregates the cell would see different input; with it off every
workbook is read exactly as before.
"""

import datetime
import io
import os
import unittest
from unittest.mock import patch

import openpyxl

from docreader.parser.excel_parser import ExcelParser
from docreader.parser.parser import Parser
from docreader.parser.xlsx_merge import fill_merged_cells_xlsx, _format_percentage

PERCENT_ENV = "DOCREADER_XLSX_PERCENT_AS_TEXT"
ROW_HINT_ENV = "DOCREADER_XLSX_PCT_ROW_HINT_MIN"
OVERRIDE_KEY = "xlsx_percent_as_text"


def _workbook_with_percentages() -> bytes:
    """A real .xlsx mixing percentages, a merge, plain numbers and dates.

    Layout (1-based; ``B3:B4`` is merged, so row 4's B cell is covered and has
    no format of its own — exactly like the yearly columns of a financial
    statement, where a label merge covers the row below):

        A             B                        C
    1   科目          2024                     2025
    2   毛利率        0.61123366  (0.00%)      0.5512345  (0.00%)
    3   收入占比      0.0833      (0.00%)      0.0912     (0.00%)
    4   收入占比(续)  <covered by B3:B4>       0.005509433890047721 (0.00%)
    5   营业收入      1234567.89  (#,##0.00)   9876543.21 (#,##0.00)
    6   报告日        2025-01-31  (yyyy-mm-dd) 2024-12-31 (yyyy-mm-dd)
    7   汇总          0.5         (General)    6100       (General)

    Row 4 is the regression case: it carries its own percentage cell in C, so a
    merge fill that runs before the percentage pass leaves that row showing both
    ``0.55%`` and a raw ``0.0833``.
    """
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.title = "财务"
    ws.append(["科目", "2024", "2025"])
    ws.append(["毛利率", 0.61123366, 0.5512345])
    ws.append(["收入占比", 0.0833, 0.0912])
    ws.append(["收入占比(续)", None, 0.005509433890047721])
    ws.append(["营业收入", 1234567.89, 9876543.21])
    ws.append(["报告日", datetime.date(2025, 1, 31), datetime.date(2024, 12, 31)])
    ws.append(["汇总", 0.5, 6100])

    for row in (2, 3):
        ws.cell(row=row, column=2).number_format = "0.00%"
        ws.cell(row=row, column=3).number_format = "0.00%"
    ws["C4"].number_format = "0.00%"
    # Merged after the formats are set: openpyxl keeps only the master (B3)
    # style, which is what makes the covered cell's format General.
    ws.merge_cells("B3:B4")
    for row in (5,):
        ws.cell(row=row, column=2).number_format = "#,##0.00"
        ws.cell(row=row, column=3).number_format = "#,##0.00"
    ws["B6"].number_format = "yyyy-mm-dd"
    ws["C6"].number_format = "yyyy-mm-dd"

    bio = io.BytesIO()
    wb.save(bio)
    return bio.getvalue()


def _workbook_with_unformatted_cell_in_percentage_row() -> bytes:
    """A percentage row (3 formatted cells) with one cell left General (D2)."""
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.append(["项目", "2024", "2025", "2026", "2027"])
    ws.append(["折旧率", 0.0592, 0.0601, 0.5, 0.0612])
    for col in (2, 3, 5):
        ws.cell(row=2, column=col).number_format = "0.00%"
    bio = io.BytesIO()
    wb.save(bio)
    return bio.getvalue()


def _workbook_without_merges() -> bytes:
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.append(["项目", "值"])
    ws.append(["占比", 0.25])
    ws["B2"].number_format = "0%"
    bio = io.BytesIO()
    wb.save(bio)
    return bio.getvalue()


def _read_cells(content: bytes, *coordinates: str):
    """Return the values of *coordinates* on the first worksheet."""
    wb = openpyxl.load_workbook(io.BytesIO(content), data_only=True)
    ws = wb.worksheets[0]
    return [ws[c].value for c in coordinates]


def _upstream_fill_merged_cells_xlsx(content: bytes) -> bytes:
    """Frozen copy of the pre-change implementation, for byte-identity checks.

    Kept verbatim (docreader/parser/xlsx_merge.py before the percentage switch)
    so the default path of the new function can be compared byte for byte with
    the behavior existing deployments have today.
    """
    import zipfile
    from io import BytesIO

    if not zipfile.is_zipfile(BytesIO(content)):
        return content

    from openpyxl import load_workbook

    wb = load_workbook(BytesIO(content), data_only=True)
    changed = False
    for ws in wb.worksheets:
        if not ws.merged_cells.ranges:
            continue
        for merge_range in list(ws.merged_cells.ranges):
            master_value = ws.cell(merge_range.min_row, merge_range.min_col).value
            ws.unmerge_cells(str(merge_range))
            for row in range(merge_range.min_row, merge_range.max_row + 1):
                for col in range(merge_range.min_col, merge_range.max_col + 1):
                    ws.cell(row, col).value = master_value
            changed = True

    if not changed:
        return content

    active_sheet = wb.active
    for chart_sheet in list(wb.chartsheets):
        wb.remove(chart_sheet)
    if active_sheet in wb.worksheets:
        wb.active = active_sheet
    elif wb.worksheets:
        wb.active = wb.worksheets[0]
    for ws in wb.worksheets:
        ws._charts.clear()

    out = BytesIO()
    wb.save(out)
    return out.getvalue()


class PercentMaterializationOffTests(unittest.TestCase):
    """Default behavior: unchanged, and byte-identical to the old function."""

    def test_default_keeps_the_raw_fraction(self):
        content = _workbook_with_percentages()

        self.assertEqual(
            fill_merged_cells_xlsx(content), _upstream_fill_merged_cells_xlsx(content)
        )

        values = _read_cells(fill_merged_cells_xlsx(content), "B2", "C2", "B3", "B4")
        self.assertEqual(values, [0.61123366, 0.5512345, 0.0833, 0.0833])

    def test_excel_parser_default_keeps_the_raw_fraction(self):
        document = ExcelParser().parse_into_text(_workbook_with_percentages())

        self.assertIn("A: 毛利率,B: 0.61123366", document.content)
        self.assertNotIn("61.12%", document.content)

    def test_switch_off_is_byte_identical_to_the_upstream_implementation(self):
        for build in (
            _workbook_with_percentages,
            _workbook_without_merges,
            _workbook_with_unformatted_cell_in_percentage_row,
        ):
            content = build()
            with self.subTest(fixture=build.__name__):
                # Explicitly off, so a stray env var in the calling shell
                # cannot turn this into an accidental behavior test.
                with patch.dict(os.environ, {PERCENT_ENV: "0"}):
                    self.assertEqual(
                        fill_merged_cells_xlsx(content),
                        _upstream_fill_merged_cells_xlsx(content),
                    )

    def test_env_var_off_forms_keep_the_raw_fraction(self):
        content = _workbook_with_percentages()
        for raw in ("0", "false", "FALSE", "no", "off", ""):
            with self.subTest(value=raw):
                with patch.dict(os.environ, {PERCENT_ENV: raw}):
                    self.assertEqual(
                        _read_cells(fill_merged_cells_xlsx(content), "B2"),
                        [0.61123366],
                    )


class PercentMaterializationOnTests(unittest.TestCase):
    """Enabled: percentages become the text the spreadsheet displays."""

    def test_percent_cells_become_display_text(self):
        content = _workbook_with_percentages()
        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            values = _read_cells(fill_merged_cells_xlsx(content), "B2", "C2", "B3", "C4")

        self.assertEqual(values, ["61.12%", "55.12%", "8.33%", "0.55%"])

    def test_merged_rows_below_the_master_are_materialized_too(self):
        """The order pitfall: B4 is covered by B3:B4 and has no format itself."""
        content = _workbook_with_percentages()
        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            b3, b4 = _read_cells(fill_merged_cells_xlsx(content), "B3", "B4")

        self.assertEqual(b3, "8.33%")
        self.assertEqual(b4, "8.33%")

    def test_no_row_shows_both_a_percentage_and_a_raw_fraction(self):
        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            document = ExcelParser().parse_into_text(_workbook_with_percentages())

        self.assertIn("A: 毛利率,B: 61.12%,C: 55.12%", document.content)
        self.assertIn("A: 收入占比,B: 8.33%,C: 9.12%", document.content)
        # Row 4 keeps its own percentage cell (C) and its merged B cell: both
        # must be text, or the same row would read as two different scales.
        self.assertIn("A: 收入占比(续),B: 8.33%,C: 0.55%", document.content)
        for raw in ("0.61123366", "0.5512345", "0.0833", "0.0912", "0.005509433890047721"):
            self.assertNotIn(raw, document.content)

    def test_currency_plain_numbers_and_dates_are_not_touched(self):
        content = _workbook_with_percentages()
        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            income, income_2025, plain, whole, start, end = _read_cells(
                fill_merged_cells_xlsx(content), "B5", "C5", "B7", "C7", "B6", "C6"
            )

        # Thousands separators / currency only affect readability.
        self.assertEqual(income, 1234567.89)
        self.assertEqual(income_2025, 9876543.21)
        # 0.5 has no percentage format, so it must stay a number.
        self.assertEqual(plain, 0.5)
        self.assertEqual(whole, 6100)
        # openpyxl already turns date formats into datetimes.
        self.assertEqual(start, datetime.datetime(2025, 1, 31))
        self.assertEqual(end, datetime.datetime(2024, 12, 31))

    def test_decimals_follow_the_number_format(self):
        """The text mirrors what the format displays: 0%, 0.0%, 0.00%."""
        wb = openpyxl.Workbook()
        ws = wb.active
        ws.append(["项目", "0%", "0.0%", "0.00%", "red"])
        ws.append(["毛利率", 0.61123366, 0.61123366, 0.61123366, 0.61123366])
        for col, fmt in (
            (2, "0%"),
            (3, "0.0%"),
            (4, "#,##0.00%"),
            (5, "0.00%;[Red]-0.00%"),
        ):
            ws.cell(row=2, column=col).number_format = fmt
        bio = io.BytesIO()
        wb.save(bio)

        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            values = _read_cells(
                fill_merged_cells_xlsx(bio.getvalue()), "B2", "C2", "D2", "E2"
            )

        self.assertEqual(values, ["61%", "61.1%", "61.12%", "61.12%"])

    def test_unformatted_cell_in_a_percentage_row_needs_the_opt_in_fallback(self):
        content = _workbook_with_unformatted_cell_in_percentage_row()
        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            self.assertEqual(
                _read_cells(fill_merged_cells_xlsx(content), "D2"), [0.5]
            )
        with patch.dict(
            os.environ, {PERCENT_ENV: "true", ROW_HINT_ENV: "3"}
        ):
            self.assertEqual(
                _read_cells(fill_merged_cells_xlsx(content), "D2"), ["50.00%"]
            )


class PercentageFormatSyntaxTests(unittest.TestCase):
    def test_literal_and_conditional_percent_formats_are_not_scaled(self):
        for fmt in (r"0.00\%", '0.00"%"', '[>1]0%;0.00%', '0.00%" growth"'):
            with self.subTest(fmt=fmt):
                self.assertIsNone(_format_percentage(0.5, fmt))

    def test_selects_the_section_for_the_value(self):
        for value, fmt, expected in (
            (0.5, "0%;0.00%", "50%"),
            (-0.5, "0%;[Red]-0.00%", "-50.00%"),
            (-0.5, "0%;(0.00%)", "(50.00%)"),
            (0, "0%;0.00%;0.0%", "0.0%"),
            (-0.5, "0.00%", "-50.00%"),
            (12.345, "#,##0.00%", "1,234.50%"),
        ):
            with self.subTest(value=value, fmt=fmt):
                self.assertEqual(_format_percentage(value, fmt), expected)

    def test_row_hint_does_not_convert_literal_percent_or_count_it_as_a_hint(self):
        wb = openpyxl.Workbook()
        ws = wb.active
        ws.append(["label", "percent", "literal", "plain"])
        ws.append(["mixed", 0.5, 0.5, 0.5])
        ws.append(["literal only", 0.5, 0.5, 0.5])
        ws["B2"].number_format = "0%"
        ws["C2"].number_format = r"0.00\%"
        ws["B3"].number_format = '0.00"%"'
        data = io.BytesIO()
        wb.save(data)
        with patch.dict(os.environ, {PERCENT_ENV: "true", ROW_HINT_ENV: "1"}):
            result = fill_merged_cells_xlsx(data.getvalue())
        self.assertEqual(_read_cells(result, "B2", "C2", "D2"), ["50%", 0.5, "50.00%"])
        self.assertEqual(_read_cells(result, "B3", "C3", "D3"), [0.5, 0.5, 0.5])


class PercentMaterializationSwitchTests(unittest.TestCase):
    """The switch itself: env default, per-upload override, precedence."""

    def test_env_var_true_forms_enable_materialization(self):
        content = _workbook_with_percentages()
        for raw in ("1", "true", "TRUE", "yes", "y", "on"):
            with self.subTest(value=raw):
                with patch.dict(os.environ, {PERCENT_ENV: raw}):
                    self.assertEqual(
                        _read_cells(fill_merged_cells_xlsx(content), "B2"),
                        ["61.12%"],
                    )

    def test_excel_parser_accepts_the_override_key(self):
        document = ExcelParser(xlsx_percent_as_text="true").parse_into_text(
            _workbook_with_percentages()
        )

        self.assertIn("A: 毛利率,B: 61.12%", document.content)

    def test_parser_engine_overrides_key_enables_materialization(self):
        document = Parser().parse_file(
            "report.xlsx",
            "xlsx",
            _workbook_with_percentages(),
            engine_overrides={OVERRIDE_KEY: "true"},
        )

        self.assertIn("A: 毛利率,B: 61.12%", document.content)

    def test_upload_override_wins_over_the_env_default(self):
        content = _workbook_with_percentages()

        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            off = ExcelParser(xlsx_percent_as_text="false").parse_into_text(content)
        with patch.dict(os.environ, {PERCENT_ENV: "false"}):
            on = ExcelParser(xlsx_percent_as_text="true").parse_into_text(content)

        self.assertNotIn("61.12%", off.content)
        self.assertIn("B: 0.61123366", off.content)
        self.assertIn("61.12%", on.content)

    def test_absent_override_keeps_the_env_default(self):
        content = _workbook_with_percentages()
        with patch.dict(os.environ, {PERCENT_ENV: "true"}):
            document = ExcelParser().parse_into_text(content)

        self.assertIn("A: 毛利率,B: 61.12%", document.content)


if __name__ == "__main__":
    unittest.main()
