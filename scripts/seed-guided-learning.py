#!/usr/bin/env python3
"""Create or recover disposable guided-learning fixtures through loopback APIs.

Requires Python 3.10+. Credentials and API/model payloads are never printed.
All state and sanitized evidence stay under this checkout's ignored .runtime.
"""

import argparse
import os
from pathlib import Path
import secrets
import sys
import time
import uuid

from guided_learning_fixtures import (
    ACTORS, Client, DEFAULT_API, DEFAULT_FRONTEND, SafeFailure, fixture_lock,
    identifier, model_config, origin, prepare, private_text, require,
    runtime_root, save_json, strict_json, validate_state,
)


FIXTURES = (
    {"slug": "concept/topic4-retrieval", "title": "Retrieval and Evidence",
     "text": "Retrieval selects source passages before generating an answer. A source chunk is a bounded passage from a document. A grounded answer cites a source chunk whose text supports the answer. Similar wording alone does not establish correctness. A citation must refer to evidence that actually supports the claim.",
     "links": ["concept/topic4-feedback"]},
    {"slug": "concept/topic4-feedback", "title": "Assessment and Feedback",
     "text": "Viewing a page indicates familiarity, not demonstrated mastery. An assessment tests knowledge using a question with a correct option and a supporting evidence quote. Feedback explains why the chosen option is correct or incorrect. Repeated correct answers provide stronger evidence of mastery than a single page view. Review helps retain knowledge after time passes.",
     "links": ["concept/topic4-retrieval", "concept/topic4-review"]},
    {"slug": "concept/topic4-review", "title": "Review and Prerequisites",
     "text": "A prerequisite is a concept needed to understand another concept. A learning graph connects related concepts and prerequisites. Review recommendations can prioritize overdue concepts and concepts adjacent to recently studied nodes. Familiarity and mastery are separate signals. Assessment evidence should remain tied to the source revision that supported it.",
     "links": ["concept/topic4-feedback"]},
)
SLUGS = {fixture["slug"] for fixture in FIXTURES}


def read_state(root, api=None, frontend=None, *, create=False):
    path = root / "seed-accounts.json"
    if path.exists() or path.is_symlink():
        state = strict_json(private_text(path))
    else:
        require(create, "fixtures_missing_run_seed_first")
        state = {"fixture_version": 1, "api_base": origin(api or DEFAULT_API, api=True),
                 "frontend_base": origin(frontend or DEFAULT_FRONTEND),
                 "run_id": secrets.token_hex(6), "users": {}}
        for label in ACTORS:
            state["users"][label] = {
                "username": f"gl_{label}_{state['run_id']}",
                "email": f"gl-{label}-{state['run_id']}@example.invalid",
                "password": "Aa9!" + secrets.token_urlsafe(18),
                "knowledge_base_id": str(uuid.uuid4()), "documents": {}, "pages": {},
            }
    require(isinstance(state, dict), "invalid_seed_state")
    validate_state(state, origin(api if api is not None else state.get("api_base"), api=True))
    saved_frontend = origin(state.get("frontend_base", DEFAULT_FRONTEND))
    require(frontend is None or origin(frontend) == saved_frontend, "seed_frontend_mismatch")
    seen_documents, seen_pages = set(), set()
    for user in state["users"].values():
        require(isinstance(user.get("documents"), dict) and set(user["documents"]) <= SLUGS
                and isinstance(user.get("pages"), dict)
                and set(user["pages"]) <= set(user["documents"]), "seed_fixture_scope")
        for doc_id in user["documents"].values():
            identifier(doc_id)
            require(doc_id not in seen_documents, "distinct_seed_documents")
            seen_documents.add(doc_id)
        for page in user["pages"].values():
            require(isinstance(page, dict), "invalid_seed_page")
            page_id = identifier(page.get("page_id"))
            require(page_id not in seen_pages, "distinct_seed_pages")
            seen_pages.add(page_id)
            chunks = page.get("chunk_ids")
            require(isinstance(chunks, list) and chunks, "invalid_seed_chunks")
            for chunk_id in chunks:
                identifier(chunk_id)
            require(len(chunks) == len(set(chunks)), "invalid_seed_chunks")
    if not path.exists():
        # Commit identities before the first remote mutation, including registration.
        save_json(path, state)
    return state


def login(root, public, state, label, *, register=False):
    user = state["users"][label]
    credentials = {key: user[key] for key in ("email", "password")}
    can_register = register and not user.get("user_id")
    response = public.call("POST", "/auth/login", credentials,
                           expected=(200, 401) if can_register else (200,))
    if response["status"] == 401:
        public.call("POST", "/auth/register", {**credentials, "username": user["username"]},
                    expected=(201,))
        response = public.call("POST", "/auth/login", credentials)
    result = response["data"]
    actual = result.get("user") or {}
    tenant = (result.get("active_tenant") or {}).get("id", actual.get("tenant_id"))
    require(actual.get("email") == user["email"] and actual.get("username") == user["username"]
            and type(tenant) is int and tenant > 0 and tenant == actual.get("tenant_id")
            and isinstance(result.get("token"), str) and result["token"],
            "authenticated_seed_identity")
    user_id = identifier(actual.get("id"))
    require(not user.get("user_id") or user["user_id"] == user_id, "authenticated_seed_identity")
    require(not user.get("tenant_id") or user["tenant_id"] == tenant, "authenticated_seed_identity")
    for other_label, other in state["users"].items():
        if other_label != label:
            require(other.get("tenant_id") != tenant, "distinct_seed_tenants")
            require(other.get("user_id") != user_id, "distinct_seed_users")
    user.update(user_id=user_id, tenant_id=tenant)
    # Tokens remain in memory. Legacy runtime state may have stored one.
    for saved in state["users"].values():
        saved.pop("token", None)
        saved.pop("refresh_token", None)
    save_json(root / "seed-accounts.json", state)
    return Client(public.base, result["token"])


def register_model(root, client, state, user, model):
    display = "Guided Learning " + state["run_id"]
    models = client.call("GET", "/models")["data"]
    require(isinstance(models, list), "model_list_contract")
    matches = [item for item in models if (
        item.get("id") == user["model_id"] if user.get("model_id")
        else item.get("display_name") == display)]
    require(len(matches) <= 1, "ambiguous_fixture_model")
    require(matches or not user.get("model_id"), "saved_fixture_model_missing")
    if matches:
        registered = matches[0]
    else:
        registered = client.call("POST", "/models", {
            "name": model["name"], "display_name": display, "type": "KnowledgeQA", "source": "remote",
            "description": "Disposable guided-learning model supplied privately by the operator",
            "parameters": {"base_url": model["base_url"], "api_key": model["api_key"],
                           "interface_type": "openai", "provider": "generic", "max_concurrency": 2},
        }, expected=(201,))["data"]
    require(registered.get("tenant_id") == user["tenant_id"]
            and registered.get("name") == model["name"] and registered.get("source") == "remote"
            and registered.get("type") == "KnowledgeQA"
            and (registered.get("parameters") or {}).get("base_url") == model["base_url"],
            "registered_model_mismatch")
    user["model_id"] = identifier(registered.get("id"))
    save_json(root / "seed-accounts.json", state)


def ensure_base(client, label, user):
    kb_id, model_id = user["knowledge_base_id"], user["model_id"]
    bases = client.call("GET", "/knowledge-bases")["data"]
    require(isinstance(bases, list), "knowledge_base_list_contract")
    matches = [kb for kb in bases if kb.get("id") == kb_id]
    require(len(matches) <= 1, "ambiguous_fixture_knowledge_base")
    kb = matches[0] if matches else client.call("POST", "/knowledge-bases", {
        "id": kb_id, "name": f"Topic4 Synthetic Learning {label}", "type": "document",
        "description": "Disposable synthetic guided-learning fixtures; no production data",
        "summary_model_id": model_id, "storage_provider_config": {"provider": "local"},
        "indexing_strategy": {"vector_enabled": False, "keyword_enabled": False,
                              "wiki_enabled": True, "graph_enabled": False},
        "wiki_config": {"synthesis_model_id": model_id, "max_pages_per_ingest": 4,
                        "ingest_map_parallel": 1, "ingest_reduce_parallel": 1, "ingest_max_inflight": 1},
        "chunking_config": {"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n"]},
    }, expected=(201,))["data"]
    require(kb.get("id") == kb_id and kb.get("tenant_id") == user["tenant_id"]
            and kb.get("type") == "document" and kb.get("summary_model_id") == model_id
            and (kb.get("indexing_strategy") or {}).get("wiki_enabled") is True
            and (kb.get("wiki_config") or {}).get("synthesis_model_id") == model_id,
            "fixture_knowledge_base_binding")


def source_content(fixture):
    return "# " + fixture["title"] + "\n\n" + fixture["text"]


def check_document(doc, user, fixture):
    identifier(doc.get("id"))
    require(doc.get("knowledge_base_id") == user["knowledge_base_id"]
            and doc.get("tenant_id") == user["tenant_id"]
            and doc.get("title") == fixture["title"] and doc.get("type") == "manual",
            "source_document_scope")
    metadata = doc.get("metadata") or {}
    require(metadata.get("content") == source_content(fixture) and metadata.get("status") == "publish",
            "fixture_source_edited")


def ensure_documents(root, client, state, user):
    path = f"/knowledge-bases/{user['knowledge_base_id']}/knowledge"
    documents = []
    # Recover a POST whose response/checkpoint was lost, including paginated lists.
    for page in range(1, 101):
        rows = client.call("GET", f"{path}?page={page}&page_size=100")["data"]
        require(isinstance(rows, list), "knowledge_list_contract")
        documents.extend(rows)
        if len(rows) < 100:
            break
    else:
        raise SafeFailure("fixture_knowledge_listing_limit")
    for fixture in FIXTURES:
        slug = fixture["slug"]
        saved_id = user["documents"].get(slug)
        matches = [doc for doc in documents if doc.get("title") == fixture["title"]]
        require(len(matches) <= 1, "ambiguous_fixture_document")
        if saved_id:
            doc = client.call("GET", "/knowledge/" + saved_id)["data"]
            require(doc.get("id") == saved_id, "source_document_scope")
        elif matches:
            doc = client.call("GET", "/knowledge/" + identifier(matches[0].get("id")))["data"]
        else:
            doc = client.call("POST", path + "/manual", {
                "title": fixture["title"], "content": source_content(fixture),
                "status": "publish", "channel": "api",
            }, expected=(200,))["data"]
        check_document(doc, user, fixture)
        user["documents"][slug] = doc["id"]
        save_json(root / "seed-accounts.json", state)


def wait_chunks(client, knowledge_id, timeout):
    identifier(knowledge_id)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        doc = client.call("GET", "/knowledge/" + knowledge_id)["data"]
        require(doc.get("parse_status") != "failed", "source_ingestion_failed_use_reparse")
        if doc.get("parse_status") == "completed":
            require(doc.get("enable_status", "enabled") == "enabled", "source_document_disabled")
            chunks = client.call("GET", f"/chunks/{knowledge_id}?page=1&page_size=100")["data"]
            require(isinstance(chunks, list), "chunk_list_contract")
            ready = [chunk for chunk in chunks if isinstance(chunk.get("content"), str)
                     and chunk["content"].strip() and chunk.get("index_status") == "ready"
                     and chunk.get("is_enabled") is True]
            require(ready, "completed_source_has_no_enabled_chunks")
            require(all(chunk.get("knowledge_id", knowledge_id) == knowledge_id for chunk in ready),
                    "source_chunk_scope")
            ids = [identifier(chunk.get("id")) for chunk in ready]
            require(len(ids) == len(set(ids)), "duplicate_source_chunks")
            return ids
        time.sleep(2)
    raise SafeFailure("source_completion_timeout_check_backend_then_rerun_seed")


def ensure_page(client, user, fixture, chunk_ids, refresh):
    path = f"/knowledgebase/{user['knowledge_base_id']}/wiki/pages"
    slug = fixture["slug"]
    desired = {
        "slug": slug, "title": fixture["title"], "page_type": "concept", "status": "published",
        "summary": fixture["text"].split(". ", 1)[0] + ".",
        "content": fixture["text"] + "\n\nRelated: " + ", ".join("[[" + link + "]]" for link in fixture["links"]),
        "source_refs": [user["documents"][slug] + "|" + fixture["title"]], "chunk_refs": chunk_ids,
    }
    response = client.call("GET", path + "/" + slug, expected=(200, 404))
    page = response["data"] if response["status"] == 200 else None
    if page:
        require(page.get("knowledge_base_id") == user["knowledge_base_id"]
                and page.get("tenant_id") == user["tenant_id"] and page.get("slug") == slug,
                "fixture_page_scope")
        identifier(page.get("id"))
        if (sorted(page.get("chunk_refs") or []) != sorted(chunk_ids)
                or page.get("source_refs") != desired["source_refs"]):
            require(refresh, "fixture_provenance_changed_use_refresh_pages")
            # The public update DTO cannot change provenance; deletion drops revisions.
            client.call("DELETE", path + "/" + slug, expected=(204,))
            page = None
    if page is None:
        page = client.call("POST", path, desired, expected=(201,))["data"]
    else:
        edits = {key: desired[key] for key in ("title", "page_type", "status", "summary", "content")}
        if any(page.get(key) != value for key, value in edits.items()):
            require(refresh, "fixture_page_edited_use_refresh_pages")
            require(type(page.get("version")) is int and page["version"] > 0, "fixture_page_version")
            page = client.call("PUT", path + "/" + slug, {**edits, "version": page["version"]})["data"]
    require(page.get("knowledge_base_id") == user["knowledge_base_id"]
            and page.get("tenant_id") == user["tenant_id"]
            and all(page.get(key) == value for key, value in desired.items() if key != "chunk_refs")
            and sorted(page.get("chunk_refs") or []) == sorted(chunk_ids), "fixture_page_not_current")
    return {"page_id": identifier(page.get("id")), "chunk_ids": chunk_ids}


def seed(args, root, state, public):
    model = model_config(args.model_env)
    description = {key: model[key] for key in ("name", "base_url")}
    require(not state.get("model") or state["model"] == description, "seed_model_config_changed")
    state["model"] = description
    save_json(root / "seed-accounts.json", state)
    clients = {label: login(root, public, state, label, register=True) for label in ACTORS}
    for label, client in clients.items():
        user = state["users"][label]
        register_model(root, client, state, user, model)
        ensure_base(client, label, user)
        ensure_documents(root, client, state, user)
        for fixture in FIXTURES:
            slug = fixture["slug"]
            chunk_ids = wait_chunks(client, user["documents"][slug], args.timeout)
            user["pages"][slug] = ensure_page(client, user, fixture, chunk_ids, args.refresh_pages)
            save_json(root / "seed-accounts.json", state)
        path = f"/knowledgebase/{user['knowledge_base_id']}/wiki"
        client.call("POST", path + "/rebuild-links")
        graph = client.call("GET", path + "/graph")["data"]
        require(SLUGS <= {node.get("slug") for node in graph.get("nodes") or []},
                "fixture_graph_missing_pages")
        # Graph titles and extra pages may be model-generated; never copy raw DTOs.
        edges = {(edge.get("source"), edge.get("target")) for edge in graph.get("edges") or []}
        expected_edges = {(fixture["slug"], link) for fixture in FIXTURES for link in fixture["links"]}
        require(expected_edges <= edges, "fixture_graph_missing_links")
        save_json(root / f"evidence/seed-{label}-graph.json", {
            "nodes": [{"slug": fixture["slug"]} for fixture in FIXTURES],
            "edges": [{"source": source, "target": target} for source, target in sorted(expected_edges)],
        })
    evidence = {
        "fixture_version": 1, "api_base": state["api_base"],
        "frontend_base": state.get("frontend_base", DEFAULT_FRONTEND),
        "status": "seeded", "users": len(ACTORS), "tenants": len(clients),
        "source_documents": sum(len(user["documents"]) for user in state["users"].values()),
        "curated_pages": sum(len(user["pages"]) for user in state["users"].values()),
        "learning_opt_in_changed": False,
        "fixtures": {label: {key: user[key] for key in
                            ("user_id", "tenant_id", "knowledge_base_id", "model_id", "documents", "pages")}
                     for label, user in state["users"].items()},
    }
    save_json(root / "evidence/seed.json", evidence)
    print("Seed complete: two disposable tenants, six completed sources and six cited Wiki pages.")
    print("Learning opt-in unchanged; private state and sanitized evidence saved in the selected runtime.")


def opt_in(args, root, state, clients):
    for client in clients.values():
        result = client.call("PUT", "/learning/settings", {"enabled": True})["data"]
        require(result.get("enabled") is True, "learning_opt_in_not_confirmed")
    save_json(root / "evidence/learning-opt-in.json", {"users": list(clients), "enabled": True})
    print("Learning enabled only for the explicitly selected fixture users.")


def configure_agent(args, root, state, clients):
    results = []
    for label, client in clients.items():
        user = state["users"][label]
        for path, key in (("/models", "model_id"), ("/knowledge-bases", "knowledge_base_id")):
            rows = client.call("GET", path)["data"]
            require(any(row.get("id") == user.get(key) and row.get("tenant_id") == user["tenant_id"]
                        for row in rows), "agent_fixture_binding_scope")
        path = "/agents/builtin-guided-learning"
        agent = client.call("GET", path)["data"]
        require(agent.get("is_builtin") is True and agent.get("tenant_id") == user["tenant_id"]
                and isinstance(agent.get("config"), dict), "builtin_agent_tenant_scope")
        before = agent["config"]
        desired = {**before, "model_id": user["model_id"], "kb_selection_mode": "selected",
                   "knowledge_bases": [user["knowledge_base_id"]]}
        changed = desired != before
        try:
            if changed:
                client.call("PUT", path, {"config": desired})
            actual = client.call("GET", path)["data"]
            require(actual.get("is_builtin") is True and actual.get("tenant_id") == user["tenant_id"]
                    and all(actual["config"].get(key) == desired[key] for key in
                            ("model_id", "kb_selection_mode", "knowledge_bases")),
                    "agent_fixture_binding_not_confirmed")
        except Exception:
            if changed:
                try:
                    client.call("PUT", path, {"config": before})
                    require(client.call("GET", path)["data"]["config"] == before, "agent_rollback_failed")
                except Exception:
                    raise SafeFailure("agent_rollback_failed") from None
            raise
        results.append({"user": label, "changed": changed})
    save_json(root / "evidence/seed-configure-agent.json", {"users": results, "consent_modified": False})
    print("Selected fixture agents bound to their existing model and Wiki knowledge base; consent unchanged.")


def reparse(args, root, state, clients):
    submitted = 0
    for label, client in clients.items():
        user = state["users"][label]
        for fixture in FIXTURES:
            doc_id = user["documents"].get(fixture["slug"])
            if not doc_id:
                continue
            doc = client.call("GET", "/knowledge/" + doc_id)["data"]
            require(doc.get("id") == doc_id, "source_document_scope")
            check_document(doc, user, fixture)
            if doc.get("parse_status") == "failed":
                client.call("POST", "/knowledge/" + doc_id + "/reparse", {})
                submitted += 1
            # Already-running jobs are awaited, not destructively restarted.
            wait_chunks(client, doc_id, args.timeout)
    save_json(root / "evidence/seed-reparse.json", {
        "users": list(clients), "reparsed": submitted, "completed": True,
    })
    print("Source checks/reparse completed; rerun seed and explicitly refresh changed citations as needed.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("seed", "opt-in", "configure-agent", "reparse"))
    parser.add_argument("--runtime-dir", type=Path, help="Checkout-relative .runtime subdirectory")
    parser.add_argument("--api-base", default=os.environ.get("GL_API_BASE"))
    parser.add_argument("--frontend-base", default=os.environ.get("GL_FRONTEND_BASE"))
    parser.add_argument("--model-env", type=Path, help="Owned 0600 literal env file; otherwise use process environment")
    parser.add_argument("--consent-create-fixtures", action="store_true",
                        help="Authorize disposable accounts, remote model registration and paid ingestion")
    parser.add_argument("--refresh-pages", action="store_true",
                        help="Allow restoring edited pages and replacing stale citations (drops page history)")
    parser.add_argument("--user", choices=(*ACTORS, "all"), help="Explicit fixture user for non-seed commands")
    parser.add_argument("--timeout", type=int, default=900, help="Per-document completion deadline, 1-3600 seconds")
    args = parser.parse_args()
    require(args.command != "seed" or args.consent_create_fixtures,
            "explicit_fixture_creation_consent_required")
    require(args.command == "seed" or args.user is not None, "explicit_fixture_user_required")
    require(args.command == "seed" or not args.refresh_pages, "refresh_pages_requires_seed")
    require(1 <= args.timeout <= 3600, "bounded_timeouts")
    if args.api_base is not None:
        origin(args.api_base, api=True)
    if args.frontend_base is not None:
        origin(args.frontend_base)
    root = runtime_root(args.runtime_dir, create=args.command == "seed")
    prepare(root)
    with fixture_lock(root):
        state = read_state(root, args.api_base, args.frontend_base, create=args.command == "seed")
        public = Client(state["api_base"])
        if args.command == "seed":
            seed(args, root, state, public)
        else:
            labels = ACTORS if args.user == "all" else (args.user,)
            require(all(state["users"][label].get("user_id") and state["users"][label].get("tenant_id")
                        for label in labels), "fixtures_incomplete_run_seed_first")
            clients = {label: login(root, public, state, label) for label in labels}
            {"opt-in": opt_in, "configure-agent": configure_agent, "reparse": reparse}[args.command](
                args, root, state, clients)
    return 0


if __name__ == "__main__":
    os.umask(0o077)
    try:
        sys.exit(main())
    except Exception as error:
        reason = str(error) if isinstance(error, SafeFailure) else "unexpected_error_redacted"
        print("Seed aborted: " + reason + "; credentials and response payloads suppressed.", file=sys.stderr)
        sys.exit(1)
