---
name: presentation-builder
description: "Create a PPTX slide deck from structured JSON. Use when the user asks to turn a JSON outline into presentation slides."
---

# Presentation Builder

Build an editable 16:9 PPTX with a cover and bullet, table, or local-image
slides. This is a WeKnora product runtime skill, not an IDE skill. It uses
`python-pptx==1.0.2`, native shapes and text, and no network or external templates.

## Runtime

Read `read_file(path="skill://presentation-builder/SKILL.md")` first. Read the
synthetic demo with
`read_file(path="skill://presentation-builder/assets/sample.json")`.
`skill://` addresses are tool resources, not shell paths.

For user content, preserve attachments in `/workspace/input`. Write a new JSON
outline with `write_sandbox_file` at `/workspace/deck.json`, then run:

```json
{
  "skill_name": "presentation-builder",
  "command": "python \"$WEKNORA_SKILL_DIR/scripts/build_presentation.py\" --input /workspace/deck.json --output presentation.pptx"
}
```

Pass that object to `shell_exec`. Its working directory remains `/workspace`;
`skill_name` selects the installed Python environment and sets
`WEKNORA_SKILL_DIR`. To run the bundled demo, use:

```json
{
  "skill_name": "presentation-builder",
  "command": "python \"$WEKNORA_SKILL_DIR/scripts/build_presentation.py\" --input \"$WEKNORA_SKILL_DIR/assets/sample.json\" --output runtime-demo.pptx"
}
```

For small inputs, `--input -` reads UTF-8 JSON from the `shell_exec` `stdin`
field (runtime limit: 65,536 bytes). Do not embed JSON in shell commands.
No credentials or runtime environment variables are required by the builder.
The selected Python environment must already contain `requirements.txt`;
the script never installs dependencies or downloads assets.

## Input

JSON files must be absolute paths under `/workspace` or this bundle's `assets`.
The CLI accepts only `--input` and `--output`; there is no output-root override.
Input is at most 256 KiB. Unknown fields, duplicate keys, non-finite numbers,
wrong types, control characters, and multiline text are rejected.

| Object | Fields and limits |
| --- | --- |
| Deck | `title`: 1-100 characters; optional `subtitle`: 0-180; `slides`: 1-30 objects |
| Every slide | `type`: `bullets`, `table`, or `image`; `title`: 1-80 characters |
| Bullets | `bullets`: 1-6 nonempty strings, each at most 120 characters |
| Table | `headers`: 1-5 nonempty strings, each at most 40 characters; `rows`: 1-8 arrays matching the header count; cells are strings of 0-60 characters |
| Image | `path`: absolute path under `/workspace/input`, or `assets/<file>` relative to this skill; optional `caption`: 0-180 characters |

Images must be local PNG or JPEG files: at most 4 MiB each, 16 MiB total,
4,096 pixels per dimension, and 12 million pixels each. Absolute paths inside
the skill's own `assets` also work. URLs, other formats, traversal, symlinks,
and non-regular files are rejected. Images retain their aspect ratio.
Use only user-supplied or original assets; the demo needs no images.

## Output And Failures

`--output` must match `[A-Za-z0-9][A-Za-z0-9_-]{0,79}.pptx`, for example
`runtime-demo.pptx`. Output is always `/workspace/output/<basename>.pptx`.
There is one cover plus one slide per `slides` entry, with no automatic
pagination. Maximum output size is 32 MiB.

Success exits 0 and prints one JSON object with `path`, `slide_count`, and
`size_bytes`. Return the reported artifact path to the user. Do not claim an
artifact exists after a failed command.

Invalid JSON, bounds, paths, images, or an existing output name exit 2.
Dependency or filesystem failures exit 1. Errors go to stderr; failed builds
do not publish a partial PPTX. Choose a new basename to retry an existing name.
Shorten or split content that exceeds the limits. Missing fonts may change
line wrapping in the viewer; this skill does not render slide previews.

## Bundle Checks

From the bundle directory, in a Linux environment with the pinned requirements:

```bash
python -B -m unittest discover -s tests -v
```

Tests use temporary directories injected through `PresentationBuilder`'s
constructor; they do not relax the CLI's workspace policy.
