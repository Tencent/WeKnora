import unittest

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.parser import Parser, detect_effective_file_type


OLE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"


class _RecordingParser(BaseParser):
    last_file_type = ""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        type(self).last_file_type = self.file_type

    def parse_into_text(self, content: bytes) -> Document:
        return Document(content="parsed")


class _RecordingRegistry:
    def __init__(self):
        self.requested_file_type = ""

    def get_parser_class(self, engine: str, file_type: str):
        self.requested_file_type = file_type
        return _RecordingParser


class ParserRoutingTest(unittest.TestCase):
    def test_legacy_doc_payload_renamed_to_docx_uses_doc_parser(self):
        registry = _RecordingRegistry()
        parser = Parser()
        parser.registry = registry

        result = parser.parse_file(
            "legacy.docx",
            "docx",
            OLE_MAGIC + b"legacy-word-payload",
        )

        self.assertEqual("parsed", result.content)
        self.assertEqual("doc", registry.requested_file_type)
        self.assertEqual("doc", _RecordingParser.last_file_type)

    def test_real_docx_payload_keeps_docx_parser_route(self):
        self.assertEqual(
            "docx",
            detect_effective_file_type(".DOCX", b"PK\x03\x04ooxml-payload"),
        )

    def test_unrelated_ole_type_is_not_reclassified(self):
        self.assertEqual("xls", detect_effective_file_type("xls", OLE_MAGIC))


if __name__ == "__main__":
    unittest.main()
