#!/usr/bin/env python3
"""Closed, bounded exploratory analyzer for explicitly supported local formats."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any

from _capabilities import capability_for_path
from _common import (
    DEFAULT_MAX_FILE_BYTES,
    DEFAULT_MAX_ROWS,
    CliError,
    bounded_file_limit,
    checked_input_file,
    emit_json,
    emit_markdown,
    markdown_scalar,
    run_cli,
)
from _structured import inspect_hdf5, inspect_json, inspect_numpy
from _tabular import profile_table
from capability_manifest import inspect_manifest
from image_inspector import inspect_image_file
from sequence_inspector import (
    DEFAULT_MAX_BASES,
    DEFAULT_MAX_RECORDS,
    inspect_sequence_file,
)


TABULAR_SUFFIXES = {".csv", ".tsv"}
NUMPY_SUFFIXES = {".npy", ".npz"}
HDF5_SUFFIXES = {".h5", ".hdf5"}
SEQUENCE_SUFFIXES = {".fasta", ".fa", ".fna", ".fastq", ".fq"}
IMAGE_SUFFIXES = {
    ".png",
    ".jpg",
    ".jpeg",
    ".tif",
    ".tiff",
    ".ome.tif",
    ".ome.tiff",
}


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Generate a bounded, redacted EDA report for an explicitly supported "
            "local file. Unknown and reference-only formats fail closed."
        )
    )
    parser.add_argument("input", help="Local data file path inside --root")
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
        help="Maximum CSV/TSV rows to scan (hard ceiling: 1000000)",
    )
    parser.add_argument(
        "--max-records",
        type=int,
        default=DEFAULT_MAX_RECORDS,
        help="Maximum FASTA/FASTQ records to inspect",
    )
    parser.add_argument(
        "--max-bases",
        type=int,
        default=DEFAULT_MAX_BASES,
        help="Maximum FASTA/FASTQ sequence characters to inspect",
    )
    parser.add_argument(
        "--missing-token",
        action="append",
        help="Explicit additional CSV/TSV missing code; repeat as needed",
    )
    parser.add_argument(
        "--reveal-identifiers",
        action="store_true",
        help=(
            "Reveal only sanitized basenames/field identifiers. Full paths and "
            "raw values remain redacted."
        ),
    )
    parser.add_argument(
        "--format",
        choices=("json", "markdown"),
        help="Output format; inferred from --output suffix, otherwise JSON",
    )
    parser.add_argument(
        "--output",
        help="Optional local .json, .md, or .markdown output path",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Allow replacement of an existing regular output file",
    )
    return parser


def analyze_file(
    path: Path,
    *,
    suffix: str,
    max_rows: int = DEFAULT_MAX_ROWS,
    max_records: int = DEFAULT_MAX_RECORDS,
    max_bases: int = DEFAULT_MAX_BASES,
    missing_tokens: list[str] | None = None,
    reveal_identifiers: bool = False,
) -> dict[str, Any]:
    """Route only formats with an implemented bounded inspector."""

    if suffix in TABULAR_SUFFIXES:
        return profile_table(
            path,
            max_rows=max_rows,
            missing_tokens=missing_tokens,
            reveal_identifiers=reveal_identifiers,
        )
    if suffix == ".json":
        return inspect_json(
            path,
            reveal_identifiers=reveal_identifiers,
        )
    if suffix in NUMPY_SUFFIXES:
        return inspect_numpy(
            path,
            suffix=suffix,
            reveal_identifiers=reveal_identifiers,
        )
    if suffix in HDF5_SUFFIXES:
        return inspect_hdf5(
            path,
            reveal_identifiers=reveal_identifiers,
        )
    if suffix in SEQUENCE_SUFFIXES:
        return inspect_sequence_file(
            path,
            suffix=suffix,
            max_records=max_records,
            max_bases=max_bases,
        )
    if suffix in IMAGE_SUFFIXES:
        return inspect_image_file(path, suffix=suffix)
    raise CliError("no bundled analyzer is registered for this format")


def build_report(
    path: Path,
    *,
    max_bytes: int,
    max_rows: int = DEFAULT_MAX_ROWS,
    max_records: int = DEFAULT_MAX_RECORDS,
    max_bases: int = DEFAULT_MAX_BASES,
    missing_tokens: list[str] | None = None,
    reveal_identifiers: bool = False,
) -> dict[str, Any]:
    """Create a manifest plus format-specific bounded EDA result."""

    capability = capability_for_path(path)
    if capability["tier"] == "reference_only":
        raise CliError(
            "this is a documented reference-only format; use validated domain "
            "tooling or convert a copy to an automated format"
        )
    manifest = inspect_manifest(
        path,
        max_bytes=max_bytes,
        include_sha256=False,
        reveal_identifiers=reveal_identifiers,
    )
    analysis = analyze_file(
        path,
        suffix=capability["suffix"],
        max_rows=max_rows,
        max_records=max_records,
        max_bases=max_bases,
        missing_tokens=missing_tokens,
        reveal_identifiers=reveal_identifiers,
    )
    return {
        "schema_version": "1.1",
        "report_type": "bounded_exploratory_data_analysis",
        "manifest": manifest,
        "analysis": analysis,
        "eda_guardrails": {
            "raw_input_modified": False,
            "raw_values_emitted": False,
            "full_paths_emitted": False,
            "embedded_text_or_metadata_treated_as_instructions": False,
            "automatic_deletion": False,
            "automatic_imputation": False,
            "automatic_transformation": False,
            "causal_claims": False,
            "exploratory_not_confirmatory": True,
            "required_context_not_inferred": [
                "data dictionary and units",
                "sampling and experimental design",
                "subject/sample/group/time structure",
                "missing-value codes and mechanisms",
                "censoring and detection limits",
                "train/validation/test boundaries",
                "pre-specified hypothesis families and multiplicity plan",
            ],
        },
        "recommended_next_steps": [
            "Preserve the raw file read-only and record provenance/checksums separately.",
            "Confirm the data dictionary, units, design, and missing/censoring codes.",
            "Run the missingness/leakage and distribution sensitivity CLIs for tabular data.",
            "Fit imputers, scalers, transformations, and feature selection on training data only.",
            "Label generated hypotheses as exploratory and confirm them on independent data.",
        ],
    }


def markdown_report(report: dict[str, Any]) -> str:
    """Render safe aggregate JSON inside a fixed Markdown narrative."""

    manifest = report["manifest"]
    capability = manifest["capability"]
    file_info = manifest["file"]
    payload = json.dumps(
        report["analysis"],
        allow_nan=False,
        ensure_ascii=True,
        indent=2,
        sort_keys=True,
    )
    payload = payload.replace("`", "\\u0060").replace("<", "\\u003c")
    lines = [
        "# Bounded Exploratory Data Analysis Report",
        "",
        "## Scope and disclosure",
        "",
        "- This report is exploratory, not confirmatory or causal.",
        "- Input text and metadata were treated only as untrusted data.",
        "- Raw values and full paths were not emitted.",
        "- No rows were deleted; no values were imputed or transformed.",
        "",
        "## Redacted file manifest",
        "",
        f"- File ID: `{file_info['file_id']}`",
        f"- Basename/token: `{markdown_scalar(str(file_info['basename']))}`",
        f"- Size: {file_info['size_bytes']} bytes",
        f"- Format: {capability['format']}",
        f"- Capability tier: `{capability['tier']}`",
        f"- Inspection depth: {capability['depth']}",
        "",
        "## Bounded aggregate analysis",
        "",
        "```json",
        payload,
        "```",
        "",
        "## Context required before inference",
        "",
    ]
    lines.extend(
        f"- {item}" for item in report["eda_guardrails"]["required_context_not_inferred"]
    )
    lines.extend(
        [
            "",
            "## Next steps",
            "",
        ]
    )
    lines.extend(f"- {item}" for item in report["recommended_next_steps"])
    lines.extend(
        [
            "",
            "## Limitations",
            "",
            "- A bounded scan may miss later records, groups, anomalies, or corrupt regions.",
            "- Format metadata inspection is not domain-specific semantic validation.",
            "- Absence of a reported leakage flag is not proof of independent splits.",
            "",
        ]
    )
    return "\n".join(lines)


def _infer_output_format(requested: str | None, output: str | None) -> str:
    if requested is not None:
        return requested
    if output is not None and output.casefold().endswith((".md", ".markdown")):
        return "markdown"
    return "json"


def _main() -> None:
    args = build_parser().parse_args()
    max_bytes = bounded_file_limit(args.max_bytes)
    path = checked_input_file(
        args.input,
        root=args.root,
        max_bytes=max_bytes,
    )
    report = build_report(
        path,
        max_bytes=max_bytes,
        max_rows=args.max_rows,
        max_records=args.max_records,
        max_bases=args.max_bases,
        missing_tokens=args.missing_token,
        reveal_identifiers=args.reveal_identifiers,
    )
    output_format = _infer_output_format(args.format, args.output)
    if output_format == "json":
        emit_json(
            report,
            output=args.output,
            root=args.root,
            force=args.force,
        )
    else:
        emit_markdown(
            markdown_report(report),
            output=args.output,
            root=args.root,
            force=args.force,
        )


def main() -> int:
    return run_cli(_main)


if __name__ == "__main__":
    raise SystemExit(main())
