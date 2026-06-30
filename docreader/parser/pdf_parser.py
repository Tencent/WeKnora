"""PDF parsing with per-page routing between native text and scanned images.

Design (aligned with how MinerU / Docling / DeepDoc route PDFs):

* The dominant signal for "this page is scanned" is the **image-area coverage
  ratio** (image bounding-box area / page area), not the raw character count.
  A scanned page is essentially one big image covering the whole page, even
  when it carries a (often low-quality) embedded OCR text layer. Trusting that
  embedded text layer is what produced garbled RAG content before.
* Pages are classified independently so hybrid PDFs (some native, some scanned)
  are handled correctly. Native pages contribute their text layer; scanned
  pages are rendered to JPEG and tagged ``image_source_type=scanned_pdf`` so the
  Go App performs OCR/VLM on them (docreader itself never runs OCR).

No external services (e.g. MinerU) are required: the builtin engine is fully
self-sufficient using pypdfium2 + the Go-side OCR that already exists.
"""

import base64
import io
import logging
import os
import re
import statistics

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.concurrency import parser_worker_limit

logger = logging.getLogger(__name__)


def _env_float(name: str, default: float) -> float:
    v = os.environ.get(name)
    if v is None or not str(v).strip():
        return default
    try:
        return float(v)
    except ValueError:
        return default


def _env_int(name: str, default: int) -> int:
    v = os.environ.get(name)
    if v is None or not str(v).strip():
        return default
    try:
        return int(str(v).strip())
    except ValueError:
        return default


def _env_bool(name: str, default: bool) -> bool:
    v = os.environ.get(name)
    if v is None or not str(v).strip():
        return default
    return str(v).strip().lower() in {"1", "true", "yes", "y", "on"}


# A page whose image objects cover at least this fraction of the page area is
# treated as scanned (image-dominated). Native digital pages measure ~0.0-0.05;
# scanned pages measure ~1.0+, so 0.5 leaves a wide safety margin.
SCAN_IMAGE_AREA_RATIO = _env_float("DOCREADER_PDF_SCAN_IMAGE_RATIO", 0.5)
# Below this many characters a page is considered to have no usable text layer.
SCAN_MIN_CHARS_PER_PAGE = _env_int("DOCREADER_PDF_SCAN_MIN_CHARS", 10)
# A near-empty-text page is only rendered as an image if it actually contains
# some image content (avoids rendering genuinely blank pages).
_LOW_TEXT_IMAGE_RATIO = 0.1

# --- Embedded figure extraction (text pages) ------------------------------
# Native pages can embed figures/charts. We surface them as image references so
# the Go App can OCR/caption them (docreader does not caption). Logos, icons,
# watermarks and tiny decorations are filtered out by size, page-area share and
# cross-page repetition.
EXTRACT_EMBEDDED_IMAGES = _env_bool("DOCREADER_PDF_EXTRACT_EMBEDDED_IMAGES", True)
# Minimum pixel width AND height for an embedded image to be kept.
EMBED_MIN_PIXELS = _env_int("DOCREADER_PDF_EMBED_MIN_PIXELS", 80)
# Minimum share of the page area for an embedded image to be kept.
EMBED_MIN_AREA_RATIO = _env_float("DOCREADER_PDF_EMBED_MIN_AREA_RATIO", 0.01)
# An identical image appearing on at least this fraction of text pages is
# treated as a running logo/watermark and dropped.
EMBED_REPEAT_PAGE_FRAC = _env_float("DOCREADER_PDF_EMBED_REPEAT_PAGE_FRAC", 0.5)
# Hard cap on the number of embedded images extracted per document.
EMBED_MAX_IMAGES = _env_int("DOCREADER_PDF_EMBED_MAX_IMAGES", 50)

# --- Layout-aware text extraction (native text pages) ---------------------
# Reconstruct reading order with a geometric XY-cut so multi-column pages are
# linearised column-by-column instead of line-interleaved.
LAYOUT_ORDERING = _env_bool("DOCREADER_PDF_LAYOUT_ORDERING", True)
# When glyphs are positioned without explicit space characters (common in OCR /
# search text layers), insert a space if the horizontal gap exceeds this
# multiple of the line's median glyph width.
WORD_GAP_WIDTH_RATIO = _env_float("DOCREADER_PDF_WORD_GAP_WIDTH_RATIO", 0.4)
# Promote visually larger lines to markdown headings (font-size proxy = rect
# height relative to the page's median line height).
DETECT_HEADINGS = _env_bool("DOCREADER_PDF_DETECT_HEADINGS", True)
# Drop invisible (render-mode 3), off-page and degenerate text — a cheap guard
# against hidden-text prompt injection and OCR artefacts.
FILTER_HIDDEN_TEXT = _env_bool("DOCREADER_PDF_FILTER_HIDDEN_TEXT", True)
# Narrow side strips (arXiv watermarks, page labels) narrower than this share of
# page width are dropped when they look like vertical / single-glyph noise.
MARGIN_COL_WIDTH_RATIO = _env_float("DOCREADER_PDF_MARGIN_COL_WIDTH_RATIO", 0.12)
# Minimum characters on a line before font-size heuristics may promote it to a
# markdown heading (avoids ``### C`` from margin glyphs).
MIN_HEADING_LINE_CHARS = _env_int("DOCREADER_PDF_MIN_HEADING_LINE_CHARS", 8)
# Strip pdfium placeholder glyphs (U+FFFE) and soft hyphens; remove axis/legend text
# from vector figures when a Figure caption is present on the page.
SANITIZE_PDF_TEXT = _env_bool("DOCREADER_PDF_SANITIZE_TEXT", True)
STRIP_CHART_TEXT_DEBRIS = _env_bool("DOCREADER_PDF_STRIP_CHART_DEBRIS", True)
# Render detected vector chart regions (no embedded bitmap) as JPEG for VLM/OCR.
RENDER_VECTOR_FIGURES = _env_bool("DOCREADER_PDF_RENDER_VECTOR_FIGURES", True)
MIN_CHART_REGION_CHARS = _env_int("DOCREADER_PDF_MIN_CHART_REGION_CHARS", 18)
MIN_CHART_REGION_AREA_RATIO = _env_float("DOCREADER_PDF_MIN_CHART_REGION_AREA", 0.015)
MAX_CHART_REGION_AREA_RATIO = _env_float("DOCREADER_PDF_MAX_CHART_REGION_AREA", 0.42)
MAX_FIGURE_HEIGHT_RATIO = _env_float("DOCREADER_PDF_MAX_FIGURE_HEIGHT_RATIO", 0.38)

# --- Force scanned mode ------------------------------------------------------
# When True, ALL PDF pages are rendered as images and routed through OCR/VLM,
# bypassing the automatic text/scanned page classification. Useful for PDFs
# with a low-quality or misleading text layer (web-print, scanned, image-heavy).
# Can be overridden per-upload via parser_engine_overrides.pdf_force_scanned.
FORCE_SCANNED_PDF = _env_bool("DOCREADER_PDF_FORCE_SCANNED", False)

# pdfium / Adobe text layers often emit U+FFFE for missing hyphenation or ligatures.
_PDF_ARTIFACT_RE = re.compile(r"[\u00ad\u200b-\u200f\ufeff\ufffe\uffff]")
_PDF_ARTIFACT_JOIN_RE = re.compile(r"(\w)[\u00ad\ufffe](\w)")
_CHART_DEBRIS_LINE_RE = re.compile(
    r"^(?:"
    r"[\d\s.]+|"
    r"\d{1,2}|"
    r"\d+-layer|"
    r"iter\.\s*\(1e4\)|"
    r"(?:training|test)\s+error\s*\(%\)"
    r")$",
    re.IGNORECASE,
)
_CHART_LAYER_RE = re.compile(r"^\d+-layer$", re.IGNORECASE)
_FIGURE_CAPTION_RE = re.compile(r"^Figure\s+\d+\b", re.IGNORECASE)
_FIGURE_CAPTION_SEARCH_RE = re.compile(r"\bFigure\s+(\d+)\b", re.IGNORECASE)
_ARXIV_LINE_RE = re.compile(r"^arXiv:\s*\S+", re.IGNORECASE)
_PAGE_NUM_LINE_RE = re.compile(r"^\d{1,3}$")


def _close_pdfium_resource(resource) -> None:
    close = getattr(resource, "close", None)
    if close:
        close()


def _normalize_image_quality(quality: int) -> int:
    return min(95, max(1, quality))


def _classify_page(image_area_ratio: float, text_len: int) -> str:
    """Classify a page as ``"scanned"`` or ``"text"``.

    Image-area coverage is the primary signal; a sparse text layer combined with
    some image content is the secondary signal.
    """
    if image_area_ratio >= SCAN_IMAGE_AREA_RATIO:
        return "scanned"
    if text_len < SCAN_MIN_CHARS_PER_PAGE and image_area_ratio >= _LOW_TEXT_IMAGE_RATIO:
        return "scanned"
    return "text"


def _page_image_area_ratio(page, raw) -> float:
    """Return the fraction of the page area covered by image objects.

    Overlapping images can push the ratio above 1.0; callers only compare it
    against a threshold so that is harmless.
    """
    width, height = page.get_size()
    page_area = float(width) * float(height)
    if page_area <= 0:
        return 0.0

    image_area = 0.0
    for obj in page.get_objects():
        try:
            if obj.type == raw.FPDF_PAGEOBJ_IMAGE:
                left, bottom, right, top = obj.get_bounds()
                image_area += abs((right - left) * (top - bottom))
        except Exception:
            continue
    return image_area / page_area


def _extract_page_text(page) -> str:
    """Plain top-to-bottom text extraction (fallback path)."""
    textpage = None
    try:
        textpage = page.get_textpage()
        return textpage.get_text_range()
    finally:
        _close_pdfium_resource(textpage)


def _sanitize_pdf_text(text: str) -> str:
    """Remove PDF text-layer placeholders and repair broken hyphenations."""
    if not text:
        return text
    text = _PDF_ARTIFACT_RE.sub("", text)
    text = _PDF_ARTIFACT_JOIN_RE.sub(r"\1\2", text)
    return text


def _is_chart_debris_line(line: str) -> bool:
    t = line.strip()
    if not t:
        return False
    if _CHART_DEBRIS_LINE_RE.match(t):
        return True
    if _CHART_LAYER_RE.match(t):
        return True
    # Tick labels like "0 1 2 3 4 5 6 0"
    if re.fullmatch(r"[\d\s.()-]+", t) and len(t) <= 24 and sum(c.isdigit() for c in t) >= 3:
        return True
    return False


def _strip_chart_text_debris(text: str) -> str:
    """Drop runs of axis/legend lines leaked from vector figures into the text layer."""
    if not text:
        return text
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    out: list = []
    i = 0
    while i < len(lines):
        if _is_chart_debris_line(lines[i]):
            j = i
            while j < len(lines) and (
                _is_chart_debris_line(lines[j]) or not lines[j].strip()
            ):
                j += 1
            if j - i >= 3:
                i = j
                continue
        out.append(lines[i])
        i += 1
    return "\n".join(out)


def _strip_arxiv_and_page_num_lines(text: str) -> str:
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    kept: list = []
    for ln in lines:
        t = ln.strip()
        if _ARXIV_LINE_RE.match(t):
            continue
        if _PAGE_NUM_LINE_RE.match(t):
            continue
        if "arXiv:" in ln:
            ln = re.sub(r"\s*arXiv:\s*\S+\s*(?:\[[^\]]+\])?\s*[^\n]*", "", ln).strip()
            if not ln:
                continue
        kept.append(ln)
    return "\n".join(kept)


def _strip_lines_above_figure_captions(text: str) -> str:
    """Remove diagram/chart label lines that sit immediately above a Figure caption."""
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    out: list = []
    for ln in lines:
        if _line_has_figure_caption(ln):
            while out and _is_figure_interior_line(out[-1]):
                out.pop()
            out.append(ln)
        else:
            out.append(ln)
    return "\n".join(out)


def _is_body_paragraph_line(text: str) -> bool:
    t = text.strip()
    if len(t) < 48:
        return False
    return len(t.split()) >= 8


def _is_figure_interior_line(text: str) -> bool:
    """Short, non-body line directly above a Figure caption (diagram labels, ticks)."""
    t = text.strip()
    if not t or _FIGURE_CAPTION_RE.match(t):
        return False
    if _ARXIV_LINE_RE.match(t) or _PAGE_NUM_LINE_RE.match(t):
        return True
    if _is_body_paragraph_line(t):
        return False
    if _is_chart_debris_line(t):
        return True
    # Prose sentence above a figure (wrapped paragraph tail) — keep in text.
    if t.endswith((".", "。", "!", "?", "！")) and len(t) >= 15:
        return False
    if len(t.split()) >= 7:
        return False
    if len(t) <= 40:
        return True
    return False


def _postprocess_pdf_text(text: str) -> str:
    if SANITIZE_PDF_TEXT:
        text = _sanitize_pdf_text(text)
    text = _strip_arxiv_and_page_num_lines(text)
    text = _strip_lines_above_figure_captions(text)
    if STRIP_CHART_TEXT_DEBRIS:
        text = _strip_chart_text_debris(text)
    return text


def _char_looks_chart_axis_tick(ch: str) -> bool:
    """Axis tick / numeric chart labels only (not words like ``layer`` in diagrams)."""
    t = ch.strip()
    if not t:
        return False
    if len(t) == 1 and t in "0123456789.%()-":
        return True
    if _CHART_LAYER_RE.match(t):
        return True
    if re.fullmatch(r"iter\.\s*\(1e4\)", t, re.I):
        return True
    if re.fullmatch(r"(?:training|test)\s+error\s*\(%\)", t, re.I):
        return True
    return False


def _chars_bbox(char_list: list) -> tuple:
    return (
        min(c["x0"] for c in char_list),
        min(c["y0"] for c in char_list),
        max(c["x1"] for c in char_list),
        max(c["y1"] for c in char_list),
    )


def _bbox_area_ratio(bbox, page_w: float, page_h: float) -> float:
    page_area = float(page_w) * float(page_h)
    if page_area <= 0:
        return 0.0
    x0, y0, x1, y1 = bbox
    return max(0.0, (x1 - x0) * (y1 - y0) / page_area)


def _chart_region_bbox(chars: list, page_w: float, page_h: float):
    """Bounding box of numeric chart axis labels (fallback when caption walk fails)."""
    chart = [c for c in chars if _char_looks_chart_axis_tick(c["ch"])]
    if len(chart) < MIN_CHART_REGION_CHARS:
        return None
    bbox = _chars_bbox(chart)
    ratio = _bbox_area_ratio(bbox, page_w, page_h)
    if ratio < MIN_CHART_REGION_AREA_RATIO or ratio > MAX_CHART_REGION_AREA_RATIO:
        return None
    x0, y0, x1, y1 = bbox
    pad_x = max(8.0, (x1 - x0) * 0.08)
    pad_y = max(8.0, (y1 - y0) * 0.08)
    return (
        max(0.0, x0 - pad_x),
        max(0.0, y0 - pad_y),
        min(page_w, x1 + pad_x),
        min(page_h, y1 + pad_y),
    )


def _expand_chart_bbox(bbox, page_w: float, page_h: float, margin_frac: float = 0.18):
    x0, y0, x1, y1 = bbox
    dx = (x1 - x0) * margin_frac
    dy = (y1 - y0) * margin_frac
    return (
        max(0.0, x0 - dx),
        max(0.0, y0 - dy),
        min(page_w, x1 + dx),
        min(page_h, y1 + dy),
    )


def _render_page_clip_jpeg(page, bbox, scale: float, quality: int, max_edge: int) -> bytes:
    """Render a PDF page region to JPEG (bbox in PDF points, bottom-left origin)."""
    left, bottom, right, top = bbox
    scale_eff = _effective_scale(page, scale, max_edge)
    bitmap = None
    try:
        bitmap = page.render(scale=scale_eff)
        pil = bitmap.to_pil().convert("RGB")
    finally:
        _close_pdfium_resource(bitmap)
    page_w, page_h = page.get_size()
    x0 = int(left * scale_eff)
    x1 = int(right * scale_eff)
    y0 = int((page_h - top) * scale_eff)
    y1 = int((page_h - bottom) * scale_eff)
    if x1 <= x0 or y1 <= y0:
        raise ValueError("degenerate clip bbox")
    return _pil_to_jpeg_bytes(pil.crop((x0, y0, x1, y1)), quality)


def _pil_to_jpeg_bytes(pil, quality: int) -> bytes:
    buf = io.BytesIO()
    if pil.mode not in ("RGB", "L"):
        pil = pil.convert("RGB")
    pil.save(buf, format="JPEG", quality=quality, optimize=True)
    return buf.getvalue()


def _group_lines_with_chars(chars: list) -> list:
    """Group glyphs into lines; each line includes its char list and bbox."""
    if not chars:
        return []
    heights = [c["y1"] - c["y0"] for c in chars if c["y1"] > c["y0"]]
    med_h = statistics.median(heights) if heights else 1.0
    ordered = sorted(chars, key=lambda c: -(c["y0"] + c["y1"]) / 2)
    groups: list = []
    cur: list = []
    ref = None
    for c in ordered:
        yc = (c["y0"] + c["y1"]) / 2
        if ref is None or abs(yc - ref) <= 0.5 * med_h:
            cur.append(c)
            ref = yc if ref is None else ref
        else:
            groups.append(cur)
            cur = [c]
            ref = yc
    if cur:
        groups.append(cur)

    lines: list = []
    for grp in groups:
        grp_sorted = sorted(grp, key=lambda c: c["x0"])
        text = _join_line_glyphs(grp_sorted)
        if not text:
            continue
        hs = [c["y1"] - c["y0"] for c in grp_sorted if c["y1"] > c["y0"]]
        lines.append(
            {
                "text": text,
                "h": statistics.median(hs) if hs else med_h,
                "chars": grp_sorted,
                "bbox": _chars_bbox(grp_sorted),
            }
        )
    return lines


def _line_has_figure_caption(text: str) -> bool:
    return bool(_FIGURE_CAPTION_SEARCH_RE.search((text or "").strip()))


def _bbox_above_caption(lines: list, cap_i: int, page_w: float, page_h: float):
    """Region above a Figure caption line (PDF coords, bottom-left origin)."""
    cap_bbox = lines[cap_i]["bbox"]
    cap_top = cap_bbox[3]
    x0, x1 = cap_bbox[0], cap_bbox[2]
    fig_h = page_h * min(MAX_FIGURE_HEIGHT_RATIO, 0.35)
    y_bottom = cap_top
    y_top = min(page_h, cap_top + fig_h)

    for j in range(cap_i - 1, -1, -1):
        t = lines[j]["text"]
        b = lines[j]["bbox"]
        if b[3] < y_bottom - 4:
            continue
        if b[1] > y_top + 4:
            break
        if _is_body_paragraph_line(t) and not _is_figure_interior_line(t):
            break
        if _is_figure_interior_line(t) or _is_chart_debris_line(t) or not t.strip():
            x0 = min(x0, b[0])
            x1 = max(x1, b[2])
            y_top = max(y_top, min(page_h, b[3] + fig_h * 0.15))

    min_h = page_h * 0.08
    if y_top - y_bottom < min_h:
        y_top = min(page_h, y_bottom + min_h)
    margin_x = max(8.0, (x1 - x0) * 0.05)
    return (
        max(0.0, x0 - margin_x),
        y_bottom,
        min(page_w, x1 + margin_x),
        y_top,
    )


def _cap_bbox_height(bbox, page_h: float, cap_y_top: float) -> tuple:
    """Limit figure bbox height (PDF coords, bottom-left origin)."""
    x0, y0, x1, y1 = bbox
    max_top = min(y1, cap_y_top + page_h * MAX_FIGURE_HEIGHT_RATIO)
    if max_top <= y0:
        return bbox
    return (x0, y0, x1, max_top)


def _inject_figure_markdown_before_captions(text: str, clips: list) -> str:
    """Place ``![...]()`` immediately before each Figure caption line in page text."""
    if not clips:
        return text
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    clip_idx = 0
    for i, ln in enumerate(lines):
        if clip_idx >= len(clips):
            break
        if not _line_has_figure_caption(ln):
            continue
        if i > 0 and lines[i - 1].lstrip().startswith("!["):
            continue
        ref_path = clips[clip_idx][0]
        fname = os.path.basename(ref_path)
        img_md = f"![{fname}]({ref_path})"
        lines[i] = f"{img_md}\n\n{ln}"
        clip_idx += 1
    return "\n".join(lines)


def _extract_vector_figure_clips(
    page,
    page_index: int,
    plain_text: str,
    raw,
    base_name: str,
    scale: float,
    quality: int,
    max_edge: int,
) -> list:
    """Render vector figure regions anchored at each ``Figure N.`` caption on the page.

    Returns ``[(ref_path, b64, y_sort, caption_line), ...]`` for markdown injection.
    """
    if not RENDER_VECTOR_FIGURES or not re.search(r"\bFigure\s+\d+", plain_text, re.I):
        return []
    textpage = None
    try:
        textpage = page.get_textpage()
        chars, page_w = _page_chars(textpage, page, raw)
        if not chars:
            return []
        page_h = page.get_size()[1]
        lines = _merge_orphan_punctuation_lines(_group_lines_with_chars(chars))
        caption_indices = [
            i for i, ln in enumerate(lines) if _line_has_figure_caption(ln["text"])
        ]
        if not caption_indices:
            return []

        results: list = []
        for fig_idx, cap_i in enumerate(caption_indices):
            cap_line = lines[cap_i]["text"].strip()
            m = _FIGURE_CAPTION_SEARCH_RE.search(cap_line)
            if m:
                cap_line = cap_line[m.start() :].split("\n", 1)[0].strip()

            bbox = _bbox_above_caption(lines, cap_i, page_w, page_h)
            if bbox is None:
                bbox = _chart_region_bbox(chars, page_w, page_h)
            if bbox is None:
                continue

            ratio = _bbox_area_ratio(bbox, page_w, page_h)
            if ratio > MAX_CHART_REGION_AREA_RATIO:
                bbox = _cap_bbox_height(bbox, page_h, lines[cap_i]["bbox"][3])
                ratio = _bbox_area_ratio(bbox, page_w, page_h)
                if ratio > MAX_CHART_REGION_AREA_RATIO:
                    continue
            if ratio < MIN_CHART_REGION_AREA_RATIO:
                continue

            bbox = _expand_chart_bbox(bbox, page_w, page_h, margin_frac=0.06)
            jpeg = _render_page_clip_jpeg(page, bbox, scale, quality, max_edge)
            fname = f"{base_name}_p{page_index + 1}_fig{fig_idx + 1}.jpg"
            ref_path = f"images/{fname}"
            results.append(
                (
                    ref_path,
                    base64.b64encode(jpeg).decode("utf-8"),
                    bbox[3],
                    cap_line,
                )
            )
        return results
    except Exception:
        logger.debug("vector figure clip failed on page %d", page_index, exc_info=True)
        return []
    finally:
        _close_pdfium_resource(textpage)


def _collect_invisible_boxes(page, raw) -> list:
    """Bounding boxes of invisible (render-mode 3) text objects on the page."""
    boxes: list = []
    try:
        for obj in page.get_objects():
            if obj.type != raw.FPDF_PAGEOBJ_TEXT:
                continue
            try:
                mode = raw.FPDFTextObj_GetTextRenderMode(obj.raw)
            except Exception:
                continue
            if mode != raw.FPDF_TEXTRENDERMODE_INVISIBLE:
                continue
            try:
                left, bottom, right, top = obj.get_bounds()
            except Exception:
                continue
            boxes.append(
                (min(left, right), min(bottom, top), max(left, right), max(bottom, top))
            )
    except Exception:
        return []
    return boxes


def _point_in_boxes(x: float, y: float, boxes: list) -> bool:
    for x0, y0, x1, y1 in boxes:
        if x0 <= x <= x1 and y0 <= y <= y1:
            return True
    return False


def _page_chars(textpage, page, raw) -> tuple:
    """Return ``(chars, page_width)`` with hidden/off-page glyphs filtered.

    Working at the glyph level (instead of pdfium rect segments) keeps mixed
    CJK + Latin/number lines in their true left-to-right order, which the
    rect-level ``get_text_bounded`` API scrambles.
    """
    n = textpage.count_chars()
    if n <= 0:
        return [], 0.0
    width, height = page.get_size()
    invisible = _collect_invisible_boxes(page, raw) if FILTER_HIDDEN_TEXT else []

    chars: list = []
    for i in range(n):
        try:
            left, bottom, right, top = textpage.get_charbox(i)
        except Exception:
            continue
        ch = textpage.get_text_range(i, 1)
        if ch in ("\r", "\n"):
            continue
        x0, x1 = (left, right) if left <= right else (right, left)
        y0, y1 = (bottom, top) if bottom <= top else (top, bottom)
        if FILTER_HIDDEN_TEXT:
            if x1 < 0 or x0 > width or y1 < 0 or y0 > height:
                continue  # off-page glyph
            if invisible and _point_in_boxes((x0 + x1) / 2, (y0 + y1) / 2, invisible):
                continue  # covered by an invisible text object
        chars.append({"x0": x0, "y0": y0, "x1": x1, "y1": y1, "ch": ch})
    return chars, width


def _find_split(items: list, axis: str, min_gap: float):
    """Return a coordinate at the widest clean gap on ``axis`` ('x'), or None.

    A "clean" gap means no item interval bridges it — i.e. a full-height column
    gutter. Used to detect multi-column layouts.
    """
    lo, hi = ("x0", "x1") if axis == "x" else ("y0", "y1")
    intervals = sorted(((s[lo], s[hi]) for s in items), key=lambda iv: iv[0])
    cur_end = intervals[0][1]
    best_gap, best_cut = 0.0, None
    for a, b in intervals[1:]:
        gap = a - cur_end
        if gap >= min_gap and gap > best_gap:
            best_gap, best_cut = gap, cur_end + gap / 2
        if b > cur_end:
            cur_end = b
    return best_cut


def _split_columns(chars: list, scale: float, width: float, depth: int = 0) -> list:
    """Split glyphs into reading-order columns at full-height gutters."""
    if len(chars) <= 1 or depth > 10:
        return [chars]
    min_gap = max(scale * 2.5, width * 0.04)
    cut = _find_split(chars, "x", min_gap)
    if cut is None:
        return [chars]
    left = [c for c in chars if (c["x0"] + c["x1"]) / 2 < cut]
    right = [c for c in chars if (c["x0"] + c["x1"]) / 2 >= cut]
    if not left or not right:
        return [chars]
    return _split_columns(left, scale, width, depth + 1) + _split_columns(
        right, scale, width, depth + 1
    )


def _column_x_span(chars: list) -> float:
    if not chars:
        return 0.0
    return max(c["x1"] for c in chars) - min(c["x0"] for c in chars)


def _column_single_line_fraction(lines: list) -> float:
    if not lines:
        return 0.0
    single = sum(1 for ln in lines if len(ln["text"]) <= 2)
    return single / len(lines)


def _is_artifact_column(chars: list, width: float) -> bool:
    """Detect margin strips and vertical watermarks (e.g. arXiv sidebar).

    Docling / MinerU solve this with learned layout regions; here we use
    geometry only: a narrow column whose lines are mostly one glyph tall is not
    part of the reading order.
    """
    if not chars or width <= 0:
        return True
    span = _column_x_span(chars)
    if span <= 0:
        return True
    lines = _group_lines(chars)
    single_frac = _column_single_line_fraction(lines)
    narrow = span / width < MARGIN_COL_WIDTH_RATIO
    if narrow and single_frac >= 0.45:
        return True
    ys = [(c["y0"] + c["y1"]) / 2 for c in chars]
    y_span = max(ys) - min(ys)
    # Vertical text: tall stack, narrow horizontal extent, mostly one char/line.
    if y_span > span * 3.5 and len(chars) >= 8 and single_frac >= 0.35:
        return True
    return False


def _filter_reading_columns(chars: list, scale: float, width: float) -> list:
    """Split into columns and drop margin / watermark strips."""
    cols = _split_columns(chars, scale, width)
    kept = [c for c in cols if not _is_artifact_column(c, width)]
    if kept:
        return kept
    # All columns looked like noise — keep the widest glyph set (main body).
    if len(cols) > 1:
        return [max(cols, key=_column_x_span)]
    return cols


def _merge_orphan_punctuation_lines(lines: list) -> list:
    """Attach lines that are only punctuation to the previous visual line.

    Many PDFs place ``.`` in figure labels or footnotes on a slightly different
    baseline; grouping by y then leaves ``Figure 1`` and ``2:`` on separate lines.
    """
    if not lines:
        return []
    merged: list = []
    for ln in lines:
        t = ln["text"].strip()
        if (
            merged
            and t
            and len(t) <= 4
            and all(c in ".,;:!?…·" or c.isspace() for c in t)
        ):
            suffix = "".join(t.split())
            prev = merged[-1]["text"]
            if suffix and prev and not prev.endswith((" ", "-")):
                merged[-1]["text"] = prev + suffix
            else:
                merged[-1]["text"] = (prev + " " + t).strip()
            continue
        merged.append(dict(ln))
    return merged


def _join_line_glyphs(ln_sorted: list) -> str:
    """Join a visual line's glyphs, inferring word spaces from horizontal gaps."""
    if not ln_sorted:
        return ""
    widths = [c["x1"] - c["x0"] for c in ln_sorted if c["x1"] > c["x0"]]
    med_w = statistics.median(widths) if widths else 1.0
    gap_threshold = med_w * WORD_GAP_WIDTH_RATIO

    parts: list[str] = []
    for i, cur in enumerate(ln_sorted):
        ch = cur["ch"]
        if i == 0:
            parts.append(ch)
            continue
        prev = ln_sorted[i - 1]
        if ch.isspace() or prev["ch"].isspace():
            if not ch.isspace() or (parts and not parts[-1].endswith(" ")):
                parts.append(ch)
            continue
        if cur["x0"] - prev["x1"] > gap_threshold:
            parts.append(" ")
        parts.append(ch)
    return "".join(parts).strip()


def _group_lines(chars: list) -> list:
    """Group a column's glyphs into lines (top-to-bottom, glyphs sorted by x)."""
    if not chars:
        return []
    heights = [c["y1"] - c["y0"] for c in chars if c["y1"] - c["y0"] > 0]
    med_h = statistics.median(heights) if heights else 1.0

    ordered = sorted(chars, key=lambda c: -(c["y0"] + c["y1"]) / 2)
    lines: list = []
    cur: list = []
    ref = None
    for c in ordered:
        yc = (c["y0"] + c["y1"]) / 2
        if ref is None or abs(yc - ref) <= 0.5 * med_h:
            cur.append(c)
            ref = yc if ref is None else ref
        else:
            lines.append(cur)
            cur = [c]
            ref = yc
    if cur:
        lines.append(cur)

    out: list = []
    for ln in lines:
        ln_sorted = sorted(ln, key=lambda c: c["x0"])
        text = _join_line_glyphs(ln_sorted)
        if not text:
            continue
        hs = [c["y1"] - c["y0"] for c in ln_sorted if c["y1"] - c["y0"] > 0]
        out.append({"h": statistics.median(hs) if hs else med_h, "text": text})
    return out


def _segments_to_markdown(lines: list) -> str:
    """Render merged lines to text, promoting visually large lines to headings."""
    if not lines:
        return ""
    body = statistics.median([ln["h"] for ln in lines])

    def level(ln) -> int:
        txt = ln["text"]
        if (
            not DETECT_HEADINGS
            or body <= 0
            or len(txt) > 80
            or len(txt) < MIN_HEADING_LINE_CHARS
        ):
            return 0
        if txt[-1:] in ".。!！?？,，;；:：":
            return 0
        r = ln["h"] / body
        if r >= 2.0:
            return 1
        if r >= 1.6:
            return 2
        if r >= 1.35:
            return 3
        return 0

    levels = [level(ln) for ln in lines]
    # If too many lines qualify, the font sizes are too uniform/noisy to trust.
    if sum(1 for x in levels if x) > max(1, int(0.4 * len(lines))):
        levels = [0] * len(lines)

    out = []
    for ln, lv in zip(lines, levels):
        out.append(("#" * lv + " " + ln["text"]) if lv else ln["text"])
    return "\n".join(out)


def _chars_to_layout_markdown(chars: list, scale: float, width: float) -> str:
    blocks: list = []
    for col in _filter_reading_columns(chars, scale, width):
        lines = _merge_orphan_punctuation_lines(_group_lines(col))
        md = _segments_to_markdown(lines)
        if md:
            blocks.append(md)
    return "\n".join(blocks)


def _layout_line_stats(text: str) -> tuple:
    """Return (line_count, single_char_line_count, punct_only_line_count)."""
    lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
    if not lines:
        return 0, 0, 0
    single = sum(1 for ln in lines if len(ln) <= 2)
    punct_only = sum(
        1
        for ln in lines
        if len(ln) <= 4 and re.fullmatch(r"[\s.,;:!?…·\-–—]+", ln)
    )
    return len(lines), single, punct_only


def _layout_garbled_line_fraction(text: str) -> float:
    """Share of lines that look like broken OCR (many 1–2 letter tokens)."""
    lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
    if not lines:
        return 0.0
    garbled = 0
    for ln in lines:
        words = ln.split()
        if len(words) >= 6 and sum(1 for w in words if len(w) <= 2) / len(words) > 0.45:
            garbled += 1
    return garbled / len(lines)


def _plain_is_well_formed(plain: str) -> bool:
    """True when pdfium plain text already has usable words and punctuation.

    Academic PDFs (arXiv) and TOCs already expose a good text layer; running
    geometric layout on them often destroys citations and words. Scanned books
    with a poor text layer (no commas in refs, short glued tokens) still need
    layout gap inference.
    """
    plain = (plain or "").strip()
    if not plain:
        return False
    if re.search(r"\[\w+,\s", plain):
        return True
    if plain.count(" . . ") >= 2:
        return True
    words = re.findall(r"\S+", plain)
    if len(words) < 30:
        return False
    avg_len = sum(len(w) for w in words) / len(words)
    return avg_len >= 5.0


def _should_prefer_plain(plain: str, layout: str) -> bool:
    """Fall back to pdfium plain text when layout reconstruction looks broken."""
    layout = (layout or "").strip()
    plain = (plain or "").strip()
    if not layout:
        return True
    if not plain:
        return False
    n, single, punct_only = _layout_line_stats(layout)
    if n == 0:
        return True
    if single / n >= 0.18 or punct_only / n >= 0.12:
        return True
    garbled = _layout_garbled_line_fraction(layout)
    if garbled >= 0.20 and _layout_garbled_line_fraction(plain) < 0.08:
        return True
    if re.search(r"\[\w+,\s", plain) and re.search(
        r"\[\w+\s+\w+\s+\d", layout
    ):
        return True
    # Title / lead sentence from plain should survive in layout.
    for ln in plain.splitlines():
        probe = ln.strip()
        if len(probe) < 24:
            continue
        alnum = "".join(c for c in probe if c.isalnum())[:16]
        if len(alnum) < 12:
            continue
        layout_alnum = "".join(c for c in layout if c.isalnum())
        if alnum not in layout_alnum:
            return True
        break
    return False


def _extract_layout_text(page, raw) -> str:
    """Layout-aware extraction: reading order + headings + hidden-text filter.

    Falls back to plain extraction on any failure so a single odd page never
    breaks the document.
    """
    textpage = None
    try:
        textpage = page.get_textpage()
        chars, width = _page_chars(textpage, page, raw)
        if not chars:
            return ""
        heights = [c["y1"] - c["y0"] for c in chars if c["y1"] - c["y0"] > 0]
        scale = (statistics.median(heights) if heights else 1.0) or 1.0
        return _chars_to_layout_markdown(chars, scale, width)
    except Exception:
        logger.debug("layout extraction failed; using plain text", exc_info=True)
        return _extract_page_text(page)
    finally:
        _close_pdfium_resource(textpage)


def _effective_scale(page, scale: float, max_edge: int) -> float:
    """Reduce ``scale`` so the rendered long edge never exceeds ``max_edge`` px.

    Some scanned PDFs declare enormous page boxes; rendering those at the raw
    DPI scale produces 100+ MP JPEGs that exceed the gRPC message limit and are
    far higher resolution than OCR needs.
    """
    if max_edge <= 0:
        return scale
    width, height = page.get_size()
    longest_pt = max(float(width), float(height))
    if longest_pt <= 0:
        return scale
    return min(scale, max_edge / longest_pt)


def _render_page_to_jpeg(page, scale: float, quality: int, max_edge: int = 0) -> bytes:
    bitmap = None
    try:
        bitmap = page.render(scale=_effective_scale(page, scale, max_edge))
        img_obj = bitmap.to_pil()
        if img_obj.mode != "RGB":
            img_obj = img_obj.convert("RGB")
        buf = io.BytesIO()
        img_obj.save(buf, format="JPEG", quality=quality, optimize=True)
        return buf.getvalue()
    finally:
        _close_pdfium_resource(bitmap)


# --- Parallel scanned-page rendering --------------------------------------
# pdfium is NOT thread-safe (concurrent get_page on one document crashes), so
# we parallelise across *processes*: each worker opens its own PdfDocument from
# a temp file and renders an assigned slice of pages. This turns the serial
# per-page render (the dominant cost for big scanned PDFs — hours on
# CPU-constrained containers) into a near-linear speedup.

# Per-worker document handle, populated by the pool initializer.
_WORKER_RENDER_DOC = None


def _render_pool_init(pdf_path: str) -> None:
    global _WORKER_RENDER_DOC
    import pypdfium2 as pdfium

    with open(pdf_path, "rb") as f:
        _WORKER_RENDER_DOC = pdfium.PdfDocument(f.read())


def _render_pool_task(args):
    index, scale, quality, max_edge = args
    page = _WORKER_RENDER_DOC[index]
    try:
        return index, _render_page_to_jpeg(page, scale, quality, max_edge)
    finally:
        _close_pdfium_resource(page)


def _select_mp_context():
    """Pick the safest available multiprocessing start method.

    ``forkserver`` forks workers from a clean, single-threaded server process,
    avoiding the fork-in-a-multithreaded-process hazards of the gRPC server.
    Falls back to ``fork`` and finally returns ``None`` (serial) when neither
    is available (e.g. Windows/dev).
    """
    import multiprocessing as mp

    for method in ("forkserver", "fork"):
        try:
            return mp.get_context(method)
        except ValueError:
            continue
    return None


def _render_pages_parallel(
    content: bytes, indices: list, scale: float, quality: int, max_edge: int, workers: int
) -> dict | None:
    """Render ``indices`` in parallel. Returns ``{index: jpeg_bytes}`` or None.

    Returns None to signal the caller to fall back to serial rendering (when
    parallelism is disabled, only one page is requested, or no usable
    multiprocessing start method exists).
    """
    if workers <= 1 or len(indices) <= 1:
        return None
    ctx = _select_mp_context()
    if ctx is None:
        return None

    import tempfile
    from concurrent.futures import ProcessPoolExecutor

    tmp_path = None
    try:
        with tempfile.NamedTemporaryFile(
            prefix="docreader_render_", suffix=".pdf", delete=False
        ) as tmp:
            tmp.write(content)
            tmp_path = tmp.name

        max_workers = min(workers, len(indices))
        tasks = [(i, scale, quality, max_edge) for i in indices]
        result: dict = {}
        with ProcessPoolExecutor(
            max_workers=max_workers,
            mp_context=ctx,
            initializer=_render_pool_init,
            initargs=(tmp_path,),
        ) as ex:
            for index, jpeg in ex.map(_render_pool_task, tasks, chunksize=4):
                result[index] = jpeg
        return result
    except Exception:
        logger.warning(
            "parallel page rendering failed; falling back to serial",
            exc_info=True,
        )
        return None
    finally:
        if tmp_path:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass


def _render_scanned_pages(
    pdf, content: bytes, indices: list, scale: float, quality: int, max_edge: int
) -> dict:
    """Render the given (scanned) page indices to JPEG bytes.

    Tries process-parallel rendering first (big win for large scanned PDFs),
    transparently falling back to serial rendering on the already-open ``pdf``
    handle when parallelism is unavailable or fails.
    """
    parallel = _render_pages_parallel(
        content, indices, scale, quality, max_edge, CONFIG.pdf_render_parallelism
    )
    if parallel is not None:
        return parallel

    out: dict = {}
    for i in indices:
        page = pdf[i]
        try:
            out[i] = _render_page_to_jpeg(page, scale, quality, max_edge)
        finally:
            _close_pdfium_resource(page)
    return out


def _select_embedded_images(
    meta: list,
    num_text_pages: int,
    *,
    min_pixels: int = EMBED_MIN_PIXELS,
    min_area_ratio: float = EMBED_MIN_AREA_RATIO,
    repeat_frac: float = EMBED_REPEAT_PAGE_FRAC,
    max_images: int = EMBED_MAX_IMAGES,
) -> list:
    """Decide which embedded-image candidates to keep (pure function).

    ``meta`` is a list of dicts with keys ``page``, ``width``, ``height``,
    ``area_ratio`` and ``hash``. Returns the indices (into ``meta``) to keep,
    after filtering by size, page-area share, cross-page repetition (logos /
    watermarks), exact in-page duplicates and a hard count cap.
    """
    from collections import defaultdict

    hash_pages = defaultdict(set)
    for m in meta:
        hash_pages[m["hash"]].add(m["page"])

    repeat_threshold = max(2, int(num_text_pages * repeat_frac)) if num_text_pages else 2
    banned = {h for h, pages in hash_pages.items() if len(pages) >= repeat_threshold}

    kept: list = []
    seen = set()
    for idx, m in enumerate(meta):
        if m["area_ratio"] < min_area_ratio:
            continue
        if m["width"] < min_pixels or m["height"] < min_pixels:
            continue
        if m["hash"] in banned:
            continue
        key = (m["page"], m["hash"])
        if key in seen:
            continue
        seen.add(key)
        kept.append(idx)
        if len(kept) >= max_images:
            break
    return kept


def _extract_embedded_images(pdf, classes, raw, base_name: str, quality: int) -> dict:
    """Extract filtered embedded figures from native text pages.

    Returns ``{page_index: [(ref_path, base64_jpeg, y_top), ...]}`` ordered so
    callers can place figures after the page text in top-to-bottom order.
    """
    import hashlib

    text_indices = [i for i, c in enumerate(classes) if c == "text"]
    if not text_indices:
        return {}

    candidates: list = []  # parallel to meta; holds heavy pixel data
    meta: list = []
    for i in text_indices:
        page = pdf[i]
        try:
            width, height = page.get_size()
            page_area = float(width) * float(height)
            if page_area <= 0:
                continue
            for obj in page.get_objects():
                if obj.type != raw.FPDF_PAGEOBJ_IMAGE:
                    continue
                try:
                    left, bottom, right, top = obj.get_bounds()
                except Exception:
                    continue
                area_ratio = abs((right - left) * (top - bottom)) / page_area
                if area_ratio < EMBED_MIN_AREA_RATIO:
                    continue  # cheap skip before decoding (logos/decorations)
                try:
                    pil = obj.get_bitmap().to_pil()
                except Exception:
                    continue
                content_hash = hashlib.md5(pil.tobytes()).hexdigest()
                candidates.append((i, top, pil))
                meta.append(
                    {
                        "page": i,
                        "width": pil.width,
                        "height": pil.height,
                        "area_ratio": area_ratio,
                        "hash": content_hash,
                    }
                )
        finally:
            _close_pdfium_resource(page)

    kept_idx = _select_embedded_images(meta, len(text_indices))
    if not kept_idx:
        return {}

    from collections import defaultdict

    result: dict = defaultdict(list)
    per_page_count: dict = defaultdict(int)
    max_edge = CONFIG.pdf_render_max_edge
    for idx in kept_idx:
        page_i, y_top, pil = candidates[idx]
        if pil.mode not in ("RGB", "L"):
            pil = pil.convert("RGB")
        if max_edge > 0 and max(pil.size) > max_edge:
            ratio = max_edge / max(pil.size)
            pil = pil.resize(
                (max(1, int(pil.width * ratio)), max(1, int(pil.height * ratio)))
            )
        buf = io.BytesIO()
        pil.save(buf, format="JPEG", quality=quality, optimize=True)
        per_page_count[page_i] += 1
        fname = f"{base_name}_p{page_i+1}_img{per_page_count[page_i]}.jpg"
        ref_path = f"images/{fname}"
        result[page_i].append(
            (ref_path, base64.b64encode(buf.getvalue()).decode("utf-8"), y_top)
        )

    # Top-to-bottom within each page (PDF y grows upward, so larger y first).
    for page_i in result:
        result[page_i].sort(key=lambda item: item[2], reverse=True)
    return result


def _strip_repeating_lines(texts: list, classes: list) -> list:
    """Remove running headers/footers that repeat across most text pages.

    Conservative: only the first/last non-empty line of each text page is a
    candidate, the line must be short, and it must appear on at least 60% of the
    text pages (and there must be enough pages to judge). Mirrors DeepDoc's
    cross-page "garbage set" idea without risking removal of real content.
    """
    from collections import Counter

    text_indices = [i for i, c in enumerate(classes) if c == "text"]
    if len(text_indices) < 4:
        return list(texts)

    counter: Counter = Counter()
    for i in text_indices:
        lines = [ln.strip() for ln in texts[i].splitlines() if ln.strip()]
        if not lines:
            continue
        for edge in {lines[0], lines[-1]}:
            if len(edge) <= 80:
                counter[edge] += 1

    threshold = max(2, int(len(text_indices) * 0.6))
    repeating = {line for line, count in counter.items() if count >= threshold}
    if not repeating:
        return list(texts)

    cleaned = []
    for i, text in enumerate(texts):
        if classes[i] != "text":
            cleaned.append(text)
            continue
        kept = [ln for ln in text.splitlines() if ln.strip() not in repeating]
        cleaned.append("\n".join(kept))
    return cleaned


class PDFScannedParser(BaseParser):
    """Render every PDF page to a JPEG image.

    Used as a robust last-resort fallback and for image-only PDFs. The Go App
    performs OCR on the extracted page images.
    """

    def parse_into_text(self, content: bytes) -> Document:
        import pypdfium2 as pdfium

        images = {}
        markdown_lines = []
        base_name = os.path.splitext(self.file_name or "document")[0]

        logger.info(
            "PDFScannedParser: Rendering PDF pages to JPEG images for %s",
            self.file_name,
        )

        try:
            with parser_worker_limit("pdf_render", CONFIG.pdf_render_max_workers):
                pdf = pdfium.PdfDocument(content)
                try:
                    page_count = len(pdf)
                    scale = max(1, CONFIG.pdf_render_dpi) / 72
                    quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)

                    rendered = _render_scanned_pages(
                        pdf,
                        content,
                        list(range(page_count)),
                        scale,
                        quality,
                        CONFIG.pdf_render_max_edge,
                    )
                finally:
                    _close_pdfium_resource(pdf)

            for i in range(page_count):
                page_filename = f"{base_name}_page_{i+1}.jpg"
                ref_path = f"images/{page_filename}"
                markdown_lines.append(f"![{page_filename}]({ref_path})")
                images[ref_path] = base64.b64encode(rendered[i]).decode("utf-8")

            text = "\n\n".join(markdown_lines)
            return Document(
                content=text,
                images=images,
                metadata={
                    "image_source_type": "scanned_pdf",
                    "page_count": page_count,
                },
            )
        except Exception as e:
            logger.exception("PDFScannedParser failed to parse PDF: %s", e)
            raise e


class PDFParser(BaseParser):
    """Per-page router between native text extraction and scanned rendering.

    For each page:
      * native text page  -> keep its text layer (fast, pypdfium2)
      * scanned page      -> render to JPEG, tag ``image_source_type=scanned_pdf``
                             so the Go App OCRs it

    Hybrid documents interleave both in reading order. On any unexpected error
    the parser falls back to rendering all pages as images (safe last resort).

    Force-scanned mode (``pdf_force_scanned=true`` override or
    ``DOCREADER_PDF_FORCE_SCANNED=true`` env) skips classification and
    renders every page as an image.
    """

    def __init__(self, file_name: str = "", file_type=None, **kwargs):
        # Capture per-upload override before BaseParser consumes kwargs.
        raw = kwargs.pop("pdf_force_scanned", None)
        super().__init__(file_name=file_name, file_type=file_type, **kwargs)
        # Priority: per-upload override > global env > default (False).
        if raw is not None:
            self._force_scanned = str(raw).strip().lower() in {
                "1", "true", "yes", "y", "on",
            }
        else:
            self._force_scanned = FORCE_SCANNED_PDF

    def parse_into_text(self, content: bytes) -> Document:
        # Force-scanned short-circuit: render every page as an image.
        if self._force_scanned:
            logger.info(
                "PDFParser: force scanned mode enabled for %s",
                self.file_name,
            )
            doc = PDFScannedParser(
                file_name=self.file_name, file_type=self.file_type
            ).parse_into_text(content)

            # Align metadata fields with automatic scanned route
            page_count = doc.metadata.get("page_count", 0)
            doc.metadata.update({
                "scanned_page_count": page_count,
                "text_page_count": 0,
                "embedded_image_count": 0,
                "vector_figure_count": 0,
            })
            return doc

        try:
            return self._route(content)
        except Exception:
            logger.exception(
                "PDFParser: per-page routing failed for %s; "
                "falling back to full image rendering",
                self.file_name,
            )
            return PDFScannedParser(
                file_name=self.file_name, file_type=self.file_type
            ).parse_into_text(content)

    def _route(self, content: bytes) -> Document:
        import pypdfium2 as pdfium
        import pypdfium2.raw as pdfium_r

        base_name = os.path.splitext(self.file_name or "document")[0]
        scale = max(1, CONFIG.pdf_render_dpi) / 72
        quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)

        pdf = pdfium.PdfDocument(content)
        images: dict = {}
        try:
            page_count = len(pdf)

            # Pass 1: cheap text extraction + image-area classification.
            texts: list = []
            classes: list = []
            vector_clips: dict = {}
            for i in range(page_count):
                page = pdf[i]
                try:
                    plain = _extract_page_text(page)
                    ratio = _page_image_area_ratio(page, pdfium_r)
                    cls = _classify_page(ratio, len(plain.strip()))
                    # Layout reconstruction only pays off (and is only spent) on
                    # native text pages; scanned pages are rendered, not read.
                    if cls == "text" and LAYOUT_ORDERING:
                        if _plain_is_well_formed(plain):
                            text = plain
                        else:
                            layout = _extract_layout_text(page, pdfium_r)
                            if layout and not _should_prefer_plain(plain, layout):
                                text = layout
                            else:
                                text = plain
                    else:
                        text = plain
                    if cls == "text":
                        clips = _extract_vector_figure_clips(
                            page,
                            i,
                            plain,
                            pdfium_r,
                            base_name,
                            scale,
                            quality,
                            CONFIG.pdf_render_max_edge,
                        )
                        if clips:
                            vector_clips[i] = clips
                            for ref_path, b64, _y, _cap in clips:
                                images[ref_path] = b64
                    text = _postprocess_pdf_text(text)
                    if cls == "text" and vector_clips.get(i):
                        text = _inject_figure_markdown_before_captions(
                            text, vector_clips[i]
                        )
                finally:
                    _close_pdfium_resource(page)
                texts.append(text)
                classes.append(cls)

            texts = _strip_repeating_lines(texts, classes)
            scanned_indices = [i for i, c in enumerate(classes) if c == "scanned"]

            # Pass 2: render only the scanned pages (heavy work, rate-limited).
            if scanned_indices:
                with parser_worker_limit("pdf_render", CONFIG.pdf_render_max_workers):
                    rendered = _render_scanned_pages(
                        pdf,
                        content,
                        scanned_indices,
                        scale,
                        quality,
                        CONFIG.pdf_render_max_edge,
                    )
                for i, img_bytes in rendered.items():
                    ref_path = f"images/{base_name}_page_{i+1}.jpg"
                    images[ref_path] = base64.b64encode(img_bytes).decode("utf-8")

            # Pass 3: extract embedded figures from native text pages so the Go
            # App can OCR/caption them (logos/watermarks/tiny images filtered).
            embedded: dict = {}
            if EXTRACT_EMBEDDED_IMAGES:
                embedded = _extract_embedded_images(
                    pdf, classes, pdfium_r, base_name, quality
                )
                for refs in embedded.values():
                    for ref_path, b64, _y in refs:
                        images[ref_path] = b64
        finally:
            _close_pdfium_resource(pdf)

        # Assemble markdown in reading order.
        embedded_count = 0
        vector_figure_count = 0
        blocks = []
        for i in range(page_count):
            if classes[i] == "scanned":
                page_filename = f"{base_name}_page_{i+1}.jpg"
                blocks.append(f"![{page_filename}](images/{page_filename})")
            else:
                stripped = texts[i].strip()
                if stripped:
                    blocks.append(stripped)
                vector_figure_count += len(vector_clips.get(i, []))
                page_images = list(embedded.get(i, []))
                page_images.sort(key=lambda item: item[2], reverse=True)
                for ref_path, _b64, _y in page_images:
                    fname = os.path.basename(ref_path)
                    blocks.append(f"![{fname}]({ref_path})")
                    embedded_count += 1

        content_text = "\n\n".join(blocks).strip()

        metadata = {
            "page_count": page_count,
            "scanned_page_count": len(scanned_indices),
            "text_page_count": page_count - len(scanned_indices),
            "embedded_image_count": embedded_count,
            "vector_figure_count": vector_figure_count,
            "image_source_type": "scanned_pdf" if scanned_indices else "pdf_text_layer",
        }

        logger.info(
            "PDFParser: %s -> %d pages (%d scanned, %d text), "
            "embedded_images=%d, content_len=%d",
            self.file_name,
            page_count,
            len(scanned_indices),
            page_count - len(scanned_indices),
            embedded_count,
            len(content_text),
        )
        return Document(content=content_text, images=images, metadata=metadata)
