"""Unicode upload signatures must be decoded before legacy encodings."""

import codecs
import unittest

from docreader.utils.endecode import decode_bytes


class DecodeBytesBOMTests(unittest.TestCase):
    def test_unicode_signature_is_removed(self):
        for encoding in ["utf-8-sig", "utf-16", "utf-32"]:
            for text in ["# Heading\nHello", "# 中文\n| 名称 | 值 |"]:
                with self.subTest(encoding=encoding, text=text):
                    self.assertEqual(decode_bytes(text.encode(encoding)), text)

    def test_big_endian_signature(self):
        text = "# 中文\nHello"
        for bom, encoding in [
            (codecs.BOM_UTF16_BE, "utf-16-be"),
            (codecs.BOM_UTF32_BE, "utf-32-be"),
        ]:
            with self.subTest(encoding=encoding):
                self.assertEqual(decode_bytes(bom + text.encode(encoding)), text)

    def test_custom_encoding_order_is_respected(self):
        data = "hello".encode("utf-8-sig")
        self.assertEqual(decode_bytes(data, encodings=["latin-1"]), data.decode("latin-1"))

    def test_unsigned_text_preserves_legacy_detection(self):
        for encoding in ["utf-8", "gb18030"]:
            with self.subTest(encoding=encoding):
                text = "中文"
                self.assertEqual(decode_bytes(text.encode(encoding)), text)

    def test_incomplete_signature_uses_existing_fallback(self):
        self.assertIsInstance(decode_bytes(codecs.BOM_UTF16_LE + b"x"), str)
