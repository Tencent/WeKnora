"""
Markdown Parser Module

This module provides comprehensive Markdown parsing functionality including:
- Table formatting and standardization
- Base64 image extraction and conversion
- Image path replacement and URL generation
- Pipeline-based parsing with multiple stages

The parser uses a pipeline approach to process Markdown content through
multiple stages: table formatting -> image processing.
"""

import base64
import logging
import os
import re
import uuid
from typing import Dict, List, Match, Optional, Tuple

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.utils import endecode

# Get logger object
logger = logging.getLogger(__name__)


class MarkdownTableUtil:
    """Utility class for formatting Markdown tables.

    This class standardizes Markdown table formatting by:
    - Normalizing column alignment markers (e.g., :---, :---:, ---:)
    - Adding consistent spacing around pipes (|)
    - Preserving indentation levels
    - Handling both header rows and data rows

    Example:
        Input:  |姓名|年龄|城市|
                |:---|---:|:---:|
                |张三|25|北京|

        Output: | 姓名 | 年龄 | 城市 |
                | :--- | ---: | :---: |
                | 张三 | 25 | 北京 |
    """

    def __init__(self):
        # Pattern to match alignment row (e.g., |:---|---:|:---:|)
        self.align_pattern = re.compile(
            r"^([\t ]*)\|[\t ]*[:-]+(?:[\t ]*\|[\t ]*[:-]+)*[\t ]*\|[\t ]*$",
            re.MULTILINE,
        )
        # Pattern to match regular table rows (header or data)
        self.line_pattern = re.compile(
            r"^([\t ]*)\|[\t ]*[^|\r\n]*(?:[\t ]*\|[^|\r\n]*)*\|[\t ]*$",
            re.MULTILINE,
        )

    def format_table(self, content: str) -> str:
        """Format all Markdown tables in the content.

        Args:
            content: Raw Markdown text containing tables

        Returns:
            Formatted Markdown text with standardized table formatting
        """

        def process_align(match: Match[str]) -> str:
            """Process alignment row to standardize format."""
            # Split by | and remove empty strings
            columns = [col.strip() for col in match.group(0).split("|") if col.strip()]

            processed = []
            for col in columns:
                # Preserve left alignment marker (:---)
                left_colon = ":" if col.startswith(":") else ""
                # Preserve right alignment marker (---:)
                right_colon = ":" if col.endswith(":") else ""
                processed.append(left_colon + "---" + right_colon)

            # Preserve original indentation
            prefix = match.group(1)
            return prefix + "| " + " | ".join(processed) + " |"

        def process_line(match: Match[str]) -> str:
            """Process regular table row to standardize format."""
            # Split by | and remove empty strings
            columns = [col.strip() for col in match.group(0).split("|") if col.strip()]

            # Preserve original indentation
            prefix = match.group(1)
            return prefix + "| " + " | ".join(columns) + " |"

        formatted_content = content
        # First format regular rows (header and data)
        formatted_content = self.line_pattern.sub(process_line, formatted_content)
        # Then format alignment rows (must be done after to avoid conflicts)
        formatted_content = self.align_pattern.sub(process_align, formatted_content)

        return formatted_content

    @staticmethod
    def _self_test():
        test_content = """
# 测试表格
普通文本---不会被匹配

## 表格1（无前置空格）

| 姓名   | 年龄  | 城市          |
|      :---------- | -------: | :------      |
| 张三 | 25 | 北京 |

## 表格3（前置4个空格+首尾|）
    |   产品   |   价格   |   库存   |
    | :-------------: | ----------- | :-----------: |
    | 手机 | 5999       | 100 |
"""
        util = MarkdownTableUtil()
        format_content = util.format_table(test_content)
        print(format_content)


class MarkdownTableFormatter(BaseParser):
    """Parser for formatting Markdown tables.

    This parser standardizes the formatting of all Markdown tables in the
    document to ensure consistent spacing and alignment markers.

    Example:
        >>> formatter = MarkdownTableFormatter()
        >>> content = b"|Name|Age|\n|---|---|\n|John|30|"
        >>> doc = formatter.parse_into_text(content)
        >>> print(doc.content)
        | Name | Age |
        | --- | --- |
        | John | 30 |
    """

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.table_helper = MarkdownTableUtil()

    def parse_into_text(self, content: bytes) -> Document:
        """Parse and format Markdown tables.

        Args:
            content: Raw Markdown content as bytes

        Returns:
            Document with formatted table content
        """
        # Decode bytes to string with automatic encoding detection
        text = endecode.decode_bytes(content)
        # Format all tables in the content
        text = self.table_helper.format_table(text)
        return Document(content=text)


class MarkdownImageUtil:
    """Utility class for handling images in Markdown.

    This class provides functionality to:
    - Extract base64-encoded images from Markdown
    - Extract image paths from Markdown
    - Replace image paths with new URLs
    - Convert base64 images to binary format

    Supported formats:
    - Base64 embedded images: ![alt](data:image/png;base64,iVBORw0...)
    - Regular image links: ![alt](path/to/image.png)
    """

    def __init__(self):
        # Pattern to match base64 embedded images
        # Captures: (1) alt text, (2) image format, (3) base64 data
        self.b64_pattern = re.compile(
            r"!\[([^\]]*)\]\(data:image/(\w+)\+?\w*;base64,([^\)]+)\)"
        )
        # Pattern to match regular image syntax
        self.image_pattern = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
        # Pattern for replacing image paths
        self.replace_pattern = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")

    def extract_image(
        self,
        content: str,
        path_prefix: Optional[str] = None,
        replace: bool = True,
    ) -> Tuple[str, List[str]]:
        """Extract image paths from Markdown content.

        Args:
            content: Markdown text containing images
            path_prefix: Optional prefix to add to image paths
            replace: Whether to replace image syntax in content

        Returns:
            Tuple of (processed_text, list_of_image_paths)

        Example:
            >>> util = MarkdownImageUtil()
            >>> text, images = util.extract_image("![logo](img/logo.png)")
            >>> print(images)
            ['img/logo.png']
        """
        # List to store extracted image paths
        images: List[str] = []

        def repl(match: Match[str]) -> str:
            """Replacement function for each image match."""
            title = match.group(1)  # Alt text
            image_path = match.group(2)  # Image path

            # Add prefix if specified
            if path_prefix:
                image_path = f"{path_prefix}/{image_path}"

            images.append(image_path)

            # Keep original if replace is False
            if not replace:
                return match.group(0)

            # Replace image path with potentially prefixed path
            return f"![{title}]({image_path})"

        text = self.image_pattern.sub(repl, content)
        logger.debug(f"Extracted {len(images)} images from markdown")
        return text, images

    def extract_base64(
        self,
        content: str,
        path_prefix: Optional[str] = None,
        replace: bool = True,
    ) -> Tuple[str, Dict[str, bytes]]:
        """Extract and decode base64 embedded images from Markdown.

        This method finds all base64-encoded images in the Markdown content,
        decodes them to binary format, generates unique filenames, and
        optionally replaces them with file path references.

        Args:
            content: Markdown text containing base64 images
            path_prefix: Optional directory prefix for generated paths
            replace: Whether to replace base64 syntax with file paths

        Returns:
            Tuple of (processed_text, dict_of_path_to_bytes)

        Example:
            >>> util = MarkdownImageUtil()
            >>> text = "![logo](data:image/png;base64,iVBORw0KGg...)"
            >>> new_text, images = util.extract_base64(text, "images")
            >>> print(new_text)
            ![logo](images/uuid.png)
            >>> print(len(images))
            1
        """
        # Dictionary mapping generated file paths to binary image data
        images: Dict[str, bytes] = {}

        def repl(match: Match[str]) -> str:
            """Replacement function for each base64 image match."""
            title = match.group(1)  # Alt text
            img_ext = match.group(2)  # Image format (png, jpg, etc.)
            img_b64 = match.group(3)  # Base64 encoded data

            # Decode base64 string to bytes
            image_byte = endecode.encode_image(img_b64, errors="ignore")
            if not image_byte:
                logger.error(f"Failed to decode base64 image skip it: {img_b64}")
                return title  # Return just the alt text if decode fails

            # Generate unique filename with original extension
            image_path = f"{uuid.uuid4()}.{img_ext}"
            if path_prefix:
                image_path = f"{path_prefix}/{image_path}"
            images[image_path] = image_byte

            # Keep original base64 if replace is False
            if not replace:
                return match.group(0)

            # Replace base64 data with file path reference
            return f"![{title}]({image_path})"

        text = self.b64_pattern.sub(repl, content)
        logger.debug(f"Extracted {len(images)} base64 images from markdown")
        return text, images

    def replace_path(self, content: str, images: Dict[str, str]) -> str:
        """Replace image paths in Markdown with new URLs.

        This method is typically used to replace local file paths with
        uploaded URLs after images have been stored.

        Args:
            content: Markdown text with image references
            images: Mapping of old paths to new URLs

        Returns:
            Markdown text with updated image URLs

        Example:
            >>> util = MarkdownImageUtil()
            >>> content = "![logo](temp/img.png)"
            >>> mapping = {"temp/img.png": "https://cdn.com/img.png"}
            >>> result = util.replace_path(content, mapping)
            >>> print(result)
            ![logo](https://cdn.com/img.png)
        """
        # Track which paths were actually replaced
        content_replace: set = set()

        def repl(match: Match[str]) -> str:
            """Replacement function for each image match."""
            title = match.group(1)  # Alt text
            image_path = match.group(2)  # Current image path

            # Only replace if path exists in mapping
            if image_path not in images:
                return match.group(0)  # Keep original

            content_replace.add(image_path)
            # Get new URL from mapping
            image_path = images[image_path]
            return f"![{title}]({image_path})" if image_path else title

        text = self.replace_pattern.sub(repl, content)
        logger.debug(f"Replaced {len(content_replace)} images in markdown")
        return text

    @staticmethod
    def _self_test():
        your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAA)test"
        image_handle = MarkdownImageUtil()
        text, images = image_handle.extract_base64(your_content)
        print(text)

        for image_url, image_byte in images.items():
            with open(image_url, "wb") as f:
                f.write(image_byte)


class MarkdownImageBase64(BaseParser):
    """Parser for extracting and uploading base64 images from Markdown.

    This parser:
    1. Extracts base64-encoded images from Markdown content
    2. Uploads the decoded images to storage
    3. Replaces base64 data with uploaded URLs
    4. Returns a Document with clean Markdown and image mappings

    Requires:
        - self.storage: Storage backend for uploading images

    Example:
        >>> parser = MarkdownImageBase64(storage=my_storage)
        >>> content = b"![logo](data:image/png;base64,iVBORw0...)"
        >>> doc = parser.parse_into_text(content)
        >>> print(doc.content)
        ![logo](https://storage.com/uuid.png)
    """

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.image_helper = MarkdownImageUtil()

    def parse_into_text(self, content: bytes) -> Document:
        """Parse Markdown and process base64 images.

        Args:
            content: Raw Markdown content as bytes

        Returns:
            Document with:
                - content: Markdown with base64 replaced by URLs
                - images: Dict mapping URLs to base64 strings
        """
        # Convert byte content to string using universal decoding method
        text = endecode.decode_bytes(content)
        # Extract base64 images and replace with temporary paths
        text, img_b64 = self.image_helper.extract_base64(text, path_prefix="images")

        # Final image mapping: URL -> base64 string (for Document)
        images: Dict[str, str] = {}
        # Temporary mapping: temp_path -> uploaded_URL (for replacement)
        image_replace: Dict[str, str] = {}

        logger.debug(f"Uploading {len(img_b64)} images from markdown")
        # Upload each extracted image to storage
        for ipath, b64_bytes in img_b64.items():
            # Get file extension for proper MIME type
            ext = os.path.splitext(ipath)[1].lower()
            # Upload binary data and get back URL
            image_url = self.storage.upload_bytes(b64_bytes, ext)

            # Map temp path to uploaded URL for replacement
            image_replace[ipath] = image_url
            # Store base64 string in final images dict
            images[image_url] = base64.b64encode(b64_bytes).decode()

        # Replace temporary paths with actual uploaded URLs
        text = self.image_helper.replace_path(text, image_replace)
        return Document(content=text, images=images)


class MarkdownParser(PipelineParser):
    """Complete Markdown parser using pipeline approach.

    This parser processes Markdown content through multiple stages:
    1. MarkdownTableFormatter: Standardizes table formatting
    2. MarkdownImageBase64: Extracts and uploads base64 images

    The pipeline ensures that content flows through each parser in sequence,
    with each stage's output becoming the next stage's input.
    """

    _parser_cls = (MarkdownTableFormatter, MarkdownImageBase64)


if __name__ == "__main__":
    # Example usage and testing
    logging.basicConfig(level=logging.DEBUG)

    # Test the complete MarkdownParser pipeline
    your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    parser = MarkdownParser()

    # Parse content and display results
    document = parser.parse_into_text(your_content.encode())
    logger.info(document.content)
    logger.info(f"Images: {len(document.images)}, name: {document.images.keys()}")

    # Run individual utility tests
    MarkdownImageUtil._self_test()
    MarkdownTableUtil._self_test()
