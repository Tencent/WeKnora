import logging
import re
import time
from typing import Dict, Optional

import markdownify
import requests

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownTableFormatter
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class StdMinerUParser(BaseParser):
    """
    Standard MinerU Parser for document parsing.

    This parser uses MinerU API to parse documents (especially PDFs) into markdown format,
    with support for tables, formulas, and images extraction.
    """

    def __init__(
            self,
            enable_markdownify: bool = True,
            mineru_endpoint: Optional[str] = None,  # Added: 支持传入自定义 endpoint
            **kwargs,
    ):
        """
        Initialize MinerU parser.

        Args:
            enable_markdownify: Whether to convert HTML tables to markdown format
            mineru_endpoint: MinerU API endpoint URL
            **kwargs: Additional arguments passed to BaseParser
        """
        super().__init__(**kwargs)
        # Get MinerU endpoint from environment variable or parameter
        # Modified: 优先使用传入的参数，否则使用 Config
        base_url = mineru_endpoint if mineru_endpoint else CONFIG.mineru_endpoint
        self.minerU = base_url.rstrip("/") if base_url else ""

        self.enable_markdownify = enable_markdownify
        # Helper for processing markdown images
        self.image_helper = MarkdownImageUtil()
        # Pattern to match base64 encoded images
        self.base64_pattern = re.compile(r"data:image/(\w+);base64,(.*)")
        # Check if MinerU API is available
        self.enable = self.ping()

    def ping(self, timeout: int = 5) -> bool:
        """
        Check if MinerU API is available.

        Args:
            timeout: Request timeout in seconds

        Returns:
            True if API is available, False otherwise
        """
        try:
            response = requests.get(
                self.minerU + "/docs", timeout=timeout, allow_redirects=True
            )
            response.raise_for_status()
            return True
        except Exception:
            return False

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse document content into text using MinerU API.

        Args:
            content: Raw document content in bytes

        Returns:
            Document object containing parsed text and images
        """
        if not self.enable:
            logger.debug("MinerU API is not enabled")
            return Document()

        logger.info(f"Parsing scanned PDF via MinerU API (size: {len(content)} bytes)")
        md_content: str = ""
        images_b64: Dict[str, str] = {}
        try:
            # Call MinerU API to parse document
            response = requests.post(
                url=self.minerU + "/file_parse",
                data={
                    "return_md": True,  # Return markdown content
                    "return_images": True,  # Return extracted images
                    "lang_list": ["ch", "en"],  # Support Chinese and English
                    "table_enable": True,  # Enable table parsing
                    "formula_enable": True,  # Enable formula parsing
                    "parse_method": "auto",  # Auto detect parsing method
                    "start_page_id": 0,  # Start from first page
                    "end_page_id": 99999,  # Parse all pages
                    "backend": "pipeline",  # Use pipeline backend
                    "response_format_zip": False,  # Return JSON instead of ZIP
                    "return_middle_json": False,  # Don't return intermediate JSON
                    "return_model_output": False,  # Don't return model output
                    "return_content_list": False,  # Don't return content list
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

        # Convert HTML tables in markdown to markdown table format
        if self.enable_markdownify:
            logger.debug("Converting HTML to Markdown")
            md_content = markdownify.markdownify(md_content)

        images = {}
        image_replace = {}
        # Filter images that are actually used in markdown content
        # Some images in images_b64 may not be referenced in md_content
        # (e.g., images embedded in tables), so we need to filter them
        for ipath, b64_str in images_b64.items():
            # Skip images that are not referenced in markdown content
            if f"images/{ipath}" not in md_content:
                logger.debug(f"Image {ipath} not used in markdown")
                continue
            # Parse base64 image data
            match = self.base64_pattern.match(b64_str)
            if match:
                # Extract image format (e.g., png, jpg)
                file_ext = match.group(1)
                # Extract base64 encoded data
                b64_str = match.group(2)

                # Decode base64 string to bytes
                image_bytes = endecode.encode_image(b64_str, errors="ignore")
                if not image_bytes:
                    logger.error("Failed to decode base64 image skip it")
                    continue

                # Upload image to storage and get URL
                image_url = self.storage.upload_bytes(
                    image_bytes, file_ext=f".{file_ext}"
                )

                # Store image mapping for later use
                images[image_url] = b64_str
                # Prepare replacement mapping for markdown content
                image_replace[f"images/{ipath}"] = image_url

        logger.info(f"Replaced {len(image_replace)} images in markdown")
        # Replace image paths in markdown with uploaded URLs
        text = self.image_helper.replace_path(md_content, image_replace)

        logger.info(
            f"Successfully parsed PDF, text: {len(text)}, images: {len(images)}"
        )
        return Document(content=text, images=images)


# Added: 新增 MinerUCloudParser 类，支持异步任务提交
class MinerUCloudParser(StdMinerUParser):
    """
    MinerU Parser for REMOTE/CLOUD API (Asynchronous).
    Uses the /submit -> /status -> /result workflow.
    """

    SUBMIT_TIMEOUT = 30
    POLL_INTERVAL = 2
    MAX_WAIT_TIME = 600

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parse document content using Cloud MinerU API (Async/Polling).
        """
        if not self.enable:
            return Document()

        logger.info(f"Parsing PDF via Cloud MinerU API (size: {len(content)} bytes)")

        try:
            # --- Step 1: Submit Task ---
            submit_url = f"{self.minerU}/submit"
            logger.info(f"Submitting task to {submit_url}")

            response = requests.post(
                url=submit_url,
                files={"files": content},
                data={
                    "enable_formula": "true",
                    "enable_table": "true",
                    "layout_model": "doclayout_yolo",
                    "backend": "pipeline",
                },
                timeout=self.SUBMIT_TIMEOUT,
            )
            response.raise_for_status()

            # Robust task_id extraction
            resp_data = response.json()
            task_id = resp_data.get("task_id") or resp_data.get("data", {}).get("task_id")

            if not task_id:
                raise ValueError(f"No task_id in response: {resp_data}")

            logger.info(f"Task submitted, ID: {task_id}, waiting for completion...")

            # --- Step 2: Poll Status ---
            start_time = time.time()

            while True:
                if time.time() - start_time > self.MAX_WAIT_TIME:
                    raise TimeoutError(f"Task {task_id} timed out after {self.MAX_WAIT_TIME}s")

                try:
                    status_resp = requests.get(
                        f"{self.minerU}/status/{task_id}",
                        timeout=10
                    )
                    status_resp.raise_for_status()
                    status_data = status_resp.json()
                except requests.RequestException as e:
                    logger.warning(f"Status check failed for {task_id}: {e}. Retrying...")
                    time.sleep(self.POLL_INTERVAL)
                    continue

                state = status_data.get("status") or status_data.get("state")

                if state in ["done", "success"]:
                    break
                elif state == "failed":
                    error_msg = status_data.get("error") or "Unknown error"
                    raise RuntimeError(f"Task {task_id} failed: {error_msg}")
                else:
                    time.sleep(self.POLL_INTERVAL)

            # --- Step 3: Get Result ---
            result_resp = requests.get(
                f"{self.minerU}/result/{task_id}",
                timeout=30
            )
            result_resp.raise_for_status()
            result_json = result_resp.json()

            # Normalize result data
            result_data = result_json.get("result", result_json)

            md_content = result_data.get("md_content", "")
            images_b64 = result_data.get("images", {})

            # 使用父类的方法处理图片和Markdown转换 (复用现有逻辑)

            # Convert HTML tables
            if self.enable_markdownify:
                md_content = markdownify.markdownify(md_content)

            images = {}
            image_replace = {}

            for ipath, b64_str in images_b64.items():
                if f"images/{ipath}" not in md_content:
                    continue
                match = self.base64_pattern.match(b64_str)
                if match:
                    file_ext = match.group(1)
                    b64_str_clean = match.group(2)
                    image_bytes = endecode.encode_image(b64_str_clean, errors="ignore")
                    if not image_bytes: continue

                    if self.storage:
                        image_url = self.storage.upload_bytes(image_bytes, file_ext=f".{file_ext}")
                        images[image_url] = b64_str_clean
                        image_replace[f"images/{ipath}"] = image_url

            if image_replace:
                md_content = self.image_helper.replace_path(md_content, image_replace)

            return Document(content=md_content, images=images)

        except Exception as e:
            logger.error(f"Cloud MinerU parsing failed: {e}", exc_info=True)
            return Document()


class MinerUParser(PipelineParser):
    """
    MinerU Parser with pipeline processing.

    This parser combines StdMinerUParser for document parsing and
    MarkdownTableFormatter for table formatting in a pipeline.
    """

    _parser_cls = (StdMinerUParser, MarkdownTableFormatter)


if __name__ == "__main__":
    import os

    # Example usage for testing
    logging.basicConfig(level=logging.DEBUG)

    # Configure your file path and MinerU endpoint
    your_file = "/path/to/your/file.pdf"

    # Added: 修改为 Localhost 方便测试
    test_endpoint = "http://localhost:9987"
    os.environ["MINERU_ENDPOINT"] = test_endpoint

    # Create parser instance
    # Modified: 传入 endpoint
    parser = MinerUParser(mineru_endpoint=test_endpoint)

    # Parse PDF file
    if os.path.exists(your_file):
        with open(your_file, "rb") as f:
            content = f.read()
            document = parser.parse_into_text(content)
            logger.error(document.content)
    else:
        print(f"File not found: {your_file}")