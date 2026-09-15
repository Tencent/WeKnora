#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${1:-$repo_root/artifacts/browserskill}"
# Optional Docker-style target; native builds default to the host platform.
target_platform="${2:-}"
case "$target_platform" in
  ""|linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;;
  *) echo "Unsupported BrowserSkill target: $target_platform" >&2; exit 1 ;;
esac
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
python3 - "$repo_root/scripts/browserskill-release.json" "$output_dir" "$target_platform" <<'PY'
import hashlib, io, json, os, pathlib, platform, sys, tarfile, tempfile, urllib.request
release = json.loads(pathlib.Path(sys.argv[1]).read_text())
os_name = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'x64', 'AMD64': 'x64'}.get(platform.machine())
if sys.argv[3]:
    os_name, target_arch = sys.argv[3].split('/')
    arch = {'amd64': 'x64', 'arm64': 'arm64'}[target_arch]
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
    # Never overwrite the inode of an executing daemon. On macOS this can
    # invalidate executable pages even when the replacement bytes are identical.
    with tempfile.NamedTemporaryFile(dir=target.parent, prefix='.bsk-', delete=False) as output:
        staged = pathlib.Path(output.name)
        output.write(archive.extractfile(member).read())
    try:
        staged.chmod(0o755)
        os.replace(staged, target)
    finally:
        staged.unlink(missing_ok=True)
print(f'BrowserSkill {release["version"]} artifacts: {sys.argv[2]}')
PY
