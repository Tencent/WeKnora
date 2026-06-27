import base64
import json
import unittest
from email.message import EmailMessage
from pathlib import Path

from docreader.parser.mhtml_parser import MHTMLParser
from docreader.parser.registry import registry

REPO_ROOT = Path(__file__).resolve().parents[2]


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

    def test_html_to_markdown_preserves_nested_list_indentation(self):
        markdown = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        )._html_to_markdown("<ul><li>parent<ul><li>child</li></ul></li></ul>")

        self.assertIn("* parent\n  + child", markdown)

    def test_html_to_markdown_preserves_blockquote_boundaries(self):
        markdown = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        )._html_to_markdown("<p>before</p><blockquote><p>quoted</p></blockquote><p>after</p>")

        self.assertIn("before\n\n> quoted\n\nafter", markdown)

    def test_html_to_markdown_preserves_fenced_code_blank_lines(self):
        markdown = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        )._html_to_markdown("<pre><code>line1\n\n\nline2\n</code></pre>")

        self.assertIn("```\nline1\n\n\nline2\n```", markdown)

    def test_html_to_markdown_collapses_excess_blank_lines_outside_code(self):
        markdown = MHTMLParser._normalize_markdown("alpha\n\n  \n\t\n\nbeta")

        self.assertEqual(markdown, "alpha\n\nbeta")
        self.assertNotIn("\n\n\n", markdown)

    def test_html_to_markdown_preserves_hard_break_spaces(self):
        markdown = MHTMLParser(
            file_name="article.mhtml", file_type="mhtml"
        )._html_to_markdown("<p>alpha<br>beta</p>")

        self.assertEqual(markdown, "alpha  \nbeta")

    def test_normalize_markdown_preserves_two_space_hard_break(self):
        markdown = MHTMLParser._normalize_markdown("alpha  \nbeta")

        self.assertEqual(markdown, "alpha  \nbeta")

    def test_html_to_markdown_normalizes_crlf(self):
        markdown = MHTMLParser._normalize_markdown("alpha\r\n\r\nbeta\rgamma")

        self.assertEqual(markdown, "alpha\n\nbeta\ngamma")

    def test_html_to_markdown_does_not_strip_leading_indentation_at_document_start(self):
        markdown = MHTMLParser._normalize_markdown("  indented start\n")

        self.assertEqual(markdown, "  indented start")

    def test_mhtml_shared_contract_fixture(self):
        fixture = REPO_ROOT / "testdata" / "mhtml" / "titled-image.mhtml"
        contract_path = REPO_ROOT / "testdata" / "mhtml" / "titled-image-contract.json"
        contract = json.loads(contract_path.read_text(encoding="utf-8"))

        document = MHTMLParser(
            file_name="titled-image.mhtml", file_type="mhtml"
        ).parse_into_text(fixture.read_bytes())

        self.assertEqual(document.content, contract["markdown_content"])
        self.assertEqual(len(document.images), 1)
        image_contract = contract["images"][0]
        self.assertIn(image_contract["original_ref"], document.images)
        self.assertEqual(
            base64.b64decode(document.images[image_contract["original_ref"]]),
            base64.b64decode(image_contract["image_data_base64"]),
        )
        self.assertIn(
            '| 赛季制建立 | BP、Rank |\n\n![图片](images/第 1 页 (测试).gif "阶段 1) 图片")',
            document.content,
        )
        self.assertIn(
            '![图片](images/第 1 页 (测试).gif "阶段 1) 图片")\n\n高机动性身法与独特枪械反馈',
            document.content,
        )
        self.assertNotIn("\n\n\n", document.content.split("```", 1)[0])
        self.assertIn("```\nline1\n\n\nline2\n```", document.content)
        self.assertIn("* parent\n  + child", document.content)
        self.assertIn("alpha  \nbeta", document.content)

    def test_registry_resolves_mhtml(self):
        self.assertIs(registry.get_parser_class("", "mhtml"), MHTMLParser)


if __name__ == "__main__":
    unittest.main()
