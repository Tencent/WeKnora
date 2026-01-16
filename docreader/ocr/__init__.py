import logging
import threading
from typing import Dict

from docreader.ocr.base import DummyOCRBackend, OCRBackend
from docreader.ocr.paddle import PaddleOCRBackend
from docreader.ocr.vlm import VLMOCRBackend

logger = logging.getLogger(__name__)


class OCREngine:
    """OCR Engine factory class for managing different OCR backend instances"""

    _instances: Dict[str, OCRBackend] = {}
    _lock = threading.Lock()

    @classmethod
    def get_instance(cls, backend_type: str) -> OCRBackend:
        backend_type = (backend_type or "dummy").lower()

        with cls._lock:
            inst = cls._instances.get(backend_type)
            if inst is not None:
                return inst

            logger.info(f"Creating OCR engine instance for backend: {backend_type}")

            if backend_type == "paddle":
                inst = PaddleOCRBackend()
            elif backend_type == "vlm":
                inst = VLMOCRBackend()
            else:
                inst = DummyOCRBackend()

            cls._instances[backend_type] = inst
            return inst
