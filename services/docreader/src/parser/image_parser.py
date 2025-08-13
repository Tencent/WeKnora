import logging
import os
import asyncio
from PIL import Image
import io
from typing import Dict, Any, Tuple, Union
from .base_parser import BaseParser, ParseResult
import numpy as np

# Set up logger for this module
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)

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

    def parse_into_text(self, content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
        """
        Parse image content, upload the image and return Markdown reference along with image map.

        Args:
            content: Raw image data (bytes)

        Returns:
            Tuple of (markdown_text, image_map) where image_map maps image URLs to PIL Image objects
        """
        logger.info(f"Parsing image content, size: {len(content)} bytes")
        image_map = {}
        
        try:
            # Upload image to storage service
            logger.info("Uploading image to storage")
            _, ext = os.path.splitext(self.file_name)
            image_url = self.upload_bytes(content, file_ext=ext)
            if not image_url:
                logger.error("Failed to upload image to storage")
                return "", {}
            logger.info(
                f"Successfully uploaded image, URL: {image_url[:50]}..."
                if len(image_url) > 50
                else f"Successfully uploaded image, URL: {image_url}"
            )

            # Create image object and add to map
            try:
                from PIL import Image
                import io
                image = Image.open(io.BytesIO(content))
                image_map[image_url] = image
                logger.info(f"Added image to image_map for URL: {image_url}")
            except Exception as img_err:
                logger.error(f"Error creating image object: {str(img_err)}")

            markdown_text = f"![{self.file_name}]({image_url})"
            return markdown_text, image_map

        except Exception as e:
            logger.error(f"Error parsing image: {str(e)}")
            return "", {}
