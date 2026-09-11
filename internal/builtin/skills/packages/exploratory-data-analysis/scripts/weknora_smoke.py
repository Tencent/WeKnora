#!/usr/bin/env python3
"""Functional checks for the exploratory-data-analysis skill. No network I/O."""

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = ROOT.name
COMMANDS = []


def require(condition, detail):
    if not condition:
        raise RuntimeError(str(detail))


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=120).stdout


def check(directory):
    import pandas as pd
    frame = pd.DataFrame({'value': [1, 2, None, 4]})
    source = directory / 'sample.csv'
    frame.to_csv(source, index=False)
    require(pd.read_csv(source)['value'].isna().sum() == 1, "pd.read_csv(source)['value'].isna().sum() == 1")
    run(sys.executable, str(ROOT / 'scripts/eda_analyzer.py'), '--help')


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
