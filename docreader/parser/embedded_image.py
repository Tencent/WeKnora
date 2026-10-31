"""Normalize embedded Office images before storage, OCR and captioning."""

from __future__ import annotations

import io
import logging
import subprocess
import tempfile
from pathlib import Path

from PIL import Image, ImageChops

from docreader.parser.excel_convert import find_soffice
from docreader.parser.pptx_media import rasterize_media_bytes

logger = logging.getLogger(__name__)

_RASTER_FORMATS = {
    "PNG": "png", "JPEG": "jpg", "GIF": "gif", "WEBP": "webp",
    "BMP": "bmp", "AVIF": "avif",
}
_METAFILE_TYPES = {
    "image/emf": ".emf", "image/x-emf": ".emf",
    "image/wmf": ".wmf", "image/x-wmf": ".wmf",
    "image/x-win-metafile": ".wmf",
}


def _rasterize_with_libreoffice(data: bytes, suffix: str) -> bytes | None:
    soffice = find_soffice()
    if not soffice:
        return None
    with tempfile.TemporaryDirectory(prefix="office_image_") as directory:
        work = Path(directory)
        source = work / f"image{suffix}"
        source.write_bytes(data)
        try:
            result = subprocess.run(
                [
                    soffice, "--headless",
                    f"-env:UserInstallation={(work / 'profile').as_uri()}",
                    "--convert-to", "png:draw_png_Export",
                    "--outdir", str(work), str(source),
                ],
                capture_output=True,
                timeout=60,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            logger.warning("LibreOffice image conversion failed: %s", exc)
            return None
        output = work / "image.png"
        if result.returncode == 0 and output.is_file():
            return output.read_bytes()
        logger.warning("LibreOffice could not render embedded %s image", suffix)
    return None


def normalize_embedded_image(data: bytes, mime_type: str) -> tuple[bytes, str] | None:
    """Return displayable bytes and their real extension, or skip the object.

    MIME labels in Office documents can be wrong. OLE/Visio packages must
    never become images, even when advertised as image/png. Their original
    bytes remain in the uploaded document; any separate preview image goes
    through the same normalization as other pictures.
    """
    if data.startswith((b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1", b"PK\x03\x04")):
        return None

    try:
        with Image.open(io.BytesIO(data)) as image:
            extension = _RASTER_FORMATS.get(image.format)
            if extension:
                image.load()
                return data, extension
    except (OSError, ValueError, SyntaxError):
        pass

    suffix = _METAFILE_TYPES.get(mime_type.lower())
    if data[:4] == b"\x01\x00\x00\x00" and data[40:44] == b" EMF":
        suffix = ".emf"
    elif data.startswith((b"\xd7\xcd\xc6\x9a", b"\x01\x00\x09\x00", b"\x02\x00\x09\x00")):
        suffix = ".wmf"
    if suffix:
        converted = rasterize_media_bytes(f"image{suffix}", data)
        if not converted:
            converted = _rasterize_with_libreoffice(data, suffix)
        if converted:
            try:
                with Image.open(io.BytesIO(converted)) as image:
                    if image.format == "PNG":
                        image.load()
                        # LibreOffice may place the imported metafile on a
                        # full white Draw page. Keep the diagram, not that page.
                        background = Image.new("RGBA", image.size, "white")
                        background.alpha_composite(image.convert("RGBA"))
                        bounds = ImageChops.difference(
                            background.convert("RGB"), Image.new("RGB", image.size, "white")
                        ).getbbox()
                        if bounds:
                            output = io.BytesIO()
                            image.crop(bounds).save(output, format="PNG")
                            converted = output.getvalue()
                        return converted, "png"
            except (OSError, ValueError, SyntaxError):
                pass
        return None

    # SVG already has native browser support; keep the existing representation.
    if mime_type.lower() == "image/svg+xml":
        return data, "svg"
    return None
