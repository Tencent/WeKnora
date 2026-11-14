"""Base64 image extraction must preserve literal examples in fenced code."""

import base64
import unittest

from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownParser


PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4"
    "z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)
IMAGE = f"![example](data:image/png;base64,{PNG_B64})"


class MarkdownImageFenceTest(unittest.TestCase):
    def test_uploaded_markdown_preserves_fenced_images(self):
        for opening, closing in (
            ("```markdown", "```"),
            ("~~~markdown", "~~~"),
            ("````", "`````"),
            ("   ```markdown", "   ```"),
        ):
            raw = f"{opening}\n{IMAGE}\n{closing}\n"
            with self.subTest(opening=opening):
                document = MarkdownParser(file_name="example.md").parse(raw.encode())
                self.assertEqual(document.content, raw)
                self.assertEqual(document.images, {})

    def test_extracts_only_real_images_before_and_after_code(self):
        code = f"```markdown\n{IMAGE}\n```"
        raw = f"{IMAGE}\n\n{code}\n\n{IMAGE}"
        document = MarkdownParser().parse(raw.encode())
        self.assertIn(code, document.content)
        self.assertEqual(len(document.images), 2)
        for path, payload in document.images.items():
            self.assertIn(f"![example]({path})", document.content)
            self.assertEqual(payload, PNG_B64)

    def test_unclosed_fence_preserves_rest_of_document(self):
        raw = f"~~~markdown\n{IMAGE}\n"
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))

    def test_shorter_different_or_nonclosing_markers_stay_inside_code(self):
        raw = f"````markdown\n```\n~~~\n```` not a closer\n{IMAGE}\n````\n"
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))

    def test_helper_preserves_crlf_without_rewriting_examples(self):
        raw = f"```markdown\r\n{IMAGE}\r\n```\r\n"
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))

    def test_invalid_base64_inside_code_is_not_dropped(self):
        raw = "```markdown\n![example](data:image/png;base64,a)\n```"
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))

    def test_backtick_in_info_string_does_not_start_fence(self):
        raw = f"```lang`example\n{IMAGE}\n"
        text, images = MarkdownImageUtil().extract_base64(raw)
        self.assertEqual(len(images), 1)
        path = next(iter(images))
        self.assertEqual(text, f"```lang`example\n![example]({path})\n")
        self.assertEqual(images[path], base64.b64decode(PNG_B64))

    def test_multiline_base64_outside_code_and_replace_false_keep_working(self):
        image = IMAGE.replace(PNG_B64, PNG_B64[:40] + "\n" + PNG_B64[40:])
        text, images = MarkdownImageUtil().extract_base64(
            image, path_prefix="images", replace=False
        )
        self.assertEqual(text, image)
        self.assertEqual(len(images), 1)
        path = next(iter(images))
        self.assertTrue(path.startswith("images/"))
        self.assertEqual(images[path], base64.b64decode(PNG_B64))


if __name__ == "__main__":
    unittest.main()
