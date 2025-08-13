import logging
import os
import io
from typing import Any, List, Iterator, Optional, Mapping, Tuple, Dict, Union

from pypdf import PdfReader
from .base_parser import BaseParser

logger = logging.getLogger(__name__)

class PDFParser(BaseParser):
    """
    PDF Document Parser

    This parser handles PDF documents by extracting text content.
    It uses the pypdf library for simple text extraction.
    """

    def parse_into_text(self, content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
        """
        Parse PDF document content into text

        This method processes a PDF document by extracting text content.

        Args:
            content: PDF document content as bytes

        Returns:
            Extracted text content
        """
        logger.info(f"Parsing PDF document, content size: {len(content)} bytes")

        try:
            # Use io.BytesIO to read content from bytes
            pdf_file = io.BytesIO(content)
            
            # Create PdfReader object
            pdf_reader = PdfReader(pdf_file)
            num_pages = len(pdf_reader.pages)
            logger.info(f"PDF has {num_pages} pages")
            
            # Extract text from all pages
            all_text = []
            for page_num, page in enumerate(pdf_reader.pages):
                try:
                    page_text = page.extract_text()
                    if page_text:
                        all_text.append(page_text)
                        logger.info(f"Successfully extracted text from page {page_num+1}/{num_pages}")
                    else:
                        logger.warning(f"No text extracted from page {page_num+1}/{num_pages}")
                except Exception as e:
                    logger.error(f"Error extracting text from page {page_num+1}: {str(e)}")
            
            # Combine all extracted text
            result = "\n\n".join(all_text)
            logger.info(f"PDF parsing complete, extracted {len(result)} characters of text")
            return result
            
        except Exception as e:
            logger.error(f"Failed to parse PDF document: {str(e)}")
            return ""
