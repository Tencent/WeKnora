import asyncio
import re
import logging
import numpy as np
import os  # Import os module to get environment variables
from typing import Dict, List, Optional, Tuple, Union, Any
from .base_parser import BaseParser

# Get logger object
logger = logging.getLogger(__name__)


class MarkdownParser(BaseParser):
    """Markdown document parser"""

    def parse_into_text(self, content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
        """Parse Markdown document, only extract text content, do not process images

        Args:
            content: Markdown document content

        Returns:
            Parsed text result
        """
        logger.info(f"Parsing Markdown document, content size: {len(content)} bytes")

        # Convert byte content to string using universal decoding method
        text = self.decode_bytes(content)
        logger.info(f"Decoded Markdown content, text length: {len(text)} characters")

        logger.info(f"Markdown parsing complete, extracted {len(text)} characters of text")
        return text

