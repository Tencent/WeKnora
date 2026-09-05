import logging
from typing import Optional

import textract

from docreader.parser.legacy_doc import LegacyDocConverter, SandboxExecutor
from docreader.models.document import Document
from docreader.parser.docx2_parser import Docx2Parser
from docreader.utils.tempfile import TempFileContext

logger = logging.getLogger(__name__)


class DocParser(Docx2Parser, LegacyDocConverter):
    """DOC document parser"""

    def __init__(self, *args, **kwargs):
        """Initialize DOC parser with sandbox executor"""
        super().__init__(*args, **kwargs)
        self.sandbox_executor = SandboxExecutor()

    def parse_into_text(self, content: bytes) -> Document:
        logger.info(f"Parsing DOC document, content size: {len(content)} bytes")

        handle_chain = [
            # 1. Try to convert to docx format to extract images
            self._parse_with_docx,
            # 2. If image extraction is not needed or conversion failed,
            # try using antiword to extract text
            self._parse_with_antiword,
            # 3. If antiword extraction fails, use textract
            # NOTE: _parse_with_textract is disabled due to SSRF vulnerability
            # self._parse_with_textract,
        ]

        # Save byte content as a temporary file
        with TempFileContext(content, ".doc") as temp_file_path:
            for handle in handle_chain:
                try:
                    document = handle(temp_file_path)
                    if document:
                        return document
                except Exception as e:
                    logger.warning(f"Failed to parse DOC with {handle.__name__} {e}")

            return Document(content="")

    def _parse_with_docx(self, temp_file_path: str) -> Document:
        logger.info("Multimodal enabled, attempting to extract images from DOC")

        docx_content = self._try_convert_doc_to_docx(temp_file_path)
        if not docx_content:
            raise RuntimeError("Failed to convert DOC to DOCX")

        logger.info("Successfully converted DOC to DOCX, using DocxParser")
        # Use existing DocxParser to parse the converted docx
        document = super(Docx2Parser, self).parse_into_text(docx_content)
        logger.info(f"Extracted {len(document.content)} characters using DocxParser")
        return document

    def _parse_with_antiword(self, temp_file_path: str) -> Document:
        logger.info("Attempting to parse DOC file with antiword")

        # Check if antiword is installed
        antiword_path = self._try_find_antiword()
        if not antiword_path:
            raise RuntimeError("antiword not found in PATH")

        # Use antiword to extract text directly in sandbox
        cmd = [antiword_path, temp_file_path]
        logger.info("Executing antiword in sandbox with proxy configuration")

        stdout, stderr, returncode = self.sandbox_executor.execute_in_sandbox(cmd)

        if returncode != 0:
            raise RuntimeError(
                f"antiword extraction failed: {stderr.decode('utf-8', errors='ignore')}"
            )
        text = stdout.decode("utf-8", errors="ignore")
        logger.info(f"Successfully extracted {len(text)} characters using antiword")
        return Document(content=text)

    def _parse_with_textract(self, temp_file_path: str) -> Document:
        logger.info(f"Parsing DOC file with textract: {temp_file_path}")
        text = textract.process(temp_file_path, method="antiword").decode("utf-8")
        logger.info(f"Successfully extracted {len(text)} bytes of DOC using textract")
        return Document(content=str(text))

    def _try_find_antiword(self) -> Optional[str]:
        """Find antiword executable path

        Returns:
            Executable path, or None if not found
        """
        # Common antiword executable paths
        possible_paths = [
            # Linux/macOS
            "/usr/bin/antiword",
            "/usr/local/bin/antiword",
            # Windows
            "C:\\Program Files\\Antiword\\antiword.exe",
            "C:\\Program Files (x86)\\Antiword\\antiword.exe",
        ]
        return self._try_find_executable_path(
            executable_name="antiword",
            possible_path=possible_paths,
            environment_variable=["ANTIWORD_PATH"],
        )


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    file_name = "/path/to/your/test.doc"
    logger.info(f"Processing file: {file_name}")
    doc_parser = DocParser(
        file_name=file_name,
        enable_multimodal=True,
        chunk_size=512,
        chunk_overlap=60,
    )
    with open(file_name, "rb") as f:
        content = f.read()

    document = doc_parser.parse_into_text(content)
    logger.info(f"Processing complete, extracted text length: {len(document.content)}")
    logger.info(f"Sample text: {document.content[:200]}...")
