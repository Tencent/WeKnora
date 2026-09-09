#!/usr/bin/env python3
"""Verify/import reviewed upstream resources, pinned by commit and SHA-256.

Default: offline verification. --fetch restores the locked files from upstream.
--archive REPOSITORY=PATH restores from a downloaded tarball instead. Nothing is
executed, no full repository is extracted, and adapted files are never replaced.
To update upstream versions, review the new files and update sources.lock.json.
"""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1] / 'internal/builtin/skills'
MAX_FILE = 8 * 1024 * 1024


def safe_path(value):
    path = PurePosixPath(value)
    if path.is_absolute() or '..' in path.parts or '\\' in value:
        raise ValueError('invalid locked path')
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fetch', action='store_true')
    parser.add_argument('--archive', action='append', default=[], metavar='REPOSITORY=PATH')
    args = parser.parse_args()
    archives = dict(value.split('=', 1) for value in args.archive)
    lock = json.loads((ROOT / 'sources.lock.json').read_text())
    count = 0
    for source in lock['sources']:
        repo, commit = source['repository'], source['commit']
        if len(commit) != 40 or any(c not in '0123456789abcdef' for c in commit):
            raise ValueError('full commit required')
        archive = tarfile.open(archives[repo]) if repo in archives else None
        try:
            members = {m.name.split('/', 1)[-1]: m for m in archive.getmembers()} if archive else {}
            for entry in source['files']:
                dest = ROOT / safe_path(entry['path'])
                remote = str(safe_path(entry['source_path']))
                if archive:
                    member = members[remote]
                    if not member.isfile() or member.size > MAX_FILE:
                        raise ValueError('unsupported tar entry')
                    content = archive.extractfile(member).read(MAX_FILE + 1)
                elif args.fetch:
                    request = urllib.request.Request(f'https://raw.githubusercontent.com/{repo}/{commit}/{remote}', headers={'User-Agent': 'WeKnora-skill-vendor'})
                    with urllib.request.urlopen(request, timeout=30) as response:
                        content = response.read(MAX_FILE + 1)
                else:
                    content = dest.read_bytes()
                if len(content) > MAX_FILE or hashlib.sha256(content).hexdigest() != entry['sha256']:
                    raise ValueError(f'content mismatch: {entry["path"]}')
                if archive or args.fetch:
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    dest.write_bytes(content)
                count += 1
        finally:
            if archive:
                archive.close()
    print(f'Verified {count} pinned upstream files')


if __name__ == '__main__':
    main()
