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

    def parse_into_text(self, content: bytes, file_extension: str = None) -> Document:
        """
        Modified to support explicit file_extension to fix Issue #544.
        If file_extension is not provided, we try to infer it or default to None.
        """
        try:
            # 核心修复点：传入 file_extension 参数
            # 如果调用方没传，markitdown 可能会报错，这里我们至少保证它有尝试的机会
            result = self.markitdown.convert(
                io.BytesIO(content),
                file_extension=file_extension,
                keep_data_uris=True
            )
            return Document(content=result.text_content)
        except Exception as e:
            logger.warning(f"Markitdown conversion failed: {e}. Fallback might be triggered.")
            # 必须抛出异常，这样外部的 FirstParser 才会切换到 DocxParser 这种备选方案
            raise e


class MarkitdownParser(PipelineParser):
    _parser_cls = (StdMarkitdownParser, MarkdownParser)
