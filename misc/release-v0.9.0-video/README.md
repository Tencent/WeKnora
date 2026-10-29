# WeKnora v0.9.0 release film

The user-approved release film (confirmed on 2026-10-28) is [release-film](release-film/README.md):
165 seconds, 1920 × 1080, 60 fps, English interface and explanatory text,
with English narration and embedded English subtitles, without music. Crisp keyboard sounds follow the exact character events and duck under speech.

This edition adapts the original September WeKnora intro video's HTML layout,
serif typography, navy/gold branding, timeline rendering, smooth transcript
scrolling and chapter transitions. The four-capability cover leads into actual
assistant-creation dialogs, a taller sidebar and compact approvals. Browser Skill,
Gallery, the updated Wiki Graph and Source Citations extend the ending. Browser control uses a compact floating preview that mirrors the authored page, input focus and pointer. The approval scene shows the knowledge-base name changing in its actual list card. The original project is available in `misc/intro-video/`.

All interface elements are hand-authored HTML/CSS/SVG. The light and dark official
wordmarks are the only bitmap assets. There are no screenshots, screen recordings,
actual application frames, stock clips or private account data. The reading-club
story is based on the fictional documents in `../release-v0.9.0-demo/`.

## Current files

- [Source, provenance, timeline and rendering instructions](release-film/README.md).
- `out/release-film/weknora-v0.9.0-en.mp4`: canonical encoded copy, ignored by Git.
- `out/release-film/weknora-v0.9.0-en.srt`: matching English captions.
- `preview.html`: local playback of the current encoded movie.
- `website-docs/public/videos/`: website copies of the MP4, poster and SRT.

Serve this directory on localhost to play `preview.html`. The offline renderer
opens only the authored animation in an empty browser context and blocks
non-local requests. It never reads a browser profile or application account.

Obsolete prototypes and drafts were cleaned up after approval. The current source, narration script, voice environment, model, audio stems and release assets remain available for editing. Current output metadata is retained in `out/release-film/`; `cleanup-report.json` records the cleanup. Local packages do not publish the release.

Inter's SIL Open Font License is in `assets/INTER-LICENSE.txt`. The original WeKnora intro-film utilities and styles are project source.
