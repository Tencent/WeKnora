"""A WeKnora parser plugin, written with the Python SDK: Jupyter notebooks
(.ipynb) become Markdown. Markdown cells are kept as they are, code cells
become fenced blocks, text outputs follow their cell and PNG/JPEG charts are
handed to WeKnora as images."""

from __future__ import annotations

import base64
import json

from weknora_plugin import ErrorCode, ParsedImage, ParseOutput, Plugin, PluginError

plugin = Plugin("weknora-examples.notebooks", "1.0.0")

IMAGE_TYPES = {"image/png": "png", "image/jpeg": "jpg"}


def _text(value) -> str:
    """Notebook strings are either a string or a list of lines."""
    if isinstance(value, list):
        return "".join(value)
    return value or ""


def _fence(body: str, lang: str = "") -> str:
    # A fence longer than any backtick run inside, so code cannot close it.
    longest, run = 0, 0
    for ch in body:
        run = run + 1 if ch == "`" else 0
        longest = max(longest, run)
    ticks = "`" * max(3, longest + 1)
    return f"{ticks}{lang}\n{body.rstrip()}\n{ticks}"


def convert(nb: dict, include_outputs: bool = True, max_output_chars: int = 2000) -> ParseOutput:
    lang = ((nb.get("metadata") or {}).get("kernelspec") or {}).get("language") or (
        (nb.get("metadata") or {}).get("language_info") or {}
    ).get("name", "")
    parts, images = [], []
    for i, cell in enumerate(nb.get("cells") or []):
        kind, source = cell.get("cell_type"), _text(cell.get("source")).strip()
        if kind == "markdown" and source:
            parts.append(source)
        elif kind == "code":
            if source:
                parts.append(_fence(source, lang))
            if include_outputs:
                parts.extend(_outputs(i, cell.get("outputs") or [], images, max_output_chars))
        elif kind == "raw" and source:
            parts.append(_fence(source))
    title = next((p.splitlines()[0][2:].strip() for p in parts if p.startswith("# ")), "")
    meta = {"cells": str(len(nb.get("cells") or [])), "language": lang}
    if title:
        meta["title"] = title
    return ParseOutput(markdown="\n\n".join(parts) + "\n", images=images, metadata=meta)


def _outputs(cell: int, outputs: list, images: list, limit: int) -> list:
    parts = []
    for j, out in enumerate(outputs):
        kind = out.get("output_type")
        if kind == "stream":
            text = _text(out.get("text"))
        elif kind == "error":
            text = f"{out.get('ename', 'Error')}: {out.get('evalue', '')}"
        else:
            data = out.get("data") or {}
            image = next((m for m in IMAGE_TYPES if m in data), None)
            if image:
                ref = f"images/cell{cell}-{j}.{IMAGE_TYPES[image]}"
                try:
                    images.append(ParsedImage(original_ref=ref, data=base64.b64decode(_text(data[image])), mime_type=image))
                    parts.append(f"![Output of cell {cell + 1}]({ref})")
                except ValueError:
                    pass
                continue
            if "text/markdown" in data:
                parts.append(_text(data["text/markdown"]).strip())
                continue
            text = _text(data.get("text/plain"))
        text = text.strip()
        if text:
            if limit and len(text) > limit:
                text = text[:limit] + "\n…"
            parts.append(_fence(text, "text"))
    return parts


@plugin.parser("ipynb")
def parse(call, doc):
    try:
        nb = json.loads(doc.content.decode("utf-8-sig"))
    except (UnicodeDecodeError, ValueError) as e:
        raise PluginError(ErrorCode.BAD_REQUEST, f"{doc.file_name or 'file'} is not a Jupyter notebook: {e}") from None
    if not isinstance(nb, dict) or "cells" not in nb:
        raise PluginError(ErrorCode.BAD_REQUEST, f"{doc.file_name or 'file'} is not a Jupyter notebook (no cells)")
    cfg = call.tenant
    return convert(
        nb,
        include_outputs=cfg.get("include_outputs", True) is not False,
        max_output_chars=int(cfg.get("max_output_chars", 2000) or 0),
    )


if __name__ == "__main__":
    plugin.serve()
