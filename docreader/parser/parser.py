import logging
from typing import Any, Optional

from docreader.models.document import Document
from docreader.parser.registry import registry
from docreader.parser.web_parser import WebParser

logger = logging.getLogger(__name__)


# OLE Compound File magic used by legacy binary Microsoft Office files.
# Some WPS/Word documents keep this payload while being renamed to .docx.
_OLE_COMPOUND_FILE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"


def detect_effective_file_type(file_type: str, content: bytes) -> str:
    """Return the parser file type after checking trustworthy file magic.

    OOXML ``.docx`` files are ZIP containers, while legacy ``.doc`` files
    use the OLE Compound File format.  Word and WPS tolerate a legacy file
    renamed to ``.docx``, so route that well-known mismatch through the DOC
    parser instead of feeding binary OLE data to the DOCX parser.
    """
    normalized = file_type.lower().lstrip(".")
    if normalized == "docx" and content.startswith(_OLE_COMPOUND_FILE_MAGIC):
        logger.warning(
            "Detected legacy DOC content with a DOCX extension; using DOC parser"
        )
        return "doc"
    return normalized


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

        effective_file_type = detect_effective_file_type(file_type, content)
        cls = self.registry.get_parser_class(engine, effective_file_type)
        logger.info(
            "Creating %s parser instance for %s file",
            cls.__name__,
            effective_file_type,
        )
        parser = cls(
            file_name=file_name,
            file_type=effective_file_type,
            **overrides,
        )

        logger.info("Starting to parse file content, size: %d bytes", len(content))
        result = parser.parse(content)

        if not result.content:
            logger.warning("Parser returned empty content for file: %s", file_name)
        logger.info("Parsed file %s, content length=%d", file_name, len(result.content))
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
