#!/usr/bin/env python3
"""Bounded aggregate schema/profile CLI for local CSV and TSV files."""

from __future__ import annotations

import argparse

from _capabilities import capability_for_path
from _common import (
    DEFAULT_MAX_FILE_BYTES,
    DEFAULT_MAX_ROWS,
    CliError,
    bounded_file_limit,
    checked_input_file,
    emit_json,
    run_cli,
)
from _tabular import profile_table


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Profile bounded local CSV/TSV rows with aggregate statistics. "
            "Raw cell values and full paths are never emitted."
        )
    )
    parser.add_argument("input", help="Local .csv or .tsv path inside --root")
    parser.add_argument(
        "--root",
        default=".",
        help="Existing local directory that bounds all input/output paths",
    )
    parser.add_argument(
        "--max-bytes",
        type=int,
        default=DEFAULT_MAX_FILE_BYTES,
        help=f"Maximum input bytes (hard ceiling: {512 * 1024 * 1024})",
    )
    parser.add_argument(
        "--max-rows",
        type=int,
        default=DEFAULT_MAX_ROWS,
        help="Maximum rows to scan (hard ceiling: 1000000)",
    )
    parser.add_argument(
        "--missing-token",
        action="append",
        help=(
            "Explicit additional missing code; repeat as needed. Empty/whitespace "
            "is always missing. Values such as NA are not assumed automatically."
        ),
    )
    parser.add_argument(
        "--reveal-identifiers",
        action="store_true",
        help="Emit sanitized column identifiers; values remain redacted",
    )
    parser.add_argument("--output", help="Optional local .json output path")
    parser.add_argument(
        "--force",
        action="store_true",
        help="Allow replacement of an existing regular output file",
    )
    return parser


def _main() -> None:
    args = build_parser().parse_args()
    max_bytes = bounded_file_limit(args.max_bytes)
    path = checked_input_file(
        args.input,
        root=args.root,
        suffixes={".csv", ".tsv"},
        max_bytes=max_bytes,
    )
    capability = capability_for_path(path)
    if capability["tier"] != "automated_core":
        raise CliError("the selected file has no core tabular capability")
    report = {
        "schema_version": "1.1",
        "capability": capability,
        "analysis": profile_table(
            path,
            max_rows=args.max_rows,
            missing_tokens=args.missing_token,
            reveal_identifiers=args.reveal_identifiers,
        ),
        "security": {
            "local_only": True,
            "file_text_is_untrusted_data": True,
            "embedded_instructions_followed": False,
            "raw_values_and_full_paths_emitted": False,
        },
    }
    emit_json(
        report,
        output=args.output,
        root=args.root,
        force=args.force,
    )


def main() -> int:
    return run_cli(_main)


if __name__ == "__main__":
    raise SystemExit(main())
