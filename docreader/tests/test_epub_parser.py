import base64
from io import BytesIO
import os
import tempfile
import unittest
from unittest.mock import patch

from ebooklib import epub
from PIL import Image

from docreader.parser.epub_parser import EPUBParser
from docreader.parser.registry import registry


def _minimal_epub_bytes() -> bytes:
    book = epub.EpubBook()
    book.set_identifier("test-epub")
    book.set_title("Tiny EPUB")
    book.set_language("en")
    book.add_author("WeKnora")

    chapter = epub.EpubHtml(
        title="Chapter One", file_name="text/chapter_01.xhtml", lang="en"
    )
    chapter.content = (
        "<html><body><h1>Chapter One</h1>"
        "<p>Hello EPUB world.</p>"
        '<p><a href="chapter_02.xhtml#sec2">Chapter 2</a> '
        '<a href="#footnote1">note</a> '
        '<a href="https://example.com">the site</a></p>'
        '<img alt="cover" src="../images/pic.png">'
        "</body></html>"
    )
    book.add_item(chapter)
    book.add_item(
        epub.EpubItem(
            uid="pic",
            file_name="images/pic.png",
            media_type="image/png",
            content=b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
        )
    )
    book.toc = (epub.Link("text/chapter_01.xhtml", "Chapter One", "chapter-one"),)
    book.spine = ["nav", chapter]
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())

    with tempfile.NamedTemporaryFile(suffix=".epub", delete=False) as handle:
        path = handle.name
    try:
        epub.write_epub(path, book)
        with open(path, "rb") as handle:
            return handle.read()
    finally:
        if os.path.exists(path):
            os.unlink(path)


def _epub_with_colliding_image_names(
    image_src: str, *, root_image_first: bool = False
) -> tuple[bytes, dict[str, bytes]]:
    book = epub.EpubBook()
    book.set_identifier("colliding-images")
    book.set_title("Images in separate directories")
    book.set_language("en")

    image_data = {}
    chapters = []
    directories = (
        ("root", "first", "second")
        if root_image_first
        else ("first", "second", "root")
    )
    for directory in directories:
        index = ("first", "second", "root").index(directory)
        image = BytesIO()
        Image.new("RGB", (1, 1), (index * 100, 0, 0)).save(image, format="PNG")
        image_data[directory] = image.getvalue()
        book.add_item(
            epub.EpubItem(
                uid=f"{directory}-image",
                file_name="pic.png" if directory == "root" else f"{directory}/pic.png",
                media_type="image/png",
                content=image_data[directory],
            )
        )
        chapter_path = (
            "root.xhtml" if directory == "root" else f"{directory}/chapter.xhtml"
        )
        chapter = epub.EpubHtml(
            title=directory, file_name=chapter_path, lang="en"
        )
        src = (
            "pic.png" if directory == "root" else image_src.format(directory=directory)
        )
        chapter.content = (
            f'<html><body><h1>{directory}</h1>'
            f'<img alt="{directory}" src="{src}">'
            "</body></html>"
        )
        book.add_item(chapter)
        chapters.append(chapter)
    # The root-level pic.png also collides with chapter-relative references.
    book.toc = tuple(chapters)
    book.spine = chapters
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    output = BytesIO()
    epub.write_epub(output, book)
    return output.getvalue(), image_data


class EPUBParserTest(unittest.TestCase):
    def assert_chapter_image_bytes(self, document, image_data):
        self.assertEqual(len(document.images), 3)
        for directory, expected_data in image_data.items():
            image_ref = next(
                ref
                for ref, encoded in document.images.items()
                if base64.b64decode(encoded) == expected_data
            )
            self.assertIn(f"![{directory}]({image_ref})", document.content)

    def test_chapter_relative_images_win_over_colliding_aliases(self):
        for image_src in (
            "pic.png",
            "../{directory}/pic.png",
            "./pic%2Epng?version=1#image",
        ):
            with self.subTest(image_src=image_src):
                content, image_data = _epub_with_colliding_image_names(image_src)
                parser = EPUBParser(file_name="colliding.epub", file_type="epub")
                with patch.object(parser, "_parse_epub_fallback") as fallback:
                    document = parser.parse(content)
                fallback.assert_not_called()
                self.assert_chapter_image_bytes(document, image_data)

    def test_zip_fallback_resolves_colliding_chapter_relative_images(self):
        for root_image_first in (False, True):
            with self.subTest(root_image_first=root_image_first):
                content, image_data = _epub_with_colliding_image_names(
                    "pic.png", root_image_first=root_image_first
                )
                with patch(
                    "docreader.parser.epub_parser.epub.read_epub",
                    side_effect=ValueError("exercise ZIP fallback"),
                ):
                    document = EPUBParser(
                        file_name="colliding.epub", file_type="epub"
                    ).parse(content)
                self.assert_chapter_image_bytes(document, image_data)

    def test_root_image_alias_is_not_overwritten_by_nested_image_basenames(self):
        content, image_data = _epub_with_colliding_image_names(
            "pic.png", root_image_first=True
        )
        parser = EPUBParser(file_name="colliding.epub", file_type="epub")
        with patch.object(parser, "_parse_epub_fallback") as fallback:
            document = parser.parse(content)
        fallback.assert_not_called()
        self.assert_chapter_image_bytes(document, image_data)

    def test_parse_minimal_epub(self):
        document = EPUBParser(
            file_name="tiny.epub", file_type="epub"
        ).parse_into_text(_minimal_epub_bytes())

        self.assertIn("Hello EPUB world", document.content)
        self.assertEqual(document.metadata["source_format"], "epub")
        self.assertEqual(len(document.images), 1)
        image_ref = next(iter(document.images))
        self.assertTrue(image_ref.startswith("images/"))
        self.assertIn(image_ref, document.content)
        self.assertNotIn("../images/pic.png", document.content)

    def test_internal_links_are_unwrapped_but_external_links_remain(self):
        document = EPUBParser(
            file_name="tiny.epub", file_type="epub"
        ).parse_into_text(_minimal_epub_bytes())

        self.assertIn("Chapter 2", document.content)
        self.assertIn("note", document.content)
        self.assertNotIn("chapter_02.xhtml#sec2", document.content)
        self.assertNotIn("#footnote1", document.content)
        self.assertIn("[the site](https://example.com)", document.content)

    def test_parse_without_images(self):
        document = EPUBParser(
            file_name="tiny.epub", file_type="epub", extract_images=False
        ).parse_into_text(_minimal_epub_bytes())

        self.assertEqual(document.images, {})

    def test_registry_resolves_epub(self):
        self.assertIs(registry.get_parser_class("", "epub"), EPUBParser)


if __name__ == "__main__":
    unittest.main()
