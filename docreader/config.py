import logging
import os
from dataclasses import dataclass
from typing import Any, Dict, Iterable, Optional, Tuple

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


def _get_first_env(keys: Iterable[str]) -> Tuple[Optional[str], Optional[str]]:
    """Return (value, key) for the first existing env var in keys."""
    for k in keys:
        if k in os.environ:
            return os.environ.get(k), k
    return None, None


def _get_str(keys: Iterable[str], default: str = "") -> str:
    v, _ = _get_first_env(keys)
    return default if v is None else str(v)


def _get_int(keys: Iterable[str], default: int) -> int:
    v, _ = _get_first_env(keys)
    if v is None or str(v).strip() == "":
        return default
    try:
        return int(str(v).strip())
    except Exception:
        return default


def _get_bool(keys: Iterable[str], default: bool) -> bool:
    v, _ = _get_first_env(keys)
    if v is None or str(v).strip() == "":
        return default
    return str(v).strip().lower() in {"1", "true", "yes", "y", "on"}


def _mask_secret(v: str) -> str:
    if not v:
        return ""
    if len(v) <= 6:
        return "***"
    return f"{v[:2]}***{v[-2:]}"


@dataclass(frozen=True)
class DocReaderConfig:
    # gRPC
    grpc_max_workers: int
    grpc_max_file_size_mb: int
    grpc_port: int

    # Proxy
    external_http_proxy: str
    external_https_proxy: str

    # Temp image output directory (shared with Go app via volume, local mode fallback)
    image_output_dir: str


def load_config() -> DocReaderConfig:
    """Load config from environment variables (lightweight version)."""

    grpc_max_workers = _get_int(["DOCREADER_GRPC_MAX_WORKERS", "GRPC_MAX_WORKERS"], 4)
    grpc_max_file_size_mb = (
        _get_int(["DOCREADER_GRPC_MAX_FILE_SIZE_MB", "MAX_FILE_SIZE_MB"], 50)
        * 1024
        * 1024
    )
    grpc_port = _get_int(["DOCREADER_GRPC_PORT", "PORT"], 50051)

    external_http_proxy = _get_str(
        ["DOCREADER_EXTERNAL_HTTP_PROXY", "EXTERNAL_HTTP_PROXY"], ""
    )
    external_https_proxy = _get_str(
        ["DOCREADER_EXTERNAL_HTTPS_PROXY", "EXTERNAL_HTTPS_PROXY"], ""
    )

    image_output_dir = _get_str(
        ["DOCREADER_IMAGE_OUTPUT_DIR", "IMAGE_OUTPUT_DIR"], "/tmp/docreader"
    )

    return DocReaderConfig(
        grpc_max_workers=grpc_max_workers,
        grpc_max_file_size_mb=grpc_max_file_size_mb,
        grpc_port=grpc_port,
        external_http_proxy=external_http_proxy,
        external_https_proxy=external_https_proxy,
        image_output_dir=image_output_dir,
    )


CONFIG = load_config()


def dump_config(mask_secrets: bool = True) -> Dict[str, Any]:
    cfg = CONFIG
    d: Dict[str, Any] = {
        "DOCREADER_GRPC_MAX_WORKERS": cfg.grpc_max_workers,
        "DOCREADER_GRPC_MAX_FILE_SIZE_MB": cfg.grpc_max_file_size_mb,
        "DOCREADER_GRPC_PORT": cfg.grpc_port,
        "DOCREADER_EXTERNAL_HTTP_PROXY": cfg.external_http_proxy,
        "DOCREADER_EXTERNAL_HTTPS_PROXY": cfg.external_https_proxy,
        "DOCREADER_IMAGE_OUTPUT_DIR": cfg.image_output_dir,
    }
    return d


def print_config() -> None:
    d = dump_config(mask_secrets=True)
    logger.info("DocReader env/config (effective values):")
    for k in sorted(d.keys()):
        logger.info("%s=%s", k, d[k])
