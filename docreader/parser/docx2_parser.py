import logging

from docreader.parser.chain_parser import FirstParser
from docreader.parser.docx_parser import DocxParser
from docreader.parser.markitdown_parser import MarkitdownParser

logger = logging.getLogger(__name__)


class Docx2Parser(FirstParser):
    _parser_cls = (MarkitdownParser, DocxParser)


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_file = "/path/to/your/file.docx"
    parser = Docx2Parser(separators=[".", "?", "!", "。", "？", "！"])
    with open(your_file, "rb") as f:
        content = f.read()

        document = parser.parse(content)
        for cc in document.chunks:
            logger.info(f"chunk: {cc}")

        # document = parser.parse_into_text(content)
        # logger.info(f"docx content: {document.content}")
        # logger.info(f"find images {document.images.keys()}")
