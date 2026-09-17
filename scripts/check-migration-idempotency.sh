#!/usr/bin/env bash
# Fail if versioned Postgres migrations introduce non-idempotent CREATE TABLE.
# Bare CREATE TABLE breaks schema_migrations force-back repair (see 000093).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="${ROOT}/migrations/versioned"

python - "${DIR}" <<'PY'
import re, sys
from pathlib import Path

root = Path(sys.argv[1])
bad = []
pat = re.compile(r"(?im)^[ \t]*CREATE\s+TABLE\s+(?!IF\s+NOT\s+EXISTS\b)(\S+)")
for path in sorted(root.glob("*.up.sql")):
    text = path.read_text(encoding="utf-8")
    for match in pat.finditer(text):
        line = text[: match.start()].count("\n") + 1
        bad.append(f"{path.name}:{line}: CREATE TABLE {match.group(1)}")

if bad:
    print("Non-idempotent CREATE TABLE found:")
    print("\n".join(bad))
    print("\nFix: use CREATE TABLE IF NOT EXISTS.")
    sys.exit(1)
print(f"OK: {len(list(root.glob('*.up.sql')))} versioned up migrations OK")
PY
