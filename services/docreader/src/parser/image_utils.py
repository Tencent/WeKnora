import base64
import io
import logging
from typing import Union
from PIL import Image
import numpy as np

logger = logging.getLogger(__name__)

def image_to_base64(image: Union[str, bytes, Image.Image, np.ndarray]) -> str:
    """Convert image to base64 encoded string
    
    Args:
        image: Image file path, bytes, PIL Image object, or numpy array
        
    Returns:
        Base64 encoded image string, or empty string if conversion fails
    """
    try:
        if isinstance(image, str):
            # It's a file path
            with open(image, "rb") as image_file:
                return base64.b64encode(image_file.read()).decode("utf-8")
        elif isinstance(image, bytes):
            # It's bytes data
            return base64.b64encode(image).decode("utf-8")
        elif isinstance(image, Image.Image):
            # It's a PIL Image
            buffer = io.BytesIO()
            image.save(buffer, format="PNG")
            return base64.b64encode(buffer.getvalue()).decode("utf-8")
        elif isinstance(image, np.ndarray):
            # It's a numpy array
            pil_image = Image.fromarray(image)
            buffer = io.BytesIO()
            pil_image.save(buffer, format="PNG")
            return base64.b64encode(buffer.getvalue()).decode("utf-8")
        else:
            logger.error(f"Unsupported image type: {type(image)}")
            return ""
    except Exception as e:
        logger.error(f"Error converting image to base64: {str(e)}")
        return ""
