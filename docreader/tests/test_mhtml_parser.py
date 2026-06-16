import unittest
from email.message import EmailMessage

from docreader.parser.mhtml_parser import MHTMLParser
from docreader.parser.registry import registry


def _minimal_mhtml_bytes() -> bytes:
    root = EmailMessage()
    root["Subject"] = "Tiny MHTML"
    root.make_related()

    main = EmailMessage()
    main.set_content(
        "<html><body><h1>Main Article</h1>"
        "<p>Hello MHTML world.</p>"
        '<p><a href="chapter03.xhtml#sec2">Chapter 3</a> '
        '<a href="#footnote1">note</a> '
        '<a href="https://example.com">the site</a></p>'
        '<img alt="tiny" src="cid:tiny-image">'
        "<script>window.noise = true</script>"
        "</body></html>",
        subtype="html",
    )
    main["Content-Location"] = "https://example.com/article"
    root.attach(main)

    ad = EmailMessage()
    ad.set_content(
        "<html><body><h1>Advertisement</h1>"
        "<p>Buy this unrelated thing.</p></body></html>",
        subtype="html",
    )
    ad["Content-Location"] = "https://googleads.example/frame.html"
    root.attach(ad)

    image = EmailMessage()
    image.set_content(
        b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
        maintype="image",
        subtype="png",
    )
    image["Content-Location"] = "cid:tiny-image"
    root.attach(image)

    return root.as_bytes()


class MHTMLParserTest(unittest.TestCase):
    def test_parse_selects_main_html_and_filters_noise(self):
        document = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        ).parse_into_text(_minimal_mhtml_bytes())

        self.assertIn("Main Article", document.content)
        self.assertIn("Hello MHTML world", document.content)
        self.assertNotIn("Advertisement", document.content)
        self.assertNotIn("window.noise", document.content)
        self.assertEqual(document.metadata["source_format"], "mhtml")

    def test_internal_links_are_unwrapped_but_external_links_remain(self):
        document = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        ).parse_into_text(_minimal_mhtml_bytes())

        self.assertIn("Chapter 3", document.content)
        self.assertIn("note", document.content)
        self.assertNotIn("chapter03.xhtml#sec2", document.content)
        self.assertNotIn("#footnote1", document.content)
        self.assertIn("[the site](https://example.com)", document.content)

    def test_image_extraction_toggle(self):
        with_images = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml", extract_images=True
        ).parse_into_text(_minimal_mhtml_bytes())
        without_images = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml", extract_images=False
        ).parse_into_text(_minimal_mhtml_bytes())

        self.assertEqual(len(with_images.images), 1)
        image_ref = next(iter(with_images.images))
        self.assertTrue(image_ref.startswith("images/"))
        self.assertIn(image_ref, with_images.content)
        self.assertNotIn("cid:tiny-image", with_images.content)
        self.assertEqual(without_images.images, {})

    def test_registry_resolves_mhtml(self):
        self.assertIs(registry.get_parser_class("", "mhtml"), MHTMLParser)


if __name__ == "__main__":
    unittest.main()
