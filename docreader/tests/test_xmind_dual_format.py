import io
import unittest
import zipfile

from docreader.parser.xmind_parser import XMindParser


def _archive(json_content):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w") as archive:
        archive.writestr("content.json", json_content)
        archive.writestr(
            "content.xml",
            "<xmap-content><sheet><title>Classic map</title><topic>"
            "<title>Recoverable topic</title></topic></sheet></xmap-content>",
        )
    return output.getvalue()


class XMindDualFormatTest(unittest.TestCase):
    def test_recovers_xml_when_json_is_unreadable(self):
        result = XMindParser(file_name="map.xmind").parse_into_text(_archive(b"{"))
        self.assertIn("Recoverable topic", result.content)
        self.assertEqual(result.metadata["xmind_content_format"], "xml")

    def test_prefers_json_when_both_formats_are_readable(self):
        content = b'[{"title":"Modern map","rootTopic":{"title":"Preferred topic"}}]'
        result = XMindParser(file_name="map.xmind").parse_into_text(_archive(content))
        self.assertIn("Preferred topic", result.content)
        self.assertNotIn("Recoverable topic", result.content)
        self.assertEqual(result.metadata["xmind_content_format"], "json")
