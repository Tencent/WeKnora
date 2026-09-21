import unittest
from unittest.mock import patch

from docreader.models.document import Document
from docreader.parser.parser import Parser


class TestParseUrlYoutubeDispatch(unittest.TestCase):
    def setUp(self) -> None:
        self.parser = Parser()

    def test_youtube_enumerate_engine_uses_enumerate_parser(self):
        with patch(
            "docreader.parser.parser.YoutubeEnumerateParser.parse_into_text",
            return_value=Document(content="ok", metadata={"youtube_kind": "video"}),
        ) as mocked:
            result = self.parser.parse_url(
                "https://www.youtube.com/watch?v=abc123",
                "",
                parser_engine="youtube-enumerate",
            )
        mocked.assert_called_once()
        self.assertEqual(result.metadata["youtube_kind"], "video")

    def test_plain_youtube_url_uses_transcript_parser_by_default(self):
        with patch(
            "docreader.parser.parser.YoutubeTranscriptParser.parse_into_text",
            return_value=Document(
                content="Source: x\n\ntranscript",
                metadata={"title": "T", "youtube_video_id": "abc123"},
            ),
        ) as mocked:
            result = self.parser.parse_url(
                "https://www.youtube.com/watch?v=abc123", "T"
            )
        mocked.assert_called_once()
        self.assertEqual(result.metadata["youtube_video_id"], "abc123")

    def test_non_youtube_url_still_uses_web_parser(self):
        with patch(
            "docreader.parser.parser.WebParser.parse_into_text",
            return_value=Document(content="ok", metadata={}),
        ) as mocked:
            self.parser.parse_url("https://example.com/article", "")
        mocked.assert_called_once()


if __name__ == "__main__":
    unittest.main()
