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
# Promote visually larger lines to markdown headings (font-size proxy = rect
# height relative to the page's median line height).
DETECT_HEADINGS = _env_bool("DOCREADER_PDF_DETECT_HEADINGS", True)
# Drop invisible (render-mode 3), off-page and degenerate text — a cheap guard
# against hidden-text prompt injection and OCR artefacts.
FILTER_HIDDEN_TEXT = _env_bool("DOCREADER_PDF_FILTER_HIDDEN_TEXT", True)


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
        text = "".join(c["ch"] for c in ln_sorted).strip()
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
        if not DETECT_HEADINGS or body <= 0 or len(txt) > 80:
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
        blocks = []
        for col in _split_columns(chars, scale, width):
            md = _segments_to_markdown(_group_lines(col))
            if md:
                blocks.append(md)
        return "\n".join(blocks)
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


def estimate_scanned_fraction(content: bytes, sample: int = 12) -> float:
    """Return the fraction of (sampled) pages that look image-dominated.

    Used by alternative engines (e.g. liteparse) that lack image-object access
    to decide whether a PDF is scanned, applying the same image-area signal the
    builtin router uses. Samples up to ``sample`` pages for speed on big PDFs.
    """
    import pypdfium2 as pdfium
    import pypdfium2.raw as pdfium_r

    pdf = pdfium.PdfDocument(content)
    try:
        page_count = len(pdf)
        if page_count <= 0:
            return 0.0
        step = max(1, page_count // sample)
        indices = list(range(0, page_count, step))
        scanned = 0
        for i in indices:
            page = pdf[i]
            try:
                ratio = _page_image_area_ratio(page, pdfium_r)
            finally:
                _close_pdfium_resource(page)
            if ratio >= SCAN_IMAGE_AREA_RATIO:
                scanned += 1
        return scanned / len(indices) if indices else 0.0
    finally:
        _close_pdfium_resource(pdf)


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
    """

    def parse_into_text(self, content: bytes) -> Document:
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
            for i in range(page_count):
                page = pdf[i]
                try:
                    plain = _extract_page_text(page)
                    ratio = _page_image_area_ratio(page, pdfium_r)
                    cls = _classify_page(ratio, len(plain.strip()))
                    # Layout reconstruction only pays off (and is only spent) on
                    # native text pages; scanned pages are rendered, not read.
                    if cls == "text" and LAYOUT_ORDERING:
                        text = _extract_layout_text(page, pdfium_r) or plain
                    else:
                        text = plain
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
        blocks = []
        for i in range(page_count):
            if classes[i] == "scanned":
                page_filename = f"{base_name}_page_{i+1}.jpg"
                blocks.append(f"![{page_filename}](images/{page_filename})")
            else:
                stripped = texts[i].strip()
                if stripped:
                    blocks.append(stripped)
                for ref_path, _b64, _y in embedded.get(i, []):
                    fname = os.path.basename(ref_path)
                    blocks.append(f"![{fname}]({ref_path})")
                    embedded_count += 1

        content_text = "\n\n".join(blocks).strip()

        metadata = {
            "page_count": page_count,
            "scanned_page_count": len(scanned_indices),
            "text_page_count": page_count - len(scanned_indices),
            "embedded_image_count": embedded_count,
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
