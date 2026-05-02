#!/usr/bin/env python3
"""
Concept Graph Material → OpenMAIC Requirement 转换器

将 WeKnora wiki 知识图谱中的 concept 页面及其关联 entity 转换为
结构化的 OpenMAIC 课程生成需求描述。两阶段转换：
  1. Concept Graph Material → Pedagogical Design JSON
  2. Pedagogical Design JSON → requirement string

此脚本仅做数据转换，不涉及网络调用。

用法:
  echo '{"concept": {...}, "entities": [...]}' | python scripts/concept-to-requirement.py
  python scripts/concept-to-requirement.py --file input.json
"""

import json
import re
import sys
from typing import Any


def _tokenize(text: str) -> list[str]:
    """Simple whitespace + punctuation tokenizer for title/slug overlap."""
    return [t.lower() for t in re.split(r"[\s_\-/]+", text) if t]


def _score_entity(
    entity: dict[str, Any],
    concept_title_tokens: list[str],
    concept_summary_keywords: list[str],
    concept_slug_tokens: list[str],
) -> int:
    """Score an entity by relevance to the concept (pure text heuristics)."""
    score = 0

    # link_type scoring
    link_type = entity.get("link_type", "")
    if link_type == "bidirectional":
        score += 3
    elif link_type == "outlink":
        score += 2
    elif link_type == "inlink":
        score += 1

    # title token overlap
    entity_title_tokens = set(_tokenize(entity.get("title", "")))
    overlap = entity_title_tokens & set(concept_title_tokens)
    score += min(len(overlap), 2)

    # summary keyword hit
    entity_summary = (entity.get("summary") or "").lower()
    keyword_hits = sum(1 for kw in concept_summary_keywords if kw in entity_summary)
    score += min(keyword_hits, 2)

    # slug token hit
    entity_slug_tokens = set(_tokenize(entity.get("slug", "")))
    slug_overlap = entity_slug_tokens & set(concept_slug_tokens)
    score += min(len(slug_overlap), 1)

    # penalty for empty summary
    if not entity.get("summary"):
        score -= 2

    return score


def _classify_entity(
    entity: dict[str, Any], concept_title: str
) -> str:
    """Classify an entity into a pedagogical role."""
    title = (entity.get("title") or "").lower()
    summary = (entity.get("summary") or "").lower()
    text = f"{title} {summary}"

    tool_keywords = ["工具", "平台", "框架", "库", "sdk", "api", "tool", "platform", "framework", "library"]
    example_keywords = ["案例", "实例", "示例", "应用", "case", "example", "application", "demo"]
    prereq_keywords = ["前提", "基础", "前置", "先决", "prerequisite", "foundation", "basic"]

    if any(kw in text for kw in tool_keywords):
        return "Tools"
    if any(kw in text for kw in example_keywords):
        return "Examples"
    if any(kw in text for kw in prereq_keywords):
        return "Prerequisites"
    return "Application Scenarios"


def _parse_markdown_sections(content: str) -> dict[str, str]:
    """Parse markdown content into sections keyed by heading."""
    sections: dict[str, str] = {}
    current_heading = ""
    current_lines: list[str] = []

    for line in content.split("\n"):
        heading_match = re.match(r"^(#{1,4})\s+(.+)$", line)
        if heading_match:
            if current_heading:
                sections[current_heading] = "\n".join(current_lines).strip()
            current_heading = heading_match.group(2).strip()
            current_lines = []
        else:
            current_lines.append(line)

    if current_heading:
        sections[current_heading] = "\n".join(current_lines).strip()

    return sections


def _extract_key_points(sections: dict[str, str]) -> list[str]:
    """Extract key points from markdown sections (definitions, mechanisms)."""
    points: list[str] = []
    definition_headings = {"定义", "概念", "概述", "简介", "Definition", "Overview", "Introduction"}
    mechanism_headings = {"机制", "原理", "工作原理", "Mechanism", "How it works", "Principle"}

    for heading, body in sections.items():
        if any(d in heading for d in definition_headings):
            first_para = body.split("\n\n")[0].strip()
            if first_para:
                points.append(first_para[:200])
        elif any(m in heading for m in mechanism_headings):
            bullets = [l.strip().lstrip("-*• ") for l in body.split("\n") if l.strip().startswith(("- ", "* ", "• "))]
            points.extend(bullets[:3])

    return points[:5]


def _extract_examples(sections: dict[str, str]) -> list[str]:
    """Extract examples from markdown sections."""
    example_headings = {"案例", "示例", "实例", "应用场景", "Example", "Use Case", "Application"}
    examples: list[str] = []

    for heading, body in sections.items():
        if any(e in heading for e in example_headings):
            bullets = [l.strip().lstrip("-*• ") for l in body.split("\n") if l.strip().startswith(("- ", "* ", "• "))]
            examples.extend(bullets[:3])

    return examples[:3]


def _extract_misconceptions(sections: dict[str, str]) -> list[str]:
    """Extract common misconceptions from markdown sections."""
    misconception_headings = {"误区", "常见错误", "误解", "Misconception", "Common mistake", "Pitfall"}
    misconceptions: list[str] = []

    for heading, body in sections.items():
        if any(m in heading for m in misconception_headings):
            bullets = [l.strip().lstrip("-*• ") for l in body.split("\n") if l.strip().startswith(("- ", "* ", "• "))]
            misconceptions.extend(bullets[:3])

    return misconceptions[:3]


def build_pedagogical_design(data: dict[str, Any]) -> dict[str, Any]:
    """Stage 1: Concept Graph Material → Pedagogical Design JSON."""
    concept = data["concept"]
    entities = data.get("entities", [])
    language = data.get("language", "zh-CN")
    depth = data.get("depth", "intermediate")
    audience = data.get("audience", "相关领域的学习者")

    concept_title = concept.get("title", "")
    concept_summary = concept.get("summary", "")
    concept_content = concept.get("content", "")
    concept_slug = concept.get("slug", "")

    # Parse markdown sections from content
    sections = _parse_markdown_sections(concept_content) if concept_content else {}

    # Extract pedagogical elements from content
    key_points = _extract_key_points(sections)
    examples_from_content = _extract_examples(sections)
    misconceptions = _extract_misconceptions(sections)

    # Score and rank entities
    concept_title_tokens = _tokenize(concept_title)
    concept_summary_keywords = [w.lower() for w in re.findall(r"\w+", concept_summary) if len(w) > 1]
    concept_slug_tokens = _tokenize(concept_slug)

    scored_entities = []
    for entity in entities:
        score = _score_entity(entity, concept_title_tokens, concept_summary_keywords, concept_slug_tokens)
        scored_entities.append((score, entity))
    scored_entities.sort(key=lambda x: x[0], reverse=True)

    # Select top entities (3-5)
    top_count = min(max(3, len(scored_entities)), 5)
    top_entities = scored_entities[:top_count]

    # Classify entities into pedagogical roles
    classified: dict[str, list[dict[str, Any]]] = {
        "Examples": [],
        "Tools": [],
        "Application Scenarios": [],
        "Prerequisites": [],
    }
    for score, entity in top_entities:
        role = _classify_entity(entity, concept_title)
        classified[role].append({
            "slug": entity.get("slug", ""),
            "title": entity.get("title", ""),
            "summary": entity.get("summary", ""),
            "link_type": entity.get("link_type", ""),
            "relevance_score": score,
        })

    # Build learning objectives from concept summary
    learning_objectives: list[str] = []
    if concept_summary:
        sentences = re.split(r"[。！？.!?]", concept_summary)
        learning_objectives = [f"理解{s.strip()}" for s in sentences if s.strip()][:3]
    if not learning_objectives:
        learning_objectives = [f"掌握 {concept_title} 的核心概念"]

    # Build practice tasks from entity examples
    practice_tasks: list[str] = []
    for ent in classified.get("Examples", []):
        practice_tasks.append(f"通过 {ent['title']} 实践 {concept_title} 的应用")
    for ent in classified.get("Application Scenarios", []):
        practice_tasks.append(f"分析 {ent['title']} 在 {concept_title} 中的作用")
    if not practice_tasks and top_entities:
        _, first_ent = top_entities[0]
        practice_tasks.append(f"结合 {first_ent.get('title', '相关实体')} 理解 {concept_title} 的实际应用")

    # Build prerequisites from entity prerequisites
    prerequisites: list[str] = []
    for ent in classified.get("Prerequisites", []):
        prerequisites.append(ent["title"])

    # Build assessment prompts
    assessment_prompts: list[str] = []
    if concept_summary:
        assessment_prompts.append(f"请解释 {concept_title} 的核心定义")
    if key_points:
        assessment_prompts.append(f"请描述 {concept_title} 的工作机制")
    if classified.get("Examples"):
        assessment_prompts.append(f"请举例说明 {concept_title} 的实际应用")

    # Build warnings from misconceptions
    warnings: list[str] = []
    for m in misconceptions:
        warnings.append(f"常见误区：{m}")

    return {
        "concept_slug": concept_slug,
        "title": concept_title,
        "teaching_anchor": concept_summary or concept_title,
        "learning_objectives": learning_objectives,
        "key_points": key_points,
        "examples": examples_from_content,
        "practice_tasks": practice_tasks,
        "prerequisites": prerequisites,
        "misconception_checks": misconceptions,
        "assessment_prompts": assessment_prompts,
        "warnings": warnings,
        "classified_entities": classified,
    }


def build_requirement(design: dict[str, Any], data: dict[str, Any]) -> str:
    """Stage 2: Pedagogical Design JSON → requirement string."""
    concept = data["concept"]
    depth = data.get("depth", "intermediate")
    audience = data.get("audience", "相关领域的学习者")
    language = data.get("language", "zh-CN")

    depth_map = {"beginner": "入门", "intermediate": "中级", "advanced": "高级"}
    depth_cn = depth_map.get(depth, "中级")

    parts: list[str] = []

    # Header
    parts.append(f"基于知识图谱概念「{design['title']}」，为{audience}创建一个{depth_cn}微课堂（micro-classroom）。")
    parts.append("")

    # Teaching anchor
    parts.append(f"教学锚点：{design['teaching_anchor']}")
    parts.append("")

    # Learning objectives
    if design["learning_objectives"]:
        parts.append("学习目标：")
        for obj in design["learning_objectives"]:
            parts.append(f"  - {obj}")
        parts.append("")

    # Key points
    if design["key_points"]:
        parts.append("核心知识点：")
        for kp in design["key_points"]:
            parts.append(f"  - {kp}")
        parts.append("")

    # Classified entities as practice context
    classified = design.get("classified_entities", {})
    entity_sections = []
    for role in ("Examples", "Tools", "Application Scenarios", "Prerequisites"):
        ents = classified.get(role, [])
        if ents:
            role_cn = {
                "Examples": "案例",
                "Tools": "工具",
                "Application Scenarios": "应用场景",
                "Prerequisites": "前置知识",
            }[role]
            ent_descs = [f"{e['title']}" + (f"：{e['summary'][:80]}" if e.get("summary") else "") for e in ents]
            entity_sections.append(f"{role_cn}：{'；'.join(ent_descs)}")

    if entity_sections:
        parts.append("关联实体（实践环节）：")
        for section in entity_sections:
            parts.append(f"  - {section}")
        parts.append("")

    # Practice tasks
    if design["practice_tasks"]:
        parts.append("实践任务：")
        for task in design["practice_tasks"]:
            parts.append(f"  - {task}")
        parts.append("")

    # Misconception checks
    if design["misconception_checks"]:
        parts.append("常见误区检查：")
        for mc in design["misconception_checks"]:
            parts.append(f"  - {mc}")
        parts.append("")

    # Assessment
    if design["assessment_prompts"]:
        parts.append("评估提示：")
        for ap in design["assessment_prompts"]:
            parts.append(f"  - {ap}")
        parts.append("")

    # Language directive
    if language == "zh-CN":
        parts.append("请使用中文生成课程内容。")

    # Concept content fallback
    concept_content = concept.get("content", "")
    if concept_content and len(concept_content) > 200:
        parts.append("")
        parts.append(f"参考内容（前500字）：{concept_content[:500]}")

    return "\n".join(parts)


def process(input_data: dict[str, Any]) -> dict[str, Any]:
    """Main processing: two-stage conversion."""
    concept = input_data.get("concept")
    if not concept:
        return {
            "requirement": "",
            "pedagogical_design": {},
            "metadata": {"error": "Missing 'concept' in input"},
        }

    entities = input_data.get("entities", [])

    # Stage 1: Build pedagogical design
    design = build_pedagogical_design(input_data)

    # Stage 2: Build requirement string
    requirement = build_requirement(design, input_data)

    return {
        "requirement": requirement,
        "pedagogical_design": design,
        "metadata": {
            "concept_slug": concept.get("slug", ""),
            "entity_count": len(entities),
            "depth": input_data.get("depth", "intermediate"),
            "language": input_data.get("language", "zh-CN"),
        },
    }


def main() -> None:
    """Entry point: read from stdin or file, output JSON."""
    import argparse

    parser = argparse.ArgumentParser(description="Concept Graph → OpenMAIC Requirement 转换器")
    parser.add_argument("--file", "-f", help="输入 JSON 文件路径")
    args = parser.parse_args()

    if args.file:
        with open(args.file, "r", encoding="utf-8") as f:
            input_data = json.load(f)
    else:
        input_text = sys.stdin.read()
        if not input_text.strip():
            print(
                "错误: 未提供输入数据。用法:\n"
                '  echo \'{"concept": {...}, "entities": [...]}\' | python concept-to-requirement.py\n'
                "  python concept-to-requirement.py --file input.json",
                file=sys.stderr,
            )
            sys.exit(1)
        try:
            input_data = json.loads(input_text)
        except json.JSONDecodeError as e:
            print(f"错误: 输入 JSON 解析失败: {e}", file=sys.stderr)
            sys.exit(1)

    result = process(input_data)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
