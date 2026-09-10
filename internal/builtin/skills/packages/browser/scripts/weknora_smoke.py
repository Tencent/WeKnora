#!/usr/bin/env python3
"""Functional checks for the browser skill. No network I/O."""

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
    from playwright.sync_api import sync_playwright
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        try:
            page = browser.new_page()
            page.set_content('<label>Name<input aria-label="Name"></label>')
            page.get_by_label('Name').fill('WeKnora')
            require(page.get_by_label('Name').input_value() == 'WeKnora', "page.get_by_label('Name').input_value() == 'WeKnora'")
            require(page.screenshot().startswith(b'\x89PNG'), "page.screenshot().startswith(b'\\x89PNG')")
        finally:
            browser.close()


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
