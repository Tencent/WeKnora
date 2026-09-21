import json
import logging
import re
from typing import Optional
from urllib.parse import parse_qs, urlparse

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.utils import endecode

logger = logging.getLogger(__name__)

_YOUTUBE_HOSTS = {"youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be"}


class YoutubeParseError(RuntimeError):
    """Raised when a YouTube URL cannot be expanded or transcribed.

    Must propagate as a parse failure, never become indexed document body.
    """


def is_youtube_url(url: str) -> bool:
    try:
        host = (urlparse(url).hostname or "").lower()
    except ValueError:
        return False
    return host in _YOUTUBE_HOSTS


def extract_video_id(url: str) -> Optional[str]:
    parsed = urlparse(url)
    host = (parsed.hostname or "").lower()
    if host == "youtu.be":
        video_id = parsed.path.lstrip("/")
        return video_id or None

    query = parse_qs(parsed.query)
    if "v" in query and query["v"]:
        return query["v"][0]

    match = re.search(r"/shorts/([\w-]+)", parsed.path)
    if match:
        return match.group(1)

    return None


def is_playlist_url(url: str) -> bool:
    query = parse_qs(urlparse(url).query)
    return "list" in query and "v" not in query


def _ytdlp_extract_info(url: str) -> dict:
    """Thin wrapper around yt-dlp so tests can patch it without invoking the
    real network call. `extract_flat` avoids resolving playable formats —
    we only need ids/titles here.
    """
    import yt_dlp

    opts = {
        "extract_flat": "in_playlist",
        "quiet": True,
        "no_warnings": True,
        "skip_download": True,
        "noplaylist": not is_playlist_url(url),
    }
    with yt_dlp.YoutubeDL(opts) as ydl:
        return ydl.extract_info(url, download=False)


def _entries_from_info(info: dict) -> tuple[str, list[dict]]:
    if info.get("_type") == "playlist":
        entries = []
        for entry in info.get("entries") or []:
            if not entry or not entry.get("id"):
                continue
            entries.append({
                "video_id": entry["id"],
                "url": f"https://www.youtube.com/watch?v={entry['id']}",
                "title": entry.get("title") or entry["id"],
            })
        return "playlist", entries

    video_id = info.get("id")
    if not video_id:
        return "video", []
    return "video", [{
        "video_id": video_id,
        "url": f"https://www.youtube.com/watch?v={video_id}",
        "title": info.get("title") or video_id,
    }]


_TRANSCRIPT_LANGUAGES = ["vi", "en"]


def _fetch_transcript_items(video_id: str) -> list[dict]:
    """Thin wrapper around youtube-transcript-api so tests can patch it."""
    from youtube_transcript_api import YouTubeTranscriptApi

    return YouTubeTranscriptApi().fetch(
        video_id, languages=_TRANSCRIPT_LANGUAGES
    ).to_raw_data()


class YoutubeEnumerateParser(BaseParser):
    """Enumerates a YouTube URL (single video or playlist) without fetching
    transcripts. Used by the batch-import expansion step so a playlist can
    be listed before committing to per-video transcript work.
    """

    def __init__(self, title: str = "", **kwargs):
        self.title = title
        super().__init__(file_name=title, **kwargs)

    def parse_into_text(self, content: bytes) -> Document:
        url = endecode.decode_bytes(content)
        logger.info("Enumerating YouTube URL: %s", url)

        if not is_youtube_url(url):
            raise YoutubeParseError(f"Not a YouTube URL: {url}")

        try:
            info = _ytdlp_extract_info(url)
        except Exception as e:
            raise YoutubeParseError(f"Failed to read YouTube URL {url}: {e}") from e

        kind, entries = _entries_from_info(info)
        if not entries:
            raise YoutubeParseError(f"No videos found for: {url}")

        lines = [f"# YouTube {kind}", ""]
        lines += [f"- {e['title']} ({e['url']})" for e in entries]
        return Document(
            content="\n".join(lines),
            metadata={
                "youtube_kind": kind,
                "youtube_videos": json.dumps(entries),
            },
        )


class YoutubeTranscriptParser(BaseParser):
    """Fetches a single YouTube video's transcript as plain text (no
    timestamps). Raises YoutubeParseError for playlist URLs — those must be
    expanded first via YoutubeEnumerateParser (see the batch YouTube
    ingestion endpoint).
    """

    def __init__(self, title: str = "", **kwargs):
        self.title = title
        super().__init__(file_name=title, **kwargs)

    def parse_into_text(self, content: bytes) -> Document:
        url = endecode.decode_bytes(content)

        if is_playlist_url(url):
            raise YoutubeParseError(
                f"{url} is a playlist URL; use the YouTube batch import instead"
            )

        video_id = extract_video_id(url)
        if not video_id:
            raise YoutubeParseError(f"Could not resolve a video id for: {url}")

        logger.info("Fetching YouTube transcript for video: %s", video_id)
        try:
            items = _fetch_transcript_items(video_id)
        except Exception as e:
            raise YoutubeParseError(
                f"No transcript available for video {video_id}: {e}"
            ) from e

        text = " ".join(
            item.get("text", "").strip() for item in items if item.get("text", "").strip()
        )
        if not text:
            raise YoutubeParseError(f"Transcript for video {video_id} is empty")

        title = self.title
        if not title:
            try:
                info = _ytdlp_extract_info(url)
                _, entries = _entries_from_info(info)
                title = entries[0]["title"] if entries else video_id
            except Exception:
                title = video_id

        return Document(
            content=f"Source: {url}\n\n{text}",
            metadata={"title": title, "youtube_video_id": video_id},
        )
