# -*- coding: utf-8 -*-
import asyncio
import io
import ipaddress
import logging
import os
import re
import time
from abc import ABC, abstractmethod
from typing import Dict, List, Optional, Tuple
from urllib.parse import urlparse

import requests
from PIL import Image

from docreader.config import CONFIG
from docreader.models.document import Chunk, Document
from docreader.models.read_config import ChunkingConfig
from docreader.ocr import OCREngine
from docreader.parser.caption import Caption
from docreader.parser.storage import create_storage
from docreader.splitter.splitter import TextSplitter
from docreader.utils import endecode

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


class BaseParser(ABC):
    """Base parser interface"""

    # Class variable for shared OCR engine instance
    _ocr_engine = None
    _ocr_engine_failed = False

    @staticmethod
    def _is_safe_url(url: str) -> bool:
        """Validate URL to prevent SSRF attacks

        Args:
            url: URL to validate

        Returns:
            True if URL is safe to fetch, False otherwise
        """
        try:
            parsed_url = urlparse(url)

            # Only allow http and https schemes
            if parsed_url.scheme not in ["http", "https"]:
                logger.warning(f"Rejected URL with invalid scheme: {parsed_url.scheme}")
                return False

            # Extract hostname
            hostname = parsed_url.hostname
            if not hostname:
                logger.warning("No hostname found in URL")
                return False
            # Try to parse as IP address
            try:
                ip = ipaddress.ip_address(hostname)
                # Reject private, loopback, link-local, multicast addresses
                if (
                    ip.is_private
                    or ip.is_loopback
                    or ip.is_link_local
                    or ip.is_multicast
                    or ip.is_reserved
                ):
                    logger.warning(f"Rejected URL with restricted IP: {ip}")
                    return False
            except ValueError:
                # Not an IP address, validate hostname
                # Reject localhost and common internal hostnames
                hostname_lower = hostname.lower()
                restricted_hostnames = [
                    "localhost",
                    "127.0.0.1",
                    "::1",
                    "metadata.google.internal",
                    "metadata.tencentyun.com",
                    "169.254.169.254",  # AWS metadata endpoint
                ]
                if hostname_lower in restricted_hostnames or hostname_lower.endswith(
                    ".local"
                ):
                    logger.warning(f"Rejected URL with restricted hostname: {hostname}")
                    return False

            return True
        except Exception as e:
            logger.warning(f"Error validating URL: {str(e)}")
            return False

    @classmethod
    def get_ocr_engine(cls, backend_type="paddle", **kwargs):
        """Get OCR engine instance

        Args:
            backend_type: OCR engine type, e.g. "paddle", "nanonets"
            **kwargs: Arguments for the OCR engine

        Returns:
            OCR engine instance or None
        """
        if cls._ocr_engine is None and not cls._ocr_engine_failed:
            try:
                cls._ocr_engine = OCREngine.get_instance(
                    backend_type=backend_type, **kwargs
                )
                if cls._ocr_engine is None:
                    cls._ocr_engine_failed = True
                    logger.error(f"Failed to initialize OCR engine ({backend_type})")
                    return None
                logger.info(f"Successfully initialized OCR engine: {backend_type}")
            except Exception as e:
                cls._ocr_engine_failed = True
                logger.error(f"Failed to initialize OCR engine: {str(e)}")
                return None
        return cls._ocr_engine

    def __init__(
        self,
        file_name: str = "",
        file_type: Optional[str] = None,
        enable_multimodal: bool = True,
        chunk_size: int = 1000,
        chunk_overlap: int = 200,
        separators: list[str] = ["\n\n", "\n", "。"],
        ocr_backend: str = "no_ocr",
        ocr_config: dict = {},
        max_image_size: int = 1920,  # Maximum image size
        max_concurrent_tasks: int = 5,  # Max concurrent tasks
        max_chunks: int = 1000,  # Max number of returned chunks
        chunking_config: Optional[ChunkingConfig] = None,
        **kwargs,
    ):
        """Initialize parser

        Args:
            file_name: File name
            file_type: File type, inferred from file_name if None
            enable_multimodal: Whether to enable multimodal
            chunk_size: Chunk size
            chunk_overlap: Chunk overlap
            separators: List of separators
            ocr_backend: OCR engine type
            ocr_config: OCR engine config
            max_image_size: Maximum image size
            max_concurrent_tasks: Max concurrent tasks
            max_chunks: Max number of returned chunks
        """
        # Storage client instance
        self.file_name = file_name
        self.file_type = file_type or os.path.splitext(file_name)[1]
        self.enable_multimodal = enable_multimodal
        self.chunk_size = chunk_size
        self.chunk_overlap = chunk_overlap
        self.separators = separators
        self.ocr_backend = ocr_backend
        self.ocr_config = ocr_config
        self.max_image_size = max_image_size
        self.max_concurrent_tasks = max_concurrent_tasks
        self.max_chunks = max_chunks
        self.chunking_config = chunking_config
        self.storage = create_storage(
            self.chunking_config.storage_config if self.chunking_config else None
        )

        logger.info(
            f"Initializing parser for file={file_name}, type={file_type}, "
            f"parser config: chunk_size={chunk_size}, "
            f"overlap={chunk_overlap}, "
            f"multimodal={enable_multimodal}, "
            f"max_chunks={max_chunks}, "
            f"ocr_backend={ocr_backend}, "
            f"max_concurrent_tasks={max_concurrent_tasks}"
        )
        # Only initialize Caption service if multimodal is enabled
        vlm_config = self.chunking_config.vlm_config if self.chunking_config else None
        self.caption_parser = (
            Caption(vlm_config=vlm_config) if self.enable_multimodal else None
        )

    @abstractmethod
    def parse_into_text(self, content: bytes) -> Document:
        """Parse document content

        Args:
            content: Document content

        Returns:
            Either a string containing the parsed text, or a tuple of (text, image_map)
            where image_map is a dict mapping image URLs to Image objects
        """

    def perform_ocr(self, image: Image.Image):
        """Execute OCR recognition on the image

        Args:
            image: Image object (PIL.Image or numpy array)

        Returns:
            Extracted text string
        """
        start_time = time.time()
        logger.info("Starting OCR recognition")

        # Resize image to avoid processing large images
        resized_image = self._resize_image_if_needed(image)

        # Get OCR engine
        ocr_engine = OCREngine.get_instance(self.ocr_backend)

        # Execute OCR prediction
        logger.info(f"Executing OCR prediction (using {self.ocr_backend} engine)")
        ocr_result = ocr_engine.predict(resized_image)

        process_time = time.time() - start_time
        logger.info(f"OCR recognition completed, time: {process_time:.2f} seconds")

        return ocr_result

    def _resize_image_if_needed(self, image: Image.Image) -> Image.Image:
        """Resize image if it exceeds maximum size limit

        Args:
            image: Image object (PIL.Image or numpy array)

        Returns:
            Resized image object
        """
        width, height = image.size
        if width > self.max_image_size or height > self.max_image_size:
            logger.info(f"Resizing PIL image, original size: {width}x{height}")
            scale = min(self.max_image_size / width, self.max_image_size / height)
            new_width = int(width * scale)
            new_height = int(height * scale)
            resized_image = image.resize((new_width, new_height))
            logger.info(f"Resized to: {new_width}x{new_height}")
            return resized_image

        logger.info(f"PIL image size is {width}x{height}, no resizing needed")
        return image

    async def process_image_async(self, image: Image.Image, image_url: str):
        """Asynchronously process image: perform OCR only (caption is generated asynchronously by Go backend)

        Args:
            image: Image object (PIL.Image or numpy array)
            image_url: Image URL (if uploaded)

        Returns:
            tuple: (ocr_text, caption, image_url)
            - ocr_text: OCR extracted text
            - caption: Always empty string (caption generation moved to async task in Go backend)
            - image_url: Image URL (if provided)
        """
        logger.info(
            "Starting asynchronous image processing (OCR only, caption deferred to async task)"
        )

        # Resize image
        resized_image = self._resize_image_if_needed(image)
        try:
            # Perform OCR recognition
            loop = asyncio.get_event_loop()
            try:
                # Add timeout mechanism to avoid infinite blocking (30 seconds timeout)
                ocr_task = loop.run_in_executor(None, self.perform_ocr, resized_image)
                ocr_text = await asyncio.wait_for(ocr_task, timeout=30.0)
            except Exception as e:
                logger.error(f"OCR processing error, skipping this image: {str(e)}")
                ocr_text = ""

            logger.info(f"Successfully obtained image ocr: {ocr_text}")
            img_base64 = endecode.decode_image(resized_image)
            caption = self.get_image_caption(img_base64)
            logger.info(f"Successfully obtained image caption: {caption}")
            return ocr_text, caption, image_url
        finally:
            resized_image.close()

    async def process_with_limit(
        self, idx: int, image: Image.Image, url: str, semaphore: asyncio.Semaphore
    ):
        """Function to process a single image using a semaphore"""
        try:
            logger.info(f"Waiting to process image {idx + 1}")
            async with semaphore:  # Use semaphore to control concurrency
                logger.info(f"Starting to process image {idx + 1}")
                result = await self.process_image_async(image, url)
                logger.info(f"Completed processing image {idx + 1}")
                return result
        except Exception as e:
            logger.error(f"Error processing image {idx + 1}: {str(e)}")
            return ("", "", url)  # Return empty result to avoid overall failure
        finally:
            # Manually release image resources
            image.close()

    async def process_multiple_images(self, images_data: List[Tuple[Image.Image, str]]):
        """Process multiple images concurrently

        Args:
            images_data: List of (image, image_url) tuples

        Returns:
            List of (ocr_text, caption, image_url) tuples
        """
        logger.info(f"Starting concurrent processing of {len(images_data)} images")

        if not images_data:
            logger.warning("No image data to process")
            return []

        # Use semaphore to limit concurrency
        semaphore = asyncio.Semaphore(self.max_concurrent_tasks)

        # Store results to avoid overall failure due to task failure
        results = []

        # Create all tasks, but use semaphore to limit actual concurrency
        tasks = [
            self.process_with_limit(i, img, url, semaphore)
            for i, (img, url) in enumerate(images_data)
        ]

        try:
            # Execute all tasks, but set overall timeout
            completed_results = await asyncio.gather(*tasks, return_exceptions=True)

            # Handle possible exception results
            for i, result in enumerate(completed_results):
                if isinstance(result, Exception):
                    logger.error(
                        f"Image {i + 1} processing returned an exception: {str(result)}"
                    )
                    # For exceptions, add empty results
                    if i < len(images_data):
                        results.append(("", "", images_data[i][1]))
                else:
                    results.append(result)
        except Exception as e:
            logger.error(f"Error during concurrent image processing: {str(e)}")
            # Add empty results for all images
            results = [("", "", url) for _, url in images_data]
        finally:
            # Clean up references and trigger garbage collection
            images_data.clear()
            logger.info("Image processing resource cleanup complete")

        logger.info(
            f"Concurrent processing of {len(results)}/{len(images_data)} images"
        )
        return results

    def get_image_caption(self, image_data: str) -> str:
        """Get image description

        Args:
            image_data: Image data (base64 encoded string or URL)

        Returns:
            Image description
        """
        if not self.caption_parser:
            logger.warning("Caption parser not initialized")
            return ""
        start_time = time.time()
        logger.info(
            f"Getting caption for image: {image_data[:250]}..."
            if len(image_data) > 250
            else f"Getting caption for image: {image_data}"
        )
        caption = self.caption_parser.get_caption(image_data)
        if caption:
            logger.info(
                f"Received caption of length: {len(caption)}, caption: {caption},"
                f"cost: {time.time() - start_time} seconds"
            )
        else:
            logger.warning("Failed to get caption for image")
        return caption

    def parse(self, content: bytes) -> Document:
        """Parse document content

        Args:
            content: Document content

        Returns:
            Parse result
        """
        logger.info(
            f"Parsing document with {self.__class__.__name__}, bytes: {len(content)}"
        )
        document = self.parse_into_text(content)
        logger.info(
            f"Extracted {len(document.content)} characters from {self.file_name}"
        )
        if document.chunks:
            return document

        splitter = TextSplitter(
            chunk_size=self.chunk_size,
            chunk_overlap=self.chunk_overlap,
            separators=self.separators,
        )
        chunk_str = splitter.split_text(document.content)
        chunks = self._str_to_chunk(chunk_str)
        logger.info(f"Created {len(chunks)} chunks from document")

        # Limit the number of returned chunks
        if len(chunks) > self.max_chunks:
            logger.warning(
                f"Limiting chunks from {len(chunks)} to maximum {self.max_chunks}"
            )
            chunks = chunks[: self.max_chunks]

        # If multimodal is enabled and file type is supported, process images
        if self.enable_multimodal:
            # Get file extension and convert to lowercase
            file_ext = (
                os.path.splitext(self.file_name)[1].lower()
                if self.file_name
                else (self.file_type.lower() if self.file_type else "")
            )

            # Define allowed file types for image processing
            allowed_types = [
                # Text files
                ".pdf",
                ".md",
                ".markdown",
                ".doc",
                ".docx",
                # Image files
                ".jpg",
                ".jpeg",
                ".png",
                ".gif",
                ".bmp",
                ".tiff",
                ".webp",
            ]

            if file_ext in allowed_types:
                logger.info(
                    f"Processing images in each chunk for file type: {file_ext}"
                )
                chunks = self.process_chunks_images(chunks, document.images)
            else:
                logger.info(
                    f"Skipping image processing for unsupported file type: {file_ext}"
                )

        document.chunks = chunks
        return document

    def _str_to_chunk(self, text: List[Tuple[int, int, str]]) -> List[Chunk]:
        """Convert string to Chunk object"""
        return [
            Chunk(seq=i, content=t, start=start, end=end)
            for i, (start, end, t) in enumerate(text)
        ]

    def _split_into_units(self, text: str) -> List[str]:
        """
        Args:
            text: 文本内容

        Returns:
            基本单元的列表
        """
        logger.info(f"Splitting text into basic units, text length: {len(text)}")

        # 定义所有需要作为整体保护的结构模式 ---
        table_pattern = r"(?m)(^\|.*\|[ \t]*\r?\n(?:[ \t]*\r?\n)?^\|\s*:?--+.*\r?\n(?:^\|.*\|\r?\n?)*)"

        # 其他需要保护的结构（代码块、公式块、行内元素）
        code_block_pattern = r"```[\s\S]*?```"
        math_block_pattern = r"\$\$[\s\S]*?\$\$"
        inline_pattern = r"!\[.*?\]\(.*?\)|\[.*?\]\(.*?\)"

        # 查找所有受保护结构的位置 ---
        protected_ranges = []
        for pattern in [
            table_pattern,
            code_block_pattern,
            math_block_pattern,
            inline_pattern,
        ]:
            for match in re.finditer(pattern, text):
                # 确保匹配到的不是空字符串，避免无效范围
                if match.group(0).strip():
                    protected_ranges.append((match.start(), match.end()))

        # 按起始位置排序
        protected_ranges.sort(key=lambda x: x[0])
        logger.info(
            f"Found {len(protected_ranges)} protected structures "
            "(tables, code, formulas, images, links)."
        )

        # 合并可能重叠的保护范围 ---
        # 确保我们有一组不相交的、需要保护的文本块
        if protected_ranges:
            merged_ranges = []
            current_start, current_end = protected_ranges[0]

            for next_start, next_end in protected_ranges[1:]:
                if next_start < current_end:
                    # 如果下一个范围与当前范围重叠，则合并它们
                    current_end = max(current_end, next_end)
                else:
                    # 如果不重叠，则完成当前范围并开始一个新的范围
                    merged_ranges.append((current_start, current_end))
                    current_start, current_end = next_start, next_end

            merged_ranges.append((current_start, current_end))
            protected_ranges = merged_ranges
            logger.info(
                f"After overlaps, {len(protected_ranges)} protected ranges remain."
            )

        # 根据保护范围和分隔符来分割文本 ---
        units = []
        last_end = 0

        # 定义分隔符的正则表达式，通过加括号来保留分隔符本身
        separator_pattern = f"({'|'.join(re.escape(s) for s in self.separators)})"

        for start, end in protected_ranges:
            # a. 处理受保护范围之前的文本
            if start > last_end:
                pre_text = text[last_end:start]
                # 对这部分非保护文本进行分割，并保留分隔符
                segments = re.split(separator_pattern, pre_text)
                units.extend([s for s in segments if s])  # 添加所有非空部分

            # b. 将整个受保护的块（例如，一个完整的表格）作为一个不可分割的单元添加
            protected_text = text[start:end]
            units.append(protected_text)

            last_end = end

        # c. 处理最后一个受保护范围之后的文本
        if last_end < len(text):
            post_text = text[last_end:]
            segments = re.split(separator_pattern, post_text)
            units.extend([s for s in segments if s])  # 添加所有非空部分

        logger.info(f"Text splitting complete, created {len(units)} final basic units.")
        return units

    def chunk_text(self, text: str) -> List[Chunk]:
        """Chunk text, preserving Markdown structure

        Args:
            text: Text content

        Returns:
            List of text chunks
        """
        if not text:
            logger.warning("Empty text provided for chunking, returning empty list")
            return []

        logger.info(f"Starting text chunking process, text length: {len(text)}")
        logger.info(
            f"Chunking parameters: size={self.chunk_size}, overlap={self.chunk_overlap}"
        )

        # Split text into basic units
        units = self._split_into_units(text)
        logger.info(f"Split text into {len(units)} basic units")

        chunks = []
        current_chunk = []
        current_size = 0
        current_start = 0

        for i, unit in enumerate(units):
            unit_size = len(unit)
            logger.info(f"Processing unit {i + 1}/{len(units)}, size: {unit_size}")

            # If current chunk plus new unit exceeds size limit, create new chunk
            if current_size + unit_size > self.chunk_size and current_chunk:
                chunk_text = "".join(current_chunk)
                chunks.append(
                    Chunk(
                        seq=len(chunks),
                        content=chunk_text,
                        start=current_start,
                        end=current_start + len(chunk_text),
                    )
                )
                logger.info(f"Created chunk {len(chunks)}, size: {len(chunk_text)}")

                # Keep overlap, ensuring structure integrity
                if self.chunk_overlap > 0:
                    # Calculate target overlap size
                    overlap_target = min(self.chunk_overlap, len(chunk_text))
                    logger.info(
                        f"Calculating overlap with target size: {overlap_target}"
                    )

                    # Find complete units from the end
                    overlap_units = []
                    overlap_size = 0

                    for u in reversed(current_chunk):
                        if overlap_size + len(u) > overlap_target:
                            logger.info(
                                f"Overlap target ({overlap_size}/{overlap_target})"
                            )
                            break
                        overlap_units.insert(0, u)
                        overlap_size += len(u)
                        logger.info(f"Added unit to overlap, size: {overlap_size}")

                    # Remove elements from overlap that are included in separators
                    start_index = 0
                    for i, u in enumerate(overlap_units):
                        # Check if u is in separators
                        all_of_separator = True
                        for uu in u:
                            if uu not in self.separators:
                                all_of_separator = False
                                break
                        if all_of_separator:
                            # Remove the first element
                            start_index = i + 1
                            overlap_size = overlap_size - len(u)
                            logger.info(f"Removed separator from overlap: '{u}'")
                        else:
                            break

                    overlap_units = overlap_units[start_index:]
                    logger.info(
                        f"Overlap: {len(overlap_units)} units, {overlap_size} size"
                    )

                    current_chunk = overlap_units
                    current_size = overlap_size
                    # Update start position, considering overlap
                    current_start = current_start + len(chunk_text) - overlap_size
                else:
                    logger.info("No overlap configured, starting fresh chunk")
                    current_chunk = []
                    current_size = 0
                    current_start = current_start + len(chunk_text)

            current_chunk.append(unit)
            current_size += unit_size
            logger.info(
                f"Added unit to current chunk, at {current_size}/{self.chunk_size}"
            )

        # Add the last chunk
        if current_chunk:
            chunk_text = "".join(current_chunk)
            chunks.append(
                Chunk(
                    seq=len(chunks),
                    content=chunk_text,
                    start=current_start,
                    end=current_start + len(chunk_text),
                )
            )
            logger.info(f"Created final chunk {len(chunks)}, size: {len(chunk_text)}")

        logger.info(f"Chunking complete, created {len(chunks)} chunks from text")
        return chunks

    def extract_images_from_chunk(self, chunk: Chunk) -> List[Dict[str, str]]:
        """Extract image information from a chunk

        Args:
            chunk: Document chunk

        Returns:
            List of image information
        """
        logger.info(f"Extracting image information from Chunk #{chunk.seq + 1}")
        text = chunk.content

        # Regex to extract image information from text,
        # support: Markdown images, HTML images
        img_pattern = r'!\[([^\]]*)\]\(([^)]+)\)|<img [^>]*src="([^"]+)" [^>]*>'

        # Extract image information
        img_matches = list(re.finditer(img_pattern, text))
        logger.info(f"Chunk #{chunk.seq + 1} found {len(img_matches)} images")

        images_info = []
        for match_idx, match in enumerate(img_matches):
            # Process image URL
            img_url = match.group(2) if match.group(2) else match.group(3)
            alt_text = match.group(1) if match.group(1) else ""

            # Record image information
            image_info = {
                "original_url": img_url,
                "start": match.start(),
                "end": match.end(),
                "alt_text": alt_text,
                "match_text": text[match.start() : match.end()],
            }
            images_info.append(image_info)

            logger.info(
                f"Image in Chunk #{chunk.seq + 1} {match_idx + 1}: URL={img_url[:50]}..."
                if len(img_url) > 50
                else f"Image in Chunk #{chunk.seq + 1} {match_idx + 1}: URL={img_url}"
            )

        return images_info

    async def download_and_upload_image(
        self, img_url: str
    ) -> Tuple[str, str, Image.Image | None]:
        """Download image and upload to object storage,
        if it's already an object storage path or local path, use directly

        Args:
            img_url: Image URL or local path

        Returns:
            tuple: (original URL, storage URL, image object),
            if failed returns (original URL, None, None)
        """

        try:
            # Check if it's already a storage URL (COS or MinIO)
            is_storage_url = any(
                pattern in img_url
                for pattern in ["cos", "myqcloud.com", "minio", ".s3."]
            )
            if is_storage_url:
                logger.info(f"Image already on COS: {img_url}, no need to re-upload")
                try:
                    # Validate URL to prevent SSRF attacks
                    if not self._is_safe_url(img_url):
                        logger.error(f"URL failed validation check: {img_url}")
                        return img_url, img_url, None
                    # Still need to get image object for OCR processing
                    # Get proxy settings from environment variables
                    proxies = {}
                    if CONFIG.external_http_proxy:
                        proxies["http"] = CONFIG.external_http_proxy
                    if CONFIG.external_https_proxy:
                        proxies["https"] = CONFIG.external_https_proxy

                    response = requests.get(img_url, timeout=5, proxies=proxies)
                    if response.status_code == 200:
                        image = Image.open(io.BytesIO(response.content))
                        return img_url, img_url, image
                    else:
                        logger.warning(
                            f"Failed to get storage image: {response.status_code}"
                        )
                        return img_url, img_url, None
                except Exception as e:
                    logger.error(f"Error getting storage image: {str(e)}")
                    return img_url, img_url, None

            # Check if it's a local file path
            elif os.path.exists(img_url) and os.path.isfile(img_url):
                logger.info(f"Using local image file: {img_url}")
                image = None
                try:
                    # Read local image
                    image = Image.open(img_url)
                    # Upload to storage
                    with open(img_url, "rb") as f:
                        content = f.read()
                    storage_url = self.storage.upload_bytes(content)
                    logger.info(
                        f"Successfully uploaded local image to storage: {storage_url}"
                    )
                    return img_url, storage_url, image
                except Exception as e:
                    logger.error(f"Error processing local image: {str(e)}")
                    if image and hasattr(image, "close"):
                        image.close()
                    return img_url, img_url, None

            # Normal remote URL download handling
            else:
                # Validate URL to prevent SSRF attacks
                if not self._is_safe_url(img_url):
                    logger.error(f"URL failed validation check: {img_url}")
                    return img_url, img_url, None
                # Get proxy settings from environment variables
                proxies = {}
                if CONFIG.external_http_proxy:
                    proxies["http"] = CONFIG.external_http_proxy
                if CONFIG.external_https_proxy:
                    proxies["https"] = CONFIG.external_https_proxy

                logger.info(f"Downloading image {img_url}, using proxy: {proxies}")
                response = requests.get(img_url, timeout=5, proxies=proxies)

                if response.status_code == 200:
                    # Download successful, create image object
                    image = Image.open(io.BytesIO(response.content))
                    try:
                        # Upload to storage using the method in BaseParser
                        storage_url = self.storage.upload_bytes(response.content)
                        logger.info(
                            f"Successfully uploaded image to storage: {storage_url}"
                        )
                        return img_url, storage_url, image
                    finally:
                        # Image will be closed by the caller
                        pass
                else:
                    logger.warning(f"Failed to download image: {response.status_code}")
                    return img_url, img_url, None

        except Exception as e:
            logger.error(f"Error downloading or processing image: {str(e)}")
            return img_url, img_url, None

    async def process_chunk_images_async(
        self, chunk, chunk_idx, total_chunks, image_map=None
    ):
        """Asynchronously process images in a single Chunk

        Args:
            chunk: Chunk object to process
            chunk_idx: Chunk index
            total_chunks: Total number of chunks
            image_map: Optional dictionary mapping image URLs to Image objects

        Returns:
            Processed Chunk object
        """

        logger.info(
            f"Starting to process images in Chunk #{chunk_idx + 1}/{total_chunks}"
        )

        # Extract image information from the Chunk
        images_info = self.extract_images_from_chunk(chunk)
        if not images_info:
            logger.info(f"Chunk #{chunk_idx + 1} found no images")
            return chunk

        # Prepare images that need to be downloaded and processed
        images_to_process = []
        # Map URL to image information
        url_to_info_map = {}

        # Record all image URLs that need to be processed
        for img_info in images_info:
            url = img_info["original_url"]
            url_to_info_map[url] = img_info

        results = []
        download_tasks = []
        # Check if image is already in the image_map
        for img_url in url_to_info_map.keys():
            if image_map and img_url in image_map:
                logger.info(
                    f"Image already in image_map: {img_url}, using cached object"
                )
                image = Image.open(
                    io.BytesIO(endecode.encode_image(image_map[img_url]))
                )
                results.append((img_url, img_url, image))
            else:
                download_task = self.download_and_upload_image(img_url)
                download_tasks.append(download_task)
        # Concurrent download and upload of images,
        # ignore images that are already in the image_map
        results.extend(await asyncio.gather(*download_tasks))

        # Process download results, prepare for OCR processing
        for orig_url, cos_url, image in results:
            if cos_url and image:
                img_info = url_to_info_map[orig_url]
                img_info["cos_url"] = cos_url
                images_to_process.append((image, cos_url))

        # If no images were successfully downloaded and uploaded,
        # return the original Chunk
        if not images_to_process:
            logger.info(
                f"Chunk #{chunk_idx + 1} not found downloaded and uploaded images"
            )
            return chunk

        # Concurrent processing of all images (OCR + caption)
        logger.info(
            f"Processing {len(images_to_process)} images in Chunk #{chunk_idx + 1}"
        )

        # Concurrent processing of all images
        processed_results = await self.process_multiple_images(images_to_process)

        # Process OCR and Caption results
        for ocr_text, caption, img_url in processed_results:
            # Find the corresponding original URL
            for orig_url, info in url_to_info_map.items():
                if info.get("cos_url") == img_url:
                    info["ocr_text"] = ocr_text if ocr_text else ""
                    info["caption"] = caption if caption else ""

                    if ocr_text:
                        logger.info(
                            f"Image OCR extracted {len(ocr_text)} characters: {img_url}"
                        )
                    if caption:
                        logger.info(f"Obtained image description: '{caption}'")
                    break

        # Add processed image information to the Chunk
        processed_images = []
        for img_info in images_info:
            if "cos_url" in img_info:
                processed_images.append(img_info)

        # Update image information in the Chunk
        chunk.images = processed_images

        logger.info(f"Completed image processing in Chunk #{chunk_idx + 1}")
        return chunk

    def process_chunks_images(
        self, chunks: List[Chunk], image_map: Dict[str, str] = {}
    ) -> List[Chunk]:
        """Concurrent processing of images in all Chunks

        Args:
            chunks: List of document chunks

        Returns:
            List of processed document chunks
        """
        logger.info(
            f"Starting concurrent processing of images in all {len(chunks)} chunks"
        )

        if not chunks:
            logger.warning("No chunks to process")
            return chunks

        # Create and run all Chunk concurrent processing tasks
        async def process_all_chunks():
            # Use semaphore to limit concurrency
            semaphore = asyncio.Semaphore(self.max_concurrent_tasks)

            async def process_with_limit(chunk, idx, total):
                """Use semaphore to control concurrent processing of Chunks"""
                async with semaphore:
                    return await self.process_chunk_images_async(
                        chunk, idx, total, image_map
                    )

            # Create tasks for all Chunks
            tasks = [
                process_with_limit(chunk, idx, len(chunks))
                for idx, chunk in enumerate(chunks)
            ]

            # Execute all tasks concurrently
            results = await asyncio.gather(*tasks, return_exceptions=True)

            # Handle possible exceptions
            processed_chunks = []
            for i, result in enumerate(results):
                if isinstance(result, Exception):
                    logger.error(f"Error processing Chunk {i + 1}: {str(result)}")
                    # Keep original Chunk
                    if i < len(chunks):
                        processed_chunks.append(chunks[i])
                else:
                    processed_chunks.append(result)

            return processed_chunks

        # Create event loop and run all tasks
        try:
            # Check if event loop already exists
            try:
                loop = asyncio.get_event_loop()
                if loop.is_closed():
                    loop = asyncio.new_event_loop()
                    asyncio.set_event_loop(loop)
            except RuntimeError:
                # If no event loop, create a new one
                loop = asyncio.new_event_loop()
                asyncio.set_event_loop(loop)

            # Execute processing for all Chunks
            processed_chunks = loop.run_until_complete(process_all_chunks())
            logger.info(
                f"Completed processing of {len(processed_chunks)}/{len(chunks)} chunks"
            )

            return processed_chunks
        except Exception as e:
            logger.error(f"Error during concurrent chunk processing: {str(e)}")
            return chunks
