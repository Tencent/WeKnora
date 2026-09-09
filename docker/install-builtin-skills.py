#!/usr/bin/env python3
"""Install and verify immutable builtin packages during image creation."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path('/opt/weknora/builtin/skills')


def run(*args):
    subprocess.run(args, check=True)


def main():
    manifest = {'schema_version': 1, 'profile': 'office-browser', 'version': '2026.09.2', 'skills': []}
    for skill in sorted(ROOT.iterdir()):
        if not skill.is_dir():
            continue
        digest = hashlib.sha256()
        for file in sorted(p for p in skill.rglob('*') if p.is_file()):
            digest.update(file.relative_to(skill).as_posix().encode() + b'\0')
            digest.update(file.read_bytes() + b'\0')
        python = str(skill / '.venv/bin/python')
        run('uv', 'venv', '--python', '3.12.11', str(skill / '.venv'))
        run('uv', 'pip', 'install', '--python', python, '--require-hashes', '-r', str(skill / 'requirements.lock'))
        if skill.name == 'browser':
            run(python, '-m', 'playwright', 'install', '--with-deps', 'chromium')
        run(python, str(skill / 'scripts/weknora_smoke.py'))
        (skill / '.bundle-digest').write_text(digest.hexdigest() + '\n')
        manifest['skills'].append({'name': skill.name, 'digest': digest.hexdigest(), 'verified': True})
    target = Path('/opt/weknora/runtime-manifest.json')
    target.write_text(json.dumps(manifest, indent=2) + '\n')
    # Never snapshot profiles or credentials from functional browser tests.
    for file in Path('/tmp').glob('weknora-browser*'):
        if file.is_file() or file.is_socket():
            file.unlink()


if __name__ == '__main__':
    main()
