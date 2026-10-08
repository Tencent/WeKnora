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

    def test_chapters_follow_the_spine_not_the_manifest(self):
        document = EPUBParser(
            file_name="ordered.epub", file_type="epub"
        ).parse_into_text(
            _ordered_epub_bytes(
                manifest=["three", "title", "one", "two"],
                spine=["title", "one", "two", "three"],
                svg_cover_in_spine=True,
            )
        )

        positions = [
            document.content.index(f"Body of {name}.")
            for name in ("title", "one", "two", "three")
        ]
        self.assertEqual(positions, sorted(positions))
        # An SVG in the spine is not a document and stays out, as before.
        self.assertNotIn("COVER ART", document.content)

    def test_documents_outside_the_spine_follow_it(self):
        document = EPUBParser(
            file_name="ordered.epub", file_type="epub"
        ).parse_into_text(
            _ordered_epub_bytes(
                manifest=["appendix", "two", "one"],
                spine=["one", "two"],
            )
        )

        positions = [
            document.content.index(f"Body of {name}.")
            for name in ("one", "two", "appendix")
        ]
        self.assertEqual(positions, sorted(positions))

    def test_a_document_listed_twice_in_the_spine_is_read_once(self):
        document = EPUBParser(
            file_name="ordered.epub", file_type="epub"
        ).parse_into_text(
            _ordered_epub_bytes(manifest=["one", "two"], spine=["one", "two", "one"])
        )

        self.assertEqual(document.content.count("Body of one."), 1)


def _ordered_epub_bytes(
    manifest: list[str], spine: list[str], svg_cover_in_spine: bool = False
) -> bytes:
    """Write an EPUB whose manifest lists the documents in ``manifest`` order
    and whose spine reads them in ``spine`` order."""
    book = epub.EpubBook()
    book.set_identifier("ordered-epub")
    book.set_title("Ordered EPUB")
    book.set_language("en")

    chapters = {}
    for name in manifest:
        chapter = epub.EpubHtml(title=name, file_name=f"text/{name}.xhtml", lang="en")
        chapter.content = f"<html><body><p>Body of {name}.</p></body></html>"
        book.add_item(chapter)
        chapters[name] = chapter
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    book.spine = ["nav"] + [chapters[name] for name in spine]
    if svg_cover_in_spine:
        cover = epub.EpubItem(
            uid="cover-svg",
            file_name="images/cover.svg",
            media_type="image/svg+xml",
            content=(
                b'<svg xmlns="http://www.w3.org/2000/svg">'
                b"<text>COVER ART</text></svg>"
            ),
        )
        book.add_item(cover)
        book.spine.insert(1, cover)

    with tempfile.NamedTemporaryFile(suffix=".epub", delete=False) as handle:
        path = handle.name
    try:
        epub.write_epub(path, book)
        with open(path, "rb") as handle:
            return handle.read()
    finally:
        if os.path.exists(path):
            os.unlink(path)


if __name__ == "__main__":
    unittest.main()
