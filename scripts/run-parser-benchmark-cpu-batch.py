"""Frozen, sequential PaddleOCR-VL benchmark; default prepares without inference."""
from __future__ import annotations

import argparse
import contextlib
import hashlib
import importlib.util
import json
import os
import re
import subprocess
import sys
import time
import urllib.request
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ENGINE = 'paddleocr_vl'
SERVER = 'weknora-parser-paddle'
CLIENT = 'weknora-parser-run-paddleocr_vl-0'
DEPLOY = Path('deploy/parser-benchmark/paddle')
BINARY = Path('artifacts/parser-benchmark/bin/parser-benchmark')
LAUNCHER = Path('scripts/run-parser-benchmark.py')
STATUSES = {'success', 'error', 'timeout', 'empty'}
ID = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_.-]{0,180}$')


class BatchError(Exception):
    """Only fixed, credential-free diagnostic codes enter persistent logs."""


def utc() -> str:
    return datetime.now(timezone.utc).isoformat()


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


@contextlib.contextmanager
def keep_system_awake(enabled: bool):
    """Keep a Windows batch running through idle time; allow the display to sleep."""
    if not enabled or os.name != 'nt':
        yield
        return
    import ctypes
    set_state = ctypes.windll.kernel32.SetThreadExecutionState
    if not set_state(0x80000001):  # ES_CONTINUOUS | ES_SYSTEM_REQUIRED
        raise BatchError('cannot_hold_system_awake')
    try:
        yield
    finally:
        set_state(0x80000000)


def file_hash(path: Path) -> str:
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(chunk)
    return result.hexdigest()


def encoded(value) -> bytes:
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode('utf-8')


def read_json(path: Path):
    return json.loads(path.read_text(encoding='utf-8'))


def inside(root: Path, path: str | Path) -> Path:
    resolved = (root / path).resolve()
    if not resolved.is_relative_to(root.resolve()):
        raise BatchError('path_outside_worktree')
    return resolved


def atomic_json(path: Path, value) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + '.tmp')
    with temporary.open('wb') as stream:
        stream.write(encoded(value))
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def freeze(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.read_bytes() != content:
            raise BatchError('frozen_file_identity_conflict')
    else:
        with path.open('xb') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())


@contextlib.contextmanager
def exclusive_lock(path: Path):
    """OS-held lock releases on a crash; keep the lock file and its data."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('a+b') as stream:
        stream.seek(0, os.SEEK_END)
        if stream.tell() == 0:
            stream.write(b'0')
            stream.flush()
        stream.seek(0)
        try:
            if os.name == 'nt':
                import msvcrt
                msvcrt.locking(stream.fileno(), msvcrt.LK_NBLCK, 1)
            else:
                import fcntl
                fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            raise BatchError('another_batch_owns_output_lock') from None
        try:
            yield
        finally:
            stream.seek(0)
            if os.name == 'nt':
                msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(stream.fileno(), fcntl.LOCK_UN)


def freeze_samples(root: Path, manifest_path: Path, batch: Path) -> tuple[dict, list[dict]]:
    raw = manifest_path.read_bytes()
    manifest = json.loads(raw)
    samples = manifest.get('samples', [])
    if not 1 <= len(samples) <= 100:
        raise BatchError('manifest_sample_count_out_of_bounds')
    seen = set()
    for sample in samples:
        sample_id = sample.get('id', '')
        if not ID.fullmatch(sample_id) or sample_id in seen:
            raise BatchError('manifest_sample_id_invalid')
        seen.add(sample_id)
        pdf = inside(root, sample['pdf_path'])
        with pdf.open('rb') as stream:
            is_pdf = stream.read(5) == b'%PDF-'
        if not is_pdf or file_hash(pdf) != sample['sha256']:
            raise BatchError('input_pdf_identity_conflict')
    # PDFs are referenced unchanged; preserve the exact parent manifest bytes.
    freeze(batch / 'parent-manifest.json', raw)
    pages = []
    for index, sample in enumerate(samples):
        singleton = dict(manifest, samples=[sample])
        singleton['cpu_batch'] = {'parent_manifest_sha256': digest(raw),
                                  'index': index, 'count': len(samples)}
        content = encoded(singleton)
        path = batch / 'manifests' / f'{index:03d}-{sample["id"]}.json'
        freeze(path, content)
        pages.append({'sample': sample, 'manifest': path, 'manifest_sha256': digest(content)})
    return {'parent_manifest_sha256': digest(raw), 'sample_count': len(samples)}, pages


def docker(*arguments: str, timeout: int = 90) -> str:
    try:
        result = subprocess.run(['docker', *arguments], capture_output=True, text=True,
                                encoding='utf-8', errors='replace', timeout=timeout)
    except (OSError, subprocess.TimeoutExpired):
        raise BatchError('docker_operation_unavailable_or_timed_out') from None
    if result.returncode:
        raise BatchError('docker_operation_failed')
    return result.stdout.strip()


def assert_client_absent() -> None:
    # Even a stopped client container can indicate another launcher has claimed
    # the name. The batch never removes/stops someone else's client container.
    if docker('ps', '-a', '--filter', f'name=^/{CLIENT}$', '--format', '{{.Names}}'):
        raise BatchError('existing_client_container_preserved_wait_for_exit')


def process_alive(pid: int) -> bool:
    if os.name == 'nt':
        # os.kill(pid, 0) is not a portable Windows liveness probe.
        import ctypes
        from ctypes import wintypes
        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        kernel.OpenProcess.restype = wintypes.HANDLE
        kernel.GetExitCodeProcess.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
        kernel.CloseHandle.argtypes = [wintypes.HANDLE]
        handle = kernel.OpenProcess(0x1000, False, pid)
        if not handle:
            if ctypes.get_last_error() == 87:  # No such PID.
                return False
            return True  # Access denied: preserve the process and halt safely.
        try:
            code = wintypes.DWORD()
            return not kernel.GetExitCodeProcess(handle, ctypes.byref(code)) or code.value == 259
        finally:
            kernel.CloseHandle(handle)
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True


def public_config(root: Path) -> dict:
    spec = importlib.util.spec_from_file_location('cpu_batch_launcher', root / LAUNCHER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    # The launcher reads only an explicitly supplied local configuration file.
    # Expose only three public options; cloud credentials remain private.
    payload = module.read_credentials()
    config = payload.get('parser_config') or {}
    output = {'paddleocr_vl_endpoint': config.get('paddleocr_vl_endpoint', 'http://weknora-parser-paddle:8080')}
    for key in ('paddleocr_vl_use_seal_recognition', 'paddleocr_vl_use_chart_recognition'):
        if config.get(key) is not None:
            if not isinstance(config[key], bool):
                raise BatchError('local_parser_option_invalid')
            output[key] = str(config[key]).lower()
    return output


def public_server_identity(root: Path) -> dict:
    server = json.loads(docker('inspect', SERVER))[0]
    if server['Name'] != '/' + SERVER or os.environ.get('PARSER_BENCHMARK_NETWORK', 'weknora-parser-benchmark') not in server['NetworkSettings']['Networks']:
        raise BatchError('paddle_container_or_network_invalid')
    config, host = server['Config'], server['HostConfig']
    environment = dict(item.split('=', 1) for item in config.get('Env', []) if '=' in item)
    runtime = root / DEPLOY / '.runtime'
    mounts = []
    for mount in server.get('Mounts', []):
        if mount['Destination'] == '/runtime':
            # Docker Desktop exposes Windows binds as D:\... or /run/desktop/mnt/host/d/...
            source = mount['Source'].replace('\\', '/').lower()
            expected = str(runtime.resolve()).replace('\\', '/').lower()
            docker_desktop = '/run/desktop/mnt/host/' + expected.replace(':', '')
            if source not in (expected, docker_desktop):
                raise BatchError('paddle_runtime_mount_mismatch')
            mounts.append({'target': '/runtime', 'source': (DEPLOY / '.runtime').as_posix(),
                           'rw': mount.get('RW', False)})
    if len(mounts) != 1 or not mounts[0]['rw']:
        raise BatchError('paddle_runtime_mount_missing')
    # Persist a digest of arbitrary command/environment values, never their text.
    execution_hash = digest(encoded({'entrypoint': config.get('Entrypoint'), 'cmd': config.get('Cmd'),
                                     'env': environment}))
    return {'image_id': server['Image'], 'execution_sha256': execution_hash, 'mounts': mounts,
            'memory': host.get('Memory'), 'nano_cpus': host.get('NanoCpus'),
            'shm_size': host.get('ShmSize'), 'memory_swap': host.get('MemorySwap')}


def verify_models(root: Path) -> None:
    runtime = root / DEPLOY / '.runtime'
    data = read_json(runtime / 'model-manifest.json')
    models = data.get('models', [])
    if not models:
        raise BatchError('model_manifest_empty')
    for model in models:
        base = model['path']
        if not base.startswith('/runtime/') or not model.get('files'):
            raise BatchError('model_manifest_path_invalid')
        for item in model['files']:
            path = inside(runtime, Path(base[len('/runtime/'):]) / item['path'])
            if file_hash(path) != item['sha256']:
                raise BatchError('model_file_identity_conflict')


def build_identity(root: Path, build_path: Path) -> dict:
    build = read_json(build_path)
    binary_hash = file_hash(root / BINARY)
    archived = root / 'artifacts/parser-benchmark/baseline-source/cmd/parser-benchmark/main.go'
    source_hash = file_hash(archived)
    if binary_hash != build['binary_sha256'] or source_hash != build['runner_source_files']['cmd/parser-benchmark/main.go']:
        raise BatchError('baseline_build_identity_conflict')
    if not build.get('compiled_label') or build['compiled_label'] == 'unknown':
        raise BatchError('baseline_source_label_unknown')
    return {'binary_sha256': binary_hash, 'source_commit': build['compiled_label'],
            'archived_source_sha256': source_hash, 'launcher_sha256': file_hash(root / LAUNCHER),
            'build_identity_sha256': file_hash(build_path)}


def current_identity(root: Path, build_path: Path, dataset: dict) -> dict:
    files = ['requirements.lock', 'models.lock.json', 'pipeline.yaml', 'Dockerfile', '.runtime/model-manifest.json']
    return {**dataset, **build_identity(root, build_path), 'engine': ENGINE, 'timeout': '30m',
            'batch_script_sha256': file_hash(Path(__file__)), 'config': public_config(root),
            'server': public_server_identity(root),
            'deployment_sha256': {name: file_hash(root / DEPLOY / name) for name in files}}


def health_once() -> bool:
    try:
        # Explicitly bypass inherited proxies for local readiness checks.
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open('http://127.0.0.1:18082/health', timeout=3) as response:
            body = json.loads(response.read(65536))
            return response.status == 200 and body.get('errorCode') == 0
    except Exception:
        return False


def wait_health(timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if health_once():
            return
        time.sleep(2)
    raise BatchError('paddle_health_timeout_no_page_submitted')


def needs_restart(record: dict) -> bool:
    if record['status'] == 'timeout':
        return True
    if record['status'] != 'error':
        return False
    error = str(record.get('error', '')).lower()
    return any(marker in error for marker in ('timeout', 'timed out', 'deadline exceeded',
               'http request:', 'read response body:', 'connection', 'broken pipe',
               'unexpected eof', 'eof', 'status 502', 'status 503', 'status 504'))


def validate_record(output: Path, page: dict, identity: dict) -> tuple[dict, dict]:
    sample = page['sample']
    record_path = output / ENGINE / (sample['id'] + '.json')
    markdown = output / ENGINE / (sample['id'] + '.md')
    if not record_path.is_file() or not markdown.is_file():
        raise BatchError('raw_record_or_markdown_missing')
    record = read_json(record_path)
    expected = {'engine': ENGINE, 'sample_id': sample['id'], 'input_sha256': sample['sha256'],
                'manifest_sha256': page['manifest_sha256'], 'source_commit': identity['source_commit'],
                'config': identity['config'], 'markdown_sha256': file_hash(markdown)}
    if any(record.get(key) != value for key, value in expected.items()):
        raise BatchError('raw_record_identity_conflict')
    if record.get('binary_sha256', identity['binary_sha256']) != identity['binary_sha256']:
        raise BatchError('raw_record_binary_identity_conflict')
    if record.get('status') not in STATUSES:
        raise BatchError('raw_record_status_invalid')
    return record, {'record_sha256': file_hash(record_path), 'markdown_sha256': expected['markdown_sha256'],
                    'status': record['status']}


def load_commands(path: Path | None) -> list[list[str]]:
    commands = read_json(path) if path else []
    if not isinstance(commands, list) or any(not isinstance(cmd, list) or not cmd or
            any(not isinstance(arg, str) or not arg or '\x00' in arg for arg in cmd) for cmd in commands):
        raise BatchError('postprocess_requires_json_array_of_argument_arrays')
    return commands


class Batch:
    def __init__(self, root: Path, args):
        self.root, self.args = root, args
        self.directory = inside(root, args.batch_dir)
        self.output = inside(root, args.output)
        self.manifest = inside(root, args.manifest)
        self.build_path = inside(root, args.build_identity)
        self.state_path = self.directory / 'batch-state.json'
        self.state = None

    def event(self, kind: str, **fields) -> None:
        event = {'at': utc(), 'event': kind, **fields}
        with (self.directory / 'events.jsonl').open('ab') as stream:
            stream.write(encoded(event).replace(b'\n', b'') + b'\n')
            stream.flush()
            os.fsync(stream.fileno())
        # Only allowlisted metadata; never child stdout, command arguments, or errors.
        with (self.directory / 'batch.log').open('a', encoding='utf-8') as stream:
            stream.write(f'{event["at"]} {kind} {fields.get("sample_id", "")}\n')
        print(json.dumps(event, ensure_ascii=False), flush=True)

    def save(self) -> None:
        self.state['updated_at'] = utc()
        self.state['counts'] = dict(Counter(item['phase'] for item in self.state['pages'].values()))
        self.state['remaining_sample_ids'] = [key for key, value in self.state['pages'].items() if value['phase'] == 'pending']
        self.state['unresolved_sample_ids'] = [key for key, value in self.state['pages'].items() if value['phase'] in ('submitted', 'indeterminate')]
        atomic_json(self.state_path, self.state)

    def restart(self) -> None:
        assert_client_absent()
        self.event('paddle_recovery_begin')
        docker('stop', '--time', '30', SERVER)
        docker('start', SERVER)
        wait_health(self.args.health_timeout)
        self.event('paddle_recovery_healthy')

    def prove_result(self, page: dict, identity: dict, recovered: bool = False) -> dict:
        sample_id = page['sample']['id']
        entry = self.state['pages'][sample_id]
        record, proof = validate_record(self.output, page, identity)
        receipt_path = self.directory / 'receipts' / (sample_id + '.json')
        if receipt_path.exists():
            receipt = read_json(receipt_path)
            expected = {'run_sha256': self.state['run_sha256'], **proof,
                        'manifest_sha256': page['manifest_sha256'], 'input_sha256': page['sample']['sha256']}
            if any(receipt.get(key) != value for key, value in expected.items()):
                raise BatchError('receipt_identity_conflict')
        else:
            # The original binary does not emit a binary hash. A durable intent
            # written BEFORE launch is mandatory; never invent history for old data.
            if entry.get('phase') != 'submitted' or entry.get('attempt_run_sha256') != self.state['run_sha256']:
                raise BatchError('unowned_existing_evidence_preserved')
            receipt = {'schema_version': 1, 'sample_id': sample_id, 'engine': ENGINE,
                       'run_sha256': self.state['run_sha256'], **proof,
                       'manifest_sha256': page['manifest_sha256'], 'input_sha256': page['sample']['sha256'],
                       'binary_sha256': identity['binary_sha256'], 'source_commit': identity['source_commit'],
                       'record_binary_hash_source': 'raw_record' if record.get('binary_sha256') else 'batch_before_and_after_execution',
                       'intent_at': entry['submitted_at'], 'verified_at': utc(),
                       'recovered_after_interruption': recovered}
            freeze(receipt_path, encoded(receipt))
        entry.update(phase='recorded', status=record['status'], receipt_sha256=file_hash(receipt_path))
        self.save()
        return record

    def execute_page(self, page: dict, identity: dict) -> dict:
        sample_id = page['sample']['id']
        # Readiness errors occur before durable submission intent and before the
        # production adapter can write any quality result.
        assert_client_absent()
        wait_health(self.args.health_timeout)
        if current_identity(self.root, self.build_path, self.dataset) != identity:
            raise BatchError('runtime_identity_changed_before_submission')
        if file_hash(inside(self.root, page['sample']['pdf_path'])) != page['sample']['sha256']:
            raise BatchError('input_pdf_changed_before_submission')
        if file_hash(page['manifest']) != page['manifest_sha256']:
            raise BatchError('singleton_manifest_changed_before_submission')
        entry = self.state['pages'][sample_id]
        entry.update(phase='submitted', submitted_at=utc(), attempt_run_sha256=self.state['run_sha256'])
        self.state['status'] = 'running'
        self.save()
        self.event('page_submitting', sample_id=sample_id)
        command = [sys.executable, str(self.root / LAUNCHER), '--engine', ENGINE, '--timeout', '30m',
                   '--execute', '--manifest', str(page['manifest']), '--output', str(self.output), '--max-samples', '1']
        # The launcher already stores redacted raw records. Discard arbitrary
        # child output so Docker diagnostics cannot expose environment secrets.
        try:
            process = subprocess.Popen(command, cwd=self.root, stdin=subprocess.DEVNULL,
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except OSError:
            entry.update(phase='pending', launch_error='process_not_started')
            self.save()
            raise BatchError('launcher_process_not_started') from None
        entry['launcher_pid'] = process.pid
        self.save()
        try:
            exit_code = process.wait(timeout=1980)
        except subprocess.TimeoutExpired:
            # A live client may still own the request. Leave it and the service
            # untouched, halt the batch, and require a later safe reconciliation.
            raise BatchError('client_still_running_batch_halted_no_next_submission') from None
        assert_client_absent()
        self.event('page_client_exited', sample_id=sample_id, exit_code=exit_code)
        entry.update(launcher_exit_code=exit_code, client_exited_at=utc())
        self.save()
        if current_identity(self.root, self.build_path, self.dataset) != identity:
            raise BatchError('runtime_identity_changed_during_submission')
        result_path = self.output / ENGINE / (sample_id + '.json')
        if not result_path.exists():
            entry.update(phase='indeterminate', recovery_required=True)
            self.save()
            # Whether the server accepted a request is unknown; no automatic retry.
            self.restart()
            entry['recovery_required'] = False
            self.save()
            raise BatchError('client_exited_without_result_remaining_pages_preserved')
        record = self.prove_result(page, identity)
        self.event('page_recorded', sample_id=sample_id, status=record['status'])
        if needs_restart(record):
            entry['recovery_required'] = True
            self.save()
            self.restart()
            entry['recovery_required'] = False
            self.save()
        if exit_code:
            raise BatchError('launcher_nonzero_existing_result_preserved')
        return record

    def run(self) -> int:
        self.directory.mkdir(parents=True, exist_ok=True)
        self.dataset, self.pages = freeze_samples(self.root, self.manifest, self.directory)
        build_identity(self.root, self.build_path)
        commands = load_commands(Path(self.args.postprocess_json) if self.args.postprocess_json else None)
        if not self.args.execute:
            print(json.dumps({'preflight': 'passed', **self.dataset, 'network_calls': 0,
                              'inference_submissions': 0, 'next_action': 'run_with_execute'}))
            return 0
        with exclusive_lock(self.output / '.paddle-cpu-batch.lock'):
            return self.run_locked(commands)

    def run_locked(self, commands: list[list[str]]) -> int:
        assert_client_absent()
        verify_models(self.root)
        identity = current_identity(self.root, self.build_path, self.dataset)
        run_hash = digest(encoded(identity))
        if self.state_path.exists():
            self.state = read_json(self.state_path)
            if self.state.get('run_sha256') != run_hash or self.state.get('output') != self.output.relative_to(self.root).as_posix():
                raise BatchError('batch_runtime_identity_conflict')
            if self.state.get('postprocess_plan_sha256') != digest(encoded(commands)):
                raise BatchError('postprocess_plan_identity_conflict')
        else:
            self.state = {'schema_version': 1, 'run_sha256': run_hash, 'identity': identity,
                          'output': self.output.relative_to(self.root).as_posix(), 'created_at': utc(),
                          'postprocess_plan_sha256': digest(encoded(commands)),
                          'status': 'prepared', 'pages': {p['sample']['id']: {'phase': 'pending'} for p in self.pages}}
        freeze(self.directory / 'run-identity.json', encoded(identity))
        self.save()
        # Reject foreign or damaged evidence BEFORE starting or restarting services.
        recover = False
        for page in self.pages:
            sample_id = page['sample']['id']
            entry = self.state['pages'][sample_id]
            if entry['phase'] == 'submitted' and not entry.get('client_exited_at') and entry.get('launcher_pid') and process_alive(entry['launcher_pid']):
                raise BatchError('previous_launcher_still_running_no_next_submission')
            result = self.output / ENGINE / (sample_id + '.json')
            markdown = result.with_suffix('.md')
            if entry['phase'] == 'submitted' and not entry.get('launcher_pid') and not result.exists():
                # A crash between Popen and saving its PID leaves a possible
                # credential-reading launcher that has not created Docker yet.
                # Container absence alone cannot prove it is safe to submit again.
                raise BatchError('submission_pid_unknown_manual_reconciliation_required')
            if result.exists():
                record = self.prove_result(page, identity, recovered=entry['phase'] == 'submitted')
                if needs_restart(record) and entry.get('recovery_required') is not False:
                    entry['recovery_required'] = True
                recover |= bool(entry.get('recovery_required'))
            elif entry['phase'] in ('recorded',) or markdown.exists():
                raise BatchError('partial_or_missing_evidence_preserved')
            elif entry['phase'] == 'submitted':
                entry.update(phase='indeterminate', recovery_required=True)
                recover = True
            elif entry['phase'] == 'indeterminate':
                recover |= bool(entry.get('recovery_required'))
        self.save()
        pending = any(entry['phase'] == 'pending' for entry in self.state['pages'].values())
        if recover:
            self.restart()
            for entry in self.state['pages'].values():
                if entry.get('recovery_required'):
                    entry['recovery_required'] = False
            self.save()
        if pending:
            # Start only the already provisioned named container; cache is retained.
            docker('start', SERVER)
            wait_health(self.args.health_timeout)
        for page in self.pages:
            entry = self.state['pages'][page['sample']['id']]
            if entry['phase'] == 'recorded':
                self.event('page_skipped_verified', sample_id=page['sample']['id'], status=entry['status'])
            elif entry['phase'] == 'pending':
                self.execute_page(page, identity)
        # Re-read every artifact before claiming completion. Non-success results
        # count as executed, while missing/indeterminate pages do not.
        if any(entry['phase'] != 'recorded' for entry in self.state['pages'].values()):
            raise BatchError('batch_incomplete_indeterminate_pages_not_resubmitted')
        for page in self.pages:
            self.prove_result(page, identity)
        self.state.update(status='execution_complete', executed_pages=len(self.pages),
                          result_counts=dict(Counter(entry['status'] for entry in self.state['pages'].values())))
        self.save()
        completion = {'schema_version': 1, 'run_sha256': run_hash, **self.dataset,
                      'executed_pages': len(self.pages), 'result_counts': self.state['result_counts'],
                      'all_pages_have_verified_evidence': True, 'all_pages_successful': self.state['result_counts'].get('success', 0) == len(self.pages)}
        freeze(self.directory / 'execution-complete.json', encoded(completion))
        self.event('execution_complete', executed_pages=len(self.pages), result_counts=self.state['result_counts'])
        if commands:
            command_hash = digest(encoded(commands))
            finished = self.state.get('postprocess', {})
            if finished.get('commands_sha256') not in (None, command_hash):
                raise BatchError('postprocess_identity_changed')
            finished.update(commands_sha256=command_hash)
            self.state['postprocess'] = finished
            for index, command in enumerate(commands):
                if index < finished.get('completed_commands', 0):
                    continue
                finished.update(status='running', current_index=index)
                self.save()
                self.event('postprocess_started', index=index)
                # JSON arrays are passed directly; never use a shell or print argv.
                result = subprocess.run(command, cwd=self.root, stdin=subprocess.DEVNULL,
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, shell=False)
                if result.returncode:
                    finished.update(status='failed', exit_code=result.returncode)
                    self.save()
                    raise BatchError('postprocess_failed_execution_evidence_retained')
                finished.update(completed_commands=index + 1, status='complete')
                self.save()
                self.event('postprocess_finished', index=index)
        self.state['status'] = 'complete'
        self.save()
        freeze(self.directory / 'batch-complete.json', encoded({**completion, 'postprocess_commands': len(commands)}))
        return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', default='dataset/parser-benchmark/manifest-full.json')
    parser.add_argument('--output', default='artifacts/parser-benchmark/runs/baseline-v1')
    parser.add_argument('--batch-dir', default='artifacts/parser-benchmark/cpu-batch-paddle-v1')
    parser.add_argument('--build-identity', default='artifacts/parser-benchmark/baseline-build-identity.json')
    parser.add_argument('--postprocess-json', help='JSON array of argv arrays; local commands run only after complete evidence')
    parser.add_argument('--health-timeout', type=float, default=300)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args(argv)
    if not 1 <= args.health_timeout <= 1800:
        parser.error('--health-timeout must be in 1..1800 seconds')
    batch = Batch(ROOT, args)
    try:
        with keep_system_awake(args.execute):
            return batch.run()
    except (Exception, KeyboardInterrupt) as exc:
        code = str(exc) if isinstance(exc, BatchError) else ('interrupted' if isinstance(exc, KeyboardInterrupt) else type(exc).__name__)
        # Do not render arbitrary exception messages: they can include subprocess
        # arguments or configuration. Unknown exceptions expose class name only.
        if batch.state is not None:
            batch.state.update(status='halted', halt_reason=code)
            batch.save()
        batch.event('batch_halted', reason=code)
        return 130 if isinstance(exc, KeyboardInterrupt) else 1


if __name__ == '__main__':
    raise SystemExit(main())
