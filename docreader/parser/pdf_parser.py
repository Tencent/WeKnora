from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import FirstParser
from docreader.parser.markitdown_parser import MarkitdownParser

import io
import os
import base64
import logging

logger = logging.getLogger(__name__)

class PDFScannedParser(BaseParser):
    """Fallback parser for scanned PDFs.
    
    If the primary parser extracts no text (e.g. Markitdown on a scanned PDF),
    this parser converts each page into an image. The Go App will then perform
    OCR on the extracted images.
    """
    
    def parse_into_text(self, content: bytes) -> Document:
        import pdfplumber
        images = {}
        markdown_lines = []
        
        base_name = os.path.splitext(self.file_name or "document")[0]
        
        logger.info("PDFScannedParser: Attempting to convert PDF pages to images for %s", self.file_name)
        
        try:
            with pdfplumber.open(io.BytesIO(content)) as pdf:
                for i, page in enumerate(pdf.pages):
                    img_obj = page.to_image(resolution=150).original
                    img_byte_arr = io.BytesIO()
                    img_obj.save(img_byte_arr, format="PNG")
                    img_bytes = img_byte_arr.getvalue()
                    
                    page_filename = f"{base_name}_page_{i+1}.png"
                    ref_path = f"images/{page_filename}"
                    
                    markdown_lines.append(f"![{page_filename}]({ref_path})")
                    images[ref_path] = base64.b64encode(img_bytes).decode("utf-8")
                    
            text = "\n\n".join(markdown_lines)
            return Document(
                content=text, 
                images=images, 
                metadata={
                    "image_source_type": "scanned_pdf",
                    "page_count": len(pdf.pages)
                }
            )
        except Exception as e:
            logger.exception("PDFScannedParser failed to parse PDF: %v", e)
            raise e

class PDFParser(FirstParser):
    """PDF Parser using chain of responsibility pattern
    
    Attempts to parse PDF files using multiple parser backends in order:
    1. MinerUParser - Primary parser for PDF documents (if enabled)
    2. MarkitdownParser - Fallback parser if MinerU fails
    3. PDFScannedParser - Final fallback for scanned PDFs
    
    The first successful parser result will be returned.
    """
    # Parser classes to try in order (chain of responsibility pattern)
    _parser_cls = (MarkitdownParser, PDFScannedParser)
