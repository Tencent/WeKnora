"""
Office format hybrid parsers: MarkItDown text extraction + LibreOffice page rendering.

Both DocxHybridParser and PptxHybridParser follow the same pattern:
  1. MarkitdownParser  — extracts text structure concurrently
  2. LibreOffice       — converts file to PDF, then PDFScannedParser renders
                         every page as a JPEG for the multimodal VLM pipeline

The two passes run in a ThreadPoolExecutor so total wall-clock time is
max(text_extraction, lo_conversion + page_render) rather than their sum.
"""

import concurrent.futures
import logging
import os
import shutil
import subprocess
import tempfile

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.pdf_parser import PDFScannedParser

logger = logging.getLogger(__name__)

# LibreOffice search order: PATH first, then common installation paths.
_SOFFICE_FALLBACKS = [
    "/opt/homebrew/bin/soffice",
    "/usr/bin/soffice",
    "/usr/local/bin/soffice",
    "/Applications/LibreOffice.app/Contents/MacOS/soffice",
]

SOFFICE_TIMEOUT = 120  # seconds per conversion


def find_soffice() -> str | None:
    """Return the path to the LibreOffice soffice binary, or None."""
    path = shutil.which("soffice")
    if path:
        return path
    for candidate in _SOFFICE_FALLBACKS:
        if os.path.isfile(candidate):
            return candidate
    return None


def _libreoffice_to_pdf(content: bytes, file_name: str, file_type: str) -> bytes:
    """Convert an Office document to PDF bytes using LibreOffice headless.

    Raises RuntimeError when LibreOffice is not found or the conversion fails.
    """
    soffice = find_soffice()
    if not soffice:
        raise RuntimeError("LibreOffice (soffice) not found — install LibreOffice to enable Office page rendering")

    ext = (file_type or "").lstrip(".") or os.path.splitext(file_name)[1].lstrip(".") or "bin"
    tmpdir = tempfile.mkdtemp(prefix="lo-convert-")
    try:
        input_path = os.path.join(tmpdir, f"input.{ext}")
        with open(input_path, "wb") as fh:
            fh.write(content)

        proc = subprocess.run(
            [soffice, "--headless", "--convert-to", "pdf", "--outdir", tmpdir, input_path],
            capture_output=True,
            timeout=SOFFICE_TIMEOUT,
        )
        if proc.returncode != 0:
            stderr = proc.stderr.decode(errors="replace")[:500]
            raise RuntimeError(f"soffice exited {proc.returncode}: {stderr}")

        pdf_files = [f for f in os.listdir(tmpdir) if f.endswith(".pdf")]
        if not pdf_files:
            raise RuntimeError("LibreOffice produced no PDF output")

        return open(os.path.join(tmpdir, pdf_files[0]), "rb").read()
    finally:
        shutil.rmtree(tmpdir, ignore_errors=True)


class _OfficeHybridParser(BaseParser):
    """Base: MarkItDown text + LibreOffice→PDF page renders, run concurrently."""

    def parse_into_text(self, content: bytes) -> Document:
        def _extract_text() -> str:
            try:
                parser = MarkitdownParser(file_name=self.file_name, file_type=self.file_type)
                doc = parser.parse_into_text(content)
                return doc.content if doc.is_valid() else ""
            except Exception:
                logger.exception("%s: MarkitdownParser failed", self.__class__.__name__)
                return ""

        def _render_pages() -> Document:
            try:
                pdf_bytes = _libreoffice_to_pdf(content, self.file_name or "doc", self.file_type or "")
                base = os.path.splitext(self.file_name or "doc")[0]
                scanner = PDFScannedParser(file_name=base + ".pdf", file_type="pdf")
                return scanner.parse_into_text(pdf_bytes)
            except Exception:
                logger.exception("%s: LibreOffice/PDFScannedParser failed", self.__class__.__name__)
                return Document()

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            text_future = executor.submit(_extract_text)
            render_future = executor.submit(_render_pages)
            text_content = text_future.result()
            render_doc = render_future.result()

        if render_doc.images:
            parts = [p for p in (text_content, render_doc.content) if p]
            return Document(
                content="\n\n".join(parts),
                images=render_doc.images,
                metadata=render_doc.metadata,
            )

        return Document(content=text_content)


class DocxHybridParser(_OfficeHybridParser):
    """Word (docx/doc) hybrid: MarkItDown text + LibreOffice page renders.

    DocxParser extracts inline images but silently drops Charts, SmartArt and
    OLE objects.  This parser renders every page via LibreOffice so the VLM
    pipeline captures all visual content including complex diagrams.
    Use for large Word files (≥5 MB) where MinerU would block the queue.
    """


class PptxHybridParser(_OfficeHybridParser):
    """PowerPoint (pptx/ppt) hybrid: MarkItDown text + LibreOffice slide renders.

    Slides are fundamentally visual — MarkItDown alone extracts bullet text only.
    LibreOffice converts the file to PDF (one page per slide), then
    PDFScannedParser renders every slide as a JPEG for VLM captioning/OCR.
    Recommended for all pptx/ppt files regardless of size.
    """
