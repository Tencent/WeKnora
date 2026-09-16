#!/usr/bin/env python3
"""Offline OpenRouter price comparison for fixed live-cache evidence; no network."""
import argparse
from decimal import Decimal
import hashlib
import json
from pathlib import Path


def component_cost(step, pricing):
    """Return known components; never infer missing cache writes or embedding prices."""
    if step.get("provider_requests") == 0 and step.get("status") == "success":
        return {"subtotal_usd": "0", "total_usd": "0", "missing": []}
    if step.get("operation") != "chat":
        return {"subtotal_usd": None, "total_usd": None, "missing": ["matching_embedding_quote"]}
    names = ("prompt_tokens", "output_tokens", "cache_read_tokens")
    values = [step.get(name) for name in names]
    if any(type(v) is not int or v < 0 for v in values):
        return {"subtotal_usd": None, "total_usd": None, "missing": ["complete_usage"]}
    prompt, output, read = values
    if read > prompt:
        raise ValueError("cache reads exceed prompt tokens")
    rates = dict(pricing)
    for override in sorted(pricing.get("overrides", []), key=lambda x: x.get("min_prompt_tokens", 0)):
        if set(override) - {"min_prompt_tokens", "prompt", "completion", "input_cache_read", "input_cache_write"}:
            raise ValueError("unsupported pricing condition")
        if "min_prompt_tokens" not in override:
            raise ValueError("missing pricing condition")
        if prompt >= override["min_prompt_tokens"]:
            rates.update(override)
    missing = []
    subtotal = Decimal(0)
    for count, field in ((prompt - read, "prompt"), (output, "completion"), (read, "input_cache_read")):
        if count == 0:
            continue
        if field not in rates:
            missing.append(field)
            continue
        rate = Decimal(rates[field])
        if not rate.is_finite() or rate < 0:
            raise ValueError("invalid price")
        subtotal += count * rate
    # DashScope implicit caching does not provide OpenRouter cache-write counters.
    # Preserve a partial estimate instead of assuming an unobserved component is free.
    missing.append("openrouter_cache_write_usage")
    return {"subtotal_usd": str(subtotal), "total_usd": None, "missing": missing}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, type=Path)
    parser.add_argument("--catalog-dir", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    summary = json.loads((args.catalog_dir / "catalog-summary.json").read_text(encoding="utf-8"))
    chat_source = next(x for x in summary["sources"] if x["kind"] == "chat")
    payload = (args.catalog_dir / chat_source["file"]).read_bytes()
    if hashlib.sha256(payload).hexdigest() != chat_source["sha256"]:
        raise ValueError("catalog digest mismatch")
    embedding_source = next(x for x in summary["sources"] if x["kind"] == "embedding")
    embedding_payload = (args.catalog_dir / embedding_source["file"]).read_bytes()
    if hashlib.sha256(embedding_payload).hexdigest() != embedding_source["sha256"]:
        raise ValueError("embedding catalog digest mismatch")
    if any(x["id"].split("/")[-1] == "qwen3.7-text-embedding"
           for x in json.loads(embedding_payload)["data"]):
        raise ValueError("embedding quote now exists; review mapping before estimating")
    model = next(x for x in json.loads(payload)["data"] if x["id"] == "qwen/qwen3.7-flash")
    plan = json.loads((args.run / "plan.json").read_text(encoding="utf-8"))
    result = json.loads((args.run / "result.json").read_text(encoding="utf-8"))
    if (plan["endpoint"] != "https://dashscope.aliyuncs.com/compatible-mode/v1"
            or plan["chat_identity"]["upstream_name"] != "qwen3.7-flash"
            or plan["embedding_identity"]["upstream_name"] != "qwen3.7-text-embedding"
            or result["plan_sha256"] != plan["sha256"]):
        raise ValueError("unsupported model identity or plan mismatch")
    rows = [{"step_id": s["id"], "operation": s["operation"],
             "actual_cost_cny_microunits": s.get("cost_microunits"),
             **component_cost(s, model["pricing"])} for s in result["steps"]]
    report = {"label": "OpenRouter 口径费用", "kind": "simulated_price_comparison",
              "currency": "USD", "actual_provider": "DashScope", "mode": result["mode"],
              "plan_sha256": plan["sha256"], "quote_checked_at": summary["checked_at_utc"],
              "quote_sha256": chat_source["sha256"], "model": model["id"],
              "input_output_read_estimate_usd": str(sum((Decimal(r["subtotal_usd"]) for r in rows
                                                       if r["subtotal_usd"] is not None), Decimal(0))),
              "total_usd": None, "steps": rows,
              "limitations": ["Embedding model has no exact OpenRouter quote in the frozen catalog.",
                              "Non-read input uses the ordinary prompt rate; cache-write adjustments are unobserved.",
                              "The input/output/read estimate is not a complete bill."]}
    with args.out.open("x", encoding="utf-8") as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write("\n")


if __name__ == "__main__":
    main()
