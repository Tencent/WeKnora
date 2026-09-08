#!/usr/bin/env python3
"""Live acceptance for disposable .runtime learner_b. No API restart or SQL.

python3 -B scripts/test-guided-learning-api.py inspect
python3 -B scripts/test-guided-learning-api.py run --consent-learner-b

Credentials, HTTP payloads, model responses and answer keys are never logged.
Only ignored api-acceptance*.json evidence files are written at runtime.
"""

import argparse
from collections import Counter
import fcntl
import json
import math
import os
from pathlib import Path
import re
import shlex
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

REPO = Path(__file__).resolve().parents[1]
RUNTIME = REPO / ".runtime"
API = "http://127.0.0.1:28081"
PROMPT_VERSION = "learning-quiz-v1"
TOOLS = {"get_learning_profile", "recommend_learning_topics", "prepare_learning_quiz",
         "wiki_search", "wiki_read_page", "wiki_read_source_doc", "thinking", "todo_write", "search_memory"}
QUIZ_STATUSES = {"pending", "running", "ready", "failed", "stale"}
ERROR_CODES = {"learning_forbidden", "learning_disabled", "learning_not_found",
               "learning_stale", "learning_not_ready", "learning_conflict",
               "learning_invalid", "learning_busy", "learning_evidence",
               "learning_unavailable", "generation_failed", "verification_failed",
               "invalid_evidence", "model_unavailable", "source_changed"}


class SafeFailure(Exception):
    """Messages must be fixed, nonsecret check names."""


def require(condition, name):
    if not condition:
        raise SafeFailure(name)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate_json_key")
        result[key] = value
    return result


def strict_json(raw):
    def reject_constant(_value):
        raise SafeFailure("nonfinite_json")
    try:
        return json.loads(raw, object_pairs_hook=unique_object, parse_constant=reject_constant)
    except (ValueError, UnicodeError):
        raise SafeFailure("invalid_json_redacted") from None


def private_text(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd) as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid()
                and stat.S_IMODE(info.st_mode) == 0o600, "private_file_permissions")
        require(info.st_size <= 1024 * 1024, "private_file_size")
        return stream.read()


def private_model():
    result = {}
    for line in private_text(RUNTIME / "model.env").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, value = line.removeprefix("export ").partition("=")
        require(sep and re.fullmatch(r"[A-Z][A-Z0-9_]*", key)
                and key not in result, "private_model_syntax")
        values = shlex.split(value, comments=False)
        require(len(values) == 1, "private_model_syntax")
        result[key] = values[0]
    selected = {}
    for name, aliases in {"name": ("DEEPSEEK_MODEL", "MODEL_NAME"),
                          "base_url": ("DEEPSEEK_BASE_URL", "MODEL_BASE_URL"),
                          "api_key": ("DEEPSEEK_API_KEY", "MODEL_API_KEY")}.items():
        values = [result[key] for key in aliases if result.get(key)]
        require(values and len(set(values)) == 1, "private_model_aliases")
        selected[name] = values[0]
    url = urllib.parse.urlsplit(selected["base_url"])
    require(url.scheme == "https" and url.hostname and not url.username
            and not url.password and not url.query and not url.fragment, "private_model_origin")
    require("deepseek" in selected["name"].lower(), "real_deepseek_required")
    return selected


def validate_origin(value):
    require(value.rstrip("/").removesuffix("/api/v1") == API, "loopback_origin_only")
    return API


def identifier(value):
    require(isinstance(value, str), "invalid_identifier")
    try:
        require(str(uuid.UUID(value)) == value, "invalid_identifier")
    except ValueError:
        raise SafeFailure("invalid_identifier") from None
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, token=None):
        self.token = token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method, path, body=None):
        require(path.startswith("/") and not path.startswith("//")
                and "://" not in path and "#" not in path, "relative_api_path_only")
        headers = {"Accept": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        data = None if body is None else json.dumps(body, allow_nan=False).encode()
        if data is not None:
            headers["Content-Type"] = "application/json"
        return urllib.request.Request(API + "/api/v1" + path, data=data,
                                      headers=headers, method=method)

    def call(self, method, path, body=None, expected=(200,), timeout=45):
        started = time.monotonic()
        try:
            response = self.opener.open(self.request(method, path, body), timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except (urllib.error.URLError, TimeoutError, OSError):
            raise SafeFailure("http_transport_failure_redacted") from None
        with response:
            status = response.code
            raw = response.read(8 * 1024 * 1024 + 1)
            require(len(raw) <= 8 * 1024 * 1024, "http_response_size")
            value = strict_json(raw) if raw else {}
            no_store = "no-store" in response.headers.get("Cache-Control", "")
        require(status in expected, "unexpected_http_status_" + str(status))
        if status < 300:
            # Wiki endpoints return bare DTOs; learning endpoints require an envelope.
            bare_wiki = path.startswith("/knowledgebase/") and "/wiki/" in path
            require(isinstance(value, dict) and (bare_wiki or value.get("success") is True),
                    "http_success_envelope")
        return {"status": status, "data": value.get("data", value),
                "error_code": value.get("error", {}).get("code")
                if isinstance(value.get("error"), dict) else None,
                "no_store": no_store, "seconds": round(time.monotonic() - started, 3)}


class Evidence:
    def __init__(self, command):
        self.started = time.monotonic()
        self.data = {"status": "running", "command": command, "prompt_version": PROMPT_VERSION,
                     "prompt_version_status": "source_contract_not_http_exposed",
                     "started_at": time.strftime("%FT%TZ", time.gmtime()), "checks": []}

    def check(self, name, condition, **facts):
        row = {"check": name, "outcome": "pass" if condition else "fail", **facts}
        self.data["checks"].append(row)
        print(json.dumps(row, sort_keys=True), flush=True)
        return condition

    def observed(self, name, **facts):
        row = {"check": name, "outcome": "observed", **facts}
        self.data["checks"].append(row)
        print(json.dumps(row, sort_keys=True), flush=True)

    def finish(self):
        self.data["seconds"] = round(time.monotonic() - self.started, 3)
        counts = Counter(row["outcome"] for row in self.data["checks"])
        self.data["counts"] = dict(counts)
        self.data["status"] = "fail" if counts["fail"] else "pass"
        name = "api-acceptance-" + time.strftime("%Y%m%dT%H%M%S", time.gmtime())
        path = RUNTIME / "evidence" / (name + "-" + str(os.getpid()) + ".json")
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(self.data, stream, indent=2, allow_nan=False)
            stream.write("\n")
        print("Evidence: " + str(path.relative_to(REPO)), flush=True)
        return 1 if counts["fail"] else 0


def accounts():
    state = strict_json(private_text(RUNTIME / "seed-accounts.json"))
    validate_origin(state["api_base"])
    require(set(state["users"]) == {"learner_a", "learner_b"}, "seed_user_scope")
    require(state["users"]["learner_a"]["tenant_id"] != state["users"]["learner_b"]["tenant_id"],
            "distinct_seed_tenants")
    manifest = strict_json((RUNTIME / "evidence/seed.json").read_text())
    clients = {}
    for label, user in state["users"].items():
        require(user["email"].endswith("@example.invalid")
                and user["username"].startswith("gl_" + label + "_"), "disposable_seed_only")
        for key in ("user_id", "knowledge_base_id", "model_id"):
            identifier(user[key])
        fixture = manifest["fixtures"][label]
        require(all(user[key] == fixture[key] for key in ("tenant_id", "knowledge_base_id", "model_id")),
                "fresh_fixture_scope")
        user["documents"], user["pages"] = fixture["documents"], fixture["pages"]
        result = Client().call("POST", "/auth/login", {
            "email": user["email"], "password": user["password"],
        })["data"]
        tenant = (result.get("active_tenant") or {}).get("id", result["user"]["tenant_id"])
        require(result["user"]["id"] == user["user_id"] and tenant == user["tenant_id"]
                and result.get("token"), "authenticated_seed_identity")
        clients[label] = Client(result["token"])
    return state, clients


def exported(client):
    return client.call("GET", "/learning/export")["data"]


def export_counts(value):
    return {key: len(value.get(key) or []) for key in ("nodes", "quizzes", "attempts")}


def inspect(state, clients, evidence):
    model = private_model()
    for label, client in clients.items():
        user = state["users"][label]
        settings = client.call("GET", "/learning/settings")
        evidence.observed(label + "_settings", enabled=settings["data"]["enabled"],
                          http_status=settings["status"], no_store=settings["no_store"])
        evidence.observed(label + "_export_counts", **export_counts(exported(client)))
        models = client.call("GET", "/models")["data"]
        registered = next((item for item in models if item["id"] == user["model_id"]), {})
        require(evidence.check(label + "_registered_deepseek",
                registered.get("name") == model["name"] and registered.get("source") == "remote",
                model_id=user["model_id"]), "registered_model_mismatch")
        statuses = Counter()
        for doc_id in user["documents"].values():
            doc = client.call("GET", "/knowledge/" + identifier(doc_id))["data"]
            status = doc.get("parse_status")
            statuses[status if status in {"pending", "processing", "chunking", "indexing",
                     "summarizing", "finalizing", "completed", "failed", "running"} else "other"] += 1
        evidence.observed(label + "_source_statuses", counts=dict(statuses))


def http_check(evidence, name, response, status=200, code=None):
    facts = {"http_status": response["status"], "seconds": response["seconds"]}
    if response["error_code"] in ERROR_CODES:
        facts["error_code"] = response["error_code"]
    return evidence.check(name, response["status"] == status and response["no_store"]
                          and (code is None or response["error_code"] == code), **facts)


def public_quiz(quiz):
    require(set(quiz) <= {"id", "page_id", "knowledge_base_id", "slug", "title", "status",
                         "error_code", "questions", "algorithm_version"}, "quiz_private_fields")
    require(quiz["status"] in QUIZ_STATUSES, "unknown_quiz_status")
    for question in quiz.get("questions") or []:
        allowed = {"id", "prompt", "options", "answered"}
        if question.get("answered"):
            allowed.add("result")
        require(set(question) <= allowed, "unanswered_question_private_fields")
        require(all(set(option) == {"id", "text"} for option in question["options"]),
                "option_private_fields")
        require(len(question["options"]) == 4 and len({o["id"] for o in question["options"]}) == 4
                and len({o["text"] for o in question["options"]}) == 4, "four_distinct_options")
    return True


def current_source(args, client, user, evidence):
    kb_id = user["knowledge_base_id"]
    slug = "concept/topic4-feedback"
    doc_id = identifier(user["documents"][slug])
    start = time.monotonic()
    while True:
        doc = client.call("GET", "/knowledge/" + doc_id)["data"]
        if doc.get("parse_status") == "completed":
            break
        require(doc.get("parse_status") != "failed", "source_ingestion_failed")
        require(time.monotonic() - start < args.source_timeout, "source_completion_timeout")
        time.sleep(3)
    require(doc["knowledge_base_id"] == kb_id and doc["enable_status"] == "enabled",
            "source_document_scope")
    page = client.call("GET", f"/knowledgebase/{kb_id}/wiki/pages/{slug}")["data"]
    chunks = client.call("GET", f"/chunks/{doc_id}?page=1&page_size=100")["data"]
    require(page["id"] == user["pages"][slug]["page_id"] and page["status"] == "published"
            and page["page_type"] == "concept", "current_published_fixture_page")
    require(any(ref.split("|", 1)[0] == doc_id for ref in page["source_refs"]), "page_source_membership")
    current = {chunk["id"]: chunk for chunk in chunks if chunk.get("content")
               and chunk.get("index_status") == "ready" and chunk.get("is_enabled") is True}
    require(page["chunk_refs"] and all(ref in current for ref in page["chunk_refs"]),
            "current_persisted_citations")
    kb = client.call("GET", "/knowledge-bases/" + kb_id)["data"]
    require(kb["indexing_strategy"]["wiki_enabled"] and
            (kb.get("wiki_config") or {}).get("synthesis_model_id", kb.get("summary_model_id"))
            == user["model_id"], "quiz_source_model_binding")
    evidence.check("current_completed_source", True, documents=1, chunks=len(page["chunk_refs"]),
                   seconds=round(time.monotonic() - start, 3), model_id=user["model_id"])
    return page, current


def poll_quiz(args, client, quiz, evidence, label):
    start = time.monotonic()
    polls = 0
    transitions = []
    while True:
        public_quiz(quiz)
        status = quiz["status"]
        if not transitions or transitions[-1] != status:
            transitions.append(status)
            evidence.observed(label + "_status", status=status, quiz_id=identifier(quiz["id"]),
                              seconds=round(time.monotonic() - start, 3))
        if status in {"ready", "failed", "stale"} or time.monotonic() - start >= args.quiz_timeout:
            break
        time.sleep(3)
        response = client.call("GET", "/learning/question-sets/" + identifier(quiz["id"]))
        require(response["no_store"], "quiz_cache_control")
        quiz = response["data"]
        polls += 1
    code = quiz.get("error_code")
    evidence.check(label + "_real_model_ready", status == "ready", status=status,
                   error_code=code if code in ERROR_CODES else None, quiz_id=quiz["id"],
                   polls=polls, seconds=round(time.monotonic() - start, 3))
    return quiz if status == "ready" else None


def quiz_acceptance(args, clients, user, page, chunks, evidence):
    b, a = clients["learner_b"], clients["learner_a"]
    response = b.call("POST", "/learning/question-sets", {"page_id": page["id"]},
                      expected=(200, 400, 403, 409, 422, 429, 500))
    if not http_check(evidence, "prepare_quiz", response):
        return
    quiz = poll_quiz(args, b, response["data"], evidence, "http_quiz")
    if quiz is None:
        return
    questions = quiz["questions"]
    evidence.check("preanswer_quiz_has_no_answer_key", public_quiz(quiz)
                   and len(questions) == 3 and all(not q["answered"] for q in questions), questions=len(questions))
    require(len(questions) == 3 and all(not q["answered"] for q in questions), "fresh_unanswered_quiz_required")
    before_export = exported(b)
    evidence.check("preanswer_export_has_no_answer_key",
                   all(public_quiz(q) for q in before_export["quizzes"])
                   and not before_export["attempts"])
    q = questions[0]
    require(any(option["id"] == "0" for option in q["options"]), "choice_zero_available")
    # This choice is fixed before receiving grading. Never search for the correct option.
    answer = {"question_id": identifier(q["id"]), "option_id": "0", "attempt_id": str(uuid.uuid4())}
    foreign_read = a.call("GET", "/learning/question-sets/" + quiz["id"], expected=(200, 403, 404))
    http_check(evidence, "cross_tenant_quiz_read", foreign_read, 404, "learning_not_found")
    if foreign_read["status"] == 404:
        foreign_answer = a.call("POST", "/learning/attempts", answer, expected=(200, 403, 404, 409))
        http_check(evidence, "cross_tenant_quiz_answer", foreign_answer, 404, "learning_not_found")
    else:
        evidence.check("cross_tenant_answer_blocked_by_read_failure", False)
    node_before = b.call("GET", "/learning/nodes/" + page["id"])["data"]
    graded = b.call("POST", "/learning/attempts", answer)
    http_check(evidence, "submit_choice_zero", graded)
    result = graded["data"]
    selected = result["selected_option"]
    server_correct = result["correct"]
    evidence.check("server_grading_matches_selected_option",
                   type(server_correct) is bool and selected == "0"
                   and server_correct == (selected == result["correct_option"])
                   and result["attempt_id"] == answer["attempt_id"]
                   and result["correct_option"] in {o["id"] for o in q["options"]},
                   server_correct=server_correct)
    quotes = result.get("evidence") or []
    supported = bool(quotes) and all(
        item["chunk_id"] in chunks
        and item["knowledge_id"] == chunks[item["chunk_id"]]["knowledge_id"]
        and " ".join(item["quote"].split()) in " ".join(chunks[item["chunk_id"]]["content"].split())
        for item in quotes)
    evidence.check("postanswer_explanation_is_source_backed", bool(result["explanation"]) and supported,
                   evidence_quotes=len(quotes))
    prior = node_before["mastery"]["p_mastery"]
    likelihood_known, likelihood_unknown = (0.9, 0.25) if server_correct else (0.1, 0.75)
    posterior = prior * likelihood_known / (prior * likelihood_known + (1 - prior) * likelihood_unknown)
    expected = posterior + (1 - posterior) * 0.15
    evidence.check("first_answer_mastery_update", result["mastery"]["attempts"] == 1
                   and math.isclose(result["mastery"]["p_mastery"], expected, abs_tol=1e-9), attempts=1)
    first_export = exported(b)
    replay = b.call("POST", "/learning/attempts", answer)
    replay_export = exported(b)
    evidence.check("same_attempt_replay_unchanged", replay["data"] == result
                   and first_export["attempts"] == replay_export["attempts"]
                   and len(replay_export["attempts"]) == 1, attempts=len(replay_export["attempts"]),
                   http_status=replay["status"])
    conflict = b.call("POST", "/learning/attempts", {**answer, "option_id": "1"}, expected=(200, 409))
    http_check(evidence, "conflicting_attempt", conflict, 409, "learning_conflict")
    after = exported(b)
    evidence.check("conflicting_attempt_does_not_increment", after["attempts"] == first_export["attempts"],
                   attempts=len(after["attempts"]))
    served = b.call("GET", "/learning/question-sets/" + quiz["id"])["data"]
    evidence.check("only_answered_question_reveals_result", public_quiz(served)
                   and sum(bool(item.get("result")) for item in served["questions"]) == 1)


def stream_agent(client, session_id, body, timeout):
    start = time.monotonic()
    summary = {"status": "incomplete", "http_status": 0, "events": Counter(),
               "calls": Counter(), "successful_tools": Counter(), "failed_tools": Counter(),
               "cards": [], "message_id": None, "private_fields": False, "answer_characters": 0}
    fields = []
    size = 0
    try:
        request = client.request("POST", "/agent-chat/" + session_id, body)
        request.add_header("Accept", "text/event-stream")
        with client.opener.open(request, timeout=timeout) as response:
            summary["http_status"] = response.code
            require("text/event-stream" in response.headers.get("Content-Type", ""), "agent_not_sse")
            while time.monotonic() - start < timeout:
                response.fp.raw._sock.settimeout(max(0.1, timeout - (time.monotonic() - start)))
                line = response.readline(4 * 1024 * 1024 + 1)
                size += len(line)
                require(size <= 16 * 1024 * 1024 and len(line) <= 4 * 1024 * 1024, "agent_stream_size")
                if not line:
                    break
                line = line.rstrip(b"\r\n")
                if line.startswith(b"data:"):
                    fields.append(line[5:].lstrip())
                elif not line and fields:
                    event = strict_json(b"\n".join(fields))
                    fields = []
                    kind = event.get("response_type", "other")
                    summary["events"][kind if kind in {"answer", "thinking", "tool_call", "tool_result",
                        "error", "complete", "agent_query", "references"} else "other"] += 1
                    data = event.get("data") or {}
                    message_id = event.get("assistant_message_id") or data.get("assistant_message_id")
                    if message_id:
                        summary["message_id"] = identifier(message_id)
                    tool = data.get("tool_name")
                    if tool:
                        tool = tool if tool in TOOLS else "other"
                        if kind == "tool_call":
                            summary["calls"][tool] += 1
                        elif kind in {"tool_result", "error"}:
                            key = "successful_tools" if data.get("success") is True else "failed_tools"
                            summary[key][tool] += 1
                    if data.get("display_type") == "learning_quiz":
                        summary["private_fields"] |= any(key in data for key in (
                            "questions", "correct_option", "answer_index", "explanation", "evidence"))
                        summary["cards"].append({"quiz_id": identifier(data["quiz_id"]),
                                                 "page_id": data.get("page_id"),
                                                 "knowledge_base_id": data.get("knowledge_base_id")})
                    if kind == "answer":
                        summary["answer_characters"] += len(event.get("content") or "")
                    if kind == "error" and event.get("done"):
                        summary["status"] = "terminal_error"
                        break
                    if kind == "complete":
                        summary["status"] = "completed"
                        break
            else:
                summary["status"] = "timeout"
    except urllib.error.HTTPError as error:
        summary["http_status"] = error.code
        summary["status"] = "http_error"
        error.close()
    except (TimeoutError, urllib.error.URLError, OSError):
        summary["status"] = "timeout_or_transport_error"
    summary["seconds"] = round(time.monotonic() - start, 3)
    return summary


def agent_session(args, b, user, page, evidence):
    created = b.call("POST", "/sessions", {"title": "Disposable guided-learning HTTP acceptance",
                     "description": "Owned learner_b acceptance; delete after test"}, expected=(200, 201))
    session_id = identifier(created["data"]["id"])
    base = {"agent_id": "builtin-guided-learning", "agent_enabled": True, "disable_title": True,
            "knowledge_base_ids": [user["knowledge_base_id"]], "knowledge_ids": [],
            "summary_model_id": user["model_id"], "web_search_enabled": False, "channel": "web"}
    queries = [
        ("recommendations", "Call get_learning_profile and recommend_learning_topics to recommend one next topic from the selected Wiki "
         "knowledge base. Read the recommended Wiki page before explaining it, and cite the returned "
         "recommendation reasons. Use wiki_read_page for the reading. Do not prepare a quiz yet."),
        ("quiz_card", "Call prepare_learning_quiz to prepare a practice quiz card for the exact Wiki page " + page["slug"]
         + " in the selected knowledge base. Read that page if needed. Let me answer through the "
         "quiz interface; do not answer any questions for me."),
    ]
    active = None
    try:
        for label, query in queries:
            evidence.observed("agent_" + label + "_started", model_id=user["model_id"])
            active = stream_agent(b, session_id, {**base, "query": query}, args.agent_timeout)
            facts = {key: active[key] for key in ("status", "http_status", "events", "calls",
                     "successful_tools", "failed_tools", "answer_characters", "seconds")}
            successful = active["successful_tools"]
            expected = {"get_learning_profile", "recommend_learning_topics", "wiki_read_page"}
            ok = all(active["calls"][tool] and successful[tool] for tool in expected) if label == "recommendations" else (
                active["calls"]["prepare_learning_quiz"] > 0 and successful["prepare_learning_quiz"] > 0
                and bool(active["cards"]) and not active["private_fields"]
                and all(card["page_id"] == page["id"] and card["knowledge_base_id"] == user["knowledge_base_id"]
                        for card in active["cards"]))
            evidence.check("agent_" + label + "_real_tool_flow", ok and active["status"] == "completed"
                           and not active["failed_tools"], **facts)
            for card in active["cards"]:
                evidence.observed("agent_quiz_card_reference", quiz_id=card["quiz_id"])
            if active["status"] != "completed":
                break
    finally:
        if active and active["status"] != "completed":
            message_id = active["message_id"]
            if not message_id:
                messages = b.call("GET", f"/messages/{session_id}/load")["data"]
                pending = [m for m in messages if m.get("role") == "assistant" and not m.get("is_completed")]
                message_id = pending[-1]["id"] if pending else None
            if message_id:
                stopped = b.call("POST", f"/sessions/{session_id}/stop", {"message_id": identifier(message_id)})
                evidence.check("agent_owned_session_stopped", stopped["status"] == 200)
        deleted = b.call("DELETE", "/sessions/" + session_id)
        evidence.check("agent_owned_session_deleted", deleted["status"] == 200)


def agent_acceptance(args, b, user, page, evidence):
    path = "/agents/builtin-guided-learning"
    original = b.call("GET", path)["data"]
    require(original["is_builtin"] and original["tenant_id"] == user["tenant_id"],
            "builtin_agent_tenant_scope")
    config = original["config"]
    evidence.observed("agent_model_binding_before", configured=bool(config.get("model_id")))
    allowed = config.get("allowed_tools") or []
    evidence.observed("agent_configured_tools", counts=dict(Counter(
        tool if tool in TOOLS else "other" for tool in allowed)))
    require(all(tool in allowed for tool in ("get_learning_profile", "recommend_learning_topics", "prepare_learning_quiz", "wiki_read_page")), "agent_tool_allowlist")
    changed = config.get("model_id") != user["model_id"]
    try:
        if changed:
            configured = b.call("PUT", path, {"config": {**config, "model_id": user["model_id"]}})
            require(configured["data"]["config"]["model_id"] == user["model_id"],
                    "agent_model_binding")
            evidence.check("agent_temporary_model_binding", True, model_id=user["model_id"])
        agent_session(args, b, user, page, evidence)
    finally:
        if changed:
            b.call("PUT", path, {"config": config})
            restored = b.call("GET", path)["data"]["config"]
            evidence.check("agent_original_config_restored", restored == config)


def preserves(before, after):
    attempts = {a["result"]["attempt_id"]: a for a in after["attempts"]}
    return (all(attempts.get(a["result"]["attempt_id"]) == a for a in before["attempts"])
            and {n["page_id"] for n in before["nodes"]} <= {n["page_id"] for n in after["nodes"]}
            and {q["id"] for q in before["quizzes"]} <= {q["id"] for q in after["quizzes"]})


def clear_acceptance(b, a, evidence):
    a_before = exported(a)
    b_before = exported(b)
    disabled = b.call("PUT", "/learning/settings", {"enabled": False})
    http_check(evidence, "opt_out", disabled)
    response = b.call("GET", "/learning/export")
    http_check(evidence, "disabled_export_available", response)
    evidence.check("opt_out_preserves_attempts", response["data"]["attempts"] == b_before["attempts"],
                   attempts=len(response["data"]["attempts"]))
    cleared = b.call("DELETE", "/learning/profile")
    http_check(evidence, "disabled_clear_available", cleared)
    evidence.observed("clear_deleted_counts", **{key: cleared["data"][key] for key in (
        "deleted_attempts", "deleted_mastery", "deleted_quizzes")})
    remaining = exported(b)
    evidence.check("learner_b_cleared_and_disabled", not remaining["settings"]["enabled"]
                   and not any(export_counts(remaining).values()), **export_counts(remaining))
    repeated = b.call("DELETE", "/learning/profile")["data"]
    evidence.check("clear_idempotent", all(repeated[key] == 0 for key in (
        "deleted_attempts", "deleted_mastery", "deleted_quizzes")))
    a_after = exported(a)
    evidence.check("learner_a_intact_across_b_clear", preserves(a_before, a_after)
                   and a_before["settings"] == a_after["settings"],
                   before_counts=export_counts(a_before), after_counts=export_counts(a_after))


def run_acceptance(args, state, clients, evidence):
    b, a, user = clients["learner_b"], clients["learner_a"], state["users"]["learner_b"]
    initial = b.call("GET", "/learning/settings")
    previous = exported(b)
    if initial["data"]["enabled"] or any(export_counts(previous).values()):
        evidence.observed("resuming_owned_b_profile", **export_counts(previous))
        b.call("DELETE", "/learning/profile")
        initial = b.call("GET", "/learning/settings")
        default_name = "post_resume_clear_default_false"
    else:
        default_name = "settings_default_false"
    evidence.check(default_name, initial["data"]["enabled"] is False and initial["no_store"], http_status=initial["status"])
    a_start = exported(a)
    try:
        enabled = b.call("PUT", "/learning/settings", {"enabled": True})
        evidence.check("explicit_b_opt_in", enabled["data"]["enabled"] is True and enabled["no_store"],
                       http_status=enabled["status"])
        query = urllib.parse.urlencode({"knowledge_base_id": user["knowledge_base_id"]})
        overview = b.call("GET", "/learning/overview?" + query)
        overview_data = overview["data"]
        evidence.check("enabled_overview", overview_data["enabled"] is True
                       and overview_data["knowledge_base_id"] == user["knowledge_base_id"]
                       and sum(overview_data["counts"].values()) == overview_data["total_nodes"]
                       and overview_data["total_nodes"] >= 3 and overview["no_store"],
                       http_status=overview["status"], total_nodes=overview_data["total_nodes"])
        recs = b.call("GET", "/learning/recommendations?" + query + "&limit=5")
        repeated = b.call("GET", "/learning/recommendations?" + query + "&limit=5")
        evidence.check("enabled_recommendations_deterministic", bool(recs["data"])
                       and recs["data"] == repeated["data"] and recs["no_store"]
                       and all(item["knowledge_base_id"] == user["knowledge_base_id"]
                               and item["reason_codes"] for item in recs["data"]),
                       http_status=recs["status"], recommendations=len(recs["data"]))
        page, chunks = current_source(args, b, user, evidence)
        prior = b.call("GET", "/learning/nodes/" + page["id"])["data"]
        b.call("POST", "/learning/nodes/" + page["id"] + "/view", {})
        viewed = b.call("GET", "/learning/nodes/" + page["id"])["data"]
        evidence.check("reading_changes_familiarity_not_mastery", viewed["familiar"]
                       and prior["mastery"] == viewed["mastery"])
        overlay = b.call("POST", "/learning/overlay", {"knowledge_base_id": user["knowledge_base_id"],
                         "slugs": [page["slug"]]})
        evidence.check("wiki_overlay_scope", len(overlay["data"]) == 1
                       and overlay["data"][0]["page_id"] == page["id"])
        for name, action in (
            ("http_quiz", lambda: quiz_acceptance(args, clients, user, page, chunks, evidence)),
            ("agent", lambda: agent_acceptance(args, b, user, page, evidence)),
        ):
            try:
                action()
            except Exception as error:
                code = str(error) if isinstance(error, SafeFailure) else "exception_redacted"
                evidence.check(name + "_" + code, False)
    finally:
        clear_acceptance(b, a, evidence)
        evidence.check("learner_a_preexisting_data_preserved", preserves(a_start, exported(a)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("inspect", "run"))
    parser.add_argument("--api-base", default=API)
    parser.add_argument("--consent-learner-b", action="store_true")
    parser.add_argument("--source-timeout", type=int, default=180)
    parser.add_argument("--quiz-timeout", type=int, default=360)
    parser.add_argument("--agent-timeout", type=int, default=300)
    args = parser.parse_args()
    validate_origin(args.api_base)
    require(args.command != "run" or args.consent_learner_b, "explicit_b_consent_required")
    require(180 <= args.quiz_timeout <= 900 and 180 <= args.agent_timeout <= 900
            and 1 <= args.source_timeout <= 900, "bounded_timeouts")
    evidence = Evidence(args.command)
    try:
        state, clients = accounts()
        inspect(state, clients, evidence)
        if args.command == "run":
            run_acceptance(args, state, clients, evidence)
    except Exception as error:
        name = str(error) if isinstance(error, SafeFailure) else "unexpected_exception_redacted"
        evidence.check(name, False)
    return evidence.finish()


if __name__ == "__main__":
    os.umask(0o077)
    try:
        # Lock the owned script itself; no lock file or shared runtime state is written.
        with Path(__file__).open() as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            sys.exit(main())
    except Exception:
        print("Acceptance aborted; error details redacted.", file=sys.stderr)
        sys.exit(1)
