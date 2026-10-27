"""Table normalization must preserve code examples in fenced blocks."""

import unittest

from docreader.parser.markdown_parser import MarkdownParser, MarkdownTableUtil


class MarkdownTableFenceTest(unittest.TestCase):
    def test_fenced_rows_are_preserved_and_surrounding_tables_are_formatted(self):
        for fence, closing in (
            ("```markdown", "```"),
            ("~~~text", "~~~"),
            ("````markdown", "````"),
            ("   ```text", "   ```"),
        ):
            code = fence + "\n|a|b|\n|---|---|\n|1|2|\n" + closing
            table = "|Name|Value|\n|---|---|\n|x|1|"
            formatted_table = "| Name | Value |\n| --- | --- |\n| x | 1 |"
            with self.subTest(fence=fence):
                self.assertEqual(
                    MarkdownTableUtil().format_table(table + "\n\n" + code + "\n\n" + table),
                    formatted_table + "\n\n" + code + "\n\n" + formatted_table,
                )

    def test_shorter_or_different_fence_does_not_close_code(self):
        raw = "````markdown\n```\n|a|b|\n~~~\n|c|d|\n`````\n"
        self.assertEqual(MarkdownTableUtil().format_table(raw), raw)

    def test_unclosed_fence_keeps_remaining_rows(self):
        raw = "~~~example\n|a|b|\n|---|---|\n|1|2|\n"
        self.assertEqual(MarkdownTableUtil().format_table(raw), raw)

    def test_fence_with_trailing_text_does_not_close_code(self):
        raw = "```text\n``` not a closer\n|a|b|\n```\n"
        self.assertEqual(MarkdownTableUtil().format_table(raw), raw)

    def test_backtick_in_info_string_is_not_an_opening_fence(self):
        raw = "```lang`example\n|a|b|\n|---|---|"
        self.assertEqual(
            MarkdownTableUtil().format_table(raw),
            "```lang`example\n| a | b |\n| --- | --- |",
        )

    def test_uploaded_markdown_preserves_fenced_table_source(self):
        raw = "```markdown\n|a|b|\n|---|---|\n|1|2|\n```\n"
        document = MarkdownParser(file_name="example.md", file_type="md").parse_into_text(raw.encode())
        self.assertIn(raw.rstrip(), document.content)
