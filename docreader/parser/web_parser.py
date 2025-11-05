from typing import Any, Optional, Tuple, Dict, Union
import os

from playwright.async_api import async_playwright
from bs4 import BeautifulSoup
from .base_parser import BaseParser, ParseResult
import logging
import asyncio

logger = logging.getLogger(__name__)


class WebParser(BaseParser):
    """Web page parser"""

    def __init__(self, title: str, **kwargs):
        self.title = title
        self.proxy = os.environ.get("WEB_PROXY", "")
        super().__init__(file_name=title, **kwargs)
        logger.info(f"Initialized WebParser with title: {title}")

    async def scrape(self, url: str) -> Any:
        logger.info(f"Starting web page scraping for URL: {url}")
        try:
            async with async_playwright() as p:
                kwargs = {}
                if self.proxy:
                    kwargs["proxy"] = {"server": self.proxy}
                logger.info("Launching WebKit browser")
                browser = await p.webkit.launch(**kwargs)
                page = await browser.new_page()

                logger.info(f"Navigating to URL: {url}")
                try:
                    await page.goto(url, timeout=30000)
                    logger.info("Initial page load complete")
                except Exception as e:
                    logger.error(f"Error navigating to URL: {str(e)}")
                    await browser.close()
                    return BeautifulSoup(
                        "", "html.parser"
                    )  # Return empty soup on navigation error

                logger.info("Retrieving page HTML content")
                content = await page.content()
                logger.info(f"Retrieved {len(content)} bytes of HTML content")

                await browser.close()
                logger.info("Browser closed")

            # Parse HTML content with BeautifulSoup
            logger.info("Parsing HTML with BeautifulSoup")
            soup = BeautifulSoup(content, "html.parser")
            logger.info("Successfully parsed HTML content")
            return soup

        except Exception as e:
            logger.error(f"Failed to scrape web page: {str(e)}")
            # Return empty BeautifulSoup object on error
            return BeautifulSoup("", "html.parser")

    def parse_into_text(self, content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
        """Parse web page

        Args:
            content: Web page content

        Returns:
            Parse result
        """
        logger.info("Starting web page parsing")

        # Call async method synchronously
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)

        try:
            # Run async method
            # Handle content possibly being a string
            if isinstance(content, bytes):
                url = self.decode_bytes(content)
                logger.info(f"Decoded URL from bytes: {url}")
            else:
                url = content
                logger.info(f"Using content as URL directly: {url}")

            logger.info(f"Scraping web page: {url}")
            soup = loop.run_until_complete(self.scrape(url))

            # Extract page text
            logger.info("Extracting text from web page")
            text = soup.get_text("\n")
            logger.info(f"Extracted {len(text)} characters of text from URL: {url}")

            # Get title, usually in <title> or <h1> tag
            if self.title != "":
                title = self.title
                logger.info(f"Using provided title: {title}")
            else:
                title = soup.title.string if soup.title else None
                logger.info(f"Found title tag: {title}")

            if not title:  # If <title> tag does not exist or is empty, try <h1> tag
                h1_tag = soup.find("h1")
                if h1_tag:
                    title = h1_tag.get_text()
                    logger.info(f"Using h1 tag as title: {title}")
                else:
                    title = "Untitled Web Page"
                    logger.info("No title found, using default")

            logger.info(f"Web page title: {title}")
            text = "\n".join(
                (line.strip() for line in text.splitlines() if line.strip())
            )

            result = title + "\n\n" + text
            logger.info(
                f"Web page parsing complete, total content: {len(result)} characters"
            )
            return result

        except Exception as e:
            logger.error(f"Error parsing web page: {str(e)}")
            return f"Error parsing web page: {str(e)}"

        finally:
            # Close event loop
            logger.info("Closing event loop")
            loop.close()
