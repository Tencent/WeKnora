#!/usr/bin/env python3
"""Functional checks for the statistical-analysis skill. No network I/O."""

import argparse
import json
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = ROOT.name
COMMANDS = []


def require(condition, detail):
    if not condition:
        raise RuntimeError(str(detail))


def check(directory):
    import numpy as np
    from scipy.stats import ttest_ind
    import statsmodels.api as sm
    import pingouin
    result = ttest_ind([1, 2, 3, 4, 5], [4, 5, 6, 7, 8])
    require(0 < result.pvalue < 0.05, '0 < result.pvalue < 0.05')
    model = sm.OLS(np.arange(10) * 2 + 1, sm.add_constant(np.arange(10))).fit()
    require(abs(model.params[1] - 2) < 1e-06, 'abs(model.params[1] - 2) < 1e-06')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', action='store_true')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='weknora-skill-smoke-') as tmp:
        check(Path(tmp))
    if args.report:
        report = ROOT / '.weknora/install-report.json'
        report.parent.mkdir(exist_ok=True)
        report.write_text(json.dumps({'commands': COMMANDS, 'blockers': []}) + '\n')
    print(json.dumps({'ok': True, 'skill': NAME, 'functional_check': 'passed'}))
if __name__ == '__main__':
    main()
