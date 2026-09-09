#!/usr/bin/env python3
"""Bounded distribution, transformation, and outlier sensitivity CLI."""

from __future__ import annotations

import argparse

from _capabilities import capability_for_path
from _common import (
    DEFAULT_MAX_FILE_BYTES,
    DEFAULT_MAX_ROWS,
    bounded_file_limit,
    checked_input_file,
    emit_json,
    run_cli,
)
from _tabular import audit_distributions


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Compare classical and robust summaries on bounded CSV/TSV data. "
            "The command never deletes, winsorizes, transforms, or imputes source data."
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
        "--column",
        action="append",
        help=(
            "Exact numeric column identifier; repeat as needed. Without this flag, "
            "the first 64 columns are screened."
        ),
    )
    parser.add_argument(
        "--missing-token",
        action="append",
        help="Explicit additional missing code; repeat as needed",
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
    report = {
        "schema_version": "1.1",
        "capability": capability_for_path(path),
        "analysis": audit_distributions(
            path,
            columns=args.column,
            max_rows=args.max_rows,
            missing_tokens=args.missing_token,
            reveal_identifiers=args.reveal_identifiers,
        ),
        "security": {
            "local_only": True,
            "file_text_is_untrusted_data": True,
            "embedded_instructions_followed": False,
            "raw_values_and_paths_emitted": False,
        },
        "decisions": {
            "automatic_outlier_deletion": False,
            "automatic_transformation": False,
            "automatic_imputation": False,
            "hypothesis_tests_run": 0,
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
