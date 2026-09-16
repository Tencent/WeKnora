"""Run frozen PDF samples through production Go adapters; secrets use stdin only."""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ENGINES = ('builtin', 'markitdown', 'opendataloader', 'weknoracloud', 'mineru', 'mineru_cloud', 'paddleocr_vl', 'paddleocr_vl_cloud')


def read_credentials(native: bool = False, stream=None) -> dict:
    """Read only explicitly supplied configuration; never inspect an application or database."""
    path = os.environ.get('PARSER_BENCHMARK_CREDENTIALS_FILE')
    if stream is not None:
        raw = stream.read(1024 * 1024 + 1)
    elif path:
        with open(path, 'rb') as source:
            raw = source.read(1024 * 1024 + 1)
    else:
        raw = b'{}'
    if len(raw) > 1024 * 1024:
        raise ValueError('Credential input exceeds 1 MiB')
    payload = json.loads(raw)
    if not isinstance(payload, dict):
        raise ValueError('Credential input must be an object')
    config = payload.setdefault('parser_config', {})
    if config is None:
        config = payload['parser_config'] = {}
    if not isinstance(config, dict):
        raise ValueError('Parser configuration must be an object')
    config.setdefault('mineru_endpoint', 'http://127.0.0.1:18081' if native else 'http://weknora-parser-mineru:8000')
    config.setdefault('paddleocr_vl_endpoint', 'http://127.0.0.1:18082' if native else 'http://weknora-parser-paddle:8080')
    return payload


def container_path(value: str) -> str:
    path = Path(value).resolve()
    try:
        return '/workspace/' + path.relative_to(ROOT).as_posix()
    except ValueError:
        raise ValueError('Manifest and output must stay inside benchmark worktree') from None


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--manifest', required=True)
    p.add_argument('--output', required=True)
    p.add_argument('--engine', choices=ENGINES, required=True)
    p.add_argument('--binary', default='artifacts/parser-benchmark/bin/parser-benchmark', help='Frozen adapter executable; use a distinct path for another build')
    p.add_argument('--timeout', default='10m')
    p.add_argument('--execute', action='store_true')
    p.add_argument('--credentials-stdin', action='store_true', help='Read credential JSON from stdin only with --execute')
    p.add_argument('--native', action='store_true', help='Run the binary on this host instead of Docker')
    p.add_argument('--docreader', help='Explicit DocReader gRPC endpoint')
    p.add_argument('--network', default=os.environ.get('PARSER_BENCHMARK_NETWORK', 'weknora-parser-benchmark'))
    p.add_argument('--runtime-image', default=os.environ.get('PARSER_BENCHMARK_RUNTIME_IMAGE',
        'wechatopenai/weknora-docreader@sha256:b9c4636b65b5d4947d5e09cd311ba6cf37f1f2da37c51d4be2b911d432f12abe'))
    p.add_argument('--max-samples', type=int, default=100)
    p.add_argument('--shard-index', type=int, default=0)
    p.add_argument('--shard-count', type=int, default=1)
    args = p.parse_args()
    if not (1 <= args.shard_count <= 4 and 0 <= args.shard_index < args.shard_count):
        raise ValueError('invalid shard count/index')
    if args.shard_count > 1:
        original = json.loads(Path(args.manifest).read_text(encoding='utf-8'))
        if len(original['samples']) > args.max_samples:
            raise ValueError('Complete manifest exceeds cap before sharding')
        original['samples'] = original['samples'][args.shard_index::args.shard_count]
        original['shard'] = {'index': args.shard_index, 'count': args.shard_count}
        shard_path = ROOT / 'artifacts/parser-benchmark/shards' / (Path(args.manifest).stem + f'-{args.shard_index}-of-{args.shard_count}.json')
        shard_path.parent.mkdir(parents=True, exist_ok=True)
        content = json.dumps(original, ensure_ascii=False, indent=2) + '\n'
        if shard_path.exists() and shard_path.read_text(encoding='utf-8') != content:
            raise ValueError('Frozen shard identity changed')
        shard_path.write_text(content, encoding='utf-8')
        args.manifest = str(shard_path)
    if args.native:
        cmd = [str(Path(args.binary).resolve()), '--root', str(ROOT), '--manifest', str(Path(args.manifest).resolve()),
               '--output', str(Path(args.output).resolve()), '--engine', args.engine, '--timeout', args.timeout,
               '--max-samples', str(args.max_samples), '--docreader', args.docreader or '127.0.0.1:50051']
    else:
        cmd = ['docker', 'run', '--rm', '-i', '--name', f'weknora-parser-run-{args.engine}-{args.shard_index}', '--network', args.network, '--memory', '1g', '--cpus', '2',
               '-e', 'SSRF_WHITELIST=weknora-parser-mineru,weknora-parser-paddle', '-e', 'LOG_LEVEL=fatal', '-e', 'JIEBA_DICT_DIR=/workspace/artifacts/parser-benchmark/bin/jieba',
               '-v', f'{ROOT}:/workspace', '-w', '/workspace', '--entrypoint', container_path(args.binary),
               args.runtime_image, '--root', '/workspace', '--manifest', container_path(args.manifest), '--output', container_path(args.output),
               '--engine', args.engine, '--timeout', args.timeout, '--max-samples', str(args.max_samples), '--docreader', args.docreader or 'weknora-parser-docreader:50051']
    payload = b''
    if args.execute:
        cmd.append('--execute')
        payload = json.dumps(read_credentials(args.native, sys.stdin.buffer if args.credentials_stdin else None), ensure_ascii=False).encode('utf-8')
    # Only public paths and flags enter the command line. Secret bytes are never
    # persisted or included in tool output; Go emits redacted per-page records.
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE)
    proc.communicate(payload)
    return proc.returncode


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f'Benchmark launcher failed ({type(exc).__name__}); no credentials emitted.', file=sys.stderr)
        raise SystemExit(1)
