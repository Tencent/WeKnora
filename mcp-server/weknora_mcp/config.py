"""Runtime configuration loaded from environment variables."""

from __future__ import annotations

import logging
import os

logger = logging.getLogger(__name__)

SERVER_NAME = "weknora-server"
SERVER_VERSION = "1.1.0"

WEKNORA_BASE_URL = os.getenv("WEKNORA_BASE_URL", "http://localhost:8080/api/v1")
WEKNORA_API_KEY = os.getenv("WEKNORA_API_KEY", "")

try:
    WEKNORA_CHAT_TIMEOUT = int(os.getenv("WEKNORA_CHAT_TIMEOUT", "300"))
except ValueError:
    logger.warning("WEKNORA_CHAT_TIMEOUT is not a valid integer; falling back to 300s.")
    WEKNORA_CHAT_TIMEOUT = 300


def network_transport_auth_token() -> str:
    """Shared secret clients must present for SSE/HTTP transports."""
    return os.getenv("MCP_SERVER_AUTH_TOKEN", "").strip()
