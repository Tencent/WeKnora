import logging
import re
from typing import Optional
from urllib.parse import parse_qs, urlparse

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
