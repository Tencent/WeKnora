#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REPORT_ROOT="$PROJECT_ROOT/../output/acceptance/training-two-stage-orchestration/stage-06"
BACKEND_CONTAINER="${LOCAL_ACCEPTANCE_BACKEND_CONTAINER:-vidsage-custom-backend}"
DATABASE_CONTAINER="${LOCAL_ACCEPTANCE_DATABASE_CONTAINER:-WeKnora-postgres}"
FRONTEND_CONTAINER="${LOCAL_ACCEPTANCE_FRONTEND_CONTAINER:-WeKnora-frontend}"
PREVIEW_BINARY="${LOCAL_ACCEPTANCE_PREVIEW_BINARY:-/app/training-orchestration-preview}"
FIXED_PAGE_URL="${LOCAL_ACCEPTANCE_URL:-http://127.0.0.1/platform/videos}"
PING_URL="${LOCAL_ACCEPTANCE_PING_URL:-http://127.0.0.1/api/custom/ping}"

usage() {
    cat <<'EOF'
执行一次培训两阶段编排隔离真实生成，并保存脱敏的前置、生成与后置报告。

用法:
  ./scripts/run-training-orchestration-stability-round.sh <轮次>

轮次可使用 01 至 30。脚本不会调用正式生成接口，也不会保存模型正文。
EOF
}

die() {
    printf '[ERROR] %s\n' "$1" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "未找到命令: $1"
}

container_health() {
    docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' "$BACKEND_CONTAINER" 2>/dev/null || true
}

http_code() {
    curl -sS --max-time 10 -o /dev/null -w '%{http_code}' "$1" 2>/dev/null || printf '000'
}

model_config_ready() {
    docker exec "$BACKEND_CONTAINER" sh -lc '
        test -n "$CUSTOM_LLM_MODEL" &&
        test -n "$CUSTOM_LLM_BASE_URL" &&
        test -n "$CUSTOM_LLM_API_KEY"
    ' >/dev/null 2>&1
}

mps_config_ready() {
    docker exec "$BACKEND_CONTAINER" sh -lc '
        if [ "$CUSTOM_TRANSCRIPTION_PROVIDER" != "tencent_mps" ]; then
            exit 0
        fi
        test -n "$TENCENTCLOUD_SECRET_ID" &&
        test -n "$TENCENTCLOUD_SECRET_KEY" &&
        test -n "$TENCENTCLOUD_MPS_INPUT_BUCKET" &&
        test -n "$TENCENTCLOUD_MPS_OUTPUT_BUCKET"
    ' >/dev/null 2>&1
}

wiki_count() {
    docker exec "$BACKEND_CONTAINER" sh -lc '
        wget -qO- \
          --header="X-API-Key: $WEKNORA_API_KEY" \
          "http://app:8080/api/v1/knowledgebase/$WEKNORA_KNOWLEDGE_KB_ID/wiki/pages?page=1&page_size=1"
    ' | jq -er '.total'
}

database_value() {
    docker exec "$DATABASE_CONTAINER" psql -U postgres -d vidsage -Atc "$1"
}

candidate_rows() {
    database_value "
        SELECT id || '|' || transcript_generation || '|' ||
               COALESCE(summary_wiki_page_id, '') || '|' || summary_wiki_page_version
        FROM videos
        WHERE deleted_at IS NULL
          AND uploaded_at IS NOT NULL
          AND TRIM(COALESCE(file_url, '')) <> ''
          AND status IN ('uploaded', 'initializing', 'ready', 'processing', 'completed', 'failed')
        ORDER BY id;
    "
}

current_summary() {
    curl -fsS --max-time 10 http://127.0.0.1/api/custom/training-orchestration/current |
        jq -c '{
          status,
          owner_scope_id: .current.owner_scope_id,
          job_id: .current.job_id,
          result_wiki_page_id: .current.result_wiki_page_id,
          source_fingerprint: .current.source_fingerprint,
          updated_at: .current.updated_at,
          result_version: .data.training_path_projection.schema_version,
          qualified_videos: .data.training_path_projection.statistics.qualified_videos
        }'
}

snapshot_state() {
    local candidate_data candidate_count_value candidate_digest candidate_ids current_json
    local jobs currents active_jobs wiki_total backend_health_value page_code ping_code
    local backend_tag backend_digest frontend_tag frontend_digest model_name prompt_version
    local model_ready=false mps_ready=false

    candidate_data="$(candidate_rows)"
    candidate_count_value="$(printf '%s\n' "$candidate_data" | awk 'NF { count++ } END { print count + 0 }')"
    candidate_digest="$(printf '%s\n' "$candidate_data" | shasum -a 256 | awk '{print "sha256:" $1}')"
    candidate_ids="$(printf '%s\n' "$candidate_data" | awk -F'|' 'NF { print substr($1, 1, 8) }' | jq -Rsc 'split("\n") | map(select(length > 0))')"
    current_json="$(current_summary)"
    jobs="$(database_value 'SELECT count(*) FROM training_orchestration_jobs;')"
    currents="$(database_value 'SELECT count(*) FROM training_orchestration_currents;')"
    active_jobs="$(database_value "SELECT count(*) FROM training_orchestration_jobs WHERE status IN ('queued', 'running');")"
    wiki_total="$(wiki_count)"
    backend_health_value="$(container_health)"
    page_code="$(http_code "$FIXED_PAGE_URL")"
    ping_code="$(http_code "$PING_URL")"
    model_config_ready && model_ready=true
    mps_config_ready && mps_ready=true
    backend_tag="$(docker inspect -f '{{.Config.Image}}' "$BACKEND_CONTAINER")"
    backend_digest="$(docker inspect -f '{{.Image}}' "$BACKEND_CONTAINER")"
    frontend_tag="$(docker inspect -f '{{.Config.Image}}' "$FRONTEND_CONTAINER")"
    frontend_digest="$(docker inspect -f '{{.Image}}' "$FRONTEND_CONTAINER")"
    model_name="$(docker exec "$BACKEND_CONTAINER" sh -lc 'printf %s "$CUSTOM_LLM_MODEL"')"
    prompt_version="$(docker exec "$BACKEND_CONTAINER" sh -lc 'printf %s "$CUSTOM_TRAINING_PROMPT_VERSION"')"

    jq -cn \
      --arg checked_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
      --arg backend_health "$backend_health_value" \
      --arg page_http "$page_code" \
      --arg ping_http "$ping_code" \
      --argjson model_ready "$model_ready" \
      --argjson mps_ready "$mps_ready" \
      --argjson candidate_count "$candidate_count_value" \
      --arg candidate_digest "$candidate_digest" \
      --argjson candidate_ids "$candidate_ids" \
      --argjson current "$current_json" \
      --argjson wiki_count "$wiki_total" \
      --argjson task_count "$jobs" \
      --argjson active_task_count "$active_jobs" \
      --argjson current_count "$currents" \
      --arg backend_tag "$backend_tag" \
      --arg backend_digest "$backend_digest" \
      --arg frontend_tag "$frontend_tag" \
      --arg frontend_digest "$frontend_digest" \
      --arg model "$model_name" \
      --arg prompt_version "$prompt_version" \
      '{
        checked_at: $checked_at,
        service: {
          backend_health: $backend_health,
          fixed_page_http: ($page_http | tonumber),
          ping_http: ($ping_http | tonumber),
          model_service_ready: $model_ready,
          mps_ready: $mps_ready
        },
        input: {
          candidate_count: $candidate_count,
          candidate_digest: $candidate_digest,
          video_id_prefixes: $candidate_ids,
          current_result_qualified_videos: $current.qualified_videos
        },
        baseline: {
          wiki_count: $wiki_count,
          task_count: $task_count,
          active_task_count: $active_task_count,
          current_count: $current_count,
          current: $current
        },
        runtime: {
          backend_image_tag: $backend_tag,
          backend_image_digest: $backend_digest,
          frontend_image_tag: $frontend_tag,
          frontend_image_digest: $frontend_digest,
          model: $model,
          prompt_version: $prompt_version
        }
      }'
}

preflight_passes() {
    jq -e '
      .service.backend_health == "healthy" and
      .service.fixed_page_http == 200 and
      .service.ping_http == 200 and
      .service.model_service_ready and
      .service.mps_ready and
      .input.candidate_count > 0 and
      .input.current_result_qualified_videos > 0 and
      .input.candidate_count == .input.current_result_qualified_videos and
      .baseline.active_task_count == 0 and
      .baseline.current_count == 1 and
      .baseline.current.status == "ready" and
      (.baseline.current.source_fingerprint | startswith("sha256:"))
    ' >/dev/null
}

sanitize_generation() {
    jq '
      def error_class:
        if .error_code == null then null
        elif ((.error // "") | test("unknown field"; "i")) then "unknown_field"
        elif ((.error // "") | test("trailing"; "i")) then "trailing_content"
        elif .error_code == "model_output_truncated" then "model_output_truncated"
        elif .error_code == "model_output_invalid" then "invalid_json"
        elif .error_code == "invalid_reference" then "evidence_out_of_bounds"
        elif .error_code == "source_changed" then "source_changed"
        elif .error_code == "model_transport_failed" then "network_error"
        elif .error_code == "timeout" then "request_timeout"
        else "business_contract_invalid"
        end;
      def safe_reason:
        if .error_code == null then null
        elif ((.error // "") | test("unknown field"; "i")) then "unknown_field"
        elif ((.error // "") | test("trailing"; "i")) then "trailing_content"
        elif ((.error // "") | test("duplicate|repeat"; "i")) then "duplicate_identifier_or_relation"
        elif ((.error // "") | test("identity or learning goal is empty|invalid summary or confidence|required cluster fields are invalid|member topic is invalid|learning stage is invalid|learning unit is invalid"; "i")) then "required_field_invalid"
        elif ((.error // "") | test("unsupported template"; "i")) then "unsupported_template"
        elif ((.error // "") | test("unsupported review status|model must return candidate review status"; "i")) then "unsupported_review_status"
        elif ((.error // "") | test("unsupported relation type"; "i")) then "unsupported_relation_type"
        elif ((.error // "") | test("cannot carry source videos or material requests|has no source videos|must have exactly one material request per source video|cluster collections must not be empty|both relation endpoints require evidence|requires video ID and reason"; "i")) then "required_collection_invalid"
        elif ((.error // "") | test("does not match catalog version|incomplete source identity|source identity changed|fingerprint does not match|source fingerprint does not match"; "i")) then "source_identity_mismatch"
        elif ((.error // "") | test("neither selected nor explicitly unselected|both selected and unselected"; "i")) then "video_selection_coverage_invalid"
        elif ((.error // "") | test("unsupported plan contract version|unsupported catalog contract version|unsupported material contract version|contract version"; "i")) then "contract_version_invalid"
        elif ((.error // "") | test("outside.*whitelist|outside.*profile|outside.*material"; "i")) then "reference_outside_whitelist"
        elif ((.error // "") | test("cycle"; "i")) then "relation_cycle"
        else (.error_code // "unclassified_failure")
        end;
      {
        schema_version,
        mode,
        published,
        wiki_writes,
        started_at,
        finished_at,
        model,
        prompt_version,
        input: {
          qualified_video_count: (.snapshot.videos | length),
          skipped_video_count: (.snapshot.skipped_videos | length),
          source_fingerprint: .snapshot.source_fingerprint,
          video_id_prefixes: [.snapshot.videos[].video_id[0:8]]
        },
        stages: {
          stage_one_entered: any(.calls[]?; .stage == "planning" or .stage == "planning_merge"),
          stage_one_completed: (.plan != null),
          stage_two_materialization_entered: (.materialized_clusters > 0),
          stage_two_generation_entered: any(.calls[]?; .stage == "cluster_generation"),
          relation_generation_entered: ((.projection != null) or any(.calls[]?; .stage == "relation_generation"))
        },
        metrics: {
          model_call_count: (.calls | length),
          estimated_input_tokens: ([.calls[].estimated_tokens] | add // 0),
          total_duration_ms: (((.finished_at | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601) - (.started_at | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601)) * 1000),
          stage_duration_ms: ([.calls[]] | group_by(.stage) | map({key: .[0].stage, value: ([.[].duration_ms] | add // 0)}) | from_entries),
          end_reasons: ([.calls[].end_reason] | unique),
          materialized_clusters,
          materialized_summary_blocks,
          materialized_evidence,
          retrieved_clusters,
          retrieved_evidence,
          generated_cluster_parts,
          generated_relation_candidates
        },
        pipeline_checks: {
          stage_one_protocol_valid: (.plan != null),
          stage_one_references_whitelisted: (.plan != null),
          stage_one_catalog_is_bounded: (all(.snapshot.videos[]?; (has("content") | not) and (has("transcript") | not))),
          stage_two_material_scope_valid: (.projection != null),
          stage_two_output_valid: (.projection != null),
          relations_normalized_deduplicated_acyclic: (.projection != null),
          assembly_and_statistics_valid: (.projection != null),
          source_fingerprint_stable: ((.plan.source_fingerprint? // "") != "" and (.plan.source_fingerprint == .projection.training_path_projection.source_fingerprint))
        },
        projection_summary: (if .projection == null then null else {
          source_fingerprint: .projection.training_path_projection.source_fingerprint,
          statistics: .projection.training_path_projection.statistics
        } end),
        error: (if .error_code == null then null else {
          code: .error_code,
          class: error_class,
          reason: safe_reason
        } end),
        generation_passed: (.error_code == null and .projection != null)
      }'
}

compare_states() {
    jq -cn --argjson before "$1" --argjson after "$2" '{
      services_healthy: (
        $after.service.backend_health == "healthy" and
        $after.service.fixed_page_http == 200 and
        $after.service.ping_http == 200 and
        $after.service.model_service_ready and
        $after.service.mps_ready
      ),
      input_unchanged: (
        $before.input.candidate_count == $after.input.candidate_count and
        $before.input.candidate_digest == $after.input.candidate_digest
      ),
      wiki_unchanged: ($before.baseline.wiki_count == $after.baseline.wiki_count),
      formal_tasks_unchanged: ($before.baseline.task_count == $after.baseline.task_count),
      current_result_unchanged: (
        $before.baseline.current_count == $after.baseline.current_count and
        $before.baseline.current.job_id == $after.baseline.current.job_id and
        $before.baseline.current.result_wiki_page_id == $after.baseline.current.result_wiki_page_id and
        $before.baseline.current.source_fingerprint == $after.baseline.current.source_fingerprint and
        $before.baseline.current.updated_at == $after.baseline.current.updated_at
      )
    }'
}

[[ "${1:-}" == "--help" || "${1:-}" == "-h" ]] && { usage; exit 0; }
[[ "$#" -eq 1 ]] || { usage >&2; exit 2; }
[[ "$1" =~ ^(0[1-9]|[12][0-9]|30)$ ]] || die '轮次必须是 01 至 30'

for command_name in docker curl jq shasum awk; do
    require_command "$command_name"
done

round="$1"
timestamp="$(date +'%Y%m%d-%H%M%S')-$$"
report_path="$REPORT_ROOT/stability-real-run-${round}-${timestamp}.json"
log_path="$REPORT_ROOT/stability-real-run-${round}-${timestamp}.log"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/training-stability.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$REPORT_ROOT"
[[ ! -e "$report_path" && ! -e "$log_path" ]] || die '报告或日志路径已存在'

docker info >/dev/null 2>&1 || die 'Docker Desktop 未运行'
docker exec --user app "$BACKEND_CONTAINER" test -x "$PREVIEW_BINARY" || die '隔离预览程序不存在'

pre_state="$(snapshot_state)"
code_version="$(git -C "$PROJECT_ROOT" rev-parse HEAD)"
code_bundle_hash="$(find "$PROJECT_ROOT/internal/custom/service/trainingorchestration" "$PROJECT_ROOT/cmd/training-orchestration-preview" -type f -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256 | awk '{print "sha256:" $1}')"
prompt_bundle_hash="$(find "$PROJECT_ROOT/internal/custom/service/trainingorchestration/prompts" -type f -print0 | sort -z | xargs -0 shasum -a 256 | shasum -a 256 | awk '{print "sha256:" $1}')"
config_hash="$(shasum -a 256 "$PROJECT_ROOT/.env" "$PROJECT_ROOT/docker-compose.acceptance.yml" | shasum -a 256 | awk '{print "sha256:" $1}')"
pre_state="$(jq -c --arg code_version "$code_version" --arg code_bundle_hash "$code_bundle_hash" --arg prompt_bundle_hash "$prompt_bundle_hash" --arg config_hash "$config_hash" '.runtime += {code_version:$code_version, code_bundle_hash:$code_bundle_hash, prompt_bundle_hash:$prompt_bundle_hash, config_hash:$config_hash}' <<<"$pre_state")"

if ! preflight_passes <<<"$pre_state"; then
    jq -n --arg schema_version 'training-orchestration/stability-round/v1' --arg batch_id "real-run-$round-$timestamp" --argjson preflight "$pre_state" '{schema_version:$schema_version,batch_id:$batch_id,result:"STOP",preflight:$preflight,generation:null,postflight:null,checks:null}' >"$report_path"
    jq -r '["batch_id=" + .batch_id, "result=" + .result, "reason=preflight_failed"] | .[]' "$report_path" >"$log_path"
    printf '[STOP] 前置门禁未通过\n报告: %s\n日志: %s\n' "$report_path" "$log_path" >&2
    exit 3
fi

if [[ "${STABILITY_PREFLIGHT_ONLY:-0}" == "1" ]]; then
    jq '{preflight_passed:true, preflight:.}' <<<"$pre_state"
    exit 0
fi

set +e
set -o pipefail
docker exec --user app "$BACKEND_CONTAINER" "$PREVIEW_BINARY" 2>/dev/null |
    sanitize_generation >"$tmp_dir/generation.json"
pipeline_status=("${PIPESTATUS[@]}")
set +o pipefail
set -e
preview_exit="${pipeline_status[0]}"
jq_exit="${pipeline_status[1]}"

if [[ "$jq_exit" -ne 0 || ! -s "$tmp_dir/generation.json" ]]; then
    jq -n --argjson exit_code "$preview_exit" '{generation_passed:false,error:{code:"preview_report_invalid",class:"service_or_configuration_error",reason:"sanitized_report_unavailable"},preview_exit_code:$exit_code}' >"$tmp_dir/generation.json"
fi

post_state="$(snapshot_state)"
state_checks="$(compare_states "$pre_state" "$post_state")"
generation_json="$(jq -c --argjson preview_exit "$preview_exit" '. + {preview_exit_code:$preview_exit}' "$tmp_dir/generation.json")"
state_checks="$(jq -cn --argjson checks "$state_checks" --argjson preflight "$pre_state" --argjson generation "$generation_json" '$checks + {
  generated_input_matches_preflight: (
    ($generation.input.qualified_video_count <= $preflight.input.candidate_count) and
    all($generation.input.video_id_prefixes[]?;
      . as $video_id |
      ($preflight.input.video_id_prefixes | index($video_id)) != null
    )
  )
}')"
round_passed="$(jq -nr --argjson generation "$generation_json" --argjson checks "$state_checks" '
  $generation.generation_passed and
  ($generation.preview_exit_code == 0) and
  $generation.stages.stage_one_completed and
  $generation.stages.stage_two_materialization_entered and
  $generation.stages.stage_two_generation_entered and
  $generation.stages.relation_generation_entered and
  ($generation.pipeline_checks | all(.[]; . == true)) and
  ($checks | all(.[]; . == true))
')"
result='STOP'
exit_code=1
if [[ "$round_passed" == true ]]; then
    result='PASS'
    exit_code=0
fi

jq -n \
  --arg schema_version 'training-orchestration/stability-round/v1' \
  --arg batch_id "real-run-$round-$timestamp" \
  --arg result "$result" \
  --argjson preflight "$pre_state" \
  --argjson generation "$generation_json" \
  --argjson postflight "$post_state" \
  --argjson checks "$state_checks" \
  '{schema_version:$schema_version,batch_id:$batch_id,result:$result,preflight:$preflight,generation:$generation,postflight:$postflight,checks:$checks}' >"$report_path"

jq -r '[
  "batch_id=" + .batch_id,
  "result=" + .result,
  "model_calls=" + (.generation.metrics.model_call_count // 0 | tostring),
  "estimated_input_tokens=" + (.generation.metrics.estimated_input_tokens // 0 | tostring),
  "duration_ms=" + (.generation.metrics.total_duration_ms // 0 | tostring),
  "error_code=" + (.generation.error.code // "none"),
  "error_class=" + (.generation.error.class // "none"),
  "source_fingerprint_stable=" + (.generation.pipeline_checks.source_fingerprint_stable // false | tostring),
  "wiki_unchanged=" + (.checks.wiki_unchanged | tostring),
  "formal_tasks_unchanged=" + (.checks.formal_tasks_unchanged | tostring),
  "current_result_unchanged=" + (.checks.current_result_unchanged | tostring)
] | .[]' "$report_path" >"$log_path"

printf '[%s] 单轮隔离真实生成完成\n报告: %s\n日志: %s\n' "$result" "$report_path" "$log_path"
exit "$exit_code"
