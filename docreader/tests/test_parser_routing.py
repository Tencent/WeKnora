import unittest

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.parser import Parser, detect_effective_file_type
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.pdf_parser import PDFParser
from docreader.parser.registry import BUILTIN_ENGINE, ParserEngineRegistry, registry
from docreader.parser.xmind_parser import XMindParser


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
    def test_builtin_registry_routes_xmind_to_xmind_parser(self):
        parser_class = registry.get_parser_class(BUILTIN_ENGINE, "xmind")

        self.assertIs(parser_class, XMindParser)

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

    def test_empty_engine_routes_pptx_to_markitdown(self):
        self.assertIs(registry.get_parser_class("", "pptx"), MarkitdownParser)
        self.assertIs(registry.get_parser_class("", "ppt"), MarkitdownParser)
        self.assertIs(registry.get_parser_class("", ".PPTX"), MarkitdownParser)

    def test_builtin_engine_still_parses_pptx(self):
        self.assertIs(registry.get_parser_class(BUILTIN_ENGINE, "pptx"), MarkitdownParser)

    def test_type_default_when_builtin_lacks_pptx(self):
        isolated = ParserEngineRegistry()
        isolated.register(BUILTIN_ENGINE, {"pdf": PDFParser})
        isolated.register(
            "markitdown",
            {"pptx": MarkitdownParser, "ppt": MarkitdownParser, "csv": MarkitdownParser},
        )

        self.assertIs(isolated.get_parser_class("", "pptx"), MarkitdownParser)
        self.assertIs(isolated.get_parser_class(BUILTIN_ENGINE, "pptx"), MarkitdownParser)
        self.assertIs(isolated.get_parser_class("", "csv"), MarkitdownParser)
        self.assertIs(isolated.get_parser_class(BUILTIN_ENGINE, "csv"), MarkitdownParser)
        self.assertIs(isolated.get_parser_class(BUILTIN_ENGINE, "pdf"), PDFParser)
        self.assertIs(isolated.get_parser_class("", "pdf"), PDFParser)

        with self.assertRaises(ValueError) as ctx:
            isolated.get_parser_class("", "xyz")
        self.assertIn("Unsupported file type: xyz", str(ctx.exception))

    def test_actionable_error_lists_supporting_engines(self):
        isolated = ParserEngineRegistry()
        isolated.register(BUILTIN_ENGINE, {"pdf": PDFParser})
        isolated.register("opendataloader", {"xmind": XMindParser})

        with self.assertRaises(ValueError) as ctx:
            isolated.get_parser_class(BUILTIN_ENGINE, "xmind")
        message = str(ctx.exception)
        self.assertIn("Configure one of:", message)
        self.assertIn("opendataloader", message)


if __name__ == "__main__":
    unittest.main()
