import logging
from typing import List, Tuple, Type

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class FirstParser(BaseParser):
    _parser_cls: Tuple[Type["BaseParser"], ...] = ()

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        self._parsers: List[BaseParser] = []
        for parser_cls in self._parser_cls:
            try:
                parser = parser_cls(*args, **kwargs)
                self._parsers.append(parser)
            except Exception as e:
                logger.error(f"Failed to create parser {parser_cls.__name__}: {e}")

    def parse_into_text(self, content: bytes) -> Document:
        for p in self._parsers:
            document = p.parse_into_text(content)
            if document.is_valid():
                return document
        return Document()

    @classmethod
    def create(cls, *parser_classes: Type["BaseParser"]) -> Type["FirstParser"]:
        names = "_".join([p.__name__ for p in parser_classes])
        return type(f"FirstParser_{names}", (cls,), {"_parser_cls": parser_classes})


class PipelineParser(BaseParser):
    _parser_cls: Tuple[Type["BaseParser"], ...] = ()

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        self._parsers: List[BaseParser] = []
        for parser_cls in self._parser_cls:
            try:
                parser = parser_cls(*args, **kwargs)
                self._parsers.append(parser)
            except Exception as e:
                logger.error(f"Failed to create parser {parser_cls.__name__}: {e}")

    def parse_into_text(self, content: bytes) -> Document:
        document = Document()
        for p in self._parsers:
            document = p.parse_into_text(content)
            content = endecode.encode_bytes(document.content)
        return document

    @classmethod
    def create(cls, *parser_classes: Type["BaseParser"]) -> Type["PipelineParser"]:
        names = "_".join([p.__name__ for p in parser_classes])
        return type(f"PipelineParser_{names}", (cls,), {"_parser_cls": parser_classes})


if __name__ == "__main__":
    from docreader.parser.markdown_parser import MarkdownParser

    cls = FirstParser.create(MarkdownParser)
    parser = cls()
    print(parser.parse_into_text(b"aaa"))
