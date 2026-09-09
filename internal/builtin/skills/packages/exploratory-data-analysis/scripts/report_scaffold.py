#!/usr/bin/env python3
"""Generate a rigorous, redacted EDA Markdown report scaffold."""

from __future__ import annotations

import argparse
from pathlib import Path

from _common import (
    DEFAULT_MAX_FILE_BYTES,
    CliError,
    bounded_file_limit,
    checked_input_file,
    emit_markdown,
    markdown_scalar,
    run_cli,
)
from capability_manifest import inspect_manifest


TEMPLATE = Path(__file__).resolve().parents[1] / "assets" / "report_template.md"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Create a local Markdown EDA scaffold that records design, missingness, "
            "censoring, leakage, sensitivity, multiplicity, and reproducibility."
        )
    )
    parser.add_argument(
        "--input",
        help="Optional local data file used only for a redacted capability manifest",
    )
    parser.add_argument(
        "--root",
        default=".",
        help="Existing local directory that bounds all input/output paths",
    )
    parser.add_argument(
        "--max-bytes",
        type=int,
        default=DEFAULT_MAX_FILE_BYTES,
        help=f"Maximum optional input bytes (hard ceiling: {512 * 1024 * 1024})",
    )
    parser.add_argument(
        "--analysis-date",
        default="not supplied",
        help="Explicit date label (for example 2026-07-23); not generated implicitly",
    )
    parser.add_argument(
        "--reveal-identifiers",
        action="store_true",
        help="Include only a sanitized input basename; never include a full path",
    )
    parser.add_argument(
        "--output",
        required=True,
        help="Required local .md output path inside --root",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Allow replacement of an existing regular output file",
    )
    return parser


def render_scaffold(
    *,
    analysis_date: str,
    manifest: dict | None,
) -> str:
    """Fill only trusted scalar placeholders in the bundled template."""

    try:
        template = TEMPLATE.read_text(encoding="utf-8")
    except OSError as exc:
        raise CliError("the bundled report template is unavailable") from exc
    if manifest is None:
        replacements = {
            "{ANALYSIS_DATE}": markdown_scalar(analysis_date),
            "{FILE_ID}": "not supplied",
            "{BASENAME}": "not supplied",
            "{FORMAT}": "not supplied",
            "{CAPABILITY_TIER}": "not supplied",
            "{FILE_SIZE_BYTES}": "not supplied",
            "{SIGNATURE_CHECKED}": "not supplied",
        }
    else:
        file_info = manifest["file"]
        capability = manifest["capability"]
        replacements = {
            "{ANALYSIS_DATE}": markdown_scalar(analysis_date),
            "{FILE_ID}": markdown_scalar(str(file_info["file_id"])),
            "{BASENAME}": markdown_scalar(str(file_info["basename"])),
            "{FORMAT}": markdown_scalar(str(capability["format"])),
            "{CAPABILITY_TIER}": markdown_scalar(str(capability["tier"])),
            "{FILE_SIZE_BYTES}": str(file_info["size_bytes"]),
            "{SIGNATURE_CHECKED}": str(
                manifest["inspection"]["format_signature_checked"]
            ).lower(),
        }
    rendered = template
    for placeholder, value in replacements.items():
        rendered = rendered.replace(placeholder, value)
    return rendered


def _main() -> None:
    args = build_parser().parse_args()
    max_bytes = bounded_file_limit(args.max_bytes)
    manifest = None
    if args.input is not None:
        path = checked_input_file(
            args.input,
            root=args.root,
            max_bytes=max_bytes,
        )
        manifest = inspect_manifest(
            path,
            max_bytes=max_bytes,
            include_sha256=False,
            reveal_identifiers=args.reveal_identifiers,
        )
    report = render_scaffold(
        analysis_date=args.analysis_date,
        manifest=manifest,
    )
    emit_markdown(
        report,
        output=args.output,
        root=args.root,
        force=args.force,
    )


def main() -> int:
    return run_cli(_main)


if __name__ == "__main__":
    raise SystemExit(main())
