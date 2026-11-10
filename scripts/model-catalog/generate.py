#!/usr/bin/env python3
"""Reproducibly merge pinned model metadata and reviewed protocol overrides.

Network updates are intentionally separate: model_catalog_diff.py compares
models.dev with the generated catalog, then maintainers review changes to the
pinned source. Upstream metadata cannot silently replace wire compatibility.
"""
import argparse
import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
DATA = ROOT / "internal/models/catalog/data"


def load(path):
    def unique(pairs):
        obj = {}
        for key, value in pairs:
            if key in obj:
                raise ValueError(f"{path}: duplicate JSON key {key}")
            obj[key] = value
        return obj
    return json.loads(path.read_text(), object_pairs_hook=unique)


def identity(entry):
    kind = entry.get("type", "KnowledgeQA")
    if not entry.get("id") and not entry.get("match"):
        raise ValueError(f"model needs id or match: {entry}")
    return kind, entry.get("id", ""), entry.get("match", "")


def generate():
    sources = load(HERE / "sources.json")
    result = {"version": 1, "providers": {}}
    for source in sources["metadata"]:
        data = load(HERE / source["path"])
        if data["version"] != 1:
            raise ValueError("unsupported metadata version")
        for provider, entries in data["providers"].items():
            if provider in result["providers"]:
                raise ValueError(f"duplicate provider {provider}")
            result["providers"][provider] = copy.deepcopy(entries)
    overrides = load(DATA / "overrides.json")
    if overrides["version"] != 1:
        raise ValueError("unsupported overrides version")
    for provider, patches in overrides["providers"].items():
        if provider not in result["providers"]:
            raise ValueError(f"override has unknown provider {provider}")
        entries = result["providers"][provider]
        index = {identity(entry): entry for entry in entries}
        seen = set()
        for patch in patches:
            key = identity(patch)
            if key in seen:
                raise ValueError(f"duplicate override {provider}/{key}")
            seen.add(key)
            # Unknown entries are explicit additions, suitable for models that
            # have no upstream metadata yet. Existing entries patch field-wise.
            if key not in index:
                entries.append(copy.deepcopy(patch))
                index[key] = entries[-1]
            else:
                index[key].update(copy.deepcopy(patch))
    for provider, entries in result["providers"].items():
        seen = set()
        for entry in entries:
            key = identity(entry)
            if key in seen:
                raise ValueError(f"duplicate model {provider}/{key}")
            seen.add(key)
            for field in ("context_window", "max_output_tokens", "dimension"):
                if field in entry and (type(entry[field]) is not int or entry[field] < 0):
                    raise ValueError(f"invalid {field} in {provider}/{key}")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if generated data is stale")
    args = parser.parse_args()
    result = generate()
    rendered = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    target = DATA / "models.generated.json"
    if args.check:
        if not target.exists() or target.read_text() != rendered:
            parser.exit(1, "model catalog is stale; run scripts/model-catalog/generate.py\n")
    else:
        target.write_text(rendered)
    print(f"Verified {len(result['providers'])} providers and "
          f"{sum(map(len, result['providers'].values()))} catalog rules")


if __name__ == "__main__":
    main()
