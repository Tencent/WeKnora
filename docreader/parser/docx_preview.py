"""Make DOCX previews browser-renderable by rasterizing EMF/WMF media.

Word stores pasted charts and some logos as Windows metafiles
(``word/media/*.emf`` / ``*.wmf``). Browsers -- and therefore ``docx-preview``
in the web UI -- cannot decode those formats, so the image shows up as a broken
icon even though every other part of the document renders fine.

This module rebuilds the package: each vector image is rendered to PNG by
LibreOffice (ImageMagick in this image has no EMF decoder), the relationship
Targets that point at the old part are rewritten, and an ``image/png`` default
is added to ``[Content_Types].xml``. Everything else, including the original
``document.xml`` bytes, is copied through untouched. The function is
best-effort: when an image cannot be rendered it keeps its original bytes and
the caller still gets a valid package.
"""

from __future__ import annotations

import io
import logging
import os
import re
import shutil
import subprocess
import tempfile
import zipfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional

from docreader.parser.excel_convert import find_soffice
from docreader.parser.pptx_media import rasterize_media_bytes

logger = logging.getLogger(__name__)

VECTOR_EXTENSIONS = {".emf", ".wmf"}

_MEDIA_PREFIX = "word/media/"
_CONTENT_TYPES_PART = "[Content_Types].xml"
_RELATIONSHIP_TARGET = re.compile(r'(Target=")([^"]+)(")')
_PNG_DEFAULT = b'<Default Extension="png" ContentType="image/png"/>'
_PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"

# LibreOffice renders a metafile onto a default A4 page and leaves a large
# white margin around the drawing. Trim it so the PNG keeps the drawing's own
# aspect ratio; Word scales the image to the wp:extent box either way.
_TRIM_FUZZ = "2%"
_RASTERIZE_TIMEOUT_SECONDS = 120


@dataclass
class DocxPreviewResult:
    """Outcome of :func:`convert_docx_preview`."""

    content: bytes = b""
    converted: bool = False
    skipped_reason: str = ""
    warnings: List[str] = field(default_factory=list)
    error: str = ""


def _is_vector_media(name: str) -> bool:
    return os.path.splitext(name)[1].lower() in VECTOR_EXTENSIONS


def find_vector_media(docx_bytes: bytes) -> List[str]:
    """Return the ``word/media`` entries that need rasterization."""
    with zipfile.ZipFile(io.BytesIO(docx_bytes)) as archive:
        return [
            info.filename
            for info in archive.infolist()
            if info.filename.startswith(_MEDIA_PREFIX)
            and not info.is_dir()
            and _is_vector_media(info.filename)
        ]


def _find_imagemagick() -> Optional[str]:
    for path in ("/usr/bin/convert", "/usr/local/bin/convert"):
        if os.path.isfile(path):
            return path
    return shutil.which("convert")


def _trim_white_margin(png: bytes) -> bytes:
    """Crop the white page LibreOffice puts around a metafile drawing."""
    convert = _find_imagemagick()
    if convert:
        with tempfile.TemporaryDirectory() as temp_dir:
            src = os.path.join(temp_dir, "page.png")
            dst = os.path.join(temp_dir, "trimmed.png")
            with open(src, "wb") as handle:
                handle.write(png)
            try:
                result = subprocess.run(
                    [
                        convert,
                        src,
                        "-fuzz",
                        _TRIM_FUZZ,
                        "-trim",
                        "+repage",
                        dst,
                    ],
                    capture_output=True,
                    timeout=60,
                )
            except (OSError, subprocess.TimeoutExpired) as exc:
                logger.warning("ImageMagick trim failed: %s", exc)
            else:
                if result.returncode == 0 and os.path.isfile(dst):
                    with open(dst, "rb") as handle:
                        trimmed = handle.read()
                    if trimmed.startswith(_PNG_SIGNATURE):
                        return trimmed
                else:
                    logger.warning(
                        "ImageMagick trim exit %s: %s",
                        result.returncode,
                        (result.stderr or b"").decode("utf-8", errors="ignore"),
                    )
    return _trim_white_margin_with_pillow(png)


def _trim_white_margin_with_pillow(png: bytes) -> bytes:
    try:
        from PIL import Image, ImageChops
    except ImportError:
        return png
    try:
        image = Image.open(io.BytesIO(png)).convert("RGB")
        white = Image.new("RGB", image.size, (255, 255, 255))
        diff = ImageChops.difference(image, white)
        # Ignore faint compression noise, but keep anti-aliased edges.
        diff = diff.point(lambda value: 255 if value > 8 else 0)
        bbox = diff.getbbox()
        if not bbox:
            return png
        cropped = image.crop(bbox)
        out = io.BytesIO()
        cropped.save(out, format="PNG")
        return out.getvalue()
    except Exception as exc:  # pragma: no cover - defensive, PIL is bundled
        logger.warning("Pillow trim failed: %s", exc)
        return png


def _rasterize_with_libreoffice(name: str, data: bytes, work_dir: str) -> Optional[bytes]:
    """Render one metafile with LibreOffice and return PNG bytes."""
    soffice = find_soffice()
    if not soffice:
        return None

    stem = os.path.splitext(os.path.basename(name))[0]
    # LibreOffice shares one profile by default, so concurrent conversions
    # contend for the profile lock and the loser silently does nothing. Give
    # every attempt a private profile (same technique as docx/excel conversion).
    for attempt in range(1, 3):
        out_dir = os.path.join(work_dir, f"out-{attempt}")
        profile_dir = os.path.join(work_dir, f"profile-{attempt}")
        os.makedirs(out_dir, exist_ok=True)
        os.makedirs(profile_dir, exist_ok=True)

        src = os.path.join(work_dir, f"{stem}-{attempt}{os.path.splitext(name)[1]}")
        with open(src, "wb") as handle:
            handle.write(data)

        command = [
            soffice,
            "--headless",
            "--norestore",
            f"-env:UserInstallation={Path(profile_dir).as_uri()}",
            "--convert-to",
            "png",
            "--outdir",
            out_dir,
            src,
        ]
        try:
            # Reuse the parser sandbox wrapper: it blocks outbound network
            # access and kills the whole process group on timeout, so a
            # crafted metafile cannot make LibreOffice reach the network.
            from docreader.parser.doc_parser import SandboxExecutor

            executor = SandboxExecutor(default_timeout=_RASTERIZE_TIMEOUT_SECONDS)
            _, stderr, returncode = executor.execute_in_sandbox(command)
        except Exception as exc:
            logger.warning("LibreOffice could not render %s: %s", name, exc)
            return None

        rendered = os.path.join(
            out_dir, os.path.splitext(os.path.basename(src))[0] + ".png"
        )
        if returncode == 0 and os.path.isfile(rendered):
            with open(rendered, "rb") as handle:
                png = handle.read()
            if png.startswith(_PNG_SIGNATURE):
                return _trim_white_margin(png)
            logger.warning("LibreOffice produced a non-PNG file for %s", name)
            return None
        logger.warning(
            "LibreOffice failed to render %s (attempt %d/2, exit %s): %s",
            name,
            attempt,
            returncode,
            (stderr or b"").decode("utf-8", errors="ignore")[:500],
        )
    return None


def rasterize_vector_media(name: str, data: bytes) -> Optional[bytes]:
    """Return PNG bytes for one EMF/WMF part, or None when unavailable.

    LibreOffice is the primary renderer. ImageMagick builds with a WMF decoder
    can also render WMF (but not EMF); it is used as a fallback.
    """
    with tempfile.TemporaryDirectory() as work_dir:
        png = _rasterize_with_libreoffice(name, data, work_dir)
    if png:
        return png
    png = rasterize_media_bytes(name, data)
    if png and png.startswith(_PNG_SIGNATURE):
        return png
    return None


def _unique_png_name(old_name: str, taken: set[str]) -> str:
    folder, base = old_name.rsplit("/", 1) if "/" in old_name else ("", old_name)
    stem = os.path.splitext(base)[0]

    def candidate_path(suffix_stem: str) -> str:
        name = f"{suffix_stem}.png"
        return f"{folder}/{name}" if folder else name

    candidate = candidate_path(stem)
    if candidate.lower() not in taken:
        return candidate
    for suffix in ("_emf", "_wmf"):
        candidate = candidate_path(f"{stem}{suffix}")
        if candidate.lower() not in taken:
            return candidate
    index = 2
    while True:
        candidate = candidate_path(f"{stem}_converted_{index}")
        if candidate.lower() not in taken:
            return candidate
        index += 1


def _rewrite_relationship_targets(data: bytes, renames: Dict[str, str]) -> bytes:
    """Point image relationships at the new PNG part, keeping everything else."""
    text = data.decode("utf-8")
    if not renames:
        return data

    def replace(match: re.Match[str]) -> str:
        prefix, target, suffix = match.group(1), match.group(2), match.group(3)
        base = target.replace("\\", "/").rsplit("/", 1)[-1]
        new_base = renames.get(base.lower())
        if not new_base:
            return match.group(0)
        return prefix + target[: len(target) - len(base)] + new_base + suffix

    rewritten = _RELATIONSHIP_TARGET.sub(replace, text)
    return data if rewritten == text else rewritten.encode("utf-8")


def _ensure_png_content_type(data: bytes) -> bytes:
    text = data.decode("utf-8")
    if re.search(r'<Default[^>]*Extension="png"', text, re.IGNORECASE):
        return data
    if "</Types>" not in text:
        return data
    return text.replace(
        "</Types>", _PNG_DEFAULT.decode("utf-8") + "</Types>", 1
    ).encode("utf-8")


def _drop_part_overrides(data: bytes, old_names: List[str]) -> bytes:
    """Remove content-type overrides for parts that no longer exist."""
    text = data.decode("utf-8")
    for name in old_names:
        text = re.sub(
            r'<Override[^>]*PartName="/%s"[^>]*/>' % re.escape(name),
            "",
            text,
            flags=re.IGNORECASE,
        )
    return text.encode("utf-8")


def convert_docx_preview(docx_bytes: bytes, file_name: str = "") -> DocxPreviewResult:
    """Rebuild a DOCX package with EMF/WMF media replaced by PNG.

    Returns the original bytes untouched when nothing needed converting (or
    when no vector renderer is available); ``converted`` tells the caller
    whether it is allowed to cache/serve the returned copy.
    """
    try:
        with zipfile.ZipFile(io.BytesIO(docx_bytes)) as archive:
            infos = archive.infolist()
            entries = [(info, archive.read(info.filename)) for info in infos]
    except (zipfile.BadZipFile, OSError) as exc:
        return DocxPreviewResult(error=f"not a valid docx package: {exc}")

    vector_entries = [
        (info, data)
        for info, data in entries
        if info.filename.startswith(_MEDIA_PREFIX)
        and not info.is_dir()
        and _is_vector_media(info.filename)
    ]
    if not vector_entries:
        return DocxPreviewResult(skipped_reason="no EMF/WMF images")

    logger.info(
        "Rasterizing %d vector image(s) for DOCX preview: %s",
        len(vector_entries),
        file_name or "<upload>",
    )

    taken = {info.filename.lower() for info, _ in entries if not info.is_dir()}
    replacements: Dict[str, tuple[bytes, zipfile.ZipInfo]] = {}
    renames: Dict[str, str] = {}
    warnings: List[str] = []
    converted_count = 0

    for info, data in vector_entries:
        png = rasterize_vector_media(info.filename, data)
        if not png:
            warnings.append(
                f"could not rasterize {os.path.basename(info.filename)}; kept original"
            )
            logger.warning("Could not rasterize %s, keeping original", info.filename)
            continue
        new_name = _unique_png_name(info.filename, taken)
        taken.add(new_name.lower())
        # Reuse the original ZipInfo (timestamps, attributes) under the new
        # name so the rebuilt archive stays as close to the input as possible.
        new_info = zipfile.ZipInfo(new_name, date_time=info.date_time)
        new_info.compress_type = zipfile.ZIP_DEFLATED
        new_info.external_attr = info.external_attr
        replacements[info.filename] = (png, new_info)
        renames[os.path.basename(info.filename).lower()] = os.path.basename(new_name)
        converted_count += 1
        logger.info(
            "Rasterized %s -> %s (%d -> %d bytes)",
            info.filename,
            new_name,
            len(data),
            len(png),
        )

    if converted_count == 0:
        return DocxPreviewResult(
            skipped_reason="no vector image could be rasterized",
            warnings=warnings,
        )

    dropped = set(replacements)
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as rebuilt:
        for info, data in entries:
            if info.is_dir():
                rebuilt.writestr(info, b"")
                continue
            if info.filename in dropped:
                png, new_info = replacements[info.filename]
                rebuilt.writestr(new_info, png)
                continue
            if info.filename.endswith(".rels"):
                data = _rewrite_relationship_targets(data, renames)
            elif info.filename == _CONTENT_TYPES_PART:
                data = _ensure_png_content_type(data)
                data = _drop_part_overrides(data, sorted(dropped))
            rebuilt.writestr(info, data)

    logger.info(
        "Rebuilt DOCX preview: %d/%d vector image(s) converted, %d bytes -> %d bytes",
        converted_count,
        len(vector_entries),
        len(docx_bytes),
        output.tell(),
    )
    return DocxPreviewResult(
        content=output.getvalue(),
        converted=True,
        warnings=warnings,
    )
