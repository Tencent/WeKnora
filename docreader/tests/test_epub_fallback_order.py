"""Recover reading order when EPUB navigation is broken but its spine is intact."""
from io import BytesIO
import unittest
from unittest.mock import patch
import zipfile

from docreader.parser.epub_parser import EPUBParser
from docreader.tests.test_epub_parser import _ordered_epub_bytes


def without_ncx(content, encoded_path=False):
    output = BytesIO()
    with zipfile.ZipFile(BytesIO(content)) as source, zipfile.ZipFile(output, "w") as target:
        for info in source.infolist():
            if not info.filename.endswith(".ncx"):
                data = source.read(info)
                name = info.filename
                if encoded_path:
                    if name.endswith(".opf"):
                        data = data.replace(b'text/one.xhtml', b'text/one%20chapter.xhtml')
                    if name.endswith("text/one.xhtml"):
                        name = name.replace("one.xhtml", "one chapter.xhtml")
                target.writestr(name, data)
    return output.getvalue()


class EPUBFallbackOrderTests(unittest.TestCase):
    def test_fallback_recovers_manifest_xhtml_without_an_html_suffix(self):
        book = _ordered_epub_bytes(
            manifest=["appendix", "two", "one"],
            spine=["one", "two", "one"],
            svg_cover_in_spine=True,
            chapter_files={"one": "text/part1", "appendix": "text/appendix.xml"},
        )
        parser = EPUBParser(file_name="book.epub")
        with patch.object(
            parser, "_parse_epub_fallback", wraps=parser._parse_epub_fallback
        ) as fallback:
            document = parser.parse(without_ncx(book))
        fallback.assert_called_once()
        bodies = [
            line
            for line in document.content.splitlines()
            if line.startswith("Body of ")
        ]
        self.assertEqual(bodies, ["Body of one.", "Body of two.", "Body of appendix."])
        self.assertNotIn("COVER ART", document.content)

    def test_real_navigation_failure_keeps_spine_order(self):
        for encoded_path in (False, True):
            with self.subTest(encoded_path=encoded_path):
                content = without_ncx(_ordered_epub_bytes(
                    manifest=["three", "title", "one", "two", "appendix"],
                    spine=["title", "one", "two", "one", "three"],
                ), encoded_path=encoded_path)
                parser = EPUBParser(file_name="broken-navigation.epub")
                with patch.object(parser, "_parse_epub_fallback", wraps=parser._parse_epub_fallback) as fallback:
                    document = parser.parse(content)
                fallback.assert_called_once()
                names = ["title", "one", "two", "three", "appendix"]
                offsets = [document.content.index(f"Body of {name}.") for name in names]
                self.assertEqual(offsets, sorted(offsets))
                for name in names:
                    self.assertEqual(document.content.count(f"Body of {name}."), 1)

    def test_manifestless_zip_still_uses_numeric_chapter_order(self):
        for container in (None, b"<broken"):
            with self.subTest(container=container):
                output = BytesIO()
                with zipfile.ZipFile(output, "w") as archive:
                    if container is not None:
                        archive.writestr("META-INF/container.xml", container)
                    for name in ("chapter10", "chapter2", "chapter1"):
                        archive.writestr(f"{name}.html", f"<p>Text of {name}.</p>")
                document = EPUBParser().parse(output.getvalue())
                offsets = [document.content.index(f"Text of chapter{n}.") for n in (1, 2, 10)]
                self.assertEqual(offsets, sorted(offsets))

    def test_manifestless_zip_orders_separated_and_nested_chapter_names(self):
        for pattern in (
            "chapter-{}.html",
            "chapter99/chapter_{}.xhtml",
            "chapter99/chapter{}.html",
        ):
            with self.subTest(pattern=pattern):
                output = BytesIO()
                with zipfile.ZipFile(output, "w") as archive:
                    for number in (10, 2, 1):
                        archive.writestr(
                            pattern.format(number), f"<p>Body of chapter {number}.</p>"
                        )

                document = EPUBParser().parse(output.getvalue())

                offsets = [
                    document.content.index(f"Body of chapter {number}.")
                    for number in (1, 2, 10)
                ]
                self.assertEqual(offsets, sorted(offsets))
