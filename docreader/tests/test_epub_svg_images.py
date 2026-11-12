"""Images inside EPUB SVG wrappers must survive Markdown conversion."""

import base64
import unittest
import zipfile
from io import BytesIO

from ebooklib import epub
from PIL import Image

from docreader.parser.parser import Parser


def _png(color):
    output = BytesIO()
    Image.new("RGB", (2, 2), color).save(output, format="PNG")
    return output.getvalue()


EXPECTED = _png("red")
DECOY = _png("blue")


def _book(svg, *, fallback=False):
    book = epub.EpubBook()
    book.set_identifier("svg-image-regression")
    book.set_title("SVG image fixture")
    book.set_language("en")
    chapter = epub.EpubHtml(title="Cover", file_name="text/cover.xhtml")
    chapter.properties.append("svg")
    chapter.content = (
        "<html><body><h1>Cover</h1><p>Before image.</p>"
        '<svg xmlns="http://www.w3.org/2000/svg" '
        'xmlns:xlink="http://www.w3.org/1999/xlink">'
        f"{svg}</svg><p>After image.</p></body></html>"
    )
    book.add_item(chapter)
    for uid, name, payload in (
        ("expected", "images/cover art.png", EXPECTED),
        ("decoy", "cover art.png", DECOY),
    ):
        book.add_item(
            epub.EpubItem(
                uid=uid, file_name=name, media_type="image/png", content=payload
            )
        )
    book.spine = [chapter]
    book.toc = [chapter]
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    output = BytesIO()
    epub.write_epub(output, book)
    if not fallback:
        return output.getvalue()
    recovered = BytesIO()
    with zipfile.ZipFile(output) as original, zipfile.ZipFile(recovered, "w") as target:
        for entry in original.infolist():
            if not entry.filename.endswith(".ncx"):
                target.writestr(entry, original.read(entry))
    return recovered.getvalue()


class EPUBSVGImageTests(unittest.TestCase):
    def parse(self, svg, *, fallback, extract_images=True):
        with self.assertLogs("docreader.parser.epub_parser", level="INFO") as logs:
            document = Parser().parse_file(
                "cover.epub",
                "epub",
                _book(svg, fallback=fallback),
                engine_overrides={"extract_images": extract_images},
            )
        self.assertEqual(
            any("trying ZIP fallback" in line for line in logs.output), fallback
        )
        self.assertIn("Before image.", document.content)
        self.assertIn("After image.", document.content)
        return document

    def test_local_svg_images_retain_the_correct_archived_bytes(self):
        for fallback in (False, True):
            for attributes in (
                'xlink:href="../images/cover%20art.png"',
                'href="../images/cover%20art.png"',
                'href="../images/cover%20art.png" xlink:href="../cover%20art.png"',
            ):
                with self.subTest(fallback=fallback, attributes=attributes):
                    doc = self.parse(
                        f'<image {attributes} width="100" height="200"/>',
                        fallback=fallback,
                    )
                    paths = [
                        path
                        for path, encoded in doc.images.items()
                        if base64.b64decode(encoded) == EXPECTED
                    ]
                    self.assertEqual(len(paths), 1)
                    self.assertIn(f"![]({paths[0]})", doc.content)
                    for path, encoded in doc.images.items():
                        if base64.b64decode(encoded) == DECOY:
                            self.assertNotIn(f"({path})", doc.content)

    def test_svg_links_survive_when_image_extraction_is_disabled(self):
        for fallback in (False, True):
            with self.subTest(fallback=fallback):
                doc = self.parse(
                    '<image href="../images/cover.png"/>',
                    fallback=fallback,
                    extract_images=False,
                )
                self.assertIn("![](../images/cover.png)", doc.content)
                self.assertEqual(doc.images, {})

    def test_remote_svg_images_do_not_use_archive_basename_aliases(self):
        for fallback in (False, True):
            with self.subTest(fallback=fallback):
                src = "https://example.test/cover%20art.png?size=large#view"
                doc = self.parse(f'<image href="{src}"/>', fallback=fallback)
                self.assertIn(f"![]({src})", doc.content)
                self.assertTrue(
                    all(f"({path})" not in doc.content for path in doc.images)
                )

    def test_empty_href_does_not_fall_back_to_xlink_href(self):
        for fallback in (False, True):
            with self.subTest(fallback=fallback):
                doc = self.parse(
                    '<image href="" xlink:href="../images/cover%20art.png"/>',
                    fallback=fallback,
                )
                self.assertNotIn("![](", doc.content)

    def test_definition_images_are_not_promoted_to_visible_content(self):
        for fallback in (False, True):
            with self.subTest(fallback=fallback):
                doc = self.parse(
                    '<defs><image href="../cover%20art.png"/></defs>'
                    '<image href="../images/cover%20art.png" aria-label="Cover art"/>',
                    fallback=fallback,
                )
                for path, encoded in doc.images.items():
                    if base64.b64decode(encoded) == EXPECTED:
                        self.assertIn(f"![Cover art]({path})", doc.content)
                    else:
                        self.assertNotIn(f"({path})", doc.content)


if __name__ == "__main__":
    unittest.main()
