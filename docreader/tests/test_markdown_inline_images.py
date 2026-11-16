"""Inline Markdown examples must not become extracted image resources."""

import base64
import unittest

from docreader.parser.markdown_parser import MarkdownImageUtil
from docreader.parser.parser import Parser

PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4"
    "z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)
IMAGE = f"![example](data:image/png;base64,{PNG_B64})"


class MarkdownInlineImageTests(unittest.TestCase):
    def parse(self, text):
        return Parser().parse_file("example.md", "md", text.encode())

    def test_literal_examples_survive_public_file_parser(self):
        for code in (
            f"`{IMAGE}`",
            f"`` {IMAGE} ` literal ``",
            f"`first line\n{IMAGE}\nlast line`",
            "`![example](data:image/png;base64,a)`",
            f"`{IMAGE}\\`",
            f"\\``{IMAGE}`",
        ):
            with self.subTest(code=code):
                raw = f"Use {code} in your documentation.\n"
                doc = self.parse(raw)
                self.assertEqual(doc.content, raw)
                self.assertEqual(doc.images, {})

    def test_only_real_images_are_extracted(self):
        code = f"`{IMAGE}`"
        raw = f"{IMAGE} and {code} and {IMAGE}"
        doc = self.parse(raw)
        self.assertIn(code, doc.content)
        self.assertEqual(len(doc.images), 2)
        for path, payload in doc.images.items():
            self.assertIn(f"![example]({path})", doc.content)
            self.assertEqual(payload, PNG_B64)

    def test_unmatched_and_escaped_openers_do_not_hide_images(self):
        for raw in (
            f"`{IMAGE}",
            f"``{IMAGE}`",
            f"\\`{IMAGE}`",
            f"`unmatched\n\n{IMAGE}\n\n`",
        ):
            with self.subTest(raw=raw):
                doc = self.parse(raw)
                self.assertEqual(len(doc.images), 1)

    def test_even_backslashes_still_allow_code(self):
        raw = f"\\\\`{IMAGE}`"
        doc = self.parse(raw)
        self.assertEqual(doc.content, raw)
        self.assertEqual(doc.images, {})

    def test_code_in_alt_text_does_not_hide_real_image(self):
        raw = IMAGE.replace("[example]", "[a `literal` example]")
        doc = self.parse(raw)
        self.assertEqual(len(doc.images), 1)
        path = next(iter(doc.images))
        self.assertEqual(doc.content, f"![a `literal` example]({path})")

    def test_replace_false_ignores_code_resources_too(self):
        raw = f"`{IMAGE}` and {IMAGE}"
        text, images = MarkdownImageUtil().extract_base64(raw, replace=False)
        self.assertEqual(text, raw)
        self.assertEqual(list(images.values()), [base64.b64decode(PNG_B64)])

    def test_inline_and_fenced_examples_together(self):
        raw = f"`{IMAGE}`\n\n```md\n{IMAGE}\n```\n\n{IMAGE}"
        doc = self.parse(raw)
        self.assertIn(f"`{IMAGE}`", doc.content)
        self.assertIn(f"```md\n{IMAGE}\n```", doc.content)
        self.assertEqual(len(doc.images), 1)

    def test_helper_preserves_multiline_crlf_code(self):
        raw = f"`first\r\n{IMAGE}\r\nlast`\r\n"
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))


if __name__ == "__main__":
    unittest.main()
