"""Version 1 of the WeKnora extension protocol, as the Go package pluginapi
defines it: constants, errors, request signatures and the handshake line."""

from __future__ import annotations

import hashlib
import hmac
import time
from enum import Enum
from typing import Dict, Optional

PROTOCOL_VERSION = "1"
API_VERSION = "weknora.plugin/v1"

PROTOCOL_HEADER = "X-WeKnora-Protocol"
REQUEST_ID_HEADER = "X-Request-Id"
SIGNATURE_HEADER = "X-WeKnora-Signature"
TIMESTAMP_HEADER = "X-WeKnora-Timestamp"
NDJSON_CONTENT_TYPE = "application/x-ndjson"

# Environment a host plugin is started with.
ENV_SOCKET = "WEKNORA_PLUGIN_SOCKET"
ENV_NETWORK = "WEKNORA_PLUGIN_NETWORK"
ENV_TOKEN = "WEKNORA_PLUGIN_TOKEN"
ENV_PLUGIN_ID = "WEKNORA_PLUGIN_ID"
ENV_PLUGIN_VERSION = "WEKNORA_PLUGIN_VERSION"
# Environment of a remote plugin.
ENV_ADDR = "WEKNORA_PLUGIN_ADDR"
ENV_SECRET = "WEKNORA_PLUGIN_SECRET"

# How old a signed request may be, in seconds.
MAX_CLOCK_SKEW = 5 * 60

HOST_KV_PATH = "/api/v1/plugin-host/kv"
HOST_KV_LIST_PATH = "/api/v1/plugin-host/kv/list"


class ErrorCode(str, Enum):
    """Classifies a failed call; WeKnora reacts to each differently."""

    INVALID_CONFIG = "invalid_config"
    UNAUTHORIZED = "unauthorized"
    NOT_FOUND = "not_found"
    RATE_LIMITED = "rate_limited"
    UNAVAILABLE = "unavailable"
    BAD_REQUEST = "bad_request"
    INTERNAL = "internal"

    @property
    def http_status(self) -> int:
        return _STATUS.get(self, 500)


_STATUS = {
    ErrorCode.INVALID_CONFIG: 400,
    ErrorCode.BAD_REQUEST: 400,
    ErrorCode.UNAUTHORIZED: 401,
    ErrorCode.NOT_FOUND: 404,
    ErrorCode.RATE_LIMITED: 429,
    ErrorCode.UNAVAILABLE: 503,
}


class PluginError(Exception):
    """A failed call. Raise it to choose the code WeKnora sees; any other
    exception becomes ``internal``. Unavailable and rate-limited errors are
    retryable unless told otherwise."""

    def __init__(
        self,
        code: ErrorCode | str,
        message: str,
        *,
        retryable: Optional[bool] = None,
        fields: Optional[Dict[str, str]] = None,
        retry_after: int = 0,
    ) -> None:
        super().__init__(message)
        try:
            self.code = ErrorCode(code)
        except ValueError:
            self.code = ErrorCode.INTERNAL
        self.message = message
        if retryable is None:
            retryable = self.code in (ErrorCode.UNAVAILABLE, ErrorCode.RATE_LIMITED)
        self.retryable = retryable
        self.fields = fields or {}
        self.retry_after = retry_after

    def __str__(self) -> str:
        return f"{self.code.value}: {self.message}"

    def to_wire(self) -> dict:
        out: dict = {"code": self.code.value, "message": self.message}
        if self.retryable:
            out["retryable"] = True
        details: dict = {}
        if self.fields:
            details["fields"] = dict(self.fields)
        if self.retry_after:
            details["retryAfter"] = self.retry_after
        if details:
            out["details"] = details
        return out

    @classmethod
    def from_wire(cls, body: dict) -> "PluginError":
        details = body.get("details") or {}
        return cls(
            body.get("code") or ErrorCode.INTERNAL,
            body.get("message") or "",
            retryable=bool(body.get("retryable")),
            fields=details.get("fields"),
            retry_after=int(details.get("retryAfter") or 0),
        )


def invalid_config(message: str, fields: Dict[str, str]) -> PluginError:
    """Reports configuration problems keyed by dotted field path; WeKnora
    shows them on the form."""
    return PluginError(ErrorCode.INVALID_CONFIG, message, fields=fields)


def sign(secret: bytes, timestamp: int, body: bytes) -> str:
    """The signature of a request body at a unix timestamp: hex HMAC-SHA256
    over ``"<timestamp>.<body>"``."""
    mac = hmac.new(secret, f"{timestamp}.".encode(), hashlib.sha256)
    mac.update(body)
    return mac.hexdigest()


def verify_signature(
    secret: bytes, timestamp: str, signature: str, body: bytes, now: Optional[float] = None
) -> None:
    """Checks a signed request; raises ValueError when it does not hold."""
    try:
        ts = int(timestamp)
    except (TypeError, ValueError):
        raise ValueError(f"bad {TIMESTAMP_HEADER}") from None
    now = time.time() if now is None else now
    if abs(now - ts) > MAX_CLOCK_SKEW:
        raise ValueError("request timestamp is outside the allowed clock skew")
    if not hmac.compare_digest(sign(secret, ts, body), signature or ""):
        raise ValueError("bad signature")


def handshake_line(network: str, address: str) -> str:
    """The line a host plugin prints on stdout once it is ready."""
    return "|".join(["WEKNORA_PLUGIN", PROTOCOL_VERSION, network, address])
