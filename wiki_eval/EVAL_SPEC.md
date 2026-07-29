# WeKnora Stardust 评测规格说明

本文档定义 `wiki_eval/eval_weknora.py` 这套 Stardust 评测系统的范围、数据契约、执行流程和指标定义。它的目标不是写一个泛化的评测平台，而是把 WeKnora 当前最关键的回归路径固定下来，便于后续 agent 快速理解、复跑和扩展。

## 1. 目标

这套评测系统主要回答四个问题：

1. KB 创建和 wiki 生成链路是否能稳定跑通
2. 页面、实体、事实、关系、搜索是否符合预期
3. 增量更新是否存在遗漏或陈旧内容残留
4. delete / retract 是否能正确清理所有应该清理的内容

## 2. 评测范围

### 2.1 纳入范围

- 创建 KB
- 手工入库 / baseline wiki 生成
- 页面、实体、事实、关系、搜索
- 增量更新
- delete / retract
- 版本对 harness 的幂等性回收验证

### 2.2 不纳入范围

- 生产流量下的在线质量监控
- 跨语料、跨项目的通用 benchmark 排名
- 依赖人工主观打分的开放式评测
- 与当前 corpus 无关的外部数据集比较

## 3. 输入契约

### 3.1 运行参数

主入口：

- `wiki_eval/eval_weknora.py`

常用参数：

- `--base-url`：WeKnora 服务地址
- `--api-prefix`：API 前缀，默认使用 `/api/v1`
- `--run-update`：只执行更新回归
- `--run-delete`：只执行删除回归
- `--run-qa`：执行 QA / 搜索相关链路

### 3.2 环境变量

创建 KB 时必须能读到：

- `WEKNORA_EMBEDDING_MODEL_ID`
- `WEKNORA_SUMMARY_MODEL_ID`
- `WEKNORA_WIKI_SYNTHESIS_MODEL_ID`（可选）

### 3.3 语料目录

当前 Stardust corpus 在：

- `wiki_eval/datasets/stardust/`

其中最重要的子项是：

- `docs/`：基线文档
- `docs_del/`：delete / retract 文档
- `gold/delete_events.json`：delete gold 定义
- `stardust-seed-graph.json`：种子图谱定义
- `stardust-seed-graph.md`：种子图谱的人类可读版本

## 4. 执行流程

评测系统的主流程可抽象为：

1. 读取环境变量和 corpus
2. 校验 corpus 一致性
3. 创建 KB
4. 导入基线文档
5. 生成 wiki 页面与图谱
6. 采集页面、实体、事实、关系、搜索结果
7. 运行增量更新回归
8. 运行 delete / retract 回归
9. 汇总指标并生成 `report.md`
10. 保存 raw payload 供复查

## 5. 指标定义

### 5.1 页面、实体、事实、关系

#### `actual_page_count`

实际生成的 wiki 页面数量。

#### `entity_name_coverage`

gold 实体名称是否被页面或图谱中的实体实体化覆盖。

#### `entity_slug_recall`

gold 实体 slug 的召回率。

#### `fact_global_term_coverage`

全局事实术语的覆盖情况。

#### `fact_expected_page_term_coverage`

预期页面上的事实术语是否被覆盖。

#### `relation_text_coverage`

关系文本是否出现在生成结果中。

### 5.2 graph 指标

#### `graph_node_recall_heuristic`

图谱节点召回的启发式估计。它用于观测图谱构建趋势，不应被解释成严格真值。

#### `graph_edge_recall_heuristic`

图谱边召回的启发式估计。当前版本的策略是：

1. 优先使用真实 graph payload 的 `source / target` 边
2. 通过 gold 实体候选匹配 `id / name / aliases / expected_slug`
3. 若没有直接端点边，则利用 `evidence_docs` 命中的 summary bridge 节点兜底

这个指标的定位是：

- 看趋势
- 看回归
- 看边是否越来越接近真实语义连接

它不是严格 truth metric。

### 5.3 update 指标

更新回归重点看：

- 新内容是否进入正确页面
- 老内容是否仍然保留
- 是否出现“更新后遗漏”

常见观察项包括：

- 新事实术语覆盖率
- 陈旧术语是否消失
- 受影响页面是否真的被更新

### 5.4 delete 指标

#### `delete_source_ref_cleanup_rate`

删除源文档后，相关 source refs 是否被清理。

#### `delete_must_remove_source_refs_rate`

必须移除的 source refs 是否真的被移除。

#### `delete_must_remove_in_links_rate`

必须移除的入链是否真的被移除。

#### `delete_must_remove_terms_absence_rate`

必须消失的术语是否真的不再出现。

#### `delete_expected_deleted_page_absence_rate`

预期应消失的页面是否真的消失。

#### `delete_false_del_rate`

误删比例。

#### `delete_keep_page_presence_rate`

不应删除的页面是否仍然存在。

#### `delete_keep_page_unchanged_rate`

不应变化的页面是否保持不变。

#### `delete_stale_inlink_count`

删除后仍残留的陈旧入链数量。

#### `delete_stale_inlink_page_rate`

含有陈旧入链的页面比例。

#### `delete_idempotent_retract`

对同一删除事件重复执行 retract 是否幂等。

当前 harness 已经确认：

- `idempotent_retract = True`

## 6. 当前 delete case 设计

当前 corpus 中有 3 个正式 delete case：

| case | target doc | title | 关注点 |
|---|---|---|---|
| `d001` | `doc05_borealis_incident.md` | `Borealis Station Incident Report` | 基线 delete / retract，验证源引用清理 |
| `d002` | `doc08_aurora_beacon_notes.md` | `Aurora Beacon Calibration Notes` | 验证删除后关联是否彻底清理 |
| `d003` | `doc06_review_board_minutes.md` | `Celestial Review Board Minutes` | 验证会议纪要类文档的回收和残留清理 |

这三个 case 的维护必须和以下文件同步：

- `wiki_eval/datasets/stardust/docs_del/`
- `wiki_eval/datasets/stardust/gold/delete_events.json`
- `wiki_eval/datasets/stardust/stardust-seed-graph.json`
- `wiki_eval/datasets/stardust/stardust-seed-graph.md`

## 7. 报告输出契约

每次运行应该输出：

- 一个 `report.md`
- 一组 raw payload
- 一组可对照的中间产物

典型目录结构：

- `wiki_eval/reports/run_<timestamp>/report.md`
- `wiki_eval/reports/run_<timestamp>/raw/`
- `wiki_eval/reports/run_<timestamp>/raw/wiki_graph.json`
- `wiki_eval/reports/run_<timestamp>/raw/update/`
- `wiki_eval/reports/run_<timestamp>/raw/delete/`
- `wiki_eval/reports/run_<timestamp>/raw/search/`
- `wiki_eval/reports/run_<timestamp>/raw/qa/`

## 8. 质量门槛

这套评测系统的最低要求是：

1. corpus 校验通过
2. baseline 跑通
3. update 回归和 delete 回归可重复执行
4. `report.md` 与 raw payload 保持一致
5. 新增 case 不破坏旧 case

## 9. 新增 case 的标准流程

如果要增加新的 delete regression，推荐按这个顺序做：

1. 在 `docs_del/` 新增对应文档
2. 在 `gold/delete_events.json` 新增事件
3. 更新 `stardust-seed-graph.json`
4. 更新 `stardust-seed-graph.md`
5. 跑严格校验
6. 跑 `--run-delete`
7. 检查是否引入误删或陈旧残留

## 10. 设计原则

- 指标要稳定，便于回归对比
- 语料、gold、图谱描述要保持同步
- graph edge 可以先保持启发式，但要持续提高准确度
- delete regression 要尽量覆盖真实回收场景，而不是只做单点 happy path
- 文档的目标是让新的 agent 在最短时间内理解“怎么跑、看什么、怎么扩”

## 11. 当前版本摘要

当前这套 Stardust 评测系统已经能覆盖：

- KB 创建
- 手工入库
- wiki 生成
- 页面 / 实体 / 事实 / 关系 / 搜索评测
- 增量更新遗漏检查
- delete 回收检查
- graph node / edge 的趋势型观测
- harness 对幂等 retract 的验证

后续如果继续演进，优先方向通常是：

- 提升 `graph_edge_recall_heuristic` 的精度
- 扩充 delete case 的覆盖面
- 把报告结构保持得更稳定，便于历史对比
