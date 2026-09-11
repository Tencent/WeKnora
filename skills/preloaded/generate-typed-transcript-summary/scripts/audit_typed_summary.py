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
        "一、学习目标、适用对象与前置知识",
        "二、培训内容体系",
        "三、核心知识要点与原文金句",
        "四、方法、操作步骤与判断标准",
        "五、案例、工具使用与互动问答",
        "六、练习、自测与应用清单",
    ],
    "salon": [
        "一、活动与参与者",
        "二、议题与观点",
        "三、观点交锋",
        "四、案例与问答",
        "五、共识与分歧",
        "六、探索方向",
    ],
    "meeting": [
        "一、会议总结",
        "二、会议基本信息",
        "三、关键议题和共识",
        "四、会议分歧点",
        "五、会议讨论详情",
        "六、待办事项",
        "七、遗留和搁置议题",
        "八、其他",
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
    summary_path = Path(sys.argv[2])
    text = summary_path.read_text(encoding="utf-8")
    primary_type = profile.get("primary_type", "general")
    expected = TEMPLATES.get(primary_type, TEMPLATES["general"])
    errors = []
    if summary_path.suffix.lower() == ".json":
        try:
            summary = json.loads(text)
        except json.JSONDecodeError as error:
            print("未通过\n- 总结不是合法 JSON：%s" % error)
            return 1
        headings = [section.get("title") for section in summary.get("sections", [])]
        profile_card = summary.get("orchestrationProfile")
        if not isinstance(profile_card, dict):
            errors.append("缺少 orchestrationProfile")
        else:
            units = profile_card.get("topicUnits")
            if profile_card.get("schemaVersion") != 1 or not isinstance(units, list) or not 1 <= len(units) <= 5:
                errors.append("orchestrationProfile 版本或主题单元数量不合法")
            else:
                blocks = {block.get("id"): block for section in summary.get("sections", []) for block in section.get("blocks", [])}
                block_ids = set(blocks)
                allowed_forms = {"skill_method", "tool_operation", "concept_cognition", "case_analysis", "humanities_reflection", "process_standard"}
                abstract_size = 0
                for index, unit in enumerate(units, start=1):
                    abstract_size += len(unit.get("abstract", ""))
                    if not unit.get("title") or not unit.get("abstract") or not unit.get("learningOutcomes"):
                        errors.append(f"主题单元 {index} 缺少必填字段")
                    if not set(unit.get("contentForms", [])) <= allowed_forms:
                        errors.append(f"主题单元 {index} 包含未知 contentForms")
                    if not set(unit.get("summaryBlockIds", [])) <= block_ids:
                        errors.append(f"主题单元 {index} 引用了不存在的 summaryBlockId")
                    block_evidence = {evidence_id for block_id in unit.get("summaryBlockIds", []) for evidence_id in blocks.get(block_id, {}).get("evidenceChunkIds", [])}
                    if not set(unit.get("evidenceChunkIds", [])) <= block_evidence:
                        errors.append(f"主题单元 {index} 的证据未闭合到正文 block")
                if abstract_size > 500:
                    errors.append("主题摘要总量超过 500 个汉字")
    else:
        headings = re.findall(r"^## (.+?)\s*$", text, flags=re.M)
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
