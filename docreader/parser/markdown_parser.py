import base64
import logging
import os
from typing import Dict

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_image_util import MarkdownImageUtil
from docreader.utils import endecode

# Get logger object
logger = logging.getLogger(__name__)


class MarkdownImageBase64(BaseParser):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.image_helper = MarkdownImageUtil()

    def parse_into_text(self, content: bytes) -> Document:
        # Convert byte content to string using universal decoding method
        text = endecode.decode_bytes(content)
        text, img_b64 = self.image_helper.extract_base64(text, path_prefix="images")

        images: Dict[str, str] = {}
        image_replace: Dict[str, str] = {}

        logger.debug(f"Uploading {len(img_b64)} images from markdown")
        for ipath, b64_bytes in img_b64.items():
            ext = os.path.splitext(ipath)[1].lower()
            image_url = self.storage.upload_bytes(b64_bytes, ext)

            image_replace[ipath] = image_url
            images[image_url] = base64.b64encode(b64_bytes).decode()

        text = self.image_helper.replace_path(text, image_replace)
        return Document(content=text, images=images)


class MarkdownParser(PipelineParser):
    _parser_cls = (MarkdownImageBase64,)


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    parser = MarkdownParser()

    document = parser.parse_into_text(your_content.encode())
    logger.info(document.content)
    logger.info(f"Images: {len(document.images)}, name: {document.images.keys()}")
