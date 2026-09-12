#!/usr/bin/env python3
"""Bounded real-provider acceptance through the actual WeKnora HTTP service.

Run in an isolated Linux container. Credentials arrive on stdin, never enter
the application database or evidence. All paid requests cross one budget relay.
"""
import argparse
import collections
import csv
import fcntl
import gzip
from decimal import Decimal, ROUND_HALF_UP
import hashlib
import http.server
import importlib.util
import io
import json
import os
from pathlib import Path
import secrets
import sqlite3
import threading
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
if not __debug__:
    raise RuntimeError('Run without -O: budget and evidence assertions are required')
spec = importlib.util.spec_from_file_location('offline_regression', ROOT / 'scripts/evaluation-rag-http-smoke.py')
offline = importlib.util.module_from_spec(spec)
spec.loader.exec_module(offline)
CHAT = 'deepseek/deepseek-v4-flash'
COMPARE = 'moonshotai/kimi-k2.5'
EMBED = 'qwen/qwen3-embedding-8b'
ALLOW = {CHAT, COMPARE, EMBED, 'qwen/qwen3.8-flash', 'deepseek/deepseek-v4-pro'}

def save(path, data):
    offline.dump(path, data)

def validate_paid_ledger(records, attempts):
    """Reconcile successful receipts and retain explicitly unknown failed costs."""
    assert len(attempts) == len(records), 'Every physical request must have a ledger attempt'
    available = collections.Counter((r['model'], Decimal(str(r['usage']['cost']))) for r in attempts
                                    if r['status'] == 200 and r.get('usage', {}).get('cost') is not None)
    unreported = collections.Counter(r['model'] for r in attempts
                                    if r['status'] == 200 and r.get('usage', {}).get('cost') is None)
    total, unknown, failed = 0, 0, 0
    for record in records:
        failed += record['status'] != 'success'
        if not record['accounting_complete']:
            assert record['cost_microunits'] is None, 'Unknown cost must remain null'
            if record['status'] == 'success':
                snapshot = json.loads(record['model_snapshot'])
                assert unreported[snapshot['name']] > 0, 'Unknown successful call requires an unreported supplier receipt'
                assert not snapshot.get('billing_usage', {}).get('usage_reported'), 'Reported usage must reconcile'
                unreported[snapshot['name']] -= 1
            unknown += 1
            continue
        snapshot = json.loads(record['model_snapshot'])
        amount = Decimal(snapshot['billing_usage']['reported_cost'])
        expected = int((amount * 1000000).quantize(Decimal(1), rounding=ROUND_HALF_UP))
        assert record['cost_microunits'] == expected
        assert snapshot['billing_usage']['cost_source'] == 'openrouter_usage_cost'
        key = (snapshot['name'], amount)
        assert available[key] > 0, 'Priced ledger has no matching supplier receipt'
        available[key] -= 1
        total += expected
    return {'known_cost_microunits': total, 'unknown_cost_attempts': unknown, 'failed_attempts': failed}

class BudgetRelay:
    def __init__(self, port, key, evidence, budget_path):
        self.key, self.evidence, self.budget_path = key, evidence, budget_path
        self.budget_guard = budget_path.with_suffix('.lock').open('a')
        fcntl.flock(self.budget_guard, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self.lock, self.records, self.errors, self.round = threading.Lock(), [], [], ''
        self.provider_only = None
        self.budget = json.loads(budget_path.read_text()) if budget_path.exists() else {'limit_usd': 20, 'requests': []}
        assert self.budget['limit_usd'] == 20
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        relay = self
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_): pass
            def do_CONNECT(self): self.send_error(403, 'Only approved model requests are allowed')
            def do_POST(self):
                try:
                    assert self.client_address[0] == '127.0.0.1'
                    assert self.headers.get('Authorization') == 'Bearer acceptance-relay-placeholder'
                    size = int(self.headers.get('Content-Length', 0))
                    assert 0 < size <= 100_000
                    payload = json.loads(self.rfile.read(size))
                    status, raw = relay.forward(self.path, payload)
                    self.send_response(status)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except Exception as error:
                    relay.errors.append(type(error).__name__ + ': ' + str(error).replace(relay.key, '[redacted]'))
                    self.send_error(502, 'Acceptance relay rejected or failed request; inspect sanitized evidence')
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', port), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
    def start(self): self.thread.start()
    def request_round(self, payload):
        """Capture attribution before network I/O; concurrent runners override this."""
        return self.round
    def close(self):
        self.server.shutdown(); self.server.server_close(); self.thread.join()
        self.budget_guard.close()
    def forward(self, path, payload):
        request_round = self.request_round(payload)
        assert path in ('/v1/chat/completions', '/v1/embeddings')
        model = payload['model']
        assert model in ALLOW and not payload.get('tools') and not payload.get('stream')
        if path.endswith('chat/completions'):
            assert 0 < payload.get('max_tokens', 0) <= 512
            payload['provider'] = {'allow_fallbacks': True, 'max_price': {'prompt': 1, 'completion': 5}}
            if self.provider_only:
                payload['provider'].update(only=[self.provider_only], allow_fallbacks=False)
            payload['reasoning'] = {'enabled': False}
        raw = json.dumps(payload, ensure_ascii=False).encode()
        assert len(raw) <= 100_000
        # UTF-8 bytes upper-bound token count; reserve input, possible cache
        # writing and maximum output. Uncertain calls retain their reservation.
        reserve = Decimal(len(raw) + 512) * Decimal('0.000002') + Decimal('0.005')
        with self.lock:
            used = sum(Decimal(str(r.get('actual_usd', r['reserved_usd']))) for r in self.budget['requests'])
            assert used + reserve < Decimal('20'), 'Cumulative USD budget exhausted'
            assert len(self.budget['requests']) < 5000, 'Request count ceiling reached'
            reservation = {'sequence': len(self.budget['requests']) + 1, 'round': request_round, 'path': path,
                           'model': model, 'reserved_usd': str(reserve), 'state': 'reserved'}
            self.budget['requests'].append(reservation)
            save(self.budget_path, self.budget)
        start = time.monotonic()
        req = urllib.request.Request('https://openrouter.ai/api' + path, raw,
              {'Authorization': 'Bearer ' + self.key, 'Content-Type': 'application/json', 'Accept-Encoding': 'gzip'})
        try:
            try:
                response = self.opener.open(req, timeout=90)
            except urllib.error.HTTPError as error:
                response = error
            encoded = response.read()
            if getattr(response, 'headers', {}).get('Content-Encoding') == 'gzip':
                encoded = gzip.decompress(encoded)
            data = json.loads(encoded)
        except Exception as error:
            # An incomplete response is still a paid attempt. Preserve its full
            # reservation and attribution even when the provider cost is unknown.
            record = {'sequence': reservation['sequence'], 'round': request_round, 'model': model, 'path': path,
                      'status': None, 'usage': {}, 'request_sha256': offline.digest(raw),
                      'elapsed_ms': round((time.monotonic()-start)*1000), 'transport_error': type(error).__name__}
            with self.lock:
                reservation['state'] = 'uncertain'
                save(self.budget_path, self.budget)
                self.records.append(record)
                with (self.evidence / 'supplier.jsonl').open('a', encoding='utf-8') as stream:
                    stream.write(json.dumps(record) + '\n')
            raise
        usage = data.get('usage', {})
        record = {'sequence': reservation['sequence'], 'round': request_round, 'model': model, 'path': path,
                  'status': response.status, 'provider_request_id': data.get('id'), 'provider': data.get('provider'),
                  'request_sha256': offline.digest(raw), 'usage': usage, 'elapsed_ms': round((time.monotonic()-start)*1000)}
        if path.endswith('embeddings') and data.get('data'):
            record['input_sha256'] = [offline.digest(s.encode()) for s in payload['input']]
            record['vector_sha256'] = [offline.digest(json.dumps(x['embedding']).encode()) for x in data['data']]
            record['dimensions'] = [len(x['embedding']) for x in data['data']]
        if data.get('error'):
            record['error'] = json.loads(json.dumps(data['error']).replace(self.key, '[redacted]'))
        with self.lock:
            reservation['state'] = 'response'
            if usage.get('cost') is not None:
                actual = Decimal(str(usage['cost']))
                assert 0 <= actual <= reserve, 'Provider cost exceeded conservative reservation'
                reservation['actual_usd'] = str(actual)
            save(self.budget_path, self.budget)
            self.records.append(record)
            with (self.evidence / 'supplier.jsonl').open('a', encoding='utf-8') as f:
                f.write(json.dumps(record, ensure_ascii=False) + '\n')
        return response.status, encoded

class Acceptance(offline.Regression):
    def __init__(self, args, key):
        super().__init__(args)
        self.supplier.server.server_close()
        self.supplier = BudgetRelay(args.supplier_port, key, self.output, args.budget_file)
        self.manifest['mode'] = 'real-openrouter'
        self.manifest['embedding_dimension'] = 1024
        for field in ('question_count', 'passage_count', 'retrieval_minimums', 'corpus_sha256'):
            self.manifest.pop(field, None)
        self.manifest['human_review_status'] = 'pending; no machine scores presented as human ratings'
        self.env['GOLANG_PROTOBUF_REGISTRATION_CONFLICT'] = 'warn'
        self.manifest['rounds'] = []
        save(self.output / 'manifest.json', self.manifest)
        self.prices = {}
        for route in ('models', 'embeddings/models'):
            response = self.supplier.opener.open('https://openrouter.ai/api/v1/' + route, timeout=30)
            data = response.read()
            save(self.output / (route.replace('/', '-') + '-catalog.json'), json.loads(data))
            self.prices.update({m['id']: m['pricing'] for m in json.loads(data)['data'] if m['id'] in ALLOW})
    def register(self):
        password = secrets.token_urlsafe(18) + '9aA!'
        self.request('register', 'POST', '/api/v1/auth/register', {'username': 'Real acceptance', 'email': 'acceptance@example.test', 'password': password}, 201)
        login, _ = self.request('login', 'POST', '/api/v1/auth/login', {'email': 'acceptance@example.test', 'password': password})
        self.token, self.tenant = login['token'], login['active_tenant']['id']
        self.models = {}
        for name in (CHAT, COMPARE, EMBED):
            kind = 'Embedding' if name == EMBED else 'KnowledgeQA'
            params = {'base_url': f'http://127.0.0.1:{self.args.supplier_port}/v1', 'api_key': 'acceptance-relay-placeholder',
                      'provider': 'openrouter', 'max_concurrency': getattr(self, 'chat_concurrency', 1)}
            if name == EMBED:
                params['embedding_parameters'] = {'dimension': 1024, 'supports_dimension_override': True}
                params['max_concurrency'] = getattr(self, 'embedding_concurrency', 1)
            else:
                params.update(max_output_tokens=512, context_window=32768, extra_config={'thinking_control': 'none'})
            created, _ = self.request('model', 'POST', '/api/v1/models', {'name': name, 'type': kind, 'source': 'remote', 'parameters': params}, 201)
            mid = created['data']['id']; self.models[name] = mid
            price = self.prices[name]
            def micro(k): return int((Decimal(str(price[k])) * Decimal(10**12)).quantize(Decimal(1), rounding=ROUND_HALF_UP))
            body = {'valid_from': '2026-09-09T00:00:00Z', 'currency': 'USD', 'input_microunits_per_million': micro('prompt'), 'output_microunits_per_million': micro('completion')}
            if 'input_cache_read' in price:
                body['cache_pricing'] = {'version': 1, 'read_microunits_per_million': micro('input_cache_read')}
            self.request('price', 'PUT', f'/api/v1/models/{mid}/pricing', body, 201)
        kb, _ = self.request('source knowledge base', 'POST', '/api/v1/knowledge-bases',
            {'name': 'Real acceptance isolated source', 'type': 'document', 'embedding_model_id': self.models[EMBED],
             'summary_model_id': '', 'indexing_strategy': {'vector_enabled': True, 'keyword_enabled': True, 'wiki_enabled': False, 'graph_enabled': False}}, 201)
        self.kb = kb['data']['id']
        self.manifest.update(model_ids=self.models, tenant_id=self.tenant, knowledge_base_id=self.kb)
    def dataset(self, label, relative, question_limit=None):
        corpus = json.loads((ROOT / relative).read_text(encoding='utf-8'))
        if question_limit:
            corpus['questions'] = corpus['questions'][:question_limit]
            qids = {q['qid'] for q in corpus['questions']}
            corpus['relevance'] = [r for r in corpus['relevance'] if r['qid'] in qids]
        ds, _ = self.request('dataset', 'POST', '/api/v1/evaluation/datasets', {'name': label, 'description': 'Frozen public/synthetic labels; human review pending'})
        version, _ = self.request('dataset version', 'POST', f"/api/v1/evaluation/datasets/{ds['data']['id']}/versions", corpus)
        return {'dataset_id': ds['data']['id'], 'dataset_version_id': version['data']['id'], 'corpus': corpus, 'label': label}
    def run_live(self, label, ds, model):
        self.supplier.round = label
        creation = {'dataset_id': ds['dataset_id'], 'dataset_version_id': ds['dataset_version_id'], 'knowledge_base_id': self.kb,
                    'chat_id': self.models[model], 'configuration': {'retrieval': {'vector_threshold': 0, 'keyword_threshold': 0, 'embedding_top_k': 6},
                    'rerank': {'rerank_top_k': 6, 'rerank_threshold': 0}, 'generation': {'temperature': 0, 'max_tokens': 512}}, 'seed': 0}
        task, _ = self.request('create real evaluation', 'POST', '/api/v1/evaluation', creation)
        tid = task['data']['task']['id']
        last_progress = 0
        deadline = time.monotonic() + 1800
        while True:
            detail, _ = self.request('poll', 'GET', '/api/v1/evaluation?task_id=' + tid, record=False)
            detail = detail['data']
            if detail['task']['status'] not in (0, 1): break
            if time.monotonic() - last_progress >= 30:
                save(self.output / 'progress.json', {'label': label, 'task': detail['task']})
                last_progress = time.monotonic()
            assert time.monotonic() < deadline, 'Evaluation timeout'
            time.sleep(1)
        save(self.output / (label + '-detail.json'), detail)
        assert detail['task']['status'] == 2, detail['task']
        exported, raw = self.request('export JSON', 'GET', f'/api/v1/evaluation/tasks/{tid}/export?format=json')
        (self.output / (label + '.json')).write_bytes(raw)
        _, csvraw = self.request('export CSV', 'GET', f'/api/v1/evaluation/tasks/{tid}/export?format=csv')
        (self.output / (label + '.csv')).write_bytes(csvraw)
        runrow = next(r for r in csv.DictReader(io.StringIO(csvraw.decode('utf-8-sig'))) if r['record_type'] == 'run')
        assert json.loads(runrow['runtime_metrics_json']) == exported['runtime_metrics'] == detail['runtime_metrics']
        with sqlite3.connect(f'file:{self.database}?mode=ro', uri=True) as db:
            db.row_factory = sqlite3.Row
            records = [dict(r) for r in db.execute('SELECT * FROM model_call_records WHERE evaluation_task_id=? ORDER BY started_at,id', (tid,))]
            persisted = db.execute('SELECT runtime_metrics FROM evaluation_tasks WHERE id=?', (tid,)).fetchone()[0]
            assert json.loads(persisted) == detail['runtime_metrics']
        # A timed-out client can finish before its supplier socket settles.
        # Wait for those attempted requests to acquire a response/unknown record.
        settle_deadline = time.monotonic() + 180
        while any(r.get('round') == label and r['state'] == 'reserved' for r in self.supplier.budget['requests']):
            assert time.monotonic() < settle_deadline, 'Supplier attempts did not settle; reservations retained'
            time.sleep(1)
        attempts = [r for r in self.supplier.records if r['round'] == label]
        assert len(attempts) == len(records), ('request ledger count', len(attempts), len(records))
        accounting = validate_paid_ledger(records, attempts)
        total = accounting['known_cost_microunits']
        assert detail['runtime_metrics']['cost']['totals'] == [{'currency': 'USD', 'cost_microunits': total}]
        save(self.output / (label + '-ledger.json'), records)
        summary = {'label': label, 'task_id': tid, 'model': model, 'questions': len(exported['questions']),
                   'attempts': dict(collections.Counter(r['path'] for r in attempts)), 'cost_microunits': total,
                   'metric': detail['metric'], 'runtime_metrics': detail['runtime_metrics'], 'accounting': accounting}
        self.manifest['rounds'].append(summary)
        save(self.output / 'manifest.json', self.manifest)
        print(json.dumps({'completed': label, 'questions': summary['questions'], 'attempts': summary['attempts'], 'cost_usd': total/1e6}), flush=True)
        return exported
    def execute(self):
        self.supplier.start()
        try:
            self.start(1); self.register()
            cmrc = self.dataset('CMRC2018 frozen 24', 'dataset/public/cmrc2018-dev/v1/registry-input.json')
            squad = self.dataset('SQuAD2 frozen 24', 'dataset/public/squad2-dev/v1/registry-input.json')
            for name, ds in (('cmrc', cmrc), ('squad', squad)):
                for model, short in ((CHAT, 'deepseek'), (COMPARE, 'kimi')):
                    for attempt in range(3):
                        try:
                            self.run_live(name + '-' + short + (f'-retry{attempt}' if attempt else ''), ds, model)
                            break
                        except AssertionError as error:
                            if 'status code: 429' not in str(error) or attempt == 2: raise
                            time.sleep(20)
            # Five paired real model runs. Clearing only this isolated cache
            # controls cold state; each warm run follows an actual process restart.
            cache_ds = self.dataset('CMRC cache probe: 2 questions, 32 passages', 'dataset/public/cmrc2018-dev/v1/registry-input.json', 2)
            for pair in range(1, 6):
                self.stop()
                with sqlite3.connect(self.database) as db: db.execute('DELETE FROM embedding_cache_entries')
                self.start(pair*2)
                cold = self.run_live(f'cache-{pair}-cold', cache_ds, CHAT)
                self.stop(); self.start(pair*2+1)
                warm = self.run_live(f'cache-{pair}-warm', cache_ds, CHAT)
                cold_records = [r for r in self.supplier.records if r['round'] == f'cache-{pair}-cold' and r['path'].endswith('embeddings')]
                warm_records = [r for r in self.supplier.records if r['round'] == f'cache-{pair}-warm' and r['path'].endswith('embeddings')]
                assert cold_records and not warm_records
                assert [q['search_results'] for q in cold['questions']] == [q['search_results'] for q in warm['questions']]
            self.manifest['status'] = 'passed'
        finally:
            self.stop(); self.supplier.close()
            self.manifest['supplier_errors'] = self.supplier.errors
            self.manifest['paid_provider_requests'] = len(self.supplier.records)
            save(self.output / 'manifest.json', self.manifest)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--budget-file', type=Path, required=True)
    parser.add_argument('--server-binary', type=Path, required=True)
    parser.add_argument('--port', type=int, default=18808)
    parser.add_argument('--supplier-port', type=int, default=18810)
    args = parser.parse_args()
    credentials = json.loads(input())
    assert credentials['approved_usd'] == 20
    Acceptance(args, credentials['openrouter_key']).execute()
