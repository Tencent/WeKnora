"""Local file path validation for MCP upload tools."""

from __future__ import annotations

import os
from typing import List


def _path_within_root(resolved_path: str, root: str) -> bool:
    root = os.path.realpath(root)
    resolved_path = os.path.realpath(resolved_path)
    try:
        common = os.path.commonpath([root, resolved_path])
    except ValueError:
        return False
    return common == root


def _allowed_upload_roots() -> List[str]:
    """Return directories local files may be read from for upload tools."""
    raw = os.getenv("MCP_ALLOWED_UPLOAD_DIRS", "").strip()
    if raw:
        return [os.path.realpath(part.strip()) for part in raw.split(",") if part.strip()]

    transport = os.getenv("MCP_TRANSPORT", "stdio").strip().lower()
    if transport in ("sse", "http"):
        return [os.path.realpath(os.getcwd())]
    return []


def resolve_upload_file_path(file_path: str) -> str:
    """Resolve and validate a local file path for create_knowledge_from_file."""
    raw = (file_path or "").strip()
    if not raw:
        raise ValueError("file path is required")
    if "\x00" in raw:
        raise ValueError("file path contains invalid characters")

    resolved = os.path.realpath(raw)
    if not os.path.isfile(resolved):
        raise ValueError(f"file not found: {file_path}")

    roots = _allowed_upload_roots()
    if roots and not any(_path_within_root(resolved, root) for root in roots):
        raise ValueError("file path is outside allowed upload directories")
    return resolved
