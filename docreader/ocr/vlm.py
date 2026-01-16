import logging
from typing import Union

from openai import OpenAI
from PIL import Image

from docreader.config import CONFIG
from docreader.ocr.base import OCRBackend
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class VLMOCRBackend(OCRBackend):
    """VLM OCR backend implementation using OpenAI API format"""

    def __init__(self):
        """Initialize VLM OCR backend

        Args:
            api_key: API key for OpenAI API
            base_url: Base URL for OpenAI API
            model: Model name
        """
        self.model = CONFIG.ocr_model
        self.client = OpenAI(
            api_key=CONFIG.ocr_api_key,
            base_url=CONFIG.ocr_api_base_url,
            timeout=30,
        )
        self.temperature = 0.0
        self.max_tokens = 5000

        # Prompt for OCR text extraction with specific formatting requirements
        self.prompt = "提取文档图片中正文的所有信息用markdown格式表示，"
        "其中页眉、页脚部分忽略，"
        "表格用html格式表达，"
        "文档中公式用latex格式表示，"
        "按照阅读顺序组织进行解析。"

    def predict(self, image: Union[str, bytes, Image.Image]) -> str:
        """Extract text from an image using VLM OCR

        Args:
            image: Image file path, bytes, or PIL Image object

        Returns:
            Extracted text
        """
        if self.client is None:
            logger.error("VLM OCR client not initialized")
            return ""

        try:
            # Encode image to base64 format for API transmission
            img_base64 = endecode.decode_image(image)
            if not img_base64:
                return ""

            # Call VLM OCR API using OpenAI-compatible format
            logger.info(f"Calling VLM OCR API with model: {self.model}")
            response = self.client.chat.completions.create(
                model=self.model,
                messages=[
                    {
                        "role": "user",
                        "content": [
                            {
                                "type": "image_url",
                                "image_url": {
                                    "url": f"data:image/png;base64,{img_base64}"
                                },
                            },
                            {
                                "type": "text",
                                "text": self.prompt,
                            },
                        ],
                    }
                ],
                temperature=self.temperature,
                max_tokens=self.max_tokens,
            )
            return response.choices[0].message.content or ""
        except Exception as e:
            logger.error(f"VLM OCR prediction error: {str(e)}")
            return ""
