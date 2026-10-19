"""Unicode upload signatures must be decoded before legacy encodings."""

import codecs

import pytest

from docreader.utils.endecode import decode_bytes


@pytest.mark.parametrize("encoding", ["utf-8-sig", "utf-16", "utf-32"])
@pytest.mark.parametrize("text", ["# Heading\nHello", "# 中文\n| 名称 | 值 |"])
def test_unicode_signature_is_removed(encoding, text):
    assert decode_bytes(text.encode(encoding)) == text


@pytest.mark.parametrize("bom,encoding", [
    (codecs.BOM_UTF16_BE, "utf-16-be"),
    (codecs.BOM_UTF32_BE, "utf-32-be"),
])
def test_big_endian_signature(bom, encoding):
    text = "# 中文\nHello"
    assert decode_bytes(bom + text.encode(encoding)) == text


def test_custom_encoding_order_is_respected():
    data = "hello".encode("utf-8-sig")
    assert decode_bytes(data, encodings=["latin-1"]) == data.decode("latin-1")


@pytest.mark.parametrize("encoding", ["utf-8", "gb18030"])
def test_unsigned_text_preserves_legacy_detection(encoding):
    text = "中文"
    assert decode_bytes(text.encode(encoding)) == text


def test_incomplete_signature_uses_existing_fallback():
    assert isinstance(decode_bytes(codecs.BOM_UTF16_LE + b"x"), str)
