import logging
from typing import Callable, Dict, List, Optional, Tuple, Type

from docreader.parser.base_parser import BaseParser
from docreader.parser.doc_parser import DocParser
from docreader.parser.docx2_parser import Docx2Parser
from docreader.parser.epub_parser import EPUBParser
from docreader.parser.excel_parser import ExcelParser
from docreader.parser.html_parser import HTMLParser
from docreader.parser.image_parser import ImageParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.mhtml_parser import MHTMLParser
from docreader.parser.opendataloader_parser import (
    OpenDataLoaderParser,
    opendataloader_available,
)
from docreader.parser.pdf_parser import PDFParser
from docreader.parser.xmind_parser import XMindParser

logger = logging.getLogger(__name__)

BUILTIN_ENGINE = "builtin"

# File types that builtin does not implement natively. When the requested
# engine (including empty / builtin) cannot parse them, route to this engine
# instead of raising ValueError — PPT/PPTX/CSV are markitdown-only.
_DEFAULT_ENGINE_BY_TYPE = {
    "ppt": "markitdown",
    "pptx": "markitdown",
    "csv": "markitdown",
}


class ParserEngineRegistry:
    """Registry for parser engines.

    Each engine maps file extensions to parser classes.
    When a requested engine doesn't support a file type, the registry
    tries a type-level default engine, then builtin.
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

        Fallback order when the requested engine is empty or does not
        support the type: type-level default (e.g. ppt → markitdown),
        then builtin. Raises an actionable error listing engines that
        do support the type.
        """
        ft = file_type.lower().lstrip(".")

        if engine and engine in self._engines:
            cls = self._engines[engine].get(ft)
            if cls:
                logger.info("Using engine '%s' for file type '%s'", engine, ft)
                return cls
            logger.info(
                "Engine '%s' does not support '%s', falling back",
                engine,
                ft,
            )

        default_engine = _DEFAULT_ENGINE_BY_TYPE.get(ft)
        if default_engine:
            cls = self._engines.get(default_engine, {}).get(ft)
            if cls:
                logger.info(
                    "Using default engine '%s' for file type '%s'",
                    default_engine,
                    ft,
                )
                return cls

        builtin = self._engines.get(BUILTIN_ENGINE, {})
        cls = builtin.get(ft)
        if cls:
            return cls

        supported = sorted(
            name for name, parsers in self._engines.items() if ft in parsers
        )
        engine_label = engine or BUILTIN_ENGINE
        if supported:
            raise ValueError(
                f"Unsupported file type {file_type!r} for engine {engine_label!r}. "
                f"Configure one of: {', '.join(supported)}"
            )
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
            "pptx": MarkitdownParser,
            "ppt": MarkitdownParser,
            "epub": EPUBParser,
            "html": HTMLParser,
            "htm": HTMLParser,
            "mhtml": MHTMLParser,
            "xmind": XMindParser,
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
        "opendataloader",
        {"pdf": OpenDataLoaderParser},
        description="OpenDataLoader PDF（版面分析，需 Java 11+）",
        check_available=lambda overrides: opendataloader_available(
            overrides, quick=True
        ),
        unavailable_hint="请安装 opendataloader-pdf 与 Java 11+",
    )

    # NOTE: Engine listing is managed by Go-side engine registry
    # (docparser.ListAllEngines). The Python list_engines method is kept for
    # backward compatibility with the gRPC ListEngines RPC but the Go app
    # no longer calls it. MinerU engines are handled natively by Go.

    return reg


registry = _build_default_registry()
