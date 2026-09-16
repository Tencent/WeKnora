#!/usr/bin/env python3
"""Prepare pinned public corpora, or check committed artifacts without a network.

Uses only Python's standard library. No model calls or database writes occur.
Source bytes are verified before parsing; --download fetches missing cache files.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
from urllib.parse import quote
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]
SEED = 20260908
CMRC_REV = "c0eb1b6ba219847457e6af3180da722bbeb656af"
SQUAD_REV = "eee5fdbf62f8613a7812b03419e6b29617b74fd1"
SQUAD_SITE_REV = "e0c66cfec263165fb3c15cb76a22175e371b71d7"
DOCS_REV = "ef9cbf456d042de4c442674e5b940dd93d15a2d2"


def source(repo, revision, path, digest):
    return {"url": f"https://raw.githubusercontent.com/{repo}/{revision}/{quote(path)}",
            "revision": revision, "path": path, "sha256": digest}


SOURCES = {
    "cmrc2018-dev.json": source("ymcui/cmrc2018", CMRC_REV, "squad-style-data/cmrc2018_dev.json", "e9ff74231f05c230c6fa88b84441ee334d97234cbb610991cd94b82db00c7f1f"),
    "cmrc2018-LICENCE": source("ymcui/cmrc2018", CMRC_REV, "LICENCE", "7abe19ec9bb73b36141b999b861d24ad855e808bafe0f81e84cce28556f6c297"),
    "squad2-dev.json": source("rajpurkar/SQuAD-explorer", SQUAD_REV, "dataset/dev-v2.0.json", "80a5225e94905956a6446d296ca1093975c4d3b3260f1d6c8f68bc2ab77182d8"),
    "squad2-home.html": source("rajpurkar/SQuAD-explorer", SQUAD_SITE_REV, "index.html", "989f7e43f27faa490ed9fc8fb440e6f0aa791c49131346a27b8361deb4a18baf"),
    "weknora-LICENSE": source("Tencent/WeKnora", DOCS_REV, "LICENSE", "5f6c35c603349247019b1a107e0c19ac32bed953c9857383449003283e7ccd20"),
    "weknora-LITE.md": source("Tencent/WeKnora", DOCS_REV, "docs/LITE.md", "d595c37c55e80105dc9a5db2380a254daedf857ee162c884d4a8c0920c3dcdf2"),
    "weknora-logging.md": source("Tencent/WeKnora", DOCS_REV, "docs/日志配置.md", "3c51e1bdffec538501769106f2fe5a551f95e5604ca7e56a7931b0b64e6369b7"),
    "weknora-vector-databases.md": source("Tencent/WeKnora", DOCS_REV, "docs/使用其他向量数据库.md", "f5e8acaf6cd632a5c73857b9939c8a010ec3c76c20fa4299d461e01e6bcd63da"),
    "weknora-knowledge-graph.md": source("Tencent/WeKnora", DOCS_REV, "docs/开启知识图谱功能.md", "b7d171961809e9556d5d92131e777badedf4e46837d7d059e640973a5b08db3f"),
    "weknora-development.md": source("Tencent/WeKnora", DOCS_REV, "docs/开发指南.md", "950648c0a51f4c21f6c6b17bfb975cebae5a70eec8e3936e0d95bb2469667420"),
}
DOC_FILES = ["weknora-LITE.md", "weknora-logging.md", "weknora-vector-databases.md",
             "weknora-knowledge-graph.md", "weknora-development.md"]
CONFIGS = {
    "cmrc2018-dev": {"name": "CMRC 2018 中文阅读理解 · 24 题", "language": "zh-CN",
        "description": "中文机器阅读理解开发集固定抽样，24 道可回答题与 8 段额外候选原文。",
        "source_url": "https://github.com/ymcui/cmrc2018", "upstream_revision": CMRC_REV,
        "answerable": 24, "unanswerable": 0, "source_file": "cmrc2018-dev.json"},
    "squad2-dev": {"name": "SQuAD 2.0 英文阅读理解 · 24 题", "language": "en",
        "description": "斯坦福问答开发集固定抽样，16 道可回答题、8 道针对指定原文的无答案题与 8 段额外候选原文。",
        "source_url": "https://rajpurkar.github.io/SQuAD-explorer/", "upstream_revision": SQUAD_REV,
        "answerable": 16, "unanswerable": 8, "source_file": "squad2-dev.json"},
}
EVAL_LIMITATIONS = [
    "固定开发集子集用于导入、检索和生成链路试验；样本分数不能解释为官方完整基准分数或总体能力估计。",
    "原始数据为给定上下文的阅读理解任务。跨段落候选语料是本项目的检索适配；仅原始题目与上下文之间存在标注，其余组合保持未标注。",
    "额外候选段落属于未标注干扰段落，可能包含答案；不将其转换为可靠负例。检索指标仅描述命中已标注原文的情况。",
    "注册格式只保存一个参考答案，取原始 answers 中第一个答案；annotations.json 保留全部原始答案及字符偏移。平台指标与官方多参考答案评分规则不同。",
    "原文来自上游收录的维基百科快照，可能含时效性、文本噪声及内容偏差；不作为当前事实核验来源。",
    "抽样候选要求全部参考答案具备有效字符偏移并与原文逐字匹配。上游不满足条件的题目数量记录在 sampling.upstream_counts.invalid_questions_excluded；筛选会改变样本分布。",
]
SQUAD_LIMITATIONS = [
    "无答案标注仅针对题目配对的原始上下文；不表示整个候选语料或用户知识库中均无答案。",
    "无答案题使用空参考答案和原始上下文 grade=0。缺少正例时平台检索指标可能为 0，该值不能解释为拒答质量。",
    "可答与无答案按 16:8 分层固定抽样，该比例不代表完整开发集分布。",
]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def cached(name, cache, download=False):
    spec = SOURCES[name]
    path = cache / name
    if not path.exists():
        if not download:
            raise ValueError(f"Missing pinned source {path}; use --download or populate --cache-dir")
        req = Request(spec["url"], headers={"User-Agent": "WeKnora-public-data-preparation/1"})
        with urlopen(req, timeout=60) as response:
            data = response.read(16 * 1024 * 1024 + 1)
        if len(data) > 16 * 1024 * 1024 or digest(data) != spec["sha256"]:
            raise ValueError(f"Downloaded source hash/size mismatch: {name}")
        cache.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
    data = path.read_bytes()
    if digest(data) != spec["sha256"]:
        raise ValueError(f"Pinned source SHA-256 mismatch: {path}")
    return data


def rank(role, identity):
    return digest(f"{SEED}:{role}:{identity}".encode("utf-8"))


def valid_question(qa, context):
    if not qa.get("question", "").strip():
        return False
    answers = qa.get("answers", [])
    impossible = qa.get("is_impossible", False)
    if impossible:
        return not answers
    return bool(answers) and all(isinstance(a.get("answer_start"), int)
        and a["answer_start"] >= 0 and a.get("text", "").strip()
        and context[a["answer_start"]:a["answer_start"] + len(a["text"])] == a["text"] for a in answers)


def select_sample(slug, upstream):
    cfg = CONFIGS[slug]
    paragraphs, candidates = {}, []
    skipped = 0
    for article_index, article in enumerate(upstream["data"]):
        for paragraph_index, para in enumerate(article["paragraphs"]):
            pid = f"{slug}-a{article_index:04d}-p{paragraph_index:04d}"
            paragraphs[pid] = {"pid": pid, "title": article.get("title", ""),
                "article_index": article_index, "paragraph_index": paragraph_index,
                "upstream_paragraph_id": para.get("id"), "context": para["context"]}
            for qa in para["qas"]:
                if valid_question(qa, para["context"]):
                    candidates.append({"pid": pid, "qa": qa})
                else:
                    skipped += 1
    chosen, used_pids = [], set()
    for impossible, count in [(False, cfg["answerable"]), (True, cfg["unanswerable"])]:
        if count == 0:
            continue
        pool = sorted((c for c in candidates if c["qa"].get("is_impossible", False) == impossible),
                      key=lambda c: (rank(f"{slug}-questions", c["qa"]["id"]), c["qa"]["id"]))
        selected = 0
        for item in pool:
            if item["pid"] in used_pids:
                continue
            used_pids.add(item["pid"])
            chosen.append(item)
            selected += 1
            if selected == count:
                break
        if selected != count:
            raise ValueError(f"Insufficient distinct contexts for {slug}: {impossible}")
    extra = sorted((pid for pid in paragraphs if pid not in used_pids),
                   key=lambda pid: (rank(f"{slug}-distractors", pid), pid))[:8]
    selected_pids = used_pids | set(extra)
    rows = [{**paragraphs[pid], "role": "unjudged_distractor" if pid in extra else "question_context"}
            for pid in sorted(selected_pids)]
    chosen.sort(key=lambda c: c["qa"]["id"])
    return {"source_file": cfg["source_file"], "source_sha256": SOURCES[cfg["source_file"]]["sha256"],
            "sample_seed": SEED, "sampling": "sha256_rank_distinct_contexts_v1",
            "upstream_counts": {"articles": len(upstream["data"]), "passages": len(paragraphs),
                                "questions": len(candidates) + skipped, "invalid_questions_excluded": skipped},
            "passages": rows, "questions": chosen}


def registry_from_sample(slug, sample):
    cfg = CONFIGS[slug]
    spec = SOURCES[cfg["source_file"]]
    payload = {"passages": [], "questions": [], "relevance": []}
    annotations = []
    for row in sample["passages"]:
        lang = "zh" if slug == "cmrc2018-dev" else "en"
        payload["passages"].append({"pid": row["pid"], "content": row["context"], "metadata": {
            "source": "public_dataset", "dataset_id": slug, "title": row["title"],
            "source_url": spec["url"], "upstream_revision": cfg["upstream_revision"],
            "upstream_sha256": spec["sha256"], "article_index": row["article_index"],
            "paragraph_index": row["paragraph_index"], "upstream_paragraph_id": row["upstream_paragraph_id"],
            "wikipedia_attribution_url": f"https://{lang}.wikipedia.org/wiki/{quote(row['title'])}",
            "wikipedia_revision": "not_provided_by_upstream",
            "source_file": f"source_docs/{row['pid']}.txt", "corpus_role": row["role"],
            "relevance_scope": "original_question_context_only", "license": "CC-BY-SA-4.0"}})
    for item in sample["questions"]:
        qa, pid = item["qa"], item["pid"]
        impossible = qa.get("is_impossible", False)
        qid = f"{slug}-{qa['id']}"
        payload["questions"].append({"qid": qid, "question": qa["question"],
                                     "answer": "" if impossible else qa["answers"][0]["text"]})
        payload["relevance"].append({"qid": qid, "pid": pid, "grade": 0 if impossible else 1})
        annotations.append({"qid": qid, "source_pid": pid, "upstream_qid": qa["id"],
            "is_impossible": impossible, "answerability_scope": "source_pid_only",
            "answers": qa["answers"], "plausible_answers": qa.get("plausible_answers", []),
            "answer_offset_unit": "unicode_code_points", "reference_answer_policy": "first_upstream_answer"})
    return payload, annotations


def checked_path(directory, relative):
    path = (directory / relative).resolve()
    if not path.is_relative_to(directory.resolve()) or path == directory.resolve():
        raise ValueError(f"Artifact path leaves dataset directory: {relative}")
    return path


def readme(slug, manifest):
    c = manifest["counts"]
    sources = "\n".join(f"- [{s['path']}]({s['url']})，版本 `{s['revision']}`，SHA-256 `{s['sha256']}`。"
                        for s in manifest["upstream_files"])
    boundaries = "\n".join(f"- {line}" for line in manifest["limitations"])
    evidence = ("`annotations.json` 保留评测题目的原始多答案标注；`source-samples.json` 保留抽样题目、原文与上游位置，支持独立核对。"
                if manifest["kind"] == "evaluation" else "本包没有评测题目和答案标注文件。")
    return f"""# {manifest['name']}

{manifest['description']}

本包版本为 `v1`，类型为 `{manifest['kind']}`，语种为 `{manifest['language']}`。
安全散列算法 256 位（Secure Hash Algorithm 256-bit，SHA-256）用于校验源文件与生成文件。

## 内容与用途

| 对象 | 数量 |
| --- | ---: |
| 段落 | {c['passages']} |
| 题目 | {c['questions']} |
| 相关性标注 | {c['relevance']} |
| 可回答题目 | {c['answerable']} |
| 指定原文无答案题目 | {c['unanswerable']} |
| 额外未标注干扰段落 | {c['distractor_passages']} |
| 可上传原文文件 | {c['source_documents']} |

表中数量对应 `registry-input.json` 的实际内容。`passages` 存储原文及出处，`questions` 存储问题与单个参考答案，`relevance` 存储题目与原文的已知相关性。`grade=1` 表示原标注可回答上下文，`grade=0` 表示原标注无答案上下文；没有标注的组合保持缺失。

`source_docs/` 中的文件可通过知识库的文档上传入口使用。`registry-input.json` 供评测数据导入；文档类型的包包含空题目列表，仅用于资料归档和上传。{evidence}

## 来源与许可

本包来源为 [{manifest['source_url']}]({manifest['source_url']})，许可为 `{manifest['license']}`。`LICENSE` 与 `NOTICE` 保存许可和归属说明。文件及抽样说明以 `manifest.json` 为准。

{sources}

## 复现与校验

以下命令在项目根目录执行，Python 3.10 或以上版本即可运行。脚本仅使用标准库。

```powershell
python scripts/prepare-public-evaluation.py --check --dataset {slug}
python scripts/prepare-public-evaluation.py --dataset {slug} --cache-dir artifacts/public-datasets/source-cache
python scripts/prepare-public-evaluation.py --dataset {slug} --cache-dir artifacts/public-datasets/source-cache --download
python scripts/prepare-public-evaluation.py --check --verify-source --dataset {slug} --cache-dir artifacts/public-datasets/source-cache
```

`--check` 使用仓库内的精简证据与文件摘要，离线核验结构、引用关系、答案偏移和生成内容。`--verify-source` 进一步读取本地完整源缓存，重跑固定抽样并核对来源。生成命令默认要求已有缓存；`--download` 仅下载缺失的固定版本源文件，任何摘要不匹配均报错。全部命令均无模型调用和业务数据库写入。

## 适用边界

{boundaries}
"""


def build(slug, cache, download=False):
    if slug in CONFIGS:
        cfg = CONFIGS[slug]
        sample = select_sample(slug, json.loads(cached(cfg["source_file"], cache, download)))
        payload, annotations = registry_from_sample(slug, sample)
        files = {"source-samples.json": encode(sample), "annotations.json": encode(annotations),
                 "registry-input.json": encode(payload)}
        for p in sample["passages"]:
            files[f"source_docs/{p['pid']}.txt"] = p["context"].encode("utf-8")
        files["LICENSE"] = cached("cmrc2018-LICENCE", cache, download)
        source_names = [cfg["source_file"], "cmrc2018-LICENCE"]
        attribution = ("Yiming Cui, Ting Liu, Wanxiang Che, Li Xiao, Zhipeng Chen, Wentao Ma, Shijin Wang, Guoping Hu; CMRC 2018; Wikipedia contributors."
                       if slug == "cmrc2018-dev" else "Pranav Rajpurkar, Robin Jia, Percy Liang and SQuAD contributors; Wikipedia contributors.")
        notice = f"{cfg['name']}\n\nAttribution: {attribution}\nSource: {cfg['source_url']}\nLicense: Creative Commons Attribution-ShareAlike 4.0 International (CC-BY-SA-4.0)\nhttps://creativecommons.org/licenses/by-sa/4.0/\n\nChanges: deterministic development-set selection; stable prefixed IDs; registry schema conversion; one upstream reference answer selected. Passage text, question text, and all original answers in the evidence files are preserved. This adaptation is distributed under CC-BY-SA-4.0. No endorsement is implied.\n\nWikipedia article titles and attribution URLs accompany each passage. Upstream does not supply exact Wikipedia revision IDs. The pinned dataset snapshot is the authoritative text for this package; linked live articles can differ.\n\nCMRC means Chinese Machine Reading Comprehension; SQuAD means Stanford Question Answering Dataset.\n"
        if slug == "squad2-dev":
            html = cached("squad2-home.html", cache, download).decode("utf-8")
            if not re.search(r"distributed under the.*?CC BY-SA 4\.0", html, re.S):
                raise ValueError("SQuAD official license statement missing")
            source_names.append("squad2-home.html")
            notice += f"\nLicense evidence: official SQuAD website at revision {SQUAD_SITE_REV}; source hash recorded in manifest. Dataset license is CC-BY-SA-4.0. The repository's software license is not used for the dataset. The included CC license text is the identical license published with CMRC 2018.\n"
        files["NOTICE"] = notice.encode("utf-8")
        counts = {key: len(payload[key]) for key in ("passages", "questions", "relevance")}
        counts.update(source_documents=len(sample["passages"]), answerable=cfg["answerable"],
                      unanswerable=cfg["unanswerable"], distractor_passages=8)
        manifest = {"id": slug, "name": cfg["name"], "description": cfg["description"],
            "kind": "evaluation", "language": cfg["language"], "source_url": cfg["source_url"],
            "license": "CC-BY-SA-4.0", "upstream_revision": cfg["upstream_revision"],
            "sample_seed": SEED, "counts": counts,
            "sampling": {"algorithm": sample["sampling"], "split": "development", "distinct_question_contexts": 24,
                         "answerable": cfg["answerable"], "unanswerable": cfg["unanswerable"], "extra_unjudged_contexts": 8,
                         "upstream_counts": sample["upstream_counts"]},
            "annotation_file": "annotations.json", "upstream_files": [SOURCES[n] for n in source_names],
            "limitations": EVAL_LIMITATIONS + (SQUAD_LIMITATIONS if slug == "squad2-dev" else [])}
    else:
        payload = {"passages": [], "questions": [], "relevance": []}
        files = {"LICENSE": cached("weknora-LICENSE", cache, download)}
        for index, name in enumerate(DOC_FILES, 1):
            data, spec = cached(name, cache, download), SOURCES[name]
            relative = f"source_docs/{index:02d}-{name.removeprefix('weknora-')}"
            files[relative] = data
            payload["passages"].append({"pid": f"weknora-docs-{index:02d}", "content": data.decode("utf-8"),
                "metadata": {"source": "public_documentation", "source_file": relative, "source_url": spec["url"],
                             "source_sha256": spec["sha256"], "upstream_revision": DOCS_REV, "license": "MIT"}})
        files["registry-input.json"] = encode(payload)
        files["NOTICE"] = ("WeKnora public documentation snapshot\n\nCopyright (C) 2025 Tencent. All rights reserved.\nSource: https://github.com/Tencent/WeKnora\nLicense: MIT; upstream LICENSE is preserved including third-party notices.\nChanges: five documentation files selected and renamed for local upload; source file bytes are unchanged. Registry metadata and a manifest are added.\n\nThis package is an upstream product documentation sample for knowledge-base upload and retrieval exploration. It has no labeled evaluation questions. It is related to this project's upstream and provides no independent quality benchmark. Source URLs identify the upstream revision and do not describe or guarantee the current local deployment.\n").encode("utf-8")
        manifest = {"id": slug, "name": "WeKnora 中文公开技术资料 · 5 篇", "kind": "documentation",
            "description": "固定版本的部署、开发、日志、向量数据库和知识图谱中文技术文档，可用于知识库上传体验。",
            "language": "zh-CN", "source_url": "https://github.com/Tencent/WeKnora", "license": "MIT",
            "upstream_revision": DOCS_REV, "sample_seed": None,
            "counts": {"passages": 5, "questions": 0, "relevance": 0, "source_documents": 5,
                       "answerable": 0, "unanswerable": 0, "distractor_passages": 0},
            "sampling": {"algorithm": "explicit_document_allowlist_v1", "source_files": DOC_FILES},
            "annotation_file": None, "upstream_files": [SOURCES[n] for n in ["weknora-LICENSE", *DOC_FILES]],
            "limitations": ["本包来自本项目的上游产品文档，用于资料上传和检索体验；没有独立人工问题真值，也不构成独立质量基准。",
                "内容描述固定提交的上游产品；本地部署配置和功能可与文档存在差异。",
                "文件中的外部链接、图片路径和命令作为资料正文保留，上传资料本身不执行其中命令。",
                "本包含五篇完整 Markdown 原文；平台文档解析后的分块数量取决于知识库配置。"]}
    files["README.md"] = readme(slug, manifest).encode("utf-8")
    manifest["files_sha256"] = {name: digest(data) for name, data in sorted(files.items())}
    files["manifest.json"] = encode(manifest)
    return files


def check(slug, directory):
    manifest = json.loads((directory / "manifest.json").read_bytes())
    if manifest["id"] != slug or manifest["sample_seed"] != (SEED if slug in CONFIGS else None):
        raise ValueError(f"Invalid package identity/seed: {slug}")
    if (manifest["kind"] != ("evaluation" if slug in CONFIGS else "documentation")
            or manifest["license"] != ("CC-BY-SA-4.0" if slug in CONFIGS else "MIT")
            or manifest["upstream_revision"] != (CONFIGS[slug]["upstream_revision"] if slug in CONFIGS else DOCS_REV)):
        raise ValueError(f"Package kind/license/revision drift: {slug}")
    expected_specs = ([SOURCES[CONFIGS[slug]["source_file"]], SOURCES["cmrc2018-LICENCE"]]
                      + ([SOURCES["squad2-home.html"]] if slug == "squad2-dev" else []) if slug in CONFIGS
                      else [SOURCES[n] for n in ["weknora-LICENSE", *DOC_FILES]])
    if manifest["upstream_files"] != expected_specs:
        raise ValueError(f"Pinned source manifest drift: {slug}")
    actual_files = {p.relative_to(directory).as_posix() for p in directory.rglob("*") if p.is_file()}
    if actual_files != set(manifest["files_sha256"]) | {"manifest.json"}:
        raise ValueError(f"Missing/unlisted artifact files: {slug}")
    for name, expected in manifest["files_sha256"].items():
        if digest(checked_path(directory, name).read_bytes()) != expected:
            raise ValueError(f"Artifact SHA-256 mismatch: {slug}/{name}")
    license_name = "cmrc2018-LICENCE" if slug in CONFIGS else "weknora-LICENSE"
    if digest((directory / "LICENSE").read_bytes()) != SOURCES[license_name]["sha256"]:
        raise ValueError(f"Pinned license text drift: {slug}")
    if (directory / "README.md").read_bytes() != readme(slug, manifest).encode("utf-8"):
        raise ValueError(f"README manifest drift: {slug}")
    payload = json.loads((directory / "registry-input.json").read_bytes())
    if set(payload) != {"passages", "questions", "relevance"}:
        raise ValueError(f"Invalid registry keys: {slug}")
    for key in payload:
        if len(payload[key]) != manifest["counts"][key]:
            raise ValueError(f"Invalid count: {slug}/{key}")
    expected_counts = ({"passages": 32, "questions": 24, "relevance": 24, "source_documents": 32,
                        "answerable": CONFIGS[slug]["answerable"], "unanswerable": CONFIGS[slug]["unanswerable"],
                        "distractor_passages": 8} if slug in CONFIGS else
                       {"passages": 5, "questions": 0, "relevance": 0, "source_documents": 5,
                        "answerable": 0, "unanswerable": 0, "distractor_passages": 0})
    if manifest["counts"] != expected_counts:
        raise ValueError(f"Package shape drift: {slug}")
    pids = {p["pid"] for p in payload["passages"]}
    qids = {q["qid"] for q in payload["questions"]}
    if len(pids) != len(payload["passages"]) or len(qids) != len(payload["questions"]):
        raise ValueError(f"Duplicate ID: {slug}")
    edges = {(e["qid"], e["pid"]) for e in payload["relevance"]}
    if len(edges) != len(payload["relevance"]):
        raise ValueError(f"Duplicate relevance: {slug}")
    for e in payload["relevance"]:
        if e["qid"] not in qids or e["pid"] not in pids or e["grade"] not in (0, 1):
            raise ValueError(f"Invalid relevance: {slug}")
    for p in payload["passages"]:
        text = checked_path(directory, p["metadata"]["source_file"]).read_bytes().decode("utf-8")
        if not p["content"].strip() or p["content"] != text or len(text.encode("utf-8")) > 1 << 20:
            raise ValueError(f"Invalid passage text/size: {p['pid']}")
    if slug in CONFIGS:
        sample = json.loads((directory / "source-samples.json").read_bytes())
        if sample["source_sha256"] != SOURCES[CONFIGS[slug]["source_file"]]["sha256"]:
            raise ValueError(f"Sample source identity mismatch: {slug}")
        by_pid = {p["pid"]: p for p in sample["passages"]}
        for item in sample["questions"]:
            if not valid_question(item["qa"], by_pid[item["pid"]]["context"]):
                raise ValueError(f"Invalid original answer span: {item['qa']['id']}")
        expected, annotations = registry_from_sample(slug, sample)
        if encode(expected) != (directory / "registry-input.json").read_bytes():
            raise ValueError(f"Registry transformation drift: {slug}")
        if encode(annotations) != (directory / "annotations.json").read_bytes():
            raise ValueError(f"Annotation transformation drift: {slug}")
        unanswerable = sum(a["is_impossible"] for a in annotations)
        if (len(annotations) != 24 or unanswerable != CONFIGS[slug]["unanswerable"]
                or len(by_pid) != 32 or len({i["pid"] for i in sample["questions"]}) != 24):
            raise ValueError(f"Sample shape drift: {slug}")
    else:
        if payload["questions"] or payload["relevance"] or len(payload["passages"]) != len(DOC_FILES):
            raise ValueError("Documentation package cannot provide evaluation labels")
        for p, name in zip(payload["passages"], DOC_FILES):
            if digest(p["content"].encode("utf-8")) != SOURCES[name]["sha256"]:
                raise ValueError(f"Documentation source drift: {name}")
    return {"id": slug, "checked": True, "counts": manifest["counts"],
            "registry_sha256": digest((directory / "registry-input.json").read_bytes()), "model_calls": 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dataset", choices=[*CONFIGS, "weknora-docs"], action="append")
    workspace = ROOT.parents[1] if ROOT.parent.name == "integrations" else ROOT
    parser.add_argument("--cache-dir", type=Path, default=workspace / ".cache/public-datasets")
    parser.add_argument("--output-root", type=Path, default=ROOT / "dataset/public")
    parser.add_argument("--check", action="store_true", help="Offline artifact and annotation check; never downloads")
    parser.add_argument("--verify-source", action="store_true", help="With --check, regenerate from pinned local source cache")
    parser.add_argument("--download", action="store_true", help="Download missing pinned sources before generation")
    args = parser.parse_args()
    if args.check and args.download:
        parser.error("--check is offline and cannot be combined with --download")
    if args.verify_source and not args.check:
        parser.error("--verify-source requires --check")
    try:
        for slug in args.dataset or [*CONFIGS, "weknora-docs"]:
            directory = args.output_root / slug / "v1"
            if not args.check or args.verify_source:
                files = build(slug, args.cache_dir, args.download)
                for name, data in files.items():
                    target = checked_path(directory, name)
                    if args.check:
                        if not target.is_file() or target.read_bytes() != data:
                            raise ValueError(f"Source regeneration mismatch: {target}")
                    else:
                        target.parent.mkdir(parents=True, exist_ok=True)
                        target.write_bytes(data)
            print(json.dumps(check(slug, directory), ensure_ascii=False))
    except (OSError, ValueError, KeyError) as error:
        parser.exit(1, f"Public dataset verification failed: {error}\n")


if __name__ == "__main__":
    main()
