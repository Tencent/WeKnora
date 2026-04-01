import asyncio
import logging
import re

from playwright.async_api import async_playwright
from trafilatura import extract

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.utils import endecode

logger = logging.getLogger(__name__)


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

    async def scrape(self, url: str) -> str:
        """Scrape web page content using Playwright.

        Args:
            url: The URL of the web page to scrape

        Returns:
            HTML content of the web page as string, empty string on error
        """
        logger.info(f"Starting web page scraping for URL: {url}")
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
                    # Navigate to URL with 30 second timeout
                    await page.goto(url, timeout=30000)
                    logger.info("Initial page load complete")
                except Exception as e:
                    logger.error(f"Error navigating to URL: {str(e)}")
                    await browser.close()
                    return ""

                logger.info("Retrieving page HTML content")
                # Get the full HTML content of the page
                content = await page.content()
                logger.info(f"Retrieved {len(content)} bytes of HTML content")

                await browser.close()
                logger.info("Browser closed")

            # Return raw HTML content for further processing
            logger.info("Successfully retrieved HTML content")
            return content

        except Exception as e:
            logger.error(f"Failed to scrape web page: {str(e)}")
            # Return empty string on error
            return ""

    def parse_into_text(self, content: bytes) -> Document:
        """Parse web page content into a Document object.

        Args:
            content: URL encoded as bytes

        Returns:
            Document object containing the parsed markdown content
        """
        # Decode bytes to get the URL string
        url = endecode.decode_bytes(content)

        logger.info(f"Scraping web page: {url}")
        # Run async scraping in sync context
        chtml = asyncio.run(self.scrape(url))
        # Extract clean content from HTML using Trafilatura
        # Convert to markdown format with metadata, images, tables, and links
        md_text = extract(
            chtml,
            output_format="markdown",
            with_metadata=True,
            include_images=True,
            include_tables=True,
            include_links=True,
        )
        if not md_text:
            logger.error("Failed to parse web page")
            return Document(content=f"Error parsing web page: {url}")

        # Extract title from trafilatura metadata output (e.g. "title: xxx" line)
        metadata = {}
        title_match = re.search(r"^title:\s*(.+)", md_text, re.MULTILINE)
        if title_match:
            extracted_title = title_match.group(1).strip()
            if extracted_title:
                metadata["title"] = extracted_title
                logger.info(f"Extracted article title from trafilatura: {extracted_title}")
        else:
            logger.info(f"No title found in trafilatura output, first 200 chars: {md_text[:200]!r}")
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
