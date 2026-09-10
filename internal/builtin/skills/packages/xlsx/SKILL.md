---
name: xlsx
description: Create, edit and analyze spreadsheets with openpyxl, pandas, formulas and verified recalculation.
version: 2026.09.5
---

# Spreadsheets

Use openpyxl for `.xlsx` editing and formatting, pandas for CSV/TSV analysis, and native openpyxl charts for editable chart output. Preserve existing formulas, styles and references. Read only relevant `references/examples/openpyxl/` examples when needed.

Prefer formulas for derived values; do not hardcode calculated results. Keep formulas readable with helper cells and correct absolute/relative references. Guard against division by zero, invalid ranges and circular references. Avoid functions unsupported by the target Excel/LibreOffice version.

Create a clear hierarchy with a title, units, restrained header fills, appropriate date/currency/percentage formats, deliberate column widths and section spacing. Do not outline every cell. Distinguish editable inputs from formulas, and put source URLs in comments or a source column when external facts are used.

openpyxl does not calculate formulas. Recalculate with `python "$WEKNORA_SKILL_DIR/scripts/xlsx_recalc.py" input.xlsx --out /tmp/task/recalculated.xlsx`, then open the result both with and without `data_only=True`: confirm formulas survive, cached values exist and there are no spreadsheet errors. Inspect the recalculation report. LibreOffice may alter unsupported Excel features; retain the original and disclose material changes.

For layout review, convert relevant sheets to PDF with a task-specific LibreOffice profile and rasterize with `pdftoppm`. Inspect images if image viewing is available; otherwise report the gap. Set print areas and scaling to avoid many empty pages. Keep source data and verify expected row counts, totals and formulas after modifications.

## WeKnora runtime

Run scripts with `shell_exec` and `skill_name: "xlsx"`; this selects the locked Python environment and sets `WEKNORA_SKILL_DIR`. Resolve bundled resources from that variable, never from a guessed working directory. Node dependencies resolve through the Skill's `node_modules` via `NODE_PATH`.

Use a task-specific temporary directory for scripts, previews and intermediates. Put only requested deliverables in the session's artifact/output directory. Respect user templates, language and scope. Read additional references only when needed; `UPSTREAM_SKILL.md` records provenance and is not a second mandatory entry point.

The image preinstalls `requirements.lock`. During installation on other images, use a local `.venv` and `uv pip install --python .venv/bin/python --require-hashes -r requirements.lock`. Do not mutate the environment during each task or install unrelated optional tools. Report a missing runtime capability explicitly. Run `scripts/weknora_smoke.py --report` as the installation check.
