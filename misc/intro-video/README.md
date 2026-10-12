# WeKnora intro video

The source for the product intro video (about 1:52, 1920×1080 at 30 fps, English on-screen text, no audio). The whole video is one HTML page, `index.html`, which draws any moment from its timestamp. `render.mjs` steps through the timeline with headless Chromium and pipes the frames into ffmpeg.

## Render

Requires Node 18+, ffmpeg and a Chromium build for Playwright.

```bash
cd misc/intro-video
npm install
npx playwright-core install chromium   # skip if CHROME_PATH points at a Chromium build
npm run render                          # writes weknora-intro.mp4, takes a few minutes
```

To check a few moments without rendering the whole video:

```bash
npm run stills -- 9.5 22 57 81          # PNGs in stills/
```

To preview in a browser, open `index.html?t=22` for one frame, or `index.html?play` to play from the start (`?play=40` starts at 40 s). Open it from the repository checkout, because the brand icons are loaded from `website-docs/homepage/public/docs/_home/brands/`.

For a README upload under GitHub's 10 MB attachment limit, re-encode:

```bash
ffmpeg -i weknora-intro.mp4 -c:v libx264 -preset slow -crf 24 -pix_fmt yuv420p -movflags +faststart weknora-intro-lite.mp4
```

## Structure

| Scene | Time (s) | Content |
|---|---|---|
| `open` | 0–6.6 | Scattered sources and files: "Your team's knowledge lives in a hundred places." |
| `logo` | 6.2–12.2 | Logo and tagline |
| `pillars` | 11.8–17 | RAG / Agent / Wiki cards |
| `rag` | 16.6–31 | Chat with retrieval steps, streamed answer, citation popover |
| `agent` | 30.6–44.4 | Memory, knowledge search, MCP tool call, SLA table |
| `skill` | 44–60.4 | Toolbox skills, then sandbox terminal and the generated .docx |
| `browser` | 60–74.6 | BrowserSkill driving Chrome: release lookup and form fill |
| `wiki` | 74.2–89 | Documents become a wiki tree and page, then a knowledge graph |
| `eco` | 88.6–100.4 | Data sources, channels and model providers |
| `outro` | 100–112 | Docker Compose quick start, logo and links |

Scene boundaries are in the `S` object near the bottom of `index.html`. Each scene reads its local time and positions its elements from it, so to retime a scene, edit its offsets inside `render()`. The total length is `DUR`.

The product UI is rebuilt in HTML rather than screenshotted, so the text stays sharp when the camera zooms in. UI labels follow `frontend/src/i18n/locales/en-US.ts`. The conversation content (Nova-KB-POC, the Q3 ticket numbers, the `ticketing.query_issues` MCP tool, the humanoid-robot report) is sample data.

## When to update

- **Version:** the release shown in the browser scene (`v0.8.2`) and the text typed into the form.
- **Counts:** the model provider count in the ecosystem scene ("27 providers").
- **Lists:** the data sources and channels, when integrations are added or removed.

`assets/logo-light.png` and `assets/logo-dark.png` are generated from `docs/images/logo.png` by `make-logos.py` (needs Pillow).
