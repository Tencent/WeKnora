#!/usr/bin/env python3
"""Compare measured candidates only after hard gates; never select by size/claims."""
import argparse, hashlib, json, math
from pathlib import Path
GATES = ('real_device', 'offline', 'authorization', 'critical_semantics', 'citations', 'stability')
IDENTITY = ('dataset_sha256', 'prompt_sha256', 'kb_snapshot_sha256', 'device_identity', 'thinking', 'context', 'concurrency', 'cache_class')
def assess(candidate):
    missing = [k for k in GATES if candidate.get('gates', {}).get(k) is not True]
    evidence = candidate.get('evidence', {})
    for k in ('binary_sha256', 'model_sha256', *IDENTITY[:3]):
        value = evidence.get(k)
        if not isinstance(value, str) or len(value) != 64 or any(c not in '0123456789abcdef' for c in value):
            missing.append('identity:' + k)
    receipt = candidate.get('measurement_receipt')
    try:
        path = Path(receipt)
        if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != candidate.get('measurement_receipt_sha256'):
            missing.append('measurement_receipt_integrity')
    except (OSError, TypeError, ValueError):
        missing.append('measurement_receipt_integrity')
    metrics = candidate.get('metrics', {})
    for k in ('task_success_rate', 'citation_precision', 'ttft_p95_s', 'peak_host_rss_mib', 'samples'):
        v = metrics.get(k)
        if isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v):
            missing.append('metric:' + k)
        elif (k.endswith('rate') or k == 'citation_precision') and not 0 <= v <= 1:
            missing.append('range:' + k)
        elif v < 0 or (k == 'samples' and (v < 20 or int(v) != v)):
            missing.append('range:' + k)
    for k in IDENTITY:
        if evidence.get(k) is None:
            missing.append('comparison:' + k)
    return {'name': candidate.get('name'), 'eligible': not missing, 'reasons': missing}
def compare(candidates, changed_factor):
    if changed_factor not in ('model', *IDENTITY):
        raise ValueError('explicit single changed factor required')
    rows = [assess(c) for c in candidates]
    valid = [c for c, r in zip(candidates, rows) if r['eligible']]
    comparison_keys = list(IDENTITY) + ([] if changed_factor == 'model' else ['model_sha256'])
    signatures = {json.dumps({k:c['evidence'][k] for k in comparison_keys if k != changed_factor}, sort_keys=True) for c in valid}
    if len(signatures) > 1:
        return {'status':'NOT_COMPARABLE', 'assessments':rows, 'recommendation':None}
    if not valid:
        return {'status':'NO_QUALIFIED_CANDIDATE', 'assessments':rows, 'recommendation':None}
    valid.sort(key=lambda c:(-c['metrics']['task_success_rate'], -c['metrics']['citation_precision'], c['metrics']['ttft_p95_s'], c['metrics']['peak_host_rss_mib']))
    return {'status':'MEASURED_ORDER_ONLY', 'assessments':rows, 'order':[c['name'] for c in valid], 'recommendation':None, 'limits':'Observed ordering is not proof of global optimum or statistical superiority; review raw receipts and confidence intervals.'}
if __name__ == '__main__':
    ap=argparse.ArgumentParser();ap.add_argument('input');ap.add_argument('--changed-factor', required=True);a=ap.parse_args()
    with open(a.input) as f:data=json.load(f)
    print(json.dumps(compare(data['candidates'], a.changed_factor), ensure_ascii=False, indent=2))
