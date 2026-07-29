# WeKnora Wiki Evaluation Harness

> 详细运行手册见：[RUN_MANUAL.md](./RUN_MANUAL.md)；迁移到新环境见：[MIGRATION_GUIDE_CN.md](./MIGRATION_GUIDE_CN.md)。

这个目录提供第一版 WeKnora Wiki 端到端评测体系，目标是验证：

- wiki 页面生成覆盖度
- 事实覆盖度和基础溯源信号
- wiki 链接图 / graph 的节点、边覆盖度
- wiki search 的 Recall@k / MRR
- 可选 QA 的答案命中率
- 可选 v2 增量更新后的新事实覆盖和旧事实残留

## 目录

```text
wiki_eval/
  eval_weknora.py
  tools/validate_stardust_corpus.py
  config.example.yaml
  requirements.txt
  datasets/stardust/
    docs_v1/             # 初始高关联 synthetic 文档集
    docs_v2/             # 增量更新文档
    gold/                # entities / relations / facts / qa / search_cases / update_events / delete_events
  reports/               # 脚本运行后生成
```

## 默认假设

- WeKnora 服务地址：`http://localhost:8080`
- API 前缀：`/api/v1`
- 认证优先读取环境变量：
  - `WEKNORA_TOKEN` -> `Authorization: Bearer <token>`
  - `WEKNORA_API_KEY` -> 默认 `X-API-Key: <key>`
- 第一版导入方式：manual knowledge API
- 默认创建新的测试知识库，不自动删除
- 默认不接 DeepEval / Ragas / LLM judge

## 安装依赖

```bash
cd /home/liusz10/wiki/WeKnora/wiki_eval   # 如果你复制到了 WSL WeKnora 目录
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
```

Windows 当前仓库中也可以：

```powershell
cd D:\rag\3-rag\wiki_eval
python -m pip install -r requirements.txt
```

## 最小运行

```bash
export WEKNORA_BASE_URL=http://localhost:8080
export WEKNORA_TOKEN='你的 JWT 或 Bearer token'
python eval_weknora.py
```

如果使用 API Key：

```bash
export WEKNORA_BASE_URL=http://localhost:8080
export WEKNORA_API_KEY='你的 API key'
export WEKNORA_API_KEY_HEADER='X-API-Key'
python eval_weknora.py
```

## 只评估已有 KB

```bash
export WEKNORA_KB_ID='existing-kb-id'
python eval_weknora.py --skip-ingest --existing-kb-id "$WEKNORA_KB_ID"
```

## 运行 QA 和增量更新

QA 部分依赖你部署的 `/sessions` 与 `/knowledge-chat/{session_id}` 请求体兼容默认模板；如果不兼容，在 `config.yaml` 中覆盖 payload 模板。

```bash
python eval_weknora.py --run-qa
python eval_weknora.py --run-update
python eval_weknora.py --run-qa --run-update
python eval_weknora.py --run-delete
python eval_weknora.py --run-qa --run-update --run-delete
```

## 数据集一致性校验

扩展 `datasets/stardust` 前后，建议先跑一次结构校验：

```bash
python wiki_eval/tools/validate_stardust_corpus.py
```

严格模式会把 warning 也当作失败，适合 CI 或提交前检查：

```bash
python wiki_eval/tools/validate_stardust_corpus.py --strict
```

## 主要输出

每次运行生成：

```text
reports/run_YYYYmmdd_HHMMSS/
  raw/                 # 所有关键 API 原始响应
  metrics.json         # 结构化指标与失败明细
  report.md            # 人类可读摘要报告
```

核心指标示例：

- `actual_page_count`
- `entity_slug_recall`
- `entity_name_coverage`
- `duplicate_like_entity_rate`
- `fact_global_term_coverage`
- `fact_expected_page_term_coverage`
- `relation_text_coverage`
- `graph_node_recall_heuristic`
- `graph_edge_recall_heuristic`
- `wiki_search_recall@1`
- `wiki_search_recall@3`
- `wiki_search_recall@5`
- `wiki_search_mrr`
- `qa_answer_contains`，仅 `--run-qa` 时出现
- `update_new_fact_term_coverage`，仅 `--run-update` 后的报告出现
- `update_stale_term_absence`，仅 `--run-update` 后的报告出现

## 注意

1. 第一版主要是确定性回归评测，不是最终人工质量评测。
2. `entity_slug_recall` 可能受 WeKnora 实际 slug 策略影响；因此同时提供 `entity_name_coverage`。
3. `graph_edge_recall_heuristic` 是启发式，具体字段需要结合你的 WeKnora graph API 响应再进一步适配。
4. QA 默认关闭，因为不同 WeKnora 部署的会话和 chat payload 可能不同。
5. 脚本不会默认删除测试 KB，避免误删数据。

## 推荐后续增强

- 接入 DeepEval 或 Ragas 做 faithfulness / answer relevancy。
- 扩展 docs_del，并把删除正文残留、陈旧 in/out links 与合理重写纳入回归。
- 针对实际 `/wiki/graph` 响应结构优化边匹配。
- 增加文件上传模式，覆盖 docreader 链路。
- 适配 HotpotQA / MultiHop-RAG / MuSiQue 子集。





