---
name: docx
description: Create, edit and render Word documents with python-docx and typography-aware review.
version: 2026.09.5
---

# Word documents

Use python-docx for `.docx` creation and editing. Preserve an existing document's styles and structure unless redesign is requested. Inspect source paragraphs, tables, sections and relationships before changing them.

Use named heading/body styles, consistent spacing, restrained colors, deliberate page breaks, header/footer and page numbering as appropriate. For Chinese/mixed text set both the Latin font and OOXML `w:eastAsia` font to `Noto Sans CJK SC`; explicitly configure sizes and paragraph spacing. Do not imitate layout using repeated spaces or empty paragraphs. Prefer native tables and avoid excessive borders.

Render with `python "$WEKNORA_SKILL_DIR/scripts/render_docx.py" input.docx --output_dir /tmp/task/previews`. Inspect page images when the Agent provides image viewing; otherwise state that visual review was unavailable. Check tables crossing pages, orphaned headings, clipping and missing glyphs. Use LibreOffice with a unique `-env:UserInstallation=file:///tmp/task/lo` for PDF conversion, and validate PDF text independently with `pdftotext`.

The runtime includes python-docx, lxml, pdf2image, LibreOffice and Poppler. This entry does not promise a tracked-changes engine: python-docx does not provide comprehensive revision editing. For revision/comment operations inspect the OOXML and preserve relationships, or explain the unsupported operation rather than flattening an existing document silently.

## WeKnora runtime

Run scripts with `shell_exec` and `skill_name: "docx"`; this selects the locked Python environment and sets `WEKNORA_SKILL_DIR`. Resolve bundled resources from that variable, never from a guessed working directory. Node dependencies resolve through the Skill's `node_modules` via `NODE_PATH`.

Use a task-specific temporary directory for scripts, previews and intermediates. Put only requested deliverables in the session's artifact/output directory. Respect user templates, language and scope. Read additional references only when needed; `UPSTREAM_SKILL.md` records provenance and is not a second mandatory entry point.

The image preinstalls `requirements.lock`. During installation on other images, use a local `.venv` and `uv pip install --python .venv/bin/python --require-hashes -r requirements.lock`. Do not mutate the environment during each task or install unrelated optional tools. Report a missing runtime capability explicitly. Run `scripts/weknora_smoke.py --report` as the installation check.
