#!/usr/bin/env python3
"""Session-local headless browser. CLI and UI share one serialized controller.

No inbound ports: commands travel over a Unix socket inside the sandbox.
The WeKnora HTTP layer authenticates the user before forwarding UI commands.
"""
import argparse
import base64
import fcntl
import json
import math
import os
from pathlib import Path
import secrets
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


class Browser:
    def __init__(self, playwright):
        self.browser = playwright.chromium.launch(headless=True)
        self.context = self.browser.new_context(viewport={'width': 1280, 'height': 800}, accept_downloads=True)
        self.page = self.context.new_page()
        self.context.set_default_timeout(10000)
        self.context.on('page', self.new_page)
        self.page.on('download', self.download)
        self.lease = ControlLease()
        self.revision = 0
        self.pointer_down = False

    def new_page(self, page):
        self.page = page
        page.on('download', self.download)
        self.revision += 1

    def download(self, download):
        # A publisher-controlled filename cannot escape the artifact directory.
        root = Path('/workspace/output')
        root.mkdir(parents=True, exist_ok=True)
        name = Path(download.suggested_filename.replace('\\', '/')).name or 'download'
        download.save_as(root / (secrets.token_hex(4) + '-' + name))

    def release_pointer(self):
        if self.pointer_down:
            try:
                self.page.mouse.up()
            finally:
                self.pointer_down = False

    def frame(self):
        if not self.lease.active():
            self.release_pointer()
        if self.page.is_closed():
            pages = self.context.pages
            self.page = pages[-1] if pages else self.context.new_page()
        return {'ok': True, 'state': 'running', 'capabilities': ['pointer'], 'url': self.page.url,
                'controlled': self.lease.active(), 'revision': self.revision,
                'width': 1280, 'height': 800,
                'image': base64.b64encode(self.page.screenshot(type='jpeg', quality=65, timeout=10000)).decode()}

    def dispatch(self, req):
        action = req.get('action')
        ui = req.get('ui') is True
        token = req.get('token', '')
        if action in ('frame', 'start', 'status'):
            return self.frame()
        if action == 'acquire' and ui:
            self.lease.acquire(token)
            return self.frame()
        self.lease.check(token, ui)
        if action == 'release' and ui:
            self.release_pointer()
            self.lease.token = ''
            return {'ok': True, 'controlled': False}
        if action == 'heartbeat' and ui:
            return {'ok': True, 'controlled': True}
        if ui and (action in ('click', 'press', 'type', 'scroll') or (action == 'pointer' and req.get('phase') in ('down', 'move'))) and req.get('revision') != self.revision:
            raise ValueError('page changed; refresh the preview before interacting')
        if action == 'open':
            url = req.get('url', '')
            if not isinstance(url, str) or len(url) > 8192 or urlparse(url).scheme not in ('http', 'https'):
                raise ValueError('an http or https URL is required')
            self.page.goto(url, wait_until='domcontentloaded', timeout=20000)
        elif action == 'click':
            if not ui and req.get('selector'):
                self.page.locator(req['selector']).click()
            else:
                self.page.mouse.click(coordinate(req.get('x'), 1280), coordinate(req.get('y'), 800))
        elif action == 'pointer' and ui:
            phase = req.get('phase')
            if phase == 'cancel':
                self.release_pointer()
            elif phase in ('down', 'move', 'up'):
                x, y = coordinate(req.get('x'), 1280), coordinate(req.get('y'), 800)
                if phase == 'down':
                    self.release_pointer()
                    self.page.mouse.move(x, y)
                    self.page.mouse.down()
                    self.pointer_down = True
                elif phase == 'move':
                    if not self.pointer_down:
                        raise ValueError('pointer is not pressed')
                    self.page.mouse.move(x, y)
                else:
                    try:
                        self.page.mouse.move(x, y)
                    finally:
                        self.release_pointer()
            else:
                raise ValueError('unsupported pointer phase')
        elif action == 'fill' and not ui:
            self.page.locator(req['selector']).fill(req.get('text', ''))
        elif action == 'press':
            key = req.get('key', '')
            if key not in ('Enter', 'Tab', 'Shift+Tab', 'Backspace', 'Delete', 'Escape', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'ControlOrMeta+A'):
                raise ValueError('unsupported key')
            self.page.keyboard.press(key)
        elif action == 'type':
            text = req.get('text', '')
            if not isinstance(text, str) or len(text) > 10000:
                raise ValueError('text exceeds limit')
            self.page.keyboard.insert_text(text)
        elif action == 'scroll':
            self.page.mouse.wheel(0, max(-1200, min(1200, float(req.get('delta', 0)))))
        elif action == 'back':
            self.page.go_back(wait_until='domcontentloaded')
        elif action == 'snapshot' and not ui:
            return {'ok': True, 'url': self.page.url, 'content': self.page.locator('body').aria_snapshot()[:60000]}
        elif action == 'screenshot' and not ui:
            target = Path('/workspace/output') / ('browser-' + secrets.token_hex(4) + '.png')
            target.parent.mkdir(parents=True, exist_ok=True)
            self.page.screenshot(path=str(target), full_page=True)
            return {'ok': True, 'path': str(target)}
        else:
            raise ValueError('unsupported browser action')
        self.revision += 1
        return self.frame() if ui else {'ok': True, 'url': self.page.url, 'revision': self.revision}


def serve():
    from playwright.sync_api import sync_playwright
    # The lock lives across the daemon's lifetime. Concurrent first commands
    # cannot unlink the socket of an already running controller.
    with open(LOCK, 'w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        with sync_playwright() as p:
            browser = Browser(p)
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
                            conn.settimeout(30)
                            try:
                                request = read_message(conn)
                                if not isinstance(request, dict):
                                    raise ValueError('request must be an object')
                                response = browser.dispatch(request)
                                last_request = time.monotonic()
                            except Exception as exc:
                                response = {'ok': False, 'error': str(exc)[:2000]}
                            try:
                                conn.sendall(json.dumps(response).encode() + b'\n')
                            except OSError:
                                pass
                finally:
                    browser.browser.close()
                    Path(SOCKET).unlink(missing_ok=True)


def request(req, allow_start=False):
    deadline = time.monotonic() + (25 if allow_start else 0)
    spawned = False
    while True:
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
                conn.settimeout(30)
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
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serve', action='store_true')
    parser.add_argument('--request', help='JSON action, e.g. {"action":"open","url":"https://example.com"}')
    parser.add_argument('--ui-request', help='Base64 JSON; authenticated WeKnora UI bridge only')
    args = parser.parse_args()
    if args.serve:
        serve()
        return
    ui = bool(args.ui_request)
    req = json.loads(base64.b64decode(args.ui_request).decode() if ui else (args.request or '{"action":"snapshot"}'))
    req['ui'] = ui
    response = request(req, allow_start=req.get('action') == 'start' or (not ui and req.get('action') == 'open'))
    print(json.dumps(response, ensure_ascii=False))
    sys.exit(0 if response.get('ok') else 1)


if __name__ == '__main__':
    main()
