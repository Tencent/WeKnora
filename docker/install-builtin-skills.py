#!/usr/bin/env python3
"""Install locked environments, then verify the exact published skill resources."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path('/opt/weknora/builtin/skills')
RUNTIME_FILES = {'.venv', '.bundle-digest', '.requirements-digest', 'node_modules', '.package-lock-digest'}


def run(*args):
    subprocess.run(args, check=True)


def resource_digest(skill):
    digest = hashlib.sha256()
    for file in sorted(p for p in skill.rglob('*')
                       if p.is_file() and p.relative_to(skill).parts[0] not in RUNTIME_FILES):
        digest.update(file.relative_to(skill).as_posix().encode() + b'\0')
        digest.update(file.read_bytes() + b'\0')
    return digest.hexdigest()


def install(locks):
    for lock in sorted(locks.glob('*.lock')):
        skill = ROOT / lock.stem
        skill.mkdir(parents=True, exist_ok=True)
        python = str(skill / '.venv/bin/python')
        run('uv', 'venv', '--python', '3.12.11', str(skill / '.venv'))
        run('uv', 'pip', 'install', '--python', python, '--require-hashes', '-r', str(lock))
        run('uv', 'pip', 'check', '--python', python)
        (skill / '.requirements-digest').write_text(hashlib.sha256(lock.read_bytes()).hexdigest() + '\n')


def node_digest(skill):
    return hashlib.sha256((skill / 'package.json').read_bytes() + b'\0' + (skill / 'package-lock.json').read_bytes()).hexdigest()


def install_node(source):
    import shutil
    skill = ROOT / source.name
    skill.mkdir(parents=True, exist_ok=True)
    for name in ('package.json', 'package-lock.json'):
        shutil.copyfile(source / name, skill / name)
    run('npm', 'ci', '--omit=dev', '--prefix', str(skill))
    (skill / '.package-lock-digest').write_text(node_digest(skill) + '\n')


def verify(manifest):
    if manifest.get('schema_version') != 1 or manifest.get('profile') != 'office-core':
        raise ValueError('missing or unsupported builtin manifest')
    declarations = manifest['skills']
    names = [item['name'] for item in declarations]
    installed = {p.name for p in ROOT.iterdir() if p.is_dir()}
    if len(set(names)) != len(names) or set(names) != installed:
        raise ValueError('manifest skill names differ from installed packages')
    if installed - {'docx', 'xlsx', 'pdf', 'powerpoint'}:
        raise ValueError('non-core skills are not supported in the image')
    for item in declarations:
        skill = ROOT / item['name']
        digest = resource_digest(skill)
        if not item.get('verified') or item['digest'] != digest:
            raise ValueError(f'resource digest mismatch: {skill.name}')
        lock_digest = hashlib.sha256((skill / 'requirements.lock').read_bytes()).hexdigest()
        if (skill / '.requirements-digest').read_text().strip() != lock_digest:
            raise ValueError(f'installed dependency lock mismatch: {skill.name}')
        if (skill / 'package-lock.json').exists():
            if (skill / '.package-lock-digest').read_text().strip() != node_digest(skill):
                raise ValueError(f'installed Node dependency lock mismatch: {skill.name}')
            run('npm', 'ls', '--omit=dev', '--all', '--prefix', str(skill))
        python = str(skill / '.venv/bin/python')
        run('uv', 'pip', 'check', '--python', python)
        run(python, str(skill / 'scripts/weknora_smoke.py'))
        (skill / '.bundle-digest').write_text(digest + '\n')
    Path('/opt/weknora/runtime-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    commands.add_parser('install').add_argument('--locks', type=Path, required=True)
    commands.add_parser('install-node').add_argument('--source', type=Path, required=True)
    commands.add_parser('verify').add_argument('--manifest', required=True)
    args = parser.parse_args()
    if args.command == 'install':
        install(args.locks)
    elif args.command == 'install-node':
        install_node(args.source)
    else:
        verify(json.loads(args.manifest))


if __name__ == '__main__':
    main()
