"""Optional PDF engine backed by LiteParse (LlamaIndex, MIT).

LiteParse is a fast Rust/PDFium text extractor that performs spatial reading-order
reconstruction natively (multi-column aware) and is considerably faster than the
Python text path. It is exposed as a *selectable* engine (``liteparse``) rather
than replacing the builtin engine, so users can opt in per knowledge base.

Scope/limitations (documented intentionally):
  * Text-first engine: it returns reading-order plain text, not figures. Scanned
    pages carry no text layer, so for image-dominated PDFs we fall back to the
    builtin scanned renderer (page -> JPEG, OCR'd by the Go App) to stay robust.
  * docreader never runs OCR itself; OCR/VLM remain Go-side responsibilities.
"""

import logging

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)

# If the extracted text averages fewer characters per page than this, the PDF is
# treated as scanned/image-dominated and routed to the builtin image renderer.
_MIN_CHARS_PER_PAGE = 20
# If at least this fraction of sampled pages are image-dominated, the PDF is
# scanned (even when it carries a garbled OCR text layer) and is routed to the
# builtin image renderer rather than trusting the low-quality text.
_SCANNED_PAGE_FRACTION = 0.5


def liteparse_available(_overrides=None):
    """Engine availability probe used by the registry/UI."""
    try:
        import liteparse  # noqa: F401
    except Exception as e:  # pragma: no cover - depends on install
        return False, f"liteparse 未安装: {e}"
    return True, ""


class LiteParseParser(BaseParser):
    """Parse a PDF with LiteParse, falling back to scanned rendering when empty."""

    def parse_into_text(self, content: bytes) -> Document:
        import liteparse

        from docreader.parser.pdf_parser import (
            PDFScannedParser,
            estimate_scanned_fraction,
        )

        # Image-dominated PDFs (incl. ones with a garbled OCR text layer) carry
        # no trustworthy text; render them as images for Go-side OCR instead.
        try:
            scanned_frac = estimate_scanned_fraction(content)
        except Exception:
            scanned_frac = 0.0
        if scanned_frac >= _SCANNED_PAGE_FRACTION:
            logger.info(
                "LiteParseParser: %s is image-dominated (%.0f%% scanned pages); "
                "using builtin scanned renderer",
                self.file_name,
                scanned_frac * 100,
            )
            return PDFScannedParser(
                file_name=self.file_name, file_type=self.file_type
            ).parse_into_text(content)

        engine = liteparse.LiteParse(ocr_enabled=False, quiet=True)
        result = engine.parse(content)
        page_count = int(result.num_pages)

        page_texts = []
        for i in range(page_count):
            page = result.get_page(i)
            page_texts.append((getattr(page, "text", "") or "").strip())

        doc_text = (getattr(result, "text", "") or "").strip()
        if not doc_text:
            doc_text = "\n\n".join(t for t in page_texts if t)

        # Image-dominated / scanned PDFs yield little to no text: defer to the
        # builtin scanned renderer so the Go App can OCR the page images.
        if page_count and len(doc_text) < _MIN_CHARS_PER_PAGE * page_count:
            logger.info(
                "LiteParseParser: %s looks scanned (%d chars / %d pages); "
                "falling back to builtin scanned renderer",
                self.file_name,
                len(doc_text),
                page_count,
            )
            return PDFScannedParser(
                file_name=self.file_name, file_type=self.file_type
            ).parse_into_text(content)

        logger.info(
            "LiteParseParser: %s -> %d pages, content_len=%d",
            self.file_name,
            page_count,
            len(doc_text),
        )
        return Document(
            content=doc_text,
            images={},
            metadata={
                "page_count": page_count,
                "image_source_type": "pdf_text_layer",
                "parser_engine": "liteparse",
            },
        )
