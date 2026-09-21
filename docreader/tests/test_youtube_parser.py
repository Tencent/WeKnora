import unittest

from docreader.parser.youtube_parser import (
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


if __name__ == "__main__":
    unittest.main()
