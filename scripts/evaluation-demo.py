#!/usr/bin/env python3
"""Run the real evaluation workbench backend with explicitly free test models.

The model transport stays on loopback. A new SQLite database is required, saved
application settings are not loaded, and no paid credentials are accepted.
"""
import argparse
import hashlib
import http.server
import importlib.util
import json
from pathlib import Path
import signal
import threading

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('evaluation_http_fixture', ROOT / 'scripts/evaluation-rag-http-smoke.py')
FIXTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FIXTURE)
EMAIL = 'demo@topic3.example.test'
PASSWORD = 'Topic3-demo-2026!'


class DemoSupplier:
    """Fixed answers and hash vectors; observations are demonstration data."""

    def __init__(self, port, output):
        self.output = output
        self.questions = []
        self.lock = threading.Lock()
        self.records = []
        self.errors = []
        for path in [ROOT / 'dataset/synthetic-campus/v1/registry-input.json',
                     *sorted((ROOT / 'dataset/public').glob('*/v1/registry-input.json'))]:
            self.questions.extend(json.loads(path.read_bytes())['questions'])
        self.questions.sort(key=lambda question: len(question['question']), reverse=True)
        supplier = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_CONNECT(self):
                self.send_error(403, 'Demo model never forwards requests')

            def do_POST(self):
                try:
                    if self.client_address[0] != '127.0.0.1':
                        raise ValueError('Loopback requests only')
                    if self.headers.get('Authorization') != 'Bearer ' + FIXTURE.FIXTURE_KEY:
                        raise ValueError('Demo model credential required')
                    length = int(self.headers.get('Content-Length', 0))
                    if not 0 < length <= 2_000_000:
                        raise ValueError('Demo request size limit')
                    payload = json.loads(self.rfile.read(length))
                    if self.path == '/v1/embeddings' and payload.get('model') == 'x07-fixture-embedding':
                        inputs = payload.get('input')
                        if not isinstance(inputs, list) or not 1 <= len(inputs) <= 100 or any(
                                not isinstance(text, str) for text in inputs):
                            raise ValueError('Expected a bounded text batch')
                        usage = {'prompt_tokens': sum(max(1, len(text.encode()) // 4) for text in inputs)}
                        usage['total_tokens'] = usage['prompt_tokens']
                        response = {'model': payload['model'], 'data': [
                            {'index': i, 'embedding': FIXTURE.vector(text)} for i, text in enumerate(inputs)],
                            'usage': usage}
                        operation = 'embedding'
                    elif self.path == '/v1/chat/completions' and payload.get('model') == 'x07-fixture-chat':
                        if payload.get('stream') or payload.get('tools'):
                            raise ValueError('This demo supports evaluation requests only')
                        text = payload['messages'][-1]['content']
                        match = next((q for q in supplier.questions if q['question'] in text), None)
                        answer = ((match['answer'] or 'The supplied reference context does not provide an answer.')
                                  if match else '本地固定演示回答：此结果用于检查流程，请勿作为真实模型效果。')
                        usage = {'prompt_tokens': max(1, len(json.dumps(payload).encode()) // 4),
                                 'completion_tokens': max(1, len(answer.encode()) // 4),
                                 'prompt_tokens_details': {'cached_tokens': 0}}
                        usage['total_tokens'] = usage['prompt_tokens'] + usage['completion_tokens']
                        response = {'id': 'local-demonstration', 'model': payload['model'], 'choices': [
                            {'index': 0, 'finish_reason': 'stop',
                             'message': {'role': 'assistant', 'content': answer}}], 'usage': usage}
                        operation = 'chat'
                    else:
                        raise ValueError('Unknown demo model or endpoint')
                    with supplier.lock:
                        record = {'mode': 'offline-fixture', 'operation': operation, 'usage': usage,
                                  'request_sha256': hashlib.sha256(json.dumps(payload).encode()).hexdigest()}
                        supplier.records.append(record)
                        with (output / 'supplier.jsonl').open('a', encoding='utf-8') as stream:
                            stream.write(json.dumps(record) + '\n')
                    raw = json.dumps(response, ensure_ascii=False).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except (ValueError, KeyError, TypeError, IndexError) as error:
                    with supplier.lock:
                        supplier.errors.append(str(error))
                    self.send_error(400, 'Invalid local demo model request')

        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', port), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def start(self):
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)


def seed(instance):
    instance.request('register demo administrator', 'POST', '/api/v1/auth/register',
                     {'username': '课题三本地演示', 'email': EMAIL, 'password': PASSWORD}, 201)
    login, _ = instance.request('login', 'POST', '/api/v1/auth/login', {'email': EMAIL, 'password': PASSWORD})
    instance.token = login['token']
    models = {}
    for role, kind, display in [('embedding', 'Embedding', '本地演示嵌入（免费）'),
                                ('chat', 'KnowledgeQA', '本地固定回答（免费演示）')]:
        parameters = {'base_url': f'http://127.0.0.1:{instance.args.supplier_port}/v1',
                      'api_key': FIXTURE.FIXTURE_KEY, 'provider': 'generic', 'interface_type': 'openai',
                      'max_concurrency': 1}
        if role == 'embedding':
            parameters['embedding_parameters'] = {'dimension': FIXTURE.DIMENSION,
                'truncate_prompt_tokens': 511, 'supports_dimension_override': True}
        else:
            parameters.update(context_window=32768, max_output_tokens=256,
                              extra_config={'thinking_control': 'none'})
        result, _ = instance.request('create demo ' + role, 'POST', '/api/v1/models',
            {'name': 'x07-fixture-' + role, 'display_name': display, 'type': kind, 'source': 'remote',
             'description': '本地固定夹具，无外部模型调用；数据用于流程演示。', 'parameters': parameters}, 201)
        models[role] = result['data']['id']
        instance.request('demo zero price', 'PUT', f"/api/v1/models/{models[role]}/pricing",
                         {'valid_from': '2026-01-01T00:00:00Z', 'currency': 'CNY',
                          'input_microunits_per_million': 0, 'output_microunits_per_million': 0}, 201)
    knowledge_base, _ = instance.request('demo source knowledge base', 'POST', '/api/v1/knowledge-bases',
        {'name': '公开数据评测 · 本地演示', 'type': 'document', 'embedding_model_id': models['embedding'],
         'summary_model_id': '', 'indexing_strategy': {'vector_enabled': True, 'keyword_enabled': True,
                                                     'wiki_enabled': False, 'graph_enabled': False}}, 201)
    return {'model_ids': models, 'knowledge_base_id': knowledge_base['data']['id'],
            'tenant_id': login['active_tenant']['id']}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True, help='new runtime directory; parent must exist')
    parser.add_argument('--server-binary', type=Path, help='sqlite_fts5 binary; omitted means build current source')
    parser.add_argument('--port', type=int, default=19080)
    parser.add_argument('--supplier-port', type=int, default=19081)
    parser.add_argument('--container-listen', action='store_true', help='bind API to 0.0.0.0 inside an isolated container')
    args = parser.parse_args()
    if not __debug__ or args.port == args.supplier_port or any(
            not 1024 <= value <= 65535 for value in (args.port, args.supplier_port)):
        parser.error('Assertions and distinct unprivileged ports are required')
    instance = FIXTURE.Regression(args)
    instance.supplier.server.server_close()
    instance.supplier = DemoSupplier(args.supplier_port, instance.output)
    if args.container_listen:
        instance.env['SERVER_HOST'] = '0.0.0.0'
    stop = threading.Event()
    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, lambda *_: stop.set())
    instance.supplier.start()
    try:
        instance.start('demo')
        identity = seed(instance)
        manifest = {'mode': 'offline-fixture', 'paid_provider_requests': 0, 'identity': identity,
                    'server_sha256': instance.manifest['server_sha256'], 'api_url': instance.base,
                    'database': str(instance.database), 'credentials': {'email': EMAIL, 'password': PASSWORD},
                    'limitation': 'Fixed answers/hash vectors demonstrate workflow, not real model quality.'}
        FIXTURE.dump(instance.output / 'manifest.json', manifest)
        FIXTURE.dump(instance.output / 'demo.json', manifest)
        print(json.dumps({'status': 'ready', 'mode': 'offline-fixture', 'api_url': instance.base,
                          'email': EMAIL, 'password': PASSWORD, 'output': str(instance.output)}), flush=True)
        while not stop.wait(1):
            if instance.process.poll() is not None:
                raise RuntimeError('Demo server exited; inspect the server log')
    finally:
        instance.stop()
        instance.supplier.close()
        FIXTURE.dump(instance.output / 'demo-transport-summary.json',
                     {'paid_provider_requests': 0, 'local_requests': len(instance.supplier.records),
                      'errors': instance.supplier.errors})


if __name__ == '__main__':
    main()
