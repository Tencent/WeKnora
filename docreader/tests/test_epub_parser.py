import base64
from html import escape
from io import BytesIO
import os
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import quote
import zipfile

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


def _epub_with_literal_path_characters(
    chapter_path: str, image_src: str, image_path: str, decoy_path: str
) -> tuple[bytes, bytes]:
    """URI-encode manifest hrefs while preserving literal ZIP entry names."""
    container = (
        '<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"'
        ' version="1.0"><rootfiles><rootfile full-path="EPUB/package.opf"'
        ' media-type="application/oebps-package+xml"/></rootfiles></container>'
    )
    package = (
        '<package xmlns="http://www.idpf.org/2007/opf" version="3.0"'
        ' unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">'
        '<dc:identifier id="id">literal-paths</dc:identifier>'
        '<dc:title>Literal paths</dc:title><dc:language>en</dc:language>'
        '<meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>'
        '</metadata><manifest>'
        f'<item id="chapter" href="{quote(chapter_path)}"'
        ' media-type="application/xhtml+xml"/>'
        f'<item id="expected" href="{quote(image_path)}" media-type="image/png"/>'
        f'<item id="decoy" href="{quote(decoy_path)}" media-type="image/png"/>'
        '<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"'
        ' properties="nav"/></manifest><spine><itemref idref="chapter"/>'
        '</spine></package>'
    )
    chapter = (
        '<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title>'
        '</head><body><h1>Chapter</h1>'
        f'<img alt="expected" src="{escape(image_src, quote=True)}"/>'
        '</body></html>'
    )
    nav = (
        '<html xmlns="http://www.w3.org/1999/xhtml"'
        ' xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title>'
        '</head><body><nav epub:type="toc"><ol><li>'
        f'<a href="{quote(chapter_path)}">Chapter</a>'
        '</li></ol></nav></body></html>'
    )
    expected = BytesIO()
    decoy = BytesIO()
    Image.new("RGB", (1, 1), "red").save(expected, format="PNG")
    Image.new("RGB", (1, 1), "blue").save(decoy, format="PNG")
    output = BytesIO()
    with zipfile.ZipFile(output, "w") as archive:
        archive.writestr("mimetype", "application/epub+zip")
        archive.writestr("META-INF/container.xml", container)
        archive.writestr("EPUB/package.opf", package)
        archive.writestr(f"EPUB/{chapter_path}", chapter)
        archive.writestr(f"EPUB/{image_path}", expected.getvalue())
        archive.writestr(f"EPUB/{decoy_path}", decoy.getvalue())
        archive.writestr("EPUB/nav.xhtml", nav)
    return output.getvalue(), expected.getvalue()


class EPUBParserTest(unittest.TestCase):
    def assert_literal_path_image_bytes(self, *, use_fallback: bool):
        cases = (
            ("Text#1/chapter.xhtml", "../images/pic.png",
             "images/pic.png", "Text#1/cover.png"),
            ("Text?1/chapter.xhtml", "../images/pic.png",
             "images/pic.png", "Text?1/cover.png"),
            ("Text%231/chapter.xhtml", "pic.png",
             "Text%231/pic.png", "Text#1/pic.png"),
            ("Text/chapter.xhtml", "../images/pic%231.png?version=1#image",
             "images/pic#1.png", "images/pic#2.png"),
            ("Text/chapter.xhtml", "../images/pic%3F1.png?version=1#image",
             "images/pic?1.png", "images/pic?2.png"),
            ("Text/chapter.xhtml", "../images/pic%25231.png",
             "images/pic%231.png", "images/pic#1.png"),
            ("Text/chapter.xhtml", "../images/pic%231.png",
             "images/pic#1.png", "images/pic%231.png"),
            # Verbatim percent-looking names must beat decoded basename aliases.
            ("chapter.xhtml", "images/my%20pic.png",
             "images/my%20pic.png", "my pic.png"),
            ("Text/chapter.xhtml", "../images/my%20pic.png?version=1#image",
             "images/my%20pic.png", "my pic.png"),
            # A literal chapter-relative path also beats a decoded root path.
            ("Text/chapter.xhtml", "images/my%20pic.png",
             "Text/images/my%20pic.png", "images/my pic.png"),
            # When both exact paths exist, URI decoding still takes precedence.
            ("Text/chapter.xhtml", "../images/my%20pic.png",
             "images/my pic.png", "images/my%20pic.png"),
            # Normalize encoded separators before joining to the chapter path.
            ("Text/chapter.xhtml", "%5Cimages/pic.png",
             "images/pic.png", "Text/images/pic.png"),
        )
        for chapter_path, image_src, image_path, decoy_path in cases:
            with self.subTest(chapter_path=chapter_path, image_src=image_src):
                content, expected = _epub_with_literal_path_characters(
                    chapter_path, image_src, image_path, decoy_path
                )
                parser = EPUBParser(file_name="literal-paths.epub", file_type="epub")
                if use_fallback:
                    with patch(
                        "docreader.parser.epub_parser.epub.read_epub",
                        side_effect=ValueError("exercise ZIP fallback"),
                    ):
                        document = parser.parse(content)
                else:
                    with patch.object(parser, "_parse_epub_fallback") as fallback:
                        document = parser.parse(content)
                    fallback.assert_not_called()
                self.assertEqual(len(document.images), 2)
                image_ref = next(
                    ref for ref, encoded in document.images.items()
                    if base64.b64decode(encoded) == expected
                )
                self.assertIn(f"![expected]({image_ref})", document.content)

    def test_literal_path_characters_are_preserved(self):
        self.assert_literal_path_image_bytes(use_fallback=False)

    def test_zip_fallback_preserves_literal_path_characters(self):
        self.assert_literal_path_image_bytes(use_fallback=True)

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
