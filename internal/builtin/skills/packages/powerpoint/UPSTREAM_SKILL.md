---
name: slides
description: Create and edit presentation slide decks (`.pptx`) with PptxGenJS, bundled layout helpers, and render/validation utilities. Use when tasks involve building a new PowerPoint deck, recreating slides from screenshots/PDFs/reference decks, modifying slide content while preserving editable output, adding charts/diagrams/visuals, or diagnosing layout issues such as overflow, overlaps, and font substitution.
---

# Slides

## Overview

Use PptxGenJS for slide authoring. Do not use `python-pptx` for deck generation unless the task is inspection-only; keep editable output in JavaScript and deliver both the `.pptx` and the source `.js`.

Keep work in a task-local directory. Only copy final artifacts to the requested destination after rendering and validation pass.

## Bundled Resources

- `assets/pptxgenjs_helpers/`: Copy this folder into the deck workspace and import it locally instead of reimplementing helper logic.
- `scripts/render_slides.py`: Rasterize a `.pptx` or `.pdf` to per-slide PNGs.
- `scripts/slides_test.py`: Detect content that overflows the slide canvas.
- `scripts/create_montage.py`: Build a contact-sheet style montage of rendered slides.
- `scripts/detect_font.py`: Report missing or substituted fonts as LibreOffice resolves them.
- `scripts/ensure_raster_image.py`: Convert SVG/EMF/HEIC/PDF-like assets into PNGs for quick inspection.
- `references/pptxgenjs-helpers.md`: Load only when you need API details or dependency notes.

## Workflow

1. Inspect the request and determine whether you are creating a new deck, recreating an existing deck, or editing one.
2. Set the slide size up front. Default to 16:9 (`LAYOUT_WIDE`) unless the source material clearly uses another aspect ratio.
3. Copy `assets/pptxgenjs_helpers/` into the working directory and import the helpers from there.
4. Build the deck in JavaScript with an explicit theme font, stable spacing, and editable PowerPoint-native elements when practical.
5. Run the bundled scripts from this skill directory or copy the needed ones into the task workspace. Render the result with `render_slides.py`, review the PNGs, and fix layout issues before delivery.
6. Run `slides_test.py` for overflow checks when slide edges are tight or the deck is dense.
7. Deliver the `.pptx`, the authoring `.js`, and any generated assets that are required to rebuild the deck.

## Authoring Rules

- Set theme fonts explicitly. Do not rely on PowerPoint defaults if typography matters.
- Use `autoFontSize`, `calcTextBox`, and related helpers to size text boxes; do not use PptxGenJS `fit` or `autoFit`.
- Use bullet options, not literal `•` characters.
- Use `imageSizingCrop` or `imageSizingContain` instead of PptxGenJS built-in image sizing.
- Use `latexToSvgDataUri()` for equations and `codeToRuns()` for syntax-highlighted code blocks.
- Prefer native PowerPoint charts for simple bar/line/pie/histogram style visuals so reviewers can edit them later.
- For charts or diagrams that PptxGenJS cannot express well, render SVG externally and place the SVG in the slide.
- Include both `warnIfSlideHasOverlaps(slide, pptx)` and `warnIfSlideElementsOutOfBounds(slide, pptx)` in the submitted JavaScript whenever you generate or substantially edit slides.
- Fix all unintentional overlap and out-of-bounds warnings before delivering. If an overlap is intentional, leave a short code comment near the relevant element.

## Recreate Or Edit Existing Slides

- Render the source deck or reference PDF first so you can compare slide geometry visually.
- Match the original aspect ratio before rebuilding layout.
- Preserve editability where possible: text should stay text, and simple charts should stay native charts.
- If a reference slide uses raster artwork, use `ensure_raster_image.py` to generate debug PNGs from vector or odd image formats before placing them.

## Validation Commands

Examples below assume you copied the needed scripts into the working directory. If not, invoke the same script paths relative to this skill folder.

```bash
# Render slides to PNGs for review
python3 scripts/render_slides.py deck.pptx --output_dir rendered

# Build a montage for quick scanning
python3 scripts/create_montage.py --input_dir rendered --output_file montage.png

# Check for overflow beyond the original slide canvas
python3 scripts/slides_test.py deck.pptx

# Detect missing or substituted fonts
python3 scripts/detect_font.py deck.pptx --json
```

Load `references/pptxgenjs-helpers.md` if you need the helper API summary or dependency details.
