import io
import logging

from markitdown import MarkItDown

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.concurrency import parser_worker_limit
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.ppt_convert import normalize_ppt_bytes
from docreader.parser.pptx_media import (
    attach_pptx_media_to_markdown,
    markdown_needs_pptx_media_attach,
)

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
        ft = (ext or "").lstrip(".").lower()
        pptx_bytes: bytes | None = None
        if ft in ("ppt", "pptx"):
            content, ext = normalize_ppt_bytes(content, ft)
            pptx_bytes = content
            ft = "pptx"
        elif ext and not ext.startswith("."):
            ext = "." + ext

        with parser_worker_limit("markitdown", CONFIG.markitdown_max_workers):
            result = self._convert_markitdown(content, ext, keep_data_uris=True)
            if result is None:
                logger.warning(
                    "MarkItDown failed with embedded images for %s; retrying without data URIs",
                    ft or ext,
                )
                result = self._convert_markitdown(content, ext, keep_data_uris=False)

        text = result.text_content
        images: dict[str, str] = {}
        if pptx_bytes is not None and markdown_needs_pptx_media_attach(text):
            text, images = attach_pptx_media_to_markdown(text, pptx_bytes)
        return Document(content=text, images=images)

    def _convert_markitdown(self, content: bytes, ext: str | None, *, keep_data_uris: bool):
        try:
            return self.markitdown.convert(
                io.BytesIO(content),
                file_extension=ext,
                keep_data_uris=keep_data_uris,
            )
        except Exception:
            if keep_data_uris:
                return None
            raise


class MarkitdownParser(PipelineParser):
    _parser_cls = (StdMarkitdownParser, MarkdownParser)
