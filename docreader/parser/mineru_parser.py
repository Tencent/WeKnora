import logging
import os
import re
from typing import Dict

import markdownify
import requests

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.markdown_parser import MarkdownImageUtil
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class MinerUParser(BaseParser):
    def __init__(
        self,
        enable_markdownify: bool = True,
        mineru_endpoint: str = "",
        **kwargs,
    ):
        super().__init__(**kwargs)
        self.minerU = os.getenv("MINERU_ENDPOINT", mineru_endpoint)
        self.enable_markdownify = enable_markdownify
        self.image_helper = MarkdownImageUtil()
        self.base64_pattern = re.compile(r"data:image/(\w+);base64,(.*)")
        self.enable = self.ping()
        assert self.ping(), "MinerU API is not reachable"

    def ping(self, timeout: int = 5) -> bool:
        try:
            response = requests.get(
                self.minerU + "/docs", timeout=timeout, allow_redirects=True
            )
            response.raise_for_status()
            return True
        except Exception:
            return False

    def parse_into_text(self, content: bytes) -> Document:
        logger.info(f"Parsing scanned PDF via MinerU API (size: {len(content)} bytes)")
        md_content: str = ""
        images_b64: Dict[str, str] = {}
        try:
            response = requests.post(
                url=self.minerU + "/file_parse",
                data={
                    "return_md": True,
                    "return_images": True,
                    "lang_list": ["ch", "en"],
                    "table_enable": True,
                    "formula_enable": True,
                    "parse_method": "auto",
                    "start_page_id": 0,
                    "end_page_id": 99999,
                    "backend": "pipeline",
                    "response_format_zip": False,
                    "return_middle_json": False,
                    "return_model_output": False,
                    "return_content_list": False,
                },
                files={"files": content},
                timeout=1000,
            )
            response.raise_for_status()
            result = response.json()["results"]["files"]
            md_content = result["md_content"]
            images_b64 = result.get("images", {})
        except Exception as e:
            logger.error(f"MinerU parsing failed: {e}", exc_info=True)
            return Document()

        # convert table(HTML) in markdown to markdown table
        if self.enable_markdownify:
            logger.debug("Converting HTML to Markdown")
            md_content = markdownify.markdownify(md_content)

        images = {}
        image_replace = {}
        # image in images_bs64 may not be used in md_content
        # such as: table ...
        # so we need to filter them
        for ipath, b64_str in images_b64.items():
            if f"images/{ipath}" not in md_content:
                logger.debug(f"Image {ipath} not used in markdown")
                continue
            match = self.base64_pattern.match(b64_str)
            if match:
                file_ext = match.group(1)
                b64_str = match.group(2)

                image_bytes = endecode.encode_image(b64_str, errors="ignore")
                if not image_bytes:
                    logger.error("Failed to decode base64 image skip it")
                    continue

                image_url = self.storage.upload_bytes(
                    image_bytes, file_ext=f".{file_ext}"
                )

                images[image_url] = b64_str
                image_replace[f"images/{ipath}"] = image_url

        logger.info(f"Replaced {len(image_replace)} images in markdown")
        text = self.image_helper.replace_path(md_content, image_replace)

        logger.info(
            f"Successfully parsed PDF, text: {len(text)}, images: {len(images)}"
        )
        return Document(content=text, images=images)


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_file = "/path/to/your/file.pdf"
    your_mineru = "http://host.docker.internal:9987"
    parser = MinerUParser(mineru_endpoint=your_mineru)
    with open(your_file, "rb") as f:
        content = f.read()
        document = parser.parse_into_text(content)
        logger.error(document.content)
