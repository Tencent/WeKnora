#!/usr/bin/env python3
"""Functional checks for the browser skill. No network I/O."""

import argparse
import json
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = ROOT.name
COMMANDS = ['agent-browser']


def require(condition, detail):
    if not condition:
        raise RuntimeError(str(detail))


def check(directory):
    import browser
    import subprocess
    import sys
    import time
    launcher = ROOT / '.weknora/bin/agent-browser'
    require(subprocess.check_output([str(launcher), '--version'], text=True).strip() ==
            'agent-browser ' + json.loads((ROOT / 'runtime.lock.json').read_text())['version'],
            'launcher does not resolve the pinned runtime')
    guide = subprocess.check_output([str(launcher), 'skills', 'get', 'core'], text=True)
    require('snapshot' in guide and 'agent-browser' in guide, 'upstream workflow guide unavailable')
    # Use isolated daemon state; validation must not retain login state or
    # interfere with a running session browser.
    browser.SOCKET, browser.LOCK = str(directory / 'b.sock'), str(directory / 'b.lock')
    script = ROOT / 'scripts/browser.py'
    boot = f"import runpy; m=runpy.run_path({str(script)!r}); m['serve'].__globals__.update(SOCKET={browser.SOCKET!r}, LOCK={browser.LOCK!r}); m['serve']()"
    process = subprocess.Popen([sys.executable, '-c', boot])
    try:
        deadline = time.monotonic() + 10
        while not Path(browser.SOCKET).exists() and time.monotonic() < deadline:
            require(process.poll() is None, 'browser adapter exited')
            time.sleep(0.1)
        def run(*args):
            result = browser.request({'args': ['--json', *args]})
            require(result.get('ok'), result)
            payload = json.loads(result['stdout'])
            require(payload.get('success'), payload)
            return payload.get('data') or {}
        run('open', 'about:blank')
        run('eval', 'document.body.innerHTML = ' + json.dumps('<label>Name<input aria-label="Name" id="name"></label>'))
        run('fill', '#name', 'WeKnora')
        require('WeKnora' in json.dumps(run('get', 'value', '#name')), 'input value missing')
        require('Name' in json.dumps(run('get', 'text', 'body')), 'page text missing')
        require('<label>' in json.dumps(run('get', 'html', 'body')), 'page HTML missing')
        target = directory / 'preview.png'
        run('screenshot', str(target))
        require(target.read_bytes().startswith(b'\x89PNG'), 'invalid screenshot')
        frame = browser.request({'action': 'frame', 'ui': True})
        require(frame.get('ok') and frame.get('image'), frame)
        import websocket
        info = browser.request({'action': 'stream', 'ui': True})
        require(info.get('ok'), info)
        ws = websocket.create_connection(f"ws://127.0.0.1:{info['port']}/?pacing=ack&maxFps=10",
                                         timeout=5, suppress_origin=True, http_no_proxy=['127.0.0.1'])
        try:
            for _ in range(20):
                message = json.loads(ws.recv())
                if message.get('type') == 'frame':
                    require(message.get('data'), 'empty stream frame')
                    ws.send(json.dumps({'type': 'ack', 'seq': message['seq']}))
                    break
            else:
                raise RuntimeError('native browser stream did not deliver a frame')
        finally:
            ws.close()
        run('close')
    finally:
        process.terminate()
        process.wait(timeout=15)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', action='store_true')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='wkbr-', dir='/tmp') as tmp:
        check(Path(tmp))
    if args.report:
        report = ROOT / '.weknora/install-report.json'
        report.parent.mkdir(exist_ok=True)
        report.write_text(json.dumps({'commands': COMMANDS, 'blockers': []}) + '\n')
    print(json.dumps({'ok': True, 'skill': NAME, 'functional_check': 'passed'}))
if __name__ == '__main__':
    main()
