#!/usr/bin/env python3
"""
数据分析脚本 - 用于分析 RAG 检索结果和知识库数据

支持功能:
- 基本统计（计数、求和、平均值、最大/最小值）
- 数值分布分析
- 文本统计（词频、字符数）

用法:
    # 通过 stdin 传入 JSON 数据
    echo '{"items": [1, 2, 3, 4, 5]}' | python analyze.py
    
    # 通过参数传入文件
    python analyze.py --file data.json
    
    # 指定分析类型
    echo '{"items": [1, 2, 3]}' | python analyze.py --type numeric
"""

import sys
import json
import argparse
from collections import Counter


def analyze_numeric(data: list) -> dict:
    """分析数值数据"""
    if not data:
        return {"error": "空数据集"}
    
    # 过滤出数值
    numbers = [x for x in data if isinstance(x, (int, float))]
    if not numbers:
        return {"error": "无有效数值数据"}
    
    numbers.sort()
    n = len(numbers)
    
    result = {
        "count": n,
        "sum": sum(numbers),
        "mean": sum(numbers) / n,
        "min": min(numbers),
        "max": max(numbers),
        "median": numbers[n // 2] if n % 2 == 1 else (numbers[n // 2 - 1] + numbers[n // 2]) / 2,
    }
    
    # 计算标准差
    mean = result["mean"]
    variance = sum((x - mean) ** 2 for x in numbers) / n
    result["std_dev"] = variance ** 0.5
    
    # 分布统计
    if n >= 5:
        result["quartiles"] = {
            "q1": numbers[n // 4],
            "q2": result["median"],
            "q3": numbers[3 * n // 4]
        }
    
    return result


def analyze_text(data: list) -> dict:
    """分析文本数据"""
    if not data:
        return {"error": "空数据集"}
    
    texts = [str(x) for x in data if x]
    
    # 基本统计
    total_chars = sum(len(t) for t in texts)
    total_words = sum(len(t.split()) for t in texts)
    
    # 词频统计（简单分词）
    all_words = []
    for text in texts:
        words = text.split()
        all_words.extend(w.strip('.,!?;:""\'()[]{}') for w in words if w.strip())
    
    word_freq = Counter(all_words)
    
    result = {
        "count": len(texts),
        "total_chars": total_chars,
        "total_words": total_words,
        "avg_chars_per_item": total_chars / len(texts) if texts else 0,
        "avg_words_per_item": total_words / len(texts) if texts else 0,
        "top_words": dict(word_freq.most_common(10)),
        "unique_words": len(word_freq)
    }
    
    return result


def analyze_mixed(data: list) -> dict:
    """分析混合数据"""
    if not data:
        return {"error": "空数据集"}
    
    # 类型统计
    type_counts = Counter(type(x).__name__ for x in data)
    
    result = {
        "total_items": len(data),
        "type_distribution": dict(type_counts),
    }
    
    # 分别分析数值和文本
    numbers = [x for x in data if isinstance(x, (int, float))]
    texts = [x for x in data if isinstance(x, str)]
    
    if numbers:
        result["numeric_analysis"] = analyze_numeric(numbers)
    if texts:
        result["text_analysis"] = analyze_text(texts)
    
    return result


def analyze_dict_list(data: list) -> dict:
    """分析字典列表（如数据库查询结果）"""
    if not data:
        return {"error": "空数据集"}
    
    if not all(isinstance(x, dict) for x in data):
        return {"error": "数据格式不正确，需要字典列表"}
    
    result = {
        "record_count": len(data),
        "fields": {},
    }
    
    # 获取所有字段
    all_keys = set()
    for item in data:
        all_keys.update(item.keys())
    
    # 分析每个字段
    for key in all_keys:
        values = [item.get(key) for item in data if key in item]
        
        # 判断字段类型
        non_null_values = [v for v in values if v is not None]
        if not non_null_values:
            result["fields"][key] = {"type": "all_null", "null_count": len(values)}
            continue
        
        sample = non_null_values[0]
        if isinstance(sample, (int, float)):
            field_analysis = analyze_numeric(non_null_values)
            field_analysis["type"] = "numeric"
        elif isinstance(sample, str):
            field_analysis = analyze_text(non_null_values)
            field_analysis["type"] = "text"
        else:
            field_analysis = {"type": type(sample).__name__, "count": len(non_null_values)}
        
        field_analysis["null_count"] = len(values) - len(non_null_values)
        result["fields"][key] = field_analysis
    
    return result


def main():
    parser = argparse.ArgumentParser(description="数据分析工具")
    parser.add_argument("--file", "-f", help="输入文件路径")
    parser.add_argument("--type", "-t", choices=["numeric", "text", "mixed", "auto"],
                       default="auto", help="分析类型")
    parser.add_argument("--pretty", "-p", action="store_true", help="格式化输出")
    args = parser.parse_args()
    
    # 读取输入
    try:
        if args.file:
            with open(args.file, 'r', encoding='utf-8') as f:
                raw_data = f.read()
        else:
            raw_data = sys.stdin.read()
        
        if not raw_data.strip():
            print(json.dumps({"error": "空输入"}))
            return
        
        data = json.loads(raw_data)
    except json.JSONDecodeError as e:
        print(json.dumps({"error": f"JSON 解析错误: {str(e)}"}))
        return
    except FileNotFoundError:
        print(json.dumps({"error": f"文件未找到: {args.file}"}))
        return
    except Exception as e:
        print(json.dumps({"error": f"读取错误: {str(e)}"}))
        return
    
    # 提取数据
    items = None
    if isinstance(data, dict):
        if "items" in data:
            items = data["items"]
        elif "data" in data:
            items = data["data"]
        elif "results" in data:
            items = data["results"]
        else:
            # 假设整个 dict 是单条记录，包装成列表
            items = [data]
    elif isinstance(data, list):
        items = data
    else:
        print(json.dumps({"error": "不支持的数据格式，需要列表或包含 items/data/results 的字典"}))
        return
    
    # 根据类型分析
    if args.type == "auto":
        # 自动检测
        if items and all(isinstance(x, dict) for x in items):
            result = analyze_dict_list(items)
        elif items and all(isinstance(x, (int, float)) for x in items):
            result = analyze_numeric(items)
        elif items and all(isinstance(x, str) for x in items):
            result = analyze_text(items)
        else:
            result = analyze_mixed(items)
    elif args.type == "numeric":
        result = analyze_numeric(items)
    elif args.type == "text":
        result = analyze_text(items)
    else:
        result = analyze_mixed(items)
    
    # 添加元信息
    output = {
        "success": True,
        "analysis": result,
        "metadata": {
            "input_type": type(data).__name__,
            "item_count": len(items) if items else 0,
            "analysis_type": args.type
        }
    }
    
    # 输出
    indent = 2 if args.pretty else None
    print(json.dumps(output, ensure_ascii=False, indent=indent))


if __name__ == "__main__":
    main()
