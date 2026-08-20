#!/usr/bin/env python3
"""检查类型化总结是否严格遵守主类型模板。"""

import json
import re
import sys
from pathlib import Path


TEMPLATES = {
    "interview": [
        "一、人物背景",
        "二、经历与决策",
        "三、核心观点",
        "四、原则与思维模型",
        "五、案例与证据",
        "六、反思与边界",
    ],
    "training": [
        "一、目标与受众",
        "二、知识地图",
        "三、核心概念",
        "四、方法与步骤",
        "五、示例与异常",
        "六、练习与应用",
    ],
    "salon": [
        "一、活动与参与者",
        "二、议题与观点",
        "三、观点交锋",
        "四、案例与问答",
        "五、共识与分歧",
        "六、探索方向",
    ],
    "general": [
        "一、定位与问题",
        "二、主张与论证",
        "三、证据与案例",
        "四、限定与反方",
        "五、影响与建议",
    ],
}


def main():
    if len(sys.argv) != 3:
        print("用法：audit_typed_summary.py PROFILE.json SUMMARY.md", file=sys.stderr)
        return 2
    profile = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
    text = Path(sys.argv[2]).read_text(encoding="utf-8")
    primary_type = profile.get("primary_type", "general")
    expected = TEMPLATES.get(primary_type, TEMPLATES["general"])
    headings = re.findall(r"^## (.+?)\s*$", text, flags=re.M)
    errors = []
    if headings != expected:
        errors.append(f"二级标题不符合 {primary_type} 模板")
        errors.append(f"期望：{expected}")
        errors.append(f"实际：{headings}")
    if errors:
        print("未通过")
        print("\n".join(f"- {error}" for error in errors))
        return 1
    print(f"通过：{primary_type} 模板，{len(expected)} 个标准章节")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
