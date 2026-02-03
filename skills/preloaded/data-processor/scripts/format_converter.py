#!/usr/bin/env python3
"""
格式转换脚本 - JSON/CSV/Markdown 相互转换

用法:
    # JSON 转 CSV
    echo '[{"name": "A", "value": 1}]' | python format_converter.py --to csv
    
    # JSON 转 Markdown 表格
    echo '[{"name": "A", "value": 1}]' | python format_converter.py --to markdown
    
    # CSV 转 JSON
    cat data.csv | python format_converter.py --from csv --to json
"""

import sys
import json
import argparse
import csv
import io


def json_to_csv(data: list) -> str:
    """将 JSON 列表转换为 CSV"""
    if not data:
        return ""
    
    if not all(isinstance(x, dict) for x in data):
        raise ValueError("JSON 数据必须是字典列表")
    
    # 获取所有字段
    fieldnames = []
    for item in data:
        for key in item.keys():
            if key not in fieldnames:
                fieldnames.append(key)
    
    output = io.StringIO()
    writer = csv.DictWriter(output, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(data)
    
    return output.getvalue()


def csv_to_json(csv_text: str) -> list:
    """将 CSV 转换为 JSON 列表"""
    reader = csv.DictReader(io.StringIO(csv_text))
    return list(reader)


def json_to_markdown(data: list) -> str:
    """将 JSON 列表转换为 Markdown 表格"""
    if not data:
        return ""
    
    if not all(isinstance(x, dict) for x in data):
        raise ValueError("JSON 数据必须是字典列表")
    
    # 获取所有字段
    fieldnames = []
    for item in data:
        for key in item.keys():
            if key not in fieldnames:
                fieldnames.append(key)
    
    # 构建表头
    lines = []
    lines.append("| " + " | ".join(fieldnames) + " |")
    lines.append("| " + " | ".join(["---"] * len(fieldnames)) + " |")
    
    # 构建数据行
    for item in data:
        row = []
        for field in fieldnames:
            value = item.get(field, "")
            # 转义 Markdown 特殊字符
            str_value = str(value) if value is not None else ""
            str_value = str_value.replace("|", "\\|")
            row.append(str_value)
        lines.append("| " + " | ".join(row) + " |")
    
    return "\n".join(lines)


def markdown_to_json(md_text: str) -> list:
    """将 Markdown 表格转换为 JSON 列表"""
    lines = [line.strip() for line in md_text.strip().split("\n") if line.strip()]
    
    if len(lines) < 2:
        raise ValueError("无效的 Markdown 表格")
    
    # 解析表头
    header_line = lines[0]
    if not header_line.startswith("|"):
        raise ValueError("无效的 Markdown 表格格式")
    
    headers = [h.strip() for h in header_line.strip("|").split("|")]
    
    # 跳过分隔行
    data_lines = lines[2:] if len(lines) > 2 else []
    
    # 解析数据
    result = []
    for line in data_lines:
        if not line.startswith("|"):
            continue
        values = [v.strip() for v in line.strip("|").split("|")]
        item = {}
        for i, header in enumerate(headers):
            if i < len(values):
                item[header] = values[i]
        result.append(item)
    
    return result


def detect_format(text: str) -> str:
    """自动检测输入格式"""
    text = text.strip()
    
    if text.startswith("[") or text.startswith("{"):
        return "json"
    elif text.startswith("|"):
        return "markdown"
    elif "," in text.split("\n")[0]:
        return "csv"
    else:
        return "unknown"


def main():
    parser = argparse.ArgumentParser(description="数据格式转换工具")
    parser.add_argument("--from", "-f", dest="from_format", 
                       choices=["json", "csv", "markdown", "auto"],
                       default="auto", help="输入格式")
    parser.add_argument("--to", "-t", dest="to_format",
                       choices=["json", "csv", "markdown"],
                       required=True, help="输出格式")
    parser.add_argument("--pretty", "-p", action="store_true", help="格式化输出")
    args = parser.parse_args()
    
    # 读取输入
    try:
        raw_input = sys.stdin.read()
        if not raw_input.strip():
            print(json.dumps({"error": "空输入"}))
            return
    except Exception as e:
        print(json.dumps({"error": f"读取错误: {str(e)}"}))
        return
    
    # 检测输入格式
    from_format = args.from_format
    if from_format == "auto":
        from_format = detect_format(raw_input)
        if from_format == "unknown":
            print(json.dumps({"error": "无法自动检测输入格式"}))
            return
    
    # 转换为中间格式（JSON 列表）
    try:
        if from_format == "json":
            data = json.loads(raw_input)
            if isinstance(data, dict):
                # 尝试提取列表
                if "items" in data:
                    data = data["items"]
                elif "data" in data:
                    data = data["data"]
                else:
                    data = [data]
            if not isinstance(data, list):
                data = [data]
        elif from_format == "csv":
            data = csv_to_json(raw_input)
        elif from_format == "markdown":
            data = markdown_to_json(raw_input)
        else:
            print(json.dumps({"error": f"不支持的输入格式: {from_format}"}))
            return
    except Exception as e:
        print(json.dumps({"error": f"解析输入失败: {str(e)}"}))
        return
    
    # 转换为目标格式
    try:
        if args.to_format == "json":
            indent = 2 if args.pretty else None
            output = json.dumps(data, ensure_ascii=False, indent=indent)
        elif args.to_format == "csv":
            output = json_to_csv(data)
        elif args.to_format == "markdown":
            output = json_to_markdown(data)
        else:
            print(json.dumps({"error": f"不支持的输出格式: {args.to_format}"}))
            return
        
        print(output)
    except Exception as e:
        print(json.dumps({"error": f"转换失败: {str(e)}"}))
        return


if __name__ == "__main__":
    main()
