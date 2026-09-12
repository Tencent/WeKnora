#!/usr/bin/env bash
# Complete acceptance pipeline: synthetic providers only, fresh SQLite database.
set -euo pipefail
repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
for tool in git go python3 make; do command -v "$tool" >/dev/null; done
python3 -c 'import sys; assert sys.version_info >= (3,11), "Python 3.11+ is required"'
source_commit=$(git rev-parse HEAD)
source_tree=$(git rev-parse 'HEAD^{tree}')
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  printf 'Use a clean committed checkout; tracked changes prevent source verification.\n' >&2
  exit 2
fi
evidence_root=${EVALUATION_EVIDENCE_ROOT:-"$repo_dir/../evaluation-evidence"}
mkdir -p "$evidence_root"
run_dir=$(mktemp -d "$evidence_root/acceptance.XXXXXX")
printf 'Fresh evidence directory: %s\n' "$run_dir"
printf '%s\n' "$source_commit" > "$run_dir/source-commit.txt"
printf '%s\n' "$source_tree" > "$run_dir/source-tree.txt"
export GITHUB_SHA="$source_commit" CGO_ENABLED=1
export GOMAXPROCS=${GOMAXPROCS:-2}
phase=environment
trap 'status=$?; printf "%s %s\n" "$phase" "$status" > "$run_dir/exit-status.txt"' EXIT
go version > "$run_dir/environment.txt"
python3 --version >> "$run_dir/environment.txt"
phase=golden
make evaluation-reproduce EVALUATION_REPORT_DIR="$run_dir/golden" > "$run_dir/golden.log" 2>&1
phase=public-data
python3 scripts/prepare-public-evaluation.py --check > "$run_dir/public-data.log" 2>&1
python3 -B scripts/prepare-public-evaluation-test.py >> "$run_dir/public-data.log" 2>&1
phase=persistent-http
python3 scripts/evaluation-rag-http-smoke.py --output "$run_dir/rag" > "$run_dir/rag.log" 2>&1
phase=fault-recovery
python3 scripts/evaluation-fault-http-smoke.py --output "$run_dir/fault" \
  --server-binary "$run_dir/rag/weknora-server" > "$run_dir/fault.log" 2>&1
phase=accounting-and-budget
go test -tags sqlite_fts5 ./internal/modelobs ./internal/modelcache ./internal/models/call \
  ./internal/models/chat ./internal/types > "$run_dir/accounting.log" 2>&1
python3 -B scripts/test_evaluation_openrouter_budget.py > "$run_dir/budget.log" 2>&1
python3 -B scripts/test_evaluation_expanded_reader.py > "$run_dir/reader.log" 2>&1
phase=parser-offline
python3 -B scripts/test_parser_benchmark.py ScoringIntegrityTests OfficialDenominatorAuditTests ReviewBindingTests ManifestMetadataTests > "$run_dir/parser.log" 2>&1
python3 -B scripts/test_parser_benchmark_launcher.py >> "$run_dir/parser.log" 2>&1
python3 -B scripts/test_parser_benchmark_cpu_batch.py >> "$run_dir/parser.log" 2>&1
phase=version
python3 scripts/check-product-version.py > "$run_dir/product-version.log" 2>&1
phase=source-and-evidence
test "$(git rev-parse HEAD)" = "$source_commit"
test "$(git rev-parse 'HEAD^{tree}')" = "$source_tree"
test -z "$(git status --porcelain --untracked-files=no)"
printf 'passed 0\n' > "$run_dir/exit-status.txt"
python3 - "$run_dir" "$source_commit" "$source_tree" <<'PY'
from pathlib import Path
import hashlib,json,sys
p=Path(sys.argv[1]); commit,tree=sys.argv[2:]
gold=json.loads((p/'golden/evaluation-regression.json').read_text())
rag=json.loads((p/'rag/manifest.json').read_text())
fault=json.loads((p/'fault/fault-results.json').read_text())
assert gold['commit']==commit and gold['regression']['passed']
assert rag['status']==fault['status']=='passed'
assert rag['source_commit']==commit
assert all(case['detail']['experiment']['code']['commit_id']==commit for case in fault['cases'])
assert rag['paid_provider_requests']==fault['paid_provider_requests']==0
assert [r['attempts'].get('embedding',0) for r in rag['rounds']]==[25,0]
assert all(r['attempts']['chat']==20 for r in rag['rounds'])
assert [r['detail']['task']['status'] for r in fault['cases']]==[6,5]
summary={'code_commit':commit,'source_tree':tree,'status':'passed',
 'golden_checks':len(gold['regression']['checks']),
 'round_attempts':[r['attempts'] for r in rag['rounds']],
 'fault_statuses':[r['detail']['task']['status'] for r in fault['cases']],
 'paid_provider_requests':0,'server_sha256':rag['server_sha256'],
 'scope':'deterministic local engineering verification; no live provider quality or human ratings'}
(p/'verification-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
files=[]
for f in sorted(p.rglob('*')):
 if f.is_file():
  h=hashlib.sha256()
  with f.open('rb') as stream:
   for chunk in iter(lambda:stream.read(1024*1024),b''):h.update(chunk)
  files.append({'path':f.relative_to(p).as_posix(),'bytes':f.stat().st_size,'sha256':h.hexdigest()})
(p/'file-manifest.json').write_text(json.dumps(files,indent=2)+'\n')
print(json.dumps(summary))
PY
phase=passed
printf 'Acceptance passed for %s. Evidence: %s\n' "$source_commit" "$run_dir"
