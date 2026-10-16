import unittest

from docreader.utils.split import (
    match_by_regex,
    split_by_char,
    split_by_regex,
    split_by_sep,
    split_text_keep_separator,
)


class SplitUtilsTest(unittest.TestCase):
    def test_split_text_keep_separator_basic(self):
        self.assertEqual(
            split_text_keep_separator("Hello\nWorld\nTest", "\n"),
            ["Hello", "\nWorld", "\nTest"],
        )

    def test_split_text_keep_separator_no_separator(self):
        self.assertEqual(split_text_keep_separator("no sep", ","), ["no sep"])

    def test_split_text_keep_separator_filters_empty_parts(self):
        self.assertEqual(split_text_keep_separator("a,,b", ","), ["a", ",", ",b"])

    def test_split_by_sep_keep(self):
        self.assertEqual(split_by_sep(",")("a,b,c"), ["a", ",b", ",c"])

    def test_split_by_sep_discard(self):
        self.assertEqual(split_by_sep(",", keep_sep=False)("a,,b"), ["a", "", "b"])

    def test_split_by_char(self):
        self.assertEqual(split_by_char()("abc"), ["a", "b", "c"])

    def test_split_by_regex_keeps_separators(self):
        self.assertEqual(split_by_regex("\n")("a\nb\nc"), ["a", "\n", "b", "\n", "c"])

    def test_match_by_regex(self):
        self.assertTrue(match_by_regex(r"^\d+")("123"))
        self.assertFalse(match_by_regex(r"^\d+")("abc"))


if __name__ == "__main__":
    unittest.main()
