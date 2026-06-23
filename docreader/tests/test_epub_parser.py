import os
import tempfile
import unittest

from ebooklib import epub

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


class EPUBParserTest(unittest.TestCase):
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
