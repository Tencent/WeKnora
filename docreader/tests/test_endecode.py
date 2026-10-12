import base64
import binascii
import re
import unittest

from docreader.utils.endecode import encode_image

# One valid PNG signature, the shortest payload that proves a decode happened.
PNG_SIGNATURE = bytes.fromhex("89504e470d0a1a0a")
VALID_B64 = base64.b64encode(PNG_SIGNATURE).decode()


class TestEncodeImage(unittest.TestCase):
    def test_valid_base64_round_trips(self):
        self.assertEqual(encode_image(VALID_B64), PNG_SIGNATURE)

    def test_ignore_swallows_bad_padding(self):
        """A truncated payload raises binascii.Error, which 'ignore' absorbs."""
        self.assertEqual(encode_image(VALID_B64[:-3], errors="ignore"), b"")

    def test_ignore_swallows_non_ascii(self):
        """Non-ASCII input is a decoding error too, so 'ignore' must absorb it.

        base64.b64decode rejects a non-ASCII str with a plain ValueError rather
        than binascii.Error, so catching only binascii.Error lets it escape a
        call that asked not to raise.
        """
        self.assertEqual(encode_image(VALID_B64 + ' "图片"', errors="ignore"), b"")

    def test_strict_still_raises_on_non_ascii(self):
        with self.assertRaises(ValueError):
            encode_image(VALID_B64 + ' "图片"')

    def test_strict_still_raises_on_bad_padding(self):
        with self.assertRaises(binascii.Error):
            encode_image(VALID_B64[:-3])


class TestMarkdownBase64Reachability(unittest.TestCase):
    """The payload group of the markdown data-URI pattern is not ASCII-only.

    Pinned here rather than in the parser tests because importing
    docreader.parser pulls in the whole parser dependency set; the pattern
    itself is what decides whether encode_image can ever see non-ASCII.
    """

    # docreader/parser/markdown_parser.py
    B64_PATTERN = re.compile(r"!\[(.*?)\]\(data:image/([^;]+);base64,([^\)]+)\)")

    def test_markdown_title_lands_in_the_payload_group(self):
        markdown = '![x](data:image/png;base64,%s "图片")' % VALID_B64
        match = self.B64_PATTERN.search(markdown)
        self.assertIsNotNone(match)
        self.assertIn("图片", match.group(3))
        # What the parser then hands to encode_image(errors="ignore").
        self.assertEqual(encode_image(match.group(3), errors="ignore"), b"")


if __name__ == "__main__":
    unittest.main()
