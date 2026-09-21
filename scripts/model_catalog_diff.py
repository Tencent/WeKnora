#!/usr/bin/env python3
"""Compare the built-in vendor catalogs against models.dev and print a diff.

Development-time helper, never run at runtime: it reports numeric facts
(context window, max output, cost, new/retired ids) so a maintainer can update
internal/models/vendors/<id>/models.json by hand. Behavioural facts (compat,
thinking format) are intentionally out of scope — models.dev does not carry
them and they must come from vendor documentation.

Usage:
    scripts/model_catalog_diff.py                # fetch https://models.dev/api.json
    scripts/model_catalog_diff.py --api api.json # use a downloaded copy
    scripts/model_catalog_diff.py --vendor deepseek
    scripts/model_catalog_diff.py --exit-code      # non-zero when anything differs

Findings are advisory: `+` is a model upstream has and we do not, `~` is a
numeric difference, `?` is an entry upstream does not list (often correct —
vendor-specific aliases and China-only ids are missing from models.dev).
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VENDORS_DIR = ROOT / "internal" / "models" / "vendors"

# WeKnora vendor id -> models.dev provider id.
PROVIDER_MAP = {
    "openai": "openai",
    "anthropic": "anthropic",
    "gemini": "google",
    "deepseek": "deepseek",
    "zhipu": "zhipuai",
    "aliyun": "alibaba-cn",
    "volcengine": "volcengine",
    "moonshot": "moonshotai-cn",
    "minimax": "minimax-cn",
    "mimo": "xiaomi",
    "siliconflow": "siliconflow-cn",
    "modelscope": "modelscope",
    "qiniu": "qiniu-ai",
    "longcat": "longcat",
    "novita": "novita-ai",
    "nvidia": "nvidia",
    "openrouter": "openrouter",
    "requesty": "requesty",
}


MODELS_DEV_URL = "https://models.dev/api.json"


def load_api(path):
    """Load the models.dev catalog from a file, or fetch it once.

    models.dev rejects the default urllib user agent, so send a real one.
    When the network is unavailable (CI, offline dev box), download the file
    by hand and pass --api, or set MODELS_DEV_API to its path.
    """
    path = path or os.environ.get("MODELS_DEV_API")
    if path:
        return json.loads(Path(path).read_text())
    request = urllib.request.Request(
        MODELS_DEV_URL,
        headers={"User-Agent": "WeKnora-model-catalog-diff/1.0 (+https://github.com/Tencent/WeKnora)"},
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as resp:
            return json.load(resp)
    except urllib.error.URLError as err:
        raise SystemExit(
            f"failed to fetch {MODELS_DEV_URL}: {err}\n"
            "Download it manually and re-run with --api <file>, or set MODELS_DEV_API."
        ) from err


def load_catalog(vendor):
    file = VENDORS_DIR / vendor / "models.json"
    if not file.exists():
        return {}
    data = json.loads(file.read_text())
    return {m["id"]: m for m in data.get("models", []) if m.get("id")}


def compare(vendor, upstream, ours):
    lines = []
    up_models = upstream.get("models", {})
    for mid, spec in sorted(up_models.items()):
        mods = spec.get("modalities", {})
        if "text" not in mods.get("output", ["text"]):
            continue  # image / audio generation, out of scope
        entry = ours.get(mid)
        limit = spec.get("limit", {})
        cost = spec.get("cost", {})
        if entry is None:
            lines.append(
                f"  + {mid}  (new upstream: ctx={limit.get('context')} out={limit.get('output')} "
                f"reasoning={spec.get('reasoning')} released={spec.get('release_date')})"
            )
            continue
        diffs = []
        if limit.get("context") and entry.get("context_window") != limit.get("context"):
            diffs.append(f"context_window {entry.get('context_window')} -> {limit.get('context')}")
        if limit.get("output") and entry.get("max_output_tokens") != limit.get("output"):
            diffs.append(f"max_output_tokens {entry.get('max_output_tokens')} -> {limit.get('output')}")
        if bool(entry.get("reasoning")) != bool(spec.get("reasoning")):
            diffs.append(f"reasoning {entry.get('reasoning')} -> {spec.get('reasoning')}")
        ours_cost = entry.get("cost") or {}
        for key, up_key in (("input", "input"), ("output", "output"), ("cache_read", "cache_read")):
            if cost.get(up_key) is not None and ours_cost.get(key) != cost.get(up_key):
                diffs.append(f"cost.{key} {ours_cost.get(key)} -> {cost.get(up_key)}")
        if diffs:
            lines.append(f"  ~ {mid}: " + "; ".join(diffs))
    for mid in sorted(ours):
        if mid not in up_models:
            lines.append(f"  ? {mid}  (not in models.dev; keep if the vendor still documents it)")
    return lines


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--api", help="path to a downloaded models.dev api.json")
    parser.add_argument("--vendor", help="limit to one WeKnora vendor id")
    parser.add_argument(
        "--exit-code", action="store_true",
        help="exit 1 when there are findings (for a scheduled job); default always exits 0",
    )
    args = parser.parse_args()

    api = load_api(args.api)
    vendors = [args.vendor] if args.vendor else sorted(PROVIDER_MAP)
    exit_code = 0
    for vendor in vendors:
        upstream_id = PROVIDER_MAP.get(vendor)
        if not upstream_id:
            print(f"== {vendor}: no models.dev mapping", file=sys.stderr)
            continue
        upstream = api.get(upstream_id)
        if upstream is None:
            print(f"== {vendor}: models.dev provider {upstream_id!r} missing", file=sys.stderr)
            continue
        lines = compare(vendor, upstream, load_catalog(vendor))
        print(f"== {vendor} (models.dev: {upstream_id}) — {len(lines)} finding(s)")
        for line in lines:
            print(line)
        if lines:
            exit_code = 1
    return exit_code if args.exit_code else 0


if __name__ == "__main__":
    sys.exit(main())
