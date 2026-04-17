#!/usr/bin/env python3
"""
RAG 检索结果 → OpenMAIC Requirement 转换器

将 WeKnora RAG 检索结果转换为结构化的 OpenMAIC 课程生成需求描述。
此脚本仅做数据转换，不涉及网络调用。

用法:
  echo '{"chunks": [...], "audience": "初学者"}' | python scripts/rag-to-requirement.py
  python scripts/rag-to-requirement.py --file results.json

输入格式 (JSON):
{
  "chunks": [
    {
      "document_name": "文档A.pdf",
      "content": "文档内容片段...",
      "metadata": {"page": 5, "section": "第三章"}
    }
  ],
  "query": "用户原始查询（可选）",
  "audience": "目标受众描述（可选，默认'相关领域的学习者'）",
  "depth": "教学深度: beginner|intermediate|advanced（可选，默认'intermediate'）",
  "language": "zh-CN|en-US（可选，默认'zh-CN'）",
  "focus_areas": ["重点领域1", "重点领域2"]  # 可选
}

输出格式 (JSON):
{
  "requirement": "结构化的教学需求描述",
  "metadata": {
    "source_documents": ["文档A.pdf", "文档B.pdf"],
    "total_chunks": 5,
    "audience": "初学者",
    "depth": "intermediate",
    "language": "zh-CN"
  }
}
"""

import json
import sys
from typing import Any


def extract_key_topics(chunks: list[dict], max_topics: int = 5) -> list[str]:
    """从文档块中提取关键主题（基于内容摘要和 section 信息）。"""
    topics = []
    seen = set()

    for chunk in chunks:
        metadata = chunk.get("metadata", {})
        # 优先使用 section/chapter 信息
        for key in ("section", "chapter", "heading", "title"):
            if key in metadata and metadata[key] not in seen:
                topics.append(metadata[key])
                seen.add(metadata[key])
                if len(topics) >= max_topics:
                    return topics

    # 如果 section 信息不足，从内容前 50 字提取
    for chunk in chunks:
        content = chunk.get("content", "")[:50].strip()
        if content and content not in seen:
            topics.append(content + "...")
            seen.add(content)
            if len(topics) >= max_topics:
                break

    return topics


def build_requirement(data: dict[str, Any]) -> str:
    """将 RAG 结果构建为 OpenMAIC requirement 字符串。"""
    chunks = data.get("chunks", [])
    query = data.get("query", "")
    audience = data.get("audience", "相关领域的学习者")
    depth = data.get("depth", "intermediate")
    language = data.get("language", "zh-CN")
    focus_areas = data.get("focus_areas", [])

    depth_map = {
        "beginner": "入门",
        "intermediate": "中级",
        "advanced": "高级",
    }
    depth_cn = depth_map.get(depth, "中级")

    # 提取文档名称
    source_docs = list({c.get("document_name", "未知文档") for c in chunks})

    # 提取关键主题
    key_topics = extract_key_topics(chunks)

    # 构建 requirement
    parts = []

    # 开头：基于什么内容，为谁创建什么课程
    if query:
        parts.append(f"基于以下知识库内容，为{audience}创建一个{depth_cn}课程。")
        parts.append(f"用户原始需求：{query}")
    else:
        parts.append(f"基于以下知识库内容，为{audience}创建一个{depth_cn}课程。")

    # 内容来源
    if source_docs:
        docs_str = "、".join(source_docs[:5])
        if len(source_docs) > 5:
            docs_str += f" 等{len(source_docs)}个文档"
        parts.append(f"\n内容来源：{docs_str}")

    # 关键主题
    if key_topics:
        parts.append("\n核心主题：")
        for i, topic in enumerate(key_topics, 1):
            parts.append(f"  {i}. {topic}")

    # 重点领域
    if focus_areas:
        parts.append("\n重点覆盖：")
        for area in focus_areas:
            parts.append(f"  - {area}")

    # 语言要求
    if language == "zh-CN":
        parts.append("\n请使用中文生成课程内容。")

    return "\n".join(parts)


def process(input_data: dict[str, Any]) -> dict[str, Any]:
    """主处理函数。"""
    chunks = input_data.get("chunks", [])
    if not chunks:
        return {
            "requirement": input_data.get("query", ""),
            "metadata": {
                "source_documents": [],
                "total_chunks": 0,
                "error": "未提供检索结果，使用原始查询作为 requirement",
            },
        }

    requirement = build_requirement(input_data)

    source_docs = list({c.get("document_name", "未知文档") for c in chunks})

    return {
        "requirement": requirement,
        "metadata": {
            "source_documents": source_docs,
            "total_chunks": len(chunks),
            "audience": input_data.get("audience", "相关领域的学习者"),
            "depth": input_data.get("depth", "intermediate"),
            "language": input_data.get("language", "zh-CN"),
        },
    }


def main() -> None:
    """入口：从 stdin 或文件读取输入，输出 JSON 结果。"""
    import argparse

    parser = argparse.ArgumentParser(description="RAG 结果 → OpenMAIC Requirement 转换器")
    parser.add_argument("--file", "-f", help="输入 JSON 文件路径")
    args = parser.parse_args()

    # 读取输入
    if args.file:
        with open(args.file, "r", encoding="utf-8") as f:
            input_data = json.load(f)
    else:
        input_text = sys.stdin.read()
        if not input_text.strip():
            print(
                "错误: 未提供输入数据。用法:\n"
                "  echo '{\"chunks\": [...]}' | python rag-to-requirement.py\n"
                "  python rag-to-requirement.py --file input.json",
                file=sys.stderr,
            )
            sys.exit(1)
        try:
            input_data = json.loads(input_text)
        except json.JSONDecodeError as e:
            print(f"错误: 输入 JSON 解析失败: {e}", file=sys.stderr)
            sys.exit(1)

    # 处理并输出
    result = process(input_data)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
