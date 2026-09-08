#!/usr/bin/env python3
"""Topic 3 reproducible runner. Uses only the Python standard library."""

from __future__ import annotations

import argparse
import datetime
import hashlib
import http.client
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = ROOT / "codex" / "topic3" / "results"
DEFAULT_BASELINE = ROOT / "codex" / "topic3" / "baseline.json"
ACTIVE_LOG: pathlib.Path | None = None
RUNNER_VERSION = "2026-09-08.3"


def emit(message: str) -> None:
    print(message, flush=True)
    if ACTIVE_LOG is not None:
        ACTIVE_LOG.parent.mkdir(parents=True, exist_ok=True)
        with ACTIVE_LOG.open("a", encoding="utf-8") as handle:
            handle.write(message + "\n")


def write_json(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")
    temporary.replace(path)


def resolve_command(command: list[str], platform: str = os.name) -> list[str]:
    if platform == "nt" and command and command[0] == "npm":
        npm_command = shutil.which("npm.cmd")
        if not npm_command:
            raise RuntimeError("npm.cmd was not found in PATH; install Node.js or add its directory to PATH")
        return [npm_command, *command[1:]]
    return command


def run_logged(command: list[str], cwd: pathlib.Path = ROOT, environment: dict[str, str] | None = None) -> None:
    command = resolve_command(command)
    emit("$ " + " ".join(command))
    process = subprocess.Popen(
        command, cwd=cwd, env=environment, text=True, stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT, encoding="utf-8", errors="replace",
    )
    assert process.stdout is not None
    for line in process.stdout:
        emit(line.rstrip())
    if process.wait() != 0:
        raise RuntimeError(f"command failed ({process.returncode}): {' '.join(command)}")


def auth_header(token: str) -> tuple[str, str]:
    if token.startswith("sk-"):
        return "X-API-Key", token
    return "Authorization", f"Bearer {token}"


def api(method: str, path: str, body: dict | None = None) -> dict:
    base = os.getenv("TOPIC3_BASE_URL", "http://localhost:8080").rstrip("/")
    token = os.getenv("TOPIC3_TOKEN", "").strip()
    if not token:
        raise RuntimeError("TOPIC3_TOKEN is required; export a local JWT or API key before a real evaluation")
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(base + path, data=data, method=method)
    header, value = auth_header(token)
    request.add_header(header, value)
    request.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(request, timeout=300) as response:
        return json.load(response)


def run_evaluation(output_dir: pathlib.Path = OUT) -> pathlib.Path:
    payload = {
        "dataset_id": os.getenv("TOPIC3_DATASET_ID", "default"),
        "knowledge_base_id": os.getenv("TOPIC3_KB_ID", ""),
        "chat_id": os.getenv("TOPIC3_CHAT_MODEL_ID", ""),
        "rerank_id": os.getenv("TOPIC3_RERANK_MODEL_ID", ""),
    }
    created = api("POST", "/api/v1/evaluation", payload)["data"]
    task_id = created["task"]["id"]
    deadline = time.time() + int(os.getenv("TOPIC3_EVAL_TIMEOUT_SECONDS", "3600"))
    while time.time() < deadline:
        result = api("GET", "/api/v1/evaluation?" + urllib.parse.urlencode({"task_id": task_id}))["data"]
        status = result["task"]["status"]
        print(f"evaluation {task_id}: {result['task'].get('finished', 0)}/{result['task'].get('total', 0)} status={status}")
        if status == 2:
            output_dir.mkdir(parents=True, exist_ok=True)
            target = output_dir / f"{task_id}.json"
            write_json(target, result)
            emit(str(target))
            return target
        if status == 3:
            raise RuntimeError(result["task"].get("err_msg", "evaluation failed"))
        time.sleep(3)
    raise TimeoutError(f"evaluation {task_id} did not finish before timeout")


def utc_now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")


def usage_summary(start: str, end: str, model: str = "") -> dict:
    params = {"start": start, "end": end}
    if model:
        params["model"] = model
    query = urllib.parse.urlencode(params)
    return api("GET", "/api/v1/evaluation/model-usage?" + query)["data"]


def restart_app(cache_enabled: bool, drop_relevant: bool = False) -> None:
    environment = os.environ.copy()
    environment["TOPIC3_EMBEDDING_CACHE_ENABLED"] = "true" if cache_enabled else "false"
    environment["TOPIC3_EVAL_DROP_RELEVANT"] = "true" if drop_relevant else "false"
    subprocess.run(
        ["docker", "compose", "up", "-d", "--force-recreate", "app"],
        cwd=ROOT, env=environment, check=True,
    )
    health = os.getenv("TOPIC3_BASE_URL", "http://localhost:8080").rstrip("/") + "/health"
    deadline = time.time() + 120
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(health, timeout=5) as response:
                if response.status == 200:
                    expected = {
                        "TOPIC3_EMBEDDING_CACHE_ENABLED": "true" if cache_enabled else "false",
                        "TOPIC3_EVAL_DROP_RELEVANT": "true" if drop_relevant else "false",
                    }
                    for name, value in expected.items():
                        actual = subprocess.run(
                            ["docker", "compose", "exec", "-T", "app", "printenv", name],
                            cwd=ROOT, check=True, text=True, capture_output=True,
                            encoding="utf-8", errors="replace",
                        ).stdout.strip().lower()
                        if actual != value:
                            raise RuntimeError(f"app setting {name} is {actual!r}, expected {value!r}")
                    return
        except (urllib.error.URLError, http.client.RemoteDisconnected, ConnectionError, TimeoutError, OSError):
            pass
        time.sleep(2)
    raise TimeoutError("app did not become healthy after cache mode restart")


def experiment_hashes(dataset_id: str) -> list[str]:
    source = ROOT / "codex" / "topic3" / "datasets" / dataset_id / "source.json"
    if not source.exists():
        raise RuntimeError(f"cache benchmark requires an auditable source.json: {source}")
    rows = json.loads(source.read_text(encoding="utf-8"))
    texts = {str(row[field]) for row in rows for field in ("passage", "question")}
    return sorted(hashlib.sha256(text.encode()).hexdigest() for text in texts)


def clear_experiment_cache(dataset_id: str) -> int:
    kb_id = os.getenv("TOPIC3_KB_ID", "")
    if not re.fullmatch(r"[0-9a-fA-F-]{36}", kb_id):
        raise RuntimeError("TOPIC3_KB_ID must be a UUID before scoped cache cleanup")
    hashes = experiment_hashes(dataset_id)
    quoted = ",".join("'" + value + "'" for value in hashes)
    sql = (
        "WITH target AS (SELECT tenant_id FROM knowledge_bases WHERE id='" + kb_id + "'), "
        "deleted AS (DELETE FROM embedding_cache_entries WHERE tenant_id=(SELECT tenant_id FROM target) "
        "AND input_hash IN (" + quoted + ") RETURNING 1) SELECT count(*) FROM deleted;"
    )
    completed = subprocess.run(
        ["docker", "compose", "exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "WeKnora", "-Atc", sql],
        cwd=ROOT, check=True, text=True, capture_output=True,
    )
    lines = [line.strip() for line in completed.stdout.splitlines() if line.strip().isdigit()]
    return int(lines[-1]) if lines else 0


def benchmark_run(phase: str, repetition: int, output_dir: pathlib.Path = OUT) -> dict:
    start = utc_now()
    result_path = run_evaluation(output_dir)
    end = utc_now()
    result = json.loads(result_path.read_text(encoding="utf-8"))
    models = {
        "chat": os.getenv("TOPIC3_CHAT_MODEL_NAME", "qwen3.7-flash-2026-07-15"),
        "embedding": os.getenv("TOPIC3_EMBEDDING_MODEL_NAME", "text-embedding-v4"),
        "rerank": os.getenv("TOPIC3_RERANK_MODEL_NAME", "qwen3-rerank"),
    }
    return {
        "phase": phase,
        "repetition": repetition,
        "started_at": start,
        "ended_at": end,
        "result": str(result_path.relative_to(ROOT)),
        "task": result.get("task", {}),
        "metric": result.get("metric", {}),
        "usage": usage_summary(start, end),
        "usage_by_model": {name: usage_summary(start, end, model) for name, model in models.items()},
    }


def run_cache_benchmark(output_dir: pathlib.Path = OUT, checkpoint=None) -> pathlib.Path:
    if os.getenv("TOPIC3_CACHE_BENCH_CONFIRM") != "YES":
        raise RuntimeError("set TOPIC3_CACHE_BENCH_CONFIRM=YES to authorize the nine-run paid benchmark")
    dataset_id = os.getenv("TOPIC3_DATASET_ID", "")
    if dataset_id != "topic3-10":
        raise RuntimeError("formal cache benchmark requires TOPIC3_DATASET_ID=topic3-10")
    report = {"dataset_id": dataset_id, "runs": []}
    target = (
        output_dir / ("cache-benchmark-" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S") + ".json")
        if output_dir == OUT else output_dir / "cache-benchmark.json"
    )

    def record(row: dict) -> None:
        report["runs"].append(row)
        write_json(target, report)
        if checkpoint is not None:
            checkpoint(row)

    try:
        restart_app(False)
        for repetition in range(1, 4):
            record(benchmark_run("disabled", repetition, output_dir / "evaluations"))
        restart_app(True)
        for repetition in range(1, 4):
            deleted = clear_experiment_cache(dataset_id)
            row = benchmark_run("cold", repetition, output_dir / "evaluations")
            row["scoped_cache_rows_deleted"] = deleted
            record(row)
        for repetition in range(1, 4):
            record(benchmark_run("warm", repetition, output_dir / "evaluations"))
    finally:
        restart_app(True)
    validate_cache_benchmark(report)
    write_json(target, report)
    return target


def validate_cache_row(row: dict) -> None:
    task = row.get("task", {})
    if task.get("status") != 2 or task.get("finished") != 10 or task.get("total") != 10:
        raise RuntimeError(f"cache {row['phase']} run {row['repetition']} did not finish 10/10")
    before = metrics(DEFAULT_BASELINE)
    current = metrics(ROOT / row["result"])
    for name in ("recall", "mrr"):
        if before[name] - current[name] > 0.03:
            raise RuntimeError(f"cache {row['phase']} run {row['repetition']} regressed {name}")
    usage = row["usage_by_model"]
    if usage["chat"].get("calls", 0) > 50 or usage["rerank"].get("calls", 0) > 50:
        raise RuntimeError("per-evaluation provider call cap exceeded")
    embedding = usage["embedding"]
    if row["phase"] in ("disabled", "cold") and embedding.get("calls", 0) < 1:
        raise RuntimeError(f"{row['phase']} run did not make a real embedding request")
    if row["phase"] == "disabled" and embedding.get("embedding_cache_hits", 0) != 0:
        raise RuntimeError("cache-disabled run unexpectedly reported a local cache hit")


def validate_cache_benchmark(report: dict) -> None:
    runs = report.get("runs", [])
    if len(runs) != 9:
        raise RuntimeError(f"cache benchmark produced {len(runs)} runs, expected 9")
    phases = [row.get("phase") for row in runs]
    if phases != ["disabled"] * 3 + ["cold"] * 3 + ["warm"] * 3:
        raise RuntimeError("cache benchmark phase order is invalid")
    cold_calls = sum(row["usage_by_model"]["embedding"].get("calls", 0) for row in runs if row["phase"] == "cold")
    warm_calls = sum(row["usage_by_model"]["embedding"].get("calls", 0) for row in runs if row["phase"] == "warm")
    report["summary"] = {
        "cold_embedding_calls": cold_calls,
        "warm_embedding_calls": warm_calls,
        "warm_reduced_real_calls": warm_calls < cold_calls,
    }
    if not report["summary"]["warm_reduced_real_calls"]:
        raise RuntimeError(f"warm cache did not reduce embedding calls: cold={cold_calls}, warm={warm_calls}")


def db_scalar(sql: str) -> str:
    completed = subprocess.run(
        ["docker", "compose", "exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "WeKnora", "-Atc", sql],
        cwd=ROOT, check=True, text=True, capture_output=True, encoding="utf-8", errors="replace",
    )
    values = [line.strip() for line in completed.stdout.splitlines() if line.strip()]
    return values[-1] if values else ""


def database_snapshot() -> dict:
    return {
        "evaluation_runs": int(db_scalar("SELECT count(*) FROM evaluation_runs;")),
        "unfinished_evaluations": int(db_scalar("SELECT count(*) FROM evaluation_runs WHERE status IN (0,1);")),
        "model_usage_rows": int(db_scalar("SELECT count(*) FROM model_usages;")),
        "embedding_cache_rows": int(db_scalar("SELECT count(*) FROM embedding_cache_entries;")),
    }


def baseline_source_hash() -> str:
    baseline = json.loads(DEFAULT_BASELINE.read_text(encoding="utf-8"))
    if not baseline.get("confirmed_by_user"):
        raise RuntimeError("formal baseline has not been confirmed by the user")
    source = ROOT / baseline["source_result"]
    actual = hashlib.sha256(source.read_bytes()).hexdigest()
    if actual != baseline.get("source_result_sha256"):
        raise RuntimeError("formal baseline source SHA-256 does not match")
    return actual


def scan_result_secrets() -> list[str]:
    pattern = re.compile(r"sk-[A-Za-z0-9._-]{20,}")
    hits = []
    if not OUT.exists():
        return hits
    for path in OUT.rglob("*"):
        if not path.is_file() or path.suffix.lower() not in {".json", ".md", ".log", ".txt"}:
            continue
        try:
            if pattern.search(path.read_text(encoding="utf-8", errors="ignore")):
                hits.append(str(path.relative_to(ROOT)))
        except OSError:
            continue
    return hits


def run_preflight(output_dir: pathlib.Path) -> dict:
    emit("[A] FREE PREFLIGHT")
    expected_services = {"app", "docreader", "frontend", "postgres", "redis"}
    running = subprocess.run(
        ["docker", "compose", "ps", "--services", "--filter", "status=running"], cwd=ROOT,
        check=True, text=True, capture_output=True, encoding="utf-8", errors="replace",
    )
    services = {line.strip() for line in running.stdout.splitlines() if line.strip()}
    if not expected_services.issubset(services):
        raise RuntimeError(f"required services are not running: {sorted(expected_services-services)}")
    migration = db_scalar("SELECT version||':'||dirty FROM schema_migrations;")
    if migration != "83:false" and migration != "83:f":
        raise RuntimeError(f"unexpected migration state: {migration}")
    if database_snapshot()["unfinished_evaluations"]:
        raise RuntimeError("an evaluation is already pending or running")
    api("GET", "/api/v1/evaluation/history?limit=1")
    ids = {
        "knowledge_base": os.getenv("TOPIC3_KB_ID", ""),
        "chat": os.getenv("TOPIC3_CHAT_MODEL_ID", ""),
        "rerank": os.getenv("TOPIC3_RERANK_MODEL_ID", ""),
        "embedding": os.getenv("TOPIC3_EMBEDDING_MODEL_ID", ""),
    }
    for name, value in ids.items():
        if not re.fullmatch(r"[0-9a-fA-F-]{36}", value):
            raise RuntimeError(f"invalid fixed {name} id")
    if int(db_scalar(f"SELECT count(*) FROM knowledge_bases WHERE id='{ids['knowledge_base']}' AND deleted_at IS NULL;")) != 1:
        raise RuntimeError("fixed evaluation knowledge base is missing")
    for name in ("chat", "rerank", "embedding"):
        if int(db_scalar(f"SELECT count(*) FROM models WHERE id='{ids[name]}' AND deleted_at IS NULL;")) != 1:
            raise RuntimeError(f"fixed {name} model is missing")
    baseline_hash = baseline_source_hash()
    if scan_result_secrets():
        raise RuntimeError("a possible API key was found in the result directory")

    run_logged([sys.executable, "-m", "unittest", "codex.topic3.test_topic3", "-v"])
    run_logged([
        "docker", "run", "--rm", "-v", f"{ROOT}:/workspace", "-v", "weknora-go-mod-cache:/go/pkg/mod",
        "-v", "weknora-go-build-cache:/root/.cache/go-build", "-w", "/workspace", "golang:1.26",
        "go", "test", "./internal/agent", "./internal/application/repository", "./internal/application/service",
    ])
    run_logged(["npm", "run", "type-check"], ROOT / "frontend")
    report = {
        "status": "passed", "migration": migration, "services": sorted(services),
        "baseline_source_sha256": baseline_hash, "database_before": database_snapshot(), "fixed_ids": ids,
    }
    write_json(output_dir / "preflight.json", report)
    return report


def wiki_probe(layout: str, sample: int) -> dict:
    return api("POST", "/api/v1/evaluation/wiki-cache-probe", {
        "chat_id": os.getenv("TOPIC3_CHAT_MODEL_ID", ""), "layout": layout, "sample": sample,
    })["data"]


def usage_cost(usage: dict) -> float:
    input_price = float(os.getenv("TOPIC3_CHAT_INPUT_PRICE_PER_MILLION_CNY", "0.2"))
    cached_price = float(os.getenv("TOPIC3_CHAT_CACHED_INPUT_PRICE_PER_MILLION_CNY", "0.04"))
    output_price = float(os.getenv("TOPIC3_CHAT_OUTPUT_PRICE_PER_MILLION_CNY", "0.8"))
    prompt = int(usage.get("prompt_tokens", 0))
    cached = int(usage.get("cache_read_tokens", 0)) if usage.get("cache_reported") else 0
    return ((prompt-cached)*input_price + cached*cached_price + int(usage.get("completion_tokens", 0))*output_price) / 1_000_000


def run_wiki_benchmark(output_dir: pathlib.Path, checkpoint=None) -> pathlib.Path:
    emit("[C] WIKI PROMPT CACHE BENCHMARK 0/8")
    sequence = [("legacy", 0, "warmup"), ("optimized", 0, "warmup")]
    for sample in range(1, 4):
        sequence.extend([("legacy", sample, "measure"), ("optimized", sample, "measure")])
    report = {"sequence": [], "runs": []}
    target = output_dir / "wiki-cache-benchmark.json"
    for index, (layout, sample, stage) in enumerate(sequence, 1):
        emit(f"[C] WIKI PROMPT CACHE BENCHMARK {index}/8 {layout} sample={sample}")
        row = wiki_probe(layout, sample)
        row["stage"] = stage
        row["estimated_cost_cny"] = usage_cost(row.get("usage", {}))
        if not row.get("output_valid") or not row.get("expected_found"):
            raise RuntimeError(f"wiki {layout} sample {sample} failed output quality validation")
        if layout == "optimized" and stage == "warmup" and row.get("usage", {}).get("prompt_tokens", 0) < 1024:
            raise RuntimeError("optimized Wiki prompt is below the 1024-token implicit-cache threshold")
        report["sequence"].append(f"{layout}:{sample}:{stage}")
        report["runs"].append(row)
        write_json(target, report)
        if checkpoint is not None:
            checkpoint(row)
    measured = [row for row in report["runs"] if row["stage"] == "measure"]
    report["summary"] = {}
    for layout in ("legacy", "optimized"):
        rows = [row for row in measured if row["layout"] == layout]
        reported = [row for row in rows if row["usage"].get("cache_reported")]
        report["summary"][layout] = {
            "calls": len(rows), "cache_reported_calls": len(reported),
            "cache_read_tokens": sum(row["usage"].get("cache_read_tokens", 0) for row in reported),
            "estimated_cost_cny": sum(row["estimated_cost_cny"] for row in rows),
            "mean_duration_ms": sum(row.get("duration_ms", 0) for row in rows) / len(rows),
        }
    optimized = report["summary"]["optimized"]
    report["conclusion"] = (
        "observed" if optimized["cache_read_tokens"] > 0 else
        "not_observed" if optimized["cache_reported_calls"] > 0 else "unknown"
    )
    write_json(target, report)
    return target


def run_negative_control(output_dir: pathlib.Path) -> pathlib.Path:
    emit("[D] REAL RETRIEVAL NEGATIVE CONTROL")
    target = output_dir / "negative-control.json"
    try:
        restart_app(True, True)
        result_path = run_evaluation(output_dir / "negative-evaluation")
        result = json.loads(result_path.read_text(encoding="utf-8"))
        rejected = False
        error = ""
        try:
            check_regression(DEFAULT_BASELINE, result_path, 0.03)
        except RuntimeError as exc:
            rejected = True
            error = str(exc)
        report = {
            "result": str(result_path.relative_to(ROOT)), "task": result.get("task", {}),
            "metric": result.get("metric", {}), "regression_rejected": rejected, "expected_error": error,
        }
        write_json(target, report)
        if not rejected:
            raise RuntimeError("real retrieval degradation unexpectedly passed regression check")
        return target
    finally:
        restart_app(True, False)


def batch_known_cost(cache_report: dict, wiki_report: dict) -> float:
    cache_cost = sum(row.get("usage", {}).get("estimated_cost_cny", 0) for row in cache_report.get("runs", []))
    wiki_cost = sum(row.get("estimated_cost_cny", 0) for row in wiki_report.get("runs", []))
    return float(cache_cost) + float(wiki_cost)


def write_acceptance_report(output_dir: pathlib.Path, manifest: dict) -> None:
    write_json(output_dir / "acceptance-report.json", manifest)
    checks = manifest.get("checks", {})
    lines = [
        "# 课题三本地正式验收报告", "", f"批次：`{manifest['batch_id']}`", "",
        f"状态：**{manifest['status']}**", "", "## 自动验收", "",
    ]
    for name, value in checks.items():
        lines.append(f"- {'通过' if value else '失败'}：{name}")
    lines.extend(["", "## 费用", "", f"- 本批次已知估算费用：{manifest.get('known_cost_cny', 0):.8f} 元", "- Embedding/ReRank 未上报 Token 时保持未知。", ""])
    (output_dir / "acceptance-report.md").write_text("\n".join(lines), encoding="utf-8")


def run_postcheck(output_dir: pathlib.Path, initial_hash: str) -> dict:
    emit("[E] AUTOMATIC POSTCHECK")
    restart_app(True, False)
    with urllib.request.urlopen(os.getenv("TOPIC3_BASE_URL", "http://localhost:8080").rstrip("/") + "/health", timeout=10) as response:
        healthy = response.status == 200
    snapshot = database_snapshot()
    result = {
        "service_healthy": healthy,
        "no_unfinished_evaluations": snapshot["unfinished_evaluations"] == 0,
        "baseline_unchanged": baseline_source_hash() == initial_hash,
        "no_result_secrets": len(scan_result_secrets()) == 0,
        "database_after": snapshot,
    }
    if not all(value for key, value in result.items() if key != "database_after"):
        raise RuntimeError(f"postcheck failed: {result}")
    write_json(output_dir / "postcheck.json", result)
    return result


def resumable_cache_batch() -> pathlib.Path | None:
    if not OUT.exists():
        return None
    candidates = sorted(OUT.glob("batch-*"), key=lambda path: path.stat().st_mtime, reverse=True)
    for candidate in candidates:
        manifest_path = candidate / "manifest.json"
        cache_path = candidate / "cache-benchmark.json"
        if not manifest_path.exists() or not cache_path.exists():
            continue
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        if manifest.get("status") != "failed" or "1024-token" not in manifest.get("error", ""):
            continue
        cache_report = json.loads(cache_path.read_text(encoding="utf-8"))
        validate_cache_benchmark(cache_report)
        return candidate
    return None


def run_all() -> pathlib.Path:
    global ACTIVE_LOG
    if os.getenv("TOPIC3_ALL_CONFIRM") != "YES":
        raise RuntimeError("set TOPIC3_ALL_CONFIRM=YES to authorize the formal paid batch")
    output_dir = resumable_cache_batch()
    resumed = output_dir is not None
    if output_dir is None:
        batch_id = "batch-" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
        output_dir = OUT / batch_id
        output_dir.mkdir(parents=True, exist_ok=False)
    else:
        batch_id = output_dir.name
    ACTIVE_LOG = output_dir / "run.log"
    started = utc_now()
    checkpoint_path = output_dir / "checkpoint.json"
    if resumed and checkpoint_path.exists():
        checkpoint_data = json.loads(checkpoint_path.read_text(encoding="utf-8"))
        checkpoint_data.setdefault("resume_history", []).append({"at": started, "runner_version": RUNNER_VERSION})
        checkpoint_data["status"] = "running"
        checkpoint_data.pop("error", None)
        checkpoint_data.pop("failed_at", None)
    else:
        checkpoint_data = {"batch_id": batch_id, "status": "running", "started_at": started, "completed": []}

    def checkpoint(stage: str, detail=None) -> None:
        checkpoint_data["completed"].append({"stage": stage, "at": utc_now(), "detail": detail})
        write_json(output_dir / "checkpoint.json", checkpoint_data)

    old_manifest = {}
    if resumed and (output_dir / "manifest.json").exists():
        old_manifest = json.loads((output_dir / "manifest.json").read_text(encoding="utf-8"))
    manifest = {
        "batch_id": batch_id, "runner_version": RUNNER_VERSION, "status": "running",
        "started_at": old_manifest.get("started_at", started), "resumed_at": started if resumed else None,
        "previous_failure": old_manifest.get("error", "") if resumed else "",
    }
    write_json(output_dir / "manifest.json", manifest)
    initial_hash = ""
    try:
        emit(f"TOPIC3 FORMAL LOCAL BATCH {batch_id} runner={RUNNER_VERSION} resumed={str(resumed).lower()}")
        emit("[BUILD] rebuild backend with the current source before any paid call")
        run_logged(["docker", "compose", "build", "app"])
        restart_app(True, False)
        preflight_dir = output_dir / "resume-preflight" if resumed else output_dir
        preflight = run_preflight(preflight_dir)
        initial_hash = preflight["baseline_source_sha256"]
        checkpoint("resume-preflight" if resumed else "preflight")

        known_cost = 0.0

        def cache_checkpoint(row: dict) -> None:
            nonlocal known_cost
            validate_cache_row(row)
            known_cost += float(row.get("usage", {}).get("estimated_cost_cny", 0))
            if known_cost >= float(os.getenv("TOPIC3_BATCH_COST_LIMIT_CNY", "5")):
                raise RuntimeError("known batch cost reached the configured limit")
            checkpoint(f"cache:{row['phase']}:{row['repetition']}")

        cache_path = output_dir / "cache-benchmark.json"
        if resumed:
            cache_report = json.loads(cache_path.read_text(encoding="utf-8"))
            validate_cache_benchmark(cache_report)
            known_cost = sum(float(row.get("usage", {}).get("estimated_cost_cny", 0)) for row in cache_report["runs"])
            emit("[B] REUSING VERIFIED 9/9 EMBEDDING CACHE RUNS; NO PAID REPEAT")
            checkpoint("cache-benchmark-reused")
            partial_wiki = output_dir / "wiki-cache-benchmark.json"
            if partial_wiki.exists():
                preserved = output_dir / "wiki-cache-benchmark.pre-threshold-failure.json"
                if not preserved.exists():
                    shutil.copy2(partial_wiki, preserved)
        else:
            cache_path = run_cache_benchmark(output_dir, cache_checkpoint)
            checkpoint("cache-benchmark")

        def wiki_checkpoint(row: dict) -> None:
            nonlocal known_cost
            known_cost += float(row.get("estimated_cost_cny", 0))
            if known_cost >= float(os.getenv("TOPIC3_BATCH_COST_LIMIT_CNY", "5")):
                raise RuntimeError("known batch cost reached the configured limit")
            checkpoint(f"wiki:{row['layout']}:{row['sample']}:{row['stage']}")

        wiki_path = run_wiki_benchmark(output_dir, wiki_checkpoint)
        checkpoint("wiki-benchmark")
        negative_path = run_negative_control(output_dir)
        checkpoint("negative-control")
        postcheck = run_postcheck(output_dir, initial_hash)
        checkpoint("postcheck")

        cache_report = json.loads(cache_path.read_text(encoding="utf-8"))
        wiki_report = json.loads(wiki_path.read_text(encoding="utf-8"))
        negative_report = json.loads(negative_path.read_text(encoding="utf-8"))
        checks = {
            "缓存实验完成9轮": len(cache_report.get("runs", [])) == 9,
            "暖缓存减少真实Embedding调用": cache_report.get("summary", {}).get("warm_reduced_real_calls") is True,
            "Wiki真实调用完成8次": len(wiki_report.get("runs", [])) == 8,
            "Wiki输出质量全部通过": all(row.get("output_valid") and row.get("expected_found") for row in wiki_report.get("runs", [])),
            "真实退化被回归门禁拒绝": negative_report.get("regression_rejected") is True,
            "正式基线保持不变": postcheck["baseline_unchanged"],
            "结果目录未发现API Key": postcheck["no_result_secrets"],
            "后端最终健康": postcheck["service_healthy"],
        }
        if not all(checks.values()):
            raise RuntimeError(f"acceptance checks failed: {checks}")
        manifest.update({
            "status": "ALL_COMPLETE", "completed_at": utc_now(), "checks": checks,
            "known_cost_cny": batch_known_cost(cache_report, wiki_report),
            "artifacts": {
                "cache": str(cache_path.relative_to(ROOT)), "wiki": str(wiki_path.relative_to(ROOT)),
                "negative": str(negative_path.relative_to(ROOT)),
            },
        })
        checkpoint_data["status"] = "ALL_COMPLETE"
        checkpoint_data["completed_at"] = manifest["completed_at"]
        write_json(output_dir / "checkpoint.json", checkpoint_data)
        write_json(output_dir / "manifest.json", manifest)
        write_acceptance_report(output_dir, manifest)
        emit("ALL_COMPLETE")
        return output_dir
    except Exception as exc:
        checkpoint_data["status"] = "failed"
        checkpoint_data["failed_at"] = utc_now()
        checkpoint_data["error"] = str(exc)
        manifest.update({"status": "failed", "failed_at": checkpoint_data["failed_at"], "error": str(exc)})
        write_json(output_dir / "checkpoint.json", checkpoint_data)
        write_json(output_dir / "manifest.json", manifest)
        emit(f"FAILED: {exc}")
        raise
    finally:
        try:
            restart_app(True, False)
        except Exception as restore_error:
            emit(f"RESTORE_FAILED: {restore_error}")
        ACTIVE_LOG = None


def metrics(path: pathlib.Path) -> dict[str, float]:
    document = json.loads(path.read_text(encoding="utf-8"))
    metric = document.get("metric", document)
    retrieval = metric.get("retrieval_metrics", {})
    generation = metric.get("generation_metrics", {})
    return {
        "recall": float(retrieval["recall"]),
        "mrr": float(retrieval["mrr"]),
        "ndcg3": float(retrieval.get("ndcg3", 0)),
        "rougel": float(generation.get("rougel", 0)),
    }


def check_regression(baseline: pathlib.Path, current: pathlib.Path, threshold: float) -> None:
    if not baseline.exists():
        raise RuntimeError(f"baseline is missing: {baseline}; a candidate run must never create or overwrite it")
    before, after = metrics(baseline), metrics(current)
    failed = []
    for name in ("recall", "mrr"):
        drop = before[name] - after[name]
        print(f"{name}: baseline={before[name]:.6f} current={after[name]:.6f} absolute_drop={drop:.6f}")
        if drop > threshold:
            failed.append(f"{name} dropped by {drop:.6f} (> {threshold:.6f})")
    if failed:
        raise RuntimeError("retrieval regression: " + "; ".join(failed))


def command_up() -> None:
    env_file = ROOT / ".env"
    if not env_file.exists():
        env_file.write_text((ROOT / ".env.example").read_text(encoding="utf-8"), encoding="utf-8")
        print("created .env from .env.example; review local secrets before provider calls")
    frontend = ROOT / "frontend"
    if not (frontend / "node_modules").exists():
        subprocess.run(["npm", "ci", "--cache", ".npm-cache"], cwd=frontend, check=True)
    subprocess.run(["npm", "run", "build"], cwd=frontend, check=True)
    subprocess.run(["docker", "compose", "up", "-d", "--build"], cwd=ROOT, check=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["up", "eval", "check", "cache-bench", "preflight", "wiki-bench", "postcheck", "all"])
    parser.add_argument("--baseline", type=pathlib.Path, default=DEFAULT_BASELINE)
    parser.add_argument("--current", type=pathlib.Path)
    parser.add_argument("--threshold", type=float, default=0.03)
    args = parser.parse_args()
    try:
        if args.action == "up":
            command_up()
        elif args.action == "eval":
            run_evaluation()
        elif args.action == "check":
            if not args.current:
                candidates = sorted(OUT.glob("*.json"), key=lambda p: p.stat().st_mtime)
                if not candidates:
                    raise RuntimeError("no evaluation result found; pass --current or run topic3-eval")
                args.current = candidates[-1]
            check_regression(args.baseline, args.current, args.threshold)
        elif args.action == "cache-bench":
            print(run_cache_benchmark())
        elif args.action == "preflight":
            target = OUT / ("preflight-" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
            print(run_preflight(target))
        elif args.action == "wiki-bench":
            target = OUT / ("wiki-benchmark-" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
            print(run_wiki_benchmark(target))
        elif args.action == "postcheck":
            target = OUT / ("postcheck-" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
            print(run_postcheck(target, baseline_source_hash()))
        else:
            print(run_all())
        return 0
    except (RuntimeError, TimeoutError, urllib.error.URLError, subprocess.CalledProcessError) as exc:
        print(f"topic3: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
