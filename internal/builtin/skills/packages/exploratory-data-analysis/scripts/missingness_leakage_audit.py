#!/usr/bin/env python3
"""Bounded missingness, group structure, and split leakage audit for CSV/TSV."""

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
from _tabular import audit_missingness_and_leakage


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Audit bounded CSV/TSV missingness and common group/entity/time split "
            "leakage. Identifiers and cell values are tokenized or omitted."
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
            "is always missing."
        ),
    )
    parser.add_argument(
        "--group-column",
        help="Exact column identifier for biological/experimental grouping",
    )
    parser.add_argument(
        "--entity-column",
        help="Exact subject/sample/entity identifier used to detect split overlap",
    )
    parser.add_argument(
        "--split-column",
        help="Exact train/validation/test or analysis split column identifier",
    )
    parser.add_argument(
        "--time-column",
        help="Exact ISO-8601 time column used for split interval overlap checks",
    )
    parser.add_argument(
        "--reveal-identifiers",
        action="store_true",
        help="Emit sanitized column identifiers; values and group/entity IDs stay tokenized",
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
        "analysis": audit_missingness_and_leakage(
            path,
            max_rows=args.max_rows,
            missing_tokens=args.missing_token,
            group_column=args.group_column,
            entity_column=args.entity_column,
            split_column=args.split_column,
            time_column=args.time_column,
            reveal_identifiers=args.reveal_identifiers,
        ),
        "security": {
            "local_only": True,
            "file_text_is_untrusted_data": True,
            "embedded_instructions_followed": False,
            "raw_values_paths_and_entity_ids_emitted": False,
        },
        "interpretation": {
            "potential_overlap_is_not_proof_of_model_leakage": True,
            "no_overlap_found_is_not_proof_of_independence": True,
            "domain_design_review_required": True,
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
