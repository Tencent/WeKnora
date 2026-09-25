"""Expand vertically merged DOCX table cells before Markdown conversion."""

from __future__ import annotations

import copy
import io
import logging
import zipfile

from lxml import etree

logger = logging.getLogger(__name__)

_WORD_NAMESPACE = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
_W = f"{{{_WORD_NAMESPACE}}}"
_TC_PR = f"{_W}tcPr"
_VMERGE = f"{_W}vMerge"
_GRID_SPAN = f"{_W}gridSpan"
_TR_PR = f"{_W}trPr"
_GRID_BEFORE = f"{_W}gridBefore"
_VAL = f"{_W}val"


def fill_vertical_merged_cells_docx(content: bytes) -> bytes:
    """Repeat a vertical merge's master content in every covered table row.

    DOCX stores vertical merges as a ``vMerge=restart`` cell followed by empty
    ``vMerge=continue`` cells. Mammoth preserves that as an HTML ``rowspan``,
    but Markdown tables cannot represent row spans and the later rows lose the
    merged value. This rewrites only Word XML parts that contain ``vMerge``;
    all other package parts and documents without vertical merges pass through
    unchanged.
    """
    source = io.BytesIO(content)
    if not zipfile.is_zipfile(source):
        return content

    with zipfile.ZipFile(source, "r") as archive:
        normalized_names = {
            info.filename.replace("\\", "/") for info in archive.infolist()
        }
        if "word/document.xml" not in normalized_names:
            return content

        rewritten_parts: dict[str, bytes] = {}
        filled_count = 0
        for info in archive.infolist():
            name = info.filename.replace("\\", "/")
            if not name.startswith("word/") or not name.endswith(".xml"):
                continue

            data = archive.read(info.filename)
            if b"vMerge" not in data:
                continue
            rewritten, part_filled_count = _fill_vertical_merges_in_xml(data)
            if rewritten is not None:
                rewritten_parts[info.filename] = rewritten
                filled_count += part_filled_count

        if not rewritten_parts:
            return content

        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as rewritten_archive:
            for info in archive.infolist():
                data = rewritten_parts.get(info.filename)
                if data is None:
                    data = archive.read(info.filename)
                rewritten_archive.writestr(info, data)

    logger.info(
        "Expanded %d vertically merged DOCX continuation cells before parse",
        filled_count,
    )
    return output.getvalue()


def _fill_vertical_merges_in_xml(data: bytes) -> tuple[bytes | None, int]:
    parser = etree.XMLParser(resolve_entities=False, no_network=True)
    try:
        root = etree.fromstring(data, parser=parser)
    except etree.XMLSyntaxError:
        return None, 0

    changed = False
    filled_count = 0
    # Process nested tables first so copied cell content is already normalized.
    for table in reversed(root.xpath(".//w:tbl", namespaces={"w": _WORD_NAMESPACE})):
        table_changed, table_filled_count = _fill_table_vertical_merges(table)
        changed = changed or table_changed
        filled_count += table_filled_count

    if not changed:
        return None, 0
    return etree.tostring(root, xml_declaration=True, encoding="UTF-8"), filled_count


def _fill_table_vertical_merges(table: etree._Element) -> tuple[bool, int]:
    active_merges: dict[int, tuple[etree._Element, ...]] = {}
    changed = False
    filled_count = 0

    for row in table.findall(f"{_W}tr"):
        next_active_merges: dict[int, tuple[etree._Element, ...]] = {}
        column = _grid_before(row)

        for cell in row.findall(f"{_W}tc"):
            cell_properties = cell.find(_TC_PR)
            span = _grid_span(cell_properties)
            occupied_columns = range(column, column + span)
            vertical_merge = (
                cell_properties.find(_VMERGE) if cell_properties is not None else None
            )

            if vertical_merge is not None:
                merge_type = vertical_merge.get(_VAL, "continue")
                if merge_type == "restart":
                    template = tuple(
                        copy.deepcopy(child) for child in cell if child.tag != _TC_PR
                    )
                    for occupied_column in occupied_columns:
                        next_active_merges[occupied_column] = template
                    cell_properties.remove(vertical_merge)
                    changed = True
                else:
                    template = next(
                        (
                            active_merges[occupied_column]
                            for occupied_column in occupied_columns
                            if occupied_column in active_merges
                        ),
                        None,
                    )
                    if template is not None:
                        for child in list(cell):
                            if child.tag != _TC_PR:
                                cell.remove(child)
                        for child in template:
                            cell.append(copy.deepcopy(child))
                        for occupied_column in occupied_columns:
                            next_active_merges[occupied_column] = template
                        cell_properties.remove(vertical_merge)
                        changed = True
                        filled_count += 1

            column += span

        active_merges = next_active_merges

    return changed, filled_count


def _grid_span(cell_properties: etree._Element | None) -> int:
    if cell_properties is None:
        return 1
    grid_span = cell_properties.find(_GRID_SPAN)
    if grid_span is None:
        return 1
    return _positive_int(grid_span.get(_VAL), default=1)


def _grid_before(row: etree._Element) -> int:
    row_properties = row.find(_TR_PR)
    if row_properties is None:
        return 0
    grid_before = row_properties.find(_GRID_BEFORE)
    if grid_before is None:
        return 0
    return _positive_int(grid_before.get(_VAL), default=0)


def _positive_int(value: str | None, default: int) -> int:
    try:
        parsed = int(value or "")
    except ValueError:
        return default
    return parsed if parsed >= 0 else default
