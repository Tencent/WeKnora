#!/usr/bin/env python3
"""
Script de importação local da Base de Conhecimento Nutrisano para o Beemotik-WeKnora.
Aplica a Abordagem 1: Organização em 4 KBs por Tipo e Propósito.
Desconsidera mensagens de 'Grupo de Whatsapp'.
"""

import csv
import json
import os
import sys
import urllib.error
import urllib.request

# Configurações Padrão
WEKNORA_HOST = os.getenv("WEKNORA_HOST", "http://localhost:8080")
EMAIL = os.getenv("WEKNORA_EMAIL", "plinio@beemotik.com")
PASSWORD = os.getenv("WEKNORA_PASSWORD", "password123")
CSV_PATH = os.getenv(
    "CSV_PATH",
    "/Users/esmerio/Downloads/Base_Conhecimento_2026-07-21.xlsx - Base de Conhecimento.csv",
)
EMBEDDING_MODEL_ID = os.getenv("EMBEDDING_MODEL_ID", "openrouter-embedding-small")

KB_DEFINITIONS = [
    {
        "name": "Legislação e Normas Sanitárias",
        "type": "document",
        "description": "Portaria CVS 03, Portaria 2619, RDC 216 e regulamentos sanitários",
    },
    {
        "name": "POPs e Manuais Operacionais",
        "type": "document",
        "description": "POPs 1 a 31, Manuais de Boas Práticas, Anexos e Planilhas (GUI)",
    },
    {
        "name": "Dúvidas e Casos de Campo",
        "type": "faq",
        "description": "Dúvidas operacionais, documentais e de atendimento dos consultores de campo",
    },
    {
        "name": "Sistemas e Institucional",
        "type": "document",
        "description": "Sistema SULTS, microvídeos, podcast, institucional e serviços Nutrisano",
    },
]


def make_request(url, data=None, headers=None, method="GET"):
    if headers is None:
        headers = {}

    req_data = None
    if data is not None:
        req_data = json.dumps(data).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = urllib.request.Request(url, data=req_data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8")
        print(f"❌ Erro HTTP {e.code} em {url}: {err_body}")
        raise e


def login():
    print(f"🔑 Autenticando no WeKnora ({WEKNORA_HOST})...")
    url = f"{WEKNORA_HOST}/api/v1/auth/login"
    res = make_request(
        url, data={"email": EMAIL, "password": PASSWORD}, method="POST"
    )
    token = res.get("token")
    if not token:
        raise RuntimeError("Token não recebido no login.")
    print("✅ Autenticado com sucesso!")
    return token


def classify_item(title):
    t_lower = title.lower()
    if any(k in t_lower for k in ["cvs 3", "cvs 03", "portaria 2619", "rdc 216", "leis"]):
        return "Legislação e Normas Sanitárias"
    elif any(k in t_lower for k in ["pop ", "pop", "manual:", "gui -"]):
        return "POPs e Manuais Operacionais"
    elif any(k in t_lower for k in ["dúvida", "dúvidas"]):
        return "Dúvidas e Casos de Campo"
    else:
        return "Sistemas e Institucional"


def ensure_knowledge_bases(token):
    headers = {"Authorization": f"Bearer {token}"}

    # Listar KBs existentes
    list_url = f"{WEKNORA_HOST}/api/v1/knowledge-bases"
    existing_res = make_request(list_url, headers=headers, method="GET")
    existing_kbs = {kb["name"]: kb for kb in existing_res.get("data", [])}

    kb_map = {}

    for kb_def in KB_DEFINITIONS:
        name = kb_def["name"]
        if name in existing_kbs:
            kb_info = existing_kbs[name]
            print(f"ℹ️ KB já existente: '{name}' (ID: {kb_info['id']})")
            kb_map[name] = kb_info
        else:
            print(f"➕ Criando KB '{name}' (Tipo: {kb_def['type']})...")
            create_payload = {
                "name": name,
                "description": kb_def["description"],
                "type": kb_def["type"],
                "embedding_model_id": EMBEDDING_MODEL_ID,
            }
            res = make_request(
                list_url, data=create_payload, headers=headers, method="POST"
            )
            kb_data = res.get("data")
            print(f"✅ KB criada: '{name}' (ID: {kb_data['id']})")
            kb_map[name] = kb_data

    return kb_map


def import_csv(token, kb_map):
    headers = {"Authorization": f"Bearer {token}"}

    if not os.path.exists(CSV_PATH):
        print(f"❌ Arquivo CSV não encontrado: {CSV_PATH}")
        sys.exit(1)

    print(f"\n📄 Lendo arquivo CSV: {CSV_PATH}")

    items_by_kb = {name: [] for name in kb_map.keys()}
    skipped_whatsapp = 0

    with open(CSV_PATH, mode="r", encoding="utf-8", errors="ignore") as f:
        reader = csv.reader(f)
        header = next(reader, None)

        for row in reader:
            if not row or len(row) < 2:
                continue

            title = row[0].strip()
            tipo = row[1].strip()
            content = row[5].strip() if len(row) > 5 else ""

            if tipo == "Grupo de Whatsapp":
                skipped_whatsapp += 1
                continue

            target_kb = classify_item(title)
            items_by_kb[target_kb].append({"title": title, "content": content})

    print(f"🚫 Mensagens de Whatsapp ignoradas: {skipped_whatsapp}")

    summary_stats = {}

    for kb_name, items in items_by_kb.items():
        kb_info = kb_map[kb_name]
        kb_id = kb_info["id"]
        kb_type = kb_info["type"]

        print(f"\n🚀 Importando {len(items)} itens para KB '{kb_name}' (Tipo: {kb_type})...")

        count_success = 0
        count_failed = 0

        for idx, item in enumerate(items, 1):
            title = item["title"]
            content = item["content"]

            try:
                if kb_type == "faq":
                    faq_url = f"{WEKNORA_HOST}/api/v1/knowledge-bases/{kb_id}/faq/entry"
                    payload = {
                        "standard_question": title,
                        "answers": [content] if content else ["Sem resposta cadastrada."],
                        "is_enabled": True,
                    }
                    make_request(faq_url, data=payload, headers=headers, method="POST")
                else:
                    doc_url = f"{WEKNORA_HOST}/api/v1/knowledge-bases/{kb_id}/knowledge/manual"
                    payload = {
                        "title": title,
                        "content": content if content else title,
                        "status": "publish",
                    }
                    make_request(doc_url, data=payload, headers=headers, method="POST")

                count_success += 1
                if idx % 10 == 0 or idx == len(items):
                    print(f"   └─ Progresso: {idx}/{len(items)} processados...")

            except Exception as e:
                count_failed += 1
                print(f"   ⚠️ Falha ao importar item '{title[:50]}...': {e}")

        summary_stats[kb_name] = {
            "id": kb_id,
            "type": kb_type,
            "total": len(items),
            "success": count_success,
            "failed": count_failed,
        }

    return summary_stats


def main():
    print("==================================================")
    print("  Importador Local de Conhecimento Nutrisano      ")
    print("  Beemotik-WeKnora (Abordagem 1)                  ")
    print("==================================================\n")

    token = login()
    kb_map = ensure_knowledge_bases(token)
    stats = import_csv(token, kb_map)

    print("\n==================================================")
    print("  RESUMO DA IMPORTAÇÃO                            ")
    print("==================================================")
    for name, st in stats.items():
        print(
            f"• KB: {name:32s} | Type: {st['type']:8s} | Sucesso: {st['success']:3d}/{st['total']:3d} | Falhas: {st['failed']}"
        )
    print("==================================================")
    print("✨ Importação concluída!")


if __name__ == "__main__":
    main()
