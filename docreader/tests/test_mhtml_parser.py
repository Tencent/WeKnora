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


def _mhtml_with_table_image_and_caption() -> bytes:
    root = EmailMessage()
    root["Subject"] = "MHTML with table image"
    root.make_related()

    main = EmailMessage()
    main.set_content(
        "<html><body>"
        "<table>"
        "<tr><th>体验方向</th><th>代表内容</th></tr>"
        "<tr><td>赛季制建立</td><td>BP、Rank</td></tr>"
        "</table>"
        '<img src="cid:test-image" alt="图片" title="图片">'
        "<p>高机动性身法与独特枪械反馈</p>"
        "</body></html>",
        subtype="html",
    )
    main["Content-Location"] = "https://example.com/article"
    root.attach(main)

    image = EmailMessage()
    image.set_content(
        b"GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00"
        b"\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;",
        maintype="image",
        subtype="gif",
    )
    image["Content-ID"] = "<test-image>"
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

    def test_table_image_and_caption_keep_markdown_block_boundaries(self):
        document = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        ).parse_into_text(_mhtml_with_table_image_and_caption())

        self.assertEqual(len(document.images), 1)
        image_ref = next(iter(document.images))
        self.assertIn(f'![图片]({image_ref} "图片")', document.content)
        self.assertIn(
            f"| 赛季制建立 | BP、Rank |\n\n![图片]({image_ref} \"图片\")",
            document.content,
        )
        self.assertIn(
            f'![图片]({image_ref} "图片")\n\n高机动性身法与独特枪械反馈',
            document.content,
        )

    def test_html_to_markdown_preserves_indentation_and_code_block_blanks(self):
        markdown = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        )._html_to_markdown(
            "<ul><li>parent<ul><li>child</li></ul></li></ul>"
            "<blockquote><p>quoted</p></blockquote>"
            "<pre><code>line1\n\n  indented\n</code></pre>"
        )

        self.assertIn("* parent\n  + child", markdown)
        self.assertIn("\n\n> quoted\n\n", markdown)
        self.assertIn("```\nline1\n\n  indented\n```", markdown)

    def test_registry_resolves_mhtml(self):
        self.assertIs(registry.get_parser_class("", "mhtml"), MHTMLParser)


if __name__ == "__main__":
    unittest.main()
