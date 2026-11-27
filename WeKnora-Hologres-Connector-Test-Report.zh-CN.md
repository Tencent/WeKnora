# WeKnora 对接 Hologres AGE Connector 测试报告

## 1. 历史测试结论

历史测试使用独立 Hologres 测试实例，对新增的 WeKnora Hologres AGE 图检索 Connector 进行了实机验证。

**结论：修复两个实机兼容性问题后，Connector 的基础集成测试和扩展测试矩阵均通过；完整实机套件开启 Go 竞态检测后连续执行 3 轮，全部通过。相关应用层定向回归测试通过，测试图已清理。**

该结论适用于历史测试的实例版本、测试数据和已覆盖的图存储／检索接口，不等同于生产性能验收，也不代表整个知识库问答系统已经完成端到端验收。

本文保留历史实机结果；移植到官方最新 `main` 后的验证应单独记录，不得将本文结果表述为移植后的实机复测。

## 2. 测试对象与环境

| 项目 | 历史测试情况 |
| --- | --- |
| WeKnora 上游基线 | `f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7` |
| 被测代码 | 在上述基线之上新增的 Hologres Connector 及配套接入修改；历史测试时的工作区快照，非本 PR 移植后的实机复测 |
| Connector 目录 | `internal/application/repository/retriever/hologres/` |
| 实机环境 | 独立 Hologres 测试实例（连接信息已脱敏） |
| 服务端版本回显 | PostgreSQL 11.3 兼容，Hologres 5.0 系列（具体构建标识省略） |
| 图存储 | AGE 实际顶点／边标签表，固定标签 `entity`、`relation` |
| 数据隔离 | 节点及关系属性中的知识库、文档标识 |
| 测试图 | 每次创建独立的 `weknora_test_<时间戳>` 图 |
| 测试权限 | 该测试账号具备本次图创建、读写及删除所需权限；未验证最小权限角色 |
| 传输方式 | 实际端点拒绝 TLS，测试使用 `sslmode=disable` |

报告不包含 AK、密码或带真实凭证的连接字符串。

## 3. 验证范围

实机验证以下图接口：

- `AddGraph`：节点和关系写入、重复写入、来源 chunk 合并、事务回滚。
- `SearchNode`：实体名称匹配、有界一跳扩展、关系方向和文档来源保留。
- `SearchGraph`：知识库范围图候选检索、孤立节点、候选预算、受限属性投影。
- `DelGraph`：文档／知识库删除、分批删除、并发删除和隔离性。
- 初始化及连接管理：AGE 图和标签检查／创建、连接池会话设置、取消请求后的连接可用性。

应用层采用定向回归测试验证后端注册、初始化校验、图提取任务及查询链路相关行为。

本次替换的是 **Neo4j 图后端**，不包括向量检索、BM25／全文检索引擎替换，也不提供通用 Cypher 或 APOC 兼容代理。

## 4. 实机测试结果

实机套件包含 2 个顶层测试：`TestHologresIntegration` 和 `TestHologresIntegrationMatrix`；后者包含 10 个子场景。

### 4.1 基础集成测试

`TestHologresIntegration`：**通过**。

- 不同文档写入同名实体，实体身份保持独立。
- 同一图重复写入，不产生重复实体和重复关系。
- 两个并发写入合并不同 chunk 引用，不丢失已有引用。
- 从关系目标节点检索，仍保留原始关系方向。
- 公共图检索保留孤立节点；聊天图检索不返回无关系的孤立种子。
- 类似注入语句的查询作为普通文本处理。
- 不返回其他知识库的数据。
- 删除一个文档后保留另一文档；删除知识库后图检索为空。

### 4.2 扩展测试矩阵

| 编号 | 场景／测试子项 | 核心验证 | 结果 |
| --- | --- | --- | --- |
| 01 | `schema_and_pool_sessions` | 已存在图的检查；4 个并发 schema 检查／确保调用；4 个物理会话的 `search_path` 和超时设置；禁止自动创建时缺失图报错 | 通过 |
| 02 | `unicode_literals_attributes_identity_and_direction` | 中文、日文、重音字符及其他 Unicode；引号、百分号、下划线、反斜杠按字面匹配；首次属性不被覆盖；chunk 有序去重合并；ID 稳定；正反向关系、自环及多种关系类型；跨知识库身份隔离 | 通过 |
| 03 | `exact_seed_order_and_case_sensitivity` | 完全匹配优先；随后按名称长度、名称及文档排序；区分大小写 | 通过 |
| 04 | `seed_caps_and_edge_dedup_before_budget` | 205 个自环种子；公共检索限制 100 个种子并给出截断原因；聊天检索限制 200 个种子；重复查询词及自环不造成重复计数 | 通过 |
| 05 | `relationship_row_caps` | 2,001 条不同类型关系；公共检索返回 512 条关系并标记截断；聊天检索返回 2,000 条；同时命中两端仍只计一条边 | 通过 |
| 06 | `bounded_public_projection_full_chat_provenance` | 超长 Unicode 名称、600 个 chunk 引用、30 个长属性及长关系类型；公共候选投影受限，聊天检索保留完整已写入属性 | 通过 |
| 07 | `concurrent_new_entity_and_chunk_updates` | 8 个并发写入同一新实体和自环；最终仅 1 个实体、1 条关系，并保留全部 8 个 chunk 引用 | 通过 |
| 08 | `server_error_rolls_back_entities_and_updates` | 通过不存在的边标签触发服务端错误；新增实体和已有实体更新全部回滚；输入包含空图指针时写入前拒绝 | 通过 |
| 09 | `cancellation_pool_recovery_and_invalid_scope` | 已取消上下文下查询、写入、删除返回错误；连接池随后可用；无残留写入；错误作用域及 NUL 字符被拒绝 | 通过 |
| 10 | `batched_deletion_concurrency_and_other_kb_survival` | 1,005 个实体及其自环；2 个并发删除调用，输入含重复 namespace；跨越 1,000 节点批次后清空目标数据，保留其他知识库实体 | 通过 |

### 4.3 预算和投影口径

- **100／200 是种子数上限，不是扩展后总节点数上限。** 一跳邻居可能增加总节点数。
- 公共检索关系候选上限为 512，聊天图检索关系上限为 2,000。
- 公共候选投影额外保留一个字符或一个项目，以便上层判断截断。例如本次验证了 513 字符的名称、513 个 chunk 引用、21 个属性、每属性 1,001 字符，以及 129 字符的关系类型。
- 上述投影是 Connector 的内部候选格式，不是最终 HTTP API 返回格式。本次未通过完整 HTTP 服务实机验证最终展示、权限过滤及证据裁剪。
- 边界规模数据通过参数化 SQL 批量准备；正常写入及并发写入由 `AddGraph` 实际执行。边界测试结果不能当作批量导入吞吐指标。

## 5. 重复执行、竞态检测及回归

### 5.1 实机稳定性复测

对完整实机套件执行：

```bash
go test -race ./internal/application/repository/retriever/hologres \
  -run Integration -count=3 -v -timeout 20m
```

执行时已配置真实测试连接环境变量。观测结果：

```text
PASS
ok github.com/Tencent/WeKnora/internal/application/repository/retriever/hologres 53.898s
```

三轮中的基础测试、10 个矩阵子场景及图清理均通过；未报告 Go 数据竞态。

`53.898s` 是本次整套重复测试的包执行耗时，包含建图、准备数据、校验和清理，**不是单次检索延迟、QPS 或吞吐性能指标**。

### 5.2 本地竞态及应用层回归

| 测试范围 | 结果 |
| --- | --- |
| `hologres`、`neo4j` 和 `internal/config` 包的 `go test -race` | 通过 |
| `internal/container` 的图相关定向测试 | 通过 |
| `internal/handler` 的图相关定向测试 | 通过 |
| `internal/application/service` 的图相关定向测试 | 通过 |
| `internal/application/service/chat_pipeline` 的图相关定向测试 | 通过 |
| `git diff --check` | 通过，无空白格式错误 |

说明：Neo4j 包回归通过，不代表本次连接了真实 Neo4j 实例并执行双后端差分测试。应用层回归为定向测试，不是全仓库测试或完整 Web／LLM 系统测试。

## 6. 实机发现的问题与修复

### 6.1 AGE 对象属性不能直接强制转换为文本

**现象：** 读取节点属性时报错：

```text
agtype argument must resolve to a scalar value
```

**原因：** 本实例的 `agtype::text` 转换不能直接序列化对象值。

**修复：** 使用 `ag_catalog.agtype_out(properties)::text` 序列化完整属性；公共检索先提取 `_search` 投影，再使用输出函数序列化。

**复验：** 重复写入、属性读取、公共投影和完整聊天来源数据测试通过。

### 6.2 AGE 运算符／函数在执行器中不能按原方式解析

**现象：** 实机出现 AGE 运算符解析错误，例如：

```text
operator does not exist: ag_catalog.agtype ->> ag_catalog.agtype
```

**原因：** 原 SQL 的解析／schema 假设不适用于该 Hologres 执行路径；委托 PostgreSQL 执行器的表达式不能依赖连接会话中的 AGE 搜索路径。

**修复：** 属性提取改用显式限定 schema 的 `ag_catalog.agtype_object_field_text`、`ag_catalog.agtype_object_field` 等函数；AGE 类型及函数均显式限定 schema，连接 `search_path` 设置为 `public`。

**复验：** 字面搜索、排序、关系扩展、并发写入及分批删除测试通过。

### 6.3 TLS 连接限制

本端点连接时曾返回 `server refused TLS connection`。测试使用 `sslmode=disable` 后认证成功。

这属于本次连接端点的传输限制，不属于上述图逻辑缺陷。**不得将本次非 TLS 测试配置直接作为生产安全方案**；上线前需确认可用的加密连接方式或经过批准的网络安全措施。

## 7. 测试数据清理与安全

- 所有实机写入位于独立测试图中，不使用生产默认图 `weknora_graph`。
- 每个测试在结束时删除自己的图，并检查 `hologres.hg_ag_label` 中该图的记录数为 0。
- 最后执行独立盘点查询：

```sql
SELECT DISTINCT graph
FROM hologres.hg_ag_label
WHERE graph LIKE 'weknora_test_%'
ORDER BY graph;
```

**实际返回 0 行，未发现该前缀的残留测试图。**

凭证通过测试进程环境传入，没有写入 Connector 源码或本报告。建议测试结束后轮换凭证，并采用专用测试账号及最小权限管理。

## 8. 尚未覆盖的验收项

以下项目不能从本次通过结果中推导出来：

1. **生产规模性能：** 百万级节点／关系、真实知识库规模、P95／P99 延迟、QPS、吞吐及资源消耗。
2. **高并发混合负载：** 持续写入与检索／删除混跑、多进程竞争、锁等待和限流。8 并发功能通过不等同于高并发性能通过。
3. **完整业务链路：** 文件解析、LLM 实体抽取、HTTP 图接口权限与展示、来源证据最终过滤、重排以及最终回答质量。
4. **Neo4j 等价性对照：** 同一真实数据集和查询集在两个实机后端上的逐项差分、召回率及结果一致性。
5. **数据迁移验收：** 现有 Neo4j 数据导入、总量对账、增量同步及切换／回退流程。
6. **运维和故障场景：** 实例重启、网络中断、运行中查询超时、灾备恢复、其他 Hologres 版本和最小权限账号。

当前取消测试使用已经取消的上下文，没有模拟正在执行的服务端查询被中途取消；schema 并发测试基于已经建立的图，没有覆盖多个进程首次同时创建不存在的图。

## 9. 复现方式与相关文件

仅对有 AGE DDL／DML 权限的测试数据库运行。预先为测试进程配置 `WEKNORA_HOLOGRES_TEST_DSN`，不在报告或源码中填写真实凭证。

如果该变量为空，集成测试会 **跳过**；此时的 `go test` 成功不能作为实机通过证据。

在项目根目录执行：

```bash
: "${WEKNORA_HOLOGRES_TEST_DSN:?Set the test DSN before running live tests}"
GOCACHE="$PWD/.cache/go-build" \
GOMODCACHE="$PWD/.cache/go-mod" \
GOPATH="$PWD/.cache/go" \
go test -race \
  ./internal/application/repository/retriever/hologres \
  -run Integration -count=3 -v -timeout 20m
```

复现命令使用 PATH 中已安装的 `go`。

相关文件：

- [实机集成测试及扩展矩阵](internal/application/repository/retriever/hologres/integration_test.go)
- [连接配置与连接池](internal/application/repository/retriever/hologres/client.go)
- [图 schema、写入及删除](internal/application/repository/retriever/hologres/repository.go)
- [图检索及候选投影](internal/application/repository/retriever/hologres/graph_search.go)
- [写入、事务及 SQL 单元测试](internal/application/repository/retriever/hologres/repository_test.go)
- [检索单元测试](internal/application/repository/retriever/hologres/graph_search_test.go)
- [应用后端注册](internal/container/graph.go)

## 10. 验收建议

本轮可以作为 **Connector 图接口实机功能验证通过** 的依据，支持进入真实业务数据的 PoC 阶段。

生产切换前，仍需补齐真实数据对账、Neo4j 双后端结果对照、完整问答链路验收、生产规模压测，以及凭证／网络安全方案。Hologres 混合 DML 事务对访问表的锁定会影响并发写入，需在目标负载下专门评估。
