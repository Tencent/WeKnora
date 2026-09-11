#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${1:-$repo_root/artifacts/browserskill}"
source_commit=5aaa36bf79a201ec40b277ce6c24f2ce23ce37ca
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
build_dir="$(mktemp -d /tmp/weknora-bsk-build.XXXXXX)"
trap 'rm -rf "$build_dir"' EXIT

git clone --no-checkout https://github.com/Tencent/BrowserSkill.git "$build_dir/source"
git -C "$build_dir/source" checkout --detach "$source_commit"
git -C "$build_dir/source" apply --check "$repo_root/patches/browserskill/remote-extension-connection.patch"
git -C "$build_dir/source" apply "$repo_root/patches/browserskill/remote-extension-connection.patch"
(
  cd "$build_dir/source"
  npx --yes pnpm@10.17.0 install --frozen-lockfile
  npx --yes pnpm@10.17.0 ext:build:zip
)
cp "$build_dir/source/apps/extension/dist/browser-skillextension-0.2.1-chrome.zip" "$output_dir/browser-skill-weknora-0.2.1.zip"
cp "$build_dir/source/LICENSE" "$output_dir/BrowserSkill-LICENSE"

# Download the server's native daemon using the release's pinned checksums.
python3 - "$repo_root/scripts/browserskill-release.json" "$output_dir" <<'PY'
import hashlib, io, json, pathlib, platform, sys, tarfile, urllib.request
release = json.loads(pathlib.Path(sys.argv[1]).read_text())
os_name = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'x64', 'AMD64': 'x64'}.get(platform.machine())
key = f'{os_name}-{arch}'
if key not in release['assets']:
    raise SystemExit('The WeKnora daemon adapter currently requires macOS or Linux')
asset = release['assets'][key]
with urllib.request.urlopen(asset['url'], timeout=60) as response:
    data = response.read()
if hashlib.sha256(data).hexdigest() != asset['sha256']:
    raise SystemExit('BrowserSkill archive checksum mismatch')
with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
    member = next(m for m in archive.getmembers() if pathlib.PurePosixPath(m.name).name == 'bsk' and m.isfile())
    target = pathlib.Path(sys.argv[2]) / 'bsk'
    target.write_bytes(archive.extractfile(member).read())
    target.chmod(0o755)
print(f'BrowserSkill {release["version"]} artifacts: {sys.argv[2]}')
PY
