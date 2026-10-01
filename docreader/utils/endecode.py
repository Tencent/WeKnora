"""
Encoding and Decoding Utilities Module

This module provides utilities for encoding and decoding various data types,
with a focus on image and text data conversion:
- Image encoding/decoding (base64)
- Text encoding/decoding (multiple character sets)
- Bytes conversion utilities
"""

import base64
import io
import logging
from typing import List, Union

import numpy as np
from PIL import Image

logger = logging.getLogger(__name__)


def decode_image(image: Union[str, bytes, Image.Image, np.ndarray]) -> str:
    """Convert image to base64 encoded string.

    This function handles multiple image input formats and converts them
    to a base64 encoded string representation, which is useful for embedding
    images in JSON, HTML, or other text-based formats.

    Args:
        image: Image in one of the following formats:
            - str: File path to an image file
            - bytes: Raw image bytes data
            - Image.Image: PIL/Pillow Image object
            - np.ndarray: NumPy array representing image data

    Returns:
        str: Base64 encoded string representation of the image

    Raises:
        ValueError: If the image type is not supported

    Example:
        >>> # From file path
        >>> base64_str = decode_image("/path/to/image.png")
        >>> # From PIL Image
        >>> from PIL import Image
        >>> img = Image.open("photo.jpg")
        >>> base64_str = decode_image(img)
    """
    if isinstance(image, str):
        # Handle file path: read file and encode to base64
        with open(image, "rb") as image_file:
            return base64.b64encode(image_file.read()).decode()

    elif isinstance(image, bytes):
        # Handle raw bytes: directly encode to base64
        return base64.b64encode(image).decode()

    elif isinstance(image, Image.Image):
        # Handle PIL Image: save to buffer then encode
        buffer = io.BytesIO()
        # Use original format if available, otherwise default to PNG
        img_format = image.format if image.format else "PNG"
        image.save(buffer, format=img_format)
        return base64.b64encode(buffer.getvalue()).decode()

    elif isinstance(image, np.ndarray):
        # Handle numpy array: convert to PIL Image, then encode as PNG
        pil_image = Image.fromarray(image)
        buffer = io.BytesIO()
        pil_image.save(buffer, format="PNG")
        return base64.b64encode(buffer.getvalue()).decode()

    raise ValueError(f"Unsupported image type: {type(image)}")


def encode_image(image: str, errors="strict") -> bytes:
    """Decode a base64 encoded image string back to bytes.

    This function converts a base64 encoded string representation of an image
    back into its original binary bytes format.

    Args:
        image: Base64 encoded string representation of an image
        errors: Error handling scheme for decoding errors:
            - 'strict' (default): Raise on decoding errors
            - 'ignore': Return empty bytes on decoding errors
            - Any other name registered with codecs.register_error

    Returns:
        bytes: Decoded image bytes, or empty bytes if errors='ignore' and decoding fails

    Raises:
        ValueError: If decoding fails and errors='strict'. base64.b64decode
            signals bad padding or a bad alphabet with binascii.Error, and a
            non-ASCII str with a plain ValueError; binascii.Error subclasses
            ValueError, so both arrive as one.

    Example:
        >>> base64_str = "iVBORw0KGgoAAAANSUhEUgAAAAUA..."
        >>> image_bytes = encode_image(base64_str)
        >>> # With error handling
        >>> image_bytes = encode_image(base64_str, errors="ignore")
    """
    try:
        # Attempt to decode the base64 string to bytes
        image_bytes = base64.b64decode(image)
    except ValueError as e:
        # Catching binascii.Error alone would miss the non-ASCII case: a str
        # that is not pure ASCII fails before the decoder runs and surfaces as
        # a plain ValueError, which errors="ignore" is supposed to absorb.
        if errors == "ignore":
            return b""
        else:
            raise e
    return image_bytes


def encode_bytes(content: str) -> bytes:
    """Convert a string to bytes using UTF-8 encoding.

    Args:
        content: String to be encoded

    Returns:
        bytes: UTF-8 encoded bytes representation of the string

    Example:
        >>> text = "Hello, 世界"
        >>> encoded = encode_bytes(text)
        >>> type(encoded)
        <class 'bytes'>
    """
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
    """Decode bytes to string with automatic encoding detection.

    This function attempts to decode bytes using multiple encoding formats
    in order of priority. It's particularly useful for handling text files
    with unknown or mixed encodings, especially for Chinese text.

    The function tries encodings in the provided order and returns the first
    successful decode. If all encodings fail, it falls back to latin-1 with
    error replacement to ensure a result is always returned.

    Args:
        content: Bytes content to be decoded
        encodings: List of encoding formats to try, in order of priority.
            Default includes common encodings for Chinese and Western text:
            - utf-8: Universal encoding (tried first)
            - gb18030, gb2312, gbk: Chinese encodings (Simplified)
            - big5: Chinese encoding (Traditional)
            - ascii, latin-1: Western encodings

    Returns:
        str: Decoded string content

    Note:
        - If all encodings fail, latin-1 with error='replace' is used as fallback
        - The fallback may result in character replacement (�) for invalid bytes
        - A warning is logged when fallback encoding is used

    Example:
        >>> # Decode with default encodings
        >>> text = decode_bytes(b"\\xe4\\xb8\\xad\\xe6\\x96\\x87")  # UTF-8 Chinese
        >>> print(text)
        中文
        >>> # Decode with custom encodings
        >>> text = decode_bytes(content, encodings=["utf-8", "gbk"])
    """
    # Try decoding with each encoding format in order
    for encoding in encodings:
        try:
            text = content.decode(encoding)
            logger.debug(f"Decode content with {encoding}: {len(text)} characters")
            return text
        except UnicodeDecodeError:
            # This encoding didn't work, try the next one
            continue

    # Fallback: use latin-1 with error replacement if all encodings fail
    # latin-1 can decode any byte sequence, but may produce incorrect characters
    text = content.decode(encoding="latin-1", errors="replace")
    logger.warning(
        "Unable to determine correct encoding, using latin-1 as fallback. "
        "This may cause character issues."
    )
    return text


if __name__ == "__main__":
    # Example: Test encode_image with error handling
    # This demonstrates decoding a base64 string with 'ignore' error mode
    img = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    encode_image(img, errors="ignore")
