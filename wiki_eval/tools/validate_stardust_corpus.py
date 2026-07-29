#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_DATASET = ROOT / "datasets" / "stardust"


def load_json(path: Path, default: Any):
    if not path.exists():
        return default
    return json.loads(path.read_text(encoding="utf-8-sig"))


def slugs_from_entities(entities):
    out = set()
    for ent in entities:
        slug = ent.get("expected_slug")
        if slug:
            out.add(str(slug).strip("/").lower())
    return out


def doc_names(*dirs: Path):
    names = set()
    for d in dirs:
        if d.exists():
            names.update(p.name for p in d.glob("*.md"))
    return names


def add_issue(bucket, kind, message, **ctx):
    bucket.append({"kind": kind, "message": message, **ctx})


def validate(dataset: Path):
    errors = []
    warnings = []
    gold = dataset / "gold"
    docs_v1 = dataset / "docs_v1"
    docs_v2 = dataset / "docs_v2"
    docs_del = dataset / "docs_del"
    delete_docs = doc_names(docs_del)

    seed = load_json(dataset / "stardust-seed-graph.json", {})
    entities = load_json(gold / "entities.json", [])
    relations = load_json(gold / "relations.json", [])
    facts = load_json(gold / "facts.json", [])
    qa = load_json(gold / "qa.json", [])
    search_cases = load_json(gold / "search_cases.json", [])
    update_events = load_json(gold / "update_events.json", [])
    delete_events = load_json(gold / "delete_events.json", [])

    all_docs = doc_names(docs_v1, docs_v2, docs_del)
    v1_docs = doc_names(docs_v1)
    gold_slugs = slugs_from_entities(entities)

    nodes = seed.get("nodes", []) or []
    edges = seed.get("edges", []) or []
    node_ids = {n.get("id") for n in nodes if n.get("id")}

    if not docs_v1.exists():
        add_issue(errors, "missing_dir", "docs_v1 directory is missing", path=str(docs_v1))
    if not docs_v2.exists():
        add_issue(warnings, "missing_dir", "docs_v2 directory is missing", path=str(docs_v2))
    if not docs_del.exists():
        add_issue(warnings, "missing_dir", "docs_del directory is missing; delete corpus is not materialized", path=str(docs_del))
    elif not delete_docs:
        add_issue(warnings, "empty_dir", "docs_del directory exists but has no markdown delete cases yet", path=str(docs_del))
    elif delete_events and len(delete_docs) < len(delete_events):
        add_issue(warnings, "delete_corpus_smaller_than_events", "docs_del has fewer markdown docs than delete events", actual=len(delete_docs), target=len(delete_events))

    targets = seed.get("coverage_targets", {}) or {}
    if targets.get("entity_count") and len(nodes) < int(targets["entity_count"]):
        add_issue(warnings, "coverage_target", "seed node count is below coverage target", actual=len(nodes), target=targets.get("entity_count"))
    if targets.get("relation_count") and len(edges) < int(targets["relation_count"]):
        add_issue(warnings, "coverage_target", "seed edge count is below coverage target", actual=len(edges), target=targets.get("relation_count"))

    for edge in edges:
        for key in ("subject", "object"):
            if edge.get(key) not in node_ids:
                add_issue(errors, "seed_edge_endpoint", "seed edge endpoint is not declared as a node", edge=edge.get("id"), field=key, value=edge.get(key))
        for doc in edge.get("evidence_docs", []) or []:
            if doc not in all_docs:
                add_issue(errors, "seed_edge_evidence", "seed edge evidence doc is missing from docs_v1/docs_v2/docs_del", edge=edge.get("id"), doc=doc)

    for ent in entities:
        if not ent.get("expected_slug"):
            add_issue(errors, "gold_entity_slug", "gold entity is missing expected_slug", entity=ent.get("id") or ent.get("name"))
        if not ent.get("name"):
            add_issue(errors, "gold_entity_name", "gold entity is missing name", entity=ent.get("id"))

    relation_ids = {(r.get("subject"), r.get("predicate"), r.get("object")) for r in relations}
    for rel in relations:
        if not rel.get("terms"):
            add_issue(warnings, "gold_relation_terms", "gold relation has no terms; text coverage may be weak", relation=f"{rel.get('subject')}:{rel.get('predicate')}:{rel.get('object')}")
    if len(relation_ids) != len(relations):
        add_issue(warnings, "gold_relation_duplicate", "gold relations contain duplicate subject/predicate/object triples")

    fact_ids = set()
    for fact in facts:
        fid = fact.get("id")
        if fid in fact_ids:
            add_issue(errors, "gold_fact_duplicate", "duplicate fact id", fact_id=fid)
        fact_ids.add(fid)
        if not fact.get("expected_terms"):
            add_issue(errors, "gold_fact_terms", "fact is missing expected_terms", fact_id=fid)
        if not fact.get("expected_pages"):
            add_issue(warnings, "gold_fact_pages", "fact has no expected_pages", fact_id=fid)

    for case in qa:
        if not case.get("expected_pages"):
            add_issue(warnings, "qa_expected_pages", "QA case has no expected_pages", case_id=case.get("id"))
        for fid in case.get("supporting_facts", []) or []:
            if fid not in fact_ids:
                add_issue(errors, "qa_supporting_fact", "QA supporting fact does not exist", case_id=case.get("id"), fact_id=fid)

    for case in search_cases:
        if not case.get("expected_slugs"):
            add_issue(errors, "search_expected_slugs", "search case has no expected_slugs", case_id=case.get("id"))
        for slug in case.get("expected_slugs", []) or []:
            if str(slug).strip("/").lower() not in gold_slugs:
                add_issue(warnings, "search_slug_not_in_gold_entities", "search expected slug is not present in gold entities", case_id=case.get("id"), slug=slug)

    for event in update_events:
        if not event.get("new_facts"):
            add_issue(errors, "update_new_facts", "update event has no new_facts", event_id=event.get("id") or event.get("event_id"))
        for fact in event.get("new_facts", []) or []:
            if not fact.get("expected_terms"):
                add_issue(errors, "update_expected_terms", "update new fact is missing expected_terms", event_id=event.get("id") or event.get("event_id"), claim=fact.get("claim"))

    for event in delete_events:
        event_id = event.get("event_id") or event.get("id")
        target_doc = event.get("target_doc_id")
        if target_doc not in v1_docs:
            add_issue(errors, "delete_target_doc", "delete target doc is not in docs_v1", event_id=event_id, target_doc_id=target_doc)
        impact = event.get("expected_impact", {}) or {}
        for field in ("must_remove_source_refs", "must_keep_pages", "must_not_change_pages"):
            if not impact.get(field):
                add_issue(errors, "delete_required_field", "delete expected_impact is missing required field", event_id=event_id, field=field)
        if not impact.get("must_remove_in_links"):
            add_issue(warnings, "delete_inlink_expectation", "delete event has no must_remove_in_links expectation", event_id=event_id)
        if not impact.get("equivalent_pages"):
            add_issue(warnings, "delete_equivalent_pages", "delete event has no equivalent_pages mapping; slug strategy changes may cause false failures", event_id=event_id)
        if not impact.get("must_remove_terms"):
            add_issue(warnings, "delete_term_expectation", "delete event has no must_remove_terms content-level expectation", event_id=event_id)

    summary = {
        "dataset": str(dataset),
        "counts": {
            "docs_v1": len(v1_docs),
            "docs_v2": len(doc_names(docs_v2)),
            "docs_del": len(delete_docs),
            "seed_nodes": len(nodes),
            "seed_edges": len(edges),
            "gold_entities": len(entities),
            "gold_relations": len(relations),
            "gold_facts": len(facts),
            "qa_cases": len(qa),
            "search_cases": len(search_cases),
            "update_events": len(update_events),
            "delete_events": len(delete_events),
        },
        "errors": errors,
        "warnings": warnings,
    }
    return summary


def main():
    parser = argparse.ArgumentParser(description="Validate Stardust wiki corpus/gold consistency")
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--strict", action="store_true", help="treat warnings as failures")
    args = parser.parse_args()

    result = validate(args.dataset)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if result["errors"] or (args.strict and result["warnings"]):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
