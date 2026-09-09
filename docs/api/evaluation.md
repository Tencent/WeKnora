# 评估功能 API

[返回目录](./README.md)

| 方法 | 路径                      | 描述                             |
| ---- | ------------------------- | -------------------------------- |
| GET  | `/evaluation/`            | 获取评估任务结果                  |
| POST | `/evaluation/`            | 创建评估任务                      |
| GET  | `/evaluation/datasets`    | 获取可选择的数据集及就绪状态       |
| GET  | `/evaluation/model-usage` | 按模型和时间范围获取租户模型用量   |

> 注：服务端路由带尾斜杠（Gin 会自动从 `/evaluation` 重定向到 `/evaluation/`），下方示例为方便阅读用了 `/evaluation`。

## GET `/evaluation` - 获取评估任务结果

**参数说明（查询参数）**:

| 字段     | 类型   | 必填 | 说明                                                |
| -------- | ------ | ---- | --------------------------------------------------- |
| task_id  | string | 是   | 从 `POST /evaluation` 返回的任务 ID                  |

**请求**:

```bash
curl --location 'http://localhost:8080/api/v1/evaluation?task_id=c34563ad-b09f-4858-b72e-e92beb80becb' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "task": {
            "id": "c34563ad-b09f-4858-b72e-e92beb80becb",
            "tenant_id": 1,
            "dataset_id": "default",
            "start_time": "2025-08-12T14:54:26.221804768+08:00",
            "status": 2,
            "total": 1,
            "finished": 1
        },
        "params": {
            "session_id": "",
            "knowledge_base_id": "2ef57434-8c8d-4442-b967-2f7fc578a2fc",
            "vector_threshold": 0.5,
            "keyword_threshold": 0.3,
            "embedding_top_k": 10,
            "vector_database": "",
            "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
            "rerank_top_k": 5,
            "rerank_threshold": 0.7,
            "chat_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "summary_config": {
                "max_tokens": 0,
                "repeat_penalty": 1,
                "top_k": 0,
                "top_p": 0,
                "frequency_penalty": 0,
                "presence_penalty": 0,
                "prompt": "这是用户和助手之间的对话。",
                "context_template": "你是一个专业的智能信息检索助手",
                "no_match_prefix": "<think>\n</think>\nNO_MATCH",
                "temperature": 0.3,
                "seed": 0,
                "max_completion_tokens": 2048
            },
            "fallback_strategy": "",
            "fallback_response": "抱歉，我无法回答这个问题。"
        },
        "metric": {
            "retrieval_metrics": {
                "precision": 0,
                "recall": 0,
                "ndcg3": 0,
                "ndcg10": 0,
                "mrr": 0,
                "map": 0
            },
            "generation_metrics": {
                "bleu1": 0.037656734016532384,
                "bleu2": 0.04067392145167686,
                "bleu4": 0.048963321289052536,
                "rouge1": 0,
                "rouge2": 0,
                "rougel": 0
            }
        }
    },
    "success": true
}
```

## GET `/evaluation/datasets` - 获取评测数据集

该接口读取数据集根目录中的 `manifest.json`，返回名称、语言、场景、来源和覆盖维度，
供评测页面选择。`available=false` 表示 manifest 或五个必需 Parquet 文件不完整；
发起评测时还会再次执行字段类型、重复 ID 和 qid/pid/aid 关系校验，失败后不会调用模型。

```bash
curl --location 'http://localhost:8080/api/v1/evaluation/datasets' \
  --header 'X-API-Key: sk-xxxxx'
```

```json
{
  "success": true,
  "data": [
    {
      "schema_version": 1,
      "id": "default",
      "name": "WeKnora built-in QA sample",
      "language": "mixed",
      "scenario": "general-rag",
      "coverage_dimensions": ["retrieval", "reranking", "answer-generation"],
      "available": true
    }
  ]
}
```

自定义 `dataset_id` 对应 `<WEKNORA_EVALUATION_DATASET_DIR>/<dataset_id>/`；内置
`default` 为兼容原目录结构，映射到 `<根目录>/samples/`。完整 manifest 和五个 Parquet
Schema 见 [`dataset/README_zh.md`](../../dataset/README_zh.md)。

## GET `/evaluation/model-usage` - 获取模型用量

该接口聚合当前租户在评测、普通问答、Wiki 和后台任务中的模型调用。数据来自
`evaluation_model_calls` 表，只包含模型、用途、Token、缓存、费用、耗时和成功状态，
不保存也不返回 Prompt 或响应正文。

**参数说明（查询参数）**:

| 字段         | 类型   | 必填 | 说明                       |
| ------------ | ------ | ---- | -------------------------- |
| `start_time` | string | 否   | RFC3339 开始时间，包含边界   |
| `end_time`   | string | 否   | RFC3339 结束时间，包含边界   |

开始时间晚于结束时间或时间格式无效时返回 400。两个参数都省略时查询当前租户保留期内
的全部记录。

**请求**:

```bash
curl --location \
  'http://localhost:8080/api/v1/evaluation/model-usage?start_time=2026-08-01T00:00:00Z&end_time=2026-09-01T00:00:00Z' \
  --header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
  "success": true,
  "data": [
    {
      "model_id": "model-uuid",
      "model_name": "qwen3",
      "model_type": "KnowledgeQA",
      "usage": {
        "call_count": 12,
        "successful_calls": 12,
        "failed_calls": 0,
        "prompt_tokens": 8400,
        "completion_tokens": 1200,
        "total_tokens": 9600,
        "cache_read_tokens": 3200,
        "cache_write_tokens": 0,
        "cache_miss_tokens": 5200,
        "cache_reported_calls": 12,
        "cache_hit_calls": 7,
        "cache_hit_rate": 0.3809523809,
        "model_duration_ms": 18400,
        "average_model_latency_ms": 1533.3333333,
        "priced_calls": 12,
        "unpriced_calls": 0,
        "cost_by_currency": {
          "USD": 0.042
        }
      }
    }
  ]
}
```

## POST `/evaluation` - 创建评估任务

**参数说明（请求体）**:

| 字段              | 类型   | 必填 | 说明                                            |
| ----------------- | ------ | ---- | ----------------------------------------------- |
| dataset_id        | string | 否   | `GET /evaluation/datasets` 返回的数据集 ID；默认 `default` |
| knowledge_base_id | string | 是   | 评估使用的知识库 ID                              |
| chat_id           | string | 是   | 评估使用的对话模型 ID                            |
| rerank_id         | string | 是   | 评估使用的重排序模型 ID                          |

**请求**:

```bash
curl --location 'http://localhost:8080/api/v1/evaluation' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "dataset_id": "default",
    "knowledge_base_id": "kb-00000001",
    "chat_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
    "rerank_id": "b30171a1-787b-426e-a293-735cd5ac16c0"
}'
```

**响应**:

```json
{
    "data": {
        "task": {
            "id": "c34563ad-b09f-4858-b72e-e92beb80becb",
            "tenant_id": 1,
            "dataset_id": "default",
            "start_time": "2025-08-12T14:54:26.221804768+08:00",
            "status": 1
        },
        "params": {
            "session_id": "",
            "knowledge_base_id": "2ef57434-8c8d-4442-b967-2f7fc578a2fc",
            "vector_threshold": 0.5,
            "keyword_threshold": 0.3,
            "embedding_top_k": 10,
            "vector_database": "",
            "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
            "rerank_top_k": 5,
            "rerank_threshold": 0.7,
            "chat_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "summary_config": {
                "max_tokens": 0,
                "repeat_penalty": 1,
                "top_k": 0,
                "top_p": 0,
                "frequency_penalty": 0,
                "presence_penalty": 0,
                "prompt": "这是用户和助手之间的对话。",
                "context_template": "你是一个专业的智能信息检索助手，xxx",
                "no_match_prefix": "<think>\n</think>\nNO_MATCH",
                "temperature": 0.3,
                "seed": 0,
                "max_completion_tokens": 2048
            },
            "fallback_strategy": "",
            "fallback_response": "抱歉，我无法回答这个问题。"
        }
    },
    "success": true
}
```
