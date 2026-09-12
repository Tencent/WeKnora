#!/usr/bin/env python3
"""Audit parser outputs, run frozen olmOCR tests, and export official OmniDocBench inputs.

No language-model calls occur here. Missing runs are distinct from failed runs;
parser protocol success is distinct from usable extracted text. OmniDocBench's
official end-to-end scorer runs separately using the generated YAML files.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from datetime import datetime, timezone
import hashlib
import html
import importlib.metadata
import inspect
import json
import math
import os
from pathlib import Path
import re
import statistics
import subprocess
import sys
import time
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[1]
DATA = ROOT / "artifacts/parser-benchmark/data"
ENGINES = ["builtin", "markitdown", "opendataloader", "weknoracloud", "mineru", "mineru_cloud", "paddleocr_vl", "paddleocr_vl_cloud"]
NORMALIZATION_VERSION = "image-reference-cleanup-v1"


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8-sig"))


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def strip_image_references(markdown):
    """Remove image transport markup; preserve real captions, tables and formula syntax."""
    text = re.sub(r"!\[[^\]]*\]\((?:[^()]|\([^()]*\))*\)", " ", markdown)
    labels = [match.group(2) or match.group(1) for match in re.finditer(r"!\[([^\]]*)\]\[([^\]]*)\]", text)]
    text = re.sub(r"!\[[^\]]*\]\[[^\]]*\]", " ", text)
    text = re.sub(r"<img\b[^>]*>", " ", text, flags=re.I)
    for label in labels:
        text = re.sub(r"^\s*\[" + re.escape(label) + r"\]:\s*\S+.*$", " ", text, flags=re.M)
    return text.replace("\r\n", "\n").replace("\r", "\n")


def normalization_provenance():
    return {"version": NORMALIZATION_VERSION,
            "function": "strip_image_references",
            "function_source_sha256": hashlib.sha256(inspect.getsource(strip_image_references).replace("\r\n", "\n").encode("utf-8")).hexdigest(),
            "encoding": "UTF-8 without BOM; LF line endings",
            "description": "Uniform removal of image references, their alt text and image paths; preserve captions, text, tables, formulas and original run outputs.",
            "leaderboard_input_difference": "Official scoring algorithms run on image-reference-cleaned copies; these inputs differ from unmodified raw-Markdown leaderboard submissions."}


def text_without_images(markdown):
    """Image alt text, file paths and data URLs are not OCR recognition evidence."""
    text = strip_image_references(markdown)
    text = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"<[^>]+>", " ", text)
    text = re.sub(r"[\s#*_`|~>\-]+", " ", html.unescape(text))
    return text.strip()


def extraction_state(markdown, image_count=0):
    cleaned = text_without_images(markdown)
    chars = sum(char.isalnum() for char in cleaned)
    if chars:
        return "usable_text", chars
    if image_count or re.search(r"!\[|<img\b", markdown, flags=re.I):
        return "image_only", 0
    return "no_text", 0


def initialize_official_olm():
    runtime_record = DATA / "runtime-deps/benchmark-python.json"
    if runtime_record.exists():
        runtime = read_json(runtime_record)
        if tuple(runtime["python_major_minor"]) != sys.version_info[:2]:
            raise RuntimeError(f"Scoring dependency ABI requires Python {runtime['python_major_minor']}; current {sys.version_info[:2]}. Run with {runtime['executable']} or reinstall requirements-score.txt into a clean target with the intended interpreter.")
    # Reuse an installed matching Playwright browser when available; no forced download.
    existing = Path(os.environ.get("LOCALAPPDATA", str(Path.home() / ".cache"))) / "ms-playwright"
    if (existing / "chromium_headless_shell-1208").exists():
        os.environ.setdefault("PLAYWRIGHT_BROWSERS_PATH", str(existing))
    else:
        os.environ.setdefault("PLAYWRIGHT_BROWSERS_PATH", str(DATA / "chromium"))
    sys.path.insert(0, str(DATA / "runtime-deps"))
    sys.path.insert(0, str(DATA / "upstream/olmocr-code"))
    try:
        from olmocr.bench.tests import load_single_test
    except ImportError as exc:
        raise RuntimeError("Official olmOCR dependencies are missing or use a different Python ABI. Install requirements-score.txt with the intended Python interpreter into a fresh runtime-deps directory. See docs/parser-benchmark-dataset.md.") from exc
    return load_single_test


def verify_evaluator_sources():
    lock = read_json(ROOT / "dataset/parser-benchmark/evaluators-lock.json")
    failures = [row["path"] for row in lock["files"] if not (ROOT / row["path"]).exists() or sha256(ROOT / row["path"]) != row["sha256"]]
    if failures:
        raise ValueError("Frozen official evaluator source mismatch: " + ", ".join(failures))
    return {"olmocr_commit": lock["olm_code_commit"], "omnidocbench_commit": lock["omni_code_commit"], "source_files_verified": len(lock["files"])}


def run_olm_tests(reference, markdown, loader):
    results = []
    for definition in reference["tests"]:
        row = {"id": definition["id"], "type": definition["type"], "upstream_checked": definition.get("checked")}
        if definition.get("checked") == "rejected":
            row.update(status="excluded_upstream_rejected", passed=None)
        else:
            try:
                test = loader(definition)
                passed, reason = test.run(markdown)
                row.update(status="scored", passed=bool(passed), reason=reason)
            except Exception as exc:
                # Evaluator failures are not false model answers and never count as passes.
                row.update(status="evaluator_error", passed=None, reason=f"{type(exc).__name__}: {str(exc)[:500]}")
        results.append(row)
    scored = [row for row in results if row["status"] == "scored"]
    return {"evaluator": "official_olmocr_load_single_test", "total": len(results), "scored": len(scored),
            "passed": sum(row["passed"] for row in scored),
            "pass_rate": sum(row["passed"] for row in scored) / len(scored) if scored else None,
            "evaluator_errors": sum(row["status"] == "evaluator_error" for row in results), "tests": results}


def extract_metadata(record):
    if record.get("metadata"):
        return record["metadata"]
    result = record.get("read_result") or {}
    return result.get("Metadata", result.get("metadata", {})) or {}


def audit_sample(sample, engine, run_dir, loader=None):
    stem = run_dir / engine / sample["id"]
    metadata_path = stem.with_suffix(".json")
    markdown_path = stem.with_suffix(".md")
    row = {"engine": engine, "sample_id": sample["id"], "source": sample["source"],
           "category": sample["category"], "language": sample["language"], "input_kind": sample["input_kind"],
           "has_text_layer": sample["input_details"]["has_text_layer"],
           "pdf_path": sample["pdf_path"], "markdown_path": str(markdown_path.resolve()),
           "markdown_sha256": None, "run_record_sha256": None,
           "status": "not_run", "extraction_state": "not_run", "usable_text_characters": 0,
           "duration_ms": None, "fallback": None, "integrity": "not_run"}
    if not metadata_path.exists():
        return row
    record = read_json(metadata_path)
    row["run_record_sha256"] = sha256(metadata_path)
    row["status"] = record.get("status", "unknown")
    row["duration_ms"] = record.get("duration_ms")
    row["error"] = record.get("error")
    markdown = markdown_path.read_text(encoding="utf-8-sig") if markdown_path.exists() else ""
    row["markdown_sha256"] = sha256(markdown_path) if markdown_path.exists() else hashlib.sha256(b"").hexdigest()
    row["markdown_characters"] = len(markdown)
    mismatches = []
    if record.get("input_sha256") != sample["sha256"]:
        mismatches.append("input_sha256")
    if record.get("sample_id") != sample["id"] or record.get("engine") != engine:
        mismatches.append("result_identity")
    recorded_md_hash = record.get("markdown_sha256")
    if recorded_md_hash and (not markdown_path.exists() or sha256(markdown_path) != recorded_md_hash):
        mismatches.append("markdown_sha256")
    row["integrity"] = "verified" if not mismatches else "mismatch:" + ",".join(mismatches)
    state, characters = extraction_state(markdown, record.get("image_count", 0))
    row["extraction_state"], row["usable_text_characters"] = state, characters
    metadata = extract_metadata(record)
    row["reader_metadata"] = metadata
    row["fallback"] = metadata.get("parser_fallback")
    if mismatches:
        row["status"] = "integrity_error"
        return row
    ref_path = ROOT / sample["reference"]["path"]
    if sha256(ref_path) != sample["reference"]["sha256"]:
        raise ValueError(f"Reference hash mismatch: {sample['id']}")
    if sample["source"] == "olmOCR-bench" and loader:
        # A completed parser error is evaluated as empty output so failures stay in denominator.
        candidate = strip_image_references(markdown) if row["status"] in ("success", "empty") else ""
        row["scoring_input_sha256"] = hashlib.sha256(candidate.encode("utf-8")).hexdigest()
        row["scoring_normalization_version"] = NORMALIZATION_VERSION
        row["olmocr"] = run_olm_tests(read_json(ref_path), candidate, loader)
    return row


def mean(values):
    values = [value for value in values if value is not None]
    return statistics.mean(values) if values else None


def aggregate(rows):
    executed = [row for row in rows if row["status"] != "not_run"]
    tests = [test for row in rows for test in row.get("olmocr", {}).get("tests", [])]
    scored = [test for test in tests if test["status"] == "scored"]
    by_type = {}
    for kind in sorted({test["type"] for test in tests}):
        typed = [test for test in scored if test["type"] == kind]
        by_type[kind] = {"scored": len(typed), "passed": sum(test["passed"] for test in typed),
                         "pass_rate": mean([test["passed"] for test in typed]),
                         "evaluator_errors": sum(test["type"] == kind and test["status"] == "evaluator_error" for test in tests)}
    return {"expected_pages": len(rows), "executed_pages": len(executed), "status_counts": dict(Counter(row["status"] for row in rows)),
            "extraction_state_counts": dict(Counter(row["extraction_state"] for row in rows)),
            "protocol_success_rate_on_executed": mean([row["status"] == "success" for row in executed]),
            "usable_text_rate_on_executed": mean([row["status"] in ("success", "empty") and row["extraction_state"] == "usable_text" for row in executed]),
            "mean_duration_ms": mean([row["duration_ms"] for row in executed]),
            "fallback_pages": sum(bool(row["fallback"]) for row in executed),
            "olmocr": {"scored_tests": len(scored), "passed_tests": sum(test["passed"] for test in scored),
                       "micro_pass_rate": mean([test["passed"] for test in scored]),
                       "macro_page_pass_rate": mean([row.get("olmocr", {}).get("pass_rate") for row in rows]),
                       "evaluator_errors": sum(test["status"] == "evaluator_error" for test in tests), "by_type": by_type}}


def path_for_environment(path, path_prefix):
    relative = Path(path).resolve().relative_to(ROOT)
    return (Path(path_prefix) / relative).as_posix() if path_prefix else Path(path).resolve().as_posix()


def export_omni(manifest, rows, output, path_prefix=None):
    samples = [sample for sample in manifest["samples"] if sample["source"] == "OmniDocBench"]
    export_dir = output / "official-omnidocbench"
    export_dir.mkdir(parents=True, exist_ok=True)
    write_json(export_dir / "ground_truth.json", [read_json(ROOT / sample["reference"]["path"]) for sample in samples])
    index = {(row["engine"], row["sample_id"]): row for row in rows}
    exports = []
    for engine in sorted({row["engine"] for row in rows}):
        predictions = export_dir / engine
        predictions.mkdir(exist_ok=True)
        coverage = Counter()
        input_records = []
        for sample in samples:
            row = index[(engine, sample["id"])]
            coverage[row["status"]] += 1
            markdown = Path(row["markdown_path"]).read_text(encoding="utf-8-sig") if row["status"] in ("success", "empty") and row["integrity"] == "verified" else ""
            # The official evaluator matches the source image stem. Raw outputs stay in the run directory.
            name = Path(sample["source_sample_id"]).stem + ".md"
            normalized = strip_image_references(markdown).encode("utf-8")
            (predictions / name).write_bytes(normalized)
            input_records.append({"sample_id": sample["id"], "source_sample_id": sample["source_sample_id"],
                                  "status": row["status"], "raw_markdown_path": row["markdown_path"],
                                  "raw_markdown_sha256": row["markdown_sha256"],
                                  "run_record_sha256": row["run_record_sha256"],
                                  "scoring_filename": name, "scoring_input_sha256": hashlib.sha256(normalized).hexdigest(),
                                  "scoring_input_bytes": len(normalized),
                                  "error_as_empty_output": row["status"] not in ("success", "empty", "not_run")})
        input_manifest = export_dir / f"{engine}-input-manifest.json"
        write_json(input_manifest, {"schema_version": 1, "engine": engine, "normalization": normalization_provenance(),
                                   "ground_truth_sha256": sha256(export_dir / "ground_truth.json"), "samples": input_records})
        gt = path_for_environment(export_dir / "ground_truth.json", path_prefix)
        pred = path_for_environment(predictions, path_prefix)
        common = ("end2end_eval:\n  metrics:\n    text_block:\n      metric: [Edit_dist]\n"
                  "    display_formula:\n      metric: [Edit_dist{formula}]\n"
                  "      cdm_workers: 1\n    table:\n      metric: [TEDS, Edit_dist]\n      teds_workers: 1\n"
                  "    reading_order:\n      metric: [Edit_dist]\n  dataset:\n"
                  "    dataset_name: end2end_dataset\n    ground_truth:\n      data_path: {gt}\n"
                  "    prediction:\n      data_path: {pred}\n    match_method: quick_match\n    match_workers: 1\n")
        for label, formula in [("standard", ""), ("cdm", ", CDM")]:
            config = export_dir / f"{engine}-{label}.yaml"
            config.write_text(common.format(formula=formula, gt=json.dumps(gt), pred=json.dumps(pred)), encoding="utf-8")
        exports.append({"engine": engine, "pages": len(samples), "coverage": dict(coverage),
                        "input_normalization": normalization_provenance(), "input_manifest_path": str(input_manifest),
                        "ready_for_official_scoring": not coverage.get("not_run", 0) and not coverage.get("integrity_error", 0),
                        "standard_config": str((export_dir / f"{engine}-standard.yaml").resolve()),
                        "cdm_config": str((export_dir / f"{engine}-cdm.yaml").resolve())})
    return exports


def human_review_queue(manifest, rows):
    """Deterministic issue-focused triage. Never substitutes for a human signature."""
    samples = {sample["id"]: sample for sample in manifest["samples"]}
    by_sample = defaultdict(list)
    for row in rows:
        by_sample[row["sample_id"]].append(row)
    reviews = []
    for sample_id, group in by_sample.items():
        sample = samples[sample_id]
        review_identity = {"schema_version": 1, "sample_id": sample_id, "input_sha256": sample["sha256"],
                           "reference_sha256": sample["reference"]["sha256"],
                           "engine_results": [{"engine": row["engine"], "status": row["status"],
                                               "markdown_sha256": row.get("markdown_sha256"),
                                               "run_record_sha256": row.get("run_record_sha256")}
                                              for row in sorted(group, key=lambda row: row["engine"])]}
        review_fingerprint = hashlib.sha256(json.dumps(review_identity, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        reasons = []
        if sample.get("element_counts", {}).get("table") or sample["category"] == "table_tests":
            reasons.append("核对表格单元格归属、跨行跨列及数字")
        if sample.get("element_counts", {}).get("equation_isolated") or "math" in sample["category"]:
            reasons.append("核对公式上下标、分数和符号；字符串指标不能证明数学等价")
        if sample.get("layout") not in ("single_column", None) or sample["category"] == "multi_column":
            reasons.append("核对多栏阅读顺序及段落连接")
        if any(row["extraction_state"] == "image_only" for row in group):
            reasons.append("确认图片引用没有被误算成已识别正文")
        if any(row["fallback"] for row in group):
            reasons.append("核对回退记录与实际输出，确认展示的引擎归属")
        if any(row.get("olmocr", {}).get("evaluator_errors") for row in group):
            reasons.append("评测工具异常需排查；当前缺失分数不能视为通过")
        if any(row["status"] not in ("success", "empty", "not_run") for row in group):
            reasons.append("查看失败或超时样本，区分权限、服务故障与解析质量")
        rates = [row.get("olmocr", {}).get("pass_rate") for row in group if row.get("olmocr", {}).get("pass_rate") is not None]
        if rates and max(rates) - min(rates) >= .3:
            reasons.append("不同引擎的官方断言通过率差异较大")
        if not reasons:
            reasons.append("常规抽样核对正文遗漏、重复与不受原文支持的内容")
        reviews.append({"sample_id": sample_id, "source": sample["source"], "category": sample["category"],
                        "language": sample["language"], "pdf_path": sample["pdf_path"],
                        "priority": len(reasons), "reasons": reasons, "status": "pending",
                        "review_fingerprint": review_fingerprint, "review_identity": review_identity,
                        "review_history": [],
                        "reviewer": None, "reviewed_at": None, "decision": None, "notes": "",
                        "engine_outputs": [{"engine": row["engine"], "status": row["status"], "extraction_state": row["extraction_state"],
                                            "markdown_path": row["markdown_path"], "markdown_sha256": row.get("markdown_sha256"),
                                            "run_record_sha256": row.get("run_record_sha256"), "fallback": row["fallback"]} for row in group]})
    reviews.sort(key=lambda row: (-row["priority"], row["sample_id"]))
    # Limit recommended first pass to 20 pages, keep every page auditable in complete list.
    return {"schema_version": 1, "human_review_completed": 0, "recommended_first_pass": [row["sample_id"] for row in reviews[:20]],
            "items": reviews, "completion_rule": "仅实际审核者填写 reviewer、reviewed_at、decision；AI 不代签。"}


def merge_previous_reviews(queue, previous_queue):
    """A signature belongs to exact outputs and run metadata, never just a sample ID."""
    previous = {row["sample_id"]: row for row in previous_queue.get("items", [])}
    fields = ("status", "reviewer", "reviewed_at", "decision", "notes")
    for row in queue["items"]:
        old = previous.get(row["sample_id"], {})
        row["review_history"] = list(old.get("review_history", []))
        same_outputs = bool(old.get("review_fingerprint")) and old["review_fingerprint"] == row["review_fingerprint"]
        if same_outputs:
            for key in fields:
                if old.get(key):
                    row[key] = old[key]
        elif old and (old.get("status", "pending") != "pending" or any(old.get(key) for key in fields[1:])):
            stale = {key: old.get(key) for key in fields}
            stale.update(review_fingerprint=old.get("review_fingerprint"),
                         stale_reason="output_or_run_identity_changed" if old.get("review_fingerprint") else "legacy_review_without_output_fingerprint",
                         archived_at=datetime.now(timezone.utc).isoformat())
            row["review_history"].append(stale)
    queue["human_review_completed"] = sum(row["status"] == "completed" and all(row.get(key) for key in ("reviewer", "reviewed_at", "decision")) for row in queue["items"])
    return queue


def run_official_omni(exports, output, container, path_prefix):
    """Run unchanged official CLI, keeping a receipt tied to exact input/config bytes."""
    folder = output / "official-omnidocbench"
    receipt_path = folder / "execution-receipts.json"
    receipts = read_json(receipt_path) if receipt_path.exists() else {}
    code = DATA / "upstream/omnidocbench-code/pdf_validation.py"
    executable = "/opt/omni-venv/bin/python"
    lock_hash = sha256(ROOT / "dataset/parser-benchmark/evaluators-lock.json")
    for export in exports:
        engine = export["engine"]
        if not export["ready_for_official_scoring"]:
            if engine in receipts:
                receipts[engine] = {"status": "not_ready", "coverage": export["coverage"],
                                    "reason": "Current inputs have missing or invalid results; previous scores are not current."}
            continue
        config = Path(export["standard_config"])
        inputs = [("ground_truth.json", sha256(folder / "ground_truth.json")), (config.name, sha256(config)),
                  (Path(export["input_manifest_path"]).name, sha256(export["input_manifest_path"]))]
        inputs += [(path.name, sha256(path)) for path in sorted((folder / engine).glob("*.md"))]
        fingerprint = hashlib.sha256(json.dumps([inputs, lock_hash], ensure_ascii=False).encode()).hexdigest()
        metric_path = folder / "result" / f"{engine}_quick_match_metric_result.json"
        previous = receipts.get(engine, {})
        if previous.get("fingerprint") == fingerprint and previous.get("status") == "completed" and metric_path.exists() and previous.get("metric_result_sha256") == sha256(metric_path):
            print(f"official OmniDocBench {engine}: verified existing result", flush=True)
            continue
        command = ["docker", "exec", "-e", "OPENBLAS_NUM_THREADS=1", "-e", "OMP_NUM_THREADS=1", "-w", path_for_environment(folder, path_prefix),
                   container, executable, path_for_environment(code, path_prefix), "--config", path_for_environment(config, path_prefix)]
        log_path = folder / f"{engine}-official.log"
        receipts[engine] = {"status": "running", "fingerprint": fingerprint, "command": command,
                            "log_path": str(log_path), "input_pages": export["pages"], "source_lock_sha256": lock_hash}
        write_json(receipt_path, receipts)
        started = time.monotonic()
        print(f"official OmniDocBench {engine}: starting {export['pages']} pages", flush=True)
        with log_path.open("w", encoding="utf-8") as log:
            process = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT)
        receipt = receipts[engine]
        receipt.update(exit_code=process.returncode, duration_seconds=round(time.monotonic() - started, 3))
        if process.returncode == 0 and metric_path.exists():
            receipt.update(status="completed", metric_result_path=str(metric_path), metric_result_sha256=sha256(metric_path), metrics=read_json(metric_path))
            run_summary = folder / "result" / f"{engine}_quick_match_run_summary.json"
            if run_summary.exists():
                receipt["run_summary_path"] = str(run_summary)
        else:
            receipt["status"] = "failed"
        write_json(receipt_path, receipts)
        print(f"official OmniDocBench {engine}: {receipt['status']}", flush=True)
    write_json(receipt_path, receipts)
    return receipts


def official_image_name(value):
    """Mirror the frozen evaluator's per-page key convention for result auditing."""
    value = str(value or "")
    return value if value.endswith((".jpg", ".png")) else "_".join(value.split("_")[:-1])


def audit_official_denominators(exports, output, receipts):
    """Report actual upstream denominator coverage; never manufacture missing scores."""
    folder = output / "official-omnidocbench"
    ground_truth = read_json(folder / "ground_truth.json")
    text_categories = {"text_block", "title", "code_txt", "code_txt_caption", "reference"}
    order_categories = text_categories | {"equation_isolated", "table"}
    expected = {kind: set() for kind in ("text_block", "display_formula", "table", "reading_order")}
    for page in ground_truth:
        name = Path(page["page_info"]["image_path"]).name
        elements = [item for item in page["layout_dets"] if not item.get("ignore")]
        if any(item["category_type"] in text_categories for item in elements):
            expected["text_block"].add(name)
        if any(item["category_type"] == "equation_isolated" for item in elements):
            expected["display_formula"].add(name)
        if any(item["category_type"] == "table" for item in elements):
            expected["table"].add(name)
        if any(item["category_type"] in order_categories and item.get("order") for item in elements):
            expected["reading_order"].add(name)
    all_names = {Path(page["page_info"]["image_path"]).name for page in ground_truth}
    audit = {"schema_version": 1, "source_pages": len(ground_truth),
             "definition": "Audit-only GT category sets follow the frozen text-ignore list, nonignored formula/table labels and nonzero reading order. Actual per-metric pages and denominators are read separately from official result files; no scores are filled or changed.",
             "upstream_behavior": {"text_and_reading_order": "Edit distance uses matched nonempty GT/prediction samples. Pages containing only ignored categories have no score.",
                                   "table_TEDS": "Official page mean includes every nonignored GT table page, adding zero for a missing scored page.",
                                   "formula_Edit_dist": "ALL_page_avg uses actual scored formula pages; official reported denominator uses GT formula pages. Both counts are exposed independently.",
                                   "CDM": "not_executed"},
             "gt_category_pages": {kind: {"count": len(names), "page_ids": sorted(names),
                                           "source_pages_without_category": sorted(all_names - names)} for kind, names in expected.items()},
             "engines": {}}
    for export in exports:
        engine = export["engine"]
        receipt = receipts.get(engine, {})
        if receipt.get("status") != "completed" or not export["ready_for_official_scoring"]:
            continue
        summary_path = folder / "result" / f"{engine}_quick_match_run_summary.json"
        if not summary_path.exists():
            audit["engines"][engine] = {"status": "missing_official_run_summary"}
            continue
        run_summary = read_json(summary_path)
        input_records = read_json(export["input_manifest_path"])["samples"]
        # Names come from the same original page image IDs used by the upstream evaluator.
        input_rows = {Path(row["source_sample_id"]).name: row for row in input_records}
        kinds = {}
        for kind, names in expected.items():
            results_path = folder / "result" / f"{engine}_quick_match_{kind}_result.json"
            results = read_json(results_path) if results_path.exists() else []
            metric = "TEDS" if kind == "table" else "Edit_dist"
            scored = [row for row in results if isinstance(row.get("metric", {}).get(metric), (int, float)) and math.isfinite(row["metric"][metric])]
            scored_names = {official_image_name(row["img_id"]) for row in scored}
            per_page_edit = folder / "result" / f"{engine}_quick_match_{kind}_per_page_edit.json"
            edit_pages = read_json(per_page_edit) if per_page_edit.exists() else {}
            finite_edit_pages = {name for name, value in edit_pages.items() if isinstance(value, (int, float)) and math.isfinite(value)}
            # TEDS has explicit upstream zero penalties; Edit_dist has no such post-fill.
            actual_denominator_names = (scored_names | names) if kind == "table" else finite_edit_pages
            failures = {name for name in names if input_rows[name]["error_as_empty_output"]}
            empty = {name for name in names if not (folder / engine / input_rows[name]["scoring_filename"]).read_text(encoding="utf-8").strip()}
            reported = run_summary.get("page_denominators", {}).get(kind, {}).get(metric, {}).get("ALL")
            kinds[kind] = {"metric": metric, "official_reported_page_denominator": reported,
                           "actual_metric_page_denominator": len(actual_denominator_names),
                           "gt_category_page_count": len(names), "scored_instances": len(scored),
                           "actual_metric_page_ids": sorted(actual_denominator_names),
                           "gt_pages_without_scored_instances": sorted(names - scored_names),
                           "gt_pages_excluded_from_metric": sorted(names - actual_denominator_names),
                           "metric_pages_without_gt_category": sorted(actual_denominator_names - names),
                           "official_zero_penalty_page_ids": sorted(names - scored_names) if kind == "table" else [],
                           "empty_prediction_gt_pages": len(empty),
                           "empty_prediction_gt_pages_in_metric": len(empty & actual_denominator_names),
                           "failed_prediction_gt_pages": len(failures),
                           "failed_prediction_gt_pages_in_metric": len(failures & actual_denominator_names),
                           "denominator_matches_actual": reported == len(actual_denominator_names)}
        audit["engines"][engine] = {"status": "audited", "metrics": kinds,
                                     "stage_execution": run_summary.get("stage_execution"),
                                     "notebook_metric_summary": run_summary.get("notebook_metric_summary")}
    audit_path = folder / "denominator-audit.json"
    write_json(audit_path, audit)
    return {"path": str(audit_path), "gt_category_page_counts": {kind: len(names) for kind, names in expected.items()},
            "engines": audit["engines"]}


def render_review_html(queue, output):
    cards = []
    for item in queue["items"]:
        # Build links directly from file URI; no embedded untrusted document HTML/scripts.
        outputs = "".join(f'<li><a href="{html.escape(Path(row["markdown_path"]).as_uri(), quote=True)}">{html.escape(row["engine"])}</a> — {html.escape(row["status"])} / {html.escape(row["extraction_state"])}</li>' for row in item["engine_outputs"])
        reasons = "".join(f"<li>{html.escape(reason)}</li>" for reason in item["reasons"])
        cards.append(f'<article><h2>{html.escape(item["sample_id"])}</h2><p>{html.escape(item["source"])} · {html.escape(item["language"])} · {html.escape(item["category"])}</p><p><a href="{html.escape((ROOT / item["pdf_path"]).resolve().as_uri(), quote=True)}">打开原始测试 PDF</a> · 人工审核：{html.escape(item["status"])}</p><ul>{reasons}</ul><details><summary>引擎输出</summary><ul>{outputs}</ul></details></article>')
    content = '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>解析引擎人工核验清单</title><style>body{font-family:system-ui,sans-serif;max-width:1100px;margin:40px auto;padding:0 24px;background:#f7f8fa;color:#18202b}h1{font-size:32px}article{background:white;border:1px solid #dfe4ea;padding:24px;margin:18px 0;border-radius:12px}h2{font-size:18px}a{color:#146a50}li{line-height:1.8}summary{cursor:pointer}</style><h1>解析引擎人工核验清单</h1><p>审核记录绑定当前页面全部引擎输出与运行记录指纹。建议先核验 JSON 中 recommended_first_pass 的最多 20 页。页面用于原文与输出定位，实际审核记录保存在 human-review.json。</p>' + "".join(cards) + "</html>"
    (output / "human-review.html").write_text(content, encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, default=ROOT / "dataset/parser-benchmark/manifest-smoke.json")
    parser.add_argument("--runs", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--engines", default=",".join(ENGINES))
    parser.add_argument("--skip-olm", action="store_true", help="Audit/export only; explicitly records official olm tests as not executed")
    parser.add_argument("--official-path-prefix", help="Root mount inside official evaluator container, e.g. /benchmark")
    parser.add_argument("--official-container", help="Run unchanged official OmniDocBench standard evaluation in this prepared container; resumes verified results")
    args = parser.parse_args()
    if args.official_container and not args.official_path_prefix:
        args.official_path_prefix = "/benchmark"
    args.runs = args.runs.resolve()
    output = (args.output or args.runs / "evaluation").resolve()
    output.mkdir(parents=True, exist_ok=True)
    manifest = read_json(args.manifest)
    for sample in manifest["samples"]:
        if sha256(ROOT / sample["pdf_path"]) != sample["sha256"]:
            raise ValueError(f"Input hash mismatch: {sample['id']}")
    provenance = verify_evaluator_sources()
    loader = None if args.skip_olm else initialize_official_olm()
    rows = []
    for engine in args.engines.split(","):
        for sample in manifest["samples"]:
            row = audit_sample(sample, engine, args.runs, loader)
            rows.append(row)
        print(f"audited {engine}", flush=True)
    engines = {}
    for engine in args.engines.split(","):
        group = [row for row in rows if row["engine"] == engine]
        engines[engine] = aggregate(group)
        engines[engine]["by_source"] = {source: aggregate([row for row in group if row["source"] == source]) for source in sorted({row["source"] for row in group})}
        engines[engine]["by_text_layer"] = {str(layer): aggregate([row for row in group if row["has_text_layer"] == layer]) for layer in (True, False)}
    summary = {"schema_version": 1, "manifest_sha256": sha256(args.manifest), "manifest_path": str(args.manifest.resolve()),
               "scope": "production_reader_markdown_before_chunking_and_image_OCR", "is_official_leaderboard_run": False,
               "evaluator_provenance": provenance, "scoring_input_normalization": normalization_provenance(),
               "olmocr_status": "not_run" if args.skip_olm else "executed_see_per_test_errors",
               "omnidocbench_status": "inputs_exported_official_scoring_separate", "engines": engines,
               "interpretation": ["protocol success 仅表示 reader 返回，usable_text 仅表示去图片后存在字母或数字，并非质量达标",
                                  "OmniDocBench 为图像封装输入；olmOCR 为原始 PDF，按来源和文字层分组解释",
                                  "not_run 不计入执行成功率；已完成错误使用空输出进入官方 olm 断言",
                                  "官方 olm 数学断言检查 KaTeX 渲染，不等同于 OmniDocBench CDM；工具异常单独列出"]}
    summary["interpretation"].append("各引擎统一移除评分副本的图片引用、alt 文本和图片路径；原始 Markdown 保留。评分输入与完全原样 Markdown 榜单提交口径不同。")
    summary["all_requested_runs_complete"] = all(row["status"] not in ("not_run", "integrity_error") for row in rows)
    summary["engines_with_full_run_coverage"] = [engine for engine, stats in engines.items() if stats["executed_pages"] == stats["expected_pages"] and not stats["status_counts"].get("integrity_error")]
    summary["official_omnidocbench_exports"] = export_omni(manifest, rows, output, args.official_path_prefix)
    write_json(output / "summary.json", summary)
    write_json(output / "page-results.json", rows)
    queue = human_review_queue(manifest, rows)
    queue_path = output / "human-review.json"
    # Preserve any genuine review records on rerun; never overwrite user decisions.
    if queue_path.exists():
        queue = merge_previous_reviews(queue, read_json(queue_path))
    write_json(queue_path, queue)
    render_review_html(queue, output)
    summary["human_review"] = {"total_pages": len(queue["items"]), "completed_pages": queue["human_review_completed"],
                             "recommended_first_pass_pages": len(queue["recommended_first_pass"]),
                             "reason_counts": dict(Counter(reason for item in queue["items"] for reason in item["reasons"])),
                             "queue_path": str(queue_path)}
    write_json(output / "summary.json", summary)
    if args.official_container:
        summary["official_omnidocbench_runs"] = run_official_omni(summary["official_omnidocbench_exports"], output, args.official_container, args.official_path_prefix)
        summary["official_omnidocbench_denominators"] = audit_official_denominators(summary["official_omnidocbench_exports"], output, summary["official_omnidocbench_runs"])
        summary["omnidocbench_status"] = "see_execution_receipts"
        write_json(output / "summary.json", summary)
    print(f"Results: {output}", flush=True)


if __name__ == "__main__":
    main()
