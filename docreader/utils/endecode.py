import base64
import binascii
import io
import logging
from typing import List, Union

import numpy as np
from PIL import Image

logger = logging.getLogger(__name__)


def decode_image(image: Union[str, bytes, Image.Image, np.ndarray]) -> str:
    """Convert image to base64 encoded string

    Args:
        image: Image file path, bytes, PIL Image object, or numpy array

    Returns:
        Base64 encoded image string, or empty string if conversion fails
    """
    if isinstance(image, str):
        # It's a file path
        with open(image, "rb") as image_file:
            return base64.b64encode(image_file.read()).decode()

    elif isinstance(image, bytes):
        # It's bytes data
        return base64.b64encode(image).decode()

    elif isinstance(image, Image.Image):
        # It's a PIL Image
        buffer = io.BytesIO()
        image.save(buffer, format=image.format)
        return base64.b64encode(buffer.getvalue()).decode()

    elif isinstance(image, np.ndarray):
        # It's a numpy array
        pil_image = Image.fromarray(image)
        buffer = io.BytesIO()
        pil_image.save(buffer, format="PNG")
        return base64.b64encode(buffer.getvalue()).decode()

    raise ValueError(f"Unsupported image type: {type(image)}")


def encode_image(image: str, errors="strict") -> bytes:
    """
    Decode image bytes using base64.

    errors
        The error handling scheme to use for the handling of decoding errors.
        The default is 'strict' meaning that decoding errors raise a
        UnicodeDecodeError. Other possible values are 'ignore' and '????'
        as well as any other name registered with codecs.register_error that
        can handle UnicodeDecodeErrors.
    """
    try:
        image_bytes = base64.b64decode(image)
    except binascii.Error as e:
        if errors == "ignore":
            return b""
        else:
            raise e
    return image_bytes


def encode_bytes(content: str) -> bytes:
    return content.encode()


def decode_bytes(
    content: bytes,
    encodings: List[str] = [
        "utf-8",
        "gb18030",
        "gb2312",
        "gbk",
        "big5",
        "ascii",
        "latin-1",
    ],
) -> str:
    # Try decoding with each encoding format
    for encoding in encodings:
        try:
            text = content.decode(encoding)
            logger.debug(f"Decode content with {encoding}: {len(text)} characters")
            return text
        except UnicodeDecodeError:
            continue

    text = content.decode(encoding="latin-1", errors="replace")
    logger.warning(
        "Unable to determine correct encoding, using latin-1 as fallback. "
        "This may cause character issues."
    )
    return text


if __name__ == "__main__":
    img = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    encode_image(img, errors="ignore")
