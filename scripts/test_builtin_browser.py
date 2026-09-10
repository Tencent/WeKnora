#!/usr/bin/env python3
"""Browser controller tests. Set WEKNORA_TEST_BROWSER=1 for real Chromium IPC tests."""
import hashlib
import io
import http.server
import threading
import importlib.util
import json
import os
from pathlib import Path
import signal
import select
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



class InstallerTest(unittest.TestCase):
    def test_checksum_rejection_preserves_existing_binary(self):
        installer_spec = importlib.util.spec_from_file_location('browser_install', SCRIPT.parent / 'install.py')
        installer = importlib.util.module_from_spec(installer_spec)
        installer_spec.loader.exec_module(installer)
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'agent-browser'
            target.write_bytes(b'existing runtime')
            asset = {'url': 'https://example.invalid/binary',
                     'sha256': hashlib.sha256(b'expected runtime').hexdigest()}
            with patch.object(installer.urllib.request, 'urlopen', return_value=io.BytesIO(b'wrong download')):
                with self.assertRaisesRegex(ValueError, 'SHA-256'):
                    installer.install_binary(asset, target)
            self.assertEqual(target.read_bytes(), b'existing runtime')


class AdapterTest(unittest.TestCase):
    def make_browser(self):
        with patch.object(browser, 'native_environment', return_value={}):
            instance = browser.Browser()
        instance.started = True
        return instance

    def test_native_argv_is_preserved(self):
        instance = self.make_browser()
        args = ['eval', '"$(echo literal)" + " words "', '--json']
        result = {'ok': True, 'stdout': '{"success":true}', 'stderr': '', 'exit_code': 0}
        with patch.object(browser, 'native_run', return_value=result) as run:
            self.assertEqual(instance.execute(args), result)
            run.assert_called_once_with(args, {})

    def test_ownership_and_human_control(self):
        instance = self.make_browser()
        for args in (['--json', 'session', 'list'], ['open', '--session=x'],
                     ['--json', 'set', 'viewport', '100', '100'], ['batch', '[]']):
            with self.assertRaisesRegex(ValueError, 'managed by WeKnora'):
                instance.execute(args)
        instance.lease.acquire('a' * 32)
        with patch.object(browser, 'native_run') as run:
            with self.assertRaisesRegex(ValueError, 'human control'):
                instance.execute(['get', 'text', 'body'])
            run.assert_not_called()

    def test_preview_never_starts_browser(self):
        instance = self.make_browser()
        instance.started = False
        with patch.object(browser, 'native_run') as run:
            self.assertEqual(instance.frame()['state'], 'not_started')
            run.assert_not_called()

    def test_failed_close_retains_running_state(self):
        instance = self.make_browser()
        with patch.object(browser, 'native_run', return_value={'ok': False, 'exit_code': 1}):
            instance.execute(['close'])
        self.assertTrue(instance.started)

    def test_hover_requires_control_and_never_captures_frame(self):
        instance = self.make_browser()
        request = {'action': 'hover', 'ui': True, 'token': 'a' * 32, 'x': 20, 'y': 30}
        with patch.object(instance, 'command') as command:
            with self.assertRaisesRegex(ValueError, 'expired'):
                instance.dispatch(request)
            command.assert_not_called()
            instance.lease.acquire('a' * 32)
            command.side_effect = [{}, {'result': 'pointer'}]
            self.assertEqual(instance.dispatch(request), {'ok': True, 'cursor': 'pointer'})
            self.assertEqual(command.call_args_list[0].args, ('mouse', 'move', '20', '30'))
            self.assertEqual(command.call_args_list[1].args[0], 'eval')
            self.assertEqual(instance.revision, 0)
            instance.pointer_down = True
            with self.assertRaisesRegex(ValueError, 'during a drag'):
                instance.dispatch(request)


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
                <button onclick="document.getElementById('saved').textContent='Saved'">Save</button><output id="saved"></output><output id="value">Slider value 0</output>
                <input style="position:absolute;left:20px;top:200px;width:200px;height:30px;cursor:auto">
                <a href="#hover" style="position:absolute;left:20px;top:250px;cursor:pointer">Hover link</a>
                </body></html>'''
                self.send_response(200); self.send_header('Content-Type', 'text/html'); self.end_headers(); self.wfile.write(content)
            def log_message(self, *args):
                pass
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with tempfile.TemporaryDirectory(prefix='wkbr-', dir='/tmp') as directory:
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
                self.assertTrue(browser.request({'args': ['open', f'http://127.0.0.1:{server.server_port}']})['ok'])
                self.assertTrue(browser.request({'args': ['fill', '#name', 'Agent']})['ok'])
                frame = browser.request({'action': 'frame', 'ui': True})
                self.assertTrue(frame['ok'])
                self.assertGreater(len(frame['image']), 100)
                acquired = browser.request({'action': 'acquire', 'ui': True, 'token': 'a' * 32})
                self.assertTrue(acquired['controlled'])
                self.assertIn('pointer', acquired['capabilities'])
                self.assertIn('cursor', acquired['capabilities'])
                for y, cursor in [(210, 'text'), (255, 'pointer')]:
                    hover = browser.request({'action': 'hover', 'ui': True, 'token': 'a' * 32, 'x': 30, 'y': y})
                    self.assertTrue(hover['ok'], hover)
                    self.assertEqual(hover['cursor'], cursor)
                    self.assertNotIn('image', hover)
                self.assertFalse(browser.request({'args': ['snapshot']})['ok'])
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
                self.assertIn('Agent-human', browser.request({'args': ['snapshot']})['stdout'])
                self.assertIn('Slider value 100', browser.request({'args': ['snapshot']})['stdout'])
                self.assertTrue(browser.request({'args': ['snapshot']})['ok'])
                self.assertTrue(browser.request({'args': ['get', 'text', 'body']})['ok'])
                self.assertIn('<label>', browser.request({'args': ['get', 'html', 'body']})['stdout'])
                snapshot = json.loads(browser.request({'args': ['snapshot', '-i', '--json']})['stdout'])
                refs = snapshot['data']['refs']
                button = next(key for key, value in refs.items() if value.get('role') == 'button' and value.get('name') == 'Save')
                self.assertTrue(browser.request({'args': ['click', '@' + button]})['ok'])
                self.assertIn('Saved', browser.request({'args': ['get', 'text', '#saved']})['stdout'])
                # Exercise the real native WebSocket through the private stdio
                # bridge, including end-to-end ACK pacing and disconnect release.
                stream_boot = (f"import sys; sys.path.insert(0, {str(SCRIPT.parent)!r}); "
                               f"import browser; browser.SOCKET={browser.SOCKET!r}; "
                               f"import stream; stream.serve({'c' * 32!r})")
                streaming = subprocess.Popen([sys.executable, '-u', '-c', stream_boot],
                                             stdin=subprocess.PIPE, stdout=subprocess.PIPE)
                pending = bytearray()
                def read_stream(timeout=10):
                    deadline = time.monotonic() + timeout
                    while b'\n' not in pending:
                        remaining = deadline - time.monotonic()
                        if remaining <= 0 or not select.select([streaming.stdout], [], [], remaining)[0]:
                            return None
                        chunk = os.read(streaming.stdout.fileno(), 65536)
                        self.assertTrue(chunk, 'stream bridge exited unexpectedly')
                        pending.extend(chunk)
                    line, _, rest = pending.partition(b'\n')
                    pending[:] = rest
                    self.assertTrue(line.startswith(b'WK_BROWSER_STREAM '), line[:100])
                    return json.loads(line[len(b'WK_BROWSER_STREAM '):])
                def send_stream(value):
                    streaming.stdin.write(json.dumps(value).encode() + b'\n')
                    streaming.stdin.flush()
                def wait_for(kind):
                    for _ in range(30):
                        value = read_stream()
                        self.assertIsNotNone(value, f'no {kind} from stream')
                        self.assertNotEqual(value['type'], 'error', value)
                        if value['type'] == kind:
                            return value
                        self.assertNotEqual(value['type'], 'frame', 'frame arrived before renderer ACK')
                    self.fail(f'no {kind} from stream')
                try:
                    first = wait_for('frame')
                    self.assertTrue(first['data'])
                    send_stream({'type': 'command', 'id': 1, 'command': {'action': 'acquire'}})
                    acquired = wait_for('result')['data']
                    self.assertTrue(acquired['ok'], acquired)
                    self.assertNotIn('image', acquired, 'stream commands must not take extra screenshots')
                    self.assertFalse(browser.request({'args': ['get', 'text', 'body']})['ok'])
                    send_stream({'type': 'command', 'id': 2, 'command': {
                        'action': 'open', 'url': f'http://127.0.0.1:{server.server_port}/changed'}})
                    self.assertTrue(wait_for('result')['data']['ok'])
                    # New pixels exist, but no newer frame may pass before ACK.
                    while True:
                        value = read_stream(0.2)
                        if value is None: break
                        self.assertNotEqual(value['type'], 'frame')
                    send_stream({'type': 'ack', 'seq': first['seq']})
                    second = wait_for('frame')
                    self.assertGreater(second['seq'], first['seq'])
                    send_stream({'type': 'disconnect'})
                    self.assertEqual(streaming.wait(timeout=10), 0)
                    self.assertTrue(browser.request({'args': ['get', 'text', 'body']})['ok'],
                                    'disconnect must release human control')
                finally:
                    if streaming.poll() is None:
                        streaming.terminate()
                        streaming.wait(timeout=10)
                    streaming.stdin.close()
                    streaming.stdout.close()
                self.assertTrue(browser.request({'args': ['close']})['ok'])
                self.assertEqual(browser.request({'action': 'frame', 'ui': True})['state'], 'not_started')
            finally:
                process.terminate()
                process.wait(timeout=10)


if __name__ == '__main__':
    unittest.main()
