# 分块调试 API

[返回目录](./README.md)

分块调试接口用于在知识库编辑器的调试面板中预览分块效果，无需写入数据库或生成 Embedding。

## 说明

- 所有接口需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- 文本上限为 64k 字符，防止单次请求占用过多 CPU。

## 端点一览

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| POST | `/chunker/preview` | 对提交的文本运行自适应分块器并返回预览结果 |

---

## POST `/chunker/preview` - 预览分块结果

对提交的文本运行自适应分块器，返回分块列表及诊断信息（所选策略、策略链、拒绝的策略、文档画像、统计摘要）。

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `text` | string | 是 | 待分块的文本，最大 64k 字符 |
| `chunking_config` | object | 是 | 分块配置 |
| `chunking_config.chunk_size` | int | 否 | 分块大小（字符数） |
| `chunking_config.chunk_overlap` | int | 否 | 分块重叠（字符数） |
| `chunking_config.separators` | string[] | 否 | 分隔符列表 |
| `chunking_config.strategy` | string | 否 | 分块策略（空字符串表示自适应） |
| `chunking_config.token_limit` | int | 否 | Token 上限 |
| `chunking_config.languages` | string[] | 否 | 目标语言列表 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/chunker/preview' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "text": "## 概述\n\nWeKnora 是一个企业级 LLM 驱动的知识框架...",
    "chunking_config": {
        "chunk_size": 512,
        "chunk_overlap": 64,
        "separators": ["\n\n", "\n", "。", "."],
        "strategy": "",
        "token_limit": 0,
        "languages": ["zh"]
    }
}'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "selected_tier": "markdown",
        "tier_chain": [
            {"name": "markdown", "priority": 1},
            {"name": "semantic", "priority": 2},
            {"name": "fixed_size", "priority": 3}
        ],
        "rejected": [
            {"tier": "semantic", "reason": "文本未达到语义分块的最低要求"}
        ],
        "profile": {
            "total_chars": 1024,
            "detected_langs": ["zh"],
            "is_structured": true
        },
        "chunks": [
            {
                "seq": 1,
                "start": 0,
                "end": 256,
                "size_chars": 256,
                "size_tokens_approx": 170,
                "context_header": "## 概述",
                "content": "## 概述\n\nWeKnora 是一个企业级 LLM 驱动的知识框架..."
            }
        ],
        "stats": {
            "count": 4,
            "avg_chars": 256,
            "min_chars": 200,
            "max_chars": 320,
            "stddev_chars": 45
        }
    }
}
```

**错误**:
- 文本为空 → 400 `text is empty`
- 文本超过 64k → 413 `text exceeds preview limit`
- 分块超时 → 504 `chunker preview timed out`

### 响应字段说明

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `selected_tier` | string | 实际使用的分块策略名称 |
| `tier_chain` | array | 策略链（按优先级排序） |
| `rejected` | array | 被拒绝的策略及原因 |
| `profile` | object | 文档画像（字符数、语言检测、是否结构化） |
| `chunks` | array | 分块结果列表 |
| `chunks[].seq` | int | 分块序号（从 1 开始） |
| `chunks[].start` | int | 在原文中的起始字节偏移 |
| `chunks[].end` | int | 在原文中的结束字节偏移 |
| `chunks[].size_chars` | int | 分块字符数 |
| `chunks[].size_tokens_approx` | int | 近似 Token 数 |
| `chunks[].context_header` | string | 上下文标题（Markdown 标题） |
| `chunks[].content` | string | 分块内容 |
| `stats.count` | int | 总分块数 |
| `stats.avg_chars` | int | 平均分块字符数 |
| `stats.min_chars` | int | 最小分块字符数 |
| `stats.max_chars` | int | 最大分块字符数 |
| `stats.stddev_chars` | int | 分块字符数标准差 |
| `stats.truncated_to` | int | 若超过 500 个分块，标记原始分块数 |
