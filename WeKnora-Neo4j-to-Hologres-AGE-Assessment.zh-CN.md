# WeKnora 的 Neo4j 图后端迁移至 Hologres AGE：可行性与实施建议

## 一、结论摘要

**可以考虑使用 Hologres AGE 替代 WeKnora 的 Neo4j 图后端，实现当前代码所使用的知识图谱能力，但不能仅修改连接地址完成迁移。**

本次分析的判断是：

- **功能层面：有条件可实现同等能力。** 所检查的 Neo4j 适配器主要使用实体存储、关系存储、实体名匹配和有界的一跳邻居扩展，未依赖 PageRank、社区发现等高级图算法。
- **兼容层面：不是 Neo4j 的直接替代品。** APOC、多标签节点、列表属性写入、数据库驱动及结果类型均需要适配。
- **性能层面：尚不能承诺同等性能。** Hologres 官方文档说明，当前 AGE 图检索主要依赖基础表扫描，需要通过客户实际规模的测试确认。
- **推荐路径：保留 WeKnora 上层 RAG 流程，新增 Hologres 图仓储适配器，采用“SQL 写入 + AGE Cypher／SQL 查询”。**

本报告保留历史设计评估，不作为本 PR 当前实施状态说明。本 PR 采用配置指定的共享图、固定 `entity`／`relation` 标签和参数化 SQL 读写；历史实机结果见 [Connector 测试报告](WeKnora-Hologres-Connector-Test-Report.zh-CN.md)，最新 `main` 的移植验证需单独记录。以下关于每知识库独立建图、Cypher 查询和迁移路径的内容属于历史方案建议，不代表适配器的实际实现或已完成的迁移工具。

## 二、分析范围与依据

### 2.1 源码版本

历史分析基于以下 WeKnora 提交：

```text
f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7
```

目标目录：

[internal/application/repository/retriever/neo4j](https://github.com/Tencent/WeKnora/tree/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/repository/retriever/neo4j)

除该目录外，还检查了图仓储接口、图检索服务、聊天流水线、依赖注入以及部分删除流程和测试。

### 2.2 Hologres 能力依据

[Hologres AGE 图检索使用指南](https://help.aliyun.com/zh/hologres/user-guide/age-graph-search-usage-guide)

文档明确说明：

- 仅 Hologres **V5.0 及以上**支持 AGE 图检索。
- 图对应 Schema，标签对应 Schema 下的表。
- 支持基础图模式匹配、固定多跳、变长路径以及部分 Cypher DML。
- 多标签顶点暂不支持。
- 复杂或批量写入建议使用标准 SQL。
- 当前 AGE 检索主要依赖基础表扫描。

客户实例的实际版本、开放能力和限制仍需现场确认。

## 三、WeKnora 当前如何使用 Neo4j

### 3.1 图后端承担的能力

所检查的实现主要完成：

1. 保存从文档中抽取的实体和关系。
2. 在实体上保存来源文档和 chunk 引用。
3. 按实体名进行区分大小写的子串匹配。
4. 从匹配实体进行有界的一跳邻居扩展。
5. 删除文档对应的图数据，支持相关重建流程。
6. 为公开图检索 API 返回实体、关系及来源候选。

当前图仓储接口分为两组：

```go
// RetrieveGraphRepository
AddGraph(...)
DelGraph(...)
SearchNode(...)

// GraphSearchRepository
SearchGraph(...)
```

其中，`SearchNode` 服务于聊天检索链路；`SearchGraph` 服务于独立的公开图查询，包含孤立实体处理、有界字段投影和显式截断信息。

### 3.2 Neo4j 不等于整个知识库

当前链路可以概括为：

```text
文档解析与切分 → 实体／关系抽取 → 图后端存储
                                      ↓
用户问题 → 实体抽取 → 图检索 → 来源 chunk 候选
                                      ↓
                  chunk 读取、权限与有效性校验
                                      ↓
                    合并检索结果、重排、生成回答
```

在所检查的聊天检索实现中，图命中用于补充 chunk 候选，不承担向量相似度计算。图候选进入现有检索与重排流程。

因此，**只替换 Neo4j 图后端，不必同时迁移向量检索、全文检索、业务元数据、文件存储或大模型服务。**

如果客户目标是将这些存储和检索能力全部统一到 Hologres，则属于另一项范围更大的改造，需要分别评估。

### 3.3 需要保留的有界检索行为

源码中的两条图查询路径具有不同预算：

| 路径 | 当前关键限制 |
| --- | --- |
| 聊天图检索 `SearchNode` | 最多 200 个种子实体、2000 行关系候选 |
| 聊天图候选转 chunk | 最多补充 30 个新 chunk |
| 公开图查询 `SearchGraph` | 内部最多 100 个种子实体、512 行关系候选 |
| 公开图查询服务 | 10 秒超时，并限制返回节点、关系、来源 chunk 和字段大小 |

迁移不能只做到“能查到邻居”，还应保留排序、去重、截断、超时和来源校验语义。

## 四、能力对照与不兼容项

| WeKnora 当前依赖 | Hologres AGE 情况 | 迁移措施 |
| --- | --- | --- |
| 节点、方向关系、属性 | 支持 | 建立图标签表 |
| 一跳邻居匹配 | 支持基础模式匹配 | 改写查询并验证返回语义 |
| `ORDER BY`、`LIMIT`、聚合、`WITH`、`UNWIND` | 官方文档列为支持 | 验证现有表达式组合 |
| 固定多跳、变长路径 | 支持 | 当前替换主要关注一跳，不需要先增加多跳能力 |
| 一个节点同时携带 KB 与文档标签 | 多标签顶点暂不支持 | 改为单标签与作用域属性 |
| `apoc.merge.node` | 无可直接沿用的 APOC 支持依据 | 用 SQL／应用逻辑实现合并与幂等 |
| `apoc.merge.relationship` | 无可直接沿用的 APOC 支持依据 | 明确定义关系身份并实现去重 |
| `apoc.coll.union` 合并 chunk 列表 | 原调用不能直接迁移 | 用 SQL／应用逻辑实现集合合并 |
| `apoc.periodic.iterate` 批量删除 | 原调用不能直接迁移 | 用应用侧 SQL 批次替代 |
| Neo4j `elementId()`、Node、Relationship | 驱动与身份表示不同 | 改为 AGE ID 和明确的结果解码 |

### 4.1 写入是主要兼容障碍

Hologres 文档列出的 Cypher 写入限制包括：

- 单条语句只能包含一个写子句，且写子句必须置于末尾。
- 写子句后不能跟 `RETURN`。
- 写入目标标签必须预先创建。
- 属性写入仅支持标量字面量或单层 Map；不支持列表、嵌套 Map、参数或属性引用。
- 写子句前不能使用 `WITH`。

WeKnora 当前写入依赖批量数据、APOC 合并和列表属性更新，因此不宜机械地把原 Cypher 包装到 `ag_catalog.cypher()` 中。

**建议将 `AddGraph` 和 `DelGraph` 的主要实现改为标准 SQL。** 图属性中的列表可选择经过验证的 SQL agtype 构造方式，或使用普通辅助表存储，再由适配器还原业务结果。

### 4.2 查询也需要逐项验证

现有查询使用了 `ANY`、存在性子查询、列表切片、Map 投影，以及 `elementId()`、`startNode()`、`endNode()` 等函数。

官方文档中“支持 Cypher 查询”并不等于逐条支持这些 Neo4j 查询。

处理原则：

- 使用实际实例验证每个表达式及其组合。
- 不兼容部分改写为较简单的 AGE 查询，或使用 SQL 完成。
- 不直接拼接用户查询文本；优先使用经过验证的参数化 SQL。Cypher 参数传递方式需另外验证。
- AGE 返回值为 `agtype`，字符串、列表和图身份需要明确解码，避免引号、转义和整数精度问题。

## 五、推荐的数据模型

### 5.1 PoC 建议：每个知识库一个图，使用固定标签

```text
Graph：对应一个知识库

Entity 节点：
  name
  kg／document_id
  chunks
  attributes

Relation 边：
  start_id
  end_id
  rel_type
```

图与标签初始化示例，语法来自官方文档：

```sql
CALL ag_catalog.hg_age_create_graph('kb_graph');
CALL ag_catalog.hg_age_create_vlabel('kb_graph', 'Entity');
CALL ag_catalog.hg_age_create_elabel('kb_graph', 'Relation');
```

原本通过动态标签表达的文档作用域，改为 `document_id`／`kg` 属性。

关系使用固定 `Relation` 标签，在属性中保存原关系类型 `rel_type`。适配器读取时，将其映射回 WeKnora 的 `GraphRelation.Type`。这样可避免每种抽取关系类型都创建一张标签表。

该设计是推荐方案，不是唯一方案。若业务还需要直接按 Cypher 关系标签查询，应评估关系类型独立标签的取舍。

### 5.2 必须保留文档级实体身份

当前实现按文档区分同名实体，不能在迁移时只按 `name` 合并。

建议逻辑身份至少包含：

```text
实体：knowledge_base_id + document_id + entity_name
关系：源实体身份 + 目标实体身份 + relation_type
```

例如两个文档中的“腾讯”应保留各自的属性与 chunk 来源，不能因为名字相同就合并为同一个实例。

Hologres 图表的 `id` 由系统生成，写入时不能手动指定。不要将 Neo4j 的物理 ID 直接写入 AGE 的 `id` 列；如有必要，使用普通辅助表维护逻辑身份与图 ID 的映射。

### 5.3 生产环境评估图的数量

“每 KB 一个图”有利于隔离和 PoC 验证，但图对应 Schema，标签对应表，知识库数量较多时需要评估元数据及运维成本。

另一种方案是多个 KB 共享一个图，通过 KB、文档等属性过滤。此方案需要特别验证：

- 节点与边两端均处于正确作用域。
- 共享知识库授权、文档选择及标签范围不会丢失。
- 查询不能返回跨作用域关系或来源。
- 扫描范围与性能是否可接受。

## 六、应用代码改造范围

### 6.1 新增 Hologres 图仓储适配器

建议保留现有接口，新增类似以下目录的实现：

```text
internal/application/repository/retriever/hologres/
```

实现四个方法：

| 方法 | 主要工作 |
| --- | --- |
| `AddGraph` | 合并节点、合并 chunk 引用、创建／去重有向关系 |
| `DelGraph` | 按文档作用域先删相关边，再删节点；控制批次并支持安全重试 |
| `SearchNode` | 保留实体匹配、排序、有界一跳扩展、方向与去重语义 |
| `SearchGraph` | 保留孤立实体、候选预算、字段投影与截断信息 |

标准 SQL 直删节点不会自动清理关联边，必须显式维护边数据。不要把 Cypher `DETACH DELETE` 的语义直接套用到 SQL 删除上。

### 6.2 修改依赖注入与配置

`internal/container/container.go` 当前直接初始化 Neo4j 驱动，并注册两组 Neo4j 图仓储。

需要增加：

- 图后端选择，例如 `neo4j`／`hologres`。
- Hologres PostgreSQL 协议连接池。
- 图与标签初始化及生命周期管理。
- 超时、取消、连接清理与故障处理。

配置名称应尽量表达“图能力”，而不是绑定某个数据库。

### 6.3 不要遗漏 Neo4j 专属开关

`internal/application/service/chat_pipeline/extract_entity.go` 直接检查 `NEO4J_ENABLE`。

如果仅替换仓储并关闭 Neo4j，此处可能使实体抽取直接跳过，导致图检索链路失效。

迁移时应检查所有 Neo4j 专属开关，将“图功能是否启用”与“选择哪种后端”分离，同时保留 KB 自身的图功能配置。

### 6.4 保留证据与权限校验

公开图检索服务会读取来源 chunk，并确认它们属于正确租户、KB 和文档，且仍然有效；聊天链路也会重新检查文档／标签范围及 chunk 有效性。

这些校验应继续保留。图中的 chunk 引用可能陈旧，不能因为图查询命中就绕过上层来源验证。

## 七、主要风险

### 7.1 性能风险

官方文档说明当前 AGE 图检索主要依赖基础表扫描。

因此：

- 返回 100 个节点，不代表只扫描 100 个节点。
- 短实体名和宽泛子串匹配可能产生较大扫描范围。
- 高度数实体可能放大邻居扩展成本。
- 一跳查询较简单，但这并不自动保证低延迟或高并发。

如果 AGE 查询性能不达标，可在同一仓储接口后用标准 SQL JOIN 实现当前有界一跳查询。这能保留应用层图能力，但不能据此声称具备通用 Neo4j 查询兼容性。

### 7.2 并发、幂等与一致性风险

需要验证：

- 重复处理同一文档不会产生重复节点或关系。
- 并发更新不会丢失 chunk 集合中的元素。
- 图 ID 映射与实际写入能够一致恢复。
- 失败重试不会产生半完成图或悬空关系。
- 文档删除与重建不会错误删除其他文档的同名实体。

SQL 与辅助映射表的事务边界、唯一性实现和冲突处理方式，应在实际 Hologres 实例上确认，不能简单假定与 Neo4j 托管事务完全相同。

### 7.3 排序与截断造成的结果差异

原实现使用 Neo4j 物理身份作为部分排序的最终打破平局依据。迁移后物理 ID 不同，边界候选可能变化，从而影响进入后续重排的 chunk 集合。

验收应关注逻辑实体、关系、证据集合和检索质量。不要只比较物理 ID，也不要将大模型回答逐字一致作为唯一标准。

## 八、PoC 与上线验证建议

### 阶段一：最小适配器

在 Hologres V5.0+ 实例上完成四个图方法，保留现有上层接口和 RAG 流程。

### 阶段二：语义与安全回归

复用现有测试意图，覆盖：

- 同名实体位于不同文档，属性与证据不混淆。
- 入边、出边、反向关系和自环方向正确。
- 同一边不会因两端均为种子而重复消耗候选预算。
- 精确实体名优先于仅子串匹配的实体。
- 公开 API 保留孤立实体，聊天检索保留其原有处理语义。
- KB、文档和标签范围正确，跨范围关系不会泄露。
- 失效、禁用或已删除的来源不能重新进入回答。
- 预算、字段投影、截断信息、超时与失败处理正确。
- 包含引号、反引号或查询语法片段的输入不会被解释为代码。

### 阶段三：写入与生命周期验证

覆盖重复导入、并发导入、故障重试、文档删除、文档重建及 KB 删除。

### 阶段四：客户规模性能测试

使用代表性数据，同时测试 Neo4j 与 Hologres，记录：

- 节点数、边数、KB 数、单文档图规模和高度数节点分布。
- 写入吞吐量及失败恢复情况。
- 查询 p50／p95／p99 延迟和并发能力。
- 宽泛实体名、中文短词和无匹配查询的开销。
- 实体／关系集合、来源 chunk 集合及最终检索质量。
- 实际执行计划、资源消耗及成本。

### 阶段五：灰度切换

先对一致的数据快照执行对照查询，再进行影子读取。通过验收后切换生产读流量，并保留 Neo4j 回退路径。

如果采用双写，应额外设计失败补偿与一致性核对，不能将双写天然视为原子操作。

## 九、需要客户补充的信息

在承诺生产“同等能力”前，应确认：

1. Hologres 实例版本及 AGE 实际可用性。
2. 当前 Neo4j 节点数、边数、KB 数及增长速度。
3. 客户是否在 WeKnora 之外直接使用 Neo4j、APOC 或图算法。
4. 峰值查询并发、导入并发与延迟目标。
5. 是否要求仅替换 Neo4j，还是同时迁移向量、全文及业务存储。
6. 可用于对照测试的脱敏文档与代表性问题集。
7. 存量迁移窗口、允许的一致性延迟及回滚要求。

## 十、最终建议

**建议推进有边界的 PoC：只替换图后端，保留上层知识库与 RAG 流程，采用 SQL 管理图数据，并通过 AGE／SQL 实现检索。**

对于当前检查到的一跳检索与来源回溯能力，适配路径清晰；主要工作量集中在写入语义、身份与作用域映射、并发幂等、配置解耦和生产性能验证。

对外建议表述为：

> Hologres AGE 有能力承接 WeKnora 当前使用的知识图谱功能，但需要后端适配，不能直接复用全部 Neo4j／APOC 代码。功能等价、性能指标及客户额外使用的图能力，应通过实际实例和客户数据验证后确认。

## 参考资料

- [Hologres AGE 图检索使用指南](https://help.aliyun.com/zh/hologres/user-guide/age-graph-search-usage-guide)
- [Neo4j 图仓储实现 repository.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/repository/retriever/neo4j/repository.go)
- [Neo4j 公开图查询适配器 graph_search.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/repository/retriever/neo4j/graph_search.go)
- [图仓储接口 retriever_graph.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/types/interfaces/retriever_graph.go)
- [公开图检索接口 graph_search.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/types/interfaces/graph_search.go)
- [图数据与作用域类型 extract_graph.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/types/extract_graph.go)
- [公开图检索预算与响应类型 graph_search.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/types/graph_search.go)
- [公开图检索与证据验证服务 graph_search.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/service/graph_search.go)
- [聊天实体抽取 extract_entity.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/service/chat_pipeline/extract_entity.go)
- [聊天实体检索 search_entity.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/service/chat_pipeline/search_entity.go)
- [依赖注入与 Neo4j 初始化 container.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/container/container.go)
- [文档删除流程 knowledge_delete.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/service/knowledge_delete.go)
- [图检索集成回归测试 search_integration_test.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/repository/retriever/neo4j/search_integration_test.go)
- [公开图检索测试 graph_search_test.go](https://github.com/Tencent/WeKnora/blob/f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7/internal/application/repository/retriever/neo4j/graph_search_test.go)
