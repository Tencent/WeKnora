---
name: pdf
description: Create, extract, merge and render PDF documents with embedded fonts and output verification.
version: 2026.09.5
---

# PDF documents

Use pdfplumber or pypdf for text/table extraction and page operations; render with pypdfium2 or Poppler for layout inspection. Text extraction does not imply an OCR capability. If a scanned PDF has no text layer, explain the missing OCR dependency before choosing another route.

For creation use ReportLab with an embedded TrueType font and deliberate page geometry. For Chinese/mixed text call `register_cjk_font()` from `scripts/weknora_fonts.py`; the image provides WenQuanYi Zen Hei as a TrueType collection. Use the returned font for all mixed text and Paragraph styles. Do not use ReportLab's default Helvetica for Chinese, non-embedded CID fallback, or insert spaces between Latin letters to repair layout. Avoid unsupported emoji. Text shaping, font metrics and embedding must agree.

Use clear typographic hierarchy, headings, generous but consistent margins, restrained accent colors, tables/charts where they explain the content, and sensible pagination. Use ReportLab Platypus flowables for prose instead of manually positioning each line. If the user also requests PPTX or DOCX, create that editable source first and export the same source to PDF for consistency.

After export, reopen the PDF, confirm page count and extract expected Chinese and Latin strings. Check embedded fonts with `pdffonts`. Render representative pages with `pdftoppm -png` and inspect them when image viewing is available. Look for clipped lines, missing glyphs and excessive word spacing. Without an image-view tool state that visual review could not be completed. Keep only the final deliverables in the artifact directory.

## WeKnora runtime

Run scripts with `shell_exec` and `skill_name: "pdf"`; this selects the locked Python environment and sets `WEKNORA_SKILL_DIR`. Resolve bundled resources from that variable, never from a guessed working directory. Node dependencies resolve through the Skill's `node_modules` via `NODE_PATH`.

Use a task-specific temporary directory for scripts, previews and intermediates. Put only requested deliverables in the session's artifact/output directory. Respect user templates, language and scope. Read additional references only when needed; `UPSTREAM_SKILL.md` records provenance and is not a second mandatory entry point.

The image preinstalls `requirements.lock`. During installation on other images, use a local `.venv` and `uv pip install --python .venv/bin/python --require-hashes -r requirements.lock`. Do not mutate the environment during each task or install unrelated optional tools. Report a missing runtime capability explicitly. Run `scripts/weknora_smoke.py --report` as the installation check.
