import logging

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class TextParser(BaseParser):
    """
    Text document parser for processing plain text files.
    This parser handles text extraction and chunking from plain text documents.
    """

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse text document content by decoding bytes to string.

        This is a straightforward parser that simply converts the binary content
        to text using appropriate character encoding.

        Args:
            content: Raw document content as bytes

        Returns:
            Parsed text content as string
        """
        logger.info(f"Parsing text document, content size: {len(content)} bytes")
        text = endecode.decode_bytes(content)
        logger.info(
            f"Successfully parsed text document, extracted {len(text)} characters"
        )
        return Document(content=text)


if __name__ == "__main__":
    logger = logging.getLogger(__name__)

    # Sample text for testing
    text = """## 标题1
    ![alt text](image.png)
    ## 标题2
    ![alt text](image2.png)
    ## 标题3
    ![alt text](image3.png)"""
    logger.info(f"Test text content: {text}")

    # Define separators for text splitting
    seperators = ["\n\n", "\n", "。"]
    parser = TextParser(separators=seperators)
    logger.info("Splitting text into units")
    units = parser._split_into_units(text)
    logger.info(f"Split text into {len(units)} units")
    logger.info(f"Units: {units}")
