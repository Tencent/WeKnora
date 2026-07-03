"""Structured DOCX table extraction helpers.

The parser in this module works from the original Word table XML instead of
from MarkItDown's markdown output. That lets us preserve merge semantics such
as ``gridSpan`` and ``vMerge`` before rendering rows into retrieval-friendly
key/value text.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from io import BytesIO
import re
from typing import Iterable, List, Optional

from docx import Document
from docx.oxml.ns import qn


@dataclass
class StructuredCell:
    text: str
    row: int
    col: int
    rowspan: int = 1
    colspan: int = 1


Matrix = List[List[Optional[StructuredCell]]]
LEADING_LIST_MARKER = re.compile(r"^\s*(?:\d+(?:\.\d+)*\.?|[A-Za-z]\.|[IVX]{1,5}\.)\s+")
VALUE_LIKE_CELL = re.compile(r"^\s*[-+]?\d+(?:[.,:/-]\d+)*(?:\s*(?:%|s|ms|sec|seconds|min|m|h|小时|分钟|秒))?\s*$", re.I)


@dataclass
class TableRenderState:
    col_count: int = 0
    context: List[str] = field(default_factory=list)
    headers: List[str] = field(default_factory=list)
    previous_values: List[str] = field(default_factory=list)


def structured_docx_tables_to_markdown(content: bytes) -> str:
    """Render DOCX tables as generic row-level key/value markdown."""
    doc = Document(BytesIO(content))
    sections = []
    state = TableRenderState()
    for index, table in enumerate(doc.tables, start=1):
        rendered, state = render_structured_table(table, index, state)
        if rendered:
            sections.append(rendered)
    if not sections:
        return ""
    return "## Structured tables\n\n" + "\n\n".join(sections)


def render_structured_table(
    table,
    table_index: int = 1,
    prior_state: Optional[TableRenderState] = None,
) -> tuple[str, TableRenderState]:
    """Render a single Word table into row-level semantic text."""
    matrix = table_to_matrix(table)
    if not matrix:
        return "", prior_state or TableRenderState()

    col_count = max((len(row) for row in matrix), default=0)
    if col_count == 0:
        return "", prior_state or TableRenderState()

    prior = prior_state or TableRenderState()
    can_inherit = (
        prior.col_count == col_count
        and bool(prior.headers)
        and not _starts_with_independent_header_row(matrix, col_count, prior)
    )
    context: List[str] = list(prior.context) if can_inherit else []
    header_row = -1
    headers: List[str] = list(prior.headers) if can_inherit else []
    previous_values: List[str] = (
        list(prior.previous_values) if can_inherit else [""] * col_count
    )
    data_started = False
    inherited_state = can_inherit
    output = [f"### Table {table_index}"]

    for row_idx, row in enumerate(matrix):
        normalized = _normalized_row(row, col_count)
        if _is_empty_row(normalized):
            continue

        if _is_wide_context_row(normalized, col_count):
            text = _unique_cells(normalized)[0].text
            if data_started or header_row >= 0:
                output.append(_render_note(table_index, text, context))
            else:
                if inherited_state:
                    context = []
                    headers = []
                    inherited_state = False
                context.append(text)
                output.append(_render_context(table_index, context))
            continue

        if (
            not inherited_state
            and not data_started
            and header_row < 0
            and _looks_like_header_row(normalized)
        ):
            header_row = row_idx
            headers = _headers_from_row(normalized, col_count)
            output.append(_render_header(table_index, headers, context))
            continue

        values = _row_values(normalized, headers, col_count, previous_values)
        if not any(value for _, value in values):
            continue
        data_started = True
        previous_values = [value for _, value in values]
        output.append(_render_data_row(table_index, row_idx + 1, values, context))

    body = "\n\n".join(part for part in output if part)
    next_state = TableRenderState(
        col_count=col_count,
        context=list(context),
        headers=list(headers),
        previous_values=list(previous_values),
    )
    rendered = body if body.strip() != f"### Table {table_index}" else ""
    return rendered, next_state


def table_to_matrix(table) -> Matrix:
    """Recover a logical table matrix from Word XML merge metadata."""
    rows: Matrix = []
    active_vmerges: dict[int, StructuredCell] = {}

    for row_idx, tr in enumerate(table._tbl.tr_lst):
        row: List[Optional[StructuredCell]] = []
        col_idx = _grid_before(tr)

        for tc in tr.tc_lst:
            while len(row) < col_idx:
                row.append(None)

            colspan = _grid_span(tc)
            vmerge = _vmerge_value(tc)
            if vmerge == "continue":
                origin = active_vmerges.get(col_idx)
                _append_span(row, origin, colspan)
                _extend_rowspan(origin, row_idx)
                col_idx += colspan
                continue

            text = _cell_text(tc)
            cell = StructuredCell(text=text, row=row_idx, col=col_idx, colspan=colspan)
            _append_span(row, cell, colspan)

            for offset in range(colspan):
                active_col = col_idx + offset
                if vmerge == "restart":
                    active_vmerges[active_col] = cell
                else:
                    active_vmerges.pop(active_col, None)
            col_idx += colspan

        rows.append(row)

    width = max((len(row) for row in rows), default=0)
    return [row + [None] * (width - len(row)) for row in rows]


def _append_span(row: List[Optional[StructuredCell]], cell: Optional[StructuredCell], span: int) -> None:
    for _ in range(max(1, span)):
        row.append(cell)


def _extend_rowspan(cell: Optional[StructuredCell], row_idx: int) -> None:
    if cell is None:
        return
    cell.rowspan = max(cell.rowspan, row_idx - cell.row + 1)


def _grid_before(tr) -> int:
    tr_pr = tr.trPr
    if tr_pr is None or tr_pr.gridBefore is None:
        return 0
    val = tr_pr.gridBefore.val
    return int(val) if val is not None else 0


def _grid_span(tc) -> int:
    tc_pr = tc.tcPr
    if tc_pr is None or tc_pr.gridSpan is None:
        return 1
    val = tc_pr.gridSpan.val
    return int(val) if val is not None else 1


def _vmerge_value(tc) -> str:
    tc_pr = tc.tcPr
    if tc_pr is None:
        return ""
    vmerge = tc_pr.find(qn("w:vMerge"))
    if vmerge is None:
        return ""
    val = vmerge.get(qn("w:val"))
    return "restart" if val == "restart" else "continue"


def _cell_text(tc) -> str:
    parts = []
    for paragraph in tc.p_lst:
        text = "".join(node.text or "" for node in paragraph.iter(qn("w:t")))
        if text.strip():
            parts.append(_clean_text(text))
    return " ".join(parts)


def _clean_text(text: str) -> str:
    return " ".join(text.replace("\xa0", " ").split())


def _normalized_row(row: List[Optional[StructuredCell]], col_count: int) -> List[Optional[StructuredCell]]:
    return row + [None] * (col_count - len(row))


def _unique_cells(row: Iterable[Optional[StructuredCell]]) -> List[StructuredCell]:
    seen = set()
    cells: List[StructuredCell] = []
    for cell in row:
        if cell is None or not cell.text:
            continue
        key = id(cell)
        if key in seen:
            continue
        seen.add(key)
        cells.append(cell)
    return cells


def _is_empty_row(row: List[Optional[StructuredCell]]) -> bool:
    return not _unique_cells(row)


def _is_wide_context_row(row: List[Optional[StructuredCell]], col_count: int) -> bool:
    cells = _unique_cells(row)
    if len(cells) != 1:
        return False
    cell = cells[0]
    return cell.colspan >= max(2, col_count - 1)


def _starts_with_independent_header_row(
    matrix: Matrix,
    col_count: int,
    prior: TableRenderState,
) -> bool:
    for row in matrix:
        normalized = _normalized_row(row, col_count)
        if _is_empty_row(normalized):
            continue
        if _is_wide_context_row(normalized, col_count):
            return False
        if not _looks_like_header_row(normalized):
            return False
        return not _row_overlaps_prior_values(normalized, prior.previous_values)
    return False


def _looks_like_header_row(row: List[Optional[StructuredCell]]) -> bool:
    if any(cell is None or not cell.text for cell in row):
        return False
    cells = _unique_cells(row)
    if len(cells) < 2:
        return False
    lengths = [len(cell.text) for cell in cells if cell.text]
    if len(lengths) < 2:
        return False
    if max(lengths) > 50:
        return False
    if sum(lengths) / len(lengths) > 25:
        return False
    if any(VALUE_LIKE_CELL.match(cell.text) for cell in cells):
        return False
    return not any(LEADING_LIST_MARKER.match(cell.text) for cell in cells)


def _row_overlaps_prior_values(
    row: List[Optional[StructuredCell]],
    previous_values: List[str],
) -> bool:
    for idx, cell in enumerate(row):
        if cell is None or idx >= len(previous_values):
            continue
        if cell.text and previous_values[idx] and cell.text == previous_values[idx]:
            return True
    return False


def _headers_from_row(row: List[Optional[StructuredCell]], col_count: int) -> List[str]:
    headers = []
    for index, cell in enumerate(row[:col_count], start=1):
        value = cell.text if cell is not None else ""
        headers.append(value or f"Column {index}")
    return _dedupe_headers(headers)


def _dedupe_headers(headers: List[str]) -> List[str]:
    counts: dict[str, int] = {}
    result = []
    for idx, header in enumerate(headers, start=1):
        name = header or f"Column {idx}"
        counts[name] = counts.get(name, 0) + 1
        result.append(name if counts[name] == 1 else f"{name} {counts[name]}")
    return result


def _row_values(
    row: List[Optional[StructuredCell]],
    headers: List[str],
    col_count: int,
    previous_values: List[str],
) -> List[tuple[str, str]]:
    if not headers:
        headers = [f"Column {idx}" for idx in range(1, col_count + 1)]

    values = []
    seen = set()
    for idx, cell in enumerate(row[:col_count]):
        header = headers[idx] if idx < len(headers) else f"Column {idx + 1}"
        if cell is None or id(cell) in seen:
            values.append((header, ""))
            continue
        seen.add(id(cell))
        values.append((header, cell.text))
    return _inherit_context_values(values, previous_values)


def _inherit_context_values(
    values: List[tuple[str, str]],
    previous_values: List[str],
) -> List[tuple[str, str]]:
    inherited = []
    last_non_empty = max((idx for idx, (_, value) in enumerate(values) if value), default=-1)
    for idx, (header, value) in enumerate(values):
        next_value = value
        if (
            not next_value
            and idx < last_non_empty
            and idx < len(previous_values)
            and previous_values[idx]
        ):
            next_value = previous_values[idx]
        inherited.append((header, next_value))
    return inherited


def _render_context(table_index: int, context: List[str]) -> str:
    return f"Table {table_index} context: {' > '.join(context)}"


def _render_note(table_index: int, note: str, context: List[str]) -> str:
    lines = [f"Table {table_index} note: {note}"]
    if context:
        lines.append(f"Context: {' > '.join(context)}")
    return "\n".join(lines)


def _render_header(table_index: int, headers: List[str], context: List[str]) -> str:
    lines = [f"Table {table_index} columns: {', '.join(headers)}"]
    if context:
        lines.append(f"Context: {' > '.join(context)}")
    return "\n".join(lines)


def _render_data_row(
    table_index: int,
    row_number: int,
    values: List[tuple[str, str]],
    context: List[str],
) -> str:
    lines = [f"Table {table_index} row {row_number}"]
    if context:
        lines.append(f"Context: {' > '.join(context)}")
    for header, value in values:
        if value:
            lines.append(f"{header}: {value}")
    return "\n".join(lines)
