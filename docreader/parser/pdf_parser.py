from docreader.parser.chain_parser import FirstParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.mineru_parser import MinerUParser


class PDFParser(FirstParser):
    _parser_cls = (MinerUParser, MarkitdownParser)
