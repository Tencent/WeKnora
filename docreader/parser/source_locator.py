"""Source locators: map ranges of parsed markdown back to the original file.

A source block is ``{"start": int, "end": int, "locator": {...}}`` where
``start``/``end`` are code point offsets into the document content (end
exclusive) and the locator says where that text sits in the original file.
For PDFs the locator is ``{"type": "pdf", "page": n}`` plus an optional
``"bbox": [x0, y0, x1, y1]`` given as fractions of the displayed page with
the origin at its top-left corner, so viewers can draw it at any zoom.
"""

from __future__ import annotations

import re
import statistics
import unicodedata
from typing import Iterable, List, Optional, Sequence, Tuple

# How far past the cursor a line is searched for, in normalized characters.
_SEARCH_WINDOW = 6000
# How much of a line's normalized text is searched for.
_KEY_CHARS = 24
# Lines shorter than this (normalized) are too ambiguous to place.
_MIN_KEY_CHARS = 2
# Lines shorter than this are placed only when their text is unique on the
# page: labels inside figures ("LLM", "EBAE") otherwise steal body offsets.
# Wide (CJK) characters count as four, since each carries about a word.
_SHORT_KEY_WEIGHT = 16

_LIST_START_RE = re.compile(
    r"^\s*(?:"
    r"\d{1,3}(?:\.\d{1,3}){0,4}[\.、．)）]?\s"  # 1  1.  8.14.11  2)
    r"|[（(]\d{1,3}[)）]"  # (1) （1）
    r"|[一二三四五六七八九十百]+[、．.]"  # 一、
    r"|[•·●○■□◆◇▪\-–*]\s"  # bullets
    r"|第[一二三四五六七八九十百零\d]+[章节条款部分篇]"  # 第三章
    r")"
)
_SENTENCE_END = tuple("。．.!?！？：:；;」』”)）")


def _fold(ch: str) -> str:
    """Letters and digits only, lower-cased, full-width folded."""
    if not ch.isalnum():
        return ""
    return unicodedata.normalize("NFKC", ch).lower()


def normalize_with_positions(text: str) -> Tuple[str, List[int]]:
    """Project text onto letters and digits.

    Returns the projection and, for each of its characters, the offset in
    ``text`` of the character it came from.
    """
    out: List[str] = []
    pos: List[int] = []
    for i, ch in enumerate(text):
        folded = _fold(ch)
        for f in folded:
            out.append(f)
            pos.append(i)
    return "".join(out), pos


def normalize(text: str) -> str:
    return "".join(_fold(ch) for ch in text)


def _key_weight(key: str) -> int:
    return sum(4 if unicodedata.east_asian_width(ch) in ("W", "F") else 1 for ch in key)


def _column_cursor(boxes, idx: int, ends: List[Optional[int]]) -> Optional[int]:
    """End of the nearest placed line above ``boxes[idx]`` in the same column."""
    x0, y0, x1, _ = boxes[idx]
    for j in range(idx - 1, max(-1, idx - 80), -1):
        if ends[j] is None:
            continue
        px0, py0, px1, _ = boxes[j]
        if px0 < x1 and x0 < px1 and py0 > y0:
            return ends[j]
    return None


def locate_lines(
    page_text: str,
    line_texts: Sequence[str],
    boxes: Optional[Sequence[Tuple[float, float, float, float]]] = None,
) -> List[Optional[int]]:
    """Find where each visual line starts in the page text.

    Lines are searched in order after the previous hit so repeated phrases
    resolve to the right occurrence. With ``boxes`` (PDF points, bottom-left
    origin) the search first continues from the line above in the same
    column, since glyph lines of side-by-side columns arrive interleaved. A
    line not found after either cursor is searched from the start of the page,
    which tolerates text extractors that order columns differently from the
    glyph grouping. Returns one offset (or None) per line.
    """
    norm, pos = normalize_with_positions(page_text)
    hits: List[Optional[int]] = []
    ends: List[Optional[int]] = []
    cursor = 0
    for idx, line in enumerate(line_texts):
        key = normalize(line)[:_KEY_CHARS]
        if len(key) < _MIN_KEY_CHARS or (
            _key_weight(key) < _SHORT_KEY_WEIGHT and norm.count(key) > 1
        ):
            hits.append(None)
            ends.append(None)
            continue
        cursors = [cursor]
        if boxes is not None:
            column = _column_cursor(boxes, idx, ends)
            if column is not None:
                cursors.insert(0, column)
        at = -1
        for start_at in cursors:
            at = norm.find(key, start_at, start_at + _SEARCH_WINDOW + len(key))
            if at >= 0:
                break
        if at < 0:
            at = norm.find(key)
            if at < 0:
                hits.append(None)
                ends.append(None)
                continue
        else:
            cursor = at + len(key)
        ends.append(at + len(key))
        start = pos[at]
        # Pull in leading punctuation or markup on the same text line
        # (brackets, "## ", list bullets) that normalization skipped.
        while start > 0 and page_text[start - 1] != "\n" and not page_text[start - 1].isalnum():
            start -= 1
        hits.append(start)
    return hits


class PageGeometry:
    """Converts PDF user-space points to fractions of the displayed page."""

    def __init__(self, cropbox: Tuple[float, float, float, float], rotation: int = 0):
        left, bottom, right, top = cropbox
        self.left, self.bottom = min(left, right), min(bottom, top)
        self.width = abs(right - left) or 1.0
        self.height = abs(top - bottom) or 1.0
        self.rotation = int(rotation or 0) % 360

    def bbox(self, box: Tuple[float, float, float, float]) -> List[float]:
        x0, y0, x1, y1 = box
        # Unrotated, top-left origin.
        ux0 = (x0 - self.left) / self.width
        ux1 = (x1 - self.left) / self.width
        uy0 = 1.0 - (y1 - self.bottom) / self.height
        uy1 = 1.0 - (y0 - self.bottom) / self.height
        corners = [self._rotate(ux0, uy0), self._rotate(ux1, uy1)]
        xs = [c[0] for c in corners]
        ys = [c[1] for c in corners]
        return [
            round(_clamp(min(xs)), 4),
            round(_clamp(min(ys)), 4),
            round(_clamp(max(xs)), 4),
            round(_clamp(max(ys)), 4),
        ]

    def _rotate(self, x: float, y: float) -> Tuple[float, float]:
        # Viewers display /Rotate clockwise.
        if self.rotation == 90:
            return 1.0 - y, x
        if self.rotation == 180:
            return 1.0 - x, 1.0 - y
        if self.rotation == 270:
            return y, 1.0 - x
        return x, y


def _clamp(v: float) -> float:
    return 0.0 if v < 0 else 1.0 if v > 1 else v


def _union(boxes: Iterable[Tuple[float, float, float, float]]):
    boxes = list(boxes)
    return (
        min(b[0] for b in boxes),
        min(b[1] for b in boxes),
        max(b[2] for b in boxes),
        max(b[3] for b in boxes),
    )


def _line_gap(prev: dict, cur: dict) -> Optional[float]:
    """Vertical gap from ``prev`` down to ``cur`` when they share a column."""
    px0, py0, px1, _ = prev["bbox"]
    cx0, _, cx1, cy1 = cur["bbox"]
    if cx1 < px0 or cx0 > px1 or cy1 > py0:
        return None
    return py0 - cy1


def _starts_paragraph(
    prev: dict, cur: dict, line_h: float, usual_gap: float, col_right: float
) -> bool:
    """Whether ``cur`` opens a new visual paragraph after ``prev``.

    Boxes are PDF points with a bottom-left origin, so reading downwards means
    decreasing y. ``usual_gap`` is the page's typical gap between consecutive
    lines of a paragraph, which absorbs double-spaced layouts.
    """
    px0, py0, px1, _ = prev["bbox"]
    cx0, _, cx1, cy1 = cur["bbox"]
    if cx1 < px0 or cx0 > px1:
        return True  # moved to another column
    if cy1 > py0 + line_h * 0.5:
        return True  # went back up the page: a new column or region
    if py0 - cy1 > usual_gap * 1.5 + line_h * 0.3:
        return True  # vertical gap wider than the usual line spacing
    if _LIST_START_RE.match(cur["text"]):
        return True
    ends_sentence = prev["text"].rstrip().endswith(_SENTENCE_END)
    short = col_right - px1 > line_h * 2
    return ends_sentence and short


def page_blocks(
    page_text: str,
    offset: int,
    page_number: int,
    lines: Sequence[dict],
    geometry: Optional[PageGeometry],
) -> List[dict]:
    """Source blocks for one text page placed at ``offset`` in the document.

    ``lines`` are visual lines ``{"text", "bbox"}`` (bbox in PDF points). Lines
    are placed in ``page_text``, grouped into paragraphs by geometry, and each
    paragraph becomes a block spanning from its first line to the next
    paragraph, boxed by the union of its lines. Text before the first placed
    line, or a page with no placeable lines, gets a page-only block, so the
    blocks always tile the page's text.
    """
    end = offset + len(page_text)
    page_only = {"type": "pdf", "page": page_number}
    if not page_text:
        return []
    placed = []
    if lines and geometry is not None:
        starts = locate_lines(
            page_text, [ln["text"] for ln in lines], [ln["bbox"] for ln in lines]
        )
        placed = sorted(
            (
                (at, ln)
                for at, ln in zip(starts, lines)
                if at is not None
            ),
            key=lambda item: item[0],
        )
    if not placed:
        return [{"start": offset, "end": end, "locator": dict(page_only)}]

    heights = [ln["bbox"][3] - ln["bbox"][1] for _, ln in placed if ln["bbox"][3] > ln["bbox"][1]]
    line_h = statistics.median(heights) if heights else 10.0
    gaps = [
        g
        for g in (_line_gap(a[1], b[1]) for a, b in zip(placed, placed[1:]))
        if g is not None and g >= 0
    ]
    usual_gap = statistics.median(gaps) if gaps else line_h * 0.5

    paragraphs: List[Tuple[int, List[dict]]] = []
    for at, ln in placed:
        if paragraphs:
            prev = paragraphs[-1][1][-1]
            col_right = max(p["bbox"][2] for p in paragraphs[-1][1])
            col_right = max(col_right, ln["bbox"][2])
            if not _starts_paragraph(prev, ln, line_h, usual_gap, col_right):
                paragraphs[-1][1].append(ln)
                continue
        paragraphs.append((at, [ln]))

    blocks: List[dict] = []
    first_at = paragraphs[0][0]
    if first_at > 0 and page_text[:first_at].strip():
        blocks.append({"start": offset, "end": offset + first_at, "locator": dict(page_only)})
    for i, (at, members) in enumerate(paragraphs):
        stop = paragraphs[i + 1][0] if i + 1 < len(paragraphs) else len(page_text)
        if i == 0:
            at = 0 if not blocks else at
        if stop <= at:
            continue
        locator = dict(page_only)
        locator["bbox"] = geometry.bbox(_union(m["bbox"] for m in members))
        blocks.append({"start": offset + at, "end": offset + stop, "locator": locator})
    return blocks
