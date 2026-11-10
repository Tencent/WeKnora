# -*- coding: utf-8 -*-
import logging
import os
from abc import ABC, abstractmethod
from typing import Optional

from docreader.models.document import Document

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


class BaseParser(ABC):
    """Base parser interface.

    After the lightweight refactoring, BaseParser only extracts markdown text
    and raw image references from documents. Chunking, image storage, OCR,
    and VLM caption are handled by the Go App module.
    """

    def __init__(
        self,
        file_name: str = "",
        file_type: Optional[str] = None,
        **kwargs,
    ):
        self.file_name = file_name
        self.file_type = file_type or os.path.splitext(file_name)[1].lstrip(".")

        logger.info(
            "Initializing parser for file=%s, type=%s",
            file_name,
            self.file_type,
        )

    @abstractmethod
    def parse_into_text(self, content: bytes) -> Document:
        """Parse document content into markdown text.

        Returns:
            Document with ``content`` (markdown string) and optional
            ``images`` dict mapping storage-relative paths to base64 data.
        """

    def parse(self, content: bytes) -> Document:
        """Parse document and return markdown + image references.

        No chunking, no OCR, no VLM caption — those are done in Go.
        """
        logger.info(
            "Parsing document with %s, bytes: %d",
            self.__class__.__name__,
            len(content),
        )
        document = self.parse_into_text(content)
        logger.info(
            "Extracted %d characters from %s",
            len(document.content),
            self.file_name,
        )
        return document
