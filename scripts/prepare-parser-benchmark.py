#!/usr/bin/env python3
"""Download a frozen, stratified research subset; never synthesize OCR text layers.

Requires pypdf, Pillow, reportlab. Inputs/annotations remain separate. PDFs from
olmOCR-bench are byte-for-byte upstream files; OmniDocBench image pixels are
losslessly embedded into PDF because that release distributes images, not PDFs.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict, deque
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path
import shutil
import time
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
DATA = ROOT / "artifacts/parser-benchmark/data"
CATALOG = ROOT / "dataset/parser-benchmark"
OMNI_REV = "aa1ee96d106dbe53d0ae59474d75c6e6d9b53fec"
OMNI_CODE = "193627ae9e97d89188468ed1ee3b7a856ff76044"
OLM_REV = "54a96a6fb6a2bd3b297e59869491db4d3625b711"
OLM_CODE = "f7cfe4c22098b154c76b6ec950d1c0a464eecf8d"
OMNI_JSON_SHA256 = "a45cd84b04ad8b793e775089640e6b681209abea33ead54c1828ddca35fae496"
SEED = "weknora-topic3-parser-v1"
OLM_CATEGORIES = ["multi_column", "table_tests", "old_scans", "old_scans_math", "arxiv_math", "headers_footers", "long_tiny_text"]


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def rel(path: Path) -> str:
    return path.relative_to(ROOT).as_posix()


def write_json(path: Path, value) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    # Freeze the original Windows-authored reference bytes across operating systems.
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\r\n")


def download(url: str, path: Path, expected_sha: str | None = None, force=False) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    if not force and path.exists() and (not expected_sha or digest(path) == expected_sha):
        return path
    for attempt in range(4):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": "WeKnora-research-benchmark/1"})
            with urllib.request.urlopen(request, timeout=90) as response, path.with_suffix(path.suffix + ".part").open("wb") as target:
                shutil.copyfileobj(response, target)
                expected_size = response.headers.get("Content-Length")
                if expected_size and target.tell() != int(expected_size):
                    raise ValueError(f"Incomplete download: {url}")
            temporary = path.with_suffix(path.suffix + ".part")
            if expected_sha and digest(temporary) != expected_sha:
                raise ValueError(f"SHA-256 mismatch: {url}")
            temporary.replace(path)
            return path
        except Exception:
            if attempt == 3:
                raise
            time.sleep(1 + attempt)
    raise AssertionError("unreachable")


def hf_url(repository: str, revision: str, name: str) -> str:
    return f"https://huggingface.co/datasets/{repository}/resolve/{revision}/{urllib.parse.quote(name, safe='/')}"


def rank(name: str) -> str:
    return hashlib.sha256((SEED + name).encode()).hexdigest()


def balanced_selection(records: list, count: int, key, identity) -> list:
    """Round-robin strata, seeded hash order inside each stratum; no score access."""
    groups = defaultdict(list)
    for row in records:
        groups[key(row)].append(row)
    queues = [deque(sorted(groups[k], key=lambda x: rank(identity(x)))) for k in sorted(groups)]
    chosen = []
    while len(chosen) < count:
        progress = False
        for queue in queues:
            if queue and len(chosen) < count:
                chosen.append(queue.popleft())
                progress = True
        if not progress:
            break
    return chosen


def omni_identity(row):
    return Path(row["page_info"]["image_path"]).name


def omni_stratum(row):
    a = row["page_info"]["page_attribute"]
    return (a.get("language", "unknown"), a.get("data_source", "unknown"), a.get("layout", "unknown"))


def read_omni() -> list:
    path = DATA / "upstream/OmniDocBench.json"
    preliminary = DATA / "upstream/omni_annotation.txt"
    if not path.exists() and preliminary.exists() and digest(preliminary) == OMNI_JSON_SHA256:
        preliminary.replace(path)
    download(hf_url("opendatalab/OmniDocBench", OMNI_REV, "OmniDocBench.json"), path, OMNI_JSON_SHA256)
    return json.loads(path.read_text(encoding="utf-8"))


def read_olm() -> list:
    def category_rows(category):
        path = download(hf_url("allenai/olmOCR-bench", OLM_REV, f"bench_data/{category}.jsonl"), DATA / f"upstream/olm/{category}.jsonl")
        groups = defaultdict(list)
        for line in path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                row = json.loads(line)
                groups[row["pdf"]].append(row)
        return [{"pdf": pdf, "category": category, "tests": tests} for pdf, tests in groups.items()]
    with ThreadPoolExecutor(4) as pool:
        return [row for group in pool.map(category_rows, OLM_CATEGORIES) for row in group]


def wrap_image(image_path: Path, pdf_path: Path) -> dict:
    from PIL import Image
    from reportlab.pdfgen.canvas import Canvas
    from reportlab.lib.utils import ImageReader
    from pypdf import PdfReader
    with Image.open(image_path) as image:
        width, height = image.size
        image.load()
    # 200 dpi is an explicit packaging coordinate convention, not source metadata.
    size = (width * 72 / 200, height * 72 / 200)
    pdf_path.parent.mkdir(parents=True, exist_ok=True)
    if not pdf_path.exists():
        canvas = Canvas(str(pdf_path), pagesize=size, invariant=1, pageCompression=1)
        canvas.setTitle("OmniDocBench research image input")
        canvas.drawImage(ImageReader(str(image_path)), 0, 0, width=size[0], height=size[1])
        canvas.showPage()
        canvas.save()
    reader = PdfReader(pdf_path)
    text_chars = sum(len(page.extract_text() or "") for page in reader.pages)
    if text_chars:
        raise ValueError("Image PDF unexpectedly has a text layer")
    return {"width_pixels": width, "height_pixels": height, "wrapper_dpi": 200, "text_layer_characters": text_chars, "has_text_layer": False, "pages": len(reader.pages)}


def prepare_omni(row) -> dict:
    name = omni_identity(row)
    sample_id = "omni-" + hashlib.sha256(name.encode()).hexdigest()[:16]
    image_path = DATA / f"images/{sample_id}{Path(name).suffix}"
    url = hf_url("opendatalab/OmniDocBench", OMNI_REV, "images/" + name)
    download(url, image_path)
    pdf_path = DATA / f"pdfs/{sample_id}.pdf"
    try:
        pdf_info = wrap_image(image_path, pdf_path)
    except (OSError, ValueError):
        download(url, image_path, force=True)
        pdf_info = wrap_image(image_path, pdf_path)
    reference = DATA / f"references/{sample_id}.json"
    write_json(reference, row)
    attr = row["page_info"]["page_attribute"]
    counts = Counter(x["category_type"] for x in row["layout_dets"] if not x.get("ignore"))
    return {"id": sample_id, "source": "OmniDocBench", "source_sample_id": name,
            "source_url": url, "pdf_path": rel(pdf_path), "sha256": digest(pdf_path),
            "image_path": rel(image_path), "image_sha256": digest(image_path),
            "input_kind": "image_wrapped_pdf", "input_details": pdf_info,
            "language": attr.get("language", "unknown"), "category": attr.get("data_source", "unknown"),
            "layout": attr.get("layout", "unknown"), "page_attributes": attr,
            "element_counts": dict(counts),
            "reference": {"format": "omnidocbench_page", "path": rel(reference), "sha256": digest(reference)},
            "human_review": {"status": "pending", "reviewer": None}}


def prepare_olm(row) -> dict:
    from pypdf import PdfReader
    name = row["pdf"]
    sample_id = "olm-" + hashlib.sha256(name.encode()).hexdigest()[:16]
    pdf_path = DATA / f"pdfs/{sample_id}.pdf"
    url = hf_url("allenai/olmOCR-bench", OLM_REV, "bench_data/pdfs/" + name)
    download(url, pdf_path)
    reader = PdfReader(pdf_path)
    if len(reader.pages) != 1 or any(t.get("page", 1) != 1 for t in row["tests"]):
        raise ValueError(f"Expected official single-page PDF: {name}")
    chars = sum(len(page.extract_text() or "") for page in reader.pages)
    reference = DATA / f"references/{sample_id}.json"
    write_json(reference, {"tests": row["tests"]})
    return {"id": sample_id, "source": "olmOCR-bench", "source_sample_id": name,
            "source_url": url, "pdf_path": rel(pdf_path), "sha256": digest(pdf_path),
            "input_kind": "upstream_original_pdf", "input_details": {"pages": 1, "has_text_layer": chars > 0, "text_layer_characters": chars},
            "language": "english", "category": row["category"], "layout": "upstream_category",
            "reference": {"format": "olmocr_tests", "path": rel(reference), "sha256": digest(reference), "test_count": len(row["tests"])},
            "human_review": {"status": "pending", "reviewer": None}}


def source_metadata():
    return [
        {"name": "OmniDocBench", "dataset": "https://huggingface.co/datasets/opendatalab/OmniDocBench", "revision": OMNI_REV,
         "code": "https://github.com/opendatalab/OmniDocBench", "code_revision": OMNI_CODE,
         "annotation_sha256": OMNI_JSON_SHA256, "usage": "research_only; original document copyrights remain with owners",
         "license_url": f"https://github.com/opendatalab/OmniDocBench/blob/{OMNI_CODE}/README.md#copyright-statement"},
        {"name": "olmOCR-bench", "dataset": "https://huggingface.co/datasets/allenai/olmOCR-bench", "revision": OLM_REV,
         "code": "https://github.com/allenai/olmocr", "code_revision": OLM_CODE,
         "usage": "Follow dataset card and original document rights; sampled original PDFs are not committed",
         "license_url": f"https://huggingface.co/datasets/allenai/olmOCR-bench/blob/{OLM_REV}/README.md"},
    ]


def make_manifest(samples, split):
    return {"schema_version": 1, "name": "WeKnora topic3 public parser benchmark", "split": split,
            "seed": SEED, "is_official_leaderboard_run": False,
            "selection": "Frozen seeded hash order, round-robin language/category/layout strata; 80 OmniDocBench + 20 olmOCR-bench; smoke prefix 6+2.",
            "sources": source_metadata(), "samples": samples,
            "counts": {"total": len(samples), "source": dict(Counter(x["source"] for x in samples)),
                       "language": dict(Counter(x["language"] for x in samples)), "category": dict(Counter(x["category"] for x in samples)),
                       "input_kind": dict(Counter(x["input_kind"] for x in samples)),
                       "has_text_layer": dict(Counter(str(x["input_details"]["has_text_layer"]) for x in samples))}}


def write_manifest_verified(path, manifest):
    """Once published, a manifest's sample identities and input bytes stay frozen."""
    if path.exists():
        previous = json.loads(path.read_text(encoding="utf-8"))
        old = {row["id"]: row for row in previous["samples"]}
        new = {row["id"]: row for row in manifest["samples"]}
        if old.keys() != new.keys():
            raise ValueError(f"Frozen manifest sample selection changed: {path}")
        for sample_id in old:
            if old[sample_id]["sha256"] != new[sample_id]["sha256"] or old[sample_id]["reference"]["sha256"] != new[sample_id]["reference"]["sha256"]:
                raise ValueError(f"Frozen sample bytes changed: {sample_id}; check the pinned packaging runtime")
    write_json(path, manifest)


def verify_wrapped_images(samples, output_path):
    from PIL import Image
    from pypdf import PdfReader, filters
    records = []
    previous_limit = filters.ZLIB_MAX_OUTPUT_LENGTH
    # Bounded increase only for local verification of known high-resolution inputs.
    filters.ZLIB_MAX_OUTPUT_LENGTH = 300 * 1024 * 1024
    try:
        for sample in samples:
            if sample["source"] != "OmniDocBench":
                continue
            page = PdfReader(ROOT / sample["pdf_path"]).pages[0]
            obj = next(iter(page["/Resources"]["/XObject"].values())).get_object()
            embedded = obj.get_data()
            original_path = ROOT / sample["image_path"]
            with Image.open(original_path) as source:
                width, height = source.size
                if "/DCTDecode" in [str(value) for value in obj.get("/Filter", [])]:
                    expected = original_path.read_bytes()
                    method = "original_jpeg_codestream_bytes"
                else:
                    expected = source.convert("RGB" if str(obj["/ColorSpace"]) == "/DeviceRGB" else "L").tobytes()
                    method = "decoded_pixel_bytes"
            records.append({"id": sample["id"], "verification_method": method,
                            "dimensions_match": width == obj["/Width"] and height == obj["/Height"],
                            "image_content_match": hashlib.sha256(embedded).digest() == hashlib.sha256(expected).digest(),
                            "text_characters": len(page.extract_text() or "")})
    finally:
        filters.ZLIB_MAX_OUTPUT_LENGTH = previous_limit
    valid = all(row["image_content_match"] and row["dimensions_match"] and row["text_characters"] == 0 for row in records)
    write_json(output_path, {"checked": len(records), "all_image_content_match": valid,
                            "note": "JPEG codestream bytes and PNG decoded pixel bytes are checked without re-encoding extracted JPEGs.", "samples": records})
    if not valid:
        raise ValueError("Wrapped image differs from its upstream source")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--smoke-only", action="store_true")
    parser.add_argument("--workers", type=int, default=4)
    parser.add_argument("--restore-evaluators", action="store_true", help="Fetch and SHA-256 check the frozen official scorer files listed in evaluators-lock.json")
    parser.add_argument("--verify-wrappers", action="store_true", help="Compare every embedded JPEG codestream or PNG pixel stream against upstream")
    args = parser.parse_args()
    if args.restore_evaluators:
        lock = json.loads((CATALOG / "evaluators-lock.json").read_text(encoding="utf-8"))
        for row in lock["files"]:
            download(row["url"], ROOT / row["path"], row["sha256"])
    omni = read_omni()
    # Smoke deliberately contains English and Chinese, multiple layouts and figures/tables.
    wanted = [("english", "academic_literature"), ("simplified_chinese", "research_report"),
              ("english", "book"), ("simplified_chinese", "newspaper"),
              ("en_ch_mixed", "colorful_textbook"), ("simplified_chinese", "note")]
    smoke_omni = []
    for language, category in wanted:
        candidates = [r for r in omni if omni_stratum(r)[:2] == (language, category)]
        smoke_omni.append(min(candidates, key=lambda r: rank(omni_identity(r))))
    used = {omni_identity(r) for r in smoke_omni}
    full_omni = smoke_omni + balanced_selection([r for r in omni if omni_identity(r) not in used], 74, omni_stratum, omni_identity)
    olm = read_olm()
    smoke_olm = [min([r for r in olm if r["category"] == c], key=lambda r: rank(r["pdf"])) for c in ["multi_column", "table_tests"]]
    used = {r["pdf"] for r in smoke_olm}
    full_olm = smoke_olm + balanced_selection([r for r in olm if r["pdf"] not in used], 18, lambda r: r["category"], lambda r: r["pdf"])
    tasks = [(prepare_omni, r) for r in smoke_omni] + [(prepare_olm, r) for r in smoke_olm]
    with ThreadPoolExecutor(args.workers) as pool:
        smoke = list(pool.map(lambda task: task[0](task[1]), tasks))
    write_manifest_verified(CATALOG / "manifest-smoke.json", make_manifest(smoke, "smoke"))
    print(f"smoke ready: {rel(CATALOG / 'manifest-smoke.json')} ({len(smoke)} pages)", flush=True)
    if args.smoke_only:
        if args.verify_wrappers:
            verify_wrapped_images(smoke, DATA / "wrapper-validation-smoke.json")
        return
    tasks = [(prepare_omni, r) for r in full_omni[6:]] + [(prepare_olm, r) for r in full_olm[2:]]
    with ThreadPoolExecutor(args.workers) as pool:
        full = smoke + list(pool.map(lambda task: task[0](task[1]), tasks))
    write_manifest_verified(CATALOG / "manifest-full.json", make_manifest(full, "full"))
    # Exact official annotations for running the upstream OmniDocBench evaluator.
    write_json(DATA / "omnidocbench-subset.json", [json.loads((ROOT / x["reference"]["path"]).read_text(encoding="utf-8")) for x in full if x["source"] == "OmniDocBench"])
    write_json(CATALOG / "sources.json", source_metadata())
    if args.verify_wrappers:
        verify_wrapped_images(full, DATA / "wrapper-validation.json")
    print(json.dumps(make_manifest(full, "full")["counts"], ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
