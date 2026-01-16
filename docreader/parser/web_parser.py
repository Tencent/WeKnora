import asyncio
import logging

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
        return Document(content=md_text)


class WebParser(PipelineParser):
    """Web parser using pipeline pattern.

    This parser chains StdWebParser (for web scraping and HTML to markdown conversion)
    with MarkdownParser (for markdown processing). The pipeline processes content
    sequentially through both parsers.
    """

    # Parser classes to be executed in sequence
    _parser_cls = (StdWebParser, MarkdownParser)


if __name__ == "__main__":
    # Configure logging for debugging
    logging.basicConfig(level=logging.DEBUG)
    logger.setLevel(logging.DEBUG)

    # Example URL to scrape
    url = "https://cloud.tencent.com/document/product/457/6759"

    # Create parser instance and parse the web page
    parser = WebParser(title="")
    cc = parser.parse_into_text(url.encode())
    # Save the parsed markdown content to file
    with open("./tencent.md", "w") as f:
        f.write(cc.content)
