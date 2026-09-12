"""Download model assets without constructing an inference pipeline."""
import hashlib
import json
import platform
import argparse
import time
from concurrent.futures import ThreadPoolExecutor
from importlib.metadata import PackageNotFoundError, version
from pathlib import Path

import requests

parser = argparse.ArgumentParser()
parser.add_argument("--runtime", type=Path, default=Path("/runtime"))
parser.add_argument("--mirror-no-proxy", action="store_true", help="Connect directly to the public ModelScope mirror while retaining proxy settings for Hugging Face file downloads")
args = parser.parse_args()


def download_large(path, repo_id, filename, size):
    """Bounded range downloads keep interrupted multi-GB model transfers resumable."""
    chunk_size = 16 * 1024 * 1024
    chunk_root = path.parent / ".chunks" / filename
    chunk_root.mkdir(parents=True, exist_ok=True)
    url = f"https://modelscope.cn/api/v1/models/{repo_id}/repo"
    chunks = [(start, min(start + chunk_size, size) - 1) for start in range(0, size, chunk_size)]

    def fetch(bounds):
        start, end = bounds
        part = chunk_root / f"{start:012d}"
        if part.exists() and part.stat().st_size == end - start + 1:
            return part
        for attempt in range(5):
            try:
                session = requests.Session()
                session.trust_env = not args.mirror_no_proxy
                with session, session.get(url, params={"Revision": "master", "FilePath": filename}, headers={"Range": f"bytes={start}-{end}"}, stream=True, timeout=(30, 90)) as response:
                    response.raise_for_status()
                    assert response.status_code == 206, "Model mirror must support byte ranges"
                    assert response.headers.get("Content-Range", "").startswith(f"bytes {start}-{end}/"), "Unexpected model byte range"
                    with part.open("wb") as output:
                        for block in response.iter_content(1024 * 1024):
                            output.write(block)
                assert part.stat().st_size == end - start + 1, "Incomplete model chunk"
                return part
            except Exception:
                if attempt == 4:
                    raise
                time.sleep(2 * (attempt + 1))

    with ThreadPoolExecutor(max_workers=4) as executor:
        for index, _ in enumerate(executor.map(fetch, chunks), 1):
            print(f"{path.parent.name}/{filename}: {index}/{len(chunks)} chunks", flush=True)
    temporary = path.with_name(path.name + ".download")
    with temporary.open("wb") as output:
        for start, _ in chunks:
            with (chunk_root / f"{start:012d}").open("rb") as source:
                for block in iter(lambda: source.read(1024 * 1024), b""):
                    output.write(block)
    temporary.replace(path)


def checksums(path):
    sha256 = hashlib.sha256()
    git_blob = hashlib.sha1(f"blob {path.stat().st_size}\0".encode())
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            sha256.update(block)
            git_blob.update(block)
    return sha256.hexdigest(), git_blob.hexdigest()

records = []
model_specs = json.loads(Path(__file__).with_name("models.lock.json").read_text(encoding="utf-8"))
for spec in model_specs:
    model_name, repo_id, revision = spec["name"], spec["repo_id"], spec["revision"]
    model_path = args.runtime / "models" / model_name
    files = []
    for entry in spec["files"]:
        name = entry["path"]
        path = model_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.exists() and path.stat().st_size != entry["size"]:
            path.replace(path.with_name(path.name + ".incomplete"))
        if not path.exists() and entry.get("lfs_sha256") and entry["size"] > 8 * 1024 * 1024:
            download_large(path, repo_id, name, entry["size"])
        elif not path.exists():
            temporary = path.with_name(path.name + ".download")
            with requests.get(f"https://huggingface.co/{repo_id}/resolve/{revision}/{name}", stream=True, timeout=(30, 120)) as download:
                download.raise_for_status()
                with temporary.open("wb") as output_file:
                    for block in download.iter_content(1024 * 1024):
                        output_file.write(block)
            temporary.replace(path)
        digest, git_blob = checksums(path)
        if entry.get("lfs_sha256"):
            assert digest == entry["lfs_sha256"], f"SHA256 mismatch: {name}"
        else:
            assert git_blob == entry["git_blob"], f"Git blob mismatch: {name}"
        files.append({"path": name, "bytes": path.stat().st_size, "sha256": digest})
    records.append({"name": model_name, "repo_id": repo_id, "revision": revision, "path": str(model_path), "files": files})
packages = {}
for name in ("paddlepaddle", "paddleocr", "paddlex", "numpy"):
    try:
        packages[name] = version(name)
    except PackageNotFoundError:
        packages[name] = None
record = {
    "python": platform.python_version(),
    "packages": packages,
    "device": "cpu",
    "models": records,
}
output = args.runtime / "model-manifest.json"
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(record, indent=2), encoding="utf-8")
print(json.dumps({"manifest": str(output), "models": [{"name": item["name"], "files": len(item["files"])} for item in records]}))
