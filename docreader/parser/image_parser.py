import base64
import logging
import re
import uuid

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class ImageParser(BaseParser):
    """Parser for standalone image files.

    Returns the image as a markdown reference with the raw image data
    in Document.images so that the Go-side ImageResolver (or main.py's
    _resolve_images) can handle storage upload.
    """

    def parse_into_text(self, content: bytes) -> Document:
        logger.info(
            "Parsing image file=%s, size=%d bytes", self.file_name, len(content)
        )

        name = self.file_name
        # A filename is not necessarily a valid bare Markdown destination.
        # Keep ordinary refs stable; use an opaque name for syntax/whitespace.
        if not name or any(c.isspace() or c in "\\()[]<>" for c in name):
            ext = "." + self.file_type.lstrip(".").lower()
            if not re.fullmatch(r"\.[a-z0-9]+", ext):
                ext = ".png"
            name = f"{uuid.uuid4().hex}{ext}"
        ref_path = f"images/{name}"

        alt = " ".join(self.file_name.splitlines())
        alt = re.sub(r"([\\\[\]<>`*_])", r"\\\1", alt)
        text = f"![{alt}]({ref_path})"
        images = {ref_path: base64.b64encode(content).decode()}

        return Document(content=text, images=images)
