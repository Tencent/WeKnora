# 扩展点指南

WeKnora 在文档解析、分块、检索、模型接入、联网搜索、数据源、IM 渠道、Agent 工具、对象存储九个层面都预留了清晰的扩展点。本章逐个给出：**核心接口定义（真实源码）→ 现有实现列表 → 新增实现步骤（含注册点文件）**。所有接口代码均摘自当前仓库源码。

## 0. 扩展点总览

```mermaid
graph LR
    subgraph DR["docreader (Python)"]
        P1["文档解析器<br/>(parser/registry.py)"]
    end
    subgraph APP["app (Go, internal/)"]
        P2["分块策略<br/>(infrastructure/chunker)"]
        P3["检索引擎<br/>(application/repository/retriever)"]
        P4["模型 Provider<br/>(models/provider)"]
        P5["联网搜索引擎<br/>(infrastructure/web_search)"]
        P6["数据源连接器<br/>(datasource/connector)"]
        P7["IM 平台适配器<br/>(im/adapter.go)"]
        P8["Agent 工具<br/>(agent/tools)"]
        P9["存储后端<br/>(application/service/file)"]
    end
    DOC["原始文档"] --> P1
    P1 -->|"markdown + 图片"| P2
    P2 -->|"chunks"| P3
    P6 -->|"外部内容同步"| P1
    P7 -->|"IM 消息"| AG["Agent 引擎"]
    AG --> P8
    P8 --> P3
    P8 --> P5
    AG --> P4
    P1 -.->|"文件读写"| P9
    P2 -.-> P9
    CT["container.go<br/>(依赖注入 / 注册中枢)"] -.->|"注册"| P3
    CT -.->|"注册"| P5
    CT -.->|"注册"| P6
    CT -.->|"注册"| P7
```

Go 侧绝大多数扩展点的**注册中枢**是 `internal/container/container.go`（依赖注入容器）：检索引擎 `initRetrieveEngineRegistry()`、联网搜索 `registerWebSearchProviders()`、IM 适配器 `registerIMAdapterFactories()`、数据源连接器 `initConnectorRegistry()`。

---

## 1. 新增文档解析器（docreader，Python）

### 接口定义

基类在 `docreader/parser/base_parser.py`。轻量化重构后 BaseParser 只负责把文档转成 markdown 文本 + 原始图片引用（分块、图片存储、OCR、VLM caption 均在 Go 侧完成）：

```python
# docreader/parser/base_parser.py
class BaseParser(ABC):
    """Base parser interface."""

    def __init__(self, file_name: str = "", file_type: Optional[str] = None, **kwargs):
        self.file_name = file_name
        self.file_type = file_type or os.path.splitext(file_name)[1].lstrip(".")

    @abstractmethod
    def parse_into_text(self, content: bytes) -> Document:
        """Parse document content into markdown text.

        Returns:
            Document with ``content`` (markdown string) and optional
            ``images`` dict mapping storage-relative paths to base64 data.
        """
```

返回值 `Document`（`docreader/models/document.py`，pydantic 模型）核心字段是 `content: str`（markdown）与 `images: Dict[str, str]`（路径 → base64）。

### 注册机制

`docreader/parser/registry.py` 的 `ParserEngineRegistry` 以"引擎名 → {文件扩展名 → Parser 类}"两级映射管理解析器；当请求的引擎不支持该文件类型时自动回落到 `builtin` 引擎。默认注册表由 `_build_default_registry()` 构建，模块级单例 `registry = _build_default_registry()`。

```python
# docreader/parser/registry.py（节选）
class ParserEngineRegistry:
    def register(self, name: str, file_types: Dict[str, Type[BaseParser]],
                 description: str = "", check_available: Callable = None,
                 unavailable_hint: str = ""): ...
    def get_parser_class(self, engine: str, file_type: str) -> Type[BaseParser]: ...
```

### 现有实现

| 引擎 | Parser | 文件 |
| --- | --- | --- |
| `builtin` | `Docx2Parser` / `DocParser` / `PDFParser` / `MarkdownParser` / `ExcelParser` / `EPUBParser` / `HTMLParser` / `MHTMLParser` / `ImageParser`（jpg/png/gif/bmp/tiff/webp 等） | `docreader/parser/docx2_parser.py`、`doc_parser.py`、`pdf_parser.py`、`markdown_parser.py`、`excel_parser.py`、`epub_parser.py`、`html_parser.py`、`mhtml_parser.py`、`image_parser.py` |
| `markitdown` | `MarkitdownParser`（微软 MarkItDown，多格式） | `docreader/parser/markitdown_parser.py` |
| `opendataloader` | `OpenDataLoaderParser`（PDF 版面分析，需 Java 11+，带 `check_available` 探测） | `docreader/parser/opendataloader_parser.py` |

### 新增步骤

1. 在 `docreader/parser/` 新建 `my_parser.py`，继承 `BaseParser`，实现 `parse_into_text(content: bytes) -> Document`；
2. **注册点：`docreader/parser/registry.py`** — 在 `_build_default_registry()` 中追加：

```python
reg.register(
    "my_engine",
    {"myext": MyParser},
    description="我的解析引擎",
    check_available=lambda overrides: (True, ""),   # 可选：依赖可用性探测
    unavailable_hint="缺依赖时给用户的提示",          # 可选
)
```

3. 若是给已有扩展名换实现，也可只往 `builtin` 的映射里加一行 `"ext": MyParser`；
4. 在 `docreader/tests/` 增加 unittest（参考 `test_parser_routing.py`），`uv run python -m unittest` 验证。

---

## 2. 新增分块策略（internal/infrastructure/chunker）

### 接口定义

分块没有 interface，而是**策略分层（tier）+ 包级函数变量覆盖**的模式。公共入口在 `internal/infrastructure/chunker/strategy.go`：

```go
// internal/infrastructure/chunker/strategy.go
// Strategy values for SplitterConfig.Strategy.
const (
    StrategyAuto      = "auto"
    StrategyHeading   = "heading"
    StrategyHeuristic = "heuristic"
    StrategyRecursive = "recursive"
    StrategyLegacy    = "legacy"
)

func Split(text string, cfg SplitterConfig) []Chunk
func SplitWithDiagnostics(text string, cfg SplitterConfig) ([]Chunk, *Diagnostics)
func SplitParentChild(text string, parentCfg, childCfg SplitterConfig) ParentChildResult
```

配置与结果类型在 `internal/infrastructure/chunker/splitter.go`：

```go
// internal/infrastructure/chunker/splitter.go
type Chunk struct {
    Content       string
    ContextHeader string
    Seq           int
    Start         int
    End           int
}

type SplitterConfig struct {
    ChunkSize    int
    ChunkOverlap int
    Separators   []string
    Strategy     string   // 空 = legacy（向后兼容）
    TokenLimit   int      // 以近似 token 数限制块大小，0 = 用 ChunkSize 字符数
    Languages    []string // 多语言启发式提示，空 = 自动检测
}
```

策略分发在 `runTier()`；heading / heuristic 两个实现通过包级函数变量在各自文件的 `init()` 中覆盖：

```go
// internal/infrastructure/chunker/strategy.go
func runTier(tier StrategyTier, text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
    switch tier {
    case TierHeading:
        return splitByHeadings(text, cfg, profile)
    case TierHeuristic:
        return splitByHeuristics(text, cfg, profile)
    case TierLegacy:
        return SplitText(text, cfg)
    }
    return SplitText(text, cfg)
}

var splitByHeadings = func(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
    return SplitText(text, cfg) // 被 heading_splitter.go 的 init() 覆盖
}
var splitByHeuristics = func(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
    return SplitText(text, cfg) // 被 heuristic_splitter.go 的 init() 覆盖
}
```

### 现有实现

| 策略 tier | 说明 | 文件 |
| --- | --- | --- |
| `TierHeading` | 按 Markdown 标题层级分块 | `internal/infrastructure/chunker/heading_hierarchy.go` 等 |
| `TierHeuristic` | 多语言启发式分块 | `internal/infrastructure/chunker/heuristic_splitter.go` |
| `TierLegacy`（=`recursive`） | 递归分隔符分块（原始实现） | `internal/infrastructure/chunker/splitter.go` 的 `SplitText()` |
| 校验器 | 每个 tier 输出经 `ValidateChunks` 验收，失败则沿链回落 | `internal/infrastructure/chunker/validator.go` |

### 新增步骤

1. 在 `internal/infrastructure/chunker/` 新建 `my_splitter.go`，实现 `func(text string, cfg SplitterConfig, profile *DocProfile) []Chunk`；
2. **注册点：`internal/infrastructure/chunker/strategy.go`** —
   - 增加策略常量（如 `StrategyMine = "mine"`）与新的 `StrategyTier`；
   - 在 `resolveChain`/`resolveChainWithProfile` 的 switch 中为新策略返回 tier 链（建议以 `TierLegacy` 兜底）；
   - 在 `runTier()` 中新增 case；
3. 调用方无需改动：知识库的 `chunking_config.strategy`（JSONB）经 `internal/application/service/knowledge.go` 的 `buildSplitterConfig` 传入；
4. 用 `SplitWithDiagnostics` 写单测验证 tier 选择与 `ValidateChunks` 验收行为。

---

## 3. 新增检索引擎（Retriever Engine）

### 接口定义

接口在 `internal/types/interfaces/retriever.go`（三层：引擎 → 仓储 → 服务 + 注册表）：

```go
// internal/types/interfaces/retriever.go
type RetrieveEngine interface {
    EngineType() types.RetrieverEngineType
    Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error)
    Support() []types.RetrieverType // 支持的检索类型（向量/关键词）
}

type RetrieveEngineRepository interface {
    Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error
    BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error
    EstimateStorageSize(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64
    DeleteByChunkIDList(ctx context.Context, indexIDList []string, dimension int, knowledgeType string) error
    DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error
    CopyIndices(ctx context.Context, sourceKnowledgeBaseID string,
        sourceToTargetKBIDMap map[string]string,
        sourceToTargetChunkIDMap map[string]string,
        targetKnowledgeBaseID string, dimension int, knowledgeType string) error
    DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, knowledgeType string) error
    BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error
    BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error
    RetrieveEngine
}

type RetrieveEngineRegistry interface {
    Register(indexService RetrieveEngineService) error
    GetRetrieveEngineService(engineType types.RetrieverEngineType) (RetrieveEngineService, error)
    GetAllRetrieveEngineServices() []RetrieveEngineService
    GetByStoreID(storeID string) (RetrieveEngineService, error)
}
```

引擎类型枚举在 `internal/types/retriever.go`：

```go
// internal/types/retriever.go
const (
    PostgresRetrieverEngineType        RetrieverEngineType = "postgres"
    ElasticsearchRetrieverEngineType   RetrieverEngineType = "elasticsearch"
    InfinityRetrieverEngineType        RetrieverEngineType = "infinity"
    ElasticFaissRetrieverEngineType    RetrieverEngineType = "elasticfaiss"
    QdrantRetrieverEngineType          RetrieverEngineType = "qdrant"
    MilvusRetrieverEngineType          RetrieverEngineType = "milvus"
    WeaviateRetrieverEngineType        RetrieverEngineType = "weaviate"
    DorisRetrieverEngineType           RetrieverEngineType = "doris"
    SQLiteRetrieverEngineType          RetrieverEngineType = "sqlite"
    TencentVectorDBRetrieverEngineType RetrieverEngineType = "tencent_vectordb"
    OpenSearchRetrieverEngineType      RetrieverEngineType = "opensearch"
)
```

### 现有实现

均在 `internal/application/repository/retriever/` 下：`postgres/`（pgvector + BM25/ParadeDB）、`elasticsearch/v7/`、`elasticsearch/v8/`、`qdrant/`、`milvus/`、`weaviate/`、`doris/`、`sqlite/`（sqlite-vec + FTS5）、`tencentvectordb/`、`opensearch/`。

### 新增步骤

1. 在 `internal/types/retriever.go` 增加 `RetrieverEngineType` 常量；
2. 在 `internal/application/repository/retriever/myengine/` 新建包，实现 `RetrieveEngineRepository` 接口（可参考 `qdrant/` 或 `sqlite/`）；
3. **注册点：`internal/container/container.go` 的 `initRetrieveEngineRegistry()`** — 按 `RETRIEVE_DRIVER` 环境变量（逗号分隔）条件注册：

```go
// internal/container/container.go（节选）
retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
if slices.Contains(retrieveDriver, "postgres") {
    postgresRepo := postgresRepo.NewPostgresRetrieveEngineRepository(db)
    if err := registry.Register(
        retriever.NewKVHybridRetrieveEngine(postgresRepo, types.PostgresRetrieverEngineType),
    ); err != nil { ... }
}
```

   仿照上例为新引擎加分支，用 `retriever.NewKVHybridRetrieveEngine(repo, 引擎类型)` 包装后注册；
4. 若引擎需要独立部署，在 `docker-compose.dev.yml` 加一个带 profile 的服务（参考 `qdrant`/`opensearch`），并在 `.env.example` 补连接变量。

---

## 4. 新增模型 Provider（internal/models/provider）

### 接口定义

Provider 元数据接口 + 全局注册表在 `internal/models/provider/provider.go`：

```go
// internal/models/provider/provider.go
type ProviderName string // "openai" / "anthropic" / "aliyun" / "zhipu" / "deepseek" / ...

type Provider interface {
    // Info 返回服务商的元数据
    Info() ProviderInfo
    // ValidateConfig 验证服务商的配置
    ValidateConfig(config *Config) error
}

// Register 添加一个提供者到全局注册表
func Register(p Provider)
```

`ProviderInfo` 描述展示名、各模型类型（chat/embedding/rerank）的默认 BaseURL、是否需要鉴权、额外配置字段等。Chat 请求的差异化适配（endpoint 拼接、thinking 参数、鉴权头、工具调用元数据）由 `internal/models/chat/provider.go` 的内部适配器接口承担：

```go
// internal/models/chat/provider.go
type providerAdapter interface {
    Name() provider.ProviderName
    Matches(model string) bool
    Thinking() ThinkingStrategy
    ShapeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool)
    TransformMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage
    Endpoint(baseURL, modelID string, isStream bool) string
    Auth(req *http.Request, creds authCreds, body []byte)
    ForceRawHTTP() bool
    ExtractToolCallMetadata(raw json.RawMessage) types.ToolCallMetadata
    InjectToolCallMetadata(toolCall map[string]any, metadata types.ToolCallMetadata)
}
```

Embedding 与 Rerank 各自有独立接口：

```go
// internal/models/embedding/embedder.go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)
    GetModelName() string
    GetDimensions() int
    GetModelID() string
    EmbedderPooler
}

// internal/models/rerank/reranker.go
type Reranker interface {
    Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error)
    GetModelName() string
    GetModelID() string
}
```

### 现有实现

`internal/models/provider/provider.go` 中已定义 27 个 `ProviderName` 常量：openai、anthropic、aliyun、zhipu、openrouter、litellm、requesty、siliconflow、jina、generic、deepseek、gemini、volcengine、hunyuan、minimax、mimo、gpustack、moonshot、modelscope、qianfan、qiniu、longcat、lkeap、nvidia 等。具体 Provider 实现分布在 `internal/models/provider/` 下的各文件（如 `zhipu.go`、`gemini.go`、`hunyuan.go`、`generic.go`）；特殊 embedding 实现如 `internal/models/embedding/jina.go`、`volcengine.go`、`nvidia.go`。

### 新增步骤

1. **注册点一：`internal/models/provider/provider.go`** — 增加 `ProviderName` 常量；
2. 在 `internal/models/provider/` 新建 `myprovider.go`，实现 `Provider` 接口（`Info()` 给出默认 URL/支持的模型类型），并通过 `provider.Register(...)`（通常在 `init()` 或集中初始化处）挂入全局注册表——OpenAI 兼容协议的服务商到这一步即可用，chat 侧默认走通用 OpenAI 适配；
3. **注册点二（可选）：`internal/models/chat/provider.go`** — 若 API 协议有差异（非标 endpoint、特殊鉴权、thinking 字段），实现并注册一个 `providerAdapter`；
4. **注册点三（可选）**：需要专有 Embedding/Rerank 协议时，在 `internal/models/embedding/`、`internal/models/rerank/` 各加实现并接入其构造工厂；
5. 如需开箱即用的内置模型，补充 `config/builtin_models.yaml` 声明（启动时会同步进 `models` 表）。

---

## 5. 新增联网搜索引擎（internal/infrastructure/web_search）

### 接口定义

```go
// internal/types/interfaces/web_search.go
type WebSearchProvider interface {
    // Name returns the name of the provider
    Name() string
    // Search performs a web search
    Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error)
}
```

注册表是工厂映射（按需用租户参数实例化）：

```go
// internal/infrastructure/web_search/registry.go
type ProviderFactory func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error)

type Registry struct {
    factories map[string]ProviderFactory
    mu        sync.RWMutex
}

func (r *Registry) Register(id string, factory ProviderFactory)
func (r *Registry) CreateProvider(providerType string, params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error)
```

### 现有实现

`internal/infrastructure/web_search/` 目录：`duckduckgo.go`、`google.go`、`bing.go`、`tavily.go`、`ollama.go`、`baidu.go`、`searxng.go`、`keenable.go`、`zhipu.go`（另有 `proxy.go` 出站代理支持）。类型常量在 `internal/types/web_search_provider.go`（`WebSearchProviderTypeBing/Google/DuckDuckGo/Tavily/Ollama/Baidu/Searxng/Keenable/Zhipu`）。

### 新增步骤

1. 在 `internal/types/web_search_provider.go` 增加 `WebSearchProviderType` 常量；
2. 在 `internal/infrastructure/web_search/` 新建 `mysearch.go`，实现 `WebSearchProvider` 并暴露工厂 `func NewMySearchProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error)`；
3. **注册点：`internal/container/container.go` 的 `registerWebSearchProviders()`**：

```go
func registerWebSearchProviders(registry *infra_web_search.Registry) {
    registry.Register("duckduckgo", infra_web_search.NewDuckDuckGoProvider)
    registry.Register("google", infra_web_search.NewGoogleProvider)
    // ... 在此追加：
    registry.Register("mysearch", infra_web_search.NewMySearchProvider)
}
```

4. 前端的 provider 下拉与参数表单如需展示新引擎，同步 `frontend/` 相应配置页组件；租户配置持久化在 `web_search_providers` 表。

---

## 6. 新增数据源连接器（internal/datasource/connector）

> 目录内附有实现指南 `internal/datasource/CONNECTOR_IMPLEMENTATION_GUIDE.md`，可对照阅读。

### 接口定义

```go
// internal/datasource/connector.go
type Connector interface {
    // Type returns the connector type identifier (e.g., "feishu", "notion")
    Type() string

    // Validate verifies that the provided configuration is valid by testing
    // connectivity and checking credentials.
    Validate(ctx context.Context, config *types.DataSourceConfig) error

    // ListResources lists available resources that can be synced.
    // parentID 支持层级资源的懒加载："" 返回顶层，非空返回该资源的直接子节点。
    ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error)

    // ResolveResourceAncestors 为懒加载树的既有选中项解析祖先链（O(depth)）。
    ResolveResourceAncestors(
        ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
    ) ([]string, error)

    // FetchAll performs a full sync of the specified resources.
    FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error)

    // FetchIncremental performs an incremental sync based on the provided cursor.
    FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error)
}
```

可选的流式接口（大数据量分页 checkpoint，内存只驻留单条 item）：

```go
// internal/datasource/connector.go
type StreamHandler interface {
    Emit(ctx context.Context, item types.FetchedItem) error
    Checkpoint(ctx context.Context, cursor *types.SyncCursor) error
}

type StreamingConnector interface {
    Connector
    FetchStream(ctx context.Context, config *types.DataSourceConfig,
        cursor *types.SyncCursor, h StreamHandler) (*types.SyncCursor, error)
}
```

注册表同文件：`ConnectorRegistry`（`NewConnectorRegistry()` / `Register(connector)` / `Get(type)` / `List()`）；连接器的 UI 元数据（名称、AuthType、capabilities）在同文件的 `ConnectorMetadataRegistry` map 中。

### 现有实现

| 类型 | 目录 | 说明 |
| --- | --- | --- |
| `feishu` / `lark` | `internal/datasource/connector/feishu/` | 同一实现，`NewConnector(RegionFeishu / RegionLark)` 区分区域 |
| `notion` | `internal/datasource/connector/notion/` | 页面与数据库 |
| `yuque` | `internal/datasource/connector/yuque/` | 语雀 |
| `rss` | `internal/datasource/connector/rss/` | RSS 订阅 |

### 新增步骤

1. 在 `internal/datasource/connector/mysource/` 新建包，实现 `Connector`（大数据量建议同时实现 `StreamingConnector`），提供 `NewConnector()`；
2. **注册点一：`internal/container/container.go` 的 `initConnectorRegistry()`**：

```go
if err := registry.Register(mysourceConnector.NewConnector()); err != nil {
    errs = errors.Join(errs, fmt.Errorf("register mysource connector: %w", err))
}
```

3. **注册点二：`internal/datasource/connector.go` 的 `ConnectorMetadataRegistry`** — 增加类型常量（`internal/types` 的 `ConnectorTypeXxx`）与元数据条目（Name/Description/AuthType/Capabilities）；
4. 同步配置结构：`types.DataSourceConfig` 若需新增凭证字段，注意加密存储约定；前端数据源接入页按元数据渲染。

---

## 7. 新增 IM 平台适配器（internal/im）

### 接口定义

```go
// internal/im/adapter.go
type Platform string // "wecom" / "feishu" / "lark" / "slack" / "telegram" / "dingtalk" /
                     // "mattermost" / "wechat" / "qqbot" / "yunzhijia"

// Adapter is the interface every IM platform must implement.
type Adapter interface {
    // Platform returns the platform identifier.
    Platform() Platform

    // VerifyCallback verifies the signature/token of an incoming callback request.
    VerifyCallback(c *gin.Context) error

    // ParseCallback parses the raw IM callback request into a unified IncomingMessage.
    // Returns nil message for non-message events (e.g., URL verification).
    ParseCallback(c *gin.Context) (*IncomingMessage, error)

    // SendReply sends a reply back to the IM platform.
    SendReply(ctx context.Context, incoming *IncomingMessage, reply *ReplyMessage) error

    // HandleURLVerification handles the initial URL verification challenge.
    HandleURLVerification(c *gin.Context) bool
}
```

两个可选能力接口：

```go
// internal/im/adapter.go
// StreamSender：实现后 IM 服务将实时推送流式回答（如飞书流式卡片、Telegram 编辑消息）
type StreamSender interface {
    StartStream(ctx context.Context, incoming *IncomingMessage) (string, error)
    UpdateStreamContent(ctx context.Context, incoming *IncomingMessage, streamID string, fullContent string) error
    FinalizeStream(ctx context.Context, incoming *IncomingMessage, streamID string, finalContent string) error
    EndStream(ctx context.Context, incoming *IncomingMessage, streamID string) error
}

// FileDownloader：实现后，配置了 knowledge_base_id 的渠道会把文件消息入库
type FileDownloader interface {
    DownloadFile(ctx context.Context, msg *IncomingMessage) (io.ReadCloser, string, error)
}
```

适配器由工厂按渠道实例化（`internal/im/service.go`）：

```go
// internal/im/service.go
type AdapterFactory func(ctx context.Context, channel *IMChannel,
    msgHandler func(ctx context.Context, msg *IncomingMessage) error,
) (Adapter, context.CancelFunc, error)

func (s *Service) RegisterAdapterFactory(platform string, factory AdapterFactory)
```

### 现有实现

`internal/im/` 下每个平台一个子包：`wecom/`、`feishu/`（lark 复用，`feishu.NewFactory(RegionLark)`）、`slack/`、`telegram/`、`dingtalk/`、`mattermost/`、`wechat/`、`qqbot/`、`yunzhijia/`。

### 新增步骤

1. 在 `internal/im/adapter.go` 增加 `Platform` 常量；
2. 新建 `internal/im/myplatform/`，实现 `Adapter`（按需加 `StreamSender`/`FileDownloader`）与 `NewFactory() im.AdapterFactory`；
3. **注册点：`internal/container/container.go` 的 `registerIMAdapterFactories()`**：

```go
func registerIMAdapterFactories(imService *imPkg.Service) {
    imService.RegisterAdapterFactory("wecom", wecom.NewFactory())
    // ... 在此追加：
    imService.RegisterAdapterFactory("myplatform", myplatform.NewFactory())
    if err := imService.LoadAndStartChannels(); err != nil { ... }
}
```

4. 渠道配置持久化在 `im_channels` 表，会话映射在 `im_channel_sessions`；前端渠道管理页需增加对应平台的配置表单。

---

## 8. 新增 Agent 工具（internal/agent/tools）

### 接口定义

工具接口定义在 `internal/types/agent.go`：

```go
// internal/types/agent.go
type Tool interface {
    // Name returns the unique identifier for this tool
    Name() string

    // Description returns a human-readable description of what the tool does
    Description() string

    // Parameters returns the JSON Schema for the tool's parameters
    Parameters() json.RawMessage

    // Execute runs the tool with the given arguments
    Execute(ctx context.Context, args json.RawMessage) (*ToolResult, error)
}
```

运行时注册表在 `internal/agent/tools/registry.go`：

```go
// internal/agent/tools/registry.go
type ToolRegistry struct {
    tools             map[string]types.Tool
    maxToolOutputSize int
}

// RegisterTool adds a tool to the registry.
// 同名工具 first-wins，防止名称碰撞劫持（GHSA-67q9-58vj-32qx）。
func (r *ToolRegistry) RegisterTool(tool types.Tool)
func (r *ToolRegistry) GetTool(name string) (types.Tool, error)
func (r *ToolRegistry) ListTools() []string
```

### 现有实现

工具名常量集中在 `internal/agent/tools/definitions.go`：`thinking`、`todo_write`、`grep_chunks`、`knowledge_search`、`list_knowledge_chunks`、`query_knowledge_graph`、`get_document_info`、`database_query`、`data_analysis`、`data_schema`、`web_search`、`web_fetch`、skills 工具（`execute_skill_script`、`read_skill`）、wiki 工具（`wiki_read_page`、`wiki_write_page`、`wiki_replace_text`、`wiki_rename_page`、`wiki_delete_page`、`wiki_search`、`wiki_read_source_doc`、`wiki_flag_issue`、`wiki_read_issue`、`wiki_update_issue`）。实现文件与工具同名（如 `grep_chunks.go`、`knowledge_search.go`、`data_analysis.go`、`mcp_tool.go`——后者把 MCP 服务的远程工具包装成 `types.Tool`）。

### 新增步骤

1. 在 `internal/agent/tools/` 新建 `my_tool.go`，实现 `types.Tool` 四个方法（`Parameters()` 返回 JSON Schema；注意工具名 ≤ 64 字符的 OpenAI 限制，见 `definitions.go` 的 `maxFunctionNameLength`）；
2. **注册点一：`internal/agent/tools/definitions.go`** — 增加 `ToolMyTool = "my_tool"` 常量，并把工具加进 `AvailableToolDefinitions()`（UI 的可选工具列表，注释明确要求与已注册工具保持同步）；
3. **注册点二：Agent 引擎的工具装配处** — 在构建 `ToolRegistry` 的服务逻辑（Agent 会话初始化，按 Agent 配置的允许工具列表实例化并 `RegisterTool`）中加入新工具的构造；带资源清理需求时实现 `Cleanup`（`types.Cleanable`）；
4. 输出体量大的工具注意 `ToolRegistry` 的 `maxToolOutputSize` 截断行为；为工具编写 `_test.go`（同目录有大量参考，如 `grep_chunks_scope_test.go`）。

---

## 9. 新增存储后端（对象存储）

### 接口定义

文件服务接口在 `internal/types/interfaces/file.go`：

```go
// internal/types/interfaces/file.go
type FileService interface {
    CheckConnectivity(ctx context.Context) error
    SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error)
    SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error)
    GetFile(ctx context.Context, filePath string) (io.ReadCloser, error)
    GetFileURL(ctx context.Context, filePath string) (string, error)
    DeleteFile(ctx context.Context, filePath string) error
    CopyFile(ctx context.Context, srcPath string, tenantID uint64, knowledgeID string) (string, error)
}
```

多后端解析（租户级 `storage_backends` 表配置 → FileService 实例）经 `internal/types/interfaces/storagebackend.go`：

```go
// internal/types/interfaces/storagebackend.go
type StorageBackendService interface {
    Create(ctx context.Context, backend *types.StorageBackend) error
    Update(ctx context.Context, backend *types.StorageBackend) error
    Delete(ctx context.Context, tenantID uint64, id string) error
    SetDefault(ctx context.Context, tenantID uint64, id string) error
    Test(ctx context.Context, backend *types.StorageBackend) error
}

type StorageBackendResolver interface {
    ResolveFileService(ctx context.Context, tenant *types.Tenant, backendID, provider, localBaseDir string) (FileService, string, error)
    ResolveBackend(ctx context.Context, tenant *types.Tenant, backendID, provider string) (*types.StorageBackend, error)
}
```

### 现有实现

均在 `internal/application/service/file/`：

| provider | 文件 | 说明 |
| --- | --- | --- |
| `local` | `local.go` | 本地文件系统 |
| `minio` | `minio.go` | MinIO / S3 兼容 |
| `cos` | `cos.go` | 腾讯云 COS |
| `tos` | `tos.go` | 火山引擎 TOS |
| `s3` | `s3.go` | AWS S3 及兼容服务 |
| `obs` | `obs.go` | 华为云 OBS |
| `oss` | `oss.go` | 阿里云 OSS |
| `ks3` | `ks3.go` | 金山云 KS3 |

### 新增步骤

1. 在 `internal/application/service/file/` 新建 `mystore.go`，实现 `FileService` 全部方法（`CheckConnectivity` 用于前端"测试连接"按钮，即 `StorageBackendService.Test`）；
2. **注册点：`internal/application/service/file/factory.go` 的 `NewFileServiceFromStorageConfig()`** — 在 provider switch 中加 case：

```go
switch p {
case "local":  // NewLocalFileService(...)
case "minio":  // NewMinioFileService(...)
// ... 在此追加：
case "mystore":
    return NewMyStoreFileService(cfg), p, nil
default:
    return nil, p, fmt.Errorf("unsupported storage provider: %s", p)
}
```

3. 若新 provider 需要新的配置字段（endpoint/bucket/region 等），扩展 `internal/types` 中的 `StorageEngineConfig` / `StorageBackend.config`（JSONB）；
4. 前端存储后端管理页增加对应 provider 的表单项；租户配置落在 `storage_backends` 表（`provider` 列即 switch 的 key）。

---

## 附：扩展点速查表

| 扩展点 | 核心接口 | 接口文件 | 注册点 |
| --- | --- | --- | --- |
| 文档解析器 | `BaseParser.parse_into_text` | `docreader/parser/base_parser.py` | `docreader/parser/registry.py` `_build_default_registry()` |
| 分块策略 | tier 函数 `func(text, cfg, profile) []Chunk` | `internal/infrastructure/chunker/strategy.go` | 同文件 `runTier()` + 策略常量 |
| 检索引擎 | `RetrieveEngineRepository` | `internal/types/interfaces/retriever.go` | `container.go` `initRetrieveEngineRegistry()`（`RETRIEVE_DRIVER` 门控） |
| 模型 Provider | `Provider` / `providerAdapter` / `Embedder` / `Reranker` | `internal/models/provider/provider.go` 等 | `provider.Register()` + `internal/models/chat/provider.go` |
| 联网搜索 | `WebSearchProvider` | `internal/types/interfaces/web_search.go` | `container.go` `registerWebSearchProviders()` |
| 数据源连接器 | `Connector` / `StreamingConnector` | `internal/datasource/connector.go` | `container.go` `initConnectorRegistry()` + `ConnectorMetadataRegistry` |
| IM 适配器 | `Adapter`（+`StreamSender`/`FileDownloader`） | `internal/im/adapter.go` | `container.go` `registerIMAdapterFactories()` |
| Agent 工具 | `types.Tool` | `internal/types/agent.go` | `internal/agent/tools/definitions.go` + `ToolRegistry.RegisterTool` |
| 存储后端 | `FileService` | `internal/types/interfaces/file.go` | `internal/application/service/file/factory.go` switch |
