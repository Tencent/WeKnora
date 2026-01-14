"""
Chain Parser Module

This module provides two chain-of-responsibility pattern implementations for document parsing:
1. FirstParser: Tries multiple parsers sequentially until one succeeds
2. PipelineParser: Chains parsers where each parser processes the output of the previous one
"""

import logging
from typing import Dict, List, Tuple, Type

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.utils import endecode

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


class FirstParser(BaseParser):
    """
    First-success parser that tries multiple parsers in sequence.

    This parser attempts to parse content using each registered parser in order.
    It returns the result from the first parser that successfully produces a valid document.
    If all parsers fail, it returns an empty Document.

    Usage:
        # Create a custom FirstParser with specific parser classes
        CustomParser = FirstParser.create(MarkdownParser, HTMLParser)
        parser = CustomParser()
        document = parser.parse_into_text(content_bytes)
    """

    # Tuple of parser classes to be instantiated
    _parser_cls: Tuple[Type["BaseParser"], ...] = ()

    def __init__(self, *args, **kwargs):
        """Initialize FirstParser with configured parser classes."""
        super().__init__(*args, **kwargs)

        # Instantiate all parser classes into parser instances
        self._parsers: List[BaseParser] = []
        for parser_cls in self._parser_cls:
            parser = parser_cls(*args, **kwargs)
            self._parsers.append(parser)

    def parse_into_text(self, content: bytes) -> Document:
        """Parse content using the first parser that succeeds.

        Args:
            content: Raw bytes content to be parsed

        Returns:
            Document: Parsed document from the first successful parser,
                     or an empty Document if all parsers fail
        """
        for p in self._parsers:
            logger.info(f"FirstParser: using parser {p.__class__.__name__}")
            try:
                document = p.parse_into_text(content)
            except Exception:
                logger.exception(
                    "FirstParser: parser %s raised exception; trying next parser",
                    p.__class__.__name__,
                )
                continue

            if document.is_valid():
                logger.info(f"FirstParser: parser {p.__class__.__name__} succeeded")
                return document
        return Document()

    @classmethod
    def create(cls, *parser_classes: Type["BaseParser"]) -> Type["FirstParser"]:
        """Factory method to create a FirstParser subclass with specific parsers.

        Args:
            *parser_classes: Variable number of BaseParser subclasses to try in order

        Returns:
            Type[FirstParser]: A new FirstParser subclass configured with the given parsers

        Example:
            CustomParser = FirstParser.create(MarkdownParser, HTMLParser)
            parser = CustomParser()
        """
        # Generate a descriptive class name based on parser names
        names = "_".join([p.__name__ for p in parser_classes])
        # Dynamically create a new class with the parser configuration
        return type(f"FirstParser_{names}", (cls,), {"_parser_cls": parser_classes})


class PipelineParser(BaseParser):
    """
    Pipeline parser that chains multiple parsers sequentially.

    This parser processes content through a series of parsers where each parser
    receives the output of the previous parser as input. Images from all parsers
    are accumulated and merged into the final document.

    Usage:
        # Create a custom PipelineParser with specific parser classes
        CustomParser = PipelineParser.create(PreParser, MarkdownParser, PostParser)
        parser = CustomParser()
        document = parser.parse_into_text(content_bytes)
    """

    # Tuple of parser classes to be instantiated and chained
    _parser_cls: Tuple[Type["BaseParser"], ...] = ()

    def __init__(self, *args, **kwargs):
        """Initialize PipelineParser with configured parser classes."""
        super().__init__(*args, **kwargs)

        # Instantiate all parser classes into parser instances
        self._parsers: List[BaseParser] = []
        for parser_cls in self._parser_cls:
            parser = parser_cls(*args, **kwargs)
            self._parsers.append(parser)

    def parse_into_text(self, content: bytes) -> Document:
        """Parse content through a pipeline of parsers.

        Each parser in the pipeline processes the output of the previous parser.
        Images from all parsers are accumulated and merged into the final document.

        Args:
            content: Raw bytes content to be parsed

        Returns:
            Document: Final document after processing through all parsers,
                     with accumulated images from all stages
        """
        # Accumulate images from all parsers
        images: Dict[str, str] = {}
        document = Document()
        for p in self._parsers:
            logger.info(f"PipelineParser: using parser {p.__class__.__name__}")
            # Parse content with current parser
            document = p.parse_into_text(content)
            # Convert document content back to bytes for next parser
            content = endecode.encode_bytes(document.content)
            # Accumulate images from this parser
            images.update(document.images)
        # Merge all accumulated images into final document
        document.images.update(images)
        return document

    @classmethod
    def create(cls, *parser_classes: Type["BaseParser"]) -> Type["PipelineParser"]:
        """Factory method to create a PipelineParser subclass with specific parsers.

        Args:
            *parser_classes: Variable number of BaseParser subclasses to chain in order

        Returns:
            Type[PipelineParser]: A new PipelineParser subclass configured with the given parsers

        Example:
            CustomParser = PipelineParser.create(PreprocessParser, MarkdownParser)
            parser = CustomParser()
        """
        # Generate a descriptive class name based on parser names
        names = "_".join([p.__name__ for p in parser_classes])
        # Dynamically create a new class with the parser configuration
        return type(f"PipelineParser_{names}", (cls,), {"_parser_cls": parser_classes})


if __name__ == "__main__":
    from docreader.parser.markdown_parser import MarkdownParser

    # Example: Create and use a FirstParser with MarkdownParser
    FpCls = FirstParser.create(MarkdownParser)
    lparser = FpCls()
    print(lparser.parse_into_text(b"aaa"))
