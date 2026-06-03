"""LibreOffice helpers for legacy binary PowerPoint (.ppt) uploads."""

from __future__ import annotations

import logging
import os
import subprocess
import tempfile
import time
from pathlib import Path
from docreader.parser.excel_convert import find_soffice

logger = logging.getLogger(__name__)

_OLE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"
_ZIP_MAGIC = b"PK\x03\x04"


def is_ole_compound(content: bytes) -> bool:
    return len(content) >= len(_OLE_MAGIC) and content.startswith(_OLE_MAGIC)


def is_zip_openxml(content: bytes) -> bool:
    return len(content) >= len(_ZIP_MAGIC) and content.startswith(_ZIP_MAGIC)


def needs_ppt_to_pptx_conversion(content: bytes, file_type: str | None) -> bool:
    """True when content is legacy .ppt (OLE), not modern .pptx (ZIP)."""
    ext = (file_type or "").lstrip(".").lower()
    if ext == "pptx" or is_zip_openxml(content):
        return False
    if ext == "ppt" or is_ole_compound(content):
        return is_ole_compound(content) or ext == "ppt"
    return False


def convert_ppt_to_pptx_bytes(content: bytes, suffix: str = ".ppt") -> bytes | None:
    """Convert legacy PowerPoint bytes to PPTX using LibreOffice, if available."""
    soffice = find_soffice()
    if not soffice:
        return None

    max_attempts = 3
    for attempt in range(1, max_attempts + 1):
        with tempfile.TemporaryDirectory() as temp_dir, tempfile.TemporaryDirectory() as profile_dir:
            src = os.path.join(temp_dir, f"input{suffix}")
            with open(src, "wb") as handle:
                handle.write(content)

            user_installation = Path(profile_dir).as_uri()
            cmd = [
                soffice,
                "--headless",
                f"-env:UserInstallation={user_installation}",
                "--convert-to",
                "pptx",
                "--outdir",
                temp_dir,
                src,
            ]
            try:
                result = subprocess.run(cmd, capture_output=True, timeout=120)
            except (OSError, subprocess.TimeoutExpired) as exc:
                logger.warning("LibreOffice PPT convert failed to start: %s", exc)
                return None

            if result.returncode != 0:
                stderr = result.stderr.decode("utf-8", errors="ignore")
                logger.warning(
                    "LibreOffice PPT convert failed (attempt %s/%s): %s",
                    attempt,
                    max_attempts,
                    stderr,
                )
                if attempt < max_attempts:
                    time.sleep(0.5 * attempt)
                    continue
                return None

            for name in os.listdir(temp_dir):
                if name.endswith(".pptx"):
                    with open(os.path.join(temp_dir, name), "rb") as handle:
                        converted = handle.read()
                    logger.info(
                        "Converted presentation via LibreOffice (%s -> pptx, %d bytes)",
                        suffix,
                        len(converted),
                    )
                    return converted

            if attempt < max_attempts:
                time.sleep(0.5 * attempt)
    return None


def normalize_ppt_bytes(content: bytes, file_type: str | None) -> tuple[bytes, str]:
    """Return (bytes, extension) suitable for MarkItDown (pptx when converted)."""
    ext = (file_type or "").lstrip(".").lower()

    if is_zip_openxml(content):
        return content, ".pptx"

    if not needs_ppt_to_pptx_conversion(content, ext):
        dotted = f".{ext}" if ext else ".pptx"
        return content, dotted

    suffix = ".ppt" if ext in ("", "ppt") else f".{ext}"
    converted = convert_ppt_to_pptx_bytes(content, suffix=suffix)
    if converted:
        return converted, ".pptx"

    raise ValueError(
        "Legacy PowerPoint (.ppt) is not supported by MarkItDown directly; "
        "LibreOffice is required to convert it to .pptx. Install LibreOffice "
        "(soffice) in the docreader environment or upload .pptx instead."
    )
