#!/usr/bin/env python3
"""Offline integrity and consistency checks for the accepted Topic 3 evidence."""

from __future__ import annotations

import hashlib
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
TOPIC = ROOT / "codex" / "topic3"
MANIFEST = TOPIC / "EVIDENCE_MANIFEST_20260908.json"
CORE_SNAPSHOT = TOPIC / "CORE_SOURCE_SNAPSHOT_20260908.json"
SECRET_PATTERN = re.compile(r"sk-[A-Za-z0-9._-]{20,}")


def load(path: pathlib.Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify() -> list[str]:
    errors: list[str] = []
    manifest = load(MANIFEST)
    if manifest.get("status") != "ALL_COMPLETE":
        errors.append("evidence manifest is not ALL_COMPLETE")
    for item in manifest.get("artifacts", []):
        path = ROOT / item["path"]
        if not path.is_file():
            errors.append(f"missing artifact: {item['path']}")
            continue
        if sha256(path) != item["sha256"]:
            errors.append(f"SHA-256 mismatch: {item['path']}")
        if path.stat().st_size != item["bytes"]:
            errors.append(f"size mismatch: {item['path']}")
        if path.suffix.lower() in {".json", ".md", ".txt", ".log"}:
            if SECRET_PATTERN.search(path.read_text(encoding="utf-8", errors="ignore")):
                errors.append(f"possible API key in artifact: {item['path']}")

    baseline = TOPIC / "baseline.json"
    if sha256(baseline) != manifest.get("baseline_sha256"):
        errors.append("formal baseline SHA-256 mismatch")

    core_snapshot = load(CORE_SNAPSHOT)
    for item in core_snapshot.get("files", []):
        path = ROOT / item["path"]
        if not path.is_file():
            errors.append(f"missing core source: {item['path']}")
            continue
        if sha256(path) != item["sha256"]:
            errors.append(f"core source changed after experiment: {item['path']}")
        if path.stat().st_size != item["bytes"]:
            errors.append(f"core source size changed after experiment: {item['path']}")

    batch = TOPIC / "results" / manifest["formal_batch"]
    acceptance = load(batch / "acceptance-report.json")
    cache = load(batch / "cache-benchmark.json")
    wiki = load(batch / "wiki-cache-benchmark.json")
    negative = load(batch / "negative-control.json")
    if acceptance.get("status") != "ALL_COMPLETE" or not all(acceptance.get("checks", {}).values()):
        errors.append("acceptance report contains a failed check")
    if len(cache.get("runs", [])) != 9:
        errors.append("cache benchmark does not contain 9 runs")
    for row in cache.get("runs", []):
        task = row.get("task", {})
        metric = row.get("metric", {}).get("retrieval_metrics", {})
        if task.get("status") != 2 or task.get("finished") != 10 or task.get("total") != 10:
            errors.append(f"incomplete cache run: {row.get('phase')}:{row.get('repetition')}")
        if metric.get("recall") != 1 or metric.get("mrr") != 1:
            errors.append(f"retrieval metric changed: {row.get('phase')}:{row.get('repetition')}")
    if not cache.get("summary", {}).get("warm_reduced_real_calls"):
        errors.append("warm cache did not reduce real embedding calls")
    if len(wiki.get("runs", [])) != 8:
        errors.append("Wiki benchmark does not contain 8 calls")
    if not all(row.get("output_valid") and row.get("expected_found") for row in wiki.get("runs", [])):
        errors.append("a Wiki output quality check failed")
    if not negative.get("regression_rejected"):
        errors.append("negative control was not rejected")
    return errors


if __name__ == "__main__":
    failures = verify()
    if failures:
        for failure in failures:
            print(f"FAIL: {failure}", file=sys.stderr)
        raise SystemExit(1)
    print("EVIDENCE_OK")
