"""MHTML parser.

Parses MIME HTML web archives into markdown text and optional embedded images.
"""

import base64
import email
import html
import logging
import os
from email.header import decode_header
from urllib.parse import unquote, urljoin, urlparse
import uuid
from typing import Dict

from bs4 import BeautifulSoup

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)

_AD_DOMAINS = (
    "googleads",
    "doubleclick",
    "googlesyndication",
    "facebook.com/tr",
    "analytics",
    "pixel",
)

_UNKNOWN_8BIT = frozenset({"unknown-8bit", "unknown"})


def _header_str(value) -> str:
    """Normalize a MIME header to str, recovering 8-bit UTF-8 bytes.

    ``email.message_from_bytes`` uses compat32 by default. Browser-saved
    ``.mhtml`` files often store raw UTF-8 in headers such as Content-Location.
    Those values come back as ``email.header.Header`` with charset
    ``unknown-8bit``: ``.strip()`` / ``.lower()`` raise AttributeError, and
    ``str(Header)`` replaces the bytes with U+FFFD so image aliases no longer
    match the HTML body.
    """
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    if isinstance(value, str):
        try:
            return value.encode("ascii", "surrogateescape").decode("utf-8")
        except UnicodeError:
            return value
    try:
        chunks = decode_header(value)
    except (TypeError, ValueError, LookupError, UnicodeError):
        return str(value)

    parts: list[str] = []
    for chunk, charset in chunks:
        if isinstance(chunk, bytes):
            encoding = (charset or "utf-8").lower()
            if encoding in _UNKNOWN_8BIT:
                encoding = "utf-8"
            try:
                parts.append(chunk.decode(encoding, errors="replace"))
            except LookupError:
                parts.append(chunk.decode("utf-8", errors="replace"))
        elif chunk:
            try:
                parts.append(
                    chunk.encode("ascii", "surrogateescape").decode("utf-8")
                )
            except UnicodeError:
                parts.append(chunk)
    return "".join(parts)


class MHTMLParser(BaseParser):
    """Parser for MHTML web archives."""

    def __init__(self, *args, extract_images: bool = True, **kwargs):
        super().__init__(*args, **kwargs)
        self.extract_images = extract_images

    def parse_into_text(self, content: bytes) -> Document:
        logger.info(
            "Parsing MHTML file: %s, size: %d bytes", self.file_name, len(content)
        )
        msg = email.message_from_bytes(content)

        html_parts = []
        images: Dict[str, str] = {}
        image_aliases: Dict[str, str] = {}
        metadata: Dict[str, object] = {}

        for part in msg.walk():
            content_type = part.get_content_type()
            location = _header_str(part.get("Content-Location", ""))

            if content_type == "text/html":
                payload = part.get_payload(decode=True)
                if not payload:
                    continue
                charset = part.get_content_charset() or "utf-8"
                try:
                    html_text = payload.decode(charset, errors="ignore")
                except LookupError:
                    html_text = payload.decode("utf-8", errors="ignore")
                html_parts.append(
                    {
                        "content": html_text,
                        "location": location,
                        "size": len(html_text),
                    }
                )
            elif content_type.startswith("image/") and self.extract_images:
                image_data = part.get_payload(decode=True)
                if image_data:
                    image_path = self._image_path_for_part(part, content_type, images)
                    images[image_path] = base64.b64encode(image_data).decode("utf-8")
                    self._add_image_aliases(image_aliases, part, image_path)

        main_html = self._select_main_html(html_parts)
        if not main_html:
            logger.warning("No HTML content found in MHTML file")
            return Document(
                content="", images=images, metadata={"source_format": "mhtml"}
            )
        html_content = main_html["content"]

        try:
            markdown_text = self.html_to_markdown(
                html_content,
                image_aliases=image_aliases,
                base_location=main_html.get("location", ""),
            )
        except Exception as e:
            logger.error("Failed to convert HTML to Markdown: %s", e)
            markdown_text = f"```html\n{html_content}\n```"

        metadata["source_format"] = "mhtml"
        metadata["file_size"] = len(content)
        metadata["image_count"] = len(images)
        return Document(content=markdown_text, images=images, metadata=metadata)

    def _select_main_html(self, html_parts) -> dict:
        """Pick the largest non-ad HTML part as the main document body."""
        if not html_parts:
            return {}

        def is_ad(location: str) -> bool:
            loc = _header_str(location)
            if not loc:
                return False
            loc = loc.lower()
            return any(ad in loc for ad in _AD_DOMAINS)

        non_ad = sorted(
            (part for part in html_parts if not is_ad(part.get("location", ""))),
            key=lambda part: part["size"],
            reverse=True,
        )
        if non_ad:
            logger.info("Selected main HTML: %d bytes", non_ad[0]["size"])
            return non_ad[0]

        largest = max(html_parts, key=lambda part: part["size"])
        logger.warning(
            "Only ad content found, using largest: %d bytes", largest["size"]
        )
        return largest

    @staticmethod
    def _add_image_aliases(
        image_aliases: Dict[str, str], part, image_path: str
    ) -> None:
        """Register the refs an MHTML document may use for an image part."""
        for raw in (
            part.get("Content-Location", ""),
            part.get("Content-ID", ""),
            part.get("X-Attachment-Id", ""),
        ):
            raw = _header_str(raw).strip()
            if not raw:
                continue
            values = {raw, html.unescape(raw), unquote(html.unescape(raw))}
            cid = raw.strip("<>")
            if cid:
                values.add(f"cid:{cid}")
                values.add(f"cid:{unquote(cid)}")
            for value in values:
                if value:
                    image_aliases[value] = image_path

    @staticmethod
    def _image_extension(content_type: str) -> str:
        return {
            "image/png": ".png",
            "image/jpeg": ".jpg",
            "image/gif": ".gif",
            "image/webp": ".webp",
            "image/bmp": ".bmp",
            "image/tiff": ".tiff",
            "image/x-icon": ".ico",
        }.get(content_type, ".png")

    @classmethod
    def _image_path_for_part(
        cls, part, content_type: str, images: Dict[str, str]
    ) -> str:
        """Choose a stable image path when the MHTML part exposes a filename."""
        ext = cls._image_extension(content_type)
        location = _header_str(part.get("Content-Location", "")).strip()
        filename = cls._filename_from_content_location(location)
        if not filename:
            return f"images/{uuid.uuid4().hex}{ext}"

        stem, location_ext = os.path.splitext(filename)
        if not location_ext:
            filename = f"{filename}{ext}"
        image_path = f"images/{filename}"
        if image_path not in images:
            return image_path

        suffix = 2
        stem, location_ext = os.path.splitext(filename)
        while True:
            candidate = f"images/{stem}_{suffix}{location_ext}"
            if candidate not in images:
                return candidate
            suffix += 1

    @staticmethod
    def _filename_from_content_location(location: str) -> str:
        decoded = unquote(html.unescape(_header_str(location).strip()))
        if not decoded or decoded.lower().startswith("cid:"):
            return ""
        path = urlparse(decoded).path or decoded
        filename = os.path.basename(path)
        if not filename or filename in {".", ".."}:
            return ""
        if "/" in filename or "\\" in filename:
            return ""
        return filename

    def html_to_markdown(
        self,
        html_content: str,
        image_aliases: Dict[str, str] | None = None,
        base_location: str = "",
        *,
        strip_internal_links: bool = True,
        fallback_to_raw_html: bool = True,
    ) -> str:
        """Convert HTML to Markdown with explicit link and fallback policies."""

        def raw_html_fallback() -> str:
            if not fallback_to_raw_html:
                return ""
            return f"```html\n{html_content[:50000]}\n```"

        try:
            from markdownify import markdownify as md

            soup = BeautifulSoup(html_content, "lxml")
            for tag in soup(["script", "style", "noscript", "iframe"]):
                tag.decompose()
            if strip_internal_links:
                self._strip_internal_links(soup)
            if image_aliases:
                self._rewrite_image_sources(soup, image_aliases, base_location)
            text_fallback = soup.get_text(separator="\n", strip=True)
            markdown_text = md(str(soup), heading_style="ATX")
            result = self._normalize_markdown(markdown_text)
            if not result and text_fallback:
                logger.warning("Markdown empty, falling back to text extraction")
                return text_fallback
            if not result:
                return raw_html_fallback()
            return result
        except ImportError:
            logger.warning("markdownify not available, returning raw HTML")
            return raw_html_fallback()
        except Exception as e:
            logger.error("HTML to Markdown conversion failed: %s", e)
            return raw_html_fallback()

    def _html_to_markdown(
        self,
        html_content: str,
        image_aliases: Dict[str, str] | None = None,
        base_location: str = "",
    ) -> str:
        """Backward-compatible wrapper for existing internal callers and tests."""
        return self.html_to_markdown(html_content, image_aliases, base_location)

    @staticmethod
    def _normalize_markdown(markdown_text: str) -> str:
        text = markdown_text.replace("\r\n", "\n").replace("\r", "\n")
        output: list[str] = []
        pending_blank = False
        fence_char: str | None = None
        fence_len = 0

        for line in text.split("\n"):
            if fence_char is not None:
                output.append(line)
                if MHTMLParser._is_closing_fence(line, fence_char, fence_len):
                    fence_char = None
                    fence_len = 0
                continue

            opening = MHTMLParser._opening_fence(line)
            if opening is not None:
                if pending_blank and output:
                    output.append("")
                pending_blank = False
                output.append(line)
                fence_char, fence_len = opening
                continue

            if not line.strip(" \t"):
                pending_blank = True
                continue

            if pending_blank and output:
                output.append("")
            pending_blank = False

            trailing_spaces = len(line) - len(line.rstrip(" "))
            if trailing_spaces >= 2:
                line = line.rstrip(" \t") + "  "
            else:
                line = line.rstrip(" \t")
            output.append(line)

        return "\n".join(output).strip("\n")

    @staticmethod
    def _opening_fence(line: str) -> tuple[str, int] | None:
        stripped = line.lstrip(" ")
        if len(line) - len(stripped) > 3 or not stripped:
            return None
        fence_char = stripped[0]
        if fence_char not in {"`", "~"}:
            return None
        fence_len = len(stripped) - len(stripped.lstrip(fence_char))
        if fence_len < 3:
            return None
        return fence_char, fence_len

    @staticmethod
    def _is_closing_fence(line: str, fence_char: str, fence_len: int) -> bool:
        stripped = line.lstrip(" ")
        if len(line) - len(stripped) > 3:
            return False
        closing_len = len(stripped) - len(stripped.lstrip(fence_char))
        if closing_len < fence_len:
            return False
        return not stripped[closing_len:].strip(" \t")

    @staticmethod
    def _strip_internal_links(soup: BeautifulSoup) -> None:
        """Unwrap links that don't point to an external resource."""
        external = ("http://", "https://", "mailto:", "tel:")
        for link in soup.find_all("a"):
            href = (link.get("href") or "").strip().lower()
            if not href or not href.startswith(external):
                link.unwrap()

    @staticmethod
    def _rewrite_image_sources(
        soup: BeautifulSoup,
        image_aliases: Dict[str, str],
        base_location: str = "",
    ) -> None:
        for img in soup.find_all("img"):
            src = (img.get("src") or "").strip()
            if not src:
                continue
            candidates = [
                src,
                html.unescape(src),
                unquote(html.unescape(src)),
            ]
            if base_location:
                candidates.append(urljoin(base_location, src))
            base_name = os.path.basename(unquote(html.unescape(src)))
            if base_name:
                candidates.append(base_name)
            for candidate in candidates:
                if candidate in image_aliases:
                    img["src"] = image_aliases[candidate]
                    break
