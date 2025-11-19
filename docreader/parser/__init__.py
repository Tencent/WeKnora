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

from .csv_parser import CSVParser
from .doc_parser import DocParser
from .docx2_parser import Docx2Parser
from .excel_parser import ExcelParser
from .image_parser import ImageParser
from .markdown_parser import MarkdownParser
from .parser import Parser
from .pdf_parser import PDFParser
from .text_parser import TextParser
from .web_parser import WebParser

# Export public classes and modules
__all__ = [
    "Docx2Parser",  # Parser for .docx files (modern Word documents)
    "DocParser",  # Parser for .doc files (legacy Word documents)
    "PDFParser",  # Parser for PDF documents
    "MarkdownParser",  # Parser for Markdown text files
    "TextParser",  # Parser for plain text files
    "ImageParser",  # Parser for images with text content
    "WebParser",  # Parser for web pages
    "Parser",  # Main parser factory that selects the appropriate parser
    "CSVParser",  # Parser for CSV files
    "ExcelParser",  # Parser for Excel files
]
