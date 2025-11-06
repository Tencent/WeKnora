import logging
from dataclasses import dataclass, field
from typing import Dict, Any, Optional, Type

from .base_parser import BaseParser, ParseResult
from .docx_parser import DocxParser
from .doc_parser import DocParser
from .pdf_parser import PDFParser
from .markdown_parser import MarkdownParser
from .text_parser import TextParser
from .image_parser import ImageParser
from .web_parser import WebParser
from .config import ChunkingConfig
import traceback

logger = logging.getLogger(__name__)

@dataclass
class Chunk:
    """
    Represents a single text chunk with associated metadata.
    Basic unit for document processing and embedding.
    """

    content: str  # Text content of the chunk
    metadata: Dict[str, Any] = None  # Associated metadata (source, page number, etc.)


class Parser:
    """
    Document parser facade that integrates all specialized parsers.
    Provides a unified interface for parsing various document types.
    """

    def __init__(self):
        logger.info("Initializing document parser")
        # Initialize all parser types
        self.parsers: Dict[str, Type[BaseParser]] = {
            "docx": DocxParser,
            "doc": DocParser,
            "pdf": PDFParser,
            "md": MarkdownParser,
            "txt": TextParser,
            "jpg": ImageParser,
            "jpeg": ImageParser,
            "png": ImageParser,
            "gif": ImageParser,
            "bmp": ImageParser,
            "tiff": ImageParser,
            "webp": ImageParser,
            "markdown": MarkdownParser,
        }
        logger.info(
            "Parser initialized with %d parsers: %s",
            len(self.parsers),
            ", ".join(self.parsers.keys()),
        )


    def get_parser(self, file_type: str) -> Optional[Type[BaseParser]]:
        """
        Get parser class for the specified file type.

        Args:
            file_type: The file extension or type identifier

        Returns:
            Parser class for the file type, or None if unsupported
        """
        file_type = file_type.lower()
        parser = self.parsers.get(file_type)
        if parser:
            logger.info(f"Found parser for file type: {file_type}")
        else:
            logger.warning(f"No parser found for file type: {file_type}")
        return parser

    def parse_file(
        self,
        file_name: str,
        file_type: str,
        content: bytes,
        config: ChunkingConfig,
    ) -> Optional[ParseResult]:
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
            f"Chunking config: size={config.chunk_size}, overlap={config.chunk_overlap}, "
            f"multimodal={config.enable_multimodal}"
        )
        
        parser_instance = None
        
        try:
            # Get appropriate parser for file type
            cls = self.get_parser(file_type)
            if cls is None:
                logger.error(f"Unsupported file type: {file_type}")
                return None

            # Parse file content
            logger.info(f"Creating parser instance for {file_type} file")
            parser_instance = cls(
                file_name=file_name,
                file_type=file_type,
                chunk_size=config.chunk_size,
                chunk_overlap=config.chunk_overlap,
                separators=config.separators,
                enable_multimodal=config.enable_multimodal,
                max_image_size=1920,  # Limit image size to 1920px
                max_concurrent_tasks=5,  # Limit concurrent tasks to 5
                chunking_config=config,  # Pass the entire chunking config
            )

            logger.info(f"Starting to parse file content, size: {len(content)} bytes")
            result = parser_instance.parse(content)

            if result:
                logger.info(
                    f"Successfully parsed file {file_name}, generated {len(result.chunks)} chunks"
                )
                if result.chunks and len(result.chunks) > 0:
                    logger.info(
                        f"First chunk content length: {len(result.chunks[0].content)}"
                    )
                else:
                    logger.warning(f"Parser returned empty chunks for file: {file_name}")
            else:
                logger.warning(f"Parser returned None result for file: {file_name}")

            # Return parse results
            return result

        except Exception as e:
            logger.error(f"Error parsing file {file_name}: {str(e)}")
            logger.info(f"Detailed traceback: {traceback.format_exc()}")
            return None

    def parse_url(
        self, url: str, title: str, config: ChunkingConfig
    ) -> Optional[ParseResult]:
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
            f"Chunking config: size={config.chunk_size}, overlap={config.chunk_overlap}, "
            f"multimodal={config.enable_multimodal}"
        )
        
        parser_instance = None

        try:
            # Create web parser instance
            logger.info("Creating WebParser instance")
            parser_instance = WebParser(
                title=title,
                chunk_size=config.chunk_size,
                chunk_overlap=config.chunk_overlap,
                separators=config.separators,
                enable_multimodal=config.enable_multimodal,
                max_image_size=1920,  # Limit image size
                max_concurrent_tasks=5,  # Limit concurrent tasks
                chunking_config=config,
            )

            logger.info(f"Starting to parse URL content")
            result = parser_instance.parse(url)

            if result:
                logger.info(
                    f"Successfully parsed URL, generated {len(result.chunks)} chunks"
                )
                logger.info(
                    f"First chunk content length: {len(result.chunks[0].content) if result.chunks else 0}"
                )
            else:
                logger.warning(f"Parser returned empty result for URL: {url}")

            # Return parse results
            return result

        except Exception as e:
            logger.error(f"Error parsing URL {url}: {str(e)}")
            logger.info(f"Detailed traceback: {traceback.format_exc()}")
            return None

