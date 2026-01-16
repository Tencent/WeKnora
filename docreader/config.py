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

    # Image processing
    image_max_concurrent: int

    # Proxy
    external_http_proxy: str
    external_https_proxy: str

    # OCR
    ocr_backend: str
    ocr_api_base_url: str
    ocr_api_key: str
    ocr_model: str

    # VLM Caption
    vlm_model_base_url: str
    vlm_model_name: str
    vlm_model_api_key: str
    vlm_interface_type: str

    # Storage
    storage_type: str

    cos_secret_id: str
    cos_secret_key: str
    cos_region: str
    cos_bucket_name: str
    cos_app_id: str
    cos_path_prefix: str
    cos_enable_old_domain: bool

    minio_access_key_id: str
    minio_secret_access_key: str
    minio_bucket_name: str
    minio_path_prefix: str
    minio_endpoint: str
    minio_public_endpoint: str
    minio_use_ssl: bool

    local_storage_base_dir: str

    # Other
    mineru_endpoint: str


def load_config() -> DocReaderConfig:
    """Load config from environment variables.

    Naming convention (new): DOCREADER_*
    Backward compatible keys are supported.
    """

    # gRPC
    grpc_max_workers = _get_int(["DOCREADER_GRPC_MAX_WORKERS", "GRPC_MAX_WORKERS"], 4)
    grpc_max_file_size_mb = (
        _get_int(["DOCREADER_GRPC_MAX_FILE_SIZE_MB", "MAX_FILE_SIZE_MB"], 50)
        * 1024
        * 1024
    )
    grpc_port = _get_int(["DOCREADER_GRPC_PORT", "PORT"], 50051)

    # Image processing
    image_max_concurrent = _get_int(
        ["DOCREADER_IMAGE_MAX_CONCURRENT", "IMAGE_MAX_CONCURRENT"], 1
    )

    # Proxies
    external_http_proxy = _get_str(
        ["DOCREADER_EXTERNAL_HTTP_PROXY", "EXTERNAL_HTTP_PROXY"], ""
    )
    external_https_proxy = _get_str(
        ["DOCREADER_EXTERNAL_HTTPS_PROXY", "EXTERNAL_HTTPS_PROXY"], ""
    )

    # OCR
    ocr_backend = _get_str(["DOCREADER_OCR_BACKEND", "OCR_BACKEND"], "")
    ocr_api_base_url = _get_str(["DOCREADER_OCR_API_BASE_URL", "OCR_API_BASE_URL"], "")
    ocr_api_key = _get_str(["DOCREADER_OCR_API_KEY", "OCR_API_KEY"], "")
    ocr_model = _get_str(["DOCREADER_OCR_MODEL", "OCR_MODEL"], "")

    # VLM Caption
    vlm_model_base_url = _get_str(
        ["DOCREADER_VLM_MODEL_BASE_URL", "VLM_MODEL_BASE_URL"], ""
    )
    vlm_model_name = _get_str(["DOCREADER_VLM_MODEL_NAME", "VLM_MODEL_NAME"], "")
    vlm_model_api_key = _get_str(
        ["DOCREADER_VLM_MODEL_API_KEY", "VLM_MODEL_API_KEY"], ""
    )
    vlm_interface_type = _get_str(
        ["DOCREADER_VLM_INTERFACE_TYPE", "VLM_INTERFACE_TYPE"], "openai"
    ).lower()

    # Storage
    storage_type = _get_str(["DOCREADER_STORAGE_TYPE", "STORAGE_TYPE"], "cos").lower()

    # COS
    cos_secret_id = _get_str(["DOCREADER_COS_SECRET_ID", "COS_SECRET_ID"], "")
    cos_secret_key = _get_str(["DOCREADER_COS_SECRET_KEY", "COS_SECRET_KEY"], "")
    cos_region = _get_str(["DOCREADER_COS_REGION", "COS_REGION"], "")
    cos_bucket_name = _get_str(["DOCREADER_COS_BUCKET_NAME", "COS_BUCKET_NAME"], "")
    cos_app_id = _get_str(["DOCREADER_COS_APP_ID", "COS_APP_ID"], "")
    cos_path_prefix = _get_str(["DOCREADER_COS_PATH_PREFIX", "COS_PATH_PREFIX"], "")
    cos_enable_old_domain = _get_bool(
        ["DOCREADER_COS_ENABLE_OLD_DOMAIN", "COS_ENABLE_OLD_DOMAIN"], True
    )

    # MinIO
    minio_access_key_id = _get_str(
        ["DOCREADER_MINIO_ACCESS_KEY_ID", "MINIO_ACCESS_KEY_ID"], "minioadmin"
    )
    minio_secret_access_key = _get_str(
        ["DOCREADER_MINIO_SECRET_ACCESS_KEY", "MINIO_SECRET_ACCESS_KEY"], "minioadmin"
    )
    minio_bucket_name = _get_str(
        ["DOCREADER_MINIO_BUCKET_NAME", "MINIO_BUCKET_NAME"], "WeKnora"
    )
    minio_path_prefix = _get_str(
        ["DOCREADER_MINIO_PATH_PREFIX", "MINIO_PATH_PREFIX"], ""
    )
    minio_endpoint = _get_str(["DOCREADER_MINIO_ENDPOINT", "MINIO_ENDPOINT"], "")
    minio_public_endpoint = _get_str(
        ["DOCREADER_MINIO_PUBLIC_ENDPOINT", "MINIO_PUBLIC_ENDPOINT"], ""
    )
    minio_use_ssl = _get_bool(["DOCREADER_MINIO_USE_SSL", "MINIO_USE_SSL"], False)

    # Local storage
    local_storage_base_dir = "./data/files"

    # Other
    mineru_endpoint = _get_str(["DOCREADER_MINERU_ENDPOINT", "MINERU_ENDPOINT"], "")

    return DocReaderConfig(
        grpc_max_workers=grpc_max_workers,
        grpc_max_file_size_mb=grpc_max_file_size_mb,
        grpc_port=grpc_port,
        image_max_concurrent=image_max_concurrent,
        external_http_proxy=external_http_proxy,
        external_https_proxy=external_https_proxy,
        ocr_backend=ocr_backend,
        ocr_api_base_url=ocr_api_base_url,
        ocr_api_key=ocr_api_key,
        ocr_model=ocr_model,
        vlm_model_base_url=vlm_model_base_url,
        vlm_model_name=vlm_model_name,
        vlm_model_api_key=vlm_model_api_key,
        vlm_interface_type=vlm_interface_type,
        storage_type=storage_type,
        cos_secret_id=cos_secret_id,
        cos_secret_key=cos_secret_key,
        cos_region=cos_region,
        cos_bucket_name=cos_bucket_name,
        cos_app_id=cos_app_id,
        cos_path_prefix=cos_path_prefix,
        cos_enable_old_domain=cos_enable_old_domain,
        minio_access_key_id=minio_access_key_id,
        minio_secret_access_key=minio_secret_access_key,
        minio_bucket_name=minio_bucket_name,
        minio_path_prefix=minio_path_prefix,
        minio_endpoint=minio_endpoint,
        minio_public_endpoint=minio_public_endpoint,
        minio_use_ssl=minio_use_ssl,
        local_storage_base_dir=local_storage_base_dir,
        mineru_endpoint=mineru_endpoint,
    )


CONFIG = load_config()


def dump_config(mask_secrets: bool = True) -> Dict[str, Any]:
    cfg = CONFIG
    d: Dict[str, Any] = {
        # gRPC
        "DOCREADER_GRPC_MAX_WORKERS": cfg.grpc_max_workers,
        "DOCREADER_GRPC_MAX_FILE_SIZE_MB": cfg.grpc_max_file_size_mb,
        "DOCREADER_GRPC_PORT": cfg.grpc_port,
        # Image processing
        "DOCREADER_IMAGE_MAX_CONCURRENT": cfg.image_max_concurrent,
        # Proxy
        "DOCREADER_EXTERNAL_HTTP_PROXY": cfg.external_http_proxy,
        "DOCREADER_EXTERNAL_HTTPS_PROXY": cfg.external_https_proxy,
        # OCR
        "DOCREADER_OCR_BACKEND": cfg.ocr_backend,
        "DOCREADER_OCR_API_BASE_URL": cfg.ocr_api_base_url,
        "DOCREADER_OCR_API_KEY": _mask_secret(cfg.ocr_api_key)
        if mask_secrets
        else cfg.ocr_api_key,
        "DOCREADER_OCR_MODEL": cfg.ocr_model,
        # VLM
        "DOCREADER_VLM_MODEL_BASE_URL": cfg.vlm_model_base_url,
        "DOCREADER_VLM_MODEL_NAME": cfg.vlm_model_name,
        "DOCREADER_VLM_MODEL_API_KEY": _mask_secret(cfg.vlm_model_api_key)
        if mask_secrets
        else cfg.vlm_model_api_key,
        "DOCREADER_VLM_INTERFACE_TYPE": cfg.vlm_interface_type,
        # Storage
        "DOCREADER_STORAGE_TYPE": cfg.storage_type,
        "DOCREADER_COS_SECRET_ID": _mask_secret(cfg.cos_secret_id)
        if mask_secrets
        else cfg.cos_secret_id,
        "DOCREADER_COS_SECRET_KEY": _mask_secret(cfg.cos_secret_key)
        if mask_secrets
        else cfg.cos_secret_key,
        "DOCREADER_COS_REGION": cfg.cos_region,
        "DOCREADER_COS_BUCKET_NAME": cfg.cos_bucket_name,
        "DOCREADER_COS_APP_ID": cfg.cos_app_id,
        "DOCREADER_COS_PATH_PREFIX": cfg.cos_path_prefix,
        "DOCREADER_COS_ENABLE_OLD_DOMAIN": cfg.cos_enable_old_domain,
        "DOCREADER_MINIO_ACCESS_KEY_ID": _mask_secret(cfg.minio_access_key_id)
        if mask_secrets
        else cfg.minio_access_key_id,
        "DOCREADER_MINIO_SECRET_ACCESS_KEY": _mask_secret(cfg.minio_secret_access_key)
        if mask_secrets
        else cfg.minio_secret_access_key,
        "DOCREADER_MINIO_BUCKET_NAME": cfg.minio_bucket_name,
        "DOCREADER_MINIO_PATH_PREFIX": cfg.minio_path_prefix,
        "DOCREADER_MINIO_ENDPOINT": cfg.minio_endpoint,
        "DOCREADER_MINIO_PUBLIC_ENDPOINT": cfg.minio_public_endpoint,
        "DOCREADER_MINIO_USE_SSL": cfg.minio_use_ssl,
        "DOCREADER_LOCAL_STORAGE_BASE_DIR": cfg.local_storage_base_dir,
        # Other
        "DOCREADER_MINERU_ENDPOINT": cfg.mineru_endpoint,
    }
    return d


def print_config() -> None:
    d = dump_config(mask_secrets=True)
    logger.info("DocReader env/config (effective values):")
    for k in sorted(d.keys()):
        logger.info("%s=%s", k, d[k])
