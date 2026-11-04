"""Embedded Markdown images retain their bytes and titles during parsing."""

import base64
import unittest

from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownParser


PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4"
    "z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)


class MarkdownImageTitleTests(unittest.TestCase):
    def test_embedded_images_with_titles(self):
        for title in (
            '"图片"',
            '"logo"',
            "'Image (front)'",
            r'"Image with \"quoted\" text"',
            r"'Image with \'quoted\' text'",
            r"(图 \) 正面)",
            '"first line\nsecond line"',
            '""',
        ):
            with self.subTest(title=title):
                raw = f"![example](data:image/png;base64,{PNG_B64} {title})"
                document = MarkdownParser(file_name="example.md").parse(raw.encode())
                self.assertEqual(len(document.images), 1)
                path, payload = next(iter(document.images.items()))
                self.assertEqual(payload, PNG_B64)
                self.assertEqual(document.content, f"![example]({path} {title})")

    def test_replace_false_keeps_title_and_original_content(self):
        raw = f'![example](data:image/png;base64,{PNG_B64} "图片")'
        text, images = MarkdownImageUtil().extract_base64(raw, replace=False)
        self.assertEqual(text, raw)
        self.assertEqual(list(images.values()), [base64.b64decode(PNG_B64)])

    def test_non_ascii_payload_is_still_skipped(self):
        raw = f"![example](data:image/png;base64,{PNG_B64}图片)"
        document = MarkdownParser().parse(raw.encode())
        self.assertEqual(document.content, "example")
        self.assertEqual(document.images, {})

    def test_unterminated_long_image_is_left_unchanged(self):
        raw = "![example](data:image/png;base64," + "A" * 100000 + " " * 100000 + '"'
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))


if __name__ == "__main__":
    unittest.main()
