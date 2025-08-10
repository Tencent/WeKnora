import os
import logging
import base64
from typing import Optional, Union, Dict, Any
from abc import ABC, abstractmethod
from PIL import Image
import io
import numpy as np
from .image_utils import image_to_base64

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)

class OCRBackend(ABC):
    """Base class for OCR backends"""
    
    @abstractmethod
    def predict(self, image: Union[str, bytes, Image.Image]) -> str:
        """Extract text from an image
        
        Args:
            image: Image file path, bytes, or PIL Image object
            
        Returns:
            Extracted text
        """
        pass

class PaddleOCRBackend(OCRBackend):
    """PaddleOCR backend implementation"""
    
    def __init__(self, **kwargs):
        """Initialize PaddleOCR backend"""
        self.ocr = None
        try:
            from paddleocr import PaddleOCR
            # Default OCR configuration
            ocr_config = {
                "use_doc_orientation_classify": False,  # Do not use document image orientation classification
                "use_doc_unwarping": False,  # Do not use document unwarping
                "use_textline_orientation": False,  # Do not use textline orientation classification
                "text_recognition_model_name": "PP-OCRv5_server_rec",
                "text_detection_model_name": "PP-OCRv5_server_det",
                "text_recognition_model_dir": "/root/.paddlex/official_models/PP-OCRv5_server_rec_infer",
                "text_detection_model_dir": "/root/.paddlex/official_models/PP-OCRv5_server_det_infer",
                "text_det_limit_type": "min",  # Limit by short side
                "text_det_limit_side_len": 736,  # Limit side length to 736
                "text_det_thresh": 0.3,  # Text detection pixel threshold
                "text_det_box_thresh": 0.6,  # Text detection box threshold
                "text_det_unclip_ratio": 1.5,  # Text detection expansion ratio
                "text_rec_score_thresh": 0.0,  # Text recognition confidence threshold
                "ocr_version": "PP-OCRv5",  # Switch to PP-OCRv4 here to compare
                "lang": "ch",
            }
            
            self.ocr = PaddleOCR(**ocr_config)
            logger.info("PaddleOCR engine initialized successfully")
        except ImportError:
            logger.error("Failed to import paddleocr. Please install it with 'pip install paddleocr'")
        except Exception as e:
            logger.error(f"Failed to initialize PaddleOCR: {str(e)}")
    
    def predict(self, image):
        """Perform OCR recognition on the image

        Args:
            image: Image object (PIL.Image or numpy array)

        Returns:
            Extracted text string
        """
        try:
            # Ensure image is in RGB format
            if hasattr(image, "convert") and image.mode != "RGBA":
                img_for_ocr = image.convert("RGBA")
                logger.info(f"Converted image from {image.mode} to RGB format")
            else:
                img_for_ocr = image

            # Convert to numpy array if not already
            if hasattr(img_for_ocr, "convert"):
                image_array = np.array(img_for_ocr)
            else:
                image_array = img_for_ocr

            ocr_result = self.ocr.predict(image_array)
            logger.info(f"ocr_result: {ocr_result}")

            # Extract text
            if ocr_result and any(ocr_result):
                ocr_text = ""
                for image_result in ocr_result:
                    ocr_text = ocr_text + " ".join(image_result["rec_texts"])
                text_length = len(ocr_text)
                if text_length > 0:
                    logger.info(f"OCR extracted {text_length} characters")
                    logger.info(
                        f"OCR text sample: {ocr_text[:100]}..."
                        if text_length > 100
                        else f"OCR text: {ocr_text}"
                    )
                    return ocr_text
                else:
                    logger.warning("OCR returned empty result")
            else:
                logger.warning("OCR did not return any result")
            return ""
        except Exception as e:
            logger.error(f"OCR recognition error: {str(e)}")
            return ""
class NanonetsOCRBackend(OCRBackend):
    """Nanonets OCR backend implementation using OpenAI API format"""
    
    def __init__(self, **kwargs):
        """Initialize Nanonets OCR backend
        
        Args:
            api_key: API key for OpenAI API
            base_url: Base URL for OpenAI API
            model: Model name
        """
        try:
            from openai import OpenAI
            self.api_key = kwargs.get("api_key", "123")
            self.base_url = kwargs.get("base_url", "http://localhost:8000/v1")
            self.model = kwargs.get("model", "nanonets/Nanonets-OCR-s")
            self.temperature = kwargs.get("temperature", 0.0)
            self.max_tokens = kwargs.get("max_tokens", 15000)
            
            self.client = OpenAI(api_key=self.api_key, base_url=self.base_url)
            self.prompt = """
## 任务说明

请从上传的文档中提取文字内容，严格按自然阅读顺序（从上到下，从左到右）输出，并遵循以下格式规范。

### 1. **文本处理**

* 按正常阅读顺序提取文字，语句流畅自然。

### 2. **表格**

* 所有表格统一转换为 **Markdown 表格格式**。
* 内容保持清晰、对齐整齐，便于阅读。

### 3. **公式**

* 所有公式转换为 **LaTeX 格式**，使用 `$$公式$$` 包裹。

### 4. **图片**

* 忽略图片信息

### 5. **链接**

* 不要猜测或补全不确定的链接地址。
"""
            logger.info(f"Nanonets OCR engine initialized with model: {self.model}")
        except ImportError:
            logger.error("Failed to import openai. Please install it with 'pip install openai'")
            self.client = None
        except Exception as e:
            logger.error(f"Failed to initialize Nanonets OCR: {str(e)}")
            self.client = None
    
    def predict(self, image: Union[str, bytes, Image.Image]) -> str:
        """Extract text from an image using Nanonets OCR
        
        Args:
            image: Image file path, bytes, or PIL Image object
            
        Returns:
            Extracted text
        """
        if self.client is None:
            logger.error("Nanonets OCR client not initialized")
            return ""
        
        try:
            # Encode image to base64
            img_base64 = image_to_base64(image)
            if not img_base64:
                return ""
            
            # Call Nanonets OCR API
            logger.info(f"Calling Nanonets OCR API with model: {self.model}")
            response = self.client.chat.completions.create(
                model=self.model,
                messages=[
                    {
                        "role": "user",
                        "content": [
                            {
                                "type": "image_url",
                                "image_url": {"url": f"data:image/png;base64,{img_base64}"},
                            },
                            {
                                "type": "text",
                                "text": self.prompt,
                            },
                        ],
                    }
                ],
                temperature=self.temperature,
                max_tokens=self.max_tokens
            )
            
            return response.choices[0].message.content
        except Exception as e:
            logger.error(f"Nanonets OCR prediction error: {str(e)}")
            return ""

class OCREngine:
    """OCR Engine factory class"""
    
    _instance = None
    
    @classmethod
    def get_instance(cls, backend_type="paddle", **kwargs) -> Optional[OCRBackend]:
        """Get OCR engine instance
        
        Args:
            backend_type: OCR backend type, one of: "paddle", "nanonets"
            **kwargs: Additional arguments for the backend
            
        Returns:
            OCR engine instance or None if initialization fails
        """
        if cls._instance is None:
            logger.info(f"Initializing OCR engine with backend: {backend_type}")
            
            if backend_type.lower() == "paddle":
                cls._instance = PaddleOCRBackend(**kwargs)
            elif backend_type.lower() == "nanonets":
                cls._instance = NanonetsOCRBackend(**kwargs)
            else:
                logger.error(f"Unknown OCR backend type: {backend_type}")
                return None
        
        return cls._instance
    
