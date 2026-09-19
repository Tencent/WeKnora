# 检索引擎（Retriever）插件制作指南

> 本文面向要独立开发一个 **检索引擎（Retriever）扩展点插件** 的开发者。
> 它只依赖本仓库的 `pkg/pluginapi` SDK 和主仓的插件扫描机制，无需改动 WeKnora 主仓源码。
>
> 五类扩展点共用的插件骨架（`plugin.yaml` 字段、`PluginControl` 握手/健康、`WEKNORA_PLUGIN_ADDR`、目录环境变量、构建与装载）见本文第 2 节。本文只讲 Retriever 特有的部分。

## 1. Retriever 插件是做什么的

Retriever 插件负责「把**宿主已经算好的向量**写入某个向量/关键词存储后端，并按查询从后端检索出来」。它对应主仓的 `RetrieveEngineRepository`，但插件**不需要**理解主仓的 embedding、分块、融合等概念——它只做最窄的一件事：**向量/关键词的存取**。

与另外四类扩展点的关键区别：

| 特性 | Retriever | 数据源/Parser/搜索/模型 |
|---|---|---|
| 状态 | **有状态**（一个 Store 是一个打开的会话，持有连接、索引） | 无状态（一次调用一次结果） |
| embedding | **宿主算好传入**，插件只负责存取 | 各司其职 |
| 生命周期 | `OpenStore` → 读写 → `CloseStore` | 无会话概念 |

宿主在「创建 VectorStore」时按 `engine_type` 找到插件，`OpenStore` 得到一个不透明的 `store_handle`，之后所有读写都携带这个 handle。

## 2. 五类扩展点的统一骨架（速览）

Retriever 插件与其他四类一样，遵循同一套发现与生命周期协议：

| 项 | Retriever 插件取值 |
|---|---|
| `api_version` | `weknora.plugin/v1`（必填） |
| `id` | 必填，插件唯一 ID |
| `name` | 必填，插件显示名称 |
| `version` | 必填，插件版本 |
| `extension_type` | `retriever`（必填） |
| `protocol_version` | `v1`（必填） |
| `weknora_version` | 可选，宿主兼容范围 |
| `entrypoint` | 必填 |
| 环境变量 | `WEKNORA_PLUGIN_ADDR`；插件目录用 `WEKNORA_PLUGIN_DIR_RETRIEVER` |

> 完整字段说明（含 `capabilities`、`config_schema`、`permissions`、`metadata`）见 [数据源插件指南第 1 节](plugin-development-datasource.md#1-插件包结构)。

启动后必须实现两个统一服务：

- `PluginControl`：`Handshake`（回报插件 ID、`extension_type=retriever`、能力、版本）+ `Health`（健康上报）。
- `Retriever`：本扩展点特有的存取协议（见下）。

> 用 SDK 的 `ServeRetriever` 一行即可同时挂起这两个服务，无需手写 gRPC 注册。

### ⚠️ `PluginControl.Health` 的重要约束

**`Health` 只上报「插件进程本身是否正常」，绝不能因为某个 Store 后端不可达就返回 `unhealthy`。** 否则一个坏 Store 会导致宿主重启整个插件运行时，把其他健康的 Store 也一并打断。

单个 Store 的后端故障应通过该 Store 的业务调用返回错误来表达，而不是让整个插件的 `Health` 失败。

## 3. 核心协议：`RetrieverPlugin` 服务

```text
rpc Describe(RetrieverDescribeRequest) returns (RetrieverDescribeResponse)
rpc OpenStore(RetrieverOpenStoreRequest) returns (RetrieverOpenStoreResponse)
rpc CloseStore(RetrieverCloseStoreRequest) returns (RetrieverResponse)
rpc BatchPut(RetrieverBatchPutRequest) returns (RetrieverBatchPutResponse)
rpc Search(RetrieverSearchRequest) returns (RetrieverSearchResponse)
rpc Delete(RetrieverDeleteRequest) returns (RetrieverResponse)
rpc Patch(RetrieverPatchRequest) returns (RetrieverResponse)
```

### 3.1 `Describe`：声明身份与能力

返回插件是什么引擎、支持什么能力、返回的分数是什么语义：

| 字段 | 类型 | 说明 |
|---|---|---|
| `EngineType` | `string` | 引擎类型名，与 `metadata.engine_type` 一致 |
| `Capabilities` | `[]string` | 能力声明，见第 5 节 |
| `ScoreSemantics` | `string` | 分数语义，见 3.5 节 |

### 3.2 `OpenStore` / `CloseStore`：会话生命周期

- `OpenStore(config)` → 返回 `store_handle`。插件在这里建立连接、连接池、创建必要的 collection/table/index。**`OpenStore` 会被多次调用**——宿主的「测试连接」会先 `OpenStore` 探测连通性再立即 `CloseStore`，正式使用时再开一次，所以你的 `Open` 工厂必须幂等、可重复调用，且每次返回独立会话。
- `store_handle` 是**不透明的、由 SDK 生成**的字符串，插件不应解析它，也不该把租户、地址、凭证编码进去。
- `CloseStore(store_handle)` → 释放该 Store 的连接、连接池、缓存等资源。宿主在**删除 VectorStore**（从注册表注销）时会调用它，所以插件必须在这里真正释放后端资源，而不是留到进程退出。

`config`（`RetrieverStoreConfig`）由宿主从 VectorStore 配置映射而来，分三部分：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Settings` | `map[string]any` | 非敏感连接参数。除宿主预置的固定键（`addr`、`host`、`port`、`use_tls`、`database`、`grpc_address`、`scheme`、`http_port`、`insecure_skip_verify`、`use_default_connection`）外，**你在 `config_schema.settings` 里声明的任意自定义字段名（如 `endpoint`、`collection_prefix`、`compat_mode`）也会原样透传到这里** |
| `Credentials` | `map[string]any` | 敏感凭证。宿主预置的固定键 `username`、`password`、`api_key` 之外，**你在 `config_schema.credentials` 里声明 `secret: true` 的任意自定义字段（如 `token`）也会原样透传到这里，且落库时同样加密** |
| `IndexConfig` | `map[string]any` | 索引/collection 配置，来自 `config_schema.index_config` 声明的字段 |

> **透传约定**：插件 `config_schema` 里声明的 `settings` 字段（无论叫什么名字）会在运行时原样出现在 `Settings` 里，宿主不解释字段含义。

> **凭证加密（重要）**：`credentials` 区的**所有字符串字段都会加密落库**——宿主预置的 `username`/`password`/`api_key` 走固定字段加密，你在 `config_schema.credentials` 里声明的自定义 secret 字段（如 `token`）则由宿主在保存时动态捕获、加密存储，运行时解密后原样透传到 `config.Credentials`。也就是说，**任意命名的凭证字段都受加密保护，不必迁就固定三键**。

### 3.3 `BatchPut`：批量写入

写入一批记录。`RetrieverRecord` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `RecordID` | `string` | 记录唯一标识 |
| `Content` | `string` | 文本内容（用于关键词检索） |
| `Embedding` | `[]float32` | **强类型向量**，宿主算好传入，插件不重新计算 |
| `Metadata` | `map[string]string` | 过滤元数据（`chunk_id`、`source_id`、`knowledge_id`、`tag_id`、`is_enabled` 等） |

### 3.4 `Search` / `Delete` / `Patch`

- `Search`：按查询/向量/过滤条件检索，返回 `[]RetrieverHit`（`RecordID` + `Score` + `Metadata`）。
- `Delete`：按精确 `record_ids` **或**按 `filter` 逻辑范围删除。
- `Patch`：按 `record_ids` 或 `filter` 定位记录，更新元数据字段（如 `enabled`、`tag_id`）。

**`filter` 是多值过滤**（`map[string][]string`，语义为「字段 ∈ 值列表」的 IN 匹配）。这是硬性要求：宿主内部删除/更新都是按 `chunk_id`、`source_id`、`knowledge_id` 这些 metadata 字段的**多值列表**来过滤的，插件必须支持这种匹配，否则无法正确删除/更新一批记录。

### 3.5 `ScoreSemantics`：分数语义（诚实声明）

插件返回的分数，宿主需要知道它是什么语义才能做跨引擎归一化。必须声明为以下三者之一（通过 `Describe` 返回，见 3.1 节）：

| 值 | 宿主归一化行为 | 插件应返回的分数 |
|---|---|---|
| `similarity_higher_better` | `clamp01(score)`（原样夹到 [0,1]） | **已归一化到 [0,1] 的相似度**（越大越相关）。若你的后端返回原始 cosine（值域 [-1,1]），请在插件内自行 `(score+1)/2` 归一到 [0,1] 再返回 |
| `distance_lower_better` | `clamp01(1 - score)`（距离转相似度） | 距离（越小越相关，如 L2 距离） |
| `rank_only` | 原样透传 | 分数无绝对意义（如 BM25），仅相对排序有效 |

> **获取时机**：宿主的 `score_semantics` 是**懒加载**的——注册插件时插件进程尚未启动（加载器先 `Register` 后 `Start`），所以宿主在**首次 `OpenStore` 时**才调用 `Describe` 读取并缓存。这意味着你只需在 `RetrieverProvider.ScoreSemantics` 里正确声明，无需关心时序。

> **归一化由宿主负责**：NaN/Inf 处理、跨引擎比较、RRF 融合全部由宿主完成。但注意 `similarity_higher_better` 意味着「分数已是 [0,1] 相似度」——插件要自己把原始 cosine 归一化，而不是把 [-1,1] 原样丢给宿主。

## 4. SDK 侧如何实现：`ServeRetriever`

SDK 提供了 `RetrieverProvider`（静态信息 + `Open` 工厂）和 `RetrieverBackend`（一个 Store 实例的薄接口）。你只需实现 `RetrieverBackend`，SDK 自动处理 gRPC 注册、`store_handle` 生成、会话 Map、并发安全、`PluginControl`。

```go
package main

import (
    "context"
    "os"

    "github.com/Tencent/WeKnora/pkg/pluginapi"
)

// 一个 Store 实例的实现
type myBackend struct {
    // 连接、连接池、collection/index 等，都藏在这里，宿主不关心
}

func (b *myBackend) BatchPut(ctx context.Context, records []pluginapi.RetrieverRecord) error {
    // 把 records 写入后端，record.Embedding 是已算好的向量
    return nil
}

func (b *myBackend) Search(ctx context.Context, req pluginapi.RetrieverSearchRequest) ([]pluginapi.RetrieverHit, error) {
    // req.RetrieverType 区分 "vector" / "keywords"
    return nil, nil
}

func (b *myBackend) Delete(ctx context.Context, recordIDs []string, filter map[string][]string) error {
    // filter 是多值 IN 匹配：chunk_id ∈ ["a","b"] 之类
    return nil
}

func (b *myBackend) Patch(ctx context.Context, recordIDs []string, filter map[string][]string, patch map[string]string) error {
    // 定位记录后更新 patch 里的字段（如 enabled="false"）
    return nil
}

func (b *myBackend) Close(ctx context.Context) error {
    // 释放连接池等资源
    return nil
}

func openBackend(ctx context.Context, config pluginapi.RetrieverStoreConfig) (pluginapi.RetrieverBackend, error) {
    // 用 config.Settings / config.Credentials / config.IndexConfig 建立连接
    return &myBackend{}, nil
}

func main() {
    provider := pluginapi.RetrieverProvider{
        PluginID:       "example.vector-db",
        EngineType:     "example-vector-db",
        Capabilities:   []string{"vector", "filter"},
        ScoreSemantics: "similarity_higher_better",
        Open:           openBackend,
    }
    addr := os.Getenv("WEKNORA_PLUGIN_ADDR")
    if err := pluginapi.ServeRetriever(context.Background(), addr, provider); err != nil {
        panic(err)
    }
}
```

要点：

- `RetrieverProvider.PluginID` 必须与 `plugin.yaml` 的 `id` 一致。
- `RetrieverProvider.EngineType` 必须与 `plugin.yaml` 的 `metadata.engine_type` 一致。
- `Open` 是工厂函数：每次 `OpenStore` 都会调用它创建**一个新的独立 Store 实例**（不同的连接/索引命名空间）。不要在多个 Store 之间共享连接或索引。
- `Handshake` / `Health` 由 SDK 自动实现，`Health` 默认返回 `running`（不聚合 Store 健康）。

## 5. `plugin.yaml` 示例

```yaml
api_version: weknora.plugin/v1
id: example.vector-db
name: Example Vector Database
version: 1.0.0
extension_type: retriever
protocol_version: v1
weknora_version: ">=0.7 <1.0"
entrypoint: ./example-vector-db

capabilities:
  - vector        # 支持向量检索
  - keywords      # 支持关键词检索（可选）
  - filter        # 支持元数据过滤

metadata:
  engine_type: example-vector-db   # 必填：宿主任凭它把 VectorStore 路由到本插件
  icon: example-vector-db.png      # 可选：插件随包 logo（本地文件或 http(s) URL）
  # 注意：score_semantics 不在 manifest 里声明，而是通过 Describe RPC 返回
  #（即 SDK 的 RetrieverProvider.ScoreSemantics 字段，见第 4 节示例）

config_schema:                     # 用 JSON Schema 描述 OpenStore 时需要的配置
  type: object
  properties:
    settings:                      # 非敏感连接参数 → 透传到 config.Settings
      type: object
      required: [endpoint]
      properties:
        endpoint:
          type: string
          title: 服务地址          # title 会在前端作为字段标签显示（缺省回落字段名）
        timeout:
          type: integer
    credentials:                   # 敏感凭证 → 透传到 config.Credentials
      type: object
      required: [api_key]
      properties:
        api_key:
          type: string
          secret: true            # 敏感字段，宿主会脱敏
          title: API Key
        token:                    # 自定义凭证字段：字段名可任意（如 token），
          type: string            # 宿主动态捕获、加密落库，运行时解密后透传
          secret: true
          title: 访问令牌
    index_config:                  # 索引/collection 配置 → 透传到 config.IndexConfig
      type: object
      properties:
        collection_prefix:
          type: string
          title: 集合名前缀

permissions:
  network: allowlist               # 需要连后端数据库，声明网络策略
  allowed_destinations:
    - "example-vectordb.com"
```

### 必填校验

- `metadata.engine_type` **不能为空**——`RegisterExternalRetriever` 明确要求 retriever 插件声明引擎类型，否则无法装载。
- `metadata.engine_type` **不能与宿主内建引擎类型重名**（`postgres`/`sqlite`/`elasticsearch`/`opensearch`/`qdrant`/`milvus`/`weaviate`/`doris`/`tencent_vectordb`）。要复刻某个内建引擎，请起别名（如 `milvux`），并通过 `ScoreSemantics` 声明正确的分数语义——宿主按语义归一化，不依赖引擎名。

### 运行方式与网络声明

插件通过 `entrypoint` 声明启动方式：可执行文件，或 `docker://镜像`。通过 `permissions.network` 声明网络范围：`none` 或 `allowlist` + `allowed_destinations`（检索引擎通常需要连后端数据库，用 `allowlist`）。出站请求必须经 `pluginapi.NewPluginHTTPClient()` 发起，不要用裸 `http.Client`，否则白名单不生效。

实际运行隔离方式（进程 / 容器、断网、出口代理）由宿主部署环境决定；插件作者不需要实现或配置 Runtime Agent / Docker Socket / 镜像校验。

### 图标（icon）

- `metadata.icon`（可选）：插件随包的 logo，支持**本地文件名**（如 `Milvus.png`，相对插件目录）或 **http(s) URL**。
- 本地文件：宿主注册时解析为绝对路径，并通过**免认证**路由 `/api/v1/vector-stores/icon/:engine_type` 流式返回（浏览器 `<img>` 无法携带认证头）。前端在「向量数据库引擎」的引擎下拉里显示该图标。
- http(s) URL：直接透传给前端使用。
- 支持的格式：png / jpg / jpeg / svg / webp / gif / ico，单文件不超过 512KB。

### config_schema 字段的 title

- 每个字段可声明 `title`（如 `title: 服务地址`），宿主会把它透传到前端作为字段标签显示；缺省时前端回落到字段名（或内置字段的 i18n 文案）。

### 能力声明（capabilities）原则

> **插件不需要能力齐全，但声明支持的能力必须真实可用。**

| 能力 | 含义 | 是否核心 |
|---|---|---|
| `vector` | 支持向量检索 | 核心 |
| `keywords` | 支持关键词（BM25 等）检索 | 核心 |
| `filter` | 支持元数据过滤 | 核心 |
| `copy_indices` | 支持直接复制索引（高级，暂未开放） | 可选 |
| `staged_rebuild` | 支持分阶段重建（高级） | 可选 |

宿主根据能力决定行为：例如插件只声明 `vector`，宿主就不会让它做关键词检索；声明了 `copy_indices` 才调用复制能力，否则宿主走「重新计算 embedding」的降级路径。

## 6. 关键语义约束（务必遵守）

1. **embedding 强类型**：`Embedding` 是 `[]float32`，由宿主算好传入。插件**不要**自己重新计算 embedding，也不要把向量塞进 `Metadata`。
2. **filter 是多值 IN 匹配**：`map[string][]string`，字段 ∈ 值列表。这是宿主删除/更新批量记录的硬性前提。
3. **幂等写入**：同一个 `RecordID` 重复写入不应产生重复记录（建议内部按 `RecordID` 做 upsert 语义）。
4. **维度不匹配要显式报错**：向量维度与已建索引不符时，必须返回错误，不能静默写入导致检索全乱。
5. **删除幂等**：删除不存在的记录不应报错。
6. **禁用记录不可被检索**：`Patch` 把 `enabled=false` 后，该记录必须立即从检索结果中排除（不能只在 metadata 里改个字段、检索时却不过滤）。
7. **分数语义诚实**：`score_semantics` 必须真实反映返回分数的含义。
8. **`Health` 不聚合 Store 健康**：见第 2 节的警告。
9. **返回顺序不必严格排序**：宿主会在检索结果进入 RRF 融合前做一次确定性排序（score 降序、`RecordID` 升序兜底），所以你不必保证返回顺序。但建议仍按 score 降序返回，尤其对 `rank_only` 或关键词无打分的场景，稳定的返回顺序有助于结果可复现。

## 7. 校验与验证

Retriever 是五类扩展点里唯一「有状态」的，验证比 Parser/Search 复杂。SDK 目前**没有** Retriever 的 conformance 入口（另有 DataSource / Parser / WebSearch / Model 四个），因此建议按以下顺序手动自检：

1. 用 SDK 的 `RetrieverProvider` 实现一个**内存 Map 后端**（`recordID → {content, vector, metadata}`），跑通全流程。
2. 验证清单：
   - `OpenStore` 创建独立会话，两个 Store 数据互不影响；
   - `BatchPut` 写入后，`Search` 能返回正确 TopK；
   - 重复写同一 `RecordID` 不产生重复记录；
   - `Delete`（按 `chunk_id` 多值 filter）后记录不可检索；
   - `Patch(enabled=false)` 后记录不可检索；
   - 插件进程重启后，宿主能重新 `OpenStore` 恢复会话。
3. 建议用 Docker 隔离（`--network` 受限）验证网络策略生效。

## 8. 宿主侧如何被使用

宿主在「创建 VectorStore」时，按 `metadata.engine_type` 找到插件，`OpenStore` 得到 `store_handle`，再创建一个 `GRPCRetrieverRepository`（实现内部 `RetrieveEngineRepository`），包装进标准的 `KVHybridRetrieveEngine` 并注册到 `RetrieveEngineRegistry`。

关键点：

- **embedding 由宿主算**（`KeywordsVectorHybridRetrieveEngineService` 在调用 Repository 前批量算好向量，放进 `params["embedding"]`），插件只收到「已算好的向量」。
- **融合、权限过滤、租户隔离、重建索引编排全部由宿主处理**，插件不参与。
- 外部插件走 gRPC 路径；内置后端（PostgreSQL/Qdrant/Milvus 等）仍走进程内 SDK 路径，性能不受影响。

## 9. 与宿主的关系（边界）

Retriever 插件**只负责「向量的写入 / 检索 / 删除 / 元数据更新」**，不负责：

- **计算 embedding**——宿主算好传入；
- **RRF 融合、跨引擎归一化**——宿主处理；
- **权限过滤、租户隔离**——宿主处理；
- **重建索引的任务编排、进度跟踪**——宿主处理；
- **Collection/维度分表、连接池、后端重试、事务**——这是插件**自己内部**的事，宿主不关心，只要协议输出正确。

一句话：**插件管「具体后端怎么存怎么查」，宿主管「算好向量、编排流程、融合结果」**。插件内部可以自由使用任何数据库、任何索引结构、任何维度策略，只要守住上面的协议契约。

## 10. 部署与网络可达性（连后端数据库的插件必读）

Retriever 插件通常用后端数据库的**原生 gRPC/TCP 客户端**（如 Milvus SDK）建立连接，而不是走 `NewPluginHTTPClient()` 的 HTTP 出口。因此它的网络可达性由**部署拓扑**决定，而不是由 `permissions.network` 的 HTTP 白名单决定。

> 本节以本仓库的 `milvux`（Milvus 兼容）插件与两套 compose 部署为例。文中的 `milvus` 服务名、`weknora_WeKnora-network` 网络名、`addr` 字段名均为示例，可按实际后端与插件替换。

### 10.1 两种入口，两种网络环境

插件用 `entrypoint` 声明运行方式，它决定了插件进程所处的网络环境：

| `entrypoint` | 运行方式 | 网络环境 |
|---|---|---|
| 可执行文件（如 `./my-retriever`） | 作为 app 容器的子进程运行 | **继承 app 容器的网络**，可访问同一 Docker 网络内的后端服务 |
| `docker://镜像` | 在独立 OCI 容器中运行 | **`--network none`**（无直连网络）；若策略允许联网，宿主会挂入一个受控 HTTP 出口代理，插件只能经该代理访问白名单内的**公网**目标 |

> ⚠️ **容器入口连不了内网后端**：受控 HTTP 出口会拒绝私网与环回地址（`IsForbiddenIP` 含 `IsPrivate()`）——即使把该地址写进 `allowed_destinations`，指向内网地址（如 `milvus:19530`）的请求也会被拒绝。因此**需要连内网向量库的 Retriever 插件应使用进程入口**，由原生客户端直连后端（见 10.2、10.3）。

### 10.2 寻址规范：用 Compose service name

后端地址请写成 **Compose service name**，而不是 `container_name`：

| 写法 | 是否稳定 |
|---|---|
| `milvus:19530` | ✅ service name，同一 Compose 网络内稳定，不随部署形态变化 |
| `WeKnora-milvus:19530` | ❌ `container_name`，随实例/项目名变化 |
| `WeKnora-milvus-dev:19530` | ❌ 另一个 compose 文件的容器名，跨项目不可解析 |

地址字段名**由插件自治**（`addr` / `endpoint` / `host` 皆可）：宿主原样透传 `settings`，不解释、不重命名。插件在 `openBackend` 里读自己的字段即可：

```go
addr := getString(config.Settings, "addr")   // addr 是 milvux 自己的字段名，非框架规定；
                                             // 别的插件可用 endpoint / host 等任意名
```

### 10.3 拓扑：app 与后端服务同处一个 Docker 网络

```text
app 容器（含 Retriever 子进程，继承 app 网络）
    │
    └── 与目标后端（milvus / qdrant / doris …）同处一个 Docker 网络
          │
          └── 插件用 service-name:port 访问后端
```

- **同一 compose 文件**：`app` 与后端服务加入同一个网络即可，天然可解析。
- **后端由另一个 compose 文件管理**：后端服务的 compose 用外部网络接入 app 所在网络，并注册 service-name 别名：

> ⚠️ `networks:` 下多个网络必须用**统一的映射写法**，不能把列表项（`- xxx`）和映射键混排——那是非法 YAML，`docker compose config` 会报 `did not find expected '-' indicator`。

```yaml
services:
  milvus:
    networks:
      WeKnora-network-dev:    # 本 compose 自己的网络（映射写法）
      WeKnora-network:        # 外部网络：app 所在的正式运行网络
        aliases:
          - milvus            # service-name 别名，使 dev 与正式环境寻址名统一

networks:
  WeKnora-network-dev:
    driver: bridge
  WeKnora-network:
    external: true
    # 默认名是「compose 项目目录名 + 网络 key」派生的（如 weknora_WeKnora-network），
    # 项目目录改名后网络名会变；用环境变量显式锚定，默认值与实际部署一致。
    name: ${WEKNORA_NETWORK_NAME:-weknora_WeKnora-network}
```

### 10.4 信任等级对网络的影响

每个插件的实际运行隔离方式由**部署管理员**配置信任等级统一决定。插件作者不需要实现或配置 Runtime Agent、Docker Socket 或出口代理，只需正确声明 `entrypoint` 与 `permissions.network`：

| 信任等级 | 适用入口 | 网络行为 |
|---|---|---|
| `offline`（默认） | 任意 | 断网：进程入口注入 `none` 策略，容器入口以 `--network none` 启动 |
| `trusted` | **进程入口** | 按 manifest 声明的策略放行；进程继承 app 网络，可访问同一 Docker 网络内的后端 |
| `isolated` | **`docker://` 容器入口** | 容器内无直连网络（`--network none`），联网只能经受控 HTTP 出口访问公网目标 |

> 需要联网访问内网后端的 Retriever 插件应使用**进程入口**，并由部署管理员把信任等级设为 `trusted`。若插件用容器入口，即使声明了网络策略，也只能经 HTTP 出口访问公网，无法直连内网后端的原生 gRPC。
