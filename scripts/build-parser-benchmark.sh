#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
mkdir -p artifacts/parser-benchmark/bin/jieba
binary_path="${PARSER_BENCHMARK_BINARY:-artifacts/parser-benchmark/bin/parser-benchmark}"
if [ -e "$binary_path" ]; then
  echo 'The selected executable is frozen. Set PARSER_BENCHMARK_BINARY to a new path for another build.' >&2
  exit 1
fi
source_commit=$(git rev-parse HEAD)
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo 'Build from a clean committed checkout to preserve source identity.' >&2
  exit 1
fi
if [ -n "${PARSER_BENCHMARK_COMMIT:-}" ] && [ "$PARSER_BENCHMARK_COMMIT" != "$source_commit" ]; then
  echo 'PARSER_BENCHMARK_COMMIT must equal the actual full source commit.' >&2
  exit 1
fi
go test -tags sqlite_fts5 ./cmd/parser-benchmark
go build -tags sqlite_fts5 \
  -ldflags "-X main.commitID=$source_commit" \
  -o "$binary_path" ./cmd/parser-benchmark
dict_module=$(go list -m -f '{{.Dir}}' github.com/yanyiwu/gojieba)
for source_dictionary in "$dict_module"/deps/cppjieba/dict/*; do
  target_dictionary="artifacts/parser-benchmark/bin/jieba/$(basename "$source_dictionary")"
  if [ -e "$target_dictionary" ]; then
    cmp -s "$source_dictionary" "$target_dictionary" || {
      echo 'The frozen Jieba dictionary differs. Build in a separate workspace to preserve this run.' >&2
      exit 1
    }
  else
    cp "$source_dictionary" "$target_dictionary"
  fi
done
