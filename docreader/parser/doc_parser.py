import logging
import os
import subprocess
from typing import List, Optional

import textract

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.docx2_parser import Docx2Parser
from docreader.utils.tempfile import TempDirContext, TempFileContext

logger = logging.getLogger(__name__)


class SandboxExecutor:
    """Sandbox executor for running commands with proxy configuration"""

    def __init__(self, proxy: Optional[str] = None, default_timeout: int = 60):
        """Initialize sandbox executor with configuration

        Args:
            proxy: Proxy URL to use for network access. If None, will use WEB_PROXY environment variable
            default_timeout: Default timeout in seconds for command execution
        """
        # Get proxy from parameter, environment variable, or use default blocking proxy
        # Use 'or None' to convert empty string to None, then apply default value
        self.proxy = proxy or CONFIG.external_https_proxy or "http://128.0.0.1:1"
        self.default_timeout = default_timeout

    def execute_in_sandbox(self, cmd: List[str]) -> tuple:
        """Execute command in sandbox with proxy configuration

        Args:
            cmd: Command to execute

        Returns:
            Tuple of (stdout, stderr, returncode)
        """
        # Try different sandbox methods in order of preference
        sandbox_methods = [
            self._execute_with_proxy,
        ]

        for method in sandbox_methods:
            try:
                return method(cmd)
            except Exception as e:
                logger.warning(f"Sandbox method {method.__name__} failed: {e}")
                continue

        raise RuntimeError("All sandbox methods failed")

    def _execute_with_proxy(self, cmd: List[str]) -> tuple:
        """Execute command with proxy configuration

        Args:
            cmd: Command to execute

        Returns:
            Tuple of (stdout, stderr, returncode)
        """
        # Set up environment with proxy configuration
        env = os.environ.copy()
        if self.proxy:
            env["http_proxy"] = self.proxy
            env["https_proxy"] = self.proxy
            env["HTTP_PROXY"] = self.proxy
            env["HTTPS_PROXY"] = self.proxy

        logger.info(f"Executing command with proxy: {' '.join(cmd)}")
        if self.proxy:
            logger.info(f"Using proxy: {self.proxy}")

        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
        )

        try:
            stdout, stderr = process.communicate(timeout=self.default_timeout)
            return stdout, stderr, process.returncode
        except subprocess.TimeoutExpired:
            process.kill()
            raise RuntimeError(
                f"Command execution timeout after {self.default_timeout} seconds"
            )


logger = logging.getLogger(__name__)


class DocParser(Docx2Parser):
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

    def _try_convert_doc_to_docx(self, doc_path: str) -> Optional[bytes]:
        """Convert DOC file to DOCX format

        Uses LibreOffice/OpenOffice for conversion

        Args:
            doc_path: DOC file path

        Returns:
            Byte stream of DOCX file content, or None if conversion fails
        """
        logger.info(f"Converting DOC to DOCX: {doc_path}")

        # Check if LibreOffice or OpenOffice is installed
        soffice_path = self._try_find_soffice()
        if not soffice_path:
            return None

        # Execute conversion command
        logger.info(f"Using {soffice_path} to convert DOC to DOCX")

        # Create a temporary directory to store the converted file
        with TempDirContext() as temp_dir:
            cmd = [
                soffice_path,
                "--headless",
                "--convert-to",
                "docx",
                "--outdir",
                temp_dir,
                doc_path,
            ]
            logger.info(f"Running command in sandbox: {' '.join(cmd)}")

            # Execute in sandbox with proxy configuration
            stdout, stderr, returncode = self.sandbox_executor.execute_in_sandbox(cmd)

            if returncode != 0:
                logger.warning(
                    f"Error converting DOC to DOCX: {stderr.decode('utf-8')}"
                )
                return None

            # Find the converted file
            docx_file = [
                file for file in os.listdir(temp_dir) if file.endswith(".docx")
            ]
            logger.info(f"Found {len(docx_file)} DOCX file(s) in temporary directory")
            for file in docx_file:
                converted_file = os.path.join(temp_dir, file)
                logger.info(f"Found converted file: {converted_file}")

                # Read the converted file content
                with open(converted_file, "rb") as f:
                    docx_content = f.read()
                    logger.info(
                        f"Successfully read DOCX file, size: {len(docx_content)}"
                    )
                    return docx_content
        return None

    def _try_find_executable_path(
        self,
        executable_name: str,
        possible_path: List[str] = [],
        environment_variable: List[str] = [],
    ) -> Optional[str]:
        """Find executable path
        Args:
            executable_name: Executable name
            possible_path: List of possible paths
            environment_variable: List of environment variables to check
            Returns:
                Executable path, or None if not found
        """
        # Common executable paths
        paths: List[str] = []
        paths.extend(possible_path)
        paths.extend(os.environ.get(env_var, "") for env_var in environment_variable)
        paths = list(set(paths))

        # Check if path is set in environment variable
        for path in paths:
            if os.path.exists(path):
                logger.info(f"Found {executable_name} at {path}")
                return path

        # Try to find in PATH
        result = subprocess.run(
            ["which", executable_name], capture_output=True, text=True
        )
        if result.returncode == 0 and result.stdout.strip():
            path = result.stdout.strip()
            logger.info(f"Found {executable_name} at {path}")
            return path

        logger.warning(f"Failed to find {executable_name}")
        return None

    def _try_find_soffice(self) -> Optional[str]:
        """Find LibreOffice/OpenOffice executable path

        Returns:
            Executable path, or None if not found
        """
        # Common LibreOffice/OpenOffice executable paths
        possible_paths = [
            # Linux
            "/usr/bin/soffice",
            "/usr/lib/libreoffice/program/soffice",
            "/opt/libreoffice25.2/program/soffice",
            # macOS
            "/Applications/LibreOffice.app/Contents/MacOS/soffice",
            # Windows
            "C:\\Program Files\\LibreOffice\\program\\soffice.exe",
            "C:\\Program Files (x86)\\LibreOffice\\program\\soffice.exe",
        ]
        return self._try_find_executable_path(
            executable_name="soffice",
            possible_path=possible_paths,
            environment_variable=["LIBREOFFICE_PATH"],
        )

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
