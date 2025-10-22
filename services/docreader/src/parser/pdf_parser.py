import logging
import os
from typing import Any, Tuple, Dict, Union
import re
import pymupdf4llm
import tempfile
from .base_parser import BaseParser
from PIL import Image
logger = logging.getLogger(__name__)

class PDFParser(BaseParser):
    """
    PDF Document Parser
    This parse handles PDF documents by pymupdf4llm.
    It can convert PDF docments to makedown,but it isn't scan pdf.
    """
    def parse_into_text(self,content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
        
        logger.info(f"Parsing PDF with pymupdf4llm, content size: {len(content)} bytes")
        temp_pdf = tempfile.NamedTemporaryFile(delete=False, suffix=".pdf")
        temp_pdf_path = temp_pdf.name
        ima_part = {}
        def replace_img(match):
            prefix = match.group(1)
            img_path = match.group(2)
            suffix = match.group(3)
            if img_path.startswith(('http://', 'https://')):
                    return match.group(0)
            
            if not os.path.exists(img_path):
                    logger.warning(f"警告：图片不存在，跳过: {img_path}")
            image_url = self.upload_file(img_path)
            ima_part[image_url] = Image.open(img_path).convert("RGBA")
            return f"{prefix}{image_url}{suffix}"
        try:
            temp_pdf.write(content)
            temp_pdf.close()
            logger.info(f"PDF content written to temporary file: {temp_pdf_path}")
            with tempfile.TemporaryDirectory() as temp_dir:
                md_text = pymupdf4llm.to_markdown(
                    doc=temp_pdf_path,
                    write_images=True,
                    table_strategy="lines_strict",
                    ignore_code=False,
                    image_path=temp_dir,
                    show_progress= True
                )
                logger.info(
                    f"Successfully extracted image for tempfile")
                img_pattern = r'(!\[.*?\]\()([^)\s]+)(\))'
                text = re.sub(img_pattern,replace_img,md_text)
            logger.info(f"PDF parsing complete.")
            return text,ima_part

        except Exception as e:
            logger.error(f"Parsing PDF with mineru is fail")
            return ""
        finally:
              # This block is GUARANTEED to execute, preventing resource leaks.
            if os.path.exists(temp_pdf_path):
                try:
                    os.remove(temp_pdf_path)
                    logging.info(f"Temporary file cleaned up: {temp_pdf_path}")
                except OSError as e:
                    logger.error(f"Error removing temporary file {temp_pdf_path}: {e}")



