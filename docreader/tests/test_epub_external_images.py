import base64
from html import escape
from io import BytesIO
import unittest
from unittest.mock import patch

from ebooklib import epub

from docreader.parser.epub_parser import EPUBParser


_LOCAL_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a"
    "c1kAAAAASUVORK5CYII="
)


def _epub_with_external_image(src: str) -> bytes:
    book = epub.EpubBook()
    book.set_identifier("external-image-collision")
    book.set_title("External and local images")
    book.set_language("en")
    chapter = epub.EpubHtml(title="Images", file_name="text/chapter.xhtml", lang="en")
    chapter.properties.append("remote-resources")
    chapter.content = (
        "<html><body><h1>Images</h1><p>Different image sources.</p>"
        f'<img alt="external" src="{escape(src, quote=True)}"/>'
        '<img alt="local" src="../images/pic.png"/>'
        "</body></html>"
    )
    book.add_item(chapter)
    book.add_item(
        epub.EpubItem(
            uid="local-image",
            file_name="images/pic.png",
            media_type="image/png",
            content=_LOCAL_PNG,
        )
    )
    book.spine = [chapter]
    book.toc = [chapter]
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    output = BytesIO()
    epub.write_epub(output, book)
    return output.getvalue()


class EPUBExternalImageTest(unittest.TestCase):
    def assert_external_images_preserved(self, *, fallback: bool):
        sources = (
            "https://example.com/pic.png",
            "HTTP://example.com/pic.png?size=large#image",
            "//example.com/pic.png",
            "https://example.com/images/pic.png",
            "https://[malformed/pic.png",
            "data:image/png;base64," + base64.b64encode(_LOCAL_PNG).decode(),
        )
        for src in sources:
            with self.subTest(src=src, fallback=fallback):
                parser = EPUBParser(file_name="images.epub", file_type="epub")
                content = _epub_with_external_image(src)
                if fallback:
                    with patch(
                        "docreader.parser.epub_parser.epub.read_epub",
                        side_effect=ValueError("exercise ZIP fallback"),
                    ):
                        document = parser.parse(content)
                else:
                    with patch.object(parser, "_parse_epub_fallback") as unexpected:
                        document = parser.parse(content)
                    unexpected.assert_not_called()
                self.assertIn(f"![external]({src})", document.content)
                self.assertEqual(len(document.images), 1)
                local_ref, encoded = next(iter(document.images.items()))
                self.assertEqual(base64.b64decode(encoded), _LOCAL_PNG)
                self.assertIn(f"![local]({local_ref})", document.content)

    def test_external_images_are_not_replaced_by_local_basename(self):
        self.assert_external_images_preserved(fallback=False)

    def test_zip_fallback_keeps_external_images_distinct(self):
        self.assert_external_images_preserved(fallback=True)


if __name__ == "__main__":
    unittest.main()
