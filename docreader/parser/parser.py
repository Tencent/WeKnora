import logging
from typing import Any, Optional

from docreader.models.document import Document
from docreader.parser.registry import registry
from docreader.parser.web_parser import WebParser

logger = logging.getLogger(__name__)


class Parser:
    """Document parser facade (lightweight version).

    Converts files/URLs to markdown + image references.
    No chunking, no storage, no OCR, no VLM.
    """

    def __init__(self):
        self.registry = registry
        logger.info(
            "Parser initialized with engines: %s",
            ", ".join(self.registry.get_engine_names()),
        )

    def parse_file(
        self,
        file_name: str,
        file_type: str,
        content: bytes,
        parser_engine: Optional[str] = None,
        engine_overrides: Optional[dict[str, Any]] = None,
    ) -> Document:
        """Parse file content to markdown."""
        engine = parser_engine or ""
        overrides = engine_overrides or {}
        logger.info(
            "Parsing file: %s, type: %s, engine: %s",
            file_name,
            file_type,
            engine or "builtin",
        )

        cls = self.registry.get_parser_class(engine, file_type)
        logger.info(
            "Creating %s parser instance for %s file",
            cls.__name__,
            file_type,
        )
        parser = cls(
            file_name=file_name,
            file_type=file_type,
            **overrides,
        )

        logger.info("Starting to parse file content, size: %d bytes", len(content))
        result = parser.parse(content)

        if not result.content:
            logger.warning("Parser returned empty content for file: %s", file_name)
        logger.info(
            "Parsed file %s, content length=%d", file_name, len(result.content)
        )
        return result

    def parse_url(
        self,
        url: str,
        title: str,
        parser_engine: Optional[str] = None,
        engine_overrides: Optional[dict[str, Any]] = None,
    ) -> Document:
        """Parse content from a URL to markdown."""
        logger.info("Parsing URL: %s, title: %s", url, title)

        parser = WebParser(title=title)
        logger.info("Starting to parse URL content")
        result = parser.parse(url.encode())

        if not result.content:
            logger.warning("Parser returned empty content for url: %s", url)
        logger.info("Parsed url %s, content length=%d", url, len(result.content))
        return result
