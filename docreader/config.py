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

    # Parser
    docx_max_pages: int
    markitdown_max_workers: int
    odl_max_workers: int
    odl_hybrid: str
    odl_hybrid_url: str
    odl_hybrid_mode: str
    odl_hybrid_fallback: bool
    odl_markdown_with_html: bool
    pdf_render_max_workers: int
    pdf_render_parallelism: int
    pdf_render_dpi: int
    pdf_jpeg_quality: int
    pdf_render_max_edge: int

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
    docx_max_pages = _get_int(["DOCREADER_DOCX_MAX_PAGES"], 0)
    markitdown_max_workers = _get_int(["DOCREADER_MARKITDOWN_MAX_WORKERS"], 1)
    odl_max_workers = _get_int(["DOCREADER_ODL_MAX_WORKERS"], 1)
    odl_hybrid = _get_str(["DOCREADER_ODL_HYBRID"], "off")
    odl_hybrid_url = _get_str(
        ["DOCREADER_ODL_HYBRID_URL"],
        "http://127.0.0.1:5002",
    )
    odl_hybrid_mode = _get_str(["DOCREADER_ODL_HYBRID_MODE"], "auto")
    odl_hybrid_fallback = _get_bool(["DOCREADER_ODL_HYBRID_FALLBACK"], False)
    odl_markdown_with_html = _get_bool(
        ["DOCREADER_ODL_MARKDOWN_WITH_HTML"], False
    )
    pdf_render_max_workers = _get_int(["DOCREADER_PDF_RENDER_MAX_WORKERS"], 1)
    # Intra-document render parallelism: how many worker processes render the
    # scanned pages of a SINGLE PDF in parallel. pdfium is not thread-safe, so
    # page rendering is fanned out across processes (each opens its own
    # document). A large scanned PDF rendered serially can take >1h on
    # CPU-constrained containers; this is the main lever to cut that wall time.
    # Default scales with CPU count but is capped so we don't oversubscribe.
    _cpu = os.cpu_count() or 1
    pdf_render_parallelism = _get_int(
        ["DOCREADER_PDF_RENDER_PARALLELISM"], max(1, min(4, _cpu))
    )
    pdf_render_dpi = _get_int(["DOCREADER_PDF_RENDER_DPI"], 200)
    pdf_jpeg_quality = _get_int(["DOCREADER_PDF_JPEG_QUALITY"], 85)
    # Cap the long edge (px) of rendered/extracted page images. Without this,
    # PDFs declaring very large page boxes render to 100+ MP JPEGs that blow the
    # gRPC message limit (and are far higher-res than OCR needs). ~2000px keeps
    # dense CJK text legible for OCR while keeping page images well under ~1MB.
    pdf_render_max_edge = _get_int(["DOCREADER_PDF_RENDER_MAX_EDGE"], 2000)

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
        docx_max_pages=docx_max_pages,
        markitdown_max_workers=markitdown_max_workers,
        odl_max_workers=odl_max_workers,
        odl_hybrid=odl_hybrid,
        odl_hybrid_url=odl_hybrid_url,
        odl_hybrid_mode=odl_hybrid_mode,
        odl_hybrid_fallback=odl_hybrid_fallback,
        odl_markdown_with_html=odl_markdown_with_html,
        pdf_render_max_workers=pdf_render_max_workers,
        pdf_render_parallelism=pdf_render_parallelism,
        pdf_render_dpi=pdf_render_dpi,
        pdf_jpeg_quality=pdf_jpeg_quality,
        pdf_render_max_edge=pdf_render_max_edge,
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
        "DOCREADER_DOCX_MAX_PAGES": cfg.docx_max_pages,
        "DOCREADER_MARKITDOWN_MAX_WORKERS": cfg.markitdown_max_workers,
        "DOCREADER_ODL_MAX_WORKERS": cfg.odl_max_workers,
        "DOCREADER_ODL_HYBRID": cfg.odl_hybrid,
        "DOCREADER_ODL_HYBRID_URL": cfg.odl_hybrid_url,
        "DOCREADER_ODL_HYBRID_MODE": cfg.odl_hybrid_mode,
        "DOCREADER_ODL_HYBRID_FALLBACK": cfg.odl_hybrid_fallback,
        "DOCREADER_ODL_MARKDOWN_WITH_HTML": cfg.odl_markdown_with_html,
        "DOCREADER_PDF_RENDER_MAX_WORKERS": cfg.pdf_render_max_workers,
        "DOCREADER_PDF_RENDER_PARALLELISM": cfg.pdf_render_parallelism,
        "DOCREADER_PDF_RENDER_DPI": cfg.pdf_render_dpi,
        "DOCREADER_PDF_JPEG_QUALITY": cfg.pdf_jpeg_quality,
        "DOCREADER_PDF_RENDER_MAX_EDGE": cfg.pdf_render_max_edge,
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
