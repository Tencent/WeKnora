import logging
from typing import Dict, Type

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.models.read_config import ChunkingConfig
from docreader.parser.base_parser import BaseParser
from docreader.parser.csv_parser import CSVParser
from docreader.parser.doc_parser import DocParser
from docreader.parser.docx2_parser import Docx2Parser
from docreader.parser.excel_parser import ExcelParser
from docreader.parser.image_parser import ImageParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.pdf_parser import PDFParser
from docreader.parser.text_parser import TextParser
from docreader.parser.web_parser import WebParser

logger = logging.getLogger(__name__)


class Parser:
    """
    Document parser facade that integrates all specialized parsers.
    Provides a unified interface for parsing various document types.
    """

    def __init__(self):
        # Initialize all parser types - maps file extensions to their corresponding parser classes
        self.parsers: Dict[str, Type[BaseParser]] = {
            # Document formats
            "docx": Docx2Parser,
            "doc": DocParser,
            "pdf": PDFParser,
            "md": MarkdownParser,
            "txt": TextParser,
            # Image formats - all use the same ImageParser
            "jpg": ImageParser,
            "jpeg": ImageParser,
            "png": ImageParser,
            "gif": ImageParser,
            "bmp": ImageParser,
            "tiff": ImageParser,
            "webp": ImageParser,
            # Alternative markdown extension
            "markdown": MarkdownParser,
            # Spreadsheet formats
            "csv": CSVParser,
            "xlsx": ExcelParser,
            "xls": ExcelParser,
        }
        logger.info(
            "Parser initialized with %d parsers: %s",
            len(self.parsers),
            ", ".join(self.parsers.keys()),
        )

    def get_parser(self, file_type: str) -> Type[BaseParser]:
        """
        Get parser class for the specified file type.

        Args:
            file_type: The file extension or type identifier

        Returns:
            Parser class for the file type, or None if unsupported
        """
        # Look up parser by file type (case-insensitive)
        parser = self.parsers.get(file_type.lower())
        if not parser:
            # Raise error if file type is not supported
            raise ValueError(f"Unsupported file type: {file_type}")
        return parser

    def parse_file(
        self,
        file_name: str,
        file_type: str,
        content: bytes,
        config: ChunkingConfig,
    ) -> Document:
        """
        Parse file content using appropriate parser based on file type.

        Args:
            file_name: Name of the file being parsed
            file_type: Type/extension of the file
            content: Raw file content as bytes
            config: Configuration for chunking process

        Returns:
            ParseResult containing chunks and metadata, or None if parsing failed
        """
        logger.info(f"Parsing file: {file_name} with type: {file_type}")
        logger.info(
            f"Chunking config: size={config.chunk_size}, "
            f"overlap={config.chunk_overlap}, "
            f"multimodal={config.enable_multimodal}"
        )

        # Get appropriate parser class for the file type
        cls = self.get_parser(file_type)

        # Create parser instance with configuration
        logger.info(f"Creating parser instance for {file_type} file")
        parser = cls(
            file_name=file_name,
            file_type=file_type,
            chunk_size=config.chunk_size,  # Size of each text chunk
            chunk_overlap=config.chunk_overlap,  # Overlap between consecutive chunks
            separators=config.separators,  # Text separators for chunking
            enable_multimodal=config.enable_multimodal,  # Enable image/multimodal processing
            max_image_size=1920,  # Limit image size to 1920px for performance
            chunking_config=config,  # Pass the entire chunking config for advanced options
            max_concurrent_tasks=CONFIG.image_max_concurrent,
            ocr_backend=CONFIG.ocr_backend,
        )

        logger.info(f"Starting to parse file content, size: {len(content)} bytes")
        # Execute the parsing process
        result = parser.parse(content)

        # Validate parsing results and log warnings if needed
        if not result.content:
            logger.warning(f"Parser returned empty content for file: {file_name}")
        elif not result.chunks:
            logger.warning(f"Parser returned empty chunks for file: {file_name}")
        elif result.chunks[0]:
            # Log first chunk size for debugging
            logger.info(f"First chunk content length: {len(result.chunks[0].content)}")
        logger.info(f"Parsed file {file_name}, with {len(result.chunks)} chunks")
        return result

    def parse_url(self, url: str, title: str, config: ChunkingConfig) -> Document:
        """
        Parse content from a URL using the WebParser.

        Args:
            url: URL to parse
            title: Title of the webpage (for metadata)
            config: Configuration for chunking process

        Returns:
            ParseResult containing chunks and metadata, or None if parsing failed
        """
        logger.info(f"Parsing URL: {url}, title: {title}")
        logger.info(
            f"Chunking config: size={config.chunk_size}, "
            f"overlap={config.chunk_overlap}, multimodal={config.enable_multimodal}"
        )

        # Create web parser instance with configuration
        logger.info("Creating WebParser instance")
        parser = WebParser(
            title=title,  # Webpage title for metadata
            chunk_size=config.chunk_size,  # Size of each text chunk
            chunk_overlap=config.chunk_overlap,  # Overlap between consecutive chunks
            separators=config.separators,  # Text separators for chunking
            enable_multimodal=config.enable_multimodal,  # Enable image/multimodal processing
            max_image_size=1920,  # Limit image size to 1920px for performance
            chunking_config=config,  # Pass the entire chunking config
            max_concurrent_tasks=CONFIG.image_max_concurrent,
            ocr_backend=CONFIG.ocr_backend,
        )

        logger.info("Starting to parse URL content")
        # Parse URL content (encode URL string to bytes as required by parser interface)
        result = parser.parse(url.encode())

        # Validate parsing results and log warnings if needed
        if not result.content:
            logger.warning(f"Parser returned empty content for url: {url}")
        elif not result.chunks:
            logger.warning(f"Parser returned empty chunks for url: {url}")
        elif result.chunks[0]:
            # Log first chunk size for debugging
            logger.info(f"First chunk content length: {len(result.chunks[0].content)}")
        logger.info(f"Parsed url {url}, with {len(result.chunks)} chunks")
        return result
