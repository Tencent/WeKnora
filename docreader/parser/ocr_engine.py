import io
import logging
import os
import platform
import subprocess
from abc import ABC, abstractmethod
from typing import Dict, Union

import numpy as np
from openai import OpenAI
from PIL import Image

from docreader.utils import endecode

logger = logging.getLogger(__name__)


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


class DummyOCRBackend(OCRBackend):
    """Dummy OCR backend implementation"""

    def predict(self, image: Union[str, bytes, Image.Image]) -> str:
        logger.warning("Dummy OCR backend is used")
        return ""


class PaddleOCRBackend(OCRBackend):
    """PaddleOCR backend implementation"""

    def __init__(self):
        """Initialize PaddleOCR backend"""
        self.ocr = None
        try:
            import paddle

            # Set PaddlePaddle to use CPU and disable GPU
            os.environ["CUDA_VISIBLE_DEVICES"] = ""
            paddle.device.set_device("cpu")

            # Try to detect if CPU supports AVX instruction set
            # CPU가 AVX 명령어 세트를 지원하는지 감지 시도
            try:
                # Detect if CPU supports AVX
                # CPU가 AVX를 지원하는지 감지
                if platform.system() == "Linux":
                    try:
                        result = subprocess.run(
                            ["grep", "-o", "avx", "/proc/cpuinfo"],
                            capture_output=True,
                            text=True,
                            timeout=5,
                        )
                        has_avx = "avx" in result.stdout.lower()
                        if not has_avx:
                            logger.warning(
                                "CPU does not support AVX instructions, "
                                "using compatibility mode"
                            )
                            # Further restrict instruction set usage
                            # 명령어 세트 사용 추가 제한
                            os.environ["FLAGS_use_avx2"] = "0"
                            os.environ["FLAGS_use_avx"] = "1"
                    except (
                        subprocess.TimeoutExpired,
                        FileNotFoundError,
                        subprocess.SubprocessError,
                    ):
                        logger.warning(
                            "Could not detect AVX support, using compatibility mode"
                        )
                        os.environ["FLAGS_use_avx2"] = "0"
                        os.environ["FLAGS_use_avx"] = "1"
            except Exception as e:
                logger.warning(
                    f"Error detecting CPU capabilities: {e}, using compatibility mode"
                )
                os.environ["FLAGS_use_avx2"] = "0"
                os.environ["FLAGS_use_avx"] = "1"

            from paddleocr import PaddleOCR

            # OCR configuration with text orientation classification enabled
            ocr_config = {
                "use_gpu": False,
                "text_det_limit_type": "max",
                "text_det_limit_side_len": 960,
                "use_doc_orientation_classify": True,  # Enable document orientation classification / 문서 방향 분류 활성화
                "use_doc_unwarping": False,
                "use_textline_orientation": True,  # Enable text line orientation detection / 텍스트 라인 방향 감지 활성화
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
            logger.error(
                f"Failed to import paddleocr: {str(e)}. "
                "Please install it with 'pip install paddleocr'"
            )
        except OSError as e:
            if "Illegal instruction" in str(e) or "core dumped" in str(e):
                logger.error(
                    f"PaddlePaddle crashed due to CPU instruction set incompatibility:"
                    f"{e}"
                )
                logger.error(
                    "This happens when the CPU doesn't support AVX instructions. "
                    "Try install CPU-only version of PaddlePaddle, "
                    "or use a different OCR backend."
                )
            else:
                logger.error(
                    f"Failed to initialize PaddleOCR due to OS error: {str(e)}"
                )
        except Exception as e:
            logger.error(f"Failed to initialize PaddleOCR: {str(e)}")

    def predict(self, image: Union[str, bytes, Image.Image]) -> str:
        """Extract text from an image

        Args:
            image: Image file path, bytes, or PIL Image object

        Returns:
            Extracted text
        """
        if isinstance(image, str):
            image = Image.open(image)
        elif isinstance(image, bytes):
            image = Image.open(io.BytesIO(image))

        if not isinstance(image, Image.Image):
            raise TypeError("image must be a string, bytes, or PIL Image object")

        return self._predict(image)

    def _predict(self, image: Image.Image) -> str:
        """Perform OCR recognition on the image

        Args:
            image: Image object (PIL.Image or numpy array)

        Returns:
            Extracted text string
        """
        if self.ocr is None:
            logger.error("PaddleOCR engine not initialized")
            return ""
        try:
            # Ensure image is in RGB format
            if image.mode != "RGB":
                image = image.convert("RGB")

            # Convert to numpy array for PaddleOCR processing
            image_array = np.array(image)

            # Perform OCR recognition
            ocr_result = self.ocr.ocr(image_array, cls=False)

            # Extract and concatenate text from OCR results
            ocr_text = ""
            if ocr_result and ocr_result[0]:
                text = [
                    line[1][0] if line and len(line) >= 2 and line[1] else ""
                    for line in ocr_result[0]
                ]
                text = [t.strip() for t in text if t]
                ocr_text = " ".join(text)

            logger.info(f"OCR extracted {len(ocr_text)} characters")
            return ocr_text

        except Exception as e:
            logger.error(f"OCR recognition error: {str(e)}")
            return ""


class NanonetsOCRBackend(OCRBackend):
    """Nanonets OCR backend implementation using OpenAI API format"""

    def __init__(self):
        """Initialize Nanonets OCR backend

        Args:
            api_key: API key for OpenAI API
            base_url: Base URL for OpenAI API
            model: Model name
        """
        # Load configuration from environment variables
        base_url = os.getenv("OCR_API_BASE_URL", "http://localhost:8000/v1")
        api_key = os.getenv("OCR_API_KEY", "123")
        timeout = 30
        self.client = OpenAI(api_key=api_key, base_url=base_url, timeout=timeout)

        self.model = os.getenv("OCR_MODEL", "nanonets/Nanonets-OCR-s")
        logger.info(f"Nanonets OCR engine initialized with model: {self.model}")
        self.temperature = 0.0
        self.max_tokens = 15000
        # Prompt for OCR text extraction with specific formatting requirements
        self.prompt = """## 작업 설명

업로드된 문서에서 텍스트 내용을 추출하고, 자연스러운 읽기 순서(위에서 아래로, 왼쪽에서 오른쪽)를 엄격히 준수하며 다음 형식 사양을 따르십시오.

### 1. **텍스트 처리**

* 정상적인 읽기 순서에 따라 텍스트를 추출하고 문장이 자연스럽고 매끄럽게 이어지도록 합니다.

### 2. **표**

* 모든 표는 **Markdown 표 형식**으로 통일하여 변환합니다.
* 내용이 명확하고 정렬이 잘 되어 있어 읽기 편하도록 유지합니다.

### 3. **수식**

* 모든 수식은 **LaTeX 형식**으로 변환하고 `$$수식$$`으로 감쌉니다.

### 4. **이미지**

* 이미지 정보는 무시합니다.

### 5. **링크**

* 불확실한 링크 주소를 추측하거나 완성하지 마십시오.
"""

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
            # Encode image to base64 format for API transmission
            img_base64 = endecode.decode_image(image)
            if not img_base64:
                return ""

            # Call Nanonets OCR API using OpenAI-compatible format
            logger.info(f"Calling Nanonets OCR API with model: {self.model}")
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
            logger.error(f"Nanonets OCR prediction error: {str(e)}")
            return ""


class OCREngine:
    """OCR Engine factory class for managing different OCR backend instances"""

    # Singleton pattern: cache instances for each backend type
    _instance: Dict[str, OCRBackend] = {}

    @classmethod
    def get_instance(cls, backend_type: str) -> OCRBackend:
        """Get OCR engine instance using factory pattern

        Args:
            backend_type: OCR backend type, one of: "paddle", "nanonets"
            **kwargs: Additional arguments for the backend

        Returns:
            OCR engine instance or None if initialization fails
        """
        backend_type = backend_type.lower()
        # Return cached instance if already initialized
        if cls._instance.get(backend_type):
            return cls._instance[backend_type]

        logger.info(f"Initializing OCR engine with backend: {backend_type}")

        if backend_type == "paddle":
            cls._instance[backend_type] = PaddleOCRBackend()

        elif backend_type == "nanonets":
            cls._instance[backend_type] = NanonetsOCRBackend()

        else:
            cls._instance[backend_type] = DummyOCRBackend()

        return cls._instance[backend_type]
