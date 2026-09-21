# YouTube Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user paste one or more YouTube URLs (single videos or playlists) into a WeKnora knowledge base and have WeKnora fetch each video's transcript (no timestamps) and ingest it as a knowledge item, reusing the existing URL-ingestion pipeline end to end.

**Architecture:** The Python `docreader` gRPC sidecar (already the parser for every non-trivial file/URL type) gains a `youtube_parser.py` with two parsers: a fast enumerator (yt-dlp `extract_flat`, expands a playlist into `{video_id,url,title}` entries) and a transcript parser (`youtube-transcript-api`, preferring `vi`/`en`). `docreader/parser/parser.py::parse_url` is extended to auto-detect YouTube hosts and route to the transcript parser by default, and to route to the enumerator when the Go caller explicitly asks via `parser_engine="youtube-enumerate"`. On the Go side, a new `knowledgeService.CreateKnowledgeFromYoutube` calls the enumerator synchronously per input URL to expand playlists, then calls the **existing** `CreateKnowledgeFromURL` once per resulting video URL — so RBAC, SSRF checks, dedup, async processing, and the transcript-parser dispatch are all reused for free, with zero proto changes. A new handler + route (`POST /knowledge-bases/:id/knowledge/youtube`) exposes this as a batch endpoint that never aborts on one bad video. The frontend gets a dedicated "Import from YouTube" dialog (multi-line textarea, one URL per line) that clears itself on success and reports success/failure counts.

**Tech Stack:** Go 1.26 + Gin (`internal/`), Python 3.10 + `docreader` gRPC service (`docreader/`) using `yt-dlp` and `youtube-transcript-api`, Vue 3 + TDesign frontend (`frontend/`).

**Spec:** `/home/pc/WeKnora/YoutubeIngestion.md` (original Vietnamese feature request — the architecture in this plan supersedes its Python/FastAPI backend assumptions, which do not match this repo; see conversation history for the feasibility analysis).

## Global Constraints

- No new services, ports, or proto/gRPC message changes — everything rides the existing `Read`/`ReadRequest`/`ReadResult` RPC already used for every URL/file parse.
- Preferred transcript languages, in order: `vi`, then `en` (per spec).
- A failure on one URL/video (no captions, private video, deleted, etc.) must not abort the batch — record it and continue (per spec).
- No temp files written to disk for transcript text; pass content directly through the existing in-memory `Read` → `Document` → knowledge-creation pipeline (per spec).
- The frontend textarea clears as soon as the batch is submitted, and a toast reports success/failure counts once the API responds — the same fire-and-forget UX the existing single-URL import dialog already uses in this codebase (a deliberate, documented deviation from the original spec's literal "clear only after 200 OK", chosen for consistency with existing conventions).
- Follow existing code conventions exactly: Go stub-struct tests with `testify/require` (no gomock/testify-mock), Python `unittest.TestCase` with `unittest.mock`, Vue components using TDesign + `vue-i18n`, i18n keys mirrored across all 5 locale files (`en-US`, `zh-CN`, `ru-RU`, `ko-KR`, `ja-JP`).

---

## File Structure

**Python (`docreader/`)**
- `docreader/pyproject.toml` — add `yt-dlp` and `youtube-transcript-api` dependencies.
- `docreader/parser/youtube_parser.py` (new) — URL helpers, `YoutubeEnumerateParser`, `YoutubeTranscriptParser`.
- `docreader/parser/parser.py` (modify) — `parse_url` dispatches to the new parsers.
- `docreader/tests/test_youtube_parser.py` (new) — unit tests for all of the above.

**Go (`internal/`)**
- `internal/types/knowledge.go` (modify) — add `YoutubeIngestFailure`, `YoutubeIngestResult`.
- `internal/types/interfaces/knowledge.go` (modify) — add `CreateKnowledgeFromYoutube` to `KnowledgeService`.
- `internal/application/service/knowledge_create.go` (modify) — add `CreateKnowledgeFromYoutube` + `expandYoutubeURL` + `youtubeVideoEntry`.
- `internal/application/service/knowledge_create_youtube_test.go` (new) — service-level tests.
- `internal/handler/knowledge.go` (modify) — add `CreateKnowledgeFromYoutube` handler.
- `internal/handler/knowledge_youtube_test.go` (new) — handler-level tests.
- `internal/router/routes_knowledge.go` (modify) — register `POST /knowledge-bases/:id/knowledge/youtube`.

**Frontend (`frontend/src/`)**
- `api/knowledge-base/index.ts` (modify) — add `createKnowledgeFromYoutube`.
- `views/knowledge/utils/youtubeImport.ts` (new) — pure textarea-parsing/validation logic.
- `views/knowledge/utils/youtubeImport.test.ts` (new) — unit tests (`node:test`).
- `views/knowledge/components/KbUploadSourceDropdown.vue` (modify) — add "Import from YouTube" menu item + dialog.
- `views/knowledge/KnowledgeBase.vue` (modify) — wire the new `youtube` emit to a submit handler + toast.
- `i18n/locales/{en-US,zh-CN,ru-RU,ko-KR,ja-JP}.ts` (modify) — add `youtubeImport*` keys next to the existing `importURL`/`urlLabel` block.

---

## Task 1: Python dependencies

**Files:**
- Modify: `docreader/pyproject.toml`
- Test: manual (`uv lock` succeeding is the verification; no application code yet)

- [ ] **Step 1: Add the two dependencies**

Edit the `dependencies = [...]` array in `docreader/pyproject.toml`, inserting alphabetically:

```toml
    "requests>=2.33.0",
    "textract==1.5.0",
    "trafilatura>=2.0.0",
    "youtube-transcript-api>=0.6.2",
    "yt-dlp>=2024.12.13",
```

(i.e. add the two new lines after `trafilatura`, keeping the closing `]` on its own line as before.)

- [ ] **Step 2: Regenerate the lock file**

Run: `cd /home/pc/WeKnora/docreader && uv lock`
Expected: `uv.lock` is updated with `yt-dlp` and `youtube-transcript-api` entries; command exits 0.

- [ ] **Step 3: Commit**

```bash
git add docreader/pyproject.toml docreader/uv.lock
git commit -m "feat(docreader): add yt-dlp and youtube-transcript-api dependencies"
```

---

## Task 2: YouTube URL helper functions

**Files:**
- Create: `docreader/parser/youtube_parser.py` (helpers only in this task)
- Test: `docreader/tests/test_youtube_parser.py`

**Interfaces:**
- Produces: `is_youtube_url(url: str) -> bool`, `extract_video_id(url: str) -> Optional[str]`, `is_playlist_url(url: str) -> bool` — used by Task 3/4/5.

- [ ] **Step 1: Write the failing tests**

Create `docreader/tests/test_youtube_parser.py`:

```python
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_youtube_parser -v`
Expected: FAIL / ImportError — `docreader.parser.youtube_parser` does not exist yet.

- [ ] **Step 3: Write the minimal implementation**

Create `docreader/parser/youtube_parser.py`:

```python
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_youtube_parser -v`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add docreader/parser/youtube_parser.py docreader/tests/test_youtube_parser.py
git commit -m "feat(docreader): add YouTube URL parsing helpers"
```

---

## Task 3: YoutubeEnumerateParser (playlist/video expansion via yt-dlp)

**Files:**
- Modify: `docreader/parser/youtube_parser.py`
- Test: `docreader/tests/test_youtube_parser.py`

**Interfaces:**
- Consumes: nothing new from Task 2 beyond what's already imported.
- Produces: `YoutubeEnumerateParser(BaseParser)` with `parse_into_text(content: bytes) -> Document`; `Document.metadata` contains `youtube_kind` (`"video"` or `"playlist"`) and `youtube_videos` (a JSON string: `[{"video_id","url","title"}, ...]`) — Task 6 (Go) unmarshals this JSON.

- [ ] **Step 1: Write the failing tests**

Append to `docreader/tests/test_youtube_parser.py` (add the import line to the existing `from docreader.parser.youtube_parser import (...)` block: add `YoutubeEnumerateParser`, and add `from unittest.mock import patch`):

```python
from unittest.mock import patch

from docreader.parser.youtube_parser import (  # noqa: F811 (extends earlier import)
    YoutubeEnumerateParser,
    YoutubeParseError,
    extract_video_id,
    is_playlist_url,
    is_youtube_url,
)
import json


class TestYoutubeEnumerateParser(unittest.TestCase):
    def _entries_response(self, info):
        return info

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_youtube_parser -v`
Expected: FAIL — `YoutubeEnumerateParser` / `_ytdlp_extract_info` do not exist.

- [ ] **Step 3: Write the minimal implementation**

Append to `docreader/parser/youtube_parser.py`:

```python
import json

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.utils import endecode


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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_youtube_parser -v`
Expected: PASS (10 tests total)

- [ ] **Step 5: Commit**

```bash
git add docreader/parser/youtube_parser.py docreader/tests/test_youtube_parser.py
git commit -m "feat(docreader): add YoutubeEnumerateParser for playlist expansion"
```

---

## Task 4: YoutubeTranscriptParser (transcript fetch via youtube-transcript-api)

**Files:**
- Modify: `docreader/parser/youtube_parser.py`
- Test: `docreader/tests/test_youtube_parser.py`

**Interfaces:**
- Consumes: `extract_video_id`, `is_playlist_url`, `YoutubeParseError` from Task 2/3.
- Produces: `YoutubeTranscriptParser(BaseParser)` with `parse_into_text(content: bytes) -> Document`; `Document.metadata["title"]` and `Document.metadata["youtube_video_id"]`; `Document.content` = `"Source: <url>\n\n<transcript text>"`.

- [ ] **Step 1: Write the failing tests**

Append to `docreader/tests/test_youtube_parser.py` (add `YoutubeTranscriptParser` to the import block):

```python
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_youtube_parser -v`
Expected: FAIL — `YoutubeTranscriptParser` / `_fetch_transcript_items` do not exist.

- [ ] **Step 3: Write the minimal implementation**

Append to `docreader/parser/youtube_parser.py`:

```python
_TRANSCRIPT_LANGUAGES = ["vi", "en"]


def _fetch_transcript_items(video_id: str) -> list[dict]:
    """Thin wrapper around youtube-transcript-api so tests can patch it."""
    from youtube_transcript_api import YouTubeTranscriptApi

    return YouTubeTranscriptApi.get_transcript(
        video_id, languages=_TRANSCRIPT_LANGUAGES
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_youtube_parser -v`
Expected: PASS (16 tests total)

- [ ] **Step 5: Commit**

```bash
git add docreader/parser/youtube_parser.py docreader/tests/test_youtube_parser.py
git commit -m "feat(docreader): add YoutubeTranscriptParser for single-video transcripts"
```

---

## Task 5: Wire YouTube dispatch into `parse_url`

**Files:**
- Modify: `docreader/parser/parser.py`
- Test: `docreader/tests/test_parser.py` (create if it doesn't already exist — check first with `ls docreader/tests/test_parser.py`; if it exists, append to it instead of creating)

**Interfaces:**
- Consumes: `is_youtube_url`, `YoutubeEnumerateParser`, `YoutubeTranscriptParser` from `docreader.parser.youtube_parser`.
- Produces: `Parser.parse_url(url, title, parser_engine="youtube-enumerate", ...)` always enumerates; any YouTube URL with any other `parser_engine` value gets the transcript parser instead of `WebParser`; non-YouTube URLs are unaffected.

- [ ] **Step 1: Write the failing tests**

Check first: `ls /home/pc/WeKnora/docreader/tests/test_parser.py`. If absent, create `docreader/tests/test_parser.py` with this content (if present, add the `TestParseUrlYoutubeDispatch` class to it, adjusting imports to match the existing file's style):

```python
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_parser -v`
Expected: FAIL — `parser.py` still always uses `WebParser`.

- [ ] **Step 3: Write the minimal implementation**

Edit `docreader/parser/parser.py`:

```python
from docreader.parser.web_parser import WebParser
from docreader.parser.youtube_parser import (
    YoutubeEnumerateParser,
    YoutubeTranscriptParser,
    is_youtube_url,
)
```

(add the new import block right after the existing `from docreader.parser.web_parser import WebParser` line)

Replace the body of `parse_url`:

```python
    def parse_url(
        self,
        url: str,
        title: str,
        parser_engine: Optional[str] = None,
        engine_overrides: Optional[dict[str, Any]] = None,
    ) -> Document:
        """Parse content from a URL to markdown."""
        logger.info("Parsing URL: %s, title: %s", url, title)

        if parser_engine == "youtube-enumerate":
            parser = YoutubeEnumerateParser(title=title)
        elif is_youtube_url(url):
            parser = YoutubeTranscriptParser(title=title)
        else:
            parser = WebParser(title=title)

        logger.info("Starting to parse URL content with %s", type(parser).__name__)
        result = parser.parse(url.encode())

        if not result.content:
            logger.warning("Parser returned empty content for url: %s", url)
        logger.info("Parsed url %s, content length=%d", url, len(result.content))
        return result
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest tests.test_parser tests.test_youtube_parser tests.test_web_parser -v`
Expected: PASS, all three suites green.

- [ ] **Step 5: Commit**

```bash
git add docreader/parser/parser.py docreader/tests/test_parser.py
git commit -m "feat(docreader): dispatch YouTube URLs to the YouTube parsers"
```

---

## Task 6: Go types + interface for batch YouTube ingestion

**Files:**
- Modify: `internal/types/knowledge.go`
- Modify: `internal/types/interfaces/knowledge.go`
- Test: none dedicated (pure type/interface declarations) — compiled and exercised by Task 7's tests.

**Interfaces:**
- Produces:
  - `types.YoutubeIngestFailure{URL string, Error string}`
  - `types.YoutubeIngestResult{SuccessCount int, Knowledge []*types.Knowledge, Failed []types.YoutubeIngestFailure}`
  - `interfaces.KnowledgeService.CreateKnowledgeFromYoutube(ctx context.Context, kbID string, urls []string, tagIDs []string, channel string) (*types.YoutubeIngestResult, error)`

- [ ] **Step 1: Add the result types**

In `internal/types/knowledge.go`, add near the other Knowledge-related payload/result types (e.g. right after the `Knowledge` struct definition or near `ManualKnowledgePayload`):

```go
// YoutubeIngestFailure records one URL (input or expanded video) that
// failed during batch YouTube ingestion, with a human-readable reason.
type YoutubeIngestFailure struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// YoutubeIngestResult is the aggregate outcome of a batch YouTube import:
// zero or more knowledge records created (one per expanded video), plus
// the per-URL failures that did not abort the rest of the batch.
type YoutubeIngestResult struct {
	SuccessCount int                    `json:"success_count"`
	Knowledge    []*Knowledge           `json:"knowledge"`
	Failed       []YoutubeIngestFailure `json:"failed"`
}
```

- [ ] **Step 2: Add the interface method**

In `internal/types/interfaces/knowledge.go`, add to the `KnowledgeService` interface, right after the existing `CreateKnowledgeFromURL` method block:

```go
	// CreateKnowledgeFromYoutube expands each URL (single video or
	// playlist) into its component videos and creates one knowledge item
	// per video via CreateKnowledgeFromURL. Per-URL/per-video failures are
	// collected rather than aborting the batch.
	CreateKnowledgeFromYoutube(
		ctx context.Context,
		kbID string,
		urls []string,
		tagIDs []string,
		channel string,
	) (*types.YoutubeIngestResult, error)
```

- [ ] **Step 3: Verify it compiles (it won't fully — expected)**

Run: `cd /home/pc/WeKnora && go build ./internal/...`
Expected: FAIL — `*knowledgeService` (and any other `KnowledgeService` implementer/mock) does not yet implement `CreateKnowledgeFromYoutube`. This is expected; Task 7 fixes it. Note which files the compiler flags, so Task 7 covers all of them (in this codebase, only `knowledgeService` implements the full interface — stub types in tests embed `interfaces.KnowledgeService` and only override the methods they use, so they remain valid).

- [ ] **Step 4: Commit**

```bash
git add internal/types/knowledge.go internal/types/interfaces/knowledge.go
git commit -m "feat(types): add YoutubeIngestResult and CreateKnowledgeFromYoutube interface"
```

---

## Task 7: Go service method `CreateKnowledgeFromYoutube`

**Files:**
- Modify: `internal/application/service/knowledge_create.go`
- Test: `internal/application/service/knowledge_create_youtube_test.go` (new)

**Interfaces:**
- Consumes: `types.YoutubeIngestResult`/`YoutubeIngestFailure` (Task 6), `s.documentReader.Read(ctx, *types.ReadRequest) (*types.ReadResult, error)` (existing `interfaces.DocumentReader`), `s.CreateKnowledgeFromURL(...)` (existing, same file), `secutils.ValidateURLForSSRF(url string) error` (existing, already imported as `secutils`).
- Produces: `(s *knowledgeService) CreateKnowledgeFromYoutube(ctx, kbID string, urls []string, tagIDs []string, channel string) (*types.YoutubeIngestResult, error)`, satisfying the Task 6 interface.

- [ ] **Step 1: Write the failing tests**

Create `internal/application/service/knowledge_create_youtube_test.go`:

```go
package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type youtubeRepoStub struct {
	interfaces.KnowledgeRepository
	created []*types.Knowledge
}

func (r *youtubeRepoStub) CheckKnowledgeExists(
	context.Context, uint64, string, *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	return false, nil, nil
}

func (r *youtubeRepoStub) CreateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.created = append(r.created, k)
	return nil
}

func (r *youtubeRepoStub) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	return map[string][]*types.KnowledgeTag{}, nil
}

type youtubeKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *youtubeKBServiceStub) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type youtubeTaskStub struct{}

func (youtubeTaskStub) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "t"}, nil
}

// youtubeDocReaderStub answers the enumerate-engine Read call with a
// canned playlist/video/error response, keyed by URL.
type youtubeDocReaderStub struct {
	interfaces.DocumentReader
	byURL map[string]*types.ReadResult
	errAt map[string]error
}

func (d *youtubeDocReaderStub) Read(
	_ context.Context, req *types.ReadRequest,
) (*types.ReadResult, error) {
	if err, ok := d.errAt[req.URL]; ok {
		return nil, err
	}
	if result, ok := d.byURL[req.URL]; ok {
		return result, nil
	}
	return &types.ReadResult{Error: "unexpected url in test: " + req.URL}, nil
}

func newYoutubeTestService(reader interfaces.DocumentReader, repo interfaces.KnowledgeRepository) *knowledgeService {
	return &knowledgeService{
		config: &config.Config{},
		repo:   repo,
		kbService: &youtubeKBServiceStub{kb: &types.KnowledgeBase{
			ID:            "kb1",
			StorageConfig: types.StorageConfig{Provider: "local"},
		}},
		documentReader: reader,
		task:           youtubeTaskStub{},
	}
}

func TestCreateKnowledgeFromYoutubeSingleVideo(t *testing.T) {
	entries := `[{"video_id":"abc123","url":"https://www.youtube.com/watch?v=abc123","title":"My Video"}]`
	reader := &youtubeDocReaderStub{byURL: map[string]*types.ReadResult{
		"https://www.youtube.com/watch?v=abc123": {
			MarkdownContent: "ok",
			Metadata:        map[string]string{"youtube_kind": "video", "youtube_videos": entries},
		},
	}}
	repo := &youtubeRepoStub{}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(
		ctx, "kb1", []string{"https://www.youtube.com/watch?v=abc123"}, nil, "",
	)
	require.NoError(t, err)
	require.Equal(t, 1, result.SuccessCount)
	require.Empty(t, result.Failed)
	require.Len(t, repo.created, 1)
	require.Equal(t, "My Video", repo.created[0].Title)
	require.Equal(t, "url", repo.created[0].Type)
}

func TestCreateKnowledgeFromYoutubePlaylistExpandsToMultipleVideos(t *testing.T) {
	playlistURL := "https://www.youtube.com/playlist?list=PL123"
	entries := `[
		{"video_id":"vid1","url":"https://www.youtube.com/watch?v=vid1","title":"First"},
		{"video_id":"vid2","url":"https://www.youtube.com/watch?v=vid2","title":"Second"}
	]`
	reader := &youtubeDocReaderStub{byURL: map[string]*types.ReadResult{
		playlistURL: {MarkdownContent: "ok", Metadata: map[string]string{
			"youtube_kind": "playlist", "youtube_videos": entries,
		}},
	}}
	repo := &youtubeRepoStub{}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(ctx, "kb1", []string{playlistURL}, nil, "")
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Len(t, repo.created, 2)
}

func TestCreateKnowledgeFromYoutubeContinuesAfterOneFailure(t *testing.T) {
	goodURL := "https://www.youtube.com/watch?v=good"
	badURL := "https://www.youtube.com/watch?v=bad"
	goodEntries := `[{"video_id":"good","url":"https://www.youtube.com/watch?v=good","title":"Good"}]`
	reader := &youtubeDocReaderStub{
		byURL: map[string]*types.ReadResult{
			goodURL: {MarkdownContent: "ok", Metadata: map[string]string{
				"youtube_kind": "video", "youtube_videos": goodEntries,
			}},
		},
		errAt: map[string]error{badURL: assertErr("expansion failed")},
	}
	repo := &youtubeRepoStub{}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(ctx, "kb1", []string{badURL, goodURL}, nil, "")
	require.NoError(t, err)
	require.Equal(t, 1, result.SuccessCount)
	require.Len(t, result.Failed, 1)
	require.Equal(t, badURL, result.Failed[0].URL)
	require.Len(t, repo.created, 1)
}

func TestCreateKnowledgeFromYoutubeRejectsUnsafeURL(t *testing.T) {
	svc := newYoutubeTestService(&youtubeDocReaderStub{byURL: map[string]*types.ReadResult{}}, &youtubeRepoStub{})

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result, err := svc.CreateKnowledgeFromYoutube(
		ctx, "kb1", []string{"http://127.0.0.1/watch?v=abc"}, nil, "",
	)
	require.NoError(t, err)
	require.Empty(t, result.Knowledge)
	require.Len(t, result.Failed, 1)
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/pc/WeKnora && go test ./internal/application/service/... -run TestCreateKnowledgeFromYoutube -v`
Expected: FAIL to compile — `CreateKnowledgeFromYoutube` is not a method on `*knowledgeService` yet. (If any stub method signature above doesn't quite match the real `interfaces.KnowledgeRepository`/`KnowledgeBaseService`/`DocumentReader` in this codebase, the compiler error will name the mismatched method — fix the stub's signature to match, the intent stays the same.)

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/application/service/knowledge_create.go`:

```go
// youtubeEnumerateEngine tells docreader's parse_url to run the fast
// yt-dlp enumeration path instead of the default transcript/web parser.
const youtubeEnumerateEngine = "youtube-enumerate"

// youtubeVideoEntry mirrors the JSON objects docreader packs into
// ReadResult.Metadata["youtube_videos"] (see docreader/parser/youtube_parser.py).
type youtubeVideoEntry struct {
	VideoID string `json:"video_id"`
	URL     string `json:"url"`
	Title   string `json:"title"`
}

// CreateKnowledgeFromYoutube expands each input URL (single video or
// playlist) via docreader's YouTube enumerator, then creates one knowledge
// item per resulting video through the existing CreateKnowledgeFromURL —
// reusing its RBAC/dedup/SSRF/async-processing pipeline unchanged. A
// failure on one URL or one video is recorded and does not abort the rest.
func (s *knowledgeService) CreateKnowledgeFromYoutube(
	ctx context.Context, kbID string, urls []string, tagIDs []string, channel string,
) (*types.YoutubeIngestResult, error) {
	result := &types.YoutubeIngestResult{
		Knowledge: []*types.Knowledge{},
		Failed:    []types.YoutubeIngestFailure{},
	}

	for _, rawURL := range urls {
		url := strings.TrimSpace(rawURL)
		if url == "" {
			continue
		}

		if err := secutils.ValidateURLForSSRF(url); err != nil {
			logger.Warnf(ctx, "YouTube ingest: URL rejected for SSRF protection: %s: %v", url, err)
			result.Failed = append(result.Failed, types.YoutubeIngestFailure{URL: url, Error: err.Error()})
			continue
		}

		entries, err := s.expandYoutubeURL(ctx, url)
		if err != nil {
			logger.Warnf(ctx, "YouTube ingest: failed to expand %s: %v", url, err)
			result.Failed = append(result.Failed, types.YoutubeIngestFailure{URL: url, Error: err.Error()})
			continue
		}

		for _, entry := range entries {
			knowledge, err := s.CreateKnowledgeFromURL(
				ctx, kbID, entry.URL, "", "", nil, entry.Title, tagIDs, channel, nil,
			)
			if err != nil {
				logger.Warnf(ctx, "YouTube ingest: failed to create knowledge for %s: %v", entry.URL, err)
				result.Failed = append(result.Failed, types.YoutubeIngestFailure{URL: entry.URL, Error: err.Error()})
				continue
			}
			result.Knowledge = append(result.Knowledge, knowledge)
			result.SuccessCount++
		}
	}

	return result, nil
}

// expandYoutubeURL asks docreader to enumerate a YouTube URL (single video
// or playlist) into its component videos, synchronously, without fetching
// any transcript yet.
func (s *knowledgeService) expandYoutubeURL(ctx context.Context, url string) ([]youtubeVideoEntry, error) {
	if s.documentReader == nil {
		return nil, fmt.Errorf("document parsing service is not configured")
	}

	readResult, err := s.documentReader.Read(ctx, &types.ReadRequest{
		URL:          url,
		ParserEngine: youtubeEnumerateEngine,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to expand YouTube URL: %w", err)
	}
	if readResult.Error != "" {
		return nil, fmt.Errorf("failed to expand YouTube URL: %s", readResult.Error)
	}

	raw := readResult.Metadata["youtube_videos"]
	if raw == "" {
		return nil, fmt.Errorf("YouTube parser returned no videos for %s", url)
	}
	var entries []youtubeVideoEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("failed to parse YouTube video list: %w", err)
	}
	return entries, nil
}
```

Note: `types.ReadResult` in this codebase does not have a `Content` field (only `MarkdownContent`) — the test above sets `MarkdownContent`, which is what real callers use; don't add a `Content` field.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/pc/WeKnora && go test ./internal/application/service/... -run TestCreateKnowledgeFromYoutube -v`
Expected: PASS (5 tests)

Then run the full package to make sure nothing else broke:

Run: `cd /home/pc/WeKnora && go test ./internal/application/service/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/service/knowledge_create.go internal/application/service/knowledge_create_youtube_test.go
git commit -m "feat(knowledge): add CreateKnowledgeFromYoutube batch ingestion"
```

---

## Task 8: Go handler + route

**Files:**
- Modify: `internal/handler/knowledge.go`
- Modify: `internal/router/routes_knowledge.go`
- Test: `internal/handler/knowledge_youtube_test.go` (new)

**Interfaces:**
- Consumes: `h.kgService.CreateKnowledgeFromYoutube(ctx, kbID, urls, tagIDs, channel)` (Task 7), `h.validateKnowledgeBaseAccess(c)` (existing).
- Produces: `(h *KnowledgeHandler) CreateKnowledgeFromYoutube(c *gin.Context)`; route `POST /knowledge-bases/:id/knowledge/youtube`.

- [ ] **Step 1: Write the failing tests**

Create `internal/handler/knowledge_youtube_test.go`:

```go
package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type youtubeHandlerKnowledge struct {
	interfaces.KnowledgeService
	result *types.YoutubeIngestResult
	err    error
	gotURLs []string
}

func (s *youtubeHandlerKnowledge) CreateKnowledgeFromYoutube(
	_ context.Context, _ string, urls []string, _ []string, _ string,
) (*types.YoutubeIngestResult, error) {
	s.gotURLs = urls
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func TestCreateKnowledgeFromYoutubeHandlerSuccess(t *testing.T) {
	kg := &youtubeHandlerKnowledge{result: &types.YoutubeIngestResult{
		SuccessCount: 2,
		Knowledge:    []*types.Knowledge{{ID: "k1"}, {ID: "k2"}},
		Failed:       []types.YoutubeIngestFailure{},
	}}
	h := &KnowledgeHandler{
		kgService: kg,
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7, CreatorID: "user"}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/knowledge-bases/:id/knowledge/youtube", h.CreateKnowledgeFromYoutube)

	w := mutationRequest(r, http.MethodPost, "/knowledge-bases/kb/knowledge/youtube",
		`{"urls":["https://www.youtube.com/watch?v=abc123"]}`)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, []string{"https://www.youtube.com/watch?v=abc123"}, kg.gotURLs)
}

func TestCreateKnowledgeFromYoutubeHandlerRequiresURLs(t *testing.T) {
	kg := &youtubeHandlerKnowledge{result: &types.YoutubeIngestResult{}}
	h := &KnowledgeHandler{
		kgService: kg,
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7, CreatorID: "user"}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/knowledge-bases/:id/knowledge/youtube", h.CreateKnowledgeFromYoutube)

	w := mutationRequest(r, http.MethodPost, "/knowledge-bases/kb/knowledge/youtube", `{"urls":[]}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/pc/WeKnora && go test ./internal/handler/... -run TestCreateKnowledgeFromYoutubeHandler -v`
Expected: FAIL to compile — `h.CreateKnowledgeFromYoutube` does not exist.

- [ ] **Step 3: Write the minimal implementation**

Add to `internal/handler/knowledge.go`, right after `CreateKnowledgeFromURL`:

```go
// CreateKnowledgeFromYoutube godoc
// @Summary      从 YouTube 批量创建知识
// @Description  接收一批 YouTube 视频或播放列表链接，展开播放列表并逐个抓取字幕导入
// @Tags         knowledge
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "知识库ID"
// @Param        request  body      object{urls=[]string,tag_ids=[]string,channel=string}  true  "YouTube 链接请求"
// @Success      201      {object}  map[string]interface{}  "批量导入结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/youtube [post]
func (h *KnowledgeHandler) CreateKnowledgeFromYoutube(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from YouTube")

	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = types.WithExecutionTenant(c.Request.Context(), effectiveTenantID)

	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	var req struct {
		URLs    []string `json:"urls" binding:"required,min=1"`
		TagIDs  []string `json:"tag_ids"`
		Channel string   `json:"channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse YouTube request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Creating knowledge from %d YouTube URL(s), knowledge base ID: %s",
		len(req.URLs), secutils.SanitizeForLog(kbID))

	result, err := h.kgService.CreateKnowledgeFromYoutube(ctx, kbID, req.URLs, req.TagIDs, req.Channel)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "YouTube ingest complete: %d succeeded, %d failed",
		result.SuccessCount, len(result.Failed))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    result,
	})
}
```

Register the route in `internal/router/routes_knowledge.go`, right after the existing `/url` line:

```go
		kb.POST("/url", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateKnowledgeFromURL)
		kb.POST("/youtube", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateKnowledgeFromYoutube)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/pc/WeKnora && go test ./internal/handler/... -run TestCreateKnowledgeFromYoutubeHandler -v`
Expected: PASS (2 tests)

Then the full backend build + test suite:

Run: `cd /home/pc/WeKnora && go build ./... && go test ./internal/...`
Expected: PASS, no compile errors anywhere (this also confirms Task 6's interface addition didn't break any other `KnowledgeService` implementer).

- [ ] **Step 5: Commit**

```bash
git add internal/handler/knowledge.go internal/handler/knowledge_youtube_test.go internal/router/routes_knowledge.go
git commit -m "feat(api): add POST /knowledge-bases/:id/knowledge/youtube endpoint"
```

---

## Task 9: Frontend — API client + textarea parsing utility

**Files:**
- Modify: `frontend/src/api/knowledge-base/index.ts`
- Create: `frontend/src/views/knowledge/utils/youtubeImport.ts`
- Test: `frontend/src/views/knowledge/utils/youtubeImport.test.ts`

**Interfaces:**
- Produces:
  - `createKnowledgeFromYoutube(kbId: string, data: {urls: string[], tag_ids?: string[]}): Promise<any>`
  - `parseYoutubeUrlsInput(text: string): {urls: string[], invalidLines: string[]}`

- [ ] **Step 1: Write the failing test**

Create `frontend/src/views/knowledge/utils/youtubeImport.test.ts`:

```typescript
import assert from 'node:assert/strict'
import test from 'node:test'

import { parseYoutubeUrlsInput } from './youtubeImport'

test('splits one URL per line, trims whitespace, drops blank lines', () => {
  const result = parseYoutubeUrlsInput(
    '  https://www.youtube.com/watch?v=abc123  \n\nhttps://youtu.be/def456\n',
  )
  assert.deepEqual(result.urls, [
    'https://www.youtube.com/watch?v=abc123',
    'https://youtu.be/def456',
  ])
  assert.deepEqual(result.invalidLines, [])
})

test('flags lines that are not valid URLs', () => {
  const result = parseYoutubeUrlsInput('not a url\nhttps://www.youtube.com/watch?v=abc123')
  assert.deepEqual(result.invalidLines, ['not a url'])
  assert.deepEqual(result.urls, ['https://www.youtube.com/watch?v=abc123'])
})

test('flags non-YouTube URLs as invalid', () => {
  const result = parseYoutubeUrlsInput('https://example.com/watch?v=abc123')
  assert.deepEqual(result.invalidLines, ['https://example.com/watch?v=abc123'])
  assert.deepEqual(result.urls, [])
})

test('empty input yields no urls and no invalid lines', () => {
  const result = parseYoutubeUrlsInput('   \n  ')
  assert.deepEqual(result.urls, [])
  assert.deepEqual(result.invalidLines, [])
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/pc/WeKnora/frontend && npm test -- --test-name-pattern="youtube" 2>&1 || npx tsx --test src/views/knowledge/utils/youtubeImport.test.ts`
Expected: FAIL — module `./youtubeImport` does not exist.

- [ ] **Step 3: Write the minimal implementation**

Create `frontend/src/views/knowledge/utils/youtubeImport.ts`:

```typescript
const YOUTUBE_HOSTS = new Set(['youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be'])

function isYoutubeUrl(line: string): boolean {
  try {
    const parsed = new URL(line)
    return YOUTUBE_HOSTS.has(parsed.hostname.toLowerCase())
  } catch {
    return false
  }
}

export function parseYoutubeUrlsInput(text: string): { urls: string[]; invalidLines: string[] } {
  const urls: string[] = []
  const invalidLines: string[] = []

  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    if (isYoutubeUrl(line)) {
      urls.push(line)
    } else {
      invalidLines.push(line)
    }
  }

  return { urls, invalidLines }
}
```

Add to `frontend/src/api/knowledge-base/index.ts`, right after `createKnowledgeFromURL`:

```typescript
// 批量从 YouTube 创建知识（自动展开播放列表）
export function createKnowledgeFromYoutube(
  kbId: string,
  data: { urls: string[]; tag_ids?: string[] },
) {
  return post(`/api/v1/knowledge-bases/${kbId}/knowledge/youtube`, data);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/pc/WeKnora/frontend && npx tsx --test src/views/knowledge/utils/youtubeImport.test.ts`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/knowledge-base/index.ts frontend/src/views/knowledge/utils/youtubeImport.ts frontend/src/views/knowledge/utils/youtubeImport.test.ts
git commit -m "feat(frontend): add YouTube ingest API client and URL-list parsing"
```

---

## Task 10: Frontend — dialog UI, parent wiring, i18n

**Files:**
- Modify: `frontend/src/views/knowledge/components/KbUploadSourceDropdown.vue`
- Modify: `frontend/src/views/knowledge/KnowledgeBase.vue`
- Modify: `frontend/src/i18n/locales/en-US.ts`, `zh-CN.ts`, `ru-RU.ts`, `ko-KR.ts`, `ja-JP.ts`

**Interfaces:**
- Consumes: `parseYoutubeUrlsInput` and `createKnowledgeFromYoutube` from Task 9.
- Produces: `KbUploadSourceDropdown` emits a new `youtube: [urls: string[]]` event; `KnowledgeBase.vue` handles it by calling the API and showing a toast.

This task has no automated test (Vue SFC rendering has no test harness in this repo — `tsx --test` covers plain TS only, per Task 9). Verify manually per Step 5.

- [ ] **Step 1: Add i18n keys to all 5 locale files**

In `frontend/src/i18n/locales/en-US.ts`, right after the existing `typeURL: 'URL',` line (line 606), add:

```typescript
    importYoutube: 'Import from YouTube',
    importYoutubeTitle: 'Import from YouTube',
    youtubeUrlsLabel: 'YouTube URLs (one per line)',
    youtubeUrlsPlaceholder: 'https://www.youtube.com/watch?v=...\nhttps://www.youtube.com/playlist?list=...',
    youtubeUrlsTip: 'Paste video or playlist links, one per line. Playlists are expanded into their individual videos automatically.',
    youtubeUrlsRequired: 'Please enter at least one YouTube URL',
    youtubeInvalidLines: 'Ignored {count} line(s) that were not valid YouTube URLs',
    youtubeImportSubmitting: 'Importing from YouTube…',
    youtubeImportResult: '{success} imported, {failed} failed',
    youtubeImportAllFailed: 'All videos failed to import',
```

Mirror the same 9 keys, translated, into the equivalent spot (right after each locale's own `typeURL:` line) in `zh-CN.ts`, `ru-RU.ts`, `ko-KR.ts`, `ja-JP.ts`. Use each file's existing translation style/tone for `importURL`/`urlLabel` as the reference for phrasing. Example for `zh-CN.ts`:

```typescript
    importYoutube: '从 YouTube 导入',
    importYoutubeTitle: '从 YouTube 导入',
    youtubeUrlsLabel: 'YouTube 链接（每行一个）',
    youtubeUrlsPlaceholder: 'https://www.youtube.com/watch?v=...\nhttps://www.youtube.com/playlist?list=...',
    youtubeUrlsTip: '粘贴视频或播放列表链接，每行一个。播放列表会自动展开为其中的每个视频。',
    youtubeUrlsRequired: '请至少输入一个 YouTube 链接',
    youtubeInvalidLines: '已忽略 {count} 行无效的 YouTube 链接',
    youtubeImportSubmitting: '正在从 YouTube 导入…',
    youtubeImportResult: '成功 {success} 个，失败 {failed} 个',
    youtubeImportAllFailed: '所有视频均导入失败',
```

(Translate similarly for `ru-RU`, `ko-KR`, `ja-JP` — keep the `{count}`/`{success}`/`{failed}` interpolation tokens verbatim.)

- [ ] **Step 2: Add the menu item + dialog to `KbUploadSourceDropdown.vue`**

In the `<script setup>` block, add state and the submit handler:

```typescript
import { parseYoutubeUrlsInput } from '../utils/youtubeImport'
```

(add next to the existing `filterUploadFiles` import)

```typescript
const youtubeDialogVisible = ref(false)
const youtubeInputValue = ref('')
```

(No separate loading flag: the dialog closes immediately on confirm and the parent shows a success/failure toast when the batch call resolves — the same fire-and-forget pattern the existing single-URL `handleUrlDialogConfirm`/`executeUrlImport` already uses in this codebase, rather than the spec's literal "loading state on the button", to stay consistent with how URL import already behaves here.)

(add next to `urlDialogVisible`/`urlInputValue`)

Add `'youtube'` to the dropdown options, right after the `importURL` entry:

```typescript
    {
      content: t('knowledgeBase.importYoutube'),
      value: 'importYoutube',
      prefixIcon: () => h(TIcon, { name: 'logo-youtube', size: '16px' }),
    },
```

Add the case in `handleActionSelect`, next to the `importURL` case:

```typescript
    case 'importYoutube':
      youtubeInputValue.value = ''
      youtubeDialogVisible.value = true
      break
```

Add the emit and confirm/cancel handlers, next to `handleUrlDialogConfirm`/`handleUrlDialogCancel`:

```typescript
const handleYoutubeDialogConfirm = () => {
  const { urls, invalidLines } = parseYoutubeUrlsInput(youtubeInputValue.value)
  if (urls.length === 0) {
    MessagePlugin.warning(t('knowledgeBase.youtubeUrlsRequired'))
    return
  }
  if (invalidLines.length > 0) {
    MessagePlugin.warning(t('knowledgeBase.youtubeInvalidLines', { count: invalidLines.length }))
  }
  youtubeDialogVisible.value = false
  youtubeInputValue.value = ''
  emit('youtube', urls)
}

const handleYoutubeDialogCancel = () => {
  youtubeDialogVisible.value = false
  youtubeInputValue.value = ''
}
```

Update `defineEmits`:

```typescript
const emit = defineEmits<{
  files: [files: File[]]
  url: [url: string]
  youtube: [urls: string[]]
  manual: []
}>()
```

In the `<template>`, add a second `t-dialog` right after the existing URL-import dialog's closing `</t-dialog>`:

```html
    <t-dialog
      v-model:visible="youtubeDialogVisible"
      :header="t('knowledgeBase.importYoutubeTitle')"
      :confirm-btn="{ content: t('common.confirm'), theme: 'primary' }"
      :cancel-btn="{ content: t('common.cancel') }"
      width="520px"
      @confirm="handleYoutubeDialogConfirm"
      @cancel="handleYoutubeDialogCancel"
    >
      <div class="url-import-form">
        <div class="url-input-label">{{ t('knowledgeBase.youtubeUrlsLabel') }}</div>
        <t-textarea
          v-model="youtubeInputValue"
          :placeholder="t('knowledgeBase.youtubeUrlsPlaceholder')"
          :autosize="{ minRows: 4, maxRows: 10 }"
        />
        <div class="url-input-tip">{{ t('knowledgeBase.youtubeUrlsTip') }}</div>
      </div>
    </t-dialog>
```

(`t-textarea` is TDesign's textarea component; if a project ESLint rule requires explicit imports for TDesign components used in templates, none is needed here since the project appears to auto-register TDesign components globally — same as `t-input`/`t-dialog` used unregistered above. If the build fails on an unregistered component, add `Textarea as TTextarea` to a script import and register it the same way other TDesign components are imported in this file.)

- [ ] **Step 3: Wire the new event in `KnowledgeBase.vue`**

Add the import, next to `createKnowledgeFromURL`:

```typescript
import { createKnowledgeFromYoutube } from '@/api/knowledge-base'
```

(adjust the import path/style to match however `createKnowledgeFromURL` is already imported in this file)

Add a handler function, right after `handleUploadSourceUrl`:

```typescript
const handleUploadSourceYoutube = async (urls: string[]) => {
  if (!ensureDocumentKbReady()) return;
  const targetKbId = kbId.value;
  if (!targetKbId) {
    MessagePlugin.error(t('error.missingKbId'));
    return;
  }
  try {
    const responseData: any = await createKnowledgeFromYoutube(targetKbId, {
      urls,
      tag_ids: selectedTagIds.value.length > 0 ? [...selectedTagIds.value] : undefined,
    });
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: targetKbId },
    }));
    const data = responseData?.data ?? responseData;
    const successCount = data?.success_count ?? 0;
    const failedCount = data?.failed?.length ?? 0;
    if (successCount === 0 && failedCount > 0) {
      MessagePlugin.error(t('knowledgeBase.youtubeImportAllFailed'));
    } else {
      MessagePlugin.success(t('knowledgeBase.youtubeImportResult', { success: successCount, failed: failedCount }));
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.urlImportFailed'));
  }
};
```

Wire the emit at the `KbUploadSourceDropdown` usage site (the same tag that has `@url="handleUploadSourceUrl"`, around line 2441):

```html
                      @url="handleUploadSourceUrl" @youtube="handleUploadSourceYoutube" @manual="handleManualCreate" />
```

- [ ] **Step 4: Type-check the frontend**

Run: `cd /home/pc/WeKnora/frontend && npm run type-check` (or `npx vue-tsc --noEmit` if there is no `type-check` script — check `package.json`'s `scripts` first)
Expected: PASS, no new type errors.

- [ ] **Step 5: Manual verification in the browser**

Start the frontend dev server (check `frontend/package.json` for the exact script, typically `npm run dev`) alongside the backend and `docreader` (via `docker-compose up` or the project's existing dev workflow). Then:
1. Open a knowledge base, click "Add Document" → "Import from YouTube".
2. Paste one real single-video URL and one real playlist URL (2–3 videos), click confirm.
3. Confirm the textarea clears immediately and a toast shows success/failure counts.
4. Confirm one knowledge item per video appears in the KB list, and after processing completes, opening one shows transcript text (no timestamps) starting with `Source: <url>`.
5. Paste a URL with no captions (or a private/deleted video) alongside a good one; confirm the good one still succeeds and the toast reflects the partial failure.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/views/knowledge/components/KbUploadSourceDropdown.vue frontend/src/views/knowledge/KnowledgeBase.vue frontend/src/i18n/locales/en-US.ts frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/ru-RU.ts frontend/src/i18n/locales/ko-KR.ts frontend/src/i18n/locales/ja-JP.ts
git commit -m "feat(frontend): add Import from YouTube dialog to knowledge base upload menu"
```

---

## Task 11: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full backend test suite**

Run: `cd /home/pc/WeKnora && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 2: Run the full docreader test suite**

Run: `cd /home/pc/WeKnora/docreader && python -m unittest discover -s tests -v`
Expected: PASS

- [ ] **Step 3: Run the full frontend test suite and type-check**

Run: `cd /home/pc/WeKnora/frontend && npm test && npm run type-check`
Expected: PASS

- [ ] **Step 4: Rebuild the docreader image so yt-dlp/youtube-transcript-api are installed**

Run: `cd /home/pc/WeKnora && docker compose build docreader`
Expected: build succeeds; `uv sync --locked --no-dev` picks up the new dependencies from `docreader/uv.lock`.

- [ ] **Step 5: Full-stack manual smoke test**

Run: `docker compose up -d` (or the project's standard dev-up command), then repeat the browser steps from Task 10 Step 5 end to end against the real containers. Confirm:
- A single video URL produces one knowledge item with a real transcript.
- A playlist URL produces N knowledge items, one per video.
- A video with no captions fails gracefully (visible error on that one knowledge item) without blocking the rest of the batch.
- Pasting a plain (non-batch) YouTube URL into the existing generic "Import from URL" dialog also now yields transcript content instead of a scraped/empty web page (a side effect of Task 5's host-based dispatch in `parse_url` — call this out to the user as expected behavior, not a bug).

- [ ] **Step 6: Final commit (if any fixups were needed)**

```bash
git add -A
git commit -m "fix: address end-to-end verification findings for YouTube ingestion"
```

(Skip this step if no fixups were needed.)
