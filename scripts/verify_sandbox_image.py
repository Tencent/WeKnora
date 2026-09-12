#!/usr/bin/env python3
"""Check image metadata against this checkout and test office output and the entrypoint."""
import argparse
import json
from pathlib import Path
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def output(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True)


def verify_cjk_fonts(image):
    for language in ('zh-cn', 'zh-tw'):
        fonts = output('docker', 'run', '--rm', '--network', 'none',
                       '--entrypoint', 'fc-list', image, f':lang={language}', 'family')
        if not fonts.strip():
            raise ValueError(f'no font supports {language}')


def verify(image, profile, backend):
    verify_cjk_fonts(image)
    expected = json.loads(output('go', 'run', './cmd/builtin-skills-manifest', '-profile', profile))
    metadata = json.loads(output('docker', 'image', 'inspect', image))[0]
    labels = metadata['Config']['Labels']
    if json.loads(labels['org.weknora.skills.manifest']) != expected:
        raise ValueError('image label does not match this checkout/profile')
    if labels['org.weknora.sandbox.profile'] != profile or labels['org.weknora.skills.version'] != expected['version']:
        raise ValueError('profile/version labels do not match the manifest')
    runtime = json.loads(output('docker', 'run', '--rm', '--entrypoint', 'cat', image,
                                '/opt/weknora/runtime-manifest.json'))
    if runtime != expected:
        raise ValueError('image runtime manifest does not match the label')
    inventory = json.loads(output('docker', 'run', '--rm', '--entrypoint', 'python3', image, '-c',
        "import json; from pathlib import Path; "
        "print(json.dumps({'skills': sorted(p.name for p in Path('/opt/weknora/builtin/skills').iterdir() if p.is_dir())}))"))
    if inventory['skills'] != sorted(s['name'] for s in expected['skills']):
        raise ValueError('installed skill directories do not match the manifest')
    subprocess.run(['docker', 'run', '--rm', '-i',
        '--entrypoint', '/opt/weknora/builtin/skills/powerpoint/.venv/bin/python', image, '-'],
        input=(ROOT / 'scripts/test_builtin_powerpoint.py').read_bytes(), check=True)
    if backend == 'cube':
        name = 'weknora-cube-check-' + uuid.uuid4().hex[:12]
        subprocess.run(['docker', 'run', '-d', '--name', name, image], check=True)
        try:
            for _ in range(30):
                result = subprocess.run(['docker', 'exec', name, 'curl', '--fail', '--silent',
                    '--max-time', '2', 'http://127.0.0.1:49983/health'], capture_output=True)
                if result.returncode == 0:
                    break
                time.sleep(1)
            else:
                raise RuntimeError('Cube entrypoint did not become healthy on port 49983')
        finally:
            subprocess.run(['docker', 'rm', '-f', name], check=True)
    print(json.dumps({'image': image, 'profile': profile, 'backend': backend, 'verified': True}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('image')
    parser.add_argument('--profile', choices=['office-core'], default='office-core')
    parser.add_argument('--backend', choices=['sandbox', 'cube'], default='sandbox')
    args = parser.parse_args()
    verify(args.image, args.profile, args.backend)
