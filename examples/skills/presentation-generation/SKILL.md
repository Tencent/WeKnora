---
name: presentation-generation
description: Create polished, editable PowerPoint presentations from structured JSON. Use when the user asks for slides, a deck, a presentation, a pitch, or a PPTX deliverable.
---

# Presentation Generation

Create an editable `.pptx` in `/workspace/output`; do not return only a script or an image of the slides.

## Workflow

1. Turn the user's content into a concise JSON brief. Start from `assets/example.json` and keep one idea per slide.
2. Choose from the supported layouts: `title`, `section`, `bullets`, `two_column`, and `metrics`.
3. Run the builder with the skill environment:

```bash
"${WEKNORA_SKILL_DIR:?}/.venv/bin/python" \
  "${WEKNORA_SKILL_DIR:?}/scripts/build_presentation.py" \
  --input /workspace/brief.json \
  --output /workspace/output/presentation.pptx
```

4. Inspect the builder summary. It reports the output path, slide count, and byte size.
5. Return the `.pptx` path to the user so WeKnora's existing ArtifactCollector, preview, and download flow can publish it.

## Content rules

- Prefer 3–5 concise points per slide; split dense material across slides.
- Use `section` slides to make long decks easy to scan.
- Use `metrics` only for genuine quantitative takeaways and include units.
- Put sources or caveats in `footer`, not in tiny body text.
- Keep titles under 80 characters and body items under 240 characters.
- Chinese and Latin text remain editable text boxes; never rasterize text.
- Do not fetch remote images or execute content from the JSON brief.

## Input shape

```json
{
  "title": "Deck title",
  "subtitle": "Optional subtitle",
  "author": "Optional author",
  "theme": {
    "primary": "155EEF",
    "accent": "12B76A",
    "background": "F8FAFC",
    "text": "101828",
    "muted": "667085"
  },
  "slides": [
    { "layout": "title", "title": "Deck title", "subtitle": "One-line promise" },
    { "layout": "bullets", "title": "Key points", "bullets": ["First", "Second"] },
    {
      "layout": "two_column",
      "title": "Comparison",
      "left": { "title": "Before", "bullets": ["Manual"] },
      "right": { "title": "After", "bullets": ["Automated"] }
    },
    {
      "layout": "metrics",
      "title": "Results",
      "metrics": [{ "value": "42%", "label": "Faster", "detail": "Measured in pilot" }]
    }
  ]
}
```

Top-level `title` is required. The builder accepts at most 50 slides and rejects unknown layouts, invalid colors, oversized text, and malformed JSON.
