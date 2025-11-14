import logging
import re
import uuid
from typing import Dict, List, Match, Optional, Tuple

from docreader.utils import endecode

# Get logger object
logger = logging.getLogger(__name__)


class MarkdownImageUtil:
    def __init__(self):
        self.b64_pattern = re.compile(
            r"!\[([^\]]*)\]\(data:image/(\w+);base64,([^\)]+)\)"
        )
        self.image_pattern = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
        self.replace_pattern = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")

    def extract_image(
        self,
        content: str,
        path_prefix: Optional[str] = None,
        replace: bool = True,
    ) -> Tuple[str, List[str]]:
        """Extract base64 encoded images from Markdown content"""

        # image_path => base64 bytes
        images: List[str] = []

        def repl(match: Match[str]) -> str:
            title = match.group(1)
            image_path = match.group(2)
            if path_prefix:
                image_path = f"{path_prefix}/{image_path}"

            images.append(image_path)

            if not replace:
                return match.group(0)

            # Replace image path with URL
            return f"![{title}]({image_path})"

        text = self.image_pattern.sub(repl, content)
        logger.debug(f"Extracted {len(images)} images from markdown")
        return text, images

    def extract_base64(
        self,
        content: str,
        path_prefix: Optional[str] = None,
        replace: bool = True,
    ) -> Tuple[str, Dict[str, bytes]]:
        """Extract base64 encoded images from Markdown content"""

        # image_path => base64 bytes
        images: Dict[str, bytes] = {}

        def repl(match: Match[str]) -> str:
            title = match.group(1)
            img_ext = match.group(2)
            img_b64 = match.group(3)

            image_byte = endecode.encode_image(img_b64, errors="ignore")
            if not image_byte:
                logger.error(f"Failed to decode base64 image skip it: {img_b64}")
                return title

            image_path = f"{uuid.uuid4()}.{img_ext}"
            if path_prefix:
                image_path = f"{path_prefix}/{image_path}"
            images[image_path] = image_byte

            if not replace:
                return match.group(0)

            # Replace image path with URL
            return f"![{title}]({image_path})"

        text = self.b64_pattern.sub(repl, content)
        logger.debug(f"Extracted {len(images)} base64 images from markdown")
        return text, images

    def replace_path(self, content: str, images: Dict[str, str]) -> str:
        content_replace: set = set()

        def repl(match: Match[str]) -> str:
            title = match.group(1)
            image_path = match.group(2)
            if image_path not in images:
                return match.group(0)

            content_replace.add(image_path)
            image_path = images[image_path]
            return f"![{title}]({image_path})"

        text = self.replace_pattern.sub(repl, content)
        logger.debug(f"Replaced {len(content_replace)} images in markdown")
        return text


if __name__ == "__main__":
    your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAA)test"
    image_handle = MarkdownImageUtil()
    text, images = image_handle.extract_base64(your_content)
    print(text)

    for image_url, image_byte in images.items():
        with open(image_url, "wb") as f:
            f.write(image_byte)
