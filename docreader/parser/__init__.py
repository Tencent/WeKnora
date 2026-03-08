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

from .doc_parser import DocParser
from .docx2_parser import Docx2Parser
from .excel_parser import ExcelParser
from .image_parser import ImageParser
from .markdown_parser import MarkdownParser
from .parser import Parser
from .pdf_parser import PDFParser
from .registry import ParserEngineRegistry, registry
from .web_parser import WebParser

# Export public classes and modules
__all__ = [
    "Docx2Parser",
    "DocParser",
    "PDFParser",
    "MarkdownParser",
    "ImageParser",
    "WebParser",
    "Parser",
    "ExcelParser",
    "ParserEngineRegistry",
    "registry",
]
