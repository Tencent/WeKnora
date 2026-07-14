"""EPUB parser.

Parses EPUB files into markdown text and optional embedded images.
"""

import base64
import logging
import os
import posixpath
import tempfile
from urllib.parse import unquote
import uuid
from typing import Dict

from bs4 import BeautifulSoup
import ebooklib
from ebooklib import epub

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class EPUBParser(BaseParser):
    """Parser for EPUB e-book files."""

    def __init__(self, *args, extract_images: bool = True, **kwargs):
        super().__init__(*args, **kwargs)
        self.extract_images = extract_images

    def parse_into_text(self, content: bytes) -> Document:
        logger.info(
            "Parsing EPUB file: %s, size: %d bytes", self.file_name, len(content)
        )
        try:
            with tempfile.NamedTemporaryFile(
                suffix=".epub", delete=False, mode="wb"
            ) as epub_file:
                epub_file.write(content)
                epub_path = epub_file.name
            try:
                book = epub.read_epub(epub_path)
                metadata = self._extract_metadata(book)
                markdown_content, images = self._extract_content(book)

                metadata["source_format"] = "epub"
                metadata["file_size"] = len(content)
                metadata["chapter_count"] = len(
                    [part for part in markdown_content.split("\n## ") if part.strip()]
                )
                metadata["image_count"] = len(images)
                return Document(
                    content=markdown_content, images=images, metadata=metadata
                )
            finally:
                if os.path.exists(epub_path):
                    os.unlink(epub_path)
        except ImportError:
            logger.error("ebooklib not installed")
            raise
        except Exception as e:
            logger.warning(
                "ebooklib failed to parse EPUB: %s, trying ZIP fallback", str(e)
            )
            return self._parse_epub_fallback(content)

    def _parse_epub_fallback(self, content: bytes) -> Document:
        """Parse EPUB directly as a ZIP when ebooklib cannot read it."""
        import re
        import zipfile
        from io import BytesIO

        metadata = {"source_format": "epub", "file_size": len(content)}
        images: Dict[str, str] = {}
        image_aliases: Dict[str, str] = {}
        markdown_parts = []

        with zipfile.ZipFile(BytesIO(content), "r") as epub_zip:
            html_files = [
                f
                for f in epub_zip.namelist()
                if f.endswith((".html", ".xhtml", ".htm"))
            ]

            def chapter_num(filename: str) -> int:
                match = re.search(r"chapter(\d+)", filename, re.IGNORECASE)
                return int(match.group(1)) if match else 999999

            html_files.sort(key=chapter_num)

            if self.extract_images:
                img_exts = (".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg")
                for img_file in epub_zip.namelist():
                    if img_file.lower().endswith(img_exts):
                        try:
                            img_data = epub_zip.read(img_file)
                            ext = os.path.splitext(img_file)[1]
                            img_path = f"images/{uuid.uuid4().hex}{ext}"
                            images[img_path] = base64.b64encode(img_data).decode(
                                "utf-8"
                            )
                            self._add_image_aliases(image_aliases, img_file, img_path)
                        except Exception as e:
                            logger.warning(
                                "Failed to extract image %s: %s", img_file, e
                            )

            for html_file in html_files:
                try:
                    html_content = epub_zip.read(html_file).decode(
                        "utf-8", errors="ignore"
                    )
                    chapter_md = self._html_to_markdown(
                        html_content,
                        image_aliases=image_aliases,
                        base_path=posixpath.dirname(html_file),
                    )
                    base = os.path.basename(html_file)
                    title = base.replace(".html", "").replace(".xhtml", "")
                    title = re.sub(
                        r"chapter[_-]?", "Chapter ", title, flags=re.IGNORECASE
                    )
                    title = title.replace("_", " ").replace("-", " ").title()
                    if chapter_md.strip():
                        markdown_parts.append(f"## {title}\n\n{chapter_md}")
                except Exception as e:
                    logger.warning("Failed to process %s: %s", html_file, e)

        metadata["chapter_count"] = len(markdown_parts)
        metadata["image_count"] = len(images)
        return Document(
            content="\n\n".join(markdown_parts), images=images, metadata=metadata
        )

    def _extract_metadata(self, book) -> Dict[str, str]:
        metadata: Dict[str, str] = {}
        mapping = {
            "title": "title",
            "creator": "author",
            "publisher": "publisher",
            "language": "language",
            "description": "description",
            "date": "date",
            "identifier": "isbn",
        }
        for dc_key, out_key in mapping.items():
            try:
                values = book.get_metadata("DC", dc_key)
            except Exception:
                values = None
            if values:
                if out_key == "author":
                    metadata[out_key] = ", ".join(value[0] for value in values)
                else:
                    metadata[out_key] = values[0][0]
        return metadata

    def _extract_content(self, book) -> tuple[str, Dict[str, str]]:
        markdown_parts = []
        images: Dict[str, str] = {}
        image_aliases: Dict[str, str] = {}

        try:
            toc = book.get_table_of_contents()
        except Exception as e:
            logger.debug("Failed to get TOC: %s, processing all HTML items", e)
            toc = []

        html_items = {}
        for item in book.get_items():
            if item.get_type() == ebooklib.ITEM_DOCUMENT:
                html_items[item.get_name()] = item

        if self.extract_images:
            for item in book.get_items():
                if item.get_type() == ebooklib.ITEM_IMAGE:
                    img_data = item.get_content()
                    ext = os.path.splitext(item.get_name())[1]
                    img_path = f"images/{uuid.uuid4().hex}{ext}"
                    images[img_path] = base64.b64encode(img_data).decode("utf-8")
                    self._add_image_aliases(image_aliases, item.get_name(), img_path)

        if toc:
            for item in toc:
                entries = item if isinstance(item, tuple) else (item,)
                for sub in entries:
                    if hasattr(sub, "get_name") and sub.get_name() in html_items:
                        markdown_parts.append(
                            self._process_chapter(
                                html_items[sub.get_name()],
                                toc_index=len(markdown_parts),
                                image_aliases=image_aliases,
                            )
                        )

        if not markdown_parts:
            for _name, item in html_items.items():
                markdown_parts.append(
                    self._process_chapter(
                        item,
                        toc_index=len(markdown_parts),
                        image_aliases=image_aliases,
                    )
                )

        return "\n\n".join(part for part in markdown_parts if part.strip()), images

    def _process_chapter(
        self,
        html_item,
        toc_index: int = 0,
        image_aliases: Dict[str, str] | None = None,
    ) -> str:
        try:
            html_content = html_item.get_content()
            soup = BeautifulSoup(html_content, "lxml")
            title_tag = soup.find(["h1", "h2"])
            if title_tag:
                chapter_title = title_tag.get_text().strip()
                title_tag.decompose()
            else:
                chapter_title = html_item.get_name().replace("/", "")
                chapter_title = chapter_title.replace(".xhtml", "")
                chapter_title = chapter_title.replace("-", " ").title()
            body_html = str(soup.body) if soup.body else str(html_content)
            chapter_md = self._html_to_markdown(
                body_html,
                image_aliases=image_aliases,
                base_path=posixpath.dirname(html_item.get_name()),
            )
            return f"## {chapter_title}\n\n{chapter_md}"
        except Exception as e:
            logger.error(
                "Failed to process chapter %s: %s", html_item.get_name(), e
            )
            return f"## Chapter {toc_index + 1}\n\n[Error processing chapter: {e}]"

    def _html_to_markdown(
        self,
        html_content: str,
        image_aliases: Dict[str, str] | None = None,
        base_path: str = "",
    ) -> str:
        try:
            from bs4 import Comment
            from markdownify import markdownify as md

            soup = BeautifulSoup(html_content, "lxml")
            for element in soup(["script", "style"]):
                element.decompose()
            for comment in soup.find_all(
                string=lambda text: isinstance(text, Comment)
            ):
                comment.extract()
            self._strip_internal_links(soup)
            if image_aliases:
                self._rewrite_image_sources(soup, image_aliases, base_path)
            markdown_text = md(str(soup), heading_style="ATX")
            return "\n".join(
                line.strip() for line in markdown_text.split("\n") if line.strip()
            )
        except ImportError:
            logger.warning("markdownify not available, using HTML as-is")
            return f"```html\n{html_content}\n```"
        except Exception as e:
            logger.error("HTML to Markdown conversion failed: %s", e)
            return f"```html\n{html_content}\n```"

    @staticmethod
    def _strip_internal_links(soup: BeautifulSoup) -> None:
        """Unwrap links that don't point to an external resource.

        EPUB internal links (other chapter files, ``#fragment`` anchors, TOC
        entries) become dead links after extraction. Keep only external links
        and replace everything else with its text.
        """
        external = ("http://", "https://", "mailto:", "tel:")
        for link in soup.find_all("a"):
            href = (link.get("href") or "").strip().lower()
            if not href or not href.startswith(external):
                link.unwrap()

    @staticmethod
    def _add_image_aliases(
        image_aliases: Dict[str, str],
        original_path: str,
        image_path: str,
    ) -> None:
        normalized = EPUBParser._normalize_epub_path(original_path)
        aliases = {
            original_path,
            normalized,
            unquote(original_path),
            unquote(normalized),
            posixpath.basename(normalized),
        }
        for alias in aliases:
            if alias:
                image_aliases[alias] = image_path

    @staticmethod
    def _rewrite_image_sources(
        soup: BeautifulSoup,
        image_aliases: Dict[str, str],
        base_path: str = "",
    ) -> None:
        for img in soup.find_all("img"):
            src = (img.get("src") or "").strip()
            if not src:
                continue
            normalized_src = EPUBParser._normalize_epub_path(src)
            candidates = [
                src,
                normalized_src,
                unquote(src),
                unquote(normalized_src),
                posixpath.basename(normalized_src),
            ]
            if base_path:
                joined = EPUBParser._normalize_epub_path(posixpath.join(base_path, src))
                candidates.extend([joined, unquote(joined)])
            for candidate in candidates:
                if candidate in image_aliases:
                    img["src"] = image_aliases[candidate]
                    break

    @staticmethod
    def _normalize_epub_path(path: str) -> str:
        path = unquote(path).split("#", 1)[0].split("?", 1)[0].replace("\\", "/")
        normalized = posixpath.normpath(path)
        return "" if normalized == "." else normalized.lstrip("/")
