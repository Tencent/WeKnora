---
name: 数据处理器
description: 数据处理与分析技能。当用户需要对知识库检索结果进行数据分析、统计计算、格式转换、数据提取或生成报告时使用此技能。支持 Python 脚本执行进行高级数据处理。
---

# Data Processor

企业级知识库数据处理与分析技能，用于处理 RAG 检索结果和执行数据分析任务。

## 核心能力

1. **数据分析**: 对检索到的文档数据进行统计分析
2. **格式转换**: JSON/CSV/Markdown 等格式相互转换
3. **数据提取**: 从非结构化文本中提取结构化信息
4. **报告生成**: 生成数据分析报告和摘要

## 使用场景

当用户请求涉及以下内容时，使用此技能：
- "分析这些数据"、"统计一下"、"计算总数/平均值"
- "转换为 JSON/CSV 格式"
- "提取关键信息"、"整理成表格"
- "生成报告"、"数据汇总"

## 可用脚本

### 1. analyze.py - 数据分析脚本

分析输入的 JSON 数据，生成统计报告。

**命令行用法** (仅供参考):
```bash
# 通过 stdin 传入 JSON 数据
echo '{"items": [1, 2, 3, 4, 5]}' | python scripts/analyze.py

# 或传入文件路径（需要文件实际存在）
python scripts/analyze.py --file data.json
```

**使用 execute_skill_script 工具时**:
- 如果你有内存中的数据（如 JSON 字符串），使用 `input` 参数传入，不要使用 `args`
- `--file` 参数仅用于读取技能目录中已存在的文件，不适用于传递内存数据

```json
// ✅ 正确：通过 input 传入数据
{
  "skill_name": "数据处理器",
  "script_path": "scripts/analyze.py",
  "input": "{\"items\": [1, 2, 3], \"query\": \"统计分析\"}"
}

// ❌ 错误：--file 需要文件路径，不能单独使用
{
  "skill_name": "数据处理器",
  "script_path": "scripts/analyze.py",
  "args": ["--file"],
  "input": "{...}"
}
```

**输入格式**:
```json
{
  "items": [数据项数组],
  "query": "可选的查询描述"
}
```

**输出**: JSON 格式的统计结果，包含计数、求和、平均值等。

### 2. format_converter.py - 格式转换脚本

在 JSON、CSV、Markdown 表格之间转换数据。

**用法**:
```bash
# JSON 转 CSV
echo '[{"name": "A", "value": 1}]' | python scripts/format_converter.py --to csv

# JSON 转 Markdown 表格
echo '[{"name": "A", "value": 1}]' | python scripts/format_converter.py --to markdown

# CSV 转 JSON
echo 'name,value\nA,1' | python scripts/format_converter.py --from csv --to json
```

### 3. extract_info.py - 信息提取脚本

从文本中提取结构化信息（数字、日期、关键词等）。

**用法**:
```bash
echo "2024年销售额为100万元，同比增长15%" | python scripts/extract_info.py
```

**输出**:
```json
{
  "numbers": ["100", "15"],
  "dates": ["2024年"],
  "percentages": ["15%"],
  "amounts": ["100万元"]
}
```

## 处理流程

### 分析 RAG 检索结果

当需要分析知识库检索结果时：

1. 收集检索到的文档片段
2. 提取关键数据点
3. 使用 `analyze.py` 进行统计
4. 整理并呈现分析结果

**示例**：
```
用户: "帮我统计知识库中提到的所有产品销售数据"

步骤:
1. 使用 knowledge_search 检索相关文档
2. 整理数据为 JSON 格式
3. 调用 execute_skill_script:
   - skill_name: "data-processor"
   - script_path: "scripts/analyze.py"
   - 通过 stdin 传入数据
4. 解析输出并生成报告
```

### 数据格式转换

当用户需要特定格式输出时：

1. 整理数据为标准 JSON 格式
2. 使用 `format_converter.py` 转换
3. 返回目标格式结果

## 最佳实践

1. **数据预处理**: 调用脚本前，确保数据格式正确
2. **错误处理**: 检查脚本执行结果，处理异常情况
3. **结果验证**: 验证输出结果的合理性
4. **渐进处理**: 大数据量时分批处理

## 输出格式

分析结果示例：
```markdown
## 数据分析报告

### 基本统计
- 数据条数: 50
- 数值总和: 1,234,567
- 平均值: 24,691.34
- 最大值: 99,999
- 最小值: 100

### 分布情况
| 区间 | 数量 | 占比 |
|------|------|------|
| 0-1000 | 10 | 20% |
| 1000-10000 | 25 | 50% |
| >10000 | 15 | 30% |

### 结论
根据数据分析，XXX...
```

## 注意事项

- 脚本在 Docker 沙箱中执行，确保安全隔离
- 执行超时默认为 60 秒
- 输入数据大小有限制，大文件请分批处理
- 脚本输出为 JSON 格式，便于后续处理
