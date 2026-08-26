#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

BASE_URL="${CUSTOM_BACKEND_BASE_URL:-http://127.0.0.1:${CUSTOM_SERVER_PORT:-8090}}"
HTTP_TIMEOUT="${CUSTOM_BASELINE_HTTP_TIMEOUT:-5}"
VIDEO_ID="${VIDEO_ID:-}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/video-content-baseline.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
  printf 'baseline: FAIL: %s\n' "$1" >&2
  exit 1
}

check_http_json() {
  local name="$1"
  local path="$2"
  local headers="$TMP_DIR/${name}.headers"
  local body="$TMP_DIR/${name}.json"
  local status

  curl --http1.1 --max-time "$HTTP_TIMEOUT" -sS \
    -D "$headers" -o "$body" "$BASE_URL$path" \
    || fail "$name request failed: $BASE_URL$path"

  status="$(awk 'NR == 1 { print $2 }' "$headers")"
  [[ "$status" =~ ^2[0-9][0-9]$ ]] || fail "$name returned HTTP $status"

  if ! rg -qi '^(content-length:|transfer-encoding:.*chunked)' "$headers"; then
    fail "$name has no explicit HTTP response framing"
  fi

  node -e 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"))' "$body" \
    || fail "$name returned invalid JSON"

  printf 'http %-8s status=%s bytes=%s framing=ok\n' "$name" "$status" "$(wc -c < "$body" | tr -d ' ')"
}

check_http_json ping /api/custom/ping
check_http_json videos /api/custom/videos

if ! command -v psql >/dev/null 2>&1; then
  fail 'psql is required for the job timeline check'
fi

if [[ "${CUSTOM_DB_DRIVER:-postgres}" == "sqlite" ]]; then
  fail 'job timeline check currently requires CUSTOM_DB_DRIVER=postgres'
fi

export PGHOST="${PGHOST:-${CUSTOM_DB_HOST:-localhost}}"
export PGPORT="${PGPORT:-${CUSTOM_DB_PORT:-5432}}"
export PGUSER="${PGUSER:-${CUSTOM_DB_USER:-postgres}}"
export PGPASSWORD="${PGPASSWORD:-${CUSTOM_DB_PASSWORD:-}}"
export PGDATABASE="${PGDATABASE:-${CUSTOM_DB_NAME:-vidsage}}"

if [[ -z "$VIDEO_ID" ]]; then
  VIDEO_ID="$(psql -AtX -v ON_ERROR_STOP=1 -c \
    "SELECT id FROM videos WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT 1")" \
    || fail 'could not select a video from the custom database'
fi
[[ -n "$VIDEO_ID" ]] || fail 'no video found; set VIDEO_ID to a READY video'

check_http_json detail "/api/custom/videos/$VIDEO_ID"

printf '%s\n' 'video stage snapshot:'
psql -X -v ON_ERROR_STOP=1 -v video_id="$VIDEO_ID" -F $'\t' -P footer=off -c \
  "SELECT id,
          status,
          CASE WHEN file_url IS NULL OR file_url = '' THEN 'no' ELSE 'yes' END AS file_url_present,
          CASE WHEN subtitle_file_url IS NULL OR subtitle_file_url = '' THEN 'no' ELSE 'yes' END AS subtitle_url_present,
          CASE WHEN transcript_knowledge_id IS NULL OR transcript_knowledge_id = '' THEN 'no' ELSE 'yes' END AS transcript_knowledge_id_present,
          CASE WHEN knowledge_base_wiki_page_id IS NULL OR knowledge_base_wiki_page_id = '' THEN 'no' ELSE 'yes' END AS graph_page_present,
          CASE WHEN outline_wiki_page_id IS NULL OR outline_wiki_page_id = '' THEN 'no' ELSE 'yes' END AS outline_page_present,
          CASE WHEN overview_wiki_page_id IS NULL OR overview_wiki_page_id = '' THEN 'no' ELSE 'yes' END AS overview_page_present,
          CASE WHEN summary_wiki_page_id IS NULL OR summary_wiki_page_id = '' THEN 'no' ELSE 'yes' END AS summary_page_present,
          CASE WHEN transcript_page_wiki_page_id IS NULL OR transcript_page_wiki_page_id = '' THEN 'no' ELSE 'yes' END AS transcript_page_present
     FROM videos
    WHERE id = :'video_id' AND deleted_at IS NULL"

printf '%s\n' 'job timeline:'
psql -X -v ON_ERROR_STOP=1 -v video_id="$VIDEO_ID" -F $'\t' -P footer=off -c \
  "SELECT job_type,
          status,
          attempt_count,
          CASE WHEN external_task_id IS NULL OR external_task_id = '' THEN 'no' ELSE 'yes' END AS external_task_present,
          CASE WHEN result_payload IS NULL OR result_payload = '' THEN 'no' ELSE 'yes' END AS result_payload_present,
          COALESCE(error_code, ''),
          CASE WHEN error_message IS NULL OR error_message = '' THEN 'no' ELSE 'yes' END AS error_present,
          COALESCE(to_char(started_at, 'YYYY-MM-DD HH24:MI:SSOF'), ''),
          COALESCE(to_char(completed_at, 'YYYY-MM-DD HH24:MI:SSOF'), '')
     FROM video_processing_jobs
    WHERE video_id = :'video_id'
    ORDER BY created_at ASC"

printf 'baseline: PASS: video_id=%s\n' "$VIDEO_ID"
