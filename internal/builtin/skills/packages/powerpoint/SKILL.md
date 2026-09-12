---
name: powerpoint
description: Create and edit editable PowerPoint presentations with PptxGenJS, measured layouts, native charts and rendered checks.
version: 2026.09.5
---

# PowerPoint

Use for `.pptx` creation and editing. For a web presentation explicitly requested by the user, prefer the separately installed frontend-slides skill. Use PPT Master only when its richer SVG/template workflow is requested.

## Authoring workflow

1. Extract the audience, purpose and key conclusions from the request. Choose a concise narrative and a consistent theme before coding. Prefer conclusion-led titles, comparison diagrams, timelines and charts over repeated title-and-bullet slides.
2. Write a Node CommonJS script using `require('pptxgenjs')`. Set `pptx.layout = 'LAYOUT_WIDE'`, explicit font sizes, margins and theme fonts. For Chinese/mixed content use `Noto Sans CJK SC` for the theme and text; confirm it with `fc-match`. Avoid unsupported emoji and use drawn shapes or verified glyphs instead.
3. Use native editable text, shapes, tables and charts. Import measured layout helpers with `require(process.env.WEKNORA_SKILL_DIR + '/assets/pptxgenjs_helpers')`; see `references/pptxgenjs-helpers.md` only if needed. `scripts/weknora_example.cjs` demonstrates a title card and native comparison chart; adapt its design to the user's material rather than copying every slide.
4. Measure text with `calcTextBox`/`autoFontSize` when useful. Match the measured and actual font. Reflow or split dense content as appropriate; there is no fixed bullet-count rejection gate. Do not reduce body text until it becomes unreadable. Use rich text runs only in the API's documented shape; never stringify an object into a slide.
5. Export with `await pptx.writeFile({fileName: outputPath})`. Helpers such as `warnIfSlideHasOverlaps` and `warnIfSlideElementsOutOfBounds` can locate issues; review intentional overlaps rather than blindly rejecting them.
6. Render with `python "$WEKNORA_SKILL_DIR/scripts/render_slides.py" deck.pptx --output_dir /tmp/task/previews`. Inspect the rendered images if an image-view tool is available. Check clipping, hierarchy, chart labels, CJK glyphs and color contrast. Extract PDF text with `pdftotext -layout` as a separate content check; it is not a substitute for visual inspection. If the Agent cannot view images, disclose that limitation instead of claiming visual review.
7. If PDF is requested, convert the same final PPTX using LibreOffice with a task-specific profile: `soffice -env:UserInstallation=file:///tmp/task/lo --headless --convert-to pdf --outdir /tmp/task deck.pptx`. Verify the resulting file and move final PPTX/PDF to the artifact directory.

## Dependencies

PptxGenJS and the bundled layout helpers use the checked-in `package-lock.json`. On non-preinstalled images install once with `npm ci --omit=dev` in the Skill directory. Python dependencies support rendering and inspection, including python-pptx for inspecting/editing existing files. LibreOffice, Poppler and fonts are system prerequisites. Advanced helper integrations not in this lock (for example MathJax) are optional and must not be invoked without their dependency.

## WeKnora runtime

Run scripts with `shell_exec` and `skill_name: "powerpoint"`; this selects the locked Python environment and sets `WEKNORA_SKILL_DIR`. Resolve bundled resources from that variable, never from a guessed working directory. Node dependencies resolve through the Skill's `node_modules` via `NODE_PATH`.

Use a task-specific temporary directory for scripts, previews and intermediates. Put only requested deliverables in the session's artifact/output directory. Respect user templates, language and scope. Read additional references only when needed; `UPSTREAM_SKILL.md` records provenance and is not a second mandatory entry point.

The image preinstalls `requirements.lock`. During installation on other images, use a local `.venv` and `uv pip install --python .venv/bin/python --require-hashes -r requirements.lock`. Do not mutate the environment during each task or install unrelated optional tools. Report a missing runtime capability explicitly. Run `scripts/weknora_smoke.py --report` as the installation check.
