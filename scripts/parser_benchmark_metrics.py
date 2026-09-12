"""Read completed, hash-verified official metric artifacts for presentation."""
from __future__ import annotations

import hashlib
import json
import math
from pathlib import Path


def _read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def _hash(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _number(value):
    return value if isinstance(value, (int, float)) and math.isfinite(value) else None


def _at(value, *keys):
    for key in keys:
        value = value.get(key) if isinstance(value, dict) else None
    return value


def load_omni_metrics(run: Path) -> dict:
    folder = run / 'evaluation/official-omnidocbench'
    receipt_file = folder / 'execution-receipts.json'
    if not receipt_file.exists():
        return {}
    receipts = _read(receipt_file)
    snapshot_file = run / 'evaluation/summary.json'
    snapshot = _read(snapshot_file) if snapshot_file.exists() else {}
    exports = {row['engine']: row for row in snapshot.get('official_omnidocbench_exports', [])}
    audit_file = folder / 'denominator-audit.json'
    audited = _read(audit_file).get('engines', {}) if audit_file.exists() else {}
    results = {}
    for engine, receipt in receipts.items():
        if receipt.get('status') != 'completed':
            continue
        export = exports.get(engine)
        if not export or not export.get('ready_for_official_scoring'):
            continue
        config = folder / f'{engine}-standard.yaml'
        input_manifest = folder / f'{engine}-input-manifest.json'
        inputs = [('ground_truth.json', _hash(folder / 'ground_truth.json')), (config.name, _hash(config)),
                  (input_manifest.name, _hash(input_manifest))]
        inputs += [(path.name, _hash(path)) for path in sorted((folder / engine).glob('*.md'))]
        fingerprint = hashlib.sha256(json.dumps([inputs, receipt['source_lock_sha256']], ensure_ascii=False).encode()).hexdigest()
        if fingerprint != receipt.get('fingerprint'):
            raise ValueError('Official score input fingerprint mismatch; rerun scoring: ' + engine)
        path = folder / 'result' / f'{engine}_quick_match_metric_result.json'
        content = path.read_bytes()
        if hashlib.sha256(content).hexdigest() != receipt['metric_result_sha256']:
            raise ValueError('Official score hash mismatch: ' + engine)
        metrics = json.loads(content)
        run_summary_path = folder / 'result' / f'{engine}_quick_match_run_summary.json'
        run_info = _read(run_summary_path)
        denominators = run_info['page_denominators']
        coverage = audited.get(engine, {}).get('metrics', {})
        def pages(kind, metric):
            # Expose the true computed population when the upstream report count differs.
            actual = coverage.get(kind, {}).get('actual_metric_page_denominator')
            return actual if actual is not None else _at(denominators, kind, metric, 'ALL')
        formula_cdm = _number(_at(metrics, 'display_formula', 'page', 'CDM', 'ALL'))
        results[engine] = {
            'text_edit_distance': _number(_at(metrics, 'text_block', 'all', 'Edit_dist', 'ALL_page_avg')),
            'table_teds_page_mean': _number(_at(metrics, 'table', 'page', 'TEDS', 'ALL')),
            'table_teds_structure_page_mean': _number(_at(metrics, 'table', 'page', 'TEDS_structure_only', 'ALL')),
            'reading_order_edit_distance': _number(_at(metrics, 'reading_order', 'all', 'Edit_dist', 'ALL_page_avg')),
            'formula_string_edit_distance': _number(_at(metrics, 'display_formula', 'all', 'Edit_dist', 'ALL_page_avg')),
            'text_pages': pages('text_block', 'Edit_dist'),
            'table_pages': pages('table', 'TEDS'),
            'reading_order_pages': pages('reading_order', 'Edit_dist'),
            'formula_pages': pages('display_formula', 'Edit_dist'),
            'reported_page_denominators': denominators,
            'denominator_audit': coverage,
            'metric_result_sha256': receipt['metric_result_sha256'],
            'metric_result_path': str(path),
            'run_summary_sha256': _hash(run_summary_path),
            'input_fingerprint': fingerprint,
            'formula_cdm': formula_cdm,
            'cdm_executed': formula_cdm is not None,
        }
    return results
