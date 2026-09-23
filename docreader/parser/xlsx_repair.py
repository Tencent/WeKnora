"""Repair common XLSX packaging issues before openpyxl/pandas read."""

from __future__ import annotations

import io
import logging
import re
import zipfile
from typing import Callable, Dict, Iterable, Set

logger = logging.getLogger(__name__)

SST_PART = "xl/sharedStrings.xml"
_SST_OVERRIDE_RE = re.compile(
    r'<Override[^>]*PartName="[^"]*sharedStrings\.xml"[^>]*/>',
    re.IGNORECASE,
)
_SST_REL_RE = re.compile(
    r'<Relationship[^>]*Type="[^"]*sharedStrings"[^>]*/>',
    re.IGNORECASE,
)


def repair_xlsx_bytes(content: bytes) -> bytes | None:
    """Return repaired XLSX bytes, or None if no repair was applied.

    Handles workbooks that reference ``xl/sharedStrings.xml`` in package
    metadata but omit the part (common with some exporters). When worksheets
    only use inline strings, manifest references are stripped so openpyxl can
    read the file.
    """
    if not zipfile.is_zipfile(io.BytesIO(content)):
        return None

    with zipfile.ZipFile(io.BytesIO(content), "r") as zin:
        names = _normalized_names(zin.namelist())
        sst_path = _find_shared_strings_path(names)
        if sst_path:
            if sst_path == SST_PART:
                return None
            return _rewrite_zip(
                zin, lambda files: _rename_shared_strings_part(files, sst_path)
            )
        if not _package_references_shared_strings(zin, names):
            return None
        if _worksheets_use_shared_string_cells(zin, names):
            return None
        return _rewrite_zip(zin, _strip_shared_strings_manifest)


# A data validation, or a scenario list, whose ``sqref`` openpyxl cannot parse
# aborts the whole workbook: openpyxl reads that attribute through a converter that
# turns the underlying range-parse ValueError into
# "TypeError: expected <class 'MultiCellRange'>" (#3599). Measured on openpyxl
# 3.1.5, a whole column (``C:C``), a whole row (``1:1``), a comma-separated
# list, ``#REF!`` and an out-of-bounds range all do it, in load_workbook and in
# pandas alike. Conditional formatting goes through the same converter, but
# openpyxl already catches it there and drops the rule with a warning, so it
# is not touched here. For scenarios the range sits on the ``<scenarios>``
# container, not on each ``<scenario>``, so the container is what goes.
_SQREF_ELEMENT_RE = re.compile(
    r"<(?P<tag>(?:\w+:)?(?:dataValidation|scenarios))\b[^>]*?(?:/>|>.*?</(?P=tag)>)",
    re.DOTALL,
)
_SQREF_ATTR_RE = re.compile(r'\bsqref="([^"]*)"')


def strip_unreadable_ranges_xlsx(content: bytes) -> bytes | None:
    """Drop data validations and scenario lists whose range openpyxl cannot parse.

    Returns the rewritten bytes, or None when every such range is readable, in
    which case the file is left exactly as it was. Neither element changes what
    a cell contains — a validation constrains input, a scenario holds what-if
    values that are not the displayed ones — so removing one costs text
    extraction nothing, while keeping it costs the whole document.
    """
    if not zipfile.is_zipfile(io.BytesIO(content)):
        return None

    with zipfile.ZipFile(io.BytesIO(content), "r") as zin:
        sheets = sorted(
            name
            for name in _normalized_names(zin.namelist())
            if name.startswith("xl/worksheets/") and name.endswith(".xml")
        )
        dropped: Dict[str, int] = {}
        for name in sheets:
            sheet = zin.read(name).decode("utf-8", errors="replace")
            count = sum(1 for m in _SQREF_ELEMENT_RE.finditer(sheet) if _unreadable(m.group(0)))
            if count:
                dropped[name] = count
        if not dropped:
            return None
        logger.warning(
            "Dropping %d data validation/scenario range(s) openpyxl cannot parse: %s",
            sum(dropped.values()),
            ", ".join(f"{name} ({n})" for name, n in dropped.items()),
        )
        return _rewrite_zip(zin, lambda files: _strip_unreadable_ranges(files, dropped))


def _unreadable(element: str) -> bool:
    """True when openpyxl would fail to build this element's range."""
    from openpyxl.worksheet.cell_range import MultiCellRange

    match = _SQREF_ATTR_RE.search(element)
    if match is None:
        return False
    try:
        MultiCellRange(match.group(1))
    except Exception:  # noqa: BLE001 - mirrors openpyxl's own bare except
        return True
    return False


def _strip_unreadable_ranges(files: Dict[str, bytes], sheets: Dict[str, int]) -> Dict[str, bytes]:
    updated = dict(files)
    for name in sheets:
        sheet = updated[name].decode("utf-8")
        sheet = _SQREF_ELEMENT_RE.sub(
            lambda m: "" if _unreadable(m.group(0)) else m.group(0), sheet
        )
        updated[name] = sheet.encode("utf-8")
    return updated

def _normalized_names(namelist: Iterable[str]) -> Set[str]:
    return {name.replace("\\", "/") for name in namelist}


def _find_shared_strings_path(names: Set[str]) -> str | None:
    for name in names:
        if name.lower().endswith("sharedstrings.xml"):
            return name
    return None


def _package_references_shared_strings(
    zin: zipfile.ZipFile, names: Set[str]
) -> bool:
    content_types = "[Content_Types].xml"
    if content_types in names:
        ct = zin.read(content_types).decode("utf-8", errors="replace")
        if "sharedstrings.xml" in ct.lower():
            return True

    rels_path = "xl/_rels/workbook.xml.rels"
    if rels_path in names:
        rels = zin.read(rels_path).decode("utf-8", errors="replace")
        if "sharedstrings" in rels.lower():
            return True
    return False


def _worksheets_use_shared_string_cells(
    zin: zipfile.ZipFile, names: Set[str]
) -> bool:
    for name in names:
        if not name.startswith("xl/worksheets/") or not name.endswith(".xml"):
            continue
        sheet = zin.read(name).decode("utf-8", errors="replace")
        if re.search(r'\bt="s"', sheet):
            return True
    return False


def _rename_shared_strings_part(
    files: Dict[str, bytes], source_path: str
) -> Dict[str, bytes]:
    updated = dict(files)
    updated[SST_PART] = updated.pop(source_path)
    return updated


def _strip_shared_strings_manifest(files: Dict[str, bytes]) -> Dict[str, bytes]:
    updated = dict(files)
    ct_path = "[Content_Types].xml"
    if ct_path in updated:
        ct = updated[ct_path].decode("utf-8")
        ct = _SST_OVERRIDE_RE.sub("", ct)
        updated[ct_path] = ct.encode("utf-8")

    rels_path = "xl/_rels/workbook.xml.rels"
    if rels_path in updated:
        rels = updated[rels_path].decode("utf-8")
        rels = _SST_REL_RE.sub("", rels)
        updated[rels_path] = rels.encode("utf-8")
    return updated


def _rewrite_zip(
    zin: zipfile.ZipFile,
    transform: Callable[[Dict[str, bytes]], Dict[str, bytes]],
) -> bytes:
    files: Dict[str, bytes] = {}
    for info in zin.infolist():
        name = info.filename.replace("\\", "/")
        files[name] = zin.read(info.filename)
    files = transform(files)

    out = io.BytesIO()
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
        for name, data in files.items():
            zout.writestr(name, data)
    return out.getvalue()
