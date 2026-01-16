import base64
import logging
import os

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

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

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse image content into markdown text
        :param content: bytes content of the image
        :return: Document object
        """
        logger.info(f"Parsing image content, size: {len(content)} bytes")

        # Get file extension
        ext = os.path.splitext(self.file_name)[1].lower()

        # Upload image to storage
        image_url = self.storage.upload_bytes(content, file_ext=ext)
        logger.info(f"Successfully uploaded image, URL: {image_url[:50]}...")

        if not image_url:
            logger.warning(f"Failed to upload image: {self.file_name}")
            return Document(content=f"{self.file_name}", images={})

        # Generate markdown text
        text = f"![{self.file_name}]({image_url})"
        images = {image_url: base64.b64encode(content).decode()}

        # Create image object and add to map
        return Document(content=text, images=images)
