#!/usr/bin/env python3
"""Functional checks for the powerpoint skill. No network I/O."""

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = ROOT.name
COMMANDS = ['node', 'soffice', 'pdftoppm', 'pdftotext']


def require(condition, detail):
    if not condition:
        raise RuntimeError(str(detail))


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=120).stdout


def check(directory):
    for command in COMMANDS:
        if not shutil.which(command):
            raise RuntimeError(f'missing runtime command: {command}')
    import os
    env = dict(os.environ, NODE_PATH=str(ROOT / 'node_modules'))
    source = directory / 'sample.pptx'
    subprocess.run(['node', str(ROOT / 'scripts/weknora_example.cjs'), str(source)], env=env, check=True, timeout=120)
    run(sys.executable, str(ROOT / 'scripts/render_slides.py'), str(source), '--output_dir', str(directory / 'render'))
    images = list((directory / 'render').glob('*.png'))
    require(len(images) == 2, 'expected two rendered slides')
    require(all(p.read_bytes().startswith(b'\x89PNG') for p in images), 'invalid PNG')
    run('soffice', '-env:UserInstallation=' + (directory / 'lo-profile').as_uri(), '--headless', '--convert-to', 'pdf', '--outdir', str(directory), str(source))
    text = run('pdftotext', '-layout', str(source.with_suffix('.pdf')), '-')
    for expected in ['English', '中文', '示例']:
        require(expected in text, 'missing text: ' + expected)
    require('[object Object]' not in text, 'invalid rich text serialization')


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
