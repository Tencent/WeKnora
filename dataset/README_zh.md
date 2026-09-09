# QA数据集采样工具

一个全面的QA数据集采样工具，使用OpenAI的GPT模型生成答案。该工具帮助您从大规模数据集（如MS MARCO）创建高质量的问答数据集。

## 功能特性

- **智能采样**：智能地从大型数据集中采样查询、文档和相关性判断
- **答案生成**：使用OpenAI的GPT模型自动生成高质量答案
- **断点续传**：支持中断后继续生成，从上次位置开始
- **进度跟踪**：实时进度更新和统计信息
- **结果可视化**：易于阅读的问答对展示，包含完整上下文

## 安装指南

### 系统要求

- Python 3.7+
- OpenAI API密钥

### 安装依赖

```bash
pip install pandas pyarrow openai
```

### 设置环境变量

```bash
export OPENAI_API_KEY="你的openai-api-key"
# 可选：使用自定义OpenAI端点
export OPENAI_BASE_URL="https://api.openai.com/v1"
```

### 准备数据集

您可以使用任何符合格式要求的QA数据集，或下载预处理好的样本：

**使用HuggingFace/ModelScope样本**
我们提供了来自流行QA数据集的预处理样本：
- MarkrAI/eli5_sample_autorag
- MarkrAI/msmarco_sample_autorag
- MarkrAI/triviaqa_sample_autorag
- gnekt/hotpotqa_small_sample_autorag

**使用您自己的数据集**
确保您的数据集包含以下文件：
- `queries.parquet`（列：id, text）
- `corpus.parquet`（列：id, text）
- `qrels.parquet`（列：qid, pid）

## 快速开始

### 1. 从大型数据集采样

首先，从完整数据集中采样查询、文档和相关性判断的子集：

```bash
python dataset/qa_dataset.py sample \
  --queries ~/dataset/mmarco-queries.parquet \
  --corpus ~/dataset/mmarco-corpus.parquet \
  --qrels ~/dataset/mmarco-qrels.parquet \
  --nq 100 \
  --output_dir ./dataset/samples
```

### 2. 生成答案

使用OpenAI的GPT模型为采样的问答生成答案：

```bash
python dataset/qa_dataset.py generate \
  --input_dir ./dataset/samples \
  --output_dir ./dataset/samples
```

### 3. 查看结果

展示生成的问答对及其上下文：

```bash
python dataset/qa_dataset.py show \
  --input_dir ./dataset/samples \
  -n 5
```

## 详细使用说明

### 采样命令

从完整数据集中创建代表性样本。

```bash
python dataset/qa_dataset.py sample [选项]
```

**必需参数：**
- `--queries`：查询parquet文件路径（列：`id`, `text`）
- `--corpus`：语料库parquet文件路径（列：`id`, `text`）
- `--qrels`：相关性判断parquet文件路径（列：`qid`, `pid`）

**可选参数：**
- `--nq`：要采样的查询数量（默认：1000）
- `--output_dir`：采样数据输出目录（默认：./save）

**示例：**
```bash
python dataset/qa_dataset.py sample \
  --queries data/queries.parquet \
  --corpus data/corpus.parquet \
  --qrels data/qrels.parquet \
  --nq 500 \
  --output_dir ./my_sample
```

### 生成命令

使用OpenAI API为采样问题生成答案。

```bash
python dataset/qa_dataset.py generate [选项]
```

**必需参数：**
- `--input_dir`：包含采样数据的目录（queries.parquet, corpus.parquet, qrels.parquet）

**可选参数：**
- `--output_dir`：生成答案的输出目录（默认：./save）

**特性：**
- **断点续传**：中断后自动从上次位置继续
- **错误处理**：API调用失败自动重试3次
- **进度保存**：每成功生成一个答案就保存进度

**示例：**
```bash
python dataset/qa_dataset.py generate \
  --input_dir ./my_sample \
  --output_dir ./my_sample
```

### 展示命令

展示生成的问答对及完整上下文。

```bash
python dataset/qa_dataset.py show [选项]
```

**必需参数：**
- `--input_dir`：包含QA数据的目录（queries.parquet, corpus.parquet, qrels.parquet, qas.parquet, answers.parquet）

**可选参数：**
- `-n`：要展示的结果数量（默认：5）

**示例：**
```bash
python dataset/qa_dataset.py show \
  --input_dir ./my_sample \
  -n 3
```

## 输入数据格式

### 数据集目录和清单

内置 `dataset_id=default` 读取 `dataset/samples/`。自定义数据集 ID（例如
`support-zh-v1`）读取 `dataset/support-zh-v1/`；可通过
`WEKNORA_EVALUATION_DATASET_DIR` 修改数据集根目录。ID 只允许 1–64 个字母、数字、
点、下划线或连字符，不能使用路径分隔符。

每个可评测目录必须额外包含 `manifest.json`：

```json
{
  "schema_version": 1,
  "id": "support-zh-v1",
  "name": "中文客服问答集 v1",
  "description": "覆盖退款、物流和账户问题",
  "language": "zh-CN",
  "scenario": "customer-support",
  "source": "内部脱敏工单",
  "license": "internal-evaluation-only",
  "created_at": "2026-09-06",
  "coverage_dimensions": ["single-hop", "multi-hop", "no-answer"]
}
```

`schema_version`、`id` 和非空 `name` 为必填项，且清单 ID 必须与请求的
`dataset_id` 完全一致。其他字段用于说明来源和覆盖范围。服务还会校验 Parquet
字段类型、空文本、重复 ID、悬空关系，以及每个问题是否同时具备相关段落和答案；
校验失败时不会启动后台评测或消耗模型额度。

### 查询文件 (queries.parquet)
| 列名 | 类型 | 描述 |
|------|------|------|
| id | int64 | 唯一查询标识符 |
| text | string | 实际的问题文本 |

### 语料库文件 (corpus.parquet)
| 列名 | 类型 | 描述 |
|------|------|------|
| id | int64 | 唯一段落/文档标识符 |
| text | string | 段落/文档内容 |

### 相关性判断文件 (qrels.parquet)
| 列名 | 类型 | 描述 |
|------|------|------|
| qid | int64 | 查询ID（匹配queries.id） |
| pid | int64 | 段落ID（匹配corpus.id） |

### 答案文件 (answers.parquet)
| 列名 | 类型 | 描述 |
|------|------|------|
| id | int64 | 唯一答案标识符 |
| text | string | 参考答案文本 |

### 问答映射文件 (qas.parquet)
| 列名 | 类型 | 描述 |
|------|------|------|
| qid | int64 | 查询ID（匹配queries.id） |
| aid | int64 | 答案ID（匹配answers.id） |

## 输出文件

运行所有命令后，输出目录将包含：

### 采样数据
- `queries.parquet`：采样的查询子集
- `corpus.parquet`：采样的文档子集
- `qrels.parquet`：采样的相关性判断

### 生成的答案
- `answers.parquet`：生成的答案（含唯一ID）
- `qas.parquet`：问答映射（qid → aid）

## 高级用法

### 自定义OpenAI配置

您可以使用不同的OpenAI模型或端点：

```bash
# 使用GPT-4 Turbo
export OPENAI_API_KEY="你的密钥"
python dataset/qa_dataset.py generate --input_dir ./samples

# 使用Azure OpenAI
export OPENAI_API_KEY="azure密钥"
export OPENAI_BASE_URL="https://你的资源.openai.azure.com/openai/deployments/gpt-4"
python dataset/qa_dataset.py generate --input_dir ./samples
```

### 大型数据集采样

对于非常大的数据集，建议分批采样：

```bash
# 第一批
python dataset/qa_dataset.py sample --nq 1000 --output_dir ./batch1
python dataset/qa_dataset.py generate --input_dir ./batch1

# 第二批
python dataset/qa_dataset.py sample --nq 1000 --output_dir ./batch2
python dataset/qa_dataset.py generate --input_dir ./batch2
```

## 故障排除

### 常见问题

**1. OpenAI API错误**
- 确保API密钥设置正确：`echo $OPENAI_API_KEY`
- 检查API配额和账单状态
- 验证与OpenAI的网络连接

**2. 大数据集内存问题**
- 减小`--nq`参数以获得更小的样本
- 确保pandas操作有足够的RAM
- 考虑使用更小的parquet文件

**3. 文件未找到错误**
- 验证所有输入文件路径是否正确
- 确保parquet文件有正确的列名
- 检查文件权限

### 调试模式

通过添加打印语句或使用Python调试器启用详细输出：

```bash
python -m pdb dataset/qa_dataset.py sample --queries ...
```

## 示例工作流

```bash
# 1. 设置环境
export OPENAI_API_KEY="sk-..."

# 2. 从MS MARCO采样200个查询
python dataset/qa_dataset.py sample \
  --queries ~/mmarco/queries.parquet \
  --corpus ~/mmarco/corpus.parquet \
  --qrels ~/mmarco/qrels.parquet \
  --nq 200 \
  --output_dir ./marco_sample

# 3. 生成答案（根据API速率限制可能需要一些时间）
python dataset/qa_dataset.py generate \
  --input_dir ./marco_sample \
  --output_dir ./marco_sample

# 4. 查看结果
python dataset/qa_dataset.py show \
  --input_dir ./marco_sample \
  -n 10
```

## 贡献

欢迎提交问题和功能增强请求！

## 许可证

MIT许可证 - 可自由用于研究和项目。
