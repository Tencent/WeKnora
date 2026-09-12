#!/usr/bin/env python3
"""Run the real Lite evaluation HTTP pipeline against a loopback-only supplier.

This is an offline-fixture contract regression, not a model quality experiment.
Only a newly created output directory/database is writable. No saved application
configuration, credentials or databases are loaded. Python standard library only.
"""
import argparse
import collections
import csv
import hashlib
import http.server
import io
import json
import math
import os
from pathlib import Path
import re
import secrets
import signal
import sqlite3
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
FIXTURE_KEY = 'x07-rag-offline-placeholder'
DIMENSION = 32
CORPUS_SHA256 = 'f32f37ab3388a42e1e671f64dd73e85198639011224b649c2cf5d345e3237ea8'
# Lower bounds from the fixed 20-question offline fixture. These guard the
# production retrieval path; they make no claim about paid model quality.
RETRIEVAL_MINIMUMS = {'recall': 0.7416666666666666, 'ndcg3': 0.5310068874168308}


def digest(data):
    return hashlib.sha256(data).hexdigest()


def dump(path, value):
    def sqlite_json(value):
        if isinstance(value, bytes):
            return json.loads(value.decode('utf-8'))
        raise TypeError(f'Unsupported evidence value: {type(value).__name__}')
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2, default=sqlite_json) + '\n', encoding='utf-8')


def redact(value):
    if isinstance(value, dict):
        return {k: '[redacted]' if k.lower() in {'token', 'access_token', 'refresh_token', 'password',
                'api_key', 'app_secret', 'authorization'} else redact(v) for k, v in value.items()}
    if isinstance(value, list):
        return [redact(v) for v in value]
    return value


def vector(text):
    """Fixed normalized character-bigram hashing, with no corpus answer oracle."""
    text = re.sub(r'\s+', '', text.lower())
    values = [0.0] * DIMENSION
    for i in range(max(1, len(text) - 1)):
        token = text[i:i + 2].encode()
        values[int.from_bytes(hashlib.sha256(token).digest()[:4], 'little') % DIMENSION] += 1
    norm = math.sqrt(sum(v * v for v in values))
    return [v / norm for v in values]


class Supplier:
    def __init__(self, port, corpus, output):
        self.corpus, self.output, self.round = corpus, output, 0
        self.lock, self.records, self.errors = threading.Lock(), [], []
        supplier = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_CONNECT(self):
                self.send_error(403, 'Offline fixture never forwards requests')

            def do_POST(self):
                try:
                    assert self.client_address[0] == '127.0.0.1'
                    assert self.headers.get('Authorization') == 'Bearer ' + FIXTURE_KEY
                    size = int(self.headers.get('Content-Length', 0))
                    assert 0 < size <= 2_000_000
                    raw = self.rfile.read(size)
                    payload = json.loads(raw)
                    start = time.monotonic()
                    record = {'mode': 'offline-fixture', 'round': supplier.round,
                              'path': self.path, 'request_sha256': digest(raw), 'model': payload['model']}
                    if self.path == '/v1/embeddings':
                        assert payload['model'] == 'x07-fixture-embedding'
                        inputs = payload['input']
                        assert isinstance(inputs, list) and 1 <= len(inputs) <= 100
                        assert payload.get('dimensions', DIMENSION) == DIMENSION
                        usage = {'prompt_tokens': sum(max(1, len(t.encode()) // 4) for t in inputs)}
                        usage['total_tokens'] = usage['prompt_tokens']
                        response = {'model': payload['model'], 'data': [
                            {'index': i, 'embedding': vector(t)} for i, t in enumerate(inputs)], 'usage': usage}
                        record.update(operation='embedding', input_sha256=[digest(t.encode()) for t in inputs])
                    elif self.path == '/v1/chat/completions':
                        assert payload['model'] == 'x07-fixture-chat'
                        assert not payload.get('stream') and not payload.get('tools')
                        assert payload.get('max_tokens') == 256
                        assert 'max_completion_tokens' not in payload
                        messages = payload['messages']
                        last = messages[-1]['content']
                        matches = [q for q in corpus['questions'] if q['question'] in last]
                        assert len(matches) == 1, 'Fixture only answers an exact frozen question'
                        question = matches[0]
                        # Deliberately fixed test response; retrieval quality still
                        # comes from actual SQLite search results and graded labels.
                        answer = question['answer']
                        usage = {'prompt_tokens': max(1, len(raw) // 4),
                                 'completion_tokens': max(1, len(answer.encode()) // 4),
                                 'prompt_tokens_details': {'cached_tokens': 0}}
                        usage['total_tokens'] = usage['prompt_tokens'] + usage['completion_tokens']
                        response = {'id': 'offline-fixture', 'model': payload['model'], 'choices': [
                            {'index': 0, 'finish_reason': 'stop',
                             'message': {'role': 'assistant', 'content': answer}}], 'usage': usage}
                        record.update(operation='chat', qid=question['qid'], answer_sha256=digest(answer.encode()))
                    else:
                        raise AssertionError('Unapproved supplier path')
                    record.update(usage=usage, duration_ms=round((time.monotonic() - start) * 1000, 3))
                    with supplier.lock:
                        assert len(supplier.records) < 160, 'Bounded local request count exceeded'
                        record['sequence'] = len(supplier.records) + 1
                        supplier.records.append(record)
                        with (output / 'supplier.jsonl').open('a', encoding='utf-8') as stream:
                            stream.write(json.dumps(record, ensure_ascii=False) + '\n')
                    encoded = json.dumps(response, ensure_ascii=False).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(encoded)))
                    self.end_headers()
                    self.wfile.write(encoded)
                except Exception as error:
                    with supplier.lock:
                        supplier.errors.append(type(error).__name__ + ': ' + str(error))
                    self.send_error(400, 'Offline fixture rejected request')

        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', port), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def start(self):
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()


class Regression:
    def __init__(self, args):
        self.args = args
        self.output = args.output.resolve()
        self.output.mkdir(mode=0o700, parents=False, exist_ok=False)
        self.database = self.output / 'x07-rag.sqlite'
        self.corpus_path = ROOT / 'dataset/synthetic-campus/v1/registry-input.json'
        raw = self.corpus_path.read_bytes().replace(b'\r\n', b'\n')
        assert digest(raw) == CORPUS_SHA256, 'Frozen corpus changed'
        self.corpus = json.loads(raw)
        assert len(self.corpus['questions']) == 20 and len(self.corpus['passages']) == 24
        self.binary = args.server_binary.resolve() if args.server_binary else self.output / 'weknora-server'
        if not args.server_binary:
            subprocess.run(['go', 'build', '-tags', 'sqlite_fts5', '-o', str(self.binary), './cmd/server'],
                           cwd=ROOT, check=True, timeout=900)
        assert self.binary.is_file()
        self.manifest = {'mode': 'offline-fixture', 'corpus_sha256': digest(raw), 'question_count': 20,
                         'passage_count': 24, 'server_sha256': digest(self.binary.read_bytes()),
                         'script_sha256': digest(Path(__file__).read_bytes()), 'embedding_dimension': DIMENSION,
                         'chunking': {'strategy': 'dataset_passage', 'one_passage_per_chunk': True},
                         'retrieval_minimums': RETRIEVAL_MINIMUMS,
                         'paid_provider_requests': 0, 'rounds': []}
        dump(self.output / 'manifest.json', self.manifest)
        self.base = f'http://127.0.0.1:{args.port}'
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.env = {
            'PATH': os.environ.get('PATH', '/usr/local/go/bin:/usr/bin:/bin'),
            'GIN_MODE': 'release', 'LOG_LEVEL': 'info', 'GOMAXPROCS': '2',
            'SERVER_HOST': '127.0.0.1', 'SERVER_PORT': str(args.port),
            'DB_DRIVER': 'sqlite', 'DB_PATH': str(self.database), 'RETRIEVE_DRIVER': 'sqlite',
            'STORAGE_TYPE': 'local', 'LOCAL_STORAGE_BASE_DIR': str(self.output / 'storage'),
            'STREAM_MANAGER_TYPE': 'memory', 'CONCURRENCY_POOL_SIZE': '1',
            'TENANT_AES_KEY': secrets.token_hex(16), 'JWT_SECRET': secrets.token_hex(32),
            'NEO4J_ENABLE': 'false', 'ENABLE_GRAPH_RAG': 'false', 'WEKNORA_SANDBOX_MODE': 'disabled',
            'DISABLE_REGISTRATION': 'false', 'WEKNORA_TENANT_ENABLE_RBAC': 'true',
            'WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS': 'false',
            'OLLAMA_BASE_URL': f'http://127.0.0.1:{args.supplier_port}', 'DOCREADER_ADDR': '',
            'REDIS_ADDR': '', 'LANGFUSE_ENABLED': 'false', 'SSRF_WHITELIST': '127.0.0.1',
            'HTTP_PROXY': f'http://127.0.0.1:{args.supplier_port}',
            'HTTPS_PROXY': f'http://127.0.0.1:{args.supplier_port}', 'NO_PROXY': '127.0.0.1,localhost',
        }
        if os.environ.get('JIEBA_DICT_DIR'):
            self.env['JIEBA_DICT_DIR'] = os.environ['JIEBA_DICT_DIR']
        self.process, self.log, self.token = None, None, None
        self.supplier = Supplier(args.supplier_port, self.corpus, self.output)

    def request(self, name, method, path, body=None, expected=200, record=True):
        assert path.startswith('/') and '://' not in path
        headers = {'Content-Type': 'application/json'}
        if self.token:
            headers['Authorization'] = 'Bearer ' + self.token
        request = urllib.request.Request(self.base + path, None if body is None else json.dumps(body).encode(),
                                         headers, method=method)
        try:
            response = self.opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        raw = response.read()
        try:
            data = json.loads(raw)
        except (ValueError, UnicodeDecodeError):
            data = raw.decode('utf-8-sig')
        if record:
            with (self.output / 'http.jsonl').open('a', encoding='utf-8') as stream:
                stream.write(json.dumps({'name': name, 'method': method, 'path': path, 'status': response.status,
                    'response': redact(data)}, ensure_ascii=False) + '\n')
        assert response.status == expected, f'{name}: HTTP {response.status}: {redact(data)}'
        return data, raw

    def start(self, round_number):
        self.log = (self.output / f'server-round-{round_number}.log').open('wb')
        self.process = subprocess.Popen([str(self.binary)], cwd=ROOT, env=self.env, stdin=subprocess.DEVNULL,
                                        stdout=self.log, stderr=self.log, start_new_session=True)
        for _ in range(200):
            assert self.process.poll() is None, 'Server exited; inspect server log'
            try:
                data, _ = self.request('ready', 'GET', '/health/ready', record=False)
                if data.get('status') == 'ok':
                    return
            except (OSError, AssertionError):
                time.sleep(0.2)
        raise AssertionError('Server startup exceeded 40 seconds')

    def stop(self):
        if self.process and self.process.poll() is None:
            self.process.send_signal(signal.SIGTERM)
            try:
                self.process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        if self.log:
            self.log.close()

    def register(self):
        password = secrets.token_urlsafe(14) + '7aA!'
        self.request('register', 'POST', '/api/v1/auth/register',
                     {'username': 'X07 RAG offline fixture', 'email': 'x07-rag@example.test', 'password': password}, 201)
        login, _ = self.request('login', 'POST', '/api/v1/auth/login',
                                {'email': 'x07-rag@example.test', 'password': password})
        self.token, self.tenant = login['token'], login['active_tenant']['id']
        models = {}
        for role, kind in (('embedding', 'Embedding'), ('chat', 'KnowledgeQA')):
            parameters = {'base_url': f'http://127.0.0.1:{self.args.supplier_port}/v1',
                          'api_key': FIXTURE_KEY, 'provider': 'generic', 'interface_type': 'openai',
                          'max_concurrency': 1}
            if role == 'embedding':
                parameters['embedding_parameters'] = {'dimension': DIMENSION,
                    'truncate_prompt_tokens': 511, 'supports_dimension_override': True}
            else:
                parameters.update(context_window=32768, max_output_tokens=512,
                                  extra_config={'thinking_control': 'none'})
            model, _ = self.request('create ' + role, 'POST', '/api/v1/models',
                {'name': f'x07-fixture-{role}', 'type': kind, 'source': 'remote',
                 'description': 'Offline fixed supplier, not real model quality', 'parameters': parameters}, 201)
            models[role] = model['data']['id']
            self.request('price ' + role, 'PUT', f"/api/v1/models/{models[role]}/pricing",
                {'valid_from': '2026-01-01T00:00:00Z', 'currency': 'CNY',
                 'input_microunits_per_million': 500000 if role == 'embedding' else 1000000,
                 'output_microunits_per_million': 0 if role == 'embedding' else 2000000}, 201)
        kb, _ = self.request('source knowledge base', 'POST', '/api/v1/knowledge-bases',
            {'name': 'X07 RAG fixed source', 'type': 'document', 'embedding_model_id': models['embedding'],
             'summary_model_id': '', 'indexing_strategy': {'vector_enabled': True, 'keyword_enabled': True,
                                                         'wiki_enabled': False, 'graph_enabled': False}}, 201)
        dataset, _ = self.request('dataset', 'POST', '/api/v1/evaluation/datasets',
            {'name': 'X07 RAG synthetic 20', 'description': 'offline-fixture; human_review_pending'})
        version, _ = self.request('immutable dataset version', 'POST',
            f"/api/v1/evaluation/datasets/{dataset['data']['id']}/versions", self.corpus)
        self.creation = {'dataset_id': dataset['data']['id'], 'dataset_version_id': version['data']['id'],
            'knowledge_base_id': kb['data']['id'], 'chat_id': models['chat'],
            'configuration': {'retrieval': {'vector_threshold': 0, 'keyword_threshold': 0, 'embedding_top_k': 6},
                              'rerank': {'rerank_top_k': 6, 'rerank_threshold': 0},
                              'generation': {'temperature': 0, 'max_tokens': 256}}, 'seed': 0}
        self.manifest.update(dataset_version=version['data'], creation=self.creation,
                             model_ids=models, tenant_id=self.tenant)
        dump(self.output / 'manifest.json', self.manifest)

    def run_round(self, number):
        self.supplier.round = number
        created, _ = self.request(f'create evaluation round {number}', 'POST', '/api/v1/evaluation', self.creation)
        task_id = created['data']['task']['id']
        for _ in range(600):
            detail, _ = self.request('poll', 'GET', '/api/v1/evaluation?task_id=' + task_id, record=False)
            detail = detail['data']
            if detail['task']['status'] not in (0, 1):
                break
            assert not self.supplier.errors, self.supplier.errors
            time.sleep(0.2)
        dump(self.output / f'round-{number}-detail.json', detail)
        assert detail['task']['status'] == 2, detail['task']
        assert detail['task']['finished'] == detail['task']['total'] == 20
        assert detail['experiment']['configuration']['chunking'] == self.manifest['chunking']
        questions, _ = self.request('question results', 'GET',
            f'/api/v1/evaluation/tasks/{task_id}/questions?page_size=500')
        questions = questions['data']['items']
        assert len(questions) == 20 and all(q['status'] == 'success' for q in questions)
        exported, raw = self.request('JSON export', 'GET', f'/api/v1/evaluation/tasks/{task_id}/export?format=json')
        (self.output / f'round-{number}.json').write_bytes(raw)
        _, csv_raw = self.request('CSV export', 'GET', f'/api/v1/evaluation/tasks/{task_id}/export?format=csv')
        (self.output / f'round-{number}.csv').write_bytes(csv_raw)
        csv_rows = list(csv.DictReader(io.StringIO(csv_raw.decode('utf-8-sig'))))
        csv_runs = [r for r in csv_rows if r['record_type'] == 'run']
        csv_questions = [r for r in csv_rows if r['record_type'] == 'question']
        assert len(csv_runs) == 1 and len(csv_questions) == 20 and exported['schema_version'] == 3
        assert exported['runtime_metrics'] == detail['runtime_metrics']
        assert exported['aggregate_metrics'] == detail['metric']
        assert json.loads(csv_runs[0]['runtime_metrics_json']) == detail['runtime_metrics']
        assert json.loads(csv_runs[0]['aggregate_metrics_json']) == detail['metric']
        for metric, minimum in RETRIEVAL_MINIMUMS.items():
            actual = detail['metric']['retrieval_metrics'][metric]
            assert math.isfinite(actual) and actual + 1e-12 >= minimum, (
                f'Round {number} retrieval regression: {metric}={actual:.15g} below frozen minimum {minimum:.15g}')
        for index, (api, ex, row) in enumerate(zip(questions, exported['questions'], csv_questions)):
            assert api['sample_index'] == ex['sample_index'] == int(row['sample_index']) == index
            for field in ('qid', 'generated_text', 'per_sample_metrics', 'metric_observations', 'search_results'):
                assert api[field] == ex[field] == json.loads(row[field + '_json']), (index, field)
            for field in ('prompt_tokens', 'completion_tokens', 'total_tokens', 'retrieval_ms', 'generation_ms', 'total_ms'):
                assert api[field] is not None and api[field] == ex[field] == int(row[field]), (index, field)
            assert api['usage_reported'] and api['search_results'] and api['metric_observations']
        with sqlite3.connect(f'file:{self.database}?mode=ro', uri=True) as db:
            db.row_factory = sqlite3.Row
            task = dict(db.execute('SELECT * FROM evaluation_tasks WHERE id=?', (task_id,)).fetchone())
            ledger = [dict(r) for r in db.execute('SELECT * FROM model_call_records WHERE evaluation_task_id=? ORDER BY started_at,id', (task_id,))]
            cache = dict(db.execute('SELECT COUNT(*) entries FROM embedding_cache_entries').fetchone())
            assert task['status'] == 2 and task['finished'] == 20
            assert json.loads(task['runtime_metrics']) == detail['runtime_metrics']
            assert json.loads(task['metric']) == detail['metric']
            persisted = [dict(r) for r in db.execute(
                'SELECT * FROM evaluation_question_results WHERE task_id=? ORDER BY sample_index', (task_id,))]
            assert len(persisted) == 20
            for api, stored in zip(questions, persisted):
                for field in ('qid', 'generated_text', 'status', 'result_hash', 'prompt_tokens',
                              'completion_tokens', 'total_tokens', 'retrieval_ms', 'generation_ms', 'total_ms'):
                    assert api[field] == stored[field], ('persistence', field)
                for field in ('per_sample_metrics', 'metric_observations', 'search_results'):
                    assert api[field] == json.loads(stored[field]), ('persistence', field)
            assert all(r['status'] == 'success' and r['accounting_complete'] == 1 for r in ledger)
            assert all(r['cost_microunits'] == (r['prompt_tokens'] * r['input_microunits_per_million'] +
                r['completion_tokens'] * r['output_microunits_per_million'] + 500000) // 1000000 for r in ledger)
        attempts = [r for r in self.supplier.records if r['round'] == number]
        by_operation = collections.Counter(r['operation'] for r in attempts)
        assert len(attempts) == len(ledger) == detail['runtime_metrics']['cost']['call_count']
        assert detail['runtime_metrics']['cost']['accounting_complete_calls'] == len(ledger)
        assert detail['runtime_metrics']['cost']['totals'] == [{'currency': 'CNY', 'cost_microunits': sum(r['cost_microunits'] for r in ledger)}]
        assert by_operation['chat'] == 20
        assert sum(r['usage']['prompt_tokens'] for r in attempts) == sum(r['prompt_tokens'] for r in ledger)
        assert sum(r['usage'].get('completion_tokens', 0) for r in attempts) == sum(r['completion_tokens'] for r in ledger)
        server_log = (self.output / f'server-round-{number}.log').read_text(errors='replace')
        keyword_count = len(re.findall(r'keywordsRetrieve: query=', server_log))
        vector_count = len(re.findall(r'vectorRetrieve: query_dim=', server_log))
        assert keyword_count >= 20 and vector_count >= 20, (keyword_count, vector_count)
        result = {'round': number, 'task_id': task_id, 'attempts': dict(by_operation), 'ledger_calls': len(ledger),
                  'ledger_cost_microunits': sum(r['cost_microunits'] for r in ledger), 'persistent_cache': cache,
                  'keyword_queries': keyword_count, 'vector_queries': vector_count,
                  'runtime_metrics': detail['runtime_metrics'], 'metric': detail['metric'],
                  'experiment_sha256': task['experiment_sha256']}
        dump(self.output / f'round-{number}-ledger.json', ledger)
        self.manifest['rounds'].append(result)
        dump(self.output / 'manifest.json', self.manifest)

    def execute(self):
        self.supplier.start()
        try:
            self.start(1)
            self.register()
            self.run_round(1)
            self.stop()
            self.start(2)
            old_id = self.manifest['rounds'][0]['task_id']
            restored, _ = self.request('persisted success after restart', 'GET', '/api/v1/evaluation?task_id=' + old_id)
            assert restored['data']['task']['status'] == 2
            assert restored['data']['runtime_metrics'] == self.manifest['rounds'][0]['runtime_metrics']
            assert restored['data']['metric'] == self.manifest['rounds'][0]['metric']
            self.run_round(2)
            first, second = self.manifest['rounds']
            assert first['experiment_sha256'] == second['experiment_sha256']
            assert first['metric'] == second['metric'], 'Identical corpus rebuild changed quality metrics'
            exports = [json.loads((self.output / f'round-{n}.json').read_text(encoding='utf-8')) for n in (1, 2)]
            for cold, warm in zip(exports[0]['questions'], exports[1]['questions']):
                for field in ('qid', 'per_sample_metrics', 'metric_observations', 'search_results',
                              'rerank_results', 'generation_pids', 'generated_text'):
                    assert cold[field] == warm[field], ('rebuild', cold['qid'], field)
            assert first['attempts'].get('embedding', 0) > 0 and second['attempts'].get('embedding', 0) == 0
            assert first['persistent_cache'] == second['persistent_cache']
            assert not self.supplier.errors
            self.manifest['quality_metrics_equal_after_rebuild'] = True
            self.manifest['status'] = 'passed'
        finally:
            self.stop()
            self.supplier.close()
            self.manifest['supplier_errors'] = self.supplier.errors
            dump(self.output / 'manifest.json', self.manifest)
        return self.manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True, help='fresh artifact directory; parent must exist')
    parser.add_argument('--server-binary', type=Path, help='existing sqlite_fts5 server; omit to build current source')
    parser.add_argument('--port', type=int, default=18708)
    parser.add_argument('--supplier-port', type=int, default=18710)
    args = parser.parse_args()
    if not __debug__:
        parser.error('Run without -O: regression assertions are required')
    assert args.port != args.supplier_port and 1024 <= args.port <= 65535 and 1024 <= args.supplier_port <= 65535
    result = Regression(args).execute()
    print(json.dumps({'mode': result['mode'], 'status': result['status'], 'paid_provider_requests': 0,
                      'round_attempts': [r['attempts'] for r in result['rounds']], 'output': str(args.output)}))


if __name__ == '__main__':
    main()
