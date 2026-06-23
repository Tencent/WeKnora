"""PDF parser backed by OpenDataLoader PDF (Apache-2.0).

Requires Java 11+ on PATH and the ``opendataloader-pdf`` Python package.
Each ``convert()`` spawns a JVM; concurrency is limited via
``DOCREADER_ODL_MAX_WORKERS``.

Hybrid mode (``docling-fast``, etc.) needs a running
``opendataloader-pdf-hybrid`` server — configure ``DOCREADER_ODL_HYBRID_URL``.
"""

from __future__ import annotations

import base64
import html
import logging
import os
import re
import shutil
import tempfile
import urllib.error
import urllib.request
from typing import Any, Dict, Mapping, Optional, Tuple

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.concurrency import parser_worker_limit

logger = logging.getLogger(__name__)

_MIN_CHARS_PER_PAGE = 20
_IMAGE_SUFFIXES = (".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp")
_MD_IMAGE_RE = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
_IMAGE_FILE_NUM_RE = re.compile(r"^imageFile(\d+)\.", re.I)


def _override_str(overrides: Optional[Mapping[str, Any]], key: str, default: str = "") -> str:
    if overrides:
        v = overrides.get(key)
        if v is not None and str(v).strip() != "":
            return str(v).strip()
    return default


def _override_bool(overrides: Optional[Mapping[str, Any]], key: str, default: bool) -> bool:
    if overrides:
        v = overrides.get(key)
        if v is not None and str(v).strip() != "":
            return str(v).strip().lower() in {"1", "true", "yes", "y", "on"}
    return default


def _java_available() -> Tuple[bool, str]:
    if not shutil.which("java"):
        return False, "需要 Java 11+（JRE），请安装并在 PATH 中配置 java"
    return True, ""


def _package_available() -> Tuple[bool, str]:
    try:
        import opendataloader_pdf  # noqa: F401
    except ImportError as e:
        return False, f"opendataloader-pdf 未安装: {e}"
    return True, ""


def _ping_hybrid(
    url: str,
    *,
    timeout_sec: float = 5.0,
    retries: int = 3,
    retry_delay_sec: float = 2.0,
) -> Tuple[bool, str]:
    import time

    base = url.rstrip("/")
    health_url = f"{base}/health"
    last_err = ""
    for attempt in range(max(1, retries)):
        try:
            req = urllib.request.Request(health_url, method="GET")
            with urllib.request.urlopen(req, timeout=timeout_sec) as resp:
                if 200 <= resp.status < 300:
                    return True, ""
                last_err = f"hybrid 健康检查 HTTP {resp.status}: {health_url}"
        except urllib.error.URLError as e:
            last_err = f"无法连接 OpenDataLoader hybrid 服务 ({health_url}): {e}"
        except Exception as e:
            last_err = f"hybrid 健康检查失败: {e}"
        if attempt + 1 < retries:
            time.sleep(retry_delay_sec)
    hint = (
        "；若刚执行 make dev-start --odl-hybrid，请等待镜像构建/服务就绪"
        "（docker logs WeKnora-odl-hybrid）"
    )
    return False, last_err + hint


def opendataloader_available(
    overrides: Optional[Mapping[str, Any]] = None,
    quick: bool = False,
) -> Tuple[bool, str]:
    """Registry / ListEngines availability probe.

    When ``quick`` is set the hybrid health check uses a single short-timeout
    attempt with no retries, so it can be used for fast status listing without
    blocking on long retry/backoff loops. Parsing keeps the patient retry
    behavior to tolerate a hybrid service that is still starting up.
    """
    ok, msg = _java_available()
    if not ok:
        return False, msg
    ok, msg = _package_available()
    if not ok:
        return False, msg

    hybrid = _resolve_hybrid(overrides)
    if hybrid and hybrid.lower() not in ("off", ""):
        url = _resolve_hybrid_url(overrides)
        if url:
            if quick:
                return _ping_hybrid(url, retries=1, timeout_sec=2.0)
            return _ping_hybrid(url, retries=6, retry_delay_sec=5.0, timeout_sec=5.0)
    return True, ""


def _resolve_hybrid(overrides: Optional[Mapping[str, Any]] = None) -> str:
    return _override_str(overrides, "odl_hybrid", CONFIG.odl_hybrid)


def _resolve_hybrid_url(overrides: Optional[Mapping[str, Any]] = None) -> str:
    return _override_str(overrides, "odl_hybrid_url", CONFIG.odl_hybrid_url)


def _find_markdown_file(output_dir: str, pdf_stem: str) -> str:
    candidates = []
    for root, _, files in os.walk(output_dir):
        for name in files:
            if name.lower().endswith(".md"):
                path = os.path.join(root, name)
                candidates.append(path)
    if not candidates:
        raise FileNotFoundError(f"OpenDataLoader 未在 {output_dir} 生成 markdown 文件")
    for path in candidates:
        base = os.path.splitext(os.path.basename(path))[0]
        if base == pdf_stem or base.startswith(pdf_stem):
            return path
    candidates.sort(key=lambda p: os.path.getmtime(p), reverse=True)
    return candidates[0]


def _normalize_odl_image_url(raw: str) -> str:
    """OpenDataLoader wraps paths as ``<images/foo.png>``; storage may HTML-escape them."""
    s = html.unescape((raw or "").strip())
    s = s.replace("&lt;", "<").replace("&gt;", ">").replace("&amp;", "&")
    s = s.strip().strip("<>").strip().strip('"').strip("'")
    if s.startswith("./"):
        s = s[2:]
    return s.replace("\\", "/")


def _canonical_image_ref(abs_path: str, output_dir: str) -> str:
    """Use ``images/<file>`` keys to match OpenDataLoader markdown conventions."""
    rel = os.path.relpath(abs_path, output_dir).replace("\\", "/")
    name = os.path.basename(abs_path)
    if rel.startswith("images/"):
        return rel
    return f"images/{name}"


def _collect_images_under_output(output_dir: str) -> Dict[str, str]:
    """Collect every extracted image under the convert output tree."""
    images: Dict[str, str] = {}
    for root, _, files in os.walk(output_dir):
        for name in files:
            if not name.lower().endswith(_IMAGE_SUFFIXES):
                continue
            abs_path = os.path.join(root, name)
            ref = _canonical_image_ref(abs_path, output_dir)
            if ref in images:
                continue
            with open(abs_path, "rb") as f:
                images[ref] = base64.b64encode(f.read()).decode("utf-8")
    return images


def _register_image_alias(aliases: Dict[str, str], alias: str, canonical: str) -> None:
    key = _normalize_odl_image_url(alias)
    if key:
        aliases[key] = canonical


def _build_path_alias_map(images: Dict[str, str]) -> Dict[str, str]:
    """Map ODL markdown spellings (angle brackets, entities, basenames) to dict keys."""
    aliases: Dict[str, str] = {}
    for ref in images:
        base = os.path.basename(ref)
        variants = [
            ref,
            base,
            f"images/{base}",
            f"<{ref}>",
            f"<images/{base}>",
            f"&lt;{ref}&gt;",
            f"&lt;images/{base}&gt;",
        ]
        for variant in variants:
            _register_image_alias(aliases, variant, ref)
    return aliases


def _resolve_image_ref(url: str, aliases: Dict[str, str]) -> Optional[str]:
    key = _normalize_odl_image_url(url)
    if not key or key.startswith("data:"):
        return None
    if key in aliases:
        return aliases[key]
    base = os.path.basename(key)
    for candidate in (base, f"images/{base}"):
        if candidate in aliases:
            return aliases[candidate]
    m = _IMAGE_FILE_NUM_RE.match(base)
    if m:
        num = int(m.group(1))
        numbered = []
        for ref in {aliases[k] for k in aliases}:
            bm = _IMAGE_FILE_NUM_RE.match(os.path.basename(ref))
            if bm:
                numbered.append((int(bm.group(1)), ref))
        numbered.sort(key=lambda x: x[0])
        for n, ref in numbered:
            if n == num:
                return ref
        if numbered and 1 <= num <= len(numbered):
            return numbered[num - 1][1]
    return None


def _rewrite_markdown_image_refs(
    markdown: str, images: Dict[str, str]
) -> str:
    if not images:
        return markdown
    aliases = _build_path_alias_map(images)

    def repl(match: re.Match[str]) -> str:
        alt, raw_url = match.group(1), match.group(2)
        url = raw_url.strip().split()[0] if raw_url else ""
        canonical = _resolve_image_ref(url, aliases)
        if canonical is None:
            return match.group(0)
        return f"![{alt}]({canonical})"

    return _MD_IMAGE_RE.sub(repl, markdown)


def _run_convert(
    pdf_path: str,
    output_dir: str,
    image_dir: str,
    overrides: Optional[Mapping[str, Any]] = None,
) -> None:
    import opendataloader_pdf

    kwargs: Dict[str, Any] = {
        "input_path": pdf_path,
        "output_dir": output_dir,
        "format": "markdown",
        "image_output": "external",
        "image_dir": image_dir,
        "quiet": True,
        "markdown_with_html": _override_bool(
            overrides, "odl_markdown_with_html", CONFIG.odl_markdown_with_html
        ),
    }
    hybrid = _resolve_hybrid(overrides)
    if hybrid and hybrid.lower() not in ("off", ""):
        kwargs["hybrid"] = hybrid
        hybrid_url = _resolve_hybrid_url(overrides)
        if hybrid_url:
            kwargs["hybrid_url"] = hybrid_url
        hybrid_mode = _override_str(overrides, "odl_hybrid_mode", CONFIG.odl_hybrid_mode)
        if hybrid_mode:
            kwargs["hybrid_mode"] = hybrid_mode
        if _override_bool(overrides, "odl_hybrid_fallback", CONFIG.odl_hybrid_fallback):
            kwargs["hybrid_fallback"] = True

    opendataloader_pdf.convert(**kwargs)


class OpenDataLoaderParser(BaseParser):
    """Parse PDFs with OpenDataLoader (layout-aware markdown + external images)."""

    def __init__(self, *args: Any, **kwargs: Any):
        self._engine_overrides: Dict[str, Any] = {
            k: v
            for k, v in kwargs.items()
            if k.startswith("odl_") or k in ("mineru_endpoint", "mineru_api_key")
        }
        super().__init__(*args, **kwargs)

    def parse_into_text(self, content: bytes) -> Document:
        ok, msg = opendataloader_available(self._engine_overrides)
        if not ok:
            raise RuntimeError(msg)

        safe_name = os.path.basename(self.file_name) or "document.pdf"
        if not safe_name.lower().endswith(".pdf"):
            safe_name = f"{os.path.splitext(safe_name)[0] or 'document'}.pdf"
        pdf_stem = os.path.splitext(safe_name)[0]

        max_workers = CONFIG.odl_max_workers
        with parser_worker_limit("opendataloader", max_workers):
            with tempfile.TemporaryDirectory(prefix="weknora-odl-") as tmp_dir:
                pdf_path = os.path.join(tmp_dir, safe_name)
                with open(pdf_path, "wb") as f:
                    f.write(content)
                image_dir = os.path.join(tmp_dir, "images")
                os.makedirs(image_dir, exist_ok=True)

                _run_convert(
                    pdf_path,
                    tmp_dir,
                    image_dir,
                    overrides=self._engine_overrides,
                )

                md_path = _find_markdown_file(tmp_dir, pdf_stem)
                with open(md_path, encoding="utf-8", errors="replace") as f:
                    text = f.read()

                images = _collect_images_under_output(tmp_dir)
                text = _rewrite_markdown_image_refs(text, images)

        if len(text.strip()) < _MIN_CHARS_PER_PAGE:
            logger.info(
                "OpenDataLoaderParser: %s yielded little text; "
                "falling back to builtin scanned renderer",
                self.file_name,
            )
            from docreader.parser.pdf_parser import PDFScannedParser

            return PDFScannedParser(
                file_name=self.file_name, file_type=self.file_type
            ).parse_into_text(content)

        logger.info(
            "OpenDataLoaderParser: %s -> content_len=%d images=%d",
            self.file_name,
            len(text),
            len(images),
        )
        return Document(
            content=text,
            images=images,
            metadata={
                "parser_engine": "opendataloader",
                "odl_hybrid": _resolve_hybrid(self._engine_overrides) or "off",
            },
        )
