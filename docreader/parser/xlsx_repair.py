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


STYLES_PART_SUFFIX = "styles.xml"
_REPLACEMENT_FILL = b'<fill><patternFill patternType="none"/></fill>'
_FILLS_BLOCK_RE = re.compile(rb"<fills\b[^>]*>.*?</fills>", re.S)
# Case-insensitive so wrong-case <Fill>…</Fill> entries are caught too.
_FILL_ELEMENT_RE = re.compile(rb"<fill\s*/>|<fill\s*>.*?</fill\s*>", re.S | re.I)
# A fill openpyxl can deserialize starts (case-sensitively) with a
# patternFill or gradientFill child — everything else, including the
# self-closing <fill/>, is rejected by its sequence descriptor.
_WELL_FORMED_FILL_RE = re.compile(rb"<fill\s*>\s*<(?:patternFill|gradientFill)[\s/>]")


def sanitize_xlsx_styles(content: bytes) -> bytes | None:
    """Rewrite styles.xml fills that openpyxl cannot deserialize.

    Some exporters (cloud-doc systems, LabView, Acumatica ERP) write ``<fill>``
    elements with no child, or a child that is neither patternFill nor
    gradientFill. openpyxl's strict sequence descriptor raises
    ``TypeError: expected <class 'openpyxl.styles.fills.Fill'>`` on them and
    treats the workbook as non-conformant, so both parse engines die on a
    file Excel itself opens fine (#3637). Replacing each malformed fill in
    place with a no-op patternFill keeps the fill list length — and therefore
    every cellXf fillId reference — stable. Returns None when nothing needs
    fixing, so callers pay one styles.xml scan on the happy path.

    Never raises: on any internal failure the original bytes flow on
    unchanged, so a caller recovering from its own error keeps that error's
    cause instead of trading it for a sanitizer failure.
    """
    try:
        return _sanitize_xlsx_styles(content)
    except Exception:
        logger.warning(
            "XLSX styles sanitize failed; passing original bytes through",
            exc_info=True,
        )
        return None


def _sanitize_xlsx_styles(content: bytes) -> bytes | None:
    if not zipfile.is_zipfile(io.BytesIO(content)):
        return None

    with zipfile.ZipFile(io.BytesIO(content)) as zin:
        names = _normalized_names(zin.namelist())
        styles_path = next(
            (n for n in names if n.lower().endswith(STYLES_PART_SUFFIX)), None
        )
        if styles_path is None:
            return None
        styles = zin.read(styles_path)

        block = _FILLS_BLOCK_RE.search(styles)
        if block is None:
            return None

        fixed_count = 0

        def _fix(match: "re.Match[bytes]") -> bytes:
            nonlocal fixed_count
            if _WELL_FORMED_FILL_RE.match(match.group(0)):
                return match.group(0)
            fixed_count += 1
            return _REPLACEMENT_FILL

        patched_block = _FILL_ELEMENT_RE.sub(_fix, block.group(0))
        if fixed_count == 0:
            return None

        # Doubles as the recurrence meter for #3637: how often real uploads
        # carry non-conforming fills, and how many per file.
        logger.info(
            "Sanitized %d non-conforming fill(s) in XLSX styles.xml before parse",
            fixed_count,
        )

        patched = styles[: block.start()] + patched_block + styles[block.end():]
        return _rewrite_zip(zin, lambda files: {**files, styles_path: patched})
