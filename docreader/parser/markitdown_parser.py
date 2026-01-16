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
    Standard MarkItDown Parser Wrapper

    This parser uses the markitdown library to convert various document formats
    (docx, pptx, pdf, etc.) into text/markdown.
    """

    def __init__(self, *args, **kwargs):
        # 这里的 super() 会调用 BaseParser 的初始化，确保 self.file_type 被正确赋值
        super().__init__(*args, **kwargs)
        self.markitdown = MarkItDown()

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parses content using MarkItDown.
        Uses self.file_type (inherited from BaseParser) to hint the stream format.
        """
        ext = self.file_type
        if ext and not ext.startswith('.'):
            ext = '.' + ext

        # 直接调用 convert，移除 try-catch，让异常由上层 PipelineParser 统一捕获
        result = self.markitdown.convert(
            io.BytesIO(content),
            file_extension=ext,
            keep_data_uris=True
        )
        return Document(content=result.text_content)


class MarkitdownParser(PipelineParser):
    _parser_cls = (StdMarkitdownParser, MarkdownParser)