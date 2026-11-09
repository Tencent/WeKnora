"""Exercise archived-image resolution through the public file parser."""

import base64
import unittest
from email.message import EmailMessage

from docreader.parser.parser import Parser

# Two valid one-pixel GIFs with different palette entries.
_IMAGE = base64.b64decode("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
_OTHER_IMAGE = _IMAGE[:13] + b"\xff\x00\x00" + _IMAGE[16:]
_PAGE_URL = "https://example.test/pages/article.html"


def _archive(head: str, src: str, locations: list[str]) -> bytes:
    archive = EmailMessage()
    archive.make_related()
    main = EmailMessage()
    main.set_content(
        f"<html><head>{head}</head><body><h1>Saved article</h1>"
        f'<img src="{src}" alt="Diagram"></body></html>',
        subtype="html",
    )
    main["Content-Location"] = _PAGE_URL
    archive.attach(main)
    for index, location in enumerate(locations):
        image = EmailMessage()
        image.set_content(
            _IMAGE if index == len(locations) - 1 else _OTHER_IMAGE,
            maintype="image",
            subtype="gif",
        )
        image["Content-Location"] = location
        image["Content-ID"] = f"<image-{index}>"
        archive.attach(image)
    return archive.as_bytes()


class MHTMLBaseImageTests(unittest.TestCase):
    def test_html_base_selects_the_correct_archived_image(self):
        for head, expected_url in [
            (
                '<base href="https://assets.example.test/manual/">',
                "https://assets.example.test/manual/figure.gif",
            ),
            ('<base href="../assets/">', "https://example.test/assets/figure.gif"),
            (
                '<base href="//assets.example.test/manual/">',
                "https://assets.example.test/manual/figure.gif",
            ),
            (
                '<base target="_blank"><base href=" ../assets/ ">'
                '<base href="https://ignored.example.test/">',
                "https://example.test/assets/figure.gif",
            ),
        ]:
            with self.subTest(head=head):
                document = Parser().parse_file(
                    "saved.mhtml",
                    "mhtml",
                    _archive(
                        head,
                        "figure.gif",
                        [
                            "https://example.test/pages/figure.gif",
                            expected_url,
                        ],
                    ),
                )
                # The page-relative decoy has the same basename. Resolve the
                # HTML base first so it cannot silently replace the right image.
                self.assertIn("![Diagram](images/figure_2.gif)", document.content)
                self.assertNotIn("![Diagram](images/figure.gif)", document.content)
                self.assertEqual(len(document.images), 2)
                self.assertEqual(
                    base64.b64decode(document.images["images/figure_2.gif"]),
                    _IMAGE,
                )
                self.assertIn("Saved article", document.content)

    def test_missing_or_unusable_base_preserves_the_document_location(self):
        for head in [
            "",
            '<base target="_blank">',
            '<base href=""><base href="https://ignored.example.test/">',
            '<base href="https://[invalid/">',
            '<base href="data:text/plain,ignored">',
            '<base href="javascript:ignored">',
        ]:
            with self.subTest(head=head):
                document = Parser().parse_file(
                    "saved.mhtml",
                    "mhtml",
                    _archive(
                        head,
                        "figure.gif",
                        [
                            "https://example.test/pages/figure.gif",
                        ],
                    ),
                )
                self.assertIn("![Diagram](images/figure.gif)", document.content)
                self.assertEqual(len(document.images), 1)

    def test_base_does_not_change_absolute_or_cid_images(self):
        for src in ["https://example.test/pages/figure.gif", "cid:image-0"]:
            with self.subTest(src=src):
                document = Parser().parse_file(
                    "saved.mhtml",
                    "mhtml",
                    _archive(
                        '<base href="https://assets.example.test/">',
                        src,
                        [
                            "https://example.test/pages/figure.gif",
                        ],
                    ),
                )
                self.assertIn("![Diagram](images/figure.gif)", document.content)
                self.assertEqual(len(document.images), 1)

    def test_unarchived_images_and_disabled_extraction_are_unchanged(self):
        content = _archive(
            '<base href="https://assets.example.test/">',
            "figure.gif",
            ["https://example.test/unrelated.gif"],
        )
        document = Parser().parse_file("saved.mhtml", "mhtml", content)
        self.assertIn("![Diagram](figure.gif)", document.content)
        self.assertEqual(len(document.images), 1)

        document = Parser().parse_file(
            "saved.mhtml",
            "mhtml",
            content,
            engine_overrides={"extract_images": False},
        )
        self.assertIn("![Diagram](figure.gif)", document.content)
        self.assertEqual(document.images, {})
