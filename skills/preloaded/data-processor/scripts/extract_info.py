#!/usr/bin/env python3
"""
信息提取脚本 - 从文本中提取结构化信息

提取内容:
- 数字
- 日期
- 百分比
- 金额
- 邮箱
- URL
- 电话号码

用法:
    echo "2024年销售额为100万元，同比增长15%" | python extract_info.py
    
    # 指定提取类型
    echo "联系我: test@example.com 或 13800138000" | python extract_info.py --types email,phone
"""

import sys
import json
import argparse
import re


def extract_numbers(text: str) -> list:
    """提取数字"""
    # 匹配整数和小数
    pattern = r'-?\d+(?:\.\d+)?'
    numbers = re.findall(pattern, text)
    # 转换为数值
    result = []
    for n in numbers:
        try:
            if '.' in n:
                result.append(float(n))
            else:
                result.append(int(n))
        except ValueError:
            result.append(n)
    return result


def extract_dates(text: str) -> list:
    """提取日期"""
    patterns = [
        r'\d{4}[-/年]\d{1,2}[-/月]\d{1,2}[日]?',  # 2024-01-01 或 2024年1月1日
        r'\d{4}[-/年]\d{1,2}[月]?',                 # 2024-01 或 2024年1月
        r'\d{4}年',                                  # 2024年
        r'\d{1,2}[-/月]\d{1,2}[日]?',              # 01-01 或 1月1日
    ]
    
    dates = []
    for pattern in patterns:
        matches = re.findall(pattern, text)
        dates.extend(matches)
    
    return list(set(dates))


def extract_percentages(text: str) -> list:
    """提取百分比"""
    pattern = r'-?\d+(?:\.\d+)?%'
    return re.findall(pattern, text)


def extract_amounts(text: str) -> list:
    """提取金额"""
    patterns = [
        r'[¥$€£]\s*\d+(?:,\d{3})*(?:\.\d+)?',      # ¥100.00
        r'\d+(?:,\d{3})*(?:\.\d+)?\s*[元万亿美金]', # 100万元
        r'\d+(?:\.\d+)?[百千万亿]+[元]?',            # 100万
    ]
    
    amounts = []
    for pattern in patterns:
        matches = re.findall(pattern, text)
        amounts.extend(matches)
    
    return list(set(amounts))


def extract_emails(text: str) -> list:
    """提取邮箱"""
    pattern = r'[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}'
    return re.findall(pattern, text)


def extract_urls(text: str) -> list:
    """提取 URL"""
    pattern = r'https?://[^\s<>"{}|\\^`\[\]]+'
    return re.findall(pattern, text)


def extract_phones(text: str) -> list:
    """提取电话号码"""
    patterns = [
        r'1[3-9]\d{9}',                           # 手机号
        r'\d{3,4}[-\s]?\d{7,8}',                  # 固话
        r'\+\d{1,3}[-\s]?\d{10,12}',             # 国际号码
    ]
    
    phones = []
    for pattern in patterns:
        matches = re.findall(pattern, text)
        phones.extend(matches)
    
    return list(set(phones))


def extract_keywords(text: str, min_len: int = 2) -> list:
    """提取关键词（中文和英文）"""
    # 中文关键词
    chinese_pattern = r'[\u4e00-\u9fa5]{2,}'
    chinese_words = re.findall(chinese_pattern, text)
    
    # 英文关键词
    english_pattern = r'[a-zA-Z]{3,}'
    english_words = re.findall(english_pattern, text)
    
    # 统计词频
    from collections import Counter
    words = chinese_words + [w.lower() for w in english_words]
    
    # 过滤停用词
    stopwords = {'的', '是', '在', '了', '和', '与', '或', '为', '有', '这', '那', '等',
                 'the', 'is', 'are', 'was', 'were', 'and', 'or', 'for', 'with', 'this'}
    words = [w for w in words if w not in stopwords and len(w) >= min_len]
    
    word_freq = Counter(words)
    return [{"word": w, "count": c} for w, c in word_freq.most_common(20)]


def main():
    parser = argparse.ArgumentParser(description="信息提取工具")
    parser.add_argument("--types", "-t", 
                       help="要提取的类型，逗号分隔 (numbers,dates,percentages,amounts,emails,urls,phones,keywords)")
    parser.add_argument("--pretty", "-p", action="store_true", help="格式化输出")
    args = parser.parse_args()
    
    # 读取输入
    try:
        text = sys.stdin.read()
        if not text.strip():
            print(json.dumps({"error": "空输入"}))
            return
    except Exception as e:
        print(json.dumps({"error": f"读取错误: {str(e)}"}))
        return
    
    # 确定要提取的类型
    all_types = ["numbers", "dates", "percentages", "amounts", "emails", "urls", "phones", "keywords"]
    if args.types:
        extract_types = [t.strip().lower() for t in args.types.split(",")]
    else:
        extract_types = all_types
    
    # 提取信息
    result = {
        "success": True,
        "text_length": len(text),
        "extracted": {}
    }
    
    extractors = {
        "numbers": extract_numbers,
        "dates": extract_dates,
        "percentages": extract_percentages,
        "amounts": extract_amounts,
        "emails": extract_emails,
        "urls": extract_urls,
        "phones": extract_phones,
        "keywords": extract_keywords,
    }
    
    for ext_type in extract_types:
        if ext_type in extractors:
            try:
                extracted = extractors[ext_type](text)
                if extracted:
                    result["extracted"][ext_type] = extracted
            except Exception as e:
                result["extracted"][ext_type] = {"error": str(e)}
    
    # 统计
    result["summary"] = {
        "total_extractions": sum(len(v) if isinstance(v, list) else 0 
                                  for v in result["extracted"].values()),
        "types_found": list(result["extracted"].keys())
    }
    
    # 输出
    indent = 2 if args.pretty else None
    print(json.dumps(result, ensure_ascii=False, indent=indent))


if __name__ == "__main__":
    main()
