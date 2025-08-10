import logging
import os
import asyncio
from PIL import Image
import io
from .base_parser import BaseParser, ParseResult
import numpy as np

# Set up logger for this module
logger = logging.getLogger(__name__)


class ImageParser(BaseParser):
    """
    Parser for image files with OCR capability.
    Extracts text from images and generates captions.

    This parser handles image processing by:
    1. Uploading the image to storage
    2. Generating a descriptive caption
    3. Performing OCR to extract text content
    4. Returning a combined result with both text and image reference
    """

    def parse_into_text(self, content: bytes) -> str:
        """
        Parse image content, only upload the image and return Markdown reference, no OCR or caption processing.

        Args:
            content: Raw image data (bytes)

        Returns:
            String containing Markdown image reference
        """
        logger.info(f"Parsing image content, size: {len(content)} bytes")
        try:
            # Upload image to storage service
            logger.info("Uploading image to storage")
            _, ext = os.path.splitext(self.file_name)
            
            # Get COS config from chunking config if available
            cos_config = None
            if hasattr(self, 'chunking_config') and self.chunking_config and hasattr(self.chunking_config, 'cos_config'):
                cos_config = self.chunking_config.cos_config
            
            image_url = self.upload_bytes(content, file_ext=ext, cos_config=cos_config)
            if not image_url:
                logger.error("Failed to upload image to storage")
                return ""
            logger.info(
                f"Successfully uploaded image, URL: {image_url[:50]}..."
                if len(image_url) > 50
                else f"Successfully uploaded image, URL: {image_url}"
            )

            # Directly generate Markdown image reference, no OCR or caption processing
            markdown_text = f"![{self.file_name}]({image_url})"
            logger.info("Generated Markdown image reference without OCR or caption processing")

            return markdown_text

        except Exception as e:
            logger.error(f"Error parsing image: {str(e)}")
            return ""
