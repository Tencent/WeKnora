# RAG 评估 API

[返回目录](./README.md)

Evaluation V2 将数据集、样本、运行和逐样本结果持久化。所有资源均按当前租户隔离。

## 指标目录

`GET /api/v1/evaluation/metrics`

返回指标的稳定名称、版本、类别、方向和输入要求。首版支持 `precision`、`recall`、`ndcg3`、`ndcg10`、`mrr`、`map`、`bleu1`、`bleu2`、`bleu4`、`rouge1`、`rouge2`、`rougel`，版本均为 `v1`。

## 数据集与样本

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/POST | `/api/v1/evaluation/datasets` | 列表、创建数据集 |
| GET/PUT/DELETE | `/api/v1/evaluation/datasets/:dataset_id` | 详情、更新、软删除 |
| GET/POST | `/api/v1/evaluation/datasets/:dataset_id/samples` | 样本分页、创建 |
| PUT/DELETE | `/api/v1/evaluation/datasets/:dataset_id/samples/:sample_id` | 更新、软删除样本 |

创建样本：

```json
{
  "question": "WeKnora 如何进行混合检索？",
  "reference_answer": "同时执行向量和关键词检索后合并结果。",
  "reference_contexts": [
    { "text": "参考文本", "knowledge_id": "可选", "chunk_id": "可选" }
  ]
}
```

检索指标优先按非空 `chunk_id` 匹配；任一侧缺少 `chunk_id` 时，回退到文本精确匹配。文本规范化仅包含去除首尾空白、统一换行符和将连续空白折叠为单个空格，保留大小写和单词边界。

## 评估运行

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/POST | `/api/v1/evaluation/runs` | 运行列表、创建运行 |
| GET | `/api/v1/evaluation/runs/:run_id` | 运行详情及聚合指标 |
| GET | `/api/v1/evaluation/runs/:run_id/results` | 逐样本结果分页 |

创建运行：

```json
{
  "dataset_id": "dataset-id",
  "knowledge_base_id": "kb-id",
  "chat_model_id": "chat-model-id",
  "rerank_model_id": "rerank-model-id",
  "vector_threshold": 0.15,
  "keyword_threshold": 0.3,
  "embedding_top_k": 50,
  "rerank_top_k": 10,
  "rerank_threshold": 0.2,
  "metrics": [
    { "name": "precision", "version": "v1" },
    { "name": "rougel", "version": "v1" }
  ]
}
```

运行创建时冻结全部样本输入和配置。历史详情、对比及兼容接口均读取快照，不使用当前全局配置重建。

`metric_scores` 以指标名为 key：

```json
{
  "precision": {
    "name": "precision",
    "version": "v1",
    "category": "retrieval",
    "score": 0.8,
    "status": "scored",
    "higher_is_better": true,
    "reason": "",
    "error": ""
  }
}
```

状态为 `scored`、`not_applicable`、`failed` 或 `skipped`。只有 `scored` 参与运行级平均和运行对比，其他状态不按零分处理。聚合结果附带 `scored_sample_count` 和 `total_sample_count`。

## 运行对比

`GET /api/v1/evaluation/comparisons?baseline_run_id=...&candidate_run_id=...`

仅允许比较同一数据集的两个运行；仅比较双方均为 `scored` 的同名同版本指标。响应包含平均绝对差值、改善方向、可比较样本数和逐样本差值，不进行统计显著性判断。

## V1 兼容接口（已弃用）

- `POST /api/v1/evaluation/` 接受 `dataset_id`、`knowledge_base_id`、`chat_id`、`rerank_id`，创建 V2 运行并返回 `{ task, params }`。`dataset_id=default` 读取内置 parquet 数据集。
- `GET /api/v1/evaluation/?task_id=...` 将 `task_id` 作为 V2 `run_id`，返回 `{ task, params, metric }`。固定旧指标中无法投影的字段使用零值。

兼容接口没有独立执行引擎。新调用方应使用 V2 API。
