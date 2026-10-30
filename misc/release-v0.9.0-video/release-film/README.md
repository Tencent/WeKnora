# WeKnora v0.9.0 — release film

165 seconds · 1920 × 1080 · 60 fps · H.264 / AAC · English narration + embedded subtitles · no music.
Approved by the user on 2026-10-28. The published-release step remains separate.

## Original reference

The September intro film provides the visual reference. Its original HTML,
rendering and audio sources are available in `misc/intro-video/`.

The source history includes `7e82ee33d` (intro-video source), `c23b8a1d1`
(first-frame, motion and 60 fps polish), and `2020c1922` (the later soundtrack).
`reference.css` and `foundation.js` adapt that project's typography, UI styles,
HTML stream renderer, easing functions and icons. The new film preserves the
serif editorial headings, paper backgrounds, navy/gold title cards, restrained
camera movement and separate chapter exit/entry. The assistant uses the current
`AssistantPanel.vue`, `AssistantCreateDialog.vue`, `AssistantAccessPicker.vue`,
`CollaborationTasks.vue`, `ToolApprovalCard.vue` and `ShellExecResult.vue` as UI references.
Creation prompt assembly follows `assistant.prompts` in `frontend/src/i18n/locales/en-US.ts`.
The appended scenes reference the local-browser guide, `ImageGallery.vue`,
`GalleryViewer.vue`, the current `WikiBrowser.vue` grouped graph and Markdown
renderer, `ChatCitationFloat.vue`, `ChatReferenceSourceView.vue` and `PdfSourceViewer.vue`.

The old soundtrack is not reused. The current mix combines a stock synthetic English narrator with synchronized typing, without background music.

## Story and provenance

| Time | Scene |
| --- | --- |
| 0–22 s | Four-capability cover; highlight the new entry beside the logo; open the right sidebar; type a v0.9.0 question; search and read the product docs; stream the answer without opening or highlighting a citation |
| 22–47 s | Open Agents → Create with assistant; type in the native request dialog; carry the expanded request into the Assistant; discover knowledge, model, MCP and Skill resources; review and create the agent |
| 47–65 s | Delegate agenda, sign-up and preparation checks to Atlas, Iris and Nova; show progress, completion and synchronization; combine their findings into a sourced briefing |
| 65–87 s | Open Automations → New automation; type in the assistant-creation dialog; carry the task, time zone and access instructions into the Assistant; review, save, run now and show the brief |
| 87–101 s | Choose Ask first; request a rename; inspect a compact before/after approval card; approve the operation; animate the name change in the knowledge-base card list |
| 101–113 s | Type a deliverable request; use the Skill and background sandbox command; open the Markdown run sheet |
| 113–127 s | Enable the connected local browser; type a form-filling request; fill title, time, capacity and notes; keep the event unpublished for review; mirror the exact authored page, focus and pointer in a compact floating preview; pause from that window |
| 127–137 s | Browse hand-drawn document images in the native gallery; type a search; filter and open a poster with its description, OCR and source |
| 137–150 s | Explore the current grouped Wiki graph; focus Reading club; open the page in the reader with formatted paragraphs, table and related links |
| 150–161 s | Ask about registration; hover a citation to preview its excerpt; open the original document at the highlighted supporting passage |
| 161–165 s | Brand close and GitHub address |

Version answers are based on the current release baseline's
`website-docs/07-releases/v0.9.0.md`, Assistant, automation and permissions guides.
The reading-club dates, agenda, registration rules and preparation tasks come
from the three fictional documents in `../../release-v0.9.0-demo/`.
Reading Club Coordinator, Atlas/Iris/Nova, Event workspace MCP, brief-writer and
their animated interactions are illustrative. The `events.example` form, vector posters, charts and Wiki nodes are authored fictional examples, consistent with the reading-club story. They are not a captured live run
or a claim about latency. Real screenshot test evidence remains separate.

Assistant chapters retain the taller sidebar on the right and explanatory text on the left. Creation starts from the actual page entry and dialog. Later chapters use full-width product layouts, adapting the original film’s browser and knowledge scenes. Original
product greens are limited to native controls and the Assistant glyph. No
Better Knowledge chapter, floating starter button, private identity, screenshot,
recording or old movie clip is used. The only bitmaps are the official wordmark
variants recovered with the original project. Product glyphs are SVG paths;
Inter is distributed under `../assets/INTER-LICENSE.txt`.

## Rendering and checks

Use Node 24+, Python 3.12 with NumPy, Chrome and FFmpeg. The local narrator uses MLX on Apple Silicon; its pinned Python dependencies are in `requirements-voice.txt`. The Mac executable paths are
configured in `render.mjs` and `check.mjs`.

From `misc/release-v0.9.0-video`, activate the retained voice environment so the audio commands use their installed dependencies:

```bash
source .venv-voice/bin/activate
```

To recreate that environment, use Python 3.12 and install `release-film/requirements-voice.txt`. Then run the local source server separately:

```bash
python3 -m http.server 3110 --bind 127.0.0.1 --directory release-film
```

Then generate stills and the exact character/audio timeline, check layout and
render the film:

```bash
python3 release-film/narration.py
node release-film/render.mjs stills
node release-film/check.mjs
node release-film/render.mjs video
python3 release-film/sfx.py
python3 release-film/mix.py
ffmpeg -v error -y -i out/release-film/video-silent.mp4 -i out/release-film/soundtrack.wav -map 0:v -map 1:a -c:v copy -c:a aac -b:a 192k -ar 48000 -t 165 -movflags +faststart out/release-film/weknora-v0.9.0-narrated-en.mp4
python3 release-film/validate.py
cp out/release-film/weknora-v0.9.0-narrated-en.mp4 out/release-film/weknora-v0.9.0-en.mp4
```

The renderer uses an empty browser context, blocks external requests and renders
the authored page. It does not access a real app or browser profile. The UI and
typing audio share 715 character events, aligned to 60 fps. `check.mjs` verifies
each character's arrival frame and checks layout at 660 sampled times, including
input/footer collisions, approval field boundaries, subtitle safe areas, browser/preview state equality and the knowledge-base rename. The eleven inputs also have
partial, ready-to-send and submitted-state checks. Browser form fields and gallery
search retain their text after entry; chat and creation inputs clear after submission. Click locations are measured
from their actual target elements, as in the original film.

`sfx.py` synthesizes crisp, short key contacts with reduced bass thump and a 38 ms tail. The typing stem has no samples outside keypress tails. `narration.py` generates 28 conversational English lines locally with the stock `af_heart` voice from [Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M), using the [MLX conversion](https://huggingface.co/mlx-community/Kokoro-82M-bf16) and [MLX Audio](https://github.com/Blaizzy/mlx-audio). The model is Apache-2.0; no individual voice is cloned. The generated audio uses model revision `a71e4d38b236d968966a2002c4c895dbd12b1c3c`. The script uses fictional public-facing material only. Model word durations drive 34 subtitle cues, written to `narration.js` and the matching SRT. Speed stays close to normal (0.98–1.073); pauses fit the existing scene timing.

`mix.py` normalizes speech and ducks typing beneath it. The 48 kHz stereo master measures -18.02 LUFS integrated, -5.01 dBTP and 5.6 LU loudness range before AAC encoding. `mix-checks.json` records levels and checks that each spoken line fits its scene slot. There is no music, chime, ambient bed or transition sound. Narration and typing stems remain separate for future edits.

English subtitles are embedded in the reserved space below the product windows and also delivered as a separate SRT. The poster is the complete branded first frame. `narration-plan.json` is the editable narration script; the generated captions should be regenerated from it instead of edited independently.
