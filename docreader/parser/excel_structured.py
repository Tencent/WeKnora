"""Structured Excel extraction for table-like workbooks.

The fallback Excel parser flattens rows as ``A: value,B: value``. That is
robust, but it loses table semantics and can duplicate long merged notes across
columns. This module keeps a conservative structured path for common RAG
workbooks: policy tables, FAQ sheets, catalogs, and other header-driven data.
"""

from __future__ import annotations

from dataclasses import dataclass
import re
from typing import Any, Dict, List, Optional

import pandas as pd

from docreader.models.document import Chunk, Document


MAX_HEADER_CHARS = 40
MIN_STRUCTURED_CHUNKS = 2
IMAGE_FUNC_RE = re.compile(r"^=?(_xlfn\.)?(DISPIMG|IMAGE)\(", re.IGNORECASE)


@dataclass
class StructuredSheet:
    """Detected table layout for one worksheet."""

    name: str
    df: pd.DataFrame
    header_idx: int
    headers: Dict[Any, str]
    note_columns: set[Any]


def build_structured_excel_document(
    sheet_frames: List[tuple[str, pd.DataFrame]],
) -> Optional[Document]:
    """Build a semantically structured document when sheets look table-like.

    Returns ``None`` when the workbook does not have enough detectable table
    structure. Callers should then use the legacy row-flattening path.
    """

    chunks: List[Chunk] = []
    parts: List[str] = []
    start = 0

    for sheet_name, df in sheet_frames:
        detected = _detect_sheet(sheet_name, df)
        if detected is None:
            if _sheet_has_content(df):
                return None
            continue

        sheet_intro = _format_sheet_intro(detected)
        if sheet_intro:
            start = _append_chunk(
                chunks,
                parts,
                sheet_intro,
                start,
                {
                    "parser": "structured_excel",
                    "sheet": sheet_name,
                    "kind": "sheet_notes",
                },
            )

        for row_idx in range(detected.header_idx + 1, len(detected.df)):
            record = _format_record(detected, row_idx)
            if not record:
                continue
            metadata = {
                "parser": "structured_excel",
                "sheet": sheet_name,
                "kind": "table_record",
                "row": row_idx + 1,
                "headers": _data_headers(detected),
            }
            start = _append_chunk(chunks, parts, record, start, metadata)

    if len(chunks) < MIN_STRUCTURED_CHUNKS:
        return None

    return Document(
        content="".join(parts),
        chunks=chunks,
        metadata={"parser": "structured_excel"},
    )


def _append_chunk(
    chunks: List[Chunk],
    parts: List[str],
    content: str,
    start: int,
    metadata: Dict[str, Any],
) -> int:
    if not content.endswith("\n"):
        content += "\n"
    end = start + len(content)
    parts.append(content)
    chunks.append(
        Chunk(
            content=content,
            seq=len(chunks),
            start=start,
            end=end,
            metadata=metadata,
        )
    )
    return end


def _detect_sheet(sheet_name: str, df: pd.DataFrame) -> Optional[StructuredSheet]:
    if df.empty:
        return None

    work = df.dropna(how="all").reset_index(drop=True)
    if work.empty:
        return None

    header_idx = _find_header_row(work)
    if header_idx is None:
        return None

    headers = _headers_from_row(work.iloc[header_idx])
    if len(headers) < 2:
        return None

    note_columns = _detect_long_context_columns(work, header_idx, headers)

    # Keep at least two data columns; otherwise structured mode would be less
    # useful than the legacy parser.
    data_headers = [h for col, h in headers.items() if col not in note_columns]
    if len(data_headers) < 2:
        return None

    return StructuredSheet(
        name=sheet_name,
        df=work,
        header_idx=header_idx,
        headers=headers,
        note_columns=note_columns,
    )


def _data_headers(sheet: StructuredSheet) -> List[str]:
    return [
        header
        for col, header in sheet.headers.items()
        if col not in sheet.note_columns
    ]


def _sheet_has_content(df: pd.DataFrame) -> bool:
    if df.empty:
        return False
    for value in df.to_numpy().flatten():
        if _cell_text(value):
            return True
    return False


def _find_header_row(df: pd.DataFrame) -> Optional[int]:
    best_idx: Optional[int] = None
    best_score = 0.0
    scan_limit = min(len(df), 20)
    for idx in range(scan_limit):
        values = [_cell_text(v) for v in df.iloc[idx].tolist()]
        non_empty = [v for v in values if v]
        if len(non_empty) < 2:
            continue

        short_values = [v for v in non_empty if len(v) <= MAX_HEADER_CHARS]
        unique_values = set(non_empty)
        unique_ratio = len(unique_values) / len(non_empty)
        score = len(short_values) + unique_ratio

        # Rows filled from a horizontally merged note usually contain the same
        # long value in every column. They should not become headers.
        if len(unique_values) == 1 and len(non_empty) > 1:
            continue
        if len(short_values) < 2:
            continue
        if score > best_score:
            best_score = score
            best_idx = idx

    return best_idx


def _headers_from_row(row: pd.Series) -> Dict[Any, str]:
    headers: Dict[Any, str] = {}
    used: Dict[str, int] = {}
    for col, value in row.items():
        header = _cell_text(value)
        if not header:
            continue
        if len(header) > MAX_HEADER_CHARS:
            continue
        count = used.get(header, 0)
        used[header] = count + 1
        if count:
            header = f"{header}_{count + 1}"
        headers[col] = header
    return headers


def _detect_long_context_columns(
    df: pd.DataFrame,
    header_idx: int,
    headers: Dict[Any, str],
) -> set[Any]:
    note_columns: set[Any] = set()
    for col in headers:
        values = [
            _cell_text(df.iloc[row_idx][col])
            for row_idx in range(header_idx + 1, len(df))
        ]
        non_empty = [v for v in values if v]
        if not non_empty:
            continue
        long_count = sum(1 for v in non_empty if len(v) > 180)
        distinct_count = len(set(non_empty))
        if long_count >= 2 and distinct_count <= max(2, len(non_empty) // 4):
            note_columns.add(col)
    return note_columns


def _format_sheet_intro(sheet: StructuredSheet) -> str:
    notes = _collect_notes(sheet)
    lines = [f"## Sheet: {sheet.name}\n"]
    if notes:
        lines.append("### Notes")
        for note in notes:
            lines.append(f"- {note}")
    return "\n".join(lines).strip() + "\n"


def _collect_notes(sheet: StructuredSheet) -> List[str]:
    notes: List[str] = []
    seen: set[str] = set()

    for row_idx in range(0, sheet.header_idx):
        for value in sheet.df.iloc[row_idx].tolist():
            _add_note(notes, seen, _cell_text(value))

    for row_idx in range(sheet.header_idx + 1, len(sheet.df)):
        for col in sheet.note_columns:
            _add_note(notes, seen, _cell_text(sheet.df.iloc[row_idx][col]))

    return notes


def _add_note(notes: List[str], seen: set[str], value: str) -> None:
    if len(value) < 20:
        return
    if value in seen:
        return
    seen.add(value)
    notes.append(value)


def _format_record(sheet: StructuredSheet, row_idx: int) -> str:
    row = sheet.df.iloc[row_idx]
    fields: List[tuple[str, str]] = []
    for col, header in sheet.headers.items():
        if col in sheet.note_columns:
            continue
        value = _cell_text(row[col])
        if not value or value == header:
            continue
        fields.append((header, value))

    if _looks_like_repeated_note_row(fields):
        return ""
    if not fields:
        return ""

    lines = [f"### {sheet.name} - Row {row_idx + 1}"]
    for header, value in fields:
        lines.append(f"- {header}: {value}")
    return "\n".join(lines) + "\n"


def _looks_like_repeated_note_row(fields: List[tuple[str, str]]) -> bool:
    """Detect rows created by horizontally filled merged notes.

    openpyxl merge filling copies a wide note into each covered column. Such a
    row should be represented once in the sheet notes, not as a table record.
    """

    if len(fields) < 2:
        return False
    values = [value for _, value in fields if value]
    unique_values = set(values)
    if len(unique_values) != 1:
        return False
    only_value = values[0]
    return len(only_value) > 80


def _cell_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        if pd.isna(value):
            return ""
    except (TypeError, ValueError):
        pass
    text = str(value).strip()
    if IMAGE_FUNC_RE.match(text):
        return ""
    if text.endswith(".0"):
        text = text[:-2]
    return text
