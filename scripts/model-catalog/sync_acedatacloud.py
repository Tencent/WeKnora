#!/usr/bin/env python3
"""Refresh Ace's reviewed seed from its public typed model contracts.

This is an explicit maintenance step. Normal builds use the pinned seed and
generate.py; they never require a network request or a customer credential.
"""
import argparse
import copy
import json
from pathlib import Path
from urllib.request import Request, urlopen

HERE = Path(__file__).resolve().parent
SEED = HERE.parents[1] / "internal/models/catalog/data/seed.json"
SOURCE = "https://platform.acedata.cloud/api/v1/models/"
PROTOCOLS = (
    ("chat_completions", "/v1/chat/completions", "openai-completions"),
    ("messages", "/v1/messages", "anthropic-messages"),
    ("responses", "/v1/responses", "openai-responses"),
)


def project(payload):
    rows = payload.get("data", payload.get("items"))
    if not isinstance(rows, list) or not rows:
        raise ValueError("The public model catalog must contain model records")
    entries = []
    seen = set()
    for row in rows:
        if not isinstance(row, dict) or row.get("type", row.get("modality")) != "chat":
            continue
        model = row.get("id")
        if not isinstance(model, str) or not model.strip() or model in seen:
            raise ValueError("Missing or duplicate chat model ID")
        seen.add(model)
        endpoints = row.get("endpoints")
        if not isinstance(endpoints, list):
            raise ValueError(f"Missing published protocol metadata for {model}")
        protocol = None
        for kind, path, api in PROTOCOLS:
            base = "https://api.acedata.cloud" + ("" if kind == "messages" else "/v1")
            if any(isinstance(e, dict) and e.get("protocol") == kind and
                   e.get("path") == path and e.get("base_url") == base and
                   e.get("auth") == "bearer" for e in endpoints):
                protocol = api
                break
        if protocol is None:
            raise ValueError(f"No supported published chat protocol for {model}")
        capabilities = row.get("capabilities", [])
        if not isinstance(capabilities, list):
            raise ValueError(f"Invalid capability metadata for {model}")
        entries.append({
            "id": model,
            "name": row.get("label") or row.get("name") or model,
            "api": protocol,
            "reasoning": "reasoning" in capabilities,
            "input": ["text", "image"] if "vision" in capabilities else ["text"],
            "source": SOURCE,
        })
    if not entries:
        raise ValueError("The public model catalog contains no chat models")
    return entries


def update(seed, entries):
    result = copy.deepcopy(seed)
    current = {entry["id"] for entry in entries}
    historical = [
        {**entry, "deprecated": True}
        for entry in seed["providers"].get("acedatacloud", [])
        if entry.get("id") and entry["id"] not in current
    ]
    # Existing saved models still resolve; deprecated entries leave the picker.
    result["providers"]["acedatacloud"] = entries + historical
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog", type=Path, help="read a previously captured public response")
    parser.add_argument("--seed", type=Path, default=SEED)
    parser.add_argument("--check", action="store_true", help="report drift without writing")
    args = parser.parse_args()
    if args.catalog:
        payload = json.loads(args.catalog.read_text())
    else:
        request = Request(SOURCE, headers={"Accept": "application/json", "User-Agent": "WeKnora-model-catalog"})
        with urlopen(request, timeout=30) as response:
            if response.url != SOURCE:
                raise ValueError("Unexpected model catalog redirect")
            payload = json.load(response)
    entries = project(payload)
    result = update(json.loads(args.seed.read_text()), entries)
    rendered = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.check:
        if args.seed.read_text() != rendered:
            parser.exit(1, "Ace catalog differs; refresh and review the generated changes\n")
    else:
        args.seed.write_text(rendered)
    print(f"Verified {len(entries)} Ace chat catalog entries; no host limits or prices inferred")


if __name__ == "__main__":
    main()
