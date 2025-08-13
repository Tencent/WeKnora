"""
Parser module for WeKnora document processing system.

This module provides document parsers for various file formats including:
- Microsoft Word documents (.doc, .docx)
- PDF documents
- Markdown files
- Plain text files
- Images with text content
- Web pages

The parsers extract content from documents and can split them into
meaningful chunks for further processing and indexing.
"""

from .base_parser import BaseParser, ParseResult
from .docx_parser import DocxParser
from .doc_parser import DocParser
from .pdf_parser import PDFParser
from .markdown_parser import MarkdownParser
from .text_parser import TextParser
from .image_parser import ImageParser
from .web_parser import WebParser
from .parser import Parser
from .config import ChunkingConfig
from .ocr_engine import OCREngine

# Export public classes and modules
__all__ = [
    "BaseParser",  # Base parser class that all format parsers inherit from
    "DocxParser",  # Parser for .docx files (modern Word documents)
    "DocParser",  # Parser for .doc files (legacy Word documents)
    "PDFParser",  # Parser for PDF documents
    "MarkdownParser",  # Parser for Markdown text files
    "TextParser",  # Parser for plain text files
    "ImageParser",  # Parser for images with text content
    "WebParser",  # Parser for web pages
    "Parser",  # Main parser factory that selects the appropriate parser
    "ChunkingConfig",  # Configuration for text chunking behavior
    "ParseResult",  # Standard result format returned by all parsers
    "OCREngine",  # OCR engine for extracting text from images
]
