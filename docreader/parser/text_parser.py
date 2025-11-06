import logging
from .base_parser import BaseParser
from typing import Dict, Any, Tuple, Union

logger = logging.getLogger(__name__)


class TextParser(BaseParser):
    """
    Text document parser for processing plain text files.
    This parser handles text extraction and chunking from plain text documents.
    """

    def parse_into_text(self, content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
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
        text = self.decode_bytes(content)
        logger.info(
            f"Successfully parsed text document, extracted {len(text)} characters"
        )
        return text


if __name__ == "__main__":
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    logger.info("Running TextParser in standalone mode")

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
