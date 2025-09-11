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
            import os
            import paddle
            
            # Set PaddlePaddle to use CPU and disable GPU
            os.environ['CUDA_VISIBLE_DEVICES'] = ''
            paddle.set_device('cpu')
            
            # 尝试检测CPU是否支持AVX指令集
            try:
                import subprocess
                import platform
                
                # 检测CPU是否支持AVX
                if platform.system() == "Linux":
                    try:
                        result = subprocess.run(['grep', '-o', 'avx', '/proc/cpuinfo'], 
                                              capture_output=True, text=True, timeout=5)
                        has_avx = 'avx' in result.stdout.lower()
                        if not has_avx:
                            logger.warning("CPU does not support AVX instructions, using compatibility mode")
                            # 进一步限制指令集使用
                            os.environ['FLAGS_use_avx2'] = '0'
                            os.environ['FLAGS_use_avx'] = '1'
                    except (subprocess.TimeoutExpired, FileNotFoundError, subprocess.SubprocessError):
                        logger.warning("Could not detect AVX support, using compatibility mode")
                        os.environ['FLAGS_use_avx2'] = '0'
                        os.environ['FLAGS_use_avx'] = '1'
            except Exception as e:
                logger.warning(f"Error detecting CPU capabilities: {e}, using compatibility mode")
                os.environ['FLAGS_use_avx2'] = '0'
                os.environ['FLAGS_use_avx'] = '1'
            
            from paddleocr import PaddleOCR
            # OCR configuration with text orientation classification enabled
            ocr_config = {
                "use_gpu": False,
                "text_det_limit_type": "max",
                "text_det_limit_side_len": 960,
                "use_doc_orientation_classify": True,  # 启用文档方向分类
                "use_doc_unwarping": False,
                "use_textline_orientation": True,  # 启用文本行方向检测
                "text_recognition_model_name": "PP-OCRv4_server_rec",
                "text_detection_model_name": "PP-OCRv4_server_det",
                "text_det_thresh": 0.3,
                "text_det_box_thresh": 0.6,
                "text_det_unclip_ratio": 1.5,
                "text_rec_score_thresh": 0.0,
                "ocr_version": "PP-OCRv4",
                "lang": "ch",
                "show_log": False,
                "use_dilation": True,  # improves accuracy
                "det_db_score_mode": "slow",  # improves accuracy
            }
            
            self.ocr = PaddleOCR(**ocr_config)
            logger.info("PaddleOCR engine initialized successfully")
            
        except ImportError as e:
            logger.error(f"Failed to import paddleocr: {str(e)}. Please install it with 'pip install paddleocr'")
        except OSError as e:
            if "Illegal instruction" in str(e) or "core dumped" in str(e):
                logger.error(f"PaddlePaddle crashed due to CPU instruction set incompatibility: {str(e)}")
                logger.error("This usually happens when the CPU doesn't support AVX instructions.")
                logger.error("Please try installing a CPU-only version of PaddlePaddle or use a different OCR backend.")
            else:
                logger.error(f"Failed to initialize PaddleOCR due to OS error: {str(e)}")
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
            if hasattr(image, "convert") and image.mode != "RGB":
                image = image.convert("RGB")

            # Convert to numpy array if needed
            if hasattr(image, "convert"):
                image_array = np.array(image)
            else:
                image_array = image

            # Perform OCR
            ocr_result = self.ocr.ocr(image_array, cls=False)
   
            # Extract text
            ocr_text = ""
            if ocr_result and ocr_result[0]:
                for line in ocr_result[0]:
                    if line and len(line) >= 2:
                        text = line[1][0] if line[1] else ""
                        if text:
                            ocr_text += text + " "
            
            text_length = len(ocr_text.strip())
            if text_length > 0:
                logger.info(f"OCR extracted {text_length} characters")
                return ocr_text.strip()
            else:
                logger.warning("OCR returned empty result")
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
    
