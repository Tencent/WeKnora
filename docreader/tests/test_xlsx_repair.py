"""Shared-string repair must not break an already readable workbook."""

import io
import unittest
import xml.etree.ElementTree as ET
import zipfile

import openpyxl

from docreader.parser.xlsx_repair import repair_xlsx_bytes


_SML = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
_CONTENT_TYPES = "http://schemas.openxmlformats.org/package/2006/content-types"
_SST_TYPE = "application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"
_SST_REL = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings"
_VALUES = [("Name", 17), ("中文内容", 23)]


def _archive_bytes(files):
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, content in files.items():
            archive.writestr(name, content)
    return buffer.getvalue()


def _workbook_files(part_path, declared_path=None, shared_cells=True):
    declared_path = declared_path or part_path
    book = openpyxl.Workbook()
    for row in _VALUES:
        book.active.append(row)
    buffer = io.BytesIO()
    book.save(buffer)
    book.close()
    with zipfile.ZipFile(io.BytesIO(buffer.getvalue())) as archive:
        files = {name: archive.read(name) for name in archive.namelist()}

    if shared_cells:
        sheet = ET.fromstring(files["xl/worksheets/sheet1.xml"])
        for index, cell in enumerate(sheet.findall(f'.//{{{_SML}}}c[@t="inlineStr"]')):
            cell.set("t", "s")
            for child in list(cell):
                cell.remove(child)
            ET.SubElement(cell, f"{{{_SML}}}v").text = str(index)
        files["xl/worksheets/sheet1.xml"] = ET.tostring(sheet, encoding="utf-8")

    sst = ET.Element(f"{{{_SML}}}sst", count="2", uniqueCount="2")
    for text, _ in _VALUES:
        ET.SubElement(ET.SubElement(sst, f"{{{_SML}}}si"), f"{{{_SML}}}t").text = text
    files[part_path] = ET.tostring(sst, encoding="utf-8")
    override = f'<Override PartName="/{declared_path}" ContentType="{_SST_TYPE}"/>'
    files["[Content_Types].xml"] = files["[Content_Types].xml"].replace(
        b"</Types>", override.encode() + b"</Types>"
    )
    relationship = (
        f'<Relationship Id="rIdSharedStrings" Type="{_SST_REL}" '
        f'Target="/{declared_path}"/>'
    )
    rels = "xl/_rels/workbook.xml.rels"
    files[rels] = files[rels].replace(
        b"</Relationships>", relationship.encode() + b"</Relationships>"
    )
    return files


def _read_values(content):
    book = openpyxl.load_workbook(io.BytesIO(content), data_only=True)
    try:
        return list(book.active.values)
    finally:
        book.close()


class TestSharedStringsRepair(unittest.TestCase):
    def _assert_readable_unchanged(self, files):
        original = _archive_bytes(files)
        self.assertEqual(_read_values(original), _VALUES)
        repaired = repair_xlsx_bytes(original)
        # Verify the reader result first so a broken path reports its KeyError.
        self.assertEqual(
            _read_values(repaired if repaired is not None else original), _VALUES
        )
        self.assertIsNone(repaired)

    def test_canonical_part_is_unchanged(self):
        self._assert_readable_unchanged(_workbook_files("xl/sharedStrings.xml"))

    def test_declared_mixed_case_part_is_unchanged(self):
        self._assert_readable_unchanged(_workbook_files("xl/SharedStrings.xml"))

    def test_declared_nested_part_is_unchanged(self):
        self._assert_readable_unchanged(_workbook_files("xl/strings/sharedStrings.xml"))

    def test_prefixed_content_types_are_respected(self):
        files = _workbook_files("xl/strings/sharedStrings.xml")
        # ElementTree serializes the namespace with a prefix; it has the same meaning.
        types = ET.fromstring(files["[Content_Types].xml"])
        self.assertEqual(types.tag, f"{{{_CONTENT_TYPES}}}Types")
        files["[Content_Types].xml"] = ET.tostring(types, encoding="utf-8")
        self._assert_readable_unchanged(files)

    def test_other_shared_string_filename_is_unchanged(self):
        self._assert_readable_unchanged(_workbook_files("xl/strings/text.xml"))

    def test_misnamed_part_still_repairs_a_dangling_canonical_reference(self):
        original = _archive_bytes(
            _workbook_files("xl/SharedStrings.xml", "xl/sharedStrings.xml")
        )
        with self.assertRaises(KeyError):
            _read_values(original)
        repaired = repair_xlsx_bytes(original)
        self.assertIsNotNone(repaired)
        self.assertEqual(_read_values(repaired), _VALUES)

    def test_missing_table_references_are_removed_for_inline_cells(self):
        files = _workbook_files("xl/sharedStrings.xml", shared_cells=False)
        del files["xl/sharedStrings.xml"]
        original = _archive_bytes(files)
        with self.assertRaises(KeyError):
            _read_values(original)
        repaired = repair_xlsx_bytes(original)
        self.assertIsNotNone(repaired)
        self.assertEqual(_read_values(repaired), _VALUES)

    def test_missing_table_is_not_discarded_when_cells_need_it(self):
        files = _workbook_files("xl/sharedStrings.xml")
        del files["xl/sharedStrings.xml"]
        self.assertIsNone(repair_xlsx_bytes(_archive_bytes(files)))

    def test_non_zip_input_is_unchanged(self):
        self.assertIsNone(repair_xlsx_bytes(b"not an XLSX"))


if __name__ == "__main__":
    unittest.main()
