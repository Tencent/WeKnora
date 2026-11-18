"""Tests for the XLSX merge-range area guard.

openpyxl expands a merged range at load time, so a tiny workbook that merely
*declares* a huge merge is expensive before any parser code runs: a 1.6 KB file
declaring ``A1:P100000`` (1.6M cells) takes ~15 s and ~576 MB to load, and
``A1:XFD1048576`` (171.8 billion cells) never returns.

The guard therefore runs on the raw bytes, before ``load_workbook``: it scans
``xl/worksheets/*.xml`` for ``<mergeCell ref=...>``, drops the declarations whose
area exceeds a limit, and rewrites the ``count`` attribute. The master cell's
value is untouched, so a file that only merged an implausible area still yields
its data.
"""

import os
import time
import unittest
import zipfile
from io import BytesIO
from unittest.mock import patch

from docreader.parser.xlsx_merge import (
    MERGE_FILL_LIMIT_ENV,
    MERGE_TOTAL_LIMIT_ENV,
    _guard_merge_ranges,
    _merge_area,
)

SHEET_XML = "xl/worksheets/sheet1.xml"


def _build_workbook(merge_ref: str, cells: str = "") -> bytes:
    """A minimal valid xlsx whose only unusual feature is the declared merge."""
    content_types = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
        '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
        '<Default Extension="xml" ContentType="application/xml"/>'
        '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
        '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
        "</Types>"
    )
    root_rels = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
        '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>'
        "</Relationships>"
    )
    workbook = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" '
        'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
        '<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>'
    )
    workbook_rels = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
        '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>'
        "</Relationships>"
    )
    sheet = (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">'
        f'<dimension ref="{merge_ref.split(":")[1]}"/>'
        f'<sheetData>{cells}</sheetData>'
        f'<mergeCells count="1"><mergeCell ref="{merge_ref}"/></mergeCells>'
        "</worksheet>"
    )
    buffer = BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("[Content_Types].xml", content_types)
        archive.writestr("_rels/.rels", root_rels)
        archive.writestr("xl/workbook.xml", workbook)
        archive.writestr("xl/_rels/workbook.xml.rels", workbook_rels)
        archive.writestr(SHEET_XML, sheet)
    return buffer.getvalue()


def _sheet_of(content: bytes) -> str:
    with zipfile.ZipFile(BytesIO(content)) as archive:
        return archive.read(SHEET_XML).decode("utf-8")


class MergeAreaTest(unittest.TestCase):
    def test_area_of_a_range(self):
        self.assertEqual(_merge_area("A1:P100000"), 16 * 100000)
        self.assertEqual(_merge_area("A1:B2"), 4)
        self.assertEqual(_merge_area("C3:C3"), 1)

    def test_unparseable_range_reports_zero(self):
        for ref in ("", "A1", "A1:", "not a range", "1:2"):
            self.assertEqual(_merge_area(ref), 0, ref)


class GuardTest(unittest.TestCase):
    def test_oversized_merge_is_dropped_and_count_rewritten(self):
        content = _build_workbook("A1:P100000")
        guarded = _guard_merge_ranges(content)

        sheet = _sheet_of(guarded)
        # The dimension tag may still name the far corner; it is the merge
        # declaration itself that openpyxl expands, so that is what must go.
        self.assertNotIn('<mergeCell ref="A1:P100000"/>', sheet)
        self.assertNotIn("<mergeCell ", sheet)
        self.assertIn('<mergeCells count="0"', sheet)
        self.assertLess(len(guarded), len(content) + 64)

    def test_normal_merge_survives(self):
        content = _build_workbook("A1:B2")
        self.assertEqual(_guard_merge_ranges(content), content)

    def test_workbook_without_merges_is_untouched(self):
        content = _build_workbook("A1:B2")
        with zipfile.ZipFile(BytesIO(content)) as archive:
            names = archive.namelist()
        self.assertIn(SHEET_XML, names)

    def test_master_cell_value_is_preserved(self):
        content = _build_workbook(
            "A1:P100000",
            cells='<row r="1"><c r="A1" t="inlineStr"><is><t>report</t></is></c></row>',
        )
        guarded = _guard_merge_ranges(content)
        self.assertIn("report", _sheet_of(guarded))

    def test_per_range_limit_is_configurable(self):
        content = _build_workbook("A1:D10")  # 40 cells
        with patch.dict(os.environ, {MERGE_FILL_LIMIT_ENV: "10"}):
            guarded = _guard_merge_ranges(content)
        self.assertNotIn('<mergeCells count="1"', _sheet_of(guarded))

    def test_total_limit_drops_the_remainder(self):
        content = _build_workbook("A1:D10")  # 40 cells
        with patch.dict(os.environ, {MERGE_TOTAL_LIMIT_ENV: "10"}):
            guarded = _guard_merge_ranges(content)
        self.assertNotIn('<mergeCells count="1"', _sheet_of(guarded))

    def test_invalid_env_falls_back_to_the_default(self):
        content = _build_workbook("A1:D10")
        with patch.dict(os.environ, {MERGE_FILL_LIMIT_ENV: "not-a-number"}):
            self.assertEqual(_guard_merge_ranges(content), content)


class EndToEndTest(unittest.TestCase):
    def test_the_bomb_loads_quickly_after_the_guard(self):
        from openpyxl import load_workbook

        content = _build_workbook("A1:P100000")
        started = time.time()
        workbook = load_workbook(BytesIO(_guard_merge_ranges(content)), data_only=True)
        elapsed = time.time() - started

        self.assertLess(elapsed, 2.0, "guarded load should not expand 1.6M cells")
        self.assertEqual(workbook.active.max_row * workbook.active.max_column, 1)


if __name__ == "__main__":
    unittest.main()
