import io
import logging

from markitdown import MarkItDown

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownParser

logger = logging.getLogger(__name__)


class StdMarkitdownParser(BaseParser):
    """
    PDF Document Parser

    This parser handles PDF documents by extracting text content.
    It uses the markitdown library for simple text extraction.
    """

    def __init__(self, *args, **kwargs):
        self.markitdown = MarkItDown()

    def parse_into_text(self, content: bytes) -> Document:
        result = self.markitdown.convert(io.BytesIO(content), keep_data_uris=True)
        return Document(content=result.text_content)


class MarkitdownParser(PipelineParser):
    _parser_cls = (StdMarkitdownParser, MarkdownParser)
