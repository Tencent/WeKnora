from docreader.models.document import Document
from docreader.config import CONFIG
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import FirstParser
from docreader.parser.concurrency import parser_worker_limit
from docreader.parser.markitdown_parser import MarkitdownParser

import io
import os
import base64
import logging

logger = logging.getLogger(__name__)


def _close_pdfium_resource(resource) -> None:
    close = getattr(resource, "close", None)
    if close:
        close()


def _normalize_image_quality(quality: int) -> int:
    return min(95, max(1, quality))


class PDFScannedParser(BaseParser):
    """Fallback parser for scanned PDFs.
    
    If the primary parser extracts no text (e.g. Markitdown on a scanned PDF),
    this parser converts each page into a JPEG image. The Go App will then
    perform OCR on the extracted images.
    """
    
    def parse_into_text(self, content: bytes) -> Document:
        import pypdfium2 as pdfium

        images = {}
        markdown_lines = []
        
        base_name = os.path.splitext(self.file_name or "document")[0]
        
        logger.info(
            "PDFScannedParser: Rendering PDF pages to JPEG images for %s",
            self.file_name,
        )
        
        try:
            with parser_worker_limit("pdf_render", CONFIG.pdf_render_max_workers):
                pdf = pdfium.PdfDocument(content)
                try:
                    page_count = len(pdf)
                    scale = max(1, CONFIG.pdf_render_dpi) / 72
                    quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)

                    for i in range(page_count):
                        page = pdf[i]
                        bitmap = None
                        try:
                            bitmap = page.render(scale=scale)
                            img_obj = bitmap.to_pil()
                            if img_obj.mode != "RGB":
                                img_obj = img_obj.convert("RGB")

                            img_byte_arr = io.BytesIO()
                            img_obj.save(
                                img_byte_arr,
                                format="JPEG",
                                quality=quality,
                                optimize=True,
                            )
                            img_bytes = img_byte_arr.getvalue()
                        finally:
                            _close_pdfium_resource(bitmap)
                            _close_pdfium_resource(page)

                        page_filename = f"{base_name}_page_{i+1}.jpg"
                        ref_path = f"images/{page_filename}"

                        markdown_lines.append(f"![{page_filename}]({ref_path})")
                        images[ref_path] = base64.b64encode(img_bytes).decode("utf-8")
                finally:
                    _close_pdfium_resource(pdf)
                    
            text = "\n\n".join(markdown_lines)
            return Document(
                content=text, 
                images=images, 
                metadata={
                    "image_source_type": "scanned_pdf",
                    "page_count": page_count
                }
            )
        except Exception as e:
            logger.exception("PDFScannedParser failed to parse PDF: %s", e)
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
