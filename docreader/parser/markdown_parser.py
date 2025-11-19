import base64
import logging
import os
import re
import uuid
from typing import Dict, List, Match, Optional, Tuple

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.utils import endecode

# Get logger object
logger = logging.getLogger(__name__)


class MarkdownTableUtil:
    def __init__(self):
        self.align_pattern = re.compile(
            r"^([\t ]*)\|[\t ]*[:-]+(?:[\t ]*\|[\t ]*[:-]+)*[\t ]*\|[\t ]*$",
            re.MULTILINE,
        )
        self.line_pattern = re.compile(
            r"^([\t ]*)\|[\t ]*[^|\r\n]*(?:[\t ]*\|[^|\r\n]*)*\|[\t ]*$",
            re.MULTILINE,
        )

    def format_table(self, content: str) -> str:
        def process_align(match: Match[str]) -> str:
            columns = [col.strip() for col in match.group(0).split("|") if col.strip()]

            processed = []
            for col in columns:
                left_colon = ":" if col.startswith(":") else ""
                right_colon = ":" if col.endswith(":") else ""
                processed.append(left_colon + "---" + right_colon)

            prefix = match.group(1)
            return prefix + "| " + " | ".join(processed) + " |"

        def process_line(match: Match[str]) -> str:
            columns = [col.strip() for col in match.group(0).split("|") if col.strip()]

            prefix = match.group(1)
            return prefix + "| " + " | ".join(columns) + " |"

        formatted_content = content
        formatted_content = self.line_pattern.sub(process_line, formatted_content)
        formatted_content = self.align_pattern.sub(process_align, formatted_content)

        return formatted_content

    @staticmethod
    def _self_test():
        test_content = """
# 测试表格
普通文本---不会被匹配

## 表格1（无前置空格）

| 姓名   | 年龄  | 城市          |
|      :---------- | -------: | :------      |
| 张三 | 25 | 北京 |

## 表格3（前置4个空格+首尾|）
    |   产品   |   价格   |   库存   |
    | :-------------: | ----------- | :-----------: |
    | 手机 | 5999       | 100 |
"""
        util = MarkdownTableUtil()
        format_content = util.format_table(test_content)
        print(format_content)


class MarkdownTableFormatter(BaseParser):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.table_helper = MarkdownTableUtil()

    def parse_into_text(self, content: bytes) -> Document:
        text = endecode.decode_bytes(content)
        text = self.table_helper.format_table(text)
        return Document(content=text)


class MarkdownImageUtil:
    def __init__(self):
        self.b64_pattern = re.compile(
            r"!\[([^\]]*)\]\(data:image/(\w+)\+?\w*;base64,([^\)]+)\)"
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

    @staticmethod
    def _self_test():
        your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAA)test"
        image_handle = MarkdownImageUtil()
        text, images = image_handle.extract_base64(your_content)
        print(text)

        for image_url, image_byte in images.items():
            with open(image_url, "wb") as f:
                f.write(image_byte)


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
    _parser_cls = (MarkdownTableFormatter, MarkdownImageBase64)


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    parser = MarkdownParser()

    document = parser.parse_into_text(your_content.encode())
    logger.info(document.content)
    logger.info(f"Images: {len(document.images)}, name: {document.images.keys()}")

    MarkdownImageUtil._self_test()
    MarkdownTableUtil._self_test()
