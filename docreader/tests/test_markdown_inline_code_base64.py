"""Base64 image syntax inside inline code spans is documentation, not an image."""

import unittest

from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownParser

PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4"
    "z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)
IMG = f"![example](data:image/png;base64,{PNG_B64})"


class InlineCodeBase64Tests(unittest.TestCase):
    def _parse(self, raw: str):
        return MarkdownParser(file_name="example.md").parse(raw.encode())

    def test_code_spans_are_kept_verbatim(self):
        for span in (
            f"`{IMG}`",
            f"``a ` b {IMG}``",
            f"`see\n{IMG}`",
        ):
            with self.subTest(span=span):
                document = self._parse(f"Example: {span}\n\n{IMG}")
                self.assertEqual(len(document.images), 1)
                path = next(iter(document.images))
                self.assertEqual(document.content, f"Example: {span}\n\n![example]({path})")

    def test_unmatched_or_escaped_backticks_do_not_protect(self):
        for prefix in ("a ` b", r"a \`", "a `` b ` c"):
            with self.subTest(prefix=prefix):
                text, images = MarkdownImageUtil().extract_base64(f"{prefix} {IMG}")
                self.assertEqual(len(images), 1)
                self.assertNotIn("data:image", text)

    def test_span_does_not_cross_paragraphs(self):
        text, images = MarkdownImageUtil().extract_base64(f"`open\n\n{IMG}`")
        self.assertEqual(len(images), 1)


if __name__ == "__main__":
    unittest.main()
