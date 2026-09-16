"""Real HTTP evaluation over larger frozen corpora, using the shared USD guard."""
import argparse
import csv
import importlib.util
import json
import sqlite3
from pathlib import Path
import time

spec = importlib.util.spec_from_file_location('acceptance', Path(__file__).with_name('evaluation-openrouter-acceptance.py'))
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)


class Expanded(module.Acceptance):
    def __init__(self, args, key):
        super().__init__(args, key)
        self.env.update(CONCURRENCY_POOL_SIZE='4', BATCH_EMBED_SIZE='5', GOMAXPROCS='5')
        self.embedding_concurrency = 4
        self.chat_concurrency = 4
        # WAL and shared-memory locking must remain on the Linux filesystem.
        # Export a consistent SQLite backup after the application stops.
        self.database = Path('/tmp/weknora-expanded.sqlite')
        assert not self.database.exists(), 'Use a fresh isolated container'
        self.env['DB_PATH'] = str(self.database)
        self.manifest['concurrency_pool_size'] = 4
        self.manifest['embedding_batch_size'] = 5
        self.manifest['question_workers'] = 4

    def import_completed(self, directory):
        label = 'cmrc-deepseek'
        detail = json.loads((directory / (label + '-detail.json')).read_text())
        exported = json.loads((directory / (label + '.json')).read_text())
        assert detail['task']['status'] == 2 and len(exported['questions']) == 50
        tid = detail['task']['id']
        with (directory / (label + '.csv')).open(encoding='utf-8-sig', newline='') as stream:
            row = next(r for r in csv.DictReader(stream) if r['record_type'] == 'run')
        assert json.loads(row['runtime_metrics_json']) == exported['runtime_metrics'] == detail['runtime_metrics']
        with sqlite3.connect(f'file:{directory / "x07-rag.sqlite"}?mode=ro', uri=True) as db:
            db.row_factory = sqlite3.Row
            records = [dict(r) for r in db.execute('SELECT * FROM model_call_records WHERE evaluation_task_id=? ORDER BY started_at,id', (tid,))]
            assert json.loads(db.execute('SELECT runtime_metrics FROM evaluation_tasks WHERE id=?', (tid,)).fetchone()[0]) == detail['runtime_metrics']
        receipts = [json.loads(line) for line in (directory / 'supplier.jsonl').read_text().splitlines()]
        receipts = [r for r in receipts if r['round'] == label]
        accounting = module.validate_paid_ledger(records, receipts)
        assert detail['runtime_metrics']['cost']['totals'] == [{'currency': 'USD', 'cost_microunits': accounting['known_cost_microunits']}]
        module.save(directory / (label + '-ledger.json'), records)
        summary = {'label': label, 'task_id': tid, 'model': module.CHAT, 'questions': 50,
                   'cost_microunits': accounting['known_cost_microunits'], 'metric': detail['metric'],
                   'runtime_metrics': detail['runtime_metrics'], 'accounting': accounting,
                   'evidence_directory': directory.name, 'question_workers': 1}
        module.save(directory / 'completed-run-audit.json', summary)
        self.manifest['rounds'].append(summary)

    def execute(self):
        self.supplier.start()
        try:
            self.start(1); self.register()
            if self.args.completed_run:
                self.import_completed(self.args.completed_run)
            for dataset in ('cmrc', 'squad'):
                fixture = self.dataset(dataset + ': 50 questions / 500 passages', self.args.data / (dataset + '-retrieval-500.json'))
                for model, label in [(module.CHAT, 'deepseek'), (module.COMPARE, 'kimi')]:
                    if any(r['label'] == dataset + '-' + label for r in self.manifest['rounds']):
                        continue
                    for attempt in range(3):
                        try:
                            self.run_live(dataset + '-' + label + (f'-retry{attempt}' if attempt else ''), fixture, model)
                            break
                        except AssertionError as error:
                            if not any(code in str(error) for code in ('status code: 429', 'status code: 502', 'status code: 503')) or attempt == 2:
                                raise
                            time.sleep(10)
            self.manifest['status'] = 'passed'
            self.manifest['label_scope'] = 'Original question context relevance; answerable questions only. Other semantically relevant passages may exist in the corpus.'
            self.manifest['human_review_status'] = 'excluded by user'
        finally:
            self.stop(); self.supplier.close()
            if self.database.exists():
                with sqlite3.connect(self.database) as source, sqlite3.connect(self.output / 'x07-rag.sqlite') as target:
                    source.backup(target)
            self.manifest['supplier_errors'] = self.supplier.errors
            self.manifest['paid_provider_requests'] = len(self.supplier.records)
            module.save(self.output / 'manifest.json', self.manifest)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--data', type=Path, required=True)
    parser.add_argument('--budget-file', type=Path, required=True)
    parser.add_argument('--server-binary', type=Path, required=True)
    parser.add_argument('--completed-run', type=Path, help='Audit and retain a completed CMRC/DeepSeek run without repeating paid answers')
    parser.add_argument('--port', type=int, default=18808)
    parser.add_argument('--supplier-port', type=int, default=18810)
    args = parser.parse_args(); credentials = json.loads(input())
    assert credentials['approved_usd'] == 20
    Expanded(args, credentials['openrouter_key']).execute()
