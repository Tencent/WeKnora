#!/usr/bin/env python3
"""Functional checks for the scientific-visualization skill. No network I/O."""

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
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    import seaborn
    import plotly.graph_objects as go
    figure, ax = plt.subplots()
    ax.plot([0, 1, 2], [0, 2, 1])
    for extension in ['png', 'svg', 'pdf']:
        output = directory / ('figure.' + extension)
        figure.savefig(output)
        require(output.stat().st_size > 100, 'output.stat().st_size > 100')
    plt.close(figure)
    require('scatter' in go.Figure(go.Scatter(x=[1], y=[2])).to_json(), "'scatter' in go.Figure(go.Scatter(x=[1], y=[2])).to_json()")


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
