# WeKnora Stardust 评测运行手册

> 面向接手该评测系统的 agent / 开发者。目标是在 WSL 中快速复跑、快速定位报告，并能理解每个指标的含义。

## 1. 这个系统做什么

`wiki_eval/eval_weknora.py` 串起了一条完整的 Stardust 回归评测链路：

- 创建 KB，并注入必要的模型 ID
- 导入 Stardust 基线文档，生成 wiki 页面
- 评测页面、实体、事实、关系、搜索结果
- 评测增量更新，检查是否有遗漏或陈旧内容残留
- 评测 delete / retract 回收链路
- 保存 raw payload、汇总指标和 Markdown 报告，便于回归对比

当前系统已经覆盖：

- 建 KB + 手工入库 + wiki 生成
- wiki 页面 / 实体 / 事实 / 关系 / 搜索 的确定性评测
- 增量更新遗漏检查
- delete regression 的 3 个正式案例
- 版本对 harness 的回收一致性验证

## 2. 你会用到的文件

- `wiki_eval/eval_weknora.py`：主入口脚本
- `wiki_eval/config.example.yaml`：配置样例
- `wiki_eval/RUN_MANUAL.md`：运行手册
- `wiki_eval/EVAL_SPEC.md`：评测规格说明
- `wiki_eval/tools/validate_stardust_corpus.py`：语料校验脚本
- `wiki_eval/datasets/stardust/`：Stardust 评测语料
- `wiki_eval/reports/`：每次运行生成的报告目录
- `weknora_eval_harness/`：版本对照 / 环境辅助 harness

## 3. 运行前准备

### 3.1 环境

- 推荐在 WSL 里运行
- WeKnora 服务可访问，默认地址是 `http://127.0.0.1:8080`
- 已准备好 Python3 环境
- 如需复现真实联调，建议先确认 `weknora_eval_harness/.env` 可用

### 3.2 必要环境变量

创建 KB 时，需要这些模型 ID 注入：

- `WEKNORA_EMBEDDING_MODEL_ID`
- `WEKNORA_SUMMARY_MODEL_ID`
- `WEKNORA_WIKI_SYNTHESIS_MODEL_ID`（可选）

这些值通常从 harness 的 `.env` 或当前部署环境中加载。

### 3.3 先校验语料

建议先跑严格校验，确保数据集是自洽的：

```bash
python3 wiki_eval/tools/validate_stardust_corpus.py --dataset wiki_eval/datasets/stardust --strict
```

预期结果：

- `errors: 0`
- `warnings: 0`

如果这里失败，先修复语料，再跑评测。

## 4. 快速开始

下面所有命令默认都在项目根目录执行。

### 4.1 加载 harness 环境并跑基线

```bash
set -a
source /mnt/d/rag/3-rag/weknora_eval_harness/.env
set +a
cd /mnt/d/rag/3-rag
python3 wiki_eval/eval_weknora.py --base-url http://127.0.0.1:8080 --api-prefix /api/v1
```

### 4.2 只跑更新评测

```bash
python3 wiki_eval/eval_weknora.py --base-url http://127.0.0.1:8080 --api-prefix /api/v1 --run-update
```

### 4.3 只跑 delete 评测

```bash
python3 wiki_eval/eval_weknora.py --base-url http://127.0.0.1:8080 --api-prefix /api/v1 --run-delete
```

### 4.4 跑整套评测

```bash
python3 wiki_eval/eval_weknora.py --run-qa --run-update --run-delete
```

> 如果你只关心当前最核心的回归路径，优先看：
>
> 1. baseline 主流程
> 2. update
> 3. delete

## 5. 产物在哪里

每次运行会在 `wiki_eval/reports/run_<timestamp>/` 下生成一套结果。

常见文件：

- `report.md`：主报告，便于人读
- `raw/`：原始 API 返回
- `raw/wiki_graph.json`：图谱原始数据
- `raw/update/`：更新相关 raw 结果
- `raw/delete/`：delete / retract 相关 raw 结果
- `raw/search/`：搜索相关 raw 结果
- `raw/qa/`：QA 相关 raw 结果

看报告时，建议先看 `report.md`，再按需回到 `raw/` 查证具体 payload。

## 6. 指标速查

### 6.1 页面 / 实体 / 事实 / 关系

| 指标 | 含义 |
|---|---|
| `actual_page_count` | 实际生成的 wiki 页面数量 |
| `entity_name_coverage` | gold 实体名称覆盖率 |
| `entity_slug_recall` | gold 实体 slug 召回率 |
| `fact_global_term_coverage` | 全局事实术语覆盖率 |
| `fact_expected_page_term_coverage` | 期望页面上的事实术语覆盖率 |
| `relation_text_coverage` | 关系文本覆盖率 |

### 6.2 graph 指标

| 指标 | 含义 |
|---|---|
| `graph_node_recall_heuristic` | 图谱节点召回的启发式指标 |
| `graph_edge_recall_heuristic` | 图谱边召回的启发式指标 |

`graph_edge_recall_heuristic` 不是严格 truth metric，而是趋势型指标。当前实现优先级如下：

1. 优先使用真实 graph payload 中的 `source / target` 边
2. 通过 gold 实体候选匹配 `id / name / aliases / expected_slug`
3. 如果没有直接端点边，再用 `evidence_docs` 命中的 summary bridge 节点兜底

这类指标适合看回归趋势，不建议当作绝对真值。

### 6.3 delete 指标

| 指标 | 含义 |
|---|---|
| `delete_source_ref_cleanup_rate` | 被删源文档相关引用的清理率 |
| `delete_must_remove_source_refs_rate` | 必须移除的 source refs 去除率 |
| `delete_must_remove_in_links_rate` | 必须移除的入链去除率 |
| `delete_must_remove_terms_absence_rate` | 必须消失的术语缺失率 |
| `delete_expected_deleted_page_absence_rate` | 被删页面应消失的满足率 |
| `delete_false_del_rate` | 误删率 |
| `delete_keep_page_presence_rate` | 应保留页面的存在率 |
| `delete_keep_page_unchanged_rate` | 应保留页面的未变率 |
| `delete_stale_inlink_count` | 陈旧入链数量 |
| `delete_stale_inlink_page_rate` | 含陈旧入链页面比例 |
| `delete_idempotent_retract` | 重复 retract 是否幂等 |

当前 harness 已经验证过 `idempotent_retract = True`。

## 7. delete case 设计

当前有 3 个正式 delete case：

| case | target doc | title | 主要关注点 |
|---|---|---|---|
| `d001` | `doc05_borealis_incident.md` | `Borealis Station Incident Report` | 基线 retract + 源引用清理 |
| `d002` | `doc08_aurora_beacon_notes.md` | `Aurora Beacon Calibration Notes` | 源文档删除后的关联清理 |
| `d003` | `doc06_review_board_minutes.md` | `Celestial Review Board Minutes` | 会议纪要类文档的残留回收 |

这些 case 的内容需要和下面几处同步维护：

1. `wiki_eval/datasets/stardust/docs_del/`
2. `wiki_eval/datasets/stardust/gold/delete_events.json`
3. `wiki_eval/datasets/stardust/stardust-seed-graph.json`
4. `wiki_eval/datasets/stardust/stardust-seed-graph.md`

新增 case 时，最好同时更新这四处，再跑一次严格校验。

## 8. 推荐的复跑顺序

如果你想快速确认系统没有回归，建议按下面顺序跑：

1. 校验语料
2. baseline 全流程
3. update 评测
4. delete 评测
5. 对照报告中的 raw payload

最常用的单步命令就是：

```bash
python3 wiki_eval/eval_weknora.py --base-url http://127.0.0.1:8080 --api-prefix /api/v1
python3 wiki_eval/eval_weknora.py --base-url http://127.0.0.1:8080 --api-prefix /api/v1 --run-update
python3 wiki_eval/eval_weknora.py --base-url http://127.0.0.1:8080 --api-prefix /api/v1 --run-delete
```

## 9. 常见问题

### 9.1 服务不可达

先确认 WeKnora 服务真的在 `http://127.0.0.1:8080`，以及 `--api-prefix /api/v1` 是否正确。

### 9.2 语料校验失败

优先检查：

- `docs_del/` 里的 delete 文档是否存在
- `delete_events.json` 是否和文档一一对应
- `stardust-seed-graph.json` / `.md` 是否同步更新

### 9.3 graph edge 指标偏低

优先查看 `raw/wiki_graph.json`，再看 `evidence_docs` 和 bridge 节点是否能对上。

### 9.4 模型 ID 未注入

确认这些环境变量已经加载：

- `WEKNORA_EMBEDDING_MODEL_ID`
- `WEKNORA_SUMMARY_MODEL_ID`
- `WEKNORA_WIKI_SYNTHESIS_MODEL_ID`

## 10. 给接手 agent 的建议

如果你是后续接手这个系统的 agent，建议先读这三个东西：

1. `wiki_eval/RUN_MANUAL.md`
2. `wiki_eval/EVAL_SPEC.md`
3. 最近一次 `wiki_eval/reports/run_*/report.md`

然后再按需看 raw payload。

维护原则：

- 指标尽量稳定、可复现
- 新增 case 时，数据、gold、图谱描述一起改
- graph edge 这类指标保持为趋势信号，不要轻易伪装成严格真值
- delete regression 优先扩“能暴露问题”的真实案例
