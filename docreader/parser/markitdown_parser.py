import io
import logging
import time

from markitdown import MarkItDown

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.concurrency import parser_worker_limit
from docreader.parser.docx_merge import fill_vertical_merged_cells_docx
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.ppt_convert import normalize_ppt_bytes
from docreader.parser.pptx_media import (
    attach_pptx_media_to_markdown,
    markdown_needs_pptx_media_attach,
)
from docreader.parser.xlsx_repair import (
    sanitize_xlsx_styles,
    strip_unreadable_ranges_xlsx,
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
        preprocess_started = time.monotonic()
        xlsx = ft in ("xlsx", "xlsm")
        if ft in ("ppt", "pptx"):
            content, ext = normalize_ppt_bytes(content, ft)
            pptx_bytes = content
            ft = "pptx"
        elif ft == "docx":
            content = fill_vertical_merged_cells_docx(content)
        elif xlsx:
            # MarkItDown reads spreadsheets through pandas/openpyxl, so the
            # ranges openpyxl cannot parse (#3599) and the non-conforming
            # styles.xml fills (#3637) that break the builtin ExcelParser break
            # this engine too. Strip the ranges first: the fill repair alone
            # would still leave the range TypeError. Both passes return None
            # for clean workbooks.
            readable = strip_unreadable_ranges_xlsx(content)
            if readable is not None:
                content = readable
            sanitized = sanitize_xlsx_styles(content)
            if sanitized is not None:
                content = sanitized
        if xlsx:
            logger.info(
                "MarkItDown XLSX preprocessing completed in %.3fs (bytes=%d)",
                time.monotonic() - preprocess_started,
                len(content),
            )
        if ext and not ext.startswith("."):
            ext = "." + ext

        # Embedded data URIs can make spreadsheet output and subsequent chunking
        # disproportionately expensive. XLSX conversion therefore starts with
        # them disabled; all other formats retain the historical image-first
        # behavior and its single bounded fallback.
        first_keep_data_uris = not xlsx
        with parser_worker_limit("markitdown", CONFIG.markitdown_max_workers):
            started = time.monotonic()
            try:
                result = self._convert_markitdown(
                    content, ext, keep_data_uris=first_keep_data_uris
                )
            except Exception as exc:
                logger.exception(
                    "MarkItDown conversion failed (format=%s bytes=%d keep_data_uris=%s)",
                    ft or ext,
                    len(content),
                    first_keep_data_uris,
                )
                raise RuntimeError(
                    f"MarkItDown conversion failed for {ft or ext or 'document'}"
                ) from exc
            logger.info(
                "MarkItDown conversion completed in %.3fs (format=%s bytes=%d text=%d keep_data_uris=%s)",
                time.monotonic() - started,
                ft or ext,
                len(content),
                len(result.text_content) if result is not None else 0,
                first_keep_data_uris,
            )
            if result is None and first_keep_data_uris:
                logger.warning(
                    "MarkItDown failed with embedded images for %s; retrying without data URIs",
                    ft or ext,
                )
                started = time.monotonic()
                try:
                    result = self._convert_markitdown(
                        content, ext, keep_data_uris=False
                    )
                except Exception as exc:
                    logger.exception(
                        "MarkItDown fallback failed (format=%s bytes=%d)",
                        ft or ext,
                        len(content),
                    )
                    raise RuntimeError(
                        f"MarkItDown conversion failed for {ft or ext or 'document'}"
                    ) from exc
                logger.info(
                    "MarkItDown fallback completed in %.3fs (format=%s bytes=%d text=%d keep_data_uris=false)",
                    time.monotonic() - started,
                    ft or ext,
                    len(content),
                    len(result.text_content) if result is not None else 0,
                )

        if result is None:
            raise RuntimeError(f"MarkItDown conversion failed for {ft or ext or 'document'}")
        text = result.text_content
        images: dict[str, str] = {}
        if pptx_bytes is not None and markdown_needs_pptx_media_attach(text):
            text, images = attach_pptx_media_to_markdown(text, pptx_bytes)
        return Document(content=text, images=images)

    def _convert_markitdown(
        self, content: bytes, ext: str | None, *, keep_data_uris: bool
    ):
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
