import logging
from abc import ABC, abstractmethod
from typing import Union

from PIL import Image

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
