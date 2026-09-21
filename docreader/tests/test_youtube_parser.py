import json
import unittest
from unittest.mock import patch

from docreader.parser.youtube_parser import (
    YoutubeEnumerateParser,
    YoutubeParseError,
    YoutubeTranscriptParser,
    extract_video_id,
    is_playlist_url,
    is_youtube_url,
)


class TestYoutubeUrlHelpers(unittest.TestCase):
    def test_is_youtube_url_recognizes_known_hosts(self):
        self.assertTrue(is_youtube_url("https://www.youtube.com/watch?v=abc123"))
        self.assertTrue(is_youtube_url("https://youtu.be/abc123"))
        self.assertTrue(is_youtube_url("https://m.youtube.com/watch?v=abc123"))

    def test_is_youtube_url_rejects_other_hosts(self):
        self.assertFalse(is_youtube_url("https://example.com/watch?v=abc123"))
        self.assertFalse(is_youtube_url("not a url"))

    def test_extract_video_id_from_watch_url(self):
        self.assertEqual(
            extract_video_id("https://www.youtube.com/watch?v=abc123&t=10s"), "abc123"
        )

    def test_extract_video_id_from_short_url(self):
        self.assertEqual(extract_video_id("https://youtu.be/abc123"), "abc123")

    def test_extract_video_id_from_shorts_url(self):
        self.assertEqual(
            extract_video_id("https://www.youtube.com/shorts/abc123"), "abc123"
        )

    def test_extract_video_id_returns_none_for_playlist_only_url(self):
        self.assertIsNone(
            extract_video_id("https://www.youtube.com/playlist?list=PL123")
        )

    def test_is_playlist_url_true_for_list_without_v(self):
        self.assertTrue(
            is_playlist_url("https://www.youtube.com/playlist?list=PL123")
        )

    def test_is_playlist_url_false_for_video_with_list_param(self):
        # A video played from within a playlist still has one video to ingest.
        self.assertFalse(
            is_playlist_url("https://www.youtube.com/watch?v=abc123&list=PL123")
        )


class TestYoutubeEnumerateParser(unittest.TestCase):
    def test_single_video_returns_one_entry(self):
        info = {"_type": "video", "id": "abc123", "title": "My Video"}
        parser = YoutubeEnumerateParser(title="")
        with patch(
            "docreader.parser.youtube_parser._ytdlp_extract_info",
            return_value=info,
        ):
            doc = parser.parse_into_text(b"https://www.youtube.com/watch?v=abc123")
        self.assertTrue(doc.is_valid())
        self.assertEqual(doc.metadata["youtube_kind"], "video")
        entries = json.loads(doc.metadata["youtube_videos"])
        self.assertEqual(entries, [{
            "video_id": "abc123",
            "url": "https://www.youtube.com/watch?v=abc123",
            "title": "My Video",
        }])

    def test_playlist_returns_all_entries(self):
        info = {
            "_type": "playlist",
            "entries": [
                {"id": "vid1", "title": "First"},
                {"id": "vid2", "title": "Second"},
                None,  # yt-dlp can yield None for unavailable entries
            ],
        }
        parser = YoutubeEnumerateParser(title="")
        with patch(
            "docreader.parser.youtube_parser._ytdlp_extract_info",
            return_value=info,
        ):
            doc = parser.parse_into_text(
                b"https://www.youtube.com/playlist?list=PL123"
            )
        entries = json.loads(doc.metadata["youtube_videos"])
        self.assertEqual(doc.metadata["youtube_kind"], "playlist")
        self.assertEqual(len(entries), 2)
        self.assertEqual(entries[0]["video_id"], "vid1")
        self.assertEqual(
            entries[0]["url"], "https://www.youtube.com/watch?v=vid1"
        )

    def test_empty_playlist_raises(self):
        info = {"_type": "playlist", "entries": []}
        parser = YoutubeEnumerateParser(title="")
        with patch(
            "docreader.parser.youtube_parser._ytdlp_extract_info",
            return_value=info,
        ):
            with self.assertRaises(YoutubeParseError):
                parser.parse_into_text(
                    b"https://www.youtube.com/playlist?list=PL123"
                )


class TestYoutubeTranscriptParser(unittest.TestCase):
    def test_playlist_url_raises_before_fetching(self):
        from docreader.parser.youtube_parser import YoutubeTranscriptParser

        parser = YoutubeTranscriptParser(title="")
        with self.assertRaises(YoutubeParseError):
            parser.parse_into_text(
                b"https://www.youtube.com/playlist?list=PL123"
            )

    def test_no_video_id_raises(self):
        from docreader.parser.youtube_parser import YoutubeTranscriptParser

        parser = YoutubeTranscriptParser(title="")
        with self.assertRaises(YoutubeParseError):
            parser.parse_into_text(b"https://www.youtube.com/")

    def test_successful_transcript_uses_given_title(self):
        from docreader.parser.youtube_parser import YoutubeTranscriptParser

        transcript_items = [
            {"text": "Hello"},
            {"text": "world."},
        ]
        parser = YoutubeTranscriptParser(title="Given Title")
        with patch(
            "docreader.parser.youtube_parser._fetch_transcript_items",
            return_value=transcript_items,
        ):
            doc = parser.parse_into_text(
                b"https://www.youtube.com/watch?v=abc123"
            )
        self.assertTrue(doc.is_valid())
        self.assertIn("Hello world.", doc.content)
        self.assertIn("https://www.youtube.com/watch?v=abc123", doc.content)
        self.assertEqual(doc.metadata["title"], "Given Title")
        self.assertEqual(doc.metadata["youtube_video_id"], "abc123")

    def test_no_title_falls_back_to_ytdlp_lookup(self):
        from docreader.parser.youtube_parser import YoutubeTranscriptParser

        parser = YoutubeTranscriptParser(title="")
        with patch(
            "docreader.parser.youtube_parser._fetch_transcript_items",
            return_value=[{"text": "content"}],
        ), patch(
            "docreader.parser.youtube_parser._ytdlp_extract_info",
            return_value={"_type": "video", "id": "abc123", "title": "Fetched Title"},
        ):
            doc = parser.parse_into_text(
                b"https://www.youtube.com/watch?v=abc123"
            )
        self.assertEqual(doc.metadata["title"], "Fetched Title")

    def test_empty_transcript_raises(self):
        from docreader.parser.youtube_parser import YoutubeTranscriptParser

        parser = YoutubeTranscriptParser(title="t")
        with patch(
            "docreader.parser.youtube_parser._fetch_transcript_items",
            return_value=[{"text": "   "}],
        ):
            with self.assertRaises(YoutubeParseError):
                parser.parse_into_text(
                    b"https://www.youtube.com/watch?v=abc123"
                )

    def test_transcript_fetch_error_becomes_parse_error(self):
        from docreader.parser.youtube_parser import YoutubeTranscriptParser

        parser = YoutubeTranscriptParser(title="t")
        with patch(
            "docreader.parser.youtube_parser._fetch_transcript_items",
            side_effect=RuntimeError("no captions"),
        ):
            with self.assertRaises(YoutubeParseError):
                parser.parse_into_text(
                    b"https://www.youtube.com/watch?v=abc123"
                )


if __name__ == "__main__":
    unittest.main()
