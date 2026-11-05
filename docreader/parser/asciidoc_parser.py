# -*- coding: utf-8 -*-
"""AsciiDoc parser.

Primary path: the ``asciidoctor`` binary converts AsciiDoc to an HTML5
fragment, then the shared HTML → Markdown conversion (the exact code path
an uploaded HTML file takes) produces the final markdown. Remote images in
the source (``image::https://...[]``) survive as ``<img>`` tags in the HTML,
which the Go side resolves and stores after its SSRF checks — same as HTML
uploads. A standalone ``.adoc`` upload cannot resolve sibling image files
(their bytes never reach this service), so those references are dropped.

Fallback path: when the binary is missing or fails, a conservative
line-oriented converter keeps headings, lists, tables, code blocks, images
and common inline formatting, so a missing Ruby gem degrades quality
instead of failing the ingest.
"""

import logging
import os
import re
import shutil
from typing import List, Optional

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.doc_parser import SandboxExecutor
from docreader.parser.html_parser import HTMLToMarkdownParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.utils import endecode
from docreader.utils.tempfile import TempFileContext

logger = logging.getLogger(__name__)

# Binary lookup and execution limits, mirroring LIBREOFFICE_PATH handling
# in doc_parser.py.
ASCIIDOCTOR_PATH_ENV = "ASCIIDOCTOR_PATH"
ASCIIDOCTOR_TIMEOUT_ENV = "DOCREADER_ASCIIDOCTOR_TIMEOUT"
DEFAULT_ASCIIDOCTOR_TIMEOUT = 120  # seconds


def _asciidoc_timeout() -> int:
    raw = os.environ.get(ASCIIDOCTOR_TIMEOUT_ENV, "").strip()
    if raw:
        try:
            value = int(raw)
            if value > 0:
                return value
        except ValueError:
            logger.warning("Invalid %s=%r; using default", ASCIIDOCTOR_TIMEOUT_ENV, raw)
    return DEFAULT_ASCIIDOCTOR_TIMEOUT


class AsciiDocToHtmlParser(BaseParser):
    """Convert AsciiDoc bytes to Markdown via asciidoctor + the HTML path."""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.sandbox_executor = SandboxExecutor(default_timeout=_asciidoc_timeout())

    def parse_into_text(self, content: bytes) -> Document:
        text = endecode.decode_bytes(content)
        if not text.strip():
            return Document()

        html = self._asciidoctor_to_html(text)
        if html:
            document = HTMLToMarkdownParser(
                file_name=self.file_name, file_type="html"
            ).parse_into_text(html.encode("utf-8"))
            if document.is_valid():
                return document
            logger.warning("asciidoctor HTML yielded no text; using fallback converter")
        else:
            logger.warning("asciidoctor unavailable; using fallback converter")

        return Document(content=_fallback_adoc_to_markdown(text))

    # ------------------------------------------------------------------
    # asciidoctor subprocess
    # ------------------------------------------------------------------

    def _find_asciidoctor(self) -> Optional[str]:
        custom = os.environ.get(ASCIIDOCTOR_PATH_ENV, "").strip()
        if custom and os.path.isfile(custom) and os.access(custom, os.X_OK):
            return custom
        return shutil.which("asciidoctor")

    def _asciidoctor_to_html(self, text: str) -> str:
        binary = self._find_asciidoctor()
        if not binary:
            return ""

        # Re-encode through the detected charset so GBK/Latin-1 sources work;
        # asciidoctor assumes UTF-8 input.
        with TempFileContext(text.encode("utf-8"), ".adoc") as adoc_path:
            cmd = [
                binary,
                "--safe-mode", "secure",   # no include directives, no docdir reads
                "-b", "html5",
                "-a", "data-uri",          # embed local images when resolvable
                "-a", "source-highlighter=",  # no highlight JS assets
                "-a", "toc!",              # no duplicate nav
                "--no-header-footer",      # body fragment only
                "-o", "-",                 # write to stdout
                adoc_path,
            ]
            stdout, stderr, returncode = self.sandbox_executor.execute_in_sandbox(cmd)

        if returncode != 0:
            detail = (stderr or b"").decode("utf-8", errors="ignore")[:500]
            logger.warning("asciidoctor failed rc=%s: %s", returncode, detail)
            return ""
        return stdout.decode("utf-8", errors="ignore").strip()


class AsciiDocParser(PipelineParser):
    """Convert AsciiDoc source to Markdown and normalize the result.

    Stage 1 converts to HTML (or fallback markdown); stage 2 runs the shared
    Markdown postprocessor so tables are normalized GFM and any
    ``data:image/...;base64`` URIs are extracted into ``images/<uuid>`` refs.
    """

    _parser_cls = (AsciiDocToHtmlParser, MarkdownParser)


# ----------------------------------------------------------------------
# Pure-Python fallback converter (used only when asciidoctor is missing)
# ----------------------------------------------------------------------

_HEADING_RE = re.compile(r"^(={1,6})\s+(.+?)\s*=*\s*$")
_SETEX_RE = re.compile(r"^([=-])\1{2,}\s*$")
_DOC_ATTR_RE = re.compile(r"^:[a-zA-Z0-9_-]+:.*$")
_COMMENT_LINE_RE = re.compile(r"^//(?!/).*")
_DELIM_RE = re.compile(r"^(-{4,}|={4,}|\.{4,}|\+{4,}|/{4,}|\*{4,}|_{4,})\s*$")
_DELIM_KIND = {
    "-": "code",        # source / listing block
    ".": "code",        # literal block
    "/": "comment",     # comment block
    "_": "quote",       # quote block
    "=": None,          # example block → keep content as prose
    "+": None,          # passthrough → keep content as-is
    "*": None,          # sidebar → keep content as prose
}
_BULLET_RE = re.compile(r"^(\s*)(\*+|-)\s+(.+)$")
_ORDERED_RE = re.compile(r"^(\s*)(\.*|\d+)\.\s+(.+)$")
_BLOCK_TITLE_RE = re.compile(r"^\.(\S.*)$")
_ATTR_LIST_RE = re.compile(r"^\[[^\]]*\]\s*$")
_ADMONITION_RE = re.compile(r"^\[(NOTE|TIP|WARNING|CAUTION|IMPORTANT)\]\s*$", re.I)
_IMAGE_BLOCK_RE = re.compile(r"(?<![\w:])image::([^\s\[]+)\[([^\],]*)[^\]]*\]")
_IMAGE_INLINE_RE = re.compile(r"(?<![\w:])image:([^\s\[]+)\[([^\],]*)[^\]]*\]")
_LINK_RE = re.compile(r"(?<![\w:])link:([^\s\[]+)\[([^\]]*)\]")
_URL_LINK_RE = re.compile(r"(https?://[^\s\[\]]+)\[([^\]]+)\]")
_XREF_RE = re.compile(r"<<([^,\]>]+)(?:,([^>]*))?>>")
_BOLD_RE = re.compile(r"(?<![\w*\\])\*([^*\n]+)\*(?![\w*])")
_ITALIC_RE = re.compile(r"(?<![\w_\\])_([^_\n]+)_(?![\w_])")
_FOOTNOTE_RE = re.compile(r"footnote:\[([^\]]*)\]")
_TABLE_ROW_START_RE = re.compile(r"^\|")
_CELL_PREFIX_RE = re.compile(r"^(?:\d+(?:\.\d+)?\+|[a-eg-psv]|\d|[<>^])*?\|")


def _inline(text: str) -> str:
    """Best-effort inline AsciiDoc → Markdown conversion."""
    text = _IMAGE_BLOCK_RE.sub(r"![\2](\1)", text)
    text = _IMAGE_INLINE_RE.sub(r"![\2](\1)", text)
    text = _LINK_RE.sub(lambda m: "[{}]({})".format((m.group(2) or m.group(1)).strip(), m.group(1)), text)
    text = _URL_LINK_RE.sub(r"[\2](\1)", text)
    text = _XREF_RE.sub(lambda m: (m.group(2) or m.group(1)).strip(), text)
    text = _FOOTNOTE_RE.sub(r" (\1)", text)
    text = _BOLD_RE.sub(r"**\1**", text)
    text = _ITALIC_RE.sub(r"*\1*", text)
    return text


def _split_table_row(line: str) -> List[str]:
    """Split an AsciiDoc table row into cell texts."""
    body = line.lstrip()[1:] if line.lstrip().startswith("|") else line.lstrip()
    cells = []
    for cell in re.split(r"(?<!\\)\|", body):
        cell = cell.replace("\\|", "|").strip()
        cell = _CELL_PREFIX_RE.sub("", cell)
        cell = _inline(cell).replace("|", "\\|")
        cells.append(cell)
    return cells


def _markdown_table(rows: List[List[str]]) -> str:
    """Render collected AsciiDoc rows as a GFM table (first row = header)."""
    if not rows:
        return ""
    width = max(len(r) for r in rows)
    rows = [r + [""] * (width - len(r)) for r in rows]

    def fmt(row: List[str]) -> str:
        return "| " + " | ".join(row) + " |"

    out = [fmt(rows[0]), "|" + " --- |" * width]
    out.extend(fmt(r) for r in rows[1:])
    return "\n".join(out)


def _fallback_adoc_to_markdown(text: str) -> str:
    """Convert AsciiDoc to Markdown without external dependencies.

    Handles the constructs that dominate real-world AsciiDoc: document title
    and section headings, bullet/ordered lists, delimited blocks (source,
    literal, comment, quote), pipe tables, images, links and basic inline
    styling. Anything exotic passes through as plain text rather than
    failing.
    """
    lines = text.splitlines()
    out: List[str] = []

    in_header = bool(lines) and _HEADING_RE.match(lines[0]) is not None
    header_done = not in_header
    block = None  # None | "code" | "comment" | "quote"
    code_buffer: List[str] = []
    table_rows: List[List[str]] = []

    def flush_table() -> None:
        nonlocal table_rows
        if table_rows:
            rendered = _markdown_table(table_rows)
            if rendered:
                out.append(rendered)
            table_rows = []

    for raw in lines:
        line = raw.rstrip("\n")

        # --- delimited block handling -----------------------------------
        delim = _DELIM_RE.match(line)
        if delim:
            marker = delim.group(1)[0]
            if block is None and marker in "=-":
                # `Title` + `====` / `----` may be a setext heading: an
                # underline at least as long as the preceding prose line.
                prev = out[-1] if out else ""
                if (
                    prev
                    and prev.strip()
                    and not prev.startswith(("#", ">", "|", "-", "*", "```", "  "))
                    and len(delim.group(1)) >= len(prev)
                ):
                    out[-1] = ("#" if marker == "=" else "##") + " " + prev
                    continue
            kind = _DELIM_KIND.get(marker, "code")
            if block is None:
                if kind is None:
                    continue  # example/passthrough/sidebar: prose, drop fence
                flush_table()
                block = kind
                code_buffer = []
            elif kind == block:
                flush_table()
                if block == "code":
                    out.append("```")
                    out.extend(code_buffer)
                    out.append("```")
                block = None
                code_buffer = []
            else:
                # delimiter-looking line inside another open block: content
                if block == "code":
                    code_buffer.append(line)
                elif block == "quote":
                    out.append(("> " + line).rstrip())
            continue

        if block == "comment":
            continue
        if block == "code":
            code_buffer.append(line)
            continue
        if block == "quote":
            out.append(("> " + line).rstrip())
            continue

        # --- table handling ----------------------------------------------
        stripped = line.strip()
        if stripped.startswith("|==="):
            # closes an unterminated table, or opens one (rows collect below)
            flush_table()
            continue
        if table_rows:
            if not stripped:
                flush_table()
                out.append("")
                continue
            if _TABLE_ROW_START_RE.match(stripped):
                table_rows.append(_split_table_row(stripped))
                continue
            continue  # attribute or continuation line inside the table
        if _TABLE_ROW_START_RE.match(stripped):
            table_rows.append(_split_table_row(stripped))
            continue

        # --- document header ----------------------------------------------
        if not header_done:
            heading = _HEADING_RE.match(stripped)
            if heading:
                level = len(heading.group(1))
                out.append("#" * level + " " + _inline(heading.group(2).strip()))
                continue
            if not stripped:
                header_done = True
                out.append("")
                continue
            if _DOC_ATTR_RE.match(stripped):
                continue  # :attributes: and author lines are metadata
            # Author line: keep as emphasized text under the title
            out.append("*" + _inline(stripped) + "*")
            continue

        # --- blank / structural lines --------------------------------------
        if not stripped:
            out.append("")
            continue
        if stripped == "--":
            continue  # open-block delimiter: content flows as prose

        heading = _HEADING_RE.match(stripped)
        if heading:
            level = len(heading.group(1))
            out.append("#" * level + " " + _inline(heading.group(2).strip()))
            continue

        setext = _SETEX_RE.match(stripped)
        if setext and out and out[-1] and not out[-1].startswith(("#", ">", "|", "-", "*", "```")):
            # Title\n=====  →  # Title (setext heading)
            level = 1 if setext.group(1) == "=" else 2
            out[-1] = "#" * level + " " + out[-1]
            continue

        if _COMMENT_LINE_RE.match(stripped):
            continue

        admonition = _ADMONITION_RE.match(stripped)
        if admonition:
            out.append("> **" + admonition.group(1).upper() + ":**")
            continue

        if _ATTR_LIST_RE.match(stripped):
            continue  # [cols=...], [NOTE], [.title] style lines

        block_title = _BLOCK_TITLE_RE.match(stripped)
        if block_title:
            out.append("**" + _inline(block_title.group(1).strip()) + "**")
            continue

        bullet = _BULLET_RE.match(line)
        if bullet:
            depth = len(bullet.group(2)) if bullet.group(2).startswith("*") else 1
            out.append("  " * (depth - 1) + "- " + _inline(bullet.group(3).strip()))
            continue

        ordered = _ORDERED_RE.match(line)
        if ordered:
            marker = ordered.group(2)
            depth = len(marker) + 1 if marker.startswith(".") else 1
            out.append("  " * (depth - 1) + "1. " + _inline(ordered.group(3).strip()))
            continue

        out.append(_inline(stripped))

    flush_table()
    if block == "code":
        out.extend(["```", *code_buffer, "```"])

    # collapse runs of blank lines
    cleaned: List[str] = []
    for line in out:
        if not line.strip() and cleaned and not cleaned[-1].strip():
            continue
        cleaned.append(line)
    return "\n".join(cleaned).strip() + "\n"


__all__ = [
    "AsciiDocParser",
    "AsciiDocToHtmlParser",
    "_fallback_adoc_to_markdown",
    "_inline",
]
