#!/usr/bin/env python3
"""Prepare an immutable registry input from the checked-in AI synthetic corpus.

No network or model calls are made. Source passage IDs are dataset identities;
the evaluation service resolves them to the chunks created for each experiment.
"""
import argparse
import hashlib
import json
from pathlib import Path


def prepare(corpus):
    def read(name):
        return json.loads((corpus / name).read_text(encoding="utf-8-sig"))

    manifest, passages, questions = read("manifest.json"), read("passages.json"), read("questions.json")
    if manifest["source"] != "ai_synthetic":
        raise ValueError("Expected a visibly labelled AI synthetic corpus")
    documents, document_text = {}, {}
    for document in manifest["documents"]:
        path = (corpus / document["file"]).resolve()
        if not path.is_relative_to(corpus.resolve()):
            raise ValueError("Source document leaves the corpus directory")
        data = path.read_bytes().replace(b"\r\n", b"\n")
        if hashlib.sha256(data).hexdigest() != document["sha256"]:
            raise ValueError(f"Source document changed: {document['document_id']}")
        if document["document_id"] in documents:
            raise ValueError(f"Duplicate source document: {document['document_id']}")
        documents[document["document_id"]] = document
        document_text[document["document_id"]] = data.decode("utf-8")
    if len(passages) != manifest["passage_count"] or len(questions) != manifest["question_count"]:
        raise ValueError("Corpus counts differ from the manifest")
    result = {"passages": [], "questions": [], "relevance": []}
    for pid, passage in sorted(passages.items()):
        document = documents[passage["document_id"]]
        if pid not in document["passage_ids"] or not passage["content"].strip():
            raise ValueError(f"Invalid source passage {pid}")
        if passage["content"] not in document_text[passage["document_id"]]:
            raise ValueError(f"Passage content differs from its source document: {pid}")
        result["passages"].append({"pid": pid, "content": passage["content"], "metadata": {
            "source": "ai_synthetic", "document_id": passage["document_id"], "heading": passage["heading"],
            "source_file": document["file"], "source_sha256": document["sha256"],
            "human_review_status": "pending",
        }})
    seen = set()
    for question in questions:
        qid, evidence = question["question_id"], question["evidence_passage_ids"]
        if qid in seen or not question["question"].strip() or question["human_review_status"] != "pending":
            raise ValueError(f"Invalid question identity or review status: {qid}")
        seen.add(qid)
        if ((question["answerable"] and not evidence)
                or len(evidence) != len(set(evidence))
                or any(pid not in passages for pid in evidence)):
            raise ValueError(f"Invalid evidence mapping: {qid}")
        result["questions"].append({"qid": qid, "question": question["question"], "answer": question["reference_answer"]})
        if question["answerable"]:
            result["relevance"].extend({"qid": qid, "pid": pid, "grade": 1} for pid in evidence)
        else:
            result["relevance"].extend({"qid": qid, "pid": pid, "grade": 0} for pid in sorted(passages))
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    root = Path(__file__).resolve().parents[1]
    parser.add_argument("--corpus", type=Path, default=root / "dataset/synthetic-campus/v1")
    parser.add_argument("--output", type=Path, help="Output file; --check defaults to the corpus registry-input.json")
    parser.add_argument("--check", action="store_true", help="Reject fixture drift without writing any files")
    args = parser.parse_args()
    if args.output is None:
        args.output = (args.corpus / "registry-input.json" if args.check
                       else root / "artifacts/synthetic-evaluation/version-input.json")
    content = prepare(args.corpus)
    payload = (json.dumps(content, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    if args.check:
        if not args.output.is_file() or args.output.read_bytes().replace(b"\r\n", b"\n") != payload:
            parser.exit(1, f"Registry input drift: {args.output}; regenerate with --output {args.output}\n")
    else:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_bytes(payload)
    print(json.dumps({"output": str(args.output), "sha256": hashlib.sha256(payload).hexdigest(),
                      "counts": {key: len(value) for key, value in content.items()},
                      "checked": args.check, "source": "ai_synthetic",
                      "human_review_status": "pending", "model_calls": 0}, ensure_ascii=False))
