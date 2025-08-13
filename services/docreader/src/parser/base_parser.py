# -*- coding: utf-8 -*-
import re
import os
import asyncio
from typing import List, Dict, Any, Optional, Tuple, Union
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
import logging
import sys
import traceback
import numpy as np
import time
import io
import json
from .ocr_engine import OCREngine
from .image_utils import image_to_base64
from .config import ChunkingConfig
from .storage import create_storage
from PIL import Image

# Add parent directory to Python path for src imports
current_dir = os.path.dirname(os.path.abspath(__file__))
parent_dir = os.path.dirname(current_dir)
if parent_dir not in sys.path:
    sys.path.insert(0, parent_dir)

try:
    from services.docreader.src.parser.caption import Caption
except ImportError:
    # Fallback: try relative import
    try:
        from .caption import Caption
    except ImportError:
        # If both imports fail, set to None
        Caption = None
        logging.warning("Failed to import Caption, image captioning will be unavailable")

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


@dataclass
class Chunk:
    """Chunk result"""

    content: str  # Chunk content
    seq: int  # Chunk sequence number
    start: int  # Chunk start position
    end: int  # Chunk end position
    images: List[Dict[str, Any]] = field(default_factory=list)  # Images in the chunk


@dataclass
class ParseResult:
    """Parse result"""

    text: str  # Extracted text content
    chunks: Optional[List[Chunk]] = None  # Chunk results


class BaseParser(ABC):
    """Base parser interface"""

    # Class variable for shared OCR engine instance
    _ocr_engine = None
    _ocr_engine_failed = False

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
                cls._ocr_engine = OCREngine.get_instance(backend_type=backend_type, **kwargs)
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
        file_type: str = None,
        enable_multimodal: bool = True,
        chunk_size: int = 1000,
        chunk_overlap: int = 200,
        separators: list = ["\n\n", "\n", "。"],
        ocr_backend: str = "paddle",
        ocr_config: dict = None,
        max_image_size: int = 1920,  # Maximum image size
        max_concurrent_tasks: int = 5,  # Max concurrent tasks
        max_chunks: int = 1000,  # Max number of returned chunks
        chunking_config: ChunkingConfig = None,  # Chunking configuration object
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
        self._storage = None
        self.file_name = file_name
        self.file_type = file_type or os.path.splitext(file_name)[1]
        self.enable_multimodal = enable_multimodal
        self.chunk_size = chunk_size
        self.chunk_overlap = chunk_overlap
        self.separators = separators
        self.ocr_backend = os.getenv("OCR_BACKEND", ocr_backend)
        self.ocr_config = ocr_config or {}
        self.max_image_size = max_image_size
        self.max_concurrent_tasks = max_concurrent_tasks
        self.max_chunks = max_chunks
        self.chunking_config = chunking_config
        
        logger.info(
            f"Initializing {self.__class__.__name__} for file: {file_name}, type: {self.file_type}"
        )
        logger.info(
            f"Parser config: chunk_size={chunk_size}, "
            f"overlap={chunk_overlap}, "
            f"multimodal={enable_multimodal}, "
            f"ocr_backend={ocr_backend}, "
            f"max_chunks={max_chunks}"
        )
        # Only initialize Caption service if multimodal is enabled
        if self.enable_multimodal:
            try:
                self.caption_parser = Caption(self.chunking_config.vlm_config)
            except Exception as e:
                logger.warning(f"Failed to initialize Caption service: {str(e)}")
                self.caption_parser = None
        else:
            self.caption_parser = None

    def perform_ocr(self, image):
        """Execute OCR recognition on the image

        Args:
            image: Image object (PIL.Image or numpy array)

        Returns:
            Extracted text string
        """
        start_time = time.time()
        logger.info("Starting OCR recognition")
        resized_image = None

        try:
            # Resize image to avoid processing large images
            resized_image = self._resize_image_if_needed(image)

            # Get OCR engine
            ocr_engine = self.get_ocr_engine(backend_type=self.ocr_backend, **self.ocr_config)
            if ocr_engine is None:
                logger.error(f"OCR engine ({self.ocr_backend}) initialization failed or unavailable, "
                             "skipping OCR recognition")
                return ""

            # Execute OCR prediction
            logger.info(f"Executing OCR prediction (using {self.ocr_backend} engine)")
            # Add extra exception handling
            try:
                ocr_result = ocr_engine.predict(resized_image)
            except RuntimeError as e:
                # Handle common CUDA memory issues or other runtime errors
                logger.error(f"OCR prediction runtime error: {str(e)}")
                return ""
            except Exception as e:
                # Handle other prediction errors
                logger.error(f"Unexpected OCR prediction error: {str(e)}")
                return ""

            process_time = time.time() - start_time
            logger.info(f"OCR recognition completed, time: {process_time:.2f} seconds")
            return ocr_result
        except Exception as e:
            process_time = time.time() - start_time
            logger.error(f"OCR recognition error: {str(e)}, time: {process_time:.2f} seconds")
            return ""
        finally:
            # Release image resources
            if resized_image is not image and hasattr(resized_image, 'close'):
                # Only close the new image we created, not the original image
                resized_image.close()

    def _resize_image_if_needed(self, image):
        """Resize image if it exceeds maximum size limit

        Args:
            image: Image object (PIL.Image or numpy array)

        Returns:
            Resized image object
        """
        try:
            # If it's a PIL Image
            if hasattr(image, 'size'):
                width, height = image.size
                if width > self.max_image_size or height > self.max_image_size:
                    logger.info(f"Resizing PIL image, original size: {width}x{height}")
                    scale = min(self.max_image_size / width, self.max_image_size / height)
                    new_width = int(width * scale)
                    new_height = int(height * scale)
                    resized_image = image.resize((new_width, new_height))
                    logger.info(f"Resized to: {new_width}x{new_height}")
                    return resized_image
                else:
                    logger.info(f"PIL image size {width}x{height} is within limits, no resizing needed")
                    return image
            # If it's a numpy array
            elif hasattr(image, 'shape'):
                height, width = image.shape[:2]
                if width > self.max_image_size or height > self.max_image_size:
                    logger.info(f"Resizing numpy image, original size: {width}x{height}")
                    scale = min(self.max_image_size / width, self.max_image_size / height)
                    new_width = int(width * scale)
                    new_height = int(height * scale)
                    # Use PIL for resizing numpy arrays
                    pil_image = Image.fromarray(image)
                    resized_pil = pil_image.resize((new_width, new_height))
                    resized_image = np.array(resized_pil)
                    logger.info(f"Resized to: {new_width}x{new_height}")
                    return resized_image
                else:
                    logger.info(f"Numpy image size {width}x{height} is within limits, no resizing needed")
                    return image
            else:
                logger.warning(f"Unknown image type: {type(image)}, cannot resize")
                return image
        except Exception as e:
            logger.error(f"Error resizing image: {str(e)}")
            return image

    def process_image(self, image, image_url=None):
        """Process image: first perform OCR, then get caption if text is available

        Args:
            image: Image object (PIL.Image or numpy array)
            image_url: Image URL (if uploaded)

        Returns:
            tuple: (ocr_text, caption, image_url)
            - ocr_text: OCR extracted text
            - caption: Image description (if OCR has text) or empty string
            - image_url: Image URL (if provided)
        """
        logger.info("Starting image processing (OCR + optional caption)")

        # Resize image
        image = self._resize_image_if_needed(image)

        # Perform OCR recognition
        ocr_text = self.perform_ocr(image)
        caption = ""

        if self.caption_parser:
            logger.info(f"OCR successfully extracted {len(ocr_text)} characters, continuing to get caption")
            # Convert image to base64 for caption generation
            img_base64 = image_to_base64(image)
            if img_base64:
                caption = self.get_image_caption(img_base64)
                if caption:
                    logger.info(f"Successfully obtained image caption: {caption}")
                else:
                    logger.warning("Failed to get caption")
            else:
                logger.warning("Failed to convert image to base64")
                caption = ""
        else:
            logger.info("Caption service not initialized, skipping caption retrieval")

        # Release image resources
        del image
        
        return ocr_text, caption, image_url

    async def process_image_async(self, image, image_url=None):
        """Asynchronously process image: first perform OCR, then get caption if text is available

        Args:
            image: Image object (PIL.Image or numpy array)
            image_url: Image URL (if uploaded)

        Returns:
            tuple: (ocr_text, caption, image_url)
            - ocr_text: OCR extracted text
            - caption: Image description (if OCR has text) or empty string
            - image_url: Image URL (if provided)
        """
        logger.info("Starting asynchronous image processing (OCR + optional caption)")
        resized_image = None

        try:
            # Resize image
            resized_image = self._resize_image_if_needed(image)

            # Perform OCR recognition (using run_in_executor to execute synchronous operations in the event loop)
            loop = asyncio.get_event_loop()
            try:
                # Add timeout mechanism to avoid infinite blocking (30 seconds timeout)
                ocr_task = loop.run_in_executor(None, self.perform_ocr, resized_image)
                ocr_text = await asyncio.wait_for(ocr_task, timeout=30.0)
            except asyncio.TimeoutError:
                logger.error("OCR processing timed out (30 seconds), skipping this image")
                ocr_text = ""
            except Exception as e:
                logger.error(f"OCR processing error: {str(e)}")
                ocr_text = ""

            logger.info(f"OCR successfully extracted {len(ocr_text)} characters, continuing to get caption")
            caption = ""
            if self.caption_parser:
                try:
                    # Convert image to base64 for caption generation
                    img_base64 = image_to_base64(resized_image)
                    if img_base64:
                        # Add timeout to avoid blocking caption retrieval (30 seconds timeout)
                        caption_task = self.get_image_caption_async(img_base64)
                        image_data, caption = await asyncio.wait_for(caption_task, timeout=30.0)
                        if caption:
                            logger.info(f"Successfully obtained image caption: {caption}")
                        else:
                            logger.warning("Failed to get caption")
                    else:
                        logger.warning("Failed to convert image to base64")
                        caption = ""
                except asyncio.TimeoutError:
                    logger.warning("Caption retrieval timed out, skipping")
                except Exception as e:
                    logger.error(f"Failed to get caption: {str(e)}")
            else:
                logger.info("Caption service not initialized, skipping caption retrieval")

            return ocr_text, caption, image_url
        finally:
            # Release image resources
            if resized_image is not image and hasattr(resized_image, 'close'):
                # Only close the new image we created, not the original image
                resized_image.close()

    async def process_with_limit(self, idx, image, url, semaphore, current_request_id=None):
        """Function to process a single image using a semaphore"""
        try:
            # Set request ID in the asynchronous task
            if current_request_id:
                try:
                    from utils.request import set_request_id
                    set_request_id(current_request_id)
                    logger.info(f"Asynchronous task {idx+1} setting request ID: {current_request_id}")
                except Exception as e:
                    logger.warning(f"Failed to set request ID in asynchronous task: {str(e)}")
            
            logger.info(f"Waiting to process image {idx+1}")
            async with semaphore:  # Use semaphore to control concurrency
                logger.info(f"Starting to process image {idx+1}")
                result = await self.process_image_async(image, url)
                logger.info(f"Completed processing image {idx+1}")
                return result
        except Exception as e:
            logger.error(f"Error processing image {idx+1}: {str(e)}")
            return ("", "", url)  # Return empty result to avoid overall failure
        finally:
            # Manually release image resources
            if hasattr(image, 'close'):
                image.close()

    async def process_multiple_images(self, images_data):
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

        # Set max concurrency, reduce concurrency to avoid resource contention
        max_concurrency = min(self.max_concurrent_tasks, 5)  # Reduce concurrency to prevent excessive memory usage

        # Use semaphore to limit concurrency
        semaphore = asyncio.Semaphore(max_concurrency)

        # Store results to avoid overall failure due to task failure
        results = []
        
        # Get current request ID to set in each asynchronous task
        current_request_id = None
        try:
            from utils.request import get_request_id
            current_request_id = get_request_id()
            logger.info(f"Capturing current request ID before async processing: {current_request_id}")
        except Exception as e:
            logger.warning(f"Failed to get current request ID: {str(e)}")

        # Create all tasks, but use semaphore to limit actual concurrency
        tasks = [
            self.process_with_limit(i, img, url, semaphore, current_request_id) 
            for i, (img, url) in enumerate(images_data)
        ]

        try:
            # Execute all tasks, but set overall timeout
            completed_results = await asyncio.gather(*tasks, return_exceptions=True)

            # Handle possible exception results
            for i, result in enumerate(completed_results):
                if isinstance(result, Exception):
                    logger.error(f"Image {i+1} processing returned an exception: {str(result)}")
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

        logger.info(f"Completed concurrent processing of {len(results)}/{len(images_data)} images")
        return results

    def decode_bytes(self, content: bytes) -> str:
        """Intelligently decode byte stream, supports multiple encodings

        Tries to decode in common encodings, if all fail, uses latin-1 as fallback

        Args:
            content: Byte stream to decode

        Returns:
            Decoded string
        """
        logger.info(f"Attempting to decode bytes of length: {len(content)}")
        # Common encodings, sorted by priority
        encodings = ["utf-8", "gb18030", "gb2312", "gbk", "big5", "ascii", "latin-1"]
        text = None

        # Try decoding with each encoding format
        for encoding in encodings:
            try:
                text = content.decode(encoding)
                logger.info(f"Successfully decoded content using {encoding} encoding")
                break
            except UnicodeDecodeError:
                logger.info(f"Failed to decode using {encoding} encoding")
                continue

        # If all encodings fail, use latin-1 as fallback
        if text is None:
            text = content.decode("latin-1")
            logger.warning(
                f"Unable to determine correct encoding, using latin-1 as fallback. "
                f"This may cause character issues."
            )

        logger.info(f"Decoded text length: {len(text)} characters")
        return text

    def get_image_caption(self, image_data: str) -> str:
        """Get image description

        Args:
            image_data: Image data (base64 encoded string or URL)

        Returns:
            Image description
        """
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

    async def get_image_caption_async(self, image_data: str) -> Tuple[str, str]:
        """Asynchronously get image description

        Args:
            image_data: Image data (base64 encoded string or URL)

        Returns:
            Tuple[str, str]: Image data and corresponding description
        """
        caption = self.get_image_caption(image_data)
        return image_data, caption

    def __init_storage(self):
        """Initialize storage client based on configuration"""
        if self._storage is None:
            storage_config = self.chunking_config.storage_config if self.chunking_config else None
            self._storage = create_storage(storage_config)
            logger.info(f"Initialized storage client: {self._storage.__class__.__name__}")
        return self._storage

    def upload_file(self, file_path: str) -> str:
        """Upload file to object storage

        Args:
            file_path: File path

        Returns:
            File URL
        """
        logger.info(f"Uploading file: {file_path}")
        try:
            storage = self.__init_storage()
            return storage.upload_file(file_path)
        except Exception as e:
            logger.error(f"Failed to upload file: {str(e)}")
            return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        """Upload bytes to object storage

        Args:
            content: Byte content to upload
            file_ext: File extension

        Returns:
            File URL
        """
        logger.info(f"Uploading bytes content, size: {len(content)} bytes")
        try:
            storage = self.__init_storage()
            return storage.upload_bytes(content, file_ext)
        except Exception as e:
            logger.error(f"Failed to upload bytes to storage: {str(e)}")
            traceback.print_exc()
            return ""

    @abstractmethod
    def parse_into_text(self, content: bytes) -> Union[str, Tuple[str, Dict[str, Any]]]:
        """Parse document content

        Args:
            content: Document content

        Returns:
            Either a string containing the parsed text, or a tuple of (text, image_map)
            where image_map is a dict mapping image URLs to Image objects
        """
        pass

    def parse(self, content: bytes) -> ParseResult:
        """Parse document content

        Args:
            content: Document content

        Returns:
            Parse result
        """
        logger.info(
            f"Parsing document with {self.__class__.__name__}, content size: {len(content)} bytes"
        )
        parse_result = self.parse_into_text(content)
        if isinstance(parse_result, tuple):
            text, image_map = parse_result
        else:
            text = parse_result
            image_map = {}
        logger.info(f"Extracted {len(text)} characters of text from {self.file_name}")
        logger.info(f"Beginning chunking process for text")
        chunks = self.chunk_text(text)
        logger.info(f"Created {len(chunks)} chunks from document")
        
        # Limit the number of returned chunks
        if len(chunks) > self.max_chunks:
            logger.warning(f"Limiting chunks from {len(chunks)} to maximum {self.max_chunks}")
            chunks = chunks[:self.max_chunks]
        
        # If multimodal is enabled and file type is supported, process images in each chunk
        if self.enable_multimodal:
            # Get file extension and convert to lowercase
            file_ext = (
                os.path.splitext(self.file_name)[1].lower()
                if self.file_name
                else (
                    self.file_type.lower()
                    if self.file_type
                    else ""
                )
            )
            
            # Define allowed file types for image processing
            allowed_types = [
                '.pdf',            # PDF files
                '.md', '.markdown',  # Markdown files
                '.doc', '.docx',     # Word documents
                # Image files
                '.jpg', '.jpeg', '.png', '.gif', '.bmp', '.tiff', '.webp'
            ]
            
            if file_ext in allowed_types:
                logger.info(f"Processing images in each chunk for file type: {file_ext}")
                chunks = self.process_chunks_images(chunks, image_map)
            else:
                logger.info(f"Skipping image processing for unsupported file type: {file_ext}")
            
        return ParseResult(text=text, chunks=chunks)

    def _split_into_units(self, text: str) -> List[str]:
        """Split text into basic units, preserving Markdown structure

        Args:
            text: Text content

        Returns:
            List of basic units
        """
        logger.info(f"Splitting text into basic units, text length: {len(text)}")
        # Match Markdown image syntax
        image_pattern = r"!\[.*?\]\(.*?\)"
        # Match Markdown link syntax
        link_pattern = r"\[.*?\]\(.*?\)"

        # First, find all structures that need to be kept intact
        images = re.finditer(image_pattern, text)
        links = re.finditer(link_pattern, text)

        # Record the start and end positions of all structures that need to be kept intact
        protected_ranges = []
        for match in images:
            protected_ranges.append((match.start(), match.end()))
        for match in links:
            protected_ranges.append((match.start(), match.end()))

        # Sort by start position
        protected_ranges.sort(key=lambda x: x[0])
        logger.info(f"Found {len(protected_ranges)} protected ranges (images/links)")

        # Remove ranges that are completely within other ranges, keep the largest range
        for start, end in protected_ranges[:]:  # Create a copy to iterate over
            is_inner = False
            for start2, end2 in protected_ranges:
                if (
                    (start > start2 and end < end2)
                    or (start > start2 and end <= end2)
                    or (start >= start2 and end < end2)
                ):
                    is_inner = True
                    break
            if is_inner:
                protected_ranges.remove((start, end))
                logger.info(f"Removed inner protected range: ({start}, {end})")

        logger.info(f"After cleanup: {len(protected_ranges)} protected ranges remain")

        # Split text, avoiding protected areas
        units = []
        last_end = 0

        for start, end in protected_ranges:
            # Add text before protected range
            if start <= last_end:
                continue
            pre_text = text[last_end:start]
            if not pre_text.strip():
                continue
            logger.info(
                f"Processing text segment before protected range, length: {len(pre_text)}"
            )
            separator_pattern = (
                f"({'|'.join(re.escape(s) for s in self.separators)})"
            )
            segments = re.split(separator_pattern, pre_text)
            for unit in segments:
                if len(unit) <= self.chunk_size:
                    units.append(unit)
                else:
                    # Split further by English .
                    logger.info(
                        f"Unit exceeds chunk size ({len(unit)}>{self.chunk_size}), splitting further"
                    )
                    separators = ["."]
                    sep_pattern = (
                        f"({'|'.join(re.escape(s) for s in separators)})"
                    )
                    additional_units = re.split(sep_pattern, unit)
                    units.extend(additional_units)
                    logger.info(
                        f"Split into {len(additional_units)} additional units"
                    )

            # Add protected range
            protected_text = text[start:end]
            logger.info(
                f"Adding protected range: {protected_text[:30]}..."
                if len(protected_text) > 30
                else f"Adding protected range: {protected_text}"
            )
            units.append(protected_text)

            last_end = end

        # Add text after the last protected range
        if last_end < len(text):
            post_text = text[last_end:]
            if post_text.strip():
                logger.info(
                    f"Processing final text segment after all protected ranges, length: {len(post_text)}"
                )
                separator_pattern = (
                    f"({'|'.join(re.escape(s) for s in self.separators)})"
                )
                segments = re.split(separator_pattern, post_text)
                for unit in segments:
                    if len(unit) <= self.chunk_size:
                        units.append(unit)
                    else:
                        # Split further by English .
                        logger.info(
                            f"Final unit exceeds chunk size ({len(unit)}>{self.chunk_size}), splitting"
                        )
                        separators = ["."]
                        sep_pattern = f"({'|'.join(re.escape(s) for s in separators)})"
                        additional_units = re.split(sep_pattern, unit)
                        units.extend(additional_units)
                        logger.info(
                            f"Split into {len(additional_units)} additional units"
                        )

        logger.info(f"Text splitting complete, created {len(units)} basic units")
        return units

    def _find_complete_units(self, units: List[str], target_size: int) -> List[str]:
        """Find a list of complete units that do not exceed the target size

        Args:
            units: List of units
            target_size: Target size

        Returns:
            List of complete units
        """
        logger.info(f"Finding complete units with target size: {target_size}")
        result = []
        current_size = 0

        for unit in units:
            unit_size = len(unit)
            if current_size + unit_size > target_size and result:
                logger.info(
                    f"Reached target size limit at {current_size} characters, stopping"
                )
                break
            result.append(unit)
            current_size += unit_size
            logger.info(
                f"Added unit of size {unit_size}, current total: {current_size}/{target_size}"
            )

        logger.info(
            f"Found {len(result)} complete units totaling {current_size} characters"
        )
        return result

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
            logger.info(f"Processing unit {i+1}/{len(units)}, size: {unit_size}")

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
                                f"Reached overlap target ({overlap_size}/{overlap_target})"
                            )
                            break
                        overlap_units.insert(0, u)
                        overlap_size += len(u)
                        logger.info(
                            f"Added unit to overlap, current overlap size: {overlap_size}"
                        )

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
                        f"Final overlap: {len(overlap_units)} units, {overlap_size} characters"
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
                f"Added unit to current chunk, now at {current_size}/{self.chunk_size} characters"
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
            List of image information, each element contains image URL and match position
        """
        logger.info(f"Extracting image information from Chunk #{chunk.seq}")
        text = chunk.content
        
        # Regex to extract image information from text, supporting Markdown images and HTML images
        img_pattern = r'!\[([^\]]*)\]\(([^)]+)\)|<img [^>]*src="([^"]+)" [^>]*>'
        
        # Extract image information
        img_matches = list(re.finditer(img_pattern, text))
        logger.info(f"Chunk #{chunk.seq} found {len(img_matches)} images")
        
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
                "match_text": text[match.start():match.end()]
            }
            images_info.append(image_info)
            
            logger.info(
                f"Image in Chunk #{chunk.seq} {match_idx+1}: "
                f"URL={img_url[:50]}..."
                if len(img_url) > 50
                else f"Image in Chunk #{chunk.seq} {match_idx+1}: URL={img_url}"
            )
            
        return images_info

    async def download_and_upload_image(self, img_url: str, current_request_id=None, image_map=None):
        """Download image and upload to object storage, if it's already an object storage path or local path, use directly

        Args:
            img_url: Image URL or local path
            current_request_id: Current request ID
            image_map: Optional dictionary mapping image URLs to Image objects

        Returns:
            tuple: (original URL, storage URL, image object), if failed returns (original URL, None, None)
        """
        # Set request ID context in the asynchronous task
        try:
            if current_request_id:
                from utils.request import set_request_id
                set_request_id(current_request_id)
                logger.info(f"Asynchronous task setting request ID: {current_request_id}")
        except Exception as e:
            logger.warning(f"Failed to set request ID in asynchronous task: {str(e)}")
        
        try:
            import requests
            from PIL import Image
            import io
            
            # Check if image is already in the image_map
            if image_map and img_url in image_map:
                logger.info(f"Image already in image_map: {img_url}, using cached object")
                return img_url, img_url, image_map[img_url]
                
            # Check if it's already a storage URL (COS or MinIO)
            is_storage_url = any(pattern in img_url for pattern in ["cos", "myqcloud.com", "minio", ".s3."])
            if is_storage_url:
                logger.info(f"Image already on COS: {img_url}, no need to re-upload")
                try:
                    # Still need to get image object for OCR processing
                    # Get proxy settings from environment variables
                    http_proxy = os.environ.get("EXTERNAL_HTTP_PROXY")
                    https_proxy = os.environ.get("EXTERNAL_HTTPS_PROXY")
                    proxies = {}
                    if http_proxy:
                        proxies["http"] = http_proxy
                    if https_proxy:
                        proxies["https"] = https_proxy
                    
                    response = requests.get(img_url, timeout=5, proxies=proxies)
                    if response.status_code == 200:
                        image = Image.open(io.BytesIO(response.content))
                        try:
                            return img_url, img_url, image
                        finally:
                            # Ensure image resources are also released after the function returns
                            # Image will be closed by the caller
                            pass
                    else:
                        logger.warning(f"Failed to get storage image: {response.status_code}")
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
                    with open(img_url, 'rb') as f:
                        content = f.read()
                    storage_url = self.upload_bytes(content)
                    logger.info(f"Successfully uploaded local image to storage: {storage_url}")
                    return img_url, storage_url, image
                except Exception as e:
                    logger.error(f"Error processing local image: {str(e)}")
                    if image and hasattr(image, 'close'):
                        image.close()
                    return img_url, None, None
            
            # Normal remote URL download handling
            else:
                # Get proxy settings from environment variables
                http_proxy = os.environ.get("EXTERNAL_HTTP_PROXY")
                https_proxy = os.environ.get("EXTERNAL_HTTPS_PROXY")
                proxies = {}
                if http_proxy:
                    proxies["http"] = http_proxy
                if https_proxy:
                    proxies["https"] = https_proxy
                    
                logger.info(f"Downloading image {img_url}, using proxy: {proxies if proxies else 'None'}")
                response = requests.get(img_url, timeout=5, proxies=proxies)
                
                if response.status_code == 200:
                    # Download successful, create image object
                    image = Image.open(io.BytesIO(response.content))
                    try:
                        # Upload to storage using the method in BaseParser
                        storage_url = self.upload_bytes(response.content)
                        logger.info(f"Successfully uploaded image to storage: {storage_url}")
                        return img_url, storage_url, image
                    finally:
                        # Image will be closed by the caller
                        pass
                else:
                    logger.warning(f"Failed to download image: {response.status_code}")
                    return img_url, None, None
                        
        except Exception as e:
            logger.error(f"Error downloading or processing image: {str(e)}")
            return img_url, None, None

    async def process_chunk_images_async(self, chunk, chunk_idx, total_chunks, current_request_id=None, image_map=None):
        """Asynchronously process images in a single Chunk

        Args:
            chunk: Chunk object to process
            chunk_idx: Chunk index
            total_chunks: Total number of chunks
            current_request_id: Current request ID

        Returns:
            Processed Chunk object
        """
        # Set request ID context in the asynchronous task
        try:
            if current_request_id:
                from utils.request import set_request_id
                set_request_id(current_request_id)
                logger.info(f"Chunk processing task #{chunk_idx+1} setting request ID: {current_request_id}")
        except Exception as e:
            logger.warning(f"Failed to set request ID in Chunk processing task: {str(e)}")
            
        logger.info(f"Starting to process images in Chunk #{chunk_idx+1}/{total_chunks}")
        
        # Extract image information from the Chunk
        images_info = self.extract_images_from_chunk(chunk)
        if not images_info:
            logger.info(f"Chunk #{chunk_idx+1} found no images")
            return chunk
            
        # Prepare images that need to be downloaded and processed
        images_to_process = []
        url_to_info_map = {}  # Map URL to image information
        
        # Record all image URLs that need to be processed
        for img_info in images_info:
            url = img_info["original_url"]
            url_to_info_map[url] = img_info
        
        # Create an asynchronous event loop (current loop)
        loop = asyncio.get_event_loop()
        
        # Concurrent download and upload of images
        tasks = [self.download_and_upload_image(url, current_request_id, image_map) for url in url_to_info_map.keys()]
        results = await asyncio.gather(*tasks)
        
        # Process download results, prepare for OCR processing
        for orig_url, cos_url, image in results:
            if cos_url and image:
                img_info = url_to_info_map[orig_url]
                img_info["cos_url"] = cos_url
                images_to_process.append((image, cos_url))
        
        # If no images were successfully downloaded and uploaded, return the original Chunk
        if not images_to_process:
            logger.info(f"Chunk #{chunk_idx+1} found no successfully downloaded and uploaded images")
            return chunk
            
        # Concurrent processing of all images (OCR + caption)
        logger.info(f"Processing {len(images_to_process)} images in Chunk #{chunk_idx+1}")
        
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
                        logger.info(f"Image OCR extracted {len(ocr_text)} characters: {img_url}")
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
        
        logger.info(f"Completed image processing in Chunk #{chunk_idx+1}")
        return chunk

    def process_chunks_images(self, chunks: List[Chunk], image_map=None) -> List[Chunk]:
        """Concurrent processing of images in all Chunks

        Args:
            chunks: List of document chunks

        Returns:
            List of processed document chunks
        """
        logger.info(f"Starting concurrent processing of images in all {len(chunks)} chunks")
        
        if not chunks:
            logger.warning("No chunks to process")
            return chunks
        
        # Get current request ID to pass to asynchronous tasks
        current_request_id = None
        try:
            from utils.request import get_request_id
            current_request_id = get_request_id()
            logger.info(f"Capturing current request ID before async processing: {current_request_id}")
        except Exception as e:
            logger.warning(f"Failed to get current request ID: {str(e)}")
        
        # Create and run all Chunk concurrent processing tasks
        async def process_all_chunks():
            # Set max concurrency, reduce concurrency to avoid resource contention
            max_concurrency = min(self.max_concurrent_tasks, 5)  # Reduce concurrency
            # Use semaphore to limit concurrency
            semaphore = asyncio.Semaphore(max_concurrency)
            
            async def process_with_limit(chunk, idx, total):
                """Use semaphore to control concurrent processing of Chunks"""
                async with semaphore:
                    return await self.process_chunk_images_async(chunk, idx, total, current_request_id, image_map)
            
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
                    logger.error(f"Error processing Chunk {i+1}: {str(result)}")
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
            logger.info(f"Successfully completed concurrent processing of {len(processed_chunks)}/{len(chunks)} chunks")
            
            return processed_chunks
        except Exception as e:
            logger.error(f"Error during concurrent chunk processing: {str(e)}")
            return chunks
