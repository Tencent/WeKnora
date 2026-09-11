#!/usr/bin/env python3
"""Private JSON-lines bridge from WeKnora to the local agent-browser stream."""
import argparse
import json
import queue
import select
from pathlib import Path
import sys
import threading
import time

import websocket
import browser

PREFIX = 'WK_BROWSER_STREAM '


def serve(token):
    if not isinstance(token, str) or not 24 <= len(token) <= 128:
        raise ValueError('invalid control token')
    info = browser.request({'action': 'stream', 'ui': True})
    if not info.get('ok'):
        raise ValueError(info.get('error', 'browser is not running'))
    port = info.get('port')
    if not isinstance(port, int) or not 1 <= port <= 65535:
        raise ValueError('invalid local stream port')
    # Endpoint is discovered inside the sandbox, never accepted from the UI.
    ws = websocket.create_connection(f'ws://127.0.0.1:{port}/?pacing=ack&maxFps=10',
                                     timeout=5, suppress_origin=True, http_no_proxy=['127.0.0.1'])
    stopped = threading.Event()
    output_lock = threading.Lock()
    actions = queue.Queue(maxsize=32)

    def emit(value):
        with output_lock:
            sys.stdout.write(PREFIX + json.dumps(value, separators=(',', ':')) + '\n')
            sys.stdout.flush()

    def frames():
        try:
            while not stopped.is_set():
                try:
                    raw = ws.recv()
                except websocket.WebSocketTimeoutException:
                    continue
                if not raw:
                    break
                if len(raw) > browser.MAX_BYTES:
                    raise ValueError('stream frame exceeds limit')
                value = json.loads(raw)
                if value.get('type') in ('frame', 'url', 'status'):
                    emit(value)
        except Exception as exc:
            if not stopped.is_set():
                emit({'type': 'error', 'message': str(exc)[:500]})
        finally:
            stopped.set()

    def commands():
        while not stopped.is_set():
            try:
                item = actions.get(timeout=0.5)
            except queue.Empty:
                continue
            command = item['command']
            # Never forward upstream input messages: the controller owns the lease.
            command.update(ui=True, stream=True, token=token)
            try:
                result = browser.request(command)
            except Exception as exc:
                result = {'ok': False, 'error': str(exc)[:500]}
            emit({'type': 'result', 'id': item['id'], 'data': result})

    threading.Thread(target=frames, daemon=True).start()
    threading.Thread(target=commands, daemon=True).start()
    emit({'type': 'ready'})
    last_input = time.monotonic()
    buffer = bytearray()
    try:
        while not stopped.is_set() and time.monotonic() - last_input < 35:
            readable, _, _ = select.select([sys.stdin], [], [], 1)
            if not readable:
                continue
            import os
            chunk = os.read(sys.stdin.fileno(), 65536)
            if not chunk:
                break
            buffer.extend(chunk)
            if len(buffer) > 128 * 1024:
                raise ValueError('stream input exceeds limit')
            while b'\n' in buffer:
                line, _, rest = buffer.partition(b'\n')
                buffer = bytearray(rest)
                item = json.loads(line)
                last_input = time.monotonic()
                kind = item.get('type')
                if kind == 'disconnect':
                    return
                if kind == 'ack':
                    seq = item.get('seq')
                    if isinstance(seq, int) and seq >= 0:
                        ws.send(json.dumps({'type': 'ack', 'seq': seq}))
                elif kind == 'command':
                    try:
                        actions.put_nowait(item)
                    except queue.Full:
                        emit({'type': 'result', 'id': item.get('id'),
                              'data': {'ok': False, 'error': 'too many pending browser actions'}})
                elif kind == 'ping':
                    ws.ping()
                    # Keep the controller and Docker idle marker alive while watched.
                    info = browser.request({'action': 'stream', 'ui': True})
                    if not info.get('ok'):
                        return
                    marker = Path('/var/lib/weknora-sandbox-activity')
                    if marker.exists():
                        marker.touch()
    finally:
        stopped.set()
        ws.close()
        # Also releases a pressed pointer. A dead transport has the lease timeout.
        try:
            browser.request({'action': 'release', 'ui': True, 'token': token})
        except Exception:
            pass


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--token', required=True)
    args = parser.parse_args()
    try:
        serve(args.token)
    except Exception as exc:
        print(PREFIX + json.dumps({'type': 'error', 'message': str(exc)[:500]}), flush=True)
        sys.exit(1)
