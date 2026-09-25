#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/frontend"

echo "[verify_frontend_pr] npm test"
npm test

echo "[verify_frontend_pr] npm run type-check"
npm run type-check

echo "[verify_frontend_pr] npm run build"
npm run build

echo "[verify_frontend_pr] OK"
