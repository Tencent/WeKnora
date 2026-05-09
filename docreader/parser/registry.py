import logging
from typing import Any, Callable, Dict, List, Optional, Tuple, Type

from docreader.parser.base_parser import BaseParser
from docreader.parser.doc_parser import DocParser
from docreader.parser.docx2_parser import Docx2Parser
from docreader.parser.excel_parser import ExcelParser
from docreader.parser.image_parser import ImageParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.office_hybrid_parser import DocxHybridParser, PptxHybridParser, find_soffice
from docreader.parser.pdf_parser import PDFHybridParser, PDFParser

logger = logging.getLogger(__name__)

BUILTIN_ENGINE = "builtin"


class ParserEngineRegistry:
    """Registry for parser engines.

    Each engine maps file extensions to parser classes.
    When a requested engine doesn't support a file type, the registry
    falls back to the builtin engine automatically.
    """

    def __init__(self):
        self._engines: Dict[str, Dict[str, Type[BaseParser]]] = {}
        self._descriptions: Dict[str, str] = {}
        self._check_available: Dict[str, Callable[..., Tuple[bool, str]]] = {}
        self._unavailable_hint: Dict[str, str] = {}

    def register(
        self,
        name: str,
        file_types: Dict[str, Type[BaseParser]],
        description: str = "",
        check_available: Callable[..., Tuple[bool, str]] | None = None,
        unavailable_hint: str = "",
    ):
        self._engines[name] = file_types
        self._descriptions[name] = description
        if check_available is not None:
            self._check_available[name] = check_available
            self._unavailable_hint[name] = unavailable_hint
        logger.info(
            "Registered parser engine '%s' with file types: %s",
            name,
            ", ".join(file_types.keys()),
        )

    def get_parser_class(self, engine: str, file_type: str) -> Type[BaseParser]:
        """Resolve parser class for the given engine and file type.

        Falls back to builtin engine when the requested engine doesn't
        support the file type.
        """
        ft = file_type.lower()

        if engine and engine in self._engines:
            cls = self._engines[engine].get(ft)
            if cls:
                logger.info("Using engine '%s' for file type '%s'", engine, ft)
                return cls
            logger.info(
                "Engine '%s' does not support '%s', falling back to builtin",
                engine,
                ft,
            )

        builtin = self._engines.get(BUILTIN_ENGINE, {})
        cls = builtin.get(ft)
        if cls:
            return cls

        raise ValueError(f"Unsupported file type: {file_type}")

    def list_engines(self, overrides: Optional[Dict[str, str]] = None) -> List[Dict]:
        """Return metadata for all registered engines, including availability.

        Args:
            overrides: tenant-level config overrides (e.g. mineru_endpoint, mineru_api_key)
                       forwarded to each engine's check_available function.
        """
        result = []
        for name, parsers in self._engines.items():
            available = True
            unavailable_reason = ""
            check = self._check_available.get(name)
            if check is not None:
                try:
                    available, unavailable_reason = check(overrides)
                except Exception as e:
                    available = False
                    unavailable_reason = str(e) or self._unavailable_hint.get(name, "")
            if not available and not unavailable_reason:
                unavailable_reason = self._unavailable_hint.get(name, "不可用")
            result.append(
                {
                    "name": name,
                    "description": self._descriptions.get(name, ""),
                    "file_types": sorted(parsers.keys()),
                    "available": available,
                    "unavailable_reason": unavailable_reason,
                }
            )
        return result

    def get_engine_names(self) -> List[str]:
        return list(self._engines.keys())


def _build_default_registry() -> ParserEngineRegistry:
    """Create and populate the default registry with all known engines."""
    reg = ParserEngineRegistry()

    _image_types = {
        ext: ImageParser for ext in ("jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp")
    }

    reg.register(
        BUILTIN_ENGINE,
        {
            "docx": Docx2Parser,
            "doc": DocParser,
            "pdf": PDFParser,
            "md": MarkdownParser,
            "markdown": MarkdownParser,
            "xlsx": ExcelParser,
            "xls": ExcelParser,
            **_image_types,
        },
        description="内置解析引擎",
    )

    reg.register(
        "markitdown",
        {
            "md": MarkitdownParser,
            "markdown": MarkitdownParser,
            "pdf": MarkitdownParser,
            "docx": MarkitdownParser,
            "doc": MarkitdownParser,
            "pptx": MarkitdownParser,
            "ppt": MarkitdownParser,
            "xlsx": MarkitdownParser,
            "xls": MarkitdownParser,
            "csv": MarkitdownParser,
        },
        description="MarkItDown 解析引擎（微软 MarkItDown 库）",
    )

    reg.register(
        "pdf_hybrid",
        {
            "pdf": PDFHybridParser,
        },
        description="PDF 混合解析引擎（文本提取 + 全页渲染）：MarkItDown 提取文字结构，PDFScannedParser 渲染每页为图片，两者合并。适合含图表的大文件 PDF，不依赖 MinerU。",
    )

    def _check_libreoffice(_overrides=None):
        if find_soffice():
            return True, ""
        return False, "LibreOffice (soffice) 未安装或不在 PATH 中"

    reg.register(
        "docx_hybrid",
        {
            "docx": DocxHybridParser,
            "doc":  DocxHybridParser,
        },
        description="Word 混合解析引擎（文本提取 + LibreOffice 全页渲染）：MarkItDown 提取文字，LibreOffice 将每页渲染为图片供 VLM 处理。适合含图表/SmartArt 的大文件 Word 文档。",
        check_available=_check_libreoffice,
        unavailable_hint="需要安装 LibreOffice",
    )

    reg.register(
        "pptx_hybrid",
        {
            "pptx": PptxHybridParser,
            "ppt":  PptxHybridParser,
        },
        description="PPT 混合解析引擎（文本提取 + LibreOffice 幻灯片渲染）：MarkItDown 提取文字，LibreOffice 将每张幻灯片渲染为图片供 VLM 处理。幻灯片天然是视觉格式，推荐所有 pptx/ppt 文件使用。",
        check_available=_check_libreoffice,
        unavailable_hint="需要安装 LibreOffice",
    )

    # NOTE: Engine listing is managed by Go-side engine registry
    # (docparser.ListAllEngines). The Python list_engines method is kept for
    # backward compatibility with the gRPC ListEngines RPC but the Go app
    # no longer calls it. MinerU engines are handled natively by Go.

    return reg


registry = _build_default_registry()
