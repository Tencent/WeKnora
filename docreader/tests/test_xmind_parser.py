import io
import json
import unittest
import zipfile

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


class XMindParserModernTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
