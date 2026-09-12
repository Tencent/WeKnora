#!/usr/bin/env python3
"""Check product version surfaces using only the Python standard library."""
import argparse
import json
from pathlib import Path
import re
import sys


def check(root):
    def read(name):
        return (root / name).read_text(encoding="utf-8-sig")

    version = read("VERSION").strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("VERSION must contain one semantic product version")
    package = json.loads(read("frontend/package.json"))
    lock = json.loads(read("frontend/package-lock.json"))
    desktop = json.loads(read("cmd/desktop/wails.json"))
    chart = re.search(r'^appVersion:\s*[\"\']?([^\s\"\']+)', read("helm/Chart.yaml"), re.M)
    changelog = re.search(r"^## \[([^]]+)\]", read("CHANGELOG.md"), re.M)
    surfaces = {
        "frontend/package.json": package["version"],
        "frontend/package-lock.json": lock["version"],
        "frontend/package-lock.json packages root": lock["packages"][""]["version"],
        "cmd/desktop/wails.json": desktop["info"]["productVersion"],
        "helm/Chart.yaml": chart.group(1).removeprefix("v") if chart else None,
        "CHANGELOG.md": changelog.group(1) if changelog else None,
    }
    errors = [f"{name}: {actual!r}; expected {version!r}" for name, actual in surfaces.items() if actual != version]
    needles = {name: f"version-{version}-" for name in ("README.md", "README_CN.md", "README_JA.md", "README_KO.md")}
    needles.update({
        "website-docs/06-development/01-dev-guide.md": f"版本号 `{version}`",
        "website-docs/05-clients/01-frontend.md": f"（版本 {version}）",
        "website-docs/01-getting-started/02-installation.md": f"（当前为 v{version}）",
    })
    errors.extend(f"{name}: missing {needle!r}" for name, needle in needles.items() if needle not in read(name))
    return {"version": version, "checked_surfaces": 1 + len(surfaces) + len(needles), "errors": errors}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    try:
        result = check(args.root)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(json.dumps({"errors": [str(error)]}, ensure_ascii=False))
        sys.exit(1)
    print(json.dumps(result, ensure_ascii=False))
    sys.exit(bool(result["errors"]))
