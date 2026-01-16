import logging
import re
from typing import Dict

import markdownify
import requests

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownTableFormatter
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class StdMinerUParser(BaseParser):
    """
    Standard MinerU Parser for document parsing.

    This parser uses MinerU API to parse documents (especially PDFs) into markdown format,
    with support for tables, formulas, and images extraction.
    """

    def __init__(
        self,
        enable_markdownify: bool = True,
        **kwargs,
    ):
        """
        Initialize MinerU parser.

        Args:
            enable_markdownify: Whether to convert HTML tables to markdown format
            mineru_endpoint: MinerU API endpoint URL
            **kwargs: Additional arguments passed to BaseParser
        """
        super().__init__(**kwargs)
        # Get MinerU endpoint from environment variable or parameter
        self.minerU = CONFIG.mineru_endpoint
        self.enable_markdownify = enable_markdownify
        # Helper for processing markdown images
        self.image_helper = MarkdownImageUtil()
        # Pattern to match base64 encoded images
        self.base64_pattern = re.compile(r"data:image/(\w+);base64,(.*)")
        # Check if MinerU API is available
        self.enable = self.ping()

    def ping(self, timeout: int = 5) -> bool:
        """
        Check if MinerU API is available.

        Args:
            timeout: Request timeout in seconds

        Returns:
            True if API is available, False otherwise
        """
        try:
            response = requests.get(
                self.minerU + "/docs", timeout=timeout, allow_redirects=True
            )
            response.raise_for_status()
            return True
        except Exception:
            return False

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse document content into text using MinerU API.

        Args:
            content: Raw document content in bytes

        Returns:
            Document object containing parsed text and images
        """
        if not self.enable:
            logger.debug("MinerU API is not enabled")
            return Document()

        logger.info(f"Parsing scanned PDF via MinerU API (size: {len(content)} bytes)")
        md_content: str = ""
        images_b64: Dict[str, str] = {}
        try:
            # Call MinerU API to parse document
            response = requests.post(
                url=self.minerU + "/file_parse",
                data={
                    "return_md": True,  # Return markdown content
                    "return_images": True,  # Return extracted images
                    "lang_list": ["ch", "en"],  # Support Chinese and English
                    "table_enable": True,  # Enable table parsing
                    "formula_enable": True,  # Enable formula parsing
                    "parse_method": "auto",  # Auto detect parsing method
                    "start_page_id": 0,  # Start from first page
                    "end_page_id": 99999,  # Parse all pages
                    "backend": "pipeline",  # Use pipeline backend
                    "response_format_zip": False,  # Return JSON instead of ZIP
                    "return_middle_json": False,  # Don't return intermediate JSON
                    "return_model_output": False,  # Don't return model output
                    "return_content_list": False,  # Don't return content list
                },
                files={"files": content},
                timeout=1000,
            )
            response.raise_for_status()
            result = response.json()["results"]["files"]
            md_content = result["md_content"]
            images_b64 = result.get("images", {})
        except Exception as e:
            logger.error(f"MinerU parsing failed: {e}", exc_info=True)
            return Document()

        # Convert HTML tables in markdown to markdown table format
        if self.enable_markdownify:
            logger.debug("Converting HTML to Markdown")
            md_content = markdownify.markdownify(md_content)

        images = {}
        image_replace = {}
        # Filter images that are actually used in markdown content
        # Some images in images_b64 may not be referenced in md_content
        # (e.g., images embedded in tables), so we need to filter them
        for ipath, b64_str in images_b64.items():
            # Skip images that are not referenced in markdown content
            if f"images/{ipath}" not in md_content:
                logger.debug(f"Image {ipath} not used in markdown")
                continue
            # Parse base64 image data
            match = self.base64_pattern.match(b64_str)
            if match:
                # Extract image format (e.g., png, jpg)
                file_ext = match.group(1)
                # Extract base64 encoded data
                b64_str = match.group(2)

                # Decode base64 string to bytes
                image_bytes = endecode.encode_image(b64_str, errors="ignore")
                if not image_bytes:
                    logger.error("Failed to decode base64 image skip it")
                    continue

                # Upload image to storage and get URL
                image_url = self.storage.upload_bytes(
                    image_bytes, file_ext=f".{file_ext}"
                )

                # Store image mapping for later use
                images[image_url] = b64_str
                # Prepare replacement mapping for markdown content
                image_replace[f"images/{ipath}"] = image_url

        logger.info(f"Replaced {len(image_replace)} images in markdown")
        # Replace image paths in markdown with uploaded URLs
        text = self.image_helper.replace_path(md_content, image_replace)

        logger.info(
            f"Successfully parsed PDF, text: {len(text)}, images: {len(images)}"
        )
        return Document(content=text, images=images)


class MinerUParser(PipelineParser):
    """
    MinerU Parser with pipeline processing.

    This parser combines StdMinerUParser for document parsing and
    MarkdownTableFormatter for table formatting in a pipeline.
    """

    _parser_cls = (StdMinerUParser, MarkdownTableFormatter)


if __name__ == "__main__":
    import os

    # Example usage for testing
    logging.basicConfig(level=logging.DEBUG)

    # Configure your file path and MinerU endpoint
    your_file = "/path/to/your/file.pdf"
    os.environ["MINERU_ENDPOINT"] = "http://host.docker.internal:9987"

    # Create parser instance
    parser = MinerUParser()

    # Parse PDF file
    with open(your_file, "rb") as f:
        content = f.read()
        document = parser.parse_into_text(content)
        logger.error(document.content)
