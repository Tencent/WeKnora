import io
import json
import unittest
import zipfile
from unittest.mock import patch

from docreader.parser.xmind_parser import XMindParser


def _xmind_zip(entries: dict[str, bytes | str]) -> bytes:
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, value in entries.items():
            payload = value.encode("utf-8") if isinstance(value, str) else value
            archive.writestr(name, payload)
    return buffer.getvalue()


def _modern_xmind_bytes(sheets: list[dict]) -> bytes:
    return _xmind_zip({"content.json": json.dumps(sheets, ensure_ascii=False)})


def _classic_xmind_bytes(xml: str) -> bytes:
    return _xmind_zip({"content.xml": xml})


def _mark_entry_encrypted(payload: bytes) -> bytes:
    marked = bytearray(payload)
    local_header = marked.index(b"PK\x03\x04")
    central_header = marked.index(b"PK\x01\x02")
    for offset in (local_header + 6, central_header + 8):
        flags = int.from_bytes(marked[offset : offset + 2], "little") | 0x1
        marked[offset : offset + 2] = flags.to_bytes(2, "little")
    return bytes(marked)


class XMindParserModernTests(unittest.TestCase):
    def test_finds_content_without_enumerating_unrelated_entries(self):
        payload = _xmind_zip(
            {
                "attachments/preview.png": b"preview",
                "content.json": json.dumps(
                    [{"title": "Outline", "rootTopic": {"title": "Root"}}]
                ),
            }
        )

        with patch.object(
            zipfile.ZipFile,
            "namelist",
            side_effect=AssertionError("archive entries must not be enumerated"),
        ):
            document = XMindParser().parse_into_text(payload)

        self.assertEqual("# Outline\n\n- Root", document.content)

    def test_parses_topic_hierarchy_and_plain_notes(self):
        payload = _modern_xmind_bytes(
            [
                {
                    "title": "Launch Plan",
                    "rootTopic": {
                        "title": "Release",
                        "notes": {"plain": {"content": "Coordinate teams"}},
                        "children": {
                            "attached": [
                                {
                                    "title": "Backend",
                                    "children": {
                                        "attached": [{"title": "API freeze"}]
                                    },
                                }
                            ]
                        },
                    },
                }
            ]
        )

        document = XMindParser(file_name="launch.xmind").parse_into_text(payload)

        self.assertEqual(
            "# Launch Plan\n\n"
            "- Release\n"
            "  > Coordinate teams\n"
            "  - Backend\n"
            "    - API freeze",
            document.content,
        )
        self.assertEqual(document.metadata["source_format"], "xmind")
        self.assertEqual(document.metadata["xmind_content_format"], "json")
        self.assertEqual(document.metadata["sheet_count"], 1)
        self.assertEqual(document.metadata["topic_count"], 3)
        self.assertEqual(document.metadata["note_count"], 1)
        self.assertEqual(document.metadata["file_size"], len(payload))

    def test_renders_multiple_sheets_with_fallback_title(self):
        payload = _modern_xmind_bytes(
            [
                {"title": "One", "rootTopic": {"title": "Alpha"}},
                {"title": "  ", "rootTopic": {"title": "Beta"}},
            ]
        )

        document = XMindParser().parse_into_text(payload)

        self.assertEqual(
            "# One\n\n- Alpha\n\n---\n\n# Sheet 2\n\n- Beta",
            document.content,
        )
        self.assertEqual(document.metadata["sheet_count"], 2)
        self.assertEqual(document.metadata["topic_count"], 2)

    def test_promotes_children_of_blank_topic(self):
        payload = _modern_xmind_bytes(
            [
                {
                    "title": "Outline",
                    "rootTopic": {
                        "title": "  ",
                        "children": {
                            "attached": [
                                {
                                    "title": "Visible",
                                    "children": {
                                        "attached": [{"title": "Nested"}]
                                    },
                                }
                            ]
                        },
                    },
                }
            ]
        )

        document = XMindParser().parse_into_text(payload)

        self.assertEqual("# Outline\n\n- Visible\n  - Nested", document.content)
        self.assertEqual(document.metadata["topic_count"], 2)

    def test_renders_multiline_note_as_blockquotes(self):
        payload = _modern_xmind_bytes(
            [
                {
                    "title": "Notes",
                    "rootTopic": {
                        "title": "Root",
                        "notes": {
                            "plain": {"content": " First line \n\n Second line "}
                        },
                    },
                }
            ]
        )

        document = XMindParser().parse_into_text(payload)

        self.assertEqual(
            "# Notes\n\n- Root\n  > First line\n  >\n  > Second line",
            document.content,
        )


class XMindParserClassicTests(unittest.TestCase):
    def test_parses_namespaced_xml_hierarchy_and_notes(self):
        payload = _classic_xmind_bytes(
            """<?xml version="1.0" encoding="UTF-8"?>
<xmap-content xmlns="urn:xmind:xmap:xmlns:content:2.0">
  <sheet>
    <title>Architecture</title>
    <topic>
      <title>Platform</title>
      <notes><plain>Owns ingress</plain></notes>
      <children>
        <topics type="attached">
          <topic><title>Gateway</title></topic>
        </topics>
      </children>
    </topic>
  </sheet>
</xmap-content>
"""
        )

        document = XMindParser().parse_into_text(payload)

        self.assertEqual(
            "# Architecture\n\n- Platform\n  > Owns ingress\n  - Gateway",
            document.content,
        )
        self.assertEqual(document.metadata["xmind_content_format"], "xml")
        self.assertEqual(document.metadata["sheet_count"], 1)
        self.assertEqual(document.metadata["topic_count"], 2)
        self.assertEqual(document.metadata["note_count"], 1)

    def test_prefers_content_json_when_both_entries_exist(self):
        xml = """<xmap-content><sheet><title>XML</title>
<topic><title>XML topic</title></topic></sheet></xmap-content>"""
        json_content = json.dumps(
            [{"title": "JSON", "rootTopic": {"title": "JSON topic"}}]
        )
        payload = _xmind_zip(
            {"content.xml": xml, "content.json": json_content}
        )

        document = XMindParser().parse_into_text(payload)

        self.assertEqual("# JSON\n\n- JSON topic", document.content)
        self.assertEqual(document.metadata["xmind_content_format"], "json")


class XMindParserValidationTests(unittest.TestCase):
    def test_rejects_invalid_zip(self):
        with self.assertRaises(ValueError) as context:
            XMindParser().parse_into_text(b"not a ZIP archive")

        self.assertEqual(str(context.exception), "invalid XMind archive")

    def test_rejects_archive_without_supported_content(self):
        payload = _xmind_zip({"manifest.json": "{}"})

        with self.assertRaises(ValueError) as context:
            XMindParser().parse_into_text(payload)

        self.assertEqual(
            str(context.exception),
            "XMind archive is missing content.json or content.xml",
        )

    def test_rejects_malformed_json(self):
        payload = _xmind_zip({"content.json": "{"})

        with self.assertRaises(ValueError) as context:
            XMindParser().parse_into_text(payload)

        self.assertEqual(str(context.exception), "invalid XMind content.json")

    def test_rejects_malformed_xml(self):
        payload = _classic_xmind_bytes("<xmap-content>")

        with self.assertRaises(ValueError) as context:
            XMindParser().parse_into_text(payload)

        self.assertEqual(str(context.exception), "invalid XMind content.xml")

    def test_rejects_archive_without_renderable_topics(self):
        payload = _modern_xmind_bytes(
            [{"title": "Empty", "rootTopic": {"title": " "}}]
        )

        with self.assertRaises(ValueError) as context:
            XMindParser().parse_into_text(payload)

        self.assertEqual(
            str(context.exception),
            "XMind archive contains no renderable topics",
        )

    def test_rejects_content_entry_over_limit(self):
        payload = _xmind_zip({"content.json": "12345"})

        with patch("docreader.parser.xmind_parser.MAX_CONTENT_BYTES", 4):
            with self.assertRaises(ValueError) as context:
                XMindParser().parse_into_text(payload)

        self.assertEqual(
            str(context.exception),
            "XMind content entry exceeds the 32 MiB limit",
        )

    def test_rejects_encrypted_content_entry(self):
        payload = _mark_entry_encrypted(
            _xmind_zip({"content.json": "[]"})
        )

        with self.assertRaises(ValueError) as context:
            XMindParser().parse_into_text(payload)

        self.assertEqual(
            str(context.exception),
            "encrypted XMind content is not supported",
        )


if __name__ == "__main__":
    unittest.main()
