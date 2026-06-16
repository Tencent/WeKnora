"""MHTML parser.

Parses MIME HTML web archives into markdown text and optional embedded images.
"""

import base64
import email
import html
import logging
import os
from urllib.parse import unquote, urljoin
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
            location = part.get("Content-Location", "")

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
                    ext = self._image_extension(content_type)
                    image_path = f"images/{uuid.uuid4().hex}{ext}"
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
            markdown_text = self._html_to_markdown(
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
            if not location:
                return False
            loc = location.lower()
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
        logger.warning("Only ad content found, using largest: %d bytes", largest["size"])
        return largest

    @staticmethod
    def _add_image_aliases(image_aliases: Dict[str, str], part, image_path: str) -> None:
        """Register the refs an MHTML document may use for an image part."""
        for raw in (
            part.get("Content-Location", ""),
            part.get("Content-ID", ""),
            part.get("X-Attachment-Id", ""),
        ):
            raw = raw.strip()
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

    def _html_to_markdown(
        self,
        html_content: str,
        image_aliases: Dict[str, str] | None = None,
        base_location: str = "",
    ) -> str:
        try:
            from markdownify import markdownify as md

            soup = BeautifulSoup(html_content, "lxml")
            for tag in soup(["script", "style", "noscript", "iframe"]):
                tag.decompose()
            self._strip_internal_links(soup)
            if image_aliases:
                self._rewrite_image_sources(soup, image_aliases, base_location)
            text_fallback = soup.get_text(separator="\n", strip=True)
            markdown_text = md(str(soup), heading_style="ATX")
            result = "\n".join(
                line.strip() for line in markdown_text.split("\n") if line.strip()
            )
            if not result and text_fallback:
                logger.warning("Markdown empty, falling back to text extraction")
                return text_fallback
            if not result:
                return f"```html\n{html_content[:50000]}\n```"
            return result
        except ImportError:
            logger.warning("markdownify not available, returning raw HTML")
            return f"```html\n{html_content}\n```"
        except Exception as e:
            logger.error("HTML to Markdown conversion failed: %s", e)
            return f"```html\n{html_content}\n```"

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
