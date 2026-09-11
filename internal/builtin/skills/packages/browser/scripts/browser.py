#!/usr/bin/env python3
"""WeKnora session/lease adapter for the pinned native agent-browser CLI.

Browser automation is implemented upstream in Rust/CDP. This stdlib-only bridge
serializes Agent and authenticated panel requests over a sandbox-local socket.
"""
import argparse
import base64
import fcntl
import json
import math
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import sys
import time
from urllib.parse import urlparse

SOCKET = '/tmp/weknora-browser.sock'
LOCK = '/tmp/weknora-browser.lock'
MAX_BYTES = 4 * 1024 * 1024
LEASE_SECONDS = 35
IDLE_SECONDS = 900


class ControlLease:
    def __init__(self):
        self.token = ''
        self.deadline = 0

    def active(self):
        if time.monotonic() >= self.deadline:
            self.token = ''
        return bool(self.token)

    def acquire(self, token):
        if not isinstance(token, str) or not 24 <= len(token) <= 128:
            raise ValueError('invalid control token')
        if self.active() and not secrets.compare_digest(token, self.token):
            raise ValueError('browser is controlled by another panel')
        self.token, self.deadline = token, time.monotonic() + LEASE_SECONDS

    def check(self, token, ui):
        active = self.active()
        if ui and (not active or not secrets.compare_digest(token, self.token)):
            raise ValueError('control lease expired; take control again')
        if not ui and active:
            raise ValueError('human control is active; wait for the user to release control')
        if active:
            self.deadline = time.monotonic() + LEASE_SECONDS


def read_message(conn):
    data = bytearray()
    while b'\n' not in data:
        chunk = conn.recv(65536)
        if not chunk:
            break
        data.extend(chunk)
        if len(data) > MAX_BYTES:
            raise ValueError('message too large')
    return json.loads(data.split(b'\n', 1)[0])


def coordinate(value, maximum):
    if not isinstance(value, (float, int)) or not math.isfinite(value) or not 0 <= value <= maximum:
        raise ValueError('invalid coordinate')
    return value


ROOT = Path(__file__).resolve().parent.parent
NATIVE = ROOT / '.weknora/lib/agent-browser'
# The Agent uses upstream browser commands; only lifecycle/configuration that
# would detach it from the session panel is owned by this adapter.
MANAGED_COMMANDS = {'connect', 'session', 'mcp', 'chat', 'dashboard', 'install',
                    'upgrade', 'plugin', 'stream', 'batch', 'screencast_start', 'screencast_stop'}
MANAGED_FLAGS = {'--session', '--session-name', '--profile', '--config', '--cdp',
                 '--auto-connect', '--provider', '-p', '--engine', '--executable-path',
                 '--headed', '--args', '--extensions', '--restore', '--idle-timeout',
                 '--stream-port', '--ios', '--device', '--all', '--namespace', '--extension'}


def native_environment():
    runtime = Path(SOCKET).with_suffix('')
    runtime.mkdir(mode=0o700, parents=True, exist_ok=True)
    config = runtime / 'config.json'
    config.write_text('{}')
    installed = json.loads((ROOT / '.weknora/runtime.json').read_text())
    env = {k: v for k, v in os.environ.items() if not k.startswith('AGENT_BROWSER_')}
    env.update(AGENT_BROWSER_SESSION='weknora', AGENT_BROWSER_SOCKET_DIR=str(runtime),
               AGENT_BROWSER_CONFIG=str(config), AGENT_BROWSER_EXECUTABLE_PATH=installed['browser_path'],
               AGENT_BROWSER_ARGS='--no-sandbox,--disable-dev-shm-usage',
               AGENT_BROWSER_IDLE_TIMEOUT_MS='0', AGENT_BROWSER_MAX_OUTPUT='1000000',
               AGENT_BROWSER_STREAM_QUALITY='80', AGENT_BROWSER_STREAM_MAX_WIDTH='1280',
               AGENT_BROWSER_STREAM_MAX_HEIGHT='800',
               AGENT_BROWSER_DOWNLOAD_PATH='/workspace/output')
    return env


def native_run(args, env, timeout=90):
    # Never use a shell: selectors, JS and typed text remain literal arguments.
    result = subprocess.run([str(NATIVE), *args], env=env, cwd=ROOT,
                            capture_output=True, text=True, timeout=timeout)
    return {'ok': result.returncode == 0, 'stdout': result.stdout,
            'stderr': result.stderr, 'exit_code': result.returncode}


class Browser:
    def __init__(self):
        self.env = native_environment()
        self.lease = ControlLease()
        self.revision = 0
        self.pointer_down = False
        self.started = False
        self.url = ''

    def command(self, *args):
        result = native_run(['--json', *args], self.env, timeout=30)
        try:
            payload = json.loads(result['stdout'])
        except ValueError:
            raise ValueError(result['stderr'][:2000] or 'agent-browser returned invalid JSON')
        if not result['ok'] or not payload.get('success'):
            raise ValueError(str(payload.get('error') or result['stderr'])[:2000])
        return payload.get('data') or {}

    def start(self):
        if not self.started:
            self.command('open')
            self.started = True
            self.command('set', 'viewport', '1280', '800')
            self.revision += 1

    def close(self):
        if self.started:
            try:
                self.command('close')
            finally:
                self.started = False
                self.pointer_down = False

    def release_pointer(self):
        if self.pointer_down:
            try:
                self.command('mouse', 'up')
            finally:
                self.pointer_down = False

    def frame(self, preview=True):
        if not self.started:
            return {'ok': False, 'state': 'not_started', 'error': 'browser is not running'}
        if not self.lease.active():
            self.release_pointer()
        url = self.command('get', 'url')['url']
        if url != self.url:
            self.url = url
            self.revision += 1
        metadata = {'ok': True, 'state': 'running', 'capabilities': ['pointer', 'stream', 'cursor'],
                    'url': self.url, 'controlled': self.lease.active(), 'revision': self.revision,
                    'width': 1280, 'height': 800}
        if not preview:
            return metadata
        target = Path(SOCKET).with_suffix('.jpg')
        try:
            self.command('screenshot', str(target), '--screenshot-format', 'jpeg', '--screenshot-quality', '65')
            data = target.read_bytes()
            if len(data) > 2 * 1024 * 1024:
                raise ValueError('browser preview exceeds limit')
            return {**metadata, 'image': base64.b64encode(data).decode()}
        finally:
            target.unlink(missing_ok=True)

    def execute(self, args):
        if not isinstance(args, list) or not args or not all(isinstance(a, str) for a in args):
            raise ValueError('expected agent-browser command arguments')
        for arg in args:
            if arg.split('=', 1)[0] in MANAGED_FLAGS:
                raise ValueError('browser session and launch options are managed by WeKnora')
        position = 0
        while position < len(args) and args[position] in ('--json', '--debug'):
            position += 1
        if position == len(args) or args[position].startswith('-'):
            raise ValueError('put the browser command first (only --json/--debug may precede it)')
        command = args[position]
        if command in MANAGED_COMMANDS or args[position:position + 2] in (['set', 'viewport'], ['set', 'device']):
            raise ValueError('this command is managed by WeKnora; use the current session browser')
        self.lease.check('', False)
        if not self.started and command == 'close':
            return {'ok': True, 'stdout': '', 'stderr': '', 'exit_code': 0}
        self.start()
        try:
            result = native_run(args, self.env)
            if command == 'close' and result['ok']:
                self.started = False
                self.pointer_down = False
            return result
        finally:
            self.revision += 1

    def dispatch(self, req):
        if req.get('ui') is not True:
            return self.execute(req.get('args'))
        action = req.get('action')
        token = req.get('token', '')
        preview = not req.get('stream', False)
        if action == 'stream':
            if not self.started:
                raise ValueError('browser is not running')
            status = self.command('stream', 'status')
            if not status.get('enabled'):
                status = self.command('stream', 'enable')
            return {'ok': True, 'port': status['port']}
        if action == 'start':
            self.start()
            return self.frame(preview=preview)
        if action == 'frame':
            return self.frame(preview=preview)
        if action == 'acquire':
            self.lease.acquire(token)
            return self.frame(preview=preview)
        self.lease.check(token, True)
        if action == 'release':
            self.release_pointer()
            self.lease.token = ''
            return {'ok': True, 'controlled': False}
        if action == 'heartbeat':
            return {'ok': True, 'controlled': True}
        if action == 'hover':
            x, y = coordinate(req.get('x'), 1280), coordinate(req.get('y'), 800)
            if self.pointer_down:
                raise ValueError('cannot hover during a drag')
            self.command('mouse', 'move', str(round(x)), str(round(y)))
            # Read the rendered target, including open shadow roots and accessible
            # frames. Cross-origin frames retain the embedding element's cursor.
            result = self.command('eval', '''(() => {
              let doc = document, x = %s, y = %s, el = doc.elementFromPoint(x, y);
              for (let depth = 0; el && depth < 12; depth++) {
                const child = el.shadowRoot?.elementFromPoint(x, y);
                if (child && child !== el) { el = child; continue; }
                if (el.tagName === 'IFRAME') {
                  try {
                    const inner = el.contentDocument;
                    if (inner) {
                      const rect = el.getBoundingClientRect();
                      x -= rect.left + el.clientLeft; y -= rect.top + el.clientTop;
                      doc = inner; el = doc.elementFromPoint(x, y); continue;
                    }
                  } catch (_) {}
                }
                break;
              }
              if (!el) return 'default';
              const cursor = doc.defaultView.getComputedStyle(el).cursor;
              if (cursor !== 'auto') return cursor;
              if (el.isContentEditable || el.matches('textarea, input:not([type]), input[type="text"], input[type="search"], input[type="email"], input[type="password"], input[type="url"], input[type="tel"], input[type="number"]')) return 'text';
              return 'default';
            })()''' % (json.dumps(x), json.dumps(y)))
            return {'ok': True, 'cursor': result.get('result', 'default')}
        if action in ('click', 'press', 'type', 'scroll', 'pointer') and req.get('phase') != 'cancel':
            # Detect navigations that occurred since the last preview.
            if self.command('get', 'url')['url'] != self.url:
                self.revision += 1
            if req.get('revision') != self.revision:
                raise ValueError('page changed; refresh the preview before interacting')
        if action == 'open':
            url = req.get('url', '')
            if not isinstance(url, str) or len(url) > 8192 or urlparse(url).scheme not in ('http', 'https'):
                raise ValueError('an http or https URL is required')
            self.command('open', url)
        elif action in ('click', 'pointer'):
            phase = 'click' if action == 'click' else req.get('phase')
            if phase == 'cancel':
                self.release_pointer()
            elif phase in ('click', 'down', 'move', 'up'):
                x, y = coordinate(req.get('x'), 1280), coordinate(req.get('y'), 800)
                if phase == 'down':
                    self.release_pointer()
                if phase == 'move' and not self.pointer_down:
                    raise ValueError('pointer is not pressed')
                self.command('mouse', 'move', str(round(x)), str(round(y)))
                if phase in ('click', 'down'):
                    self.command('mouse', 'down')
                    self.pointer_down = True
                if phase in ('click', 'up'):
                    self.release_pointer()
            else:
                raise ValueError('unsupported pointer phase')
        elif action == 'press':
            key = req.get('key', '')
            if key not in ('Enter', 'Tab', 'Shift+Tab', 'Backspace', 'Delete', 'Escape', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'ControlOrMeta+A'):
                raise ValueError('unsupported key')
            self.command('press', 'Control+a' if key == 'ControlOrMeta+A' else key)
        elif action == 'type':
            text = req.get('text', '')
            if not isinstance(text, str) or len(text) > 10000:
                raise ValueError('text exceeds limit')
            self.command('keyboard', 'inserttext', text)
        elif action == 'scroll':
            self.command('mouse', 'wheel', str(round(max(-1200, min(1200, float(req.get('delta', 0)))))))
        elif action == 'back':
            self.command('back')
        else:
            raise ValueError('unsupported browser panel action')
        self.revision += 1
        return self.frame(preview=preview)


def serve():
    def stop(signum, frame):
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with open(LOCK, 'w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        browser = Browser()
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as server:
            Path(SOCKET).unlink(missing_ok=True)
            server.bind(SOCKET)
            os.chmod(SOCKET, 0o600)
            server.listen(8)
            server.settimeout(1)
            last_request = time.monotonic()
            try:
                while time.monotonic() - last_request < IDLE_SECONDS:
                    try:
                        conn, _ = server.accept()
                    except socket.timeout:
                        continue
                    with conn:
                        conn.settimeout(130)
                        try:
                            req = read_message(conn)
                            if not isinstance(req, dict):
                                raise ValueError('request must be an object')
                            response = browser.dispatch(req)
                            last_request = time.monotonic()
                        except Exception as exc:
                            response = {'ok': False, 'error': str(exc)[:2000]}
                        try:
                            encoded = json.dumps(response).encode() + b'\n'
                            if len(encoded) > MAX_BYTES:
                                encoded = b'{"ok":false,"error":"browser result too large; narrow the selector or reduce --max-output"}\n'
                            conn.sendall(encoded)
                        except OSError:
                            pass
            finally:
                browser.close()
                Path(SOCKET).unlink(missing_ok=True)


def request(req, allow_start=False):
    deadline = time.monotonic() + (25 if allow_start else 0)
    spawned = False
    while True:
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
                conn.settimeout(130)
                conn.connect(SOCKET)
                conn.sendall(json.dumps(req).encode() + b'\n')
                return read_message(conn)
        except (FileNotFoundError, ConnectionRefusedError):
            if not allow_start or time.monotonic() > deadline:
                return {'ok': False, 'state': 'not_started', 'error': 'browser is not running'}
            if not spawned:
                with open('/tmp/weknora-browser.log', 'ab') as log:
                    subprocess.Popen([sys.executable, str(Path(__file__).resolve()), '--serve'],
                                     stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
                spawned = True
            time.sleep(0.15)


def main():
    if '--ui-request' in sys.argv[1:]:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument('--ui-request', required=True)
        args = parser.parse_args()
        req = json.loads(base64.b64decode(args.ui_request).decode())
        req['ui'] = True
        response = request(req, allow_start=req.get('action') == 'start')
        print(json.dumps(response, ensure_ascii=False))
        sys.exit(0 if response.get('ok') else 1)
    if sys.argv[1:] == ['--serve']:
        serve()
        return
    args = sys.argv[1:]
    if not args:
        args = ['--help']
    # Offline help is supplied by the exact installed binary and needs no
    # browser, daemon or login state (including during human takeover).
    if '--help' in args or '-h' in args or args in (['--version'], ['-V']) or args[0] == 'skills':
        env = {k: v for k, v in os.environ.items() if not k.startswith('AGENT_BROWSER_')}
        env['AGENT_BROWSER_SKILLS_DIR'] = str(ROOT / '.weknora/skill-data')
        result = subprocess.run([str(NATIVE), *args], cwd=ROOT, env=env)
        sys.exit(result.returncode)
    response = request({'args': args}, allow_start=True)
    if 'stdout' in response:
        sys.stdout.write(response['stdout'])
        sys.stderr.write(response['stderr'])
    else:
        print(json.dumps(response, ensure_ascii=False))
    sys.exit(response.get('exit_code', 0 if response.get('ok') else 1))


if __name__ == '__main__':
    main()
