#!/usr/bin/env python3
"""检查快速概览是否只包含一段 150～300 个汉字的总结。"""

import re
import sys
from pathlib import Path


def main():
    if len(sys.argv) != 2:
        print("用法：audit_overview.py OVERVIEW.md", file=sys.stderr)
        return 2
    text = Path(sys.argv[1]).read_text(encoding="utf-8")
    match = re.search(r"^## 快速概览\s*$\n(.*)\Z", text, flags=re.M | re.S)
    errors = []
    if not match:
        errors.append("缺少 ## 快速概览，或其后存在额外二级区块")
        body = ""
    else:
        body = match.group(1).strip()
    paragraphs = [part.strip() for part in re.split(r"\n\s*\n", body) if part.strip()]
    if len(paragraphs) != 1:
        errors.append(f"快速概览正文共 {len(paragraphs)} 段，应只有 1 段")
    if re.search(r"^#{2,6}\s|^[-*]\s", body, flags=re.M):
        errors.append("快速概览不应包含子标题或列表")
    han_count = len(re.findall(r"[\u3400-\u4dbf\u4e00-\u9fff]", body))
    if not 150 <= han_count <= 300:
        errors.append(f"快速概览包含 {han_count} 个汉字，应为 150～300 个")
    for forbidden in ("关键收获", "内容质量提示", "审计警示"):
        if forbidden in text:
            errors.append(f"快速概览不应包含：{forbidden}")
    if errors:
        print("未通过")
        print("\n".join(f"- {error}" for error in errors))
        return 1
    print(f"通过：快速概览为 1 段，包含 {han_count} 个汉字")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
