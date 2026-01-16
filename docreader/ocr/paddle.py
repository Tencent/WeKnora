import io
import logging
import os
import platform
import subprocess
from typing import Union

import numpy as np
from PIL import Image

from docreader.ocr.base import OCRBackend

logger = logging.getLogger(__name__)


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
            # 尝试检测CPU是否支持AVX指令集
            try:
                # Detect if CPU supports AVX
                # 检测CPU是否支持AVX
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
                            # 进一步限制指令集使用
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
                "use_doc_orientation_classify": True,  # Enable document orientation classification / 启用文档方向分类
                "use_doc_unwarping": False,
                "use_textline_orientation": True,  # Enable text line orientation detection / 启用文本行方向检测
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
