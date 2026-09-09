#!/usr/bin/env python3
"""Browser controller tests. Set WEKNORA_TEST_BROWSER=1 for real Chromium IPC tests."""
import http.server
import threading
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SCRIPT = Path(os.environ['WEKNORA_BROWSER_SCRIPT']) if os.environ.get('WEKNORA_BROWSER_SCRIPT') else Path(__file__).resolve().parents[1] / 'internal/builtin/skills/packages/browser/scripts/browser.py'
spec = importlib.util.spec_from_file_location('weknora_browser', SCRIPT)
browser = importlib.util.module_from_spec(spec)
spec.loader.exec_module(browser)


class LeaseTest(unittest.TestCase):
    def test_takeover_blocks_agent_and_other_panels(self):
        lease = browser.ControlLease()
        lease.acquire('a' * 32)
        with self.assertRaisesRegex(ValueError, 'human control'):
            lease.check('', False)
        with self.assertRaisesRegex(ValueError, 'another panel'):
            lease.acquire('b' * 32)
        lease.check('a' * 32, True)
        with patch.object(browser.time, 'monotonic', return_value=lease.deadline + 1):
            self.assertFalse(lease.active())
            lease.check('', False)
            with self.assertRaisesRegex(ValueError, 'expired'):
                lease.check('a' * 32, True)

    def test_coordinate_bounds(self):
        for value in [None, -1, 1281, float('nan'), float('inf'), '1']:
            with self.assertRaises(ValueError):
                browser.coordinate(value, 1280)
        self.assertEqual(browser.coordinate(50, 1280), 50)


@unittest.skipUnless(os.environ.get('WEKNORA_TEST_BROWSER') == '1', 'real browser opt-in')
class BrowserIntegrationTest(unittest.TestCase):
    def test_shared_browser_and_control_lifecycle(self):
        # Override the module's socket/lock in a child interpreter so this test
        # never touches another task's controller, browser or login state.
        class Fixture(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                content = b'''<html><body><label>Name<input id="name" aria-label="Name"></label>
                <input id="drag" aria-label="Drag" type="range" min="0" max="100" value="0"
                  style="position:absolute;left:100px;top:100px;width:300px;height:40px"
                  oninput="document.getElementById('value').textContent='Slider value '+this.value">
                <output id="value">Slider value 0</output></body></html>'''
                self.send_response(200); self.send_header('Content-Type', 'text/html'); self.end_headers(); self.wfile.write(content)
            def log_message(self, *args):
                pass
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with tempfile.TemporaryDirectory(prefix='weknora-browser-test-') as directory:
            browser.SOCKET, browser.LOCK = directory + '/browser.sock', directory + '/browser.lock'
            boot = f"import runpy; m=runpy.run_path({str(SCRIPT)!r}); m['serve'].__globals__.update(SOCKET={browser.SOCKET!r}, LOCK={browser.LOCK!r}); m['serve']()"
            process = subprocess.Popen([sys.executable, '-c', boot], start_new_session=True)
            try:
                deadline = time.monotonic() + 20
                while not Path(browser.SOCKET).exists() and time.monotonic() < deadline:
                    if process.poll() is not None:
                        self.fail('browser daemon failed to start')
                    time.sleep(0.1)
                self.assertTrue(Path(browser.SOCKET).exists())
                self.assertTrue(browser.request({'action': 'open', 'url': f'http://127.0.0.1:{server.server_port}'})['ok'])
                self.assertTrue(browser.request({'action': 'fill', 'selector': '#name', 'text': 'Agent'})['ok'])
                frame = browser.request({'action': 'frame', 'ui': True})
                self.assertTrue(frame['ok'])
                self.assertGreater(len(frame['image']), 100)
                acquired = browser.request({'action': 'acquire', 'ui': True, 'token': 'a' * 32})
                self.assertTrue(acquired['controlled'])
                self.assertIn('pointer', acquired['capabilities'])
                self.assertFalse(browser.request({'action': 'snapshot'})['ok'])
                self.assertFalse(browser.request({'action': 'acquire', 'ui': True, 'token': 'b' * 32})['ok'])
                self.assertFalse(browser.request({'action': 'click', 'ui': True, 'token': 'a' * 32,
                                                  'revision': -1, 'x': 10, 'y': 10})['ok'])
                typed = browser.request({'action': 'type', 'ui': True, 'token': 'a' * 32,
                                         'revision': acquired['revision'], 'text': '-human'})
                self.assertTrue(typed['ok'], typed)
                revision = typed['revision']
                for phase, x in [('down', 110), ('move', 240), ('move', 380), ('up', 398)]:
                    response = browser.request({'action': 'pointer', 'phase': phase, 'x': x, 'y': 120,
                                                'revision': revision, 'ui': True, 'token': 'a' * 32})
                    self.assertTrue(response['ok'], response)
                    revision = response['revision']
                # A stale cancel still releases the mouse; loss of the previous
                # screenshot must never leave the page with a pressed button.
                response = browser.request({'action': 'pointer', 'phase': 'cancel', 'revision': -1,
                                            'ui': True, 'token': 'a' * 32})
                self.assertTrue(response['ok'], response)
                self.assertTrue(browser.request({'action': 'release', 'ui': True, 'token': 'a' * 32})['ok'])
                self.assertIn('Agent-human', browser.request({'action': 'snapshot'})['content'])
                self.assertIn('Slider value 100', browser.request({'action': 'snapshot'})['content'])
                self.assertTrue(browser.request({'action': 'snapshot'})['ok'])
                self.assertFalse(browser.request({'action': 'open', 'url': 'file:///etc/passwd'})['ok'])
            finally:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=10)


if __name__ == '__main__':
    unittest.main()
