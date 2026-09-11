#!/usr/bin/env python3
"""Install the verified native agent-browser binary and its session adapter."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


def install_binary(asset, target):
    if target.exists() and hashlib.sha256(target.read_bytes()).hexdigest() == asset['sha256']:
        target.chmod(0o755)
        return
    target.parent.mkdir(parents=True, exist_ok=True)
    request = urllib.request.Request(asset['url'], headers={'User-Agent': 'WeKnora-browser-installer'})
    with urllib.request.urlopen(request, timeout=90) as response:
        content = response.read(64 * 1024 * 1024 + 1)
    if len(content) > 64 * 1024 * 1024 or hashlib.sha256(content).hexdigest() != asset['sha256']:
        raise ValueError('agent-browser download failed SHA-256 verification')
    with tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as tmp:
        tmp.write(content)
        temporary = Path(tmp.name)
    try:
        temporary.chmod(0o755)
        temporary.replace(target)
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--with-browser', action='store_true', help='Install Debian Chromium if no browser is available')
    parser.add_argument('--browser-path', help='Use an already installed Chrome/Chromium executable')
    args = parser.parse_args()
    lock = json.loads((ROOT / 'runtime.lock.json').read_text())
    arch = platform.machine().lower()
    if platform.system() != 'Linux' or arch not in lock['assets']:
        raise RuntimeError('This sandbox package supports Linux x86_64 and aarch64')
    browser_path = args.browser_path or next((shutil.which(x) for x in ('chromium', 'chromium-browser', 'google-chrome') if shutil.which(x)), None)
    if not browser_path and args.with_browser:
        os_release = Path('/etc/os-release').read_text()
        if 'ID=debian' not in os_release or not shutil.which('apt-get'):
            raise RuntimeError('Install Chrome/Chromium for this distribution, then rerun with --browser-path. Automatic installation supports Debian sandbox images.')
        prefix = [] if os.geteuid() == 0 else ['sudo', '-n']
        subprocess.run([*prefix, 'apt-get', 'update'], check=True)
        subprocess.run([*prefix, 'apt-get', 'install', '-y', '--no-install-recommends', 'chromium'], check=True)
        browser_path = shutil.which('chromium')
    if not browser_path or not os.path.isabs(browser_path) or not os.access(browser_path, os.X_OK):
        raise RuntimeError('Chrome/Chromium is required. Use --with-browser on Debian or --browser-path /absolute/path.')
    target = ROOT / '.weknora/lib/agent-browser'
    install_binary(lock['assets'][arch], target)
    version = subprocess.check_output([str(target), '--version'], text=True).strip()
    if version != 'agent-browser ' + lock['version']:
        raise RuntimeError('unexpected agent-browser version: ' + version)
    python = ROOT / '.venv/bin/python'
    if not python.exists():
        subprocess.run([sys.executable, '-m', 'venv', '--without-pip', str(ROOT / '.venv')], check=True)
    subprocess.run([str(python), '-m', 'ensurepip'], check=True)
    subprocess.run([str(python), '-m', 'pip', 'install', '--only-binary=:all:', '--require-hashes',
                    '-r', str(ROOT / 'requirements.lock')], check=True)
    guide = ROOT / '.weknora/skill-data/core'
    shutil.copytree(ROOT / 'upstream-core', guide, dirs_exist_ok=True)
    (guide / 'SKILL.md').write_bytes((guide / 'CORE.md').read_bytes())
    (guide / 'CORE.md').unlink()
    command = ROOT / '.weknora/bin/agent-browser'
    command.parent.mkdir(parents=True, exist_ok=True)
    command.write_text('#!/bin/sh\nroot="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"\nexec "$root/.venv/bin/python" "$root/scripts/browser.py" "$@"\n')
    command.chmod(0o755)
    report = {'agent_browser_version': lock['version'], 'sha256': lock['assets'][arch]['sha256'],
              'browser_path': browser_path,
              'browser_version': subprocess.check_output([browser_path, '--version'], text=True).strip()}
    (ROOT / '.weknora/runtime.json').write_text(json.dumps(report) + '\n')
    print(json.dumps({'ok': True, **report}))


if __name__ == '__main__':
    main()
