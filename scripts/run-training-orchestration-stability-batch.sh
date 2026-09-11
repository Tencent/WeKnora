#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REPORT_ROOT="$PROJECT_ROOT/../output/acceptance/training-two-stage-orchestration/stage-06"
ROUND_SCRIPT="$SCRIPT_DIR/run-training-orchestration-stability-round.sh"

usage() {
    cat <<'EOF'
串行执行培训两阶段编排隔离真实生成批量压测；单轮失败只记录，不打断后续轮次。

用法:
  ./scripts/run-training-orchestration-stability-batch.sh [轮数]

轮数默认为 30，允许范围为 1 至 30。每轮使用独立的报告和日志文件。
EOF
}

die() {
    printf '[ERROR] %s\n' "$1" >&2
    exit 1
}

[[ "${1:-}" != "--help" && "${1:-}" != "-h" ]] || {
    usage
    exit 0
}

round_count="${1:-30}"
[[ "$round_count" =~ ^[0-9]+$ ]] || die '轮数必须是整数'
(( round_count >= 1 && round_count <= 30 )) || die '轮数必须在 1 至 30 之间'
[[ -x "$ROUND_SCRIPT" ]] || die "单轮脚本不可执行: $ROUND_SCRIPT"

mkdir -p "$REPORT_ROOT"
batch_timestamp="$(date +'%Y%m%d-%H%M%S')-$$"
batch_report="$REPORT_ROOT/stability-batch-${round_count}-${batch_timestamp}.json"
batch_log="$REPORT_ROOT/stability-batch-${round_count}-${batch_timestamp}.log"
[[ ! -e "$batch_report" && ! -e "$batch_log" ]] || die '批量报告或日志路径已存在'

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/training-stability-batch.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

round_rows="$tmp_dir/rounds.ndjson"
: >"$round_rows"

for round_number in $(seq 1 "$round_count"); do
    round="$(printf '%02d' "$round_number")"
    started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
    set +e
    output="$("$ROUND_SCRIPT" "$round" 2>&1)"
    exit_code=$?
    set -e

    report_path="$(printf '%s\n' "$output" | sed -n 's/^报告: //p' | tail -1)"
    if [[ -n "$report_path" && -f "$report_path" ]]; then
        jq -c \
            --arg round "$round" \
            --arg started_at "$started_at" \
            --arg finished_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
            --argjson runner_exit "$exit_code" \
            '. + {batch_round:$round, batch_started_at:$started_at, batch_finished_at:$finished_at, runner_exit_code:$runner_exit}' \
            "$report_path" >>"$round_rows"
    else
        jq -cn \
            --arg round "$round" \
            --arg started_at "$started_at" \
            --arg finished_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
            --argjson runner_exit "$exit_code" \
            '{batch_round:$round, batch_started_at:$started_at, batch_finished_at:$finished_at, result:"STOP", generation:{error:{code:"round_report_unavailable",class:"service_or_configuration_error",reason:"round_report_unavailable"}}, checks:{}, runner_exit_code:$runner_exit}' \
            >>"$round_rows"
    fi

    printf '[BATCH] round=%s exit=%s report=%s\n' "$round" "$exit_code" "${report_path:-unavailable}" | tee -a "$batch_log"
done

jq -s \
    --arg schema_version 'training-orchestration/stability-batch/v1' \
    --arg batch_id "stability-batch-${round_count}-${batch_timestamp}" \
    --arg started_at "$(head -1 "$round_rows" | jq -r '.batch_started_at')" \
    --arg finished_at "$(tail -1 "$round_rows" | jq -r '.batch_finished_at')" \
    '{
      schema_version:$schema_version,
      batch_id:$batch_id,
      requested_rounds:('"$round_count"'),
      started_at:$started_at,
      finished_at:$finished_at,
      rounds: .,
      summary: {
        pass_count: (map(select(.result=="PASS")) | length),
        stop_count: (map(select(.result=="STOP")) | length),
        error_codes: (map(.generation.error.code // "none") | sort | group_by(.) | map({code:.[0], count:length})),
        error_classes: (map(.generation.error.class // "none") | sort | group_by(.) | map({class:.[0], count:length})),
        stage_outcomes: {
          stage_one_completed: (map(select(.generation.stages.stage_one_completed == true)) | length),
          stage_two_materialization_entered: (map(select(.generation.stages.stage_two_materialization_entered == true)) | length),
          stage_two_generation_entered: (map(select(.generation.stages.stage_two_generation_entered == true)) | length),
          relation_generation_entered: (map(select(.generation.stages.relation_generation_entered == true)) | length)
        },
        safety_checks: {
          wiki_unchanged_all_rounds: all(.[]; .checks.wiki_unchanged == true),
          formal_tasks_unchanged_all_rounds: all(.[]; .checks.formal_tasks_unchanged == true),
          current_result_unchanged_all_rounds: all(.[]; .checks.current_result_unchanged == true),
          services_healthy_all_rounds: all(.[]; .checks.services_healthy == true)
        }
      }
    }' "$round_rows" >"$batch_report"

printf '[BATCH] completed rounds=%s report=%s log=%s\n' "$round_count" "$batch_report" "$batch_log"
