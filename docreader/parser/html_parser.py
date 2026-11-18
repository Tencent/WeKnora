"""Static HTML file parser."""

import logging
from collections import Counter

from bs4 import BeautifulSoup
from trafilatura import extract

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.mhtml_parser import MHTMLParser

logger = logging.getLogger(__name__)

# An uploaded HTML file is converted in full by default, so navigation,
# sidebars and footers are indexed as if they were document body. Setting
# DOCREADER_HTML_MAIN_CONTENT=true indexes only the main content instead, the
# way the URL import path (web_parser.py) already does with trafilatura. It is
# off by default on purpose, so upgrading never changes what a knowledge base
# indexes for documents it already ingests.
#
# Main-content extraction can fail in two ways: the page has no clear article
# body (a form page, a bare link list) or trafilatura misjudges it. Both
# thresholds below keep the full conversion in that case, which is also the
# upstream behavior.
_MIN_EXTRACTED_CHARS = 250
# trafilatura falls back to a plain-text rendering below its own
# MIN_EXTRACTED_SIZE (250); that rendering drops heading levels, tables and
# link targets, so a page that lands between the two thresholds would lose
# structure instead of just losing its chrome. Bracketing at trafilatura's
# own floor keeps the full conversion in charge there.
_MIN_KEEP_RATIO = 0.15


def extract_main_content(html: str) -> str:
    """Extract the main content of a page with trafilatura.

    Returns the extracted markdown, or an empty string when trafilatura finds
    nothing usable or raises; callers then keep the full conversion.
    """
    try:
        extracted = extract(
            html,
            output_format="markdown",
            include_tables=True,
            include_links=True,
            include_formatting=True,
            # Keep references in Markdown; Go can only resolve/download body
            # images if their URLs survive this extraction step.
            include_images=True,
        )
    except Exception as exc:  # never fail a parse the full conversion can serve
        logger.warning("HTML main-content extraction failed: %s", exc)
        return ""
    return (extracted or "").strip()


class HTMLToMarkdownParser(BaseParser):
    """Convert uploaded HTML bytes to Markdown without browser or network access."""

    def _full_markdown(self, html: str) -> str:
        """Convert the whole page, the way this parser always did."""
        return MHTMLParser(
            file_name=self.file_name,
            file_type=self.file_type,
            extract_images=False,
        ).html_to_markdown(
            html,
            strip_internal_links=False,
            fallback_to_raw_html=False,
        ) or ""

    def parse_into_text(self, content: bytes) -> Document:
        if not content.strip():
            return Document()

        # Inspect the original bytes so BOMs and HTML charset declarations are
        # honored before the shared Markdown conversion runs.
        html = BeautifulSoup(content, "lxml").decode()

        full = self._full_markdown(html)
        if not CONFIG.html_main_content:
            return Document(content=full)

        extracted = extract_main_content(html)
        # Some short articles are repeated by trafilatura's fallback. Do not
        # index extra copies or let them inflate the length/retention guards.
        # Only reject repetitions absent from the full conversion: deliberate
        # repeated paragraphs in the source must remain valid.
        blocks = Counter(line.strip() for line in extracted.splitlines())
        for block, occurrences in blocks.items():
            if occurrences > 1 and len(block) >= 80 and 0 < full.count(block) < occurrences:
                logger.info(
                    "HTML main-content extraction duplicated a body block; keeping the full conversion"
                )
                return Document(content=full)
        if len(extracted) < _MIN_EXTRACTED_CHARS:
            logger.info(
                "HTML main-content extraction returned %d chars (<%d); "
                "keeping the full conversion",
                len(extracted),
                _MIN_EXTRACTED_CHARS,
            )
            return Document(content=full)
        if full and len(extracted) < len(full) * _MIN_KEEP_RATIO:
            logger.info(
                "HTML main-content extraction kept only %d chars of %d; "
                "keeping the full conversion",
                len(extracted),
                len(full),
            )
            return Document(content=full)

        logger.info(
            "HTML main-content extraction applied: kept %d chars, "
            "full conversion would be %d",
            len(extracted),
            len(full),
        )
        return Document(content=extracted)


class HTMLParser(PipelineParser):
    """Extract static HTML content and normalize the resulting Markdown."""

    _parser_cls = (HTMLToMarkdownParser, MarkdownParser)
