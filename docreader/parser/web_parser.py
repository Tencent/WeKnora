import asyncio
import logging
import re
from dataclasses import dataclass
from typing import Optional

from lxml.etree import XPath
from playwright.async_api import Page, async_playwright
from trafilatura import extract, utils, xpaths

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.utils import endecode

logger = logging.getLogger(__name__)

_GOTO_TIMEOUT_MS = 30_000
_NETWORK_IDLE_TIMEOUT_MS = 10_000
_SPA_WAIT_TIMEOUT_MS = 15_000
# Minimum visible characters before treating an SPA shell as "rendered".
_SPA_MIN_TEXT_LEN = 80
# Minimum visible characters for Playwright text fallback when trafilatura fails.
_MIN_FALLBACK_TEXT_LEN = 50

# Monkey-patch trafilatura internals to better support WeChat Official Account
# articles, whose images live on `mmbiz.qpic.cn` without a standard file
# extension and whose main content sits inside `#js_content` /
# `.rich_media_content`. Trafilatura's `utils.IMAGE_EXTENSION` and
# `xpaths.BODY_XPATH` are internal APIs, so we guard the patch and skip
# silently if they are renamed/removed in a future release.
try:
    _WECHAT_IMAGE_EXTENSION = re.compile(
        r"[^\s]+\.(avif|bmp|gif|hei[cf]|jpe?g|png|webp)(\b|$)|"  # Standard extensions
        r"mmbiz\.qpic\.cn/[^\s]*wx_fmt=(jpeg|jpg|png|gif|webp)"  # WeChat query format
    )
    utils.IMAGE_EXTENSION = _WECHAT_IMAGE_EXTENSION

    _WECHAT_BODY_XPATH = XPath(
        '(.//*[@id="js_content" or contains(@class, "rich_media_content")])[1]'
    )
    _wechat_xpath_str = str(_WECHAT_BODY_XPATH)
    if not any(str(x) == _wechat_xpath_str for x in xpaths.BODY_XPATH):
        xpaths.BODY_XPATH.insert(0, _WECHAT_BODY_XPATH)
except (AttributeError, ImportError) as e:
    logger.warning(
        "Failed to patch trafilatura internals for WeChat support: %s", e
    )


@dataclass(frozen=True)
class _ScrapeResult:
    html: str
    visible_text: str
    page_title: str


def extract_markdown_from_html(html: str) -> Optional[str]:
    """Run trafilatura on HTML; return markdown or None if nothing extracted."""
    if not html or not html.strip():
        return None
    md_text = extract(
        html,
        output_format="markdown",
        with_metadata=True,
        include_images=True,
        include_tables=True,
        include_links=True,
    )
    if not md_text or not md_text.strip():
        return None
    return md_text


def build_visible_text_fallback(visible_text: str, page_title: str = "") -> Optional[str]:
    """Build markdown from Playwright-visible text when trafilatura finds no article body."""
    text = (visible_text or "").strip()
    if len(text) < _MIN_FALLBACK_TEXT_LEN:
        return None
    title = (page_title or "").strip()
    if title and not text.startswith(title):
        return f"# {title}\n\n{text}"
    return text


async def wait_for_rendered_content(page: Page) -> None:
    """Wait for SPA/JS pages beyond the initial HTML shell."""
    try:
        await page.wait_for_load_state("networkidle", timeout=_NETWORK_IDLE_TIMEOUT_MS)
        logger.info("Network idle after navigation")
    except Exception:
        logger.info("Network idle wait timed out, continuing")

    try:
        await page.wait_for_function(
            """(minLen) => {
                const root = document.querySelector('#app')
                    || document.querySelector('main')
                    || document.body;
                return ((root?.innerText || '').trim().length >= minLen);
            }""",
            arg=_SPA_MIN_TEXT_LEN,
            timeout=_SPA_WAIT_TIMEOUT_MS,
        )
        logger.info("SPA/root visible text reached minimum length")
    except Exception:
        logger.info("SPA text wait timed out, using current DOM")


async def read_visible_text(page: Page) -> str:
    """Prefer #app/main innerText, then fall back to body."""
    return await page.evaluate(
        """() => {
            const root = document.querySelector('#app')
                || document.querySelector('main')
                || document.querySelector('[role="main"]')
                || document.body;
            return (root?.innerText || '').trim();
        }"""
    )


class StdWebParser(BaseParser):
    """Standard web page parser using Playwright and Trafilatura.

    This parser scrapes web pages using Playwright's WebKit browser and extracts
    clean content using Trafilatura library. It supports proxy configuration and
    converts HTML content to markdown format.
    """

    def __init__(self, title: str, **kwargs):
        """Initialize the web parser.

        Args:
            title: Title of the web page to be used as file name
            **kwargs: Additional arguments passed to BaseParser
        """
        self.title = title
        # Get proxy configuration from config if available
        self.proxy = CONFIG.external_https_proxy
        super().__init__(file_name=title, **kwargs)
        logger.info(f"Initialized WebParser with title: {title}")

    async def scrape(self, url: str) -> _ScrapeResult:
        """Scrape web page content using Playwright.

        Args:
            url: The URL of the web page to scrape

        Returns:
            HTML, visible text, and document title; empty fields on hard failure
        """
        logger.info(f"Starting web page scraping for URL: {url}")
        empty = _ScrapeResult(html="", visible_text="", page_title="")
        try:
            async with async_playwright() as p:
                kwargs = {}
                # Configure proxy if available
                if self.proxy:
                    kwargs["proxy"] = {"server": self.proxy}
                logger.info("Launching WebKit browser")
                browser = await p.webkit.launch(**kwargs)
                page = await browser.new_page()

                logger.info(f"Navigating to URL: {url}")
                try:
                    await page.goto(
                        url,
                        timeout=_GOTO_TIMEOUT_MS,
                        wait_until="domcontentloaded",
                    )
                    logger.info("Initial page load complete")
                except Exception as e:
                    logger.error(f"Error navigating to URL: {str(e)}")
                    await browser.close()
                    return empty

                await wait_for_rendered_content(page)

                page_title = await page.title()
                visible_text = await read_visible_text(page)
                content = await page.content()
                logger.info(
                    "Retrieved %d bytes HTML, %d chars visible text, title=%r",
                    len(content),
                    len(visible_text),
                    page_title[:80] if page_title else "",
                )

                await browser.close()
                logger.info("Browser closed")

            logger.info("Successfully retrieved HTML content")
            return _ScrapeResult(
                html=content,
                visible_text=visible_text,
                page_title=page_title or "",
            )

        except Exception as e:
            logger.error(f"Failed to scrape web page: {str(e)}")
            return empty

    def parse_into_text(self, content: bytes) -> Document:
        """Parse web page content into a Document object.

        Args:
            content: URL encoded as bytes

        Returns:
            Document object containing the parsed markdown content
        """
        url = endecode.decode_bytes(content)

        logger.info(f"Scraping web page: {url}")
        scrape_result = asyncio.run(self.scrape(url))
        if not scrape_result.html and not scrape_result.visible_text:
            logger.error("Failed to scrape web page (no HTML or visible text)")
            return Document(content=f"Error parsing web page: {url}")

        md_text = extract_markdown_from_html(scrape_result.html)
        if not md_text:
            md_text = build_visible_text_fallback(
                scrape_result.visible_text,
                scrape_result.page_title,
            )
            if md_text:
                logger.info(
                    "Trafilatura empty; using Playwright visible-text fallback (%d chars)",
                    len(md_text),
                )

        if not md_text:
            logger.error("Failed to parse web page")
            return Document(content=f"Error parsing web page: {url}")

        metadata = {}
        title_match = re.search(r"^title:\s*(.+)", md_text, re.MULTILINE)
        if title_match:
            extracted_title = title_match.group(1).strip()
            if extracted_title:
                metadata["title"] = extracted_title
                logger.info(
                    f"Extracted article title from trafilatura: {extracted_title}"
                )
        elif scrape_result.page_title:
            metadata["title"] = scrape_result.page_title.strip()
            logger.info(
                "Using page title from Playwright: %s", metadata["title"]
            )
        else:
            logger.info(
                "No title found in trafilatura output, first 200 chars: %r",
                md_text[:200],
            )
        return Document(content=md_text, metadata=metadata)


class WebParser(PipelineParser):
    """Web parser using pipeline pattern.

    This parser chains StdWebParser (for web scraping and HTML to markdown conversion)
    with MarkdownParser (for markdown processing). The pipeline processes content
    sequentially through both parsers.
    """

    # Parser classes to be executed in sequence
    _parser_cls = (StdWebParser, MarkdownParser)


if __name__ == "__main__":
    import sys

    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(name)s: %(message)s")

    url = sys.argv[1] if len(sys.argv) > 1 else "https://cloud.tencent.com/document/product/457/6759"
    print(f"\n{'='*60}")
    print(f"URL: {url}")
    print(f"{'='*60}\n")

    parser = WebParser(title="")
    doc = parser.parse_into_text(url.encode())

    print(f"--- metadata ---")
    for k, v in doc.metadata.items():
        print(f"  {k}: {v}")

    print(f"\n--- images ({len(doc.images)}) ---")
    for path in list(doc.images.keys())[:10]:
        print(f"  {path}  ({len(doc.images[path])} chars base64)")

    print(f"\n--- content ({len(doc.content)} chars) ---")
    print(doc.content[:300000])
    if len(doc.content) > 300000:
        print(f"\n... (truncated, total {len(doc.content)} chars)")

    print(f"\n--- chunks ({len(doc.chunks)}) ---")
    for i, chunk in enumerate(doc.chunks[:5]):
        print(f"  [{i}] seq={chunk.seq} range=[{chunk.start}:{chunk.end}] len={len(chunk.content)}")
        print(f"      {chunk.content[:120]}{'...' if len(chunk.content) > 120 else ''}")
    if len(doc.chunks) > 5:
        print(f"  ... ({len(doc.chunks) - 5} more chunks)")
