"""Download the fixed MinerU 3.4.5 CPU pipeline models for Chinese and English."""
from __future__ import annotations

import hashlib
import json
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import requests

REPOSITORY = "OpenDataLab/PDF-Extract-Kit-1.0"
REVISION = "05eaf85cc4ddab92c2be61e10abec4586d25c1a6"
PATTERNS = [
    "models/Layout/PP-DocLayoutV2",
    "models/MFR/unimernet_hf_small_2503",
    "models/OCR/paddleocr_torch/ch_PP-OCRv6_small_det_infer.safetensors",
    "models/OCR/paddleocr_torch/ch_PP-OCRv6_small_rec_infer.safetensors",
    "models/TabRec/SlanetPlus/slanet-plus.onnx",
    "models/TabRec/UnetStructure/unet.onnx",
    "models/TabCls/paddle_table_cls/PP-LCNet_x1_0_table_cls.onnx",
]


def checksum(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def download(entry: dict, path: Path) -> None:
    size = entry["Size"]
    if path.exists() and path.stat().st_size == size:
        if not entry.get("Sha256") or checksum(path) == entry["Sha256"]:
            return
    path.parent.mkdir(parents=True, exist_ok=True)
    url = f"https://modelscope.cn/api/v1/models/{REPOSITORY}/repo"
    params = {"Revision": REVISION, "FilePath": entry["Path"]}
    temporary = path.with_name(path.name + ".range-download")
    if size <= 16 * 1024 * 1024:
        with requests.get(url, params=params, stream=True, timeout=(30, 180)) as response:
            response.raise_for_status()
            with temporary.open("wb") as output:
                for block in response.iter_content(1024 * 1024):
                    output.write(block)
    else:
        chunk_size = 16 * 1024 * 1024
        chunks = [(start, min(start + chunk_size, size) - 1) for start in range(0, size, chunk_size)]
        chunk_root = Path("/state/range-parts") / entry["Path"]
        chunk_root.mkdir(parents=True, exist_ok=True)

        def fetch(bounds):
            start, end = bounds
            part = chunk_root / str(start)
            if part.exists() and part.stat().st_size == end - start + 1:
                return
            for attempt in range(5):
                try:
                    with requests.get(url, params=params, headers={"Range": f"bytes={start}-{end}"}, stream=True, timeout=(30, 120)) as response:
                        response.raise_for_status()
                        if response.status_code != 206 or not response.headers.get("Content-Range", "").startswith(f"bytes {start}-{end}/"):
                            raise ValueError("Model source returned an unexpected byte range")
                        with part.open("wb") as output:
                            for block in response.iter_content(1024 * 1024):
                                output.write(block)
                    if part.stat().st_size != end - start + 1:
                        raise ValueError("Incomplete model chunk")
                    return
                except Exception:
                    if attempt == 4:
                        raise
                    time.sleep(2 * (attempt + 1))
        with ThreadPoolExecutor(max_workers=6) as executor:
            for index, _ in enumerate(executor.map(fetch, chunks), 1):
                print(f'{entry["Path"]}: {index}/{len(chunks)} chunks', flush=True)
        with temporary.open("wb") as output:
            for start, _ in chunks:
                with (chunk_root / str(start)).open("rb") as source:
                    for block in iter(lambda: source.read(1024 * 1024), b""):
                        output.write(block)
    if temporary.stat().st_size != size:
        raise ValueError(f'Wrong file size: {entry["Path"]}')
    if entry.get("Sha256") and checksum(temporary) != entry["Sha256"]:
        raise ValueError(f'Model SHA256 mismatch: {entry["Path"]}')
    temporary.replace(path)


def main() -> None:
    root = Path(f"/state/modelscope/models/OpenDataLab--PDF-Extract-Kit-1.0/snapshots/{REVISION}")
    response = requests.get(f"https://modelscope.cn/api/v1/models/{REPOSITORY}/repo/files", params={"Revision": REVISION, "Recursive": "true"}, timeout=60)
    response.raise_for_status()
    entries = [entry for entry in response.json()["Data"]["Files"] if entry["Type"] == "blob" and any(entry["Path"] == pattern or entry["Path"].startswith(pattern + "/") for pattern in PATTERNS)]
    for entry in sorted(entries, key=lambda entry: -entry["Size"]):
        path = root / entry["Path"]
        if not path.resolve().is_relative_to(root.resolve()):
            raise ValueError("Unexpected model file path")
        download(entry, path)
    config = {
        "config_version": "1.3.2",
        "models-dir": {"pipeline": str(root)},
        "model-source": "modelscope",
    }
    (Path.home() / "mineru.json").write_text(json.dumps(config, indent=2), encoding="utf-8")
    files = []
    for base in PATTERNS:
        model_path = root / base
        if not model_path.exists():
            raise FileNotFoundError(model_path)
        for item in sorted(model_path.rglob("*")) if model_path.is_dir() else [model_path]:
            if item.is_file():
                if item.name.endswith((".incomplete", ".range-download")):
                    continue
                files.append({"path": str(item.relative_to(root)), "size": item.stat().st_size, "sha256": checksum(item)})
    metadata = {"repository": REPOSITORY, "revision": REVISION, "mineru_version": "3.4.5", "files": files}
    Path("/state/models-manifest.json").write_text(json.dumps(metadata, indent=2), encoding="utf-8")
    print(json.dumps({"model_root": str(root), "files": len(files), "bytes": sum(f["size"] for f in files)}))


if __name__ == "__main__":
    main()
