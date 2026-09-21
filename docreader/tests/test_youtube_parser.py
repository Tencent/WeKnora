import json
import unittest
from unittest.mock import MagicMock, patch

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

    def test_youtube_transcript_api_has_expected_fetch_method(self):
        """Guards against another breaking API change silently passing every
        mocked test — this is the one place this module touches the real
        youtube_transcript_api surface without mocking it."""
        from youtube_transcript_api import YouTubeTranscriptApi

        self.assertTrue(hasattr(YouTubeTranscriptApi, "fetch"))
        result = YouTubeTranscriptApi.fetch
        self.assertTrue(callable(result))


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

    def test_non_youtube_url_raises_before_ytdlp(self):
        parser = YoutubeEnumerateParser(title="")
        with patch(
            "docreader.parser.youtube_parser._ytdlp_extract_info",
        ) as mock_extract:
            with self.assertRaises(YoutubeParseError):
                parser.parse_into_text(b"https://example.com/watch?v=abc123")
        mock_extract.assert_not_called()


class TestYtdlpNoplaylistOption(unittest.TestCase):
    """Verifies _ytdlp_extract_info sets `noplaylist` consistently with
    is_playlist_url, so a `watch?v=X&list=Y` URL behaves the same (single
    video) whether it goes through YoutubeTranscriptParser (which checks
    is_playlist_url directly) or YoutubeEnumerateParser/yt-dlp."""

    def _captured_opts(self, url):
        from docreader.parser import youtube_parser

        captured = {}

        class FakeYoutubeDL:
            def __init__(self, opts):
                captured["opts"] = opts

            def __enter__(self):
                return self

            def __exit__(self, *exc_info):
                return False

            def extract_info(self, url, download=False):
                return {"_type": "video", "id": "abc123"}

        fake_yt_dlp = type("FakeModule", (), {"YoutubeDL": FakeYoutubeDL})
        with patch.dict("sys.modules", {"yt_dlp": fake_yt_dlp}):
            youtube_parser._ytdlp_extract_info(url)
        return captured["opts"]

    def test_noplaylist_true_for_video_with_list_param(self):
        opts = self._captured_opts(
            "https://www.youtube.com/watch?v=abc123&list=PL123"
        )
        self.assertTrue(opts["noplaylist"])

    def test_noplaylist_false_for_playlist_url(self):
        opts = self._captured_opts(
            "https://www.youtube.com/playlist?list=PL123"
        )
        self.assertFalse(opts["noplaylist"])


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


class TestFetchTranscriptItemsFallback(unittest.TestCase):
    """_fetch_transcript_items falls back to any available transcript when
    neither vi nor en exists, instead of failing the video outright.
    """

    def test_falls_back_to_available_language_when_vi_en_missing(self):
        from docreader.parser.youtube_parser import _fetch_transcript_items
        from youtube_transcript_api import NoTranscriptFound

        fake_fetched = MagicMock()
        fake_fetched.to_raw_data.return_value = [{"text": "こんにちは"}]

        fake_transcript = MagicMock()
        fake_transcript.language_code = "ja"
        fake_transcript.is_generated = False
        fake_transcript.fetch.return_value = fake_fetched

        fake_api = MagicMock()
        fake_api.fetch.side_effect = NoTranscriptFound(
            "abc123", ["vi", "en"], MagicMock()
        )
        fake_api.list.return_value = [fake_transcript]

        with patch(
            "youtube_transcript_api.YouTubeTranscriptApi",
            return_value=fake_api,
        ):
            result = _fetch_transcript_items("abc123")

        self.assertEqual(result, [{"text": "こんにちは"}])
        fake_transcript.fetch.assert_called_once()

    def test_reraises_when_no_transcripts_exist_at_all(self):
        from docreader.parser.youtube_parser import _fetch_transcript_items
        from youtube_transcript_api import NoTranscriptFound

        fake_api = MagicMock()
        fake_api.fetch.side_effect = NoTranscriptFound(
            "abc123", ["vi", "en"], MagicMock()
        )
        fake_api.list.return_value = []

        with patch(
            "youtube_transcript_api.YouTubeTranscriptApi",
            return_value=fake_api,
        ):
            with self.assertRaises(NoTranscriptFound):
                _fetch_transcript_items("abc123")

    def test_prefers_vi_en_when_available_no_fallback_needed(self):
        from docreader.parser.youtube_parser import _fetch_transcript_items

        fake_fetched = MagicMock()
        fake_fetched.to_raw_data.return_value = [{"text": "hello"}]

        fake_api = MagicMock()
        fake_api.fetch.return_value = fake_fetched

        with patch(
            "youtube_transcript_api.YouTubeTranscriptApi",
            return_value=fake_api,
        ):
            result = _fetch_transcript_items("abc123")

        self.assertEqual(result, [{"text": "hello"}])
        fake_api.list.assert_not_called()


if __name__ == "__main__":
    unittest.main()
