"""Shared transient LibreOffice conversion for parsing and knowledge preview."""
import io
import logging
import os
import signal
import subprocess
import time
import zipfile
from pathlib import Path
from typing import List, Optional

from docreader.config import CONFIG
from docreader.utils.tempfile import TempDirContext, TempFileContext

logger = logging.getLogger(__name__)
DOCX_MIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
OLE_MAGIC = bytes.fromhex("d0cf11e0a1b11ae1")

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

    def execute_in_sandbox(self, cmd: List[str], timeout=None, is_active=None) -> tuple:
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
                return method(cmd, timeout, is_active)
            except Exception as e:
                logger.warning(f"Sandbox method {method.__name__} failed: {e}")
                continue

        raise RuntimeError("All sandbox methods failed")

    def _execute_with_proxy(self, cmd: List[str], timeout=None, is_active=None) -> tuple:
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
            start_new_session=os.name == "posix",
        )

        deadline = time.monotonic() + (timeout if timeout is not None else self.default_timeout)
        try:
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0 or (is_active is not None and not is_active()):
                    raise TimeoutError("Conversion deadline exceeded")
                try:
                    stdout, stderr = process.communicate(timeout=min(remaining, 0.2))
                    return stdout, stderr, process.returncode
                except subprocess.TimeoutExpired:
                    continue
        finally:
            if os.name == "posix":
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            elif process.poll() is None:
                process.kill()
            process.communicate()  # reap before temporary profiles are removed


class LegacyDocConverter:
    def __init__(self):
        self.sandbox_executor = SandboxExecutor()

    def normalize(self, content: bytes, timeout: float, max_bytes: int, is_active=None) -> bytes:
        if not content.startswith(OLE_MAGIC) or len(content) > max_bytes:
            raise ValueError("Unsupported legacy Word input")
        deadline = time.monotonic() + min(timeout, 25.0)
        with TempFileContext(content, ".doc") as source:
            result = self._try_convert_doc_to_docx(source, deadline, is_active, max_bytes)
        if not result:
            raise ValueError("Legacy Word preview unavailable")
        # Validate the representation without expanding archive members.
        with zipfile.ZipFile(io.BytesIO(result)) as package:
            if not {"[Content_Types].xml", "word/document.xml"}.issubset(package.namelist()):
                raise ValueError("Invalid DOCX representation")
        return result

    def _try_convert_doc_to_docx(self, doc_path: str, deadline=None, is_active=None, max_bytes=None) -> Optional[bytes]:
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

        # LibreOffice shares a single user profile by default, so concurrent
        # `soffice` invocations contend for the same profile lock and the loser
        # silently fails to convert. Give each attempt a dedicated profile dir
        # and retry a few times so concurrent requests don't fall back to the
        # lower-fidelity antiword path.
        max_attempts = 3
        for attempt in range(1, max_attempts + 1):
            if deadline is not None and (time.monotonic() >= deadline or (is_active and not is_active())):
                raise TimeoutError("Conversion deadline exceeded")
            # Create a temporary directory to store the converted file
            with TempDirContext() as temp_dir, TempDirContext() as profile_dir:
                user_installation = Path(profile_dir).as_uri()
                cmd = [
                    soffice_path,
                    "--headless",
                    f"-env:UserInstallation={user_installation}",
                    "--convert-to",
                    "docx",
                    "--outdir",
                    temp_dir,
                    doc_path,
                ]
                logger.info(
                    f"Running command in sandbox (attempt {attempt}/{max_attempts}): "
                    f"{' '.join(cmd)}"
                )

                # Execute in sandbox with proxy configuration
                stdout, stderr, returncode = self.sandbox_executor.execute_in_sandbox(
                    cmd, timeout=max(0, deadline - time.monotonic()) if deadline is not None else None, is_active=is_active
                )

                if returncode != 0:
                    logger.warning(
                        f"Error converting DOC to DOCX (attempt {attempt}/"
                        f"{max_attempts}): {stderr.decode('utf-8', errors='ignore')}"
                    )
                    if attempt < max_attempts:
                        time.sleep(0.5 * attempt)
                        continue
                    return None

                # Find the converted file
                docx_file = [
                    file for file in os.listdir(temp_dir) if file.endswith(".docx")
                ]
                logger.info(
                    f"Found {len(docx_file)} DOCX file(s) in temporary directory"
                )
                for file in docx_file:
                    converted_file = os.path.join(temp_dir, file)
                    logger.info(f"Found converted file: {converted_file}")

                    # Read the converted file content
                    with open(converted_file, "rb") as f:
                        docx_content = f.read(max_bytes + 1) if max_bytes is not None else f.read()
                        if max_bytes is not None and len(docx_content) > max_bytes:
                            raise ValueError("Preview too large")
                        logger.info(
                            f"Successfully read DOCX file, size: {len(docx_content)}"
                        )
                        return docx_content

                # Conversion reported success but produced no docx; retry.
                logger.warning(
                    f"No DOCX produced despite success (attempt {attempt}/"
                    f"{max_attempts})"
                )
                if attempt < max_attempts:
                    time.sleep(0.5 * attempt)
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
