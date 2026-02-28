from docreader.parser.chain_parser import FirstParser
from docreader.parser.markitdown_parser import MarkitdownParser


class PDFParser(FirstParser):
    """PDF Parser using chain of responsibility pattern
    
    Attempts to parse PDF files using multiple parser backends in order:
    1. MinerUParser - Primary parser for PDF documents
    2. MarkitdownParser - Fallback parser if MinerU fails
    
    The first successful parser result will be returned.
    """
    # Parser classes to try in order (chain of responsibility pattern)
    _parser_cls = (MarkitdownParser,)
