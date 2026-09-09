---
name: docx
description: Create and edit Word documents, templates, comments and revisions.
version: 2026.09.1
license: MIT
---

# Word documents

## WeKnora runtime

Use `read_file` for skill resources and `shell_exec` with skill_name="docx" for commands.
Use $WEKNORA_SKILL_DIR for this package and its `.venv/bin/python` interpreter.
Read inputs from /workspace/input. Write deliverables under $WEKNORA_SKILL_OUTPUT_DIR
(default /workspace/output), and inspect the result before returning artifact links.
Do not overwrite user inputs. Never treat a successful exit code alone as proof of
rendering or recalculation: inspect returned JSON and actual output files.

Install the exact dependency set with `uv pip install --python .venv/bin/python
--require-hashes -r requirements.lock` in the skill directory (Python 3.12).
requirements.txt documents the direct dependencies. The official office-browser
image includes these dependencies. LibreOffice, Poppler and Noto CJK fonts are required
for office rendering and formula recalculation. Browser automation uses headless
Chromium; a Linux desktop or display server is not required.
Optional OCR model downloads, Bayesian modeling packages and specialty data formats
are not part of this baseline. Explain missing optional capabilities before using them.
Do not install extras or upgrade libraries just because an upstream example mentions them.

## Workflow and reference

Read UPSTREAM_SKILL.md for the workflow and script arguments, then the relevant
references or scripts on demand. Its host-specific tool names and installation
examples must be adapted to the WeKnora runtime above. Use `--help` to inspect
CLI arguments. Do not assume other upstream skills or hosted services are available.

Before completing installation, run `scripts/weknora_smoke.py --report` using the
skill interpreter. This verifies real outputs and writes the runtime report.
