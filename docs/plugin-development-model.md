# 模型管理（Model）插件制作指南

> 本文面向要独立开发一个 **模型管理（Model Provider）扩展点插件** 的开发者。
> 它只依赖本仓库的 `pkg/pluginapi` SDK 和主仓的插件扫描机制，无需改动 WeKnora 主仓源码。
>
> 五类扩展点共用的插件骨架见本文第 2 节。本文只讲 Model 特有的部分。

## 1. Model 插件是做什么的

Model 插件负责把一个外部模型服务（自有模型、第三方 API 等）接入 WeKnora。与只暴露单一业务 RPC 的 Parser / Web Search 不同，Model 插件的**一个进程可以同时承载多达五种能力**：

| 能力 | capability 字符串 | 作用 |
|---|---|---|
| 对话 | `chat` | `Chat` / `ChatStream` |
| 向量化 | `embedding` | `Embed` / `BatchEmbed` |
| 重排序 | `rerank` | `Rerank` |
| 视觉语言模型 | `vllm` | `PredictVLM`（注意 capability 串是 `vllm`，不是 `vlm`） |
| 语音识别 | `asr` | `Transcribe` |

一个插件可实现全部或其中任意子集；未实现的能力把对应回调留 `nil` 即可，宿主调用时插件会返回「not implemented」。

## 2. 五类扩展点的统一骨架（速览）

| 项 | Model 插件取值 |
|---|---|
| `api_version` | `weknora.plugin/v1`（必填） |
| `id` | 必填，插件唯一 ID |
| `name` | 必填，插件显示名称 |
| `version` | 必填，插件版本 |
| `extension_type` | `model`（必填） |
| `protocol_version` | `v1`（必填） |
| `weknora_version` | 可选，宿主兼容范围 |
| `entrypoint` | 必填 |
| 环境变量 | `WEKNORA_PLUGIN_ADDR` |

> 完整字段说明（含 `capabilities`、`config_schema`、`permissions`、`metadata`）见 [数据源插件指南第 1 节](plugin-development-datasource.md#1-插件包结构)。

启动后必须实现两个统一服务：

- `PluginControl`：`Handshake` + `Health`。
- `Model`：本扩展点的模型协议（五种能力，见下）。

用 SDK 的 `ServeModel` 一行即可同时挂起这两个服务。

## 3. 核心协议：`ModelPlugin` 服务

Model 的 RPC 比其它四类多，但每个都很薄：

```text
rpc ModelInfo(ModelInfoRequest) returns (ModelInfoResponse)          // 回报能力列表
rpc ValidateConfig(ModelValidateRequest) returns (ModelValidateResponse)  // 校验配置
rpc Chat(ModelChatRequest) returns (ModelChatResponse)               // 对话
rpc ChatStream(ModelChatRequest) returns (stream ModelStreamResponse) // 流式对话
rpc Embed(ModelEmbedRequest) returns (ModelEmbedResponse)            // 单条向量化
rpc BatchEmbed(ModelBatchEmbedRequest) returns (ModelBatchEmbedResponse) // 批量向量化
rpc Rerank(ModelRerankRequest) returns (ModelRerankResponse)         // 重排序
rpc PredictVLM(ModelVLMRequest) returns (ModelVLMResponse)           // 视觉语言
rpc Transcribe(ModelASRRequest) returns (ModelASRResponse)           // 语音识别
```

## 4. SDK 侧如何实现：`ServeModel`

SDK 提供了 `ModelHandler` 结构体，你只需填充关心的回调，其余留 `nil`：

```go
package main

import (
    "context"
    "os"

    "github.com/Tencent/WeKnora/pkg/pluginapi"
)

type myModel struct{}

func (myModel) Chat(ctx context.Context, req pluginapi.ChatRequest) (pluginapi.ChatResult, error) {
    // req.Messages / req.Options 已由 SDK 解码好
    return pluginapi.ChatResult{Content: "hello"}, nil
}

func (myModel) Embed(ctx context.Context, text string) ([]float32, error) {
    return []float32{0.1, 0.2, 0.3}, nil
}

func (myModel) Rerank(ctx context.Context, query string, docs []string) ([]pluginapi.RerankResult, error) {
    return []pluginapi.RerankResult{{Index: 0, RelevanceScore: 0.9}}, nil
}

func main() {
    handler := pluginapi.ModelHandler{
        PluginID:     "example.mymodel",
        Capabilities: []string{"chat", "embedding", "rerank"}, // 只声明实现了的能力
        OnChat:       (myModel{}).Chat,
        OnEmbed:      (myModel{}).Embed,
        OnRerank:     (myModel{}).Rerank,
        // OnChatStream / OnBatchEmbed / OnPredictVLM / OnTranscribe 留 nil
    }
    addr := os.Getenv("WEKNORA_PLUGIN_ADDR")
    if err := pluginapi.ServeModel(context.Background(), addr, handler); err != nil {
        panic(err)
    }
}
```

### 各回调签名速查

| 回调 | 签名 |
|---|---|
| `OnValidateConfig` | `func(ctx, map[string]any) error` |
| `OnChat` | `func(ctx, ChatRequest) (ChatResult, error)` |
| `OnChatStream` | `func(ctx, ChatRequest, func(StreamChunk) error) error` |
| `OnEmbed` | `func(ctx, string) ([]float32, error)` |
| `OnBatchEmbed` | `func(ctx, []string) ([][]float32, error)` |
| `OnRerank` | `func(ctx, string, []string) ([]RerankResult, error)` |
| `OnPredictVLM` | `func(ctx, [][]byte, string) (string, error)` |
| `OnTranscribe` | `func(ctx, []byte, string) (string, error)` |
| `OnHealth` | `func(ctx) HealthResponse` |

要点：

- `ModelHandler.PluginID` 必须与 `plugin.yaml` 的 `id` 一致。
- `Capabilities` 数组声明实现了哪些能力，且**必须与 plugin.yaml 的 capabilities 一致**——声明了却没实现回调，会在调用时返回「not implemented」。
- 向量返回维度由 `Embed` 的返回长度决定（`BatchEmbed` 用首个向量的长度作为 `Dimensions`）。

### 流式对话（`OnChatStream`）

流式能力通过回调式 API 实现，插件不需要手写 gRPC stream：

```go
func (myModel) ChatStream(ctx context.Context, req pluginapi.ChatRequest, emit func(pluginapi.StreamChunk) error) error {
    // 逐块产出
    for _, chunk := range myStreamFromRemote(ctx, req) {
        if err := emit(pluginapi.StreamChunk{Content: chunk}); err != nil {
            return err // 下游取消
        }
    }
    return emit(pluginapi.StreamChunk{Done: true})
}
```

`StreamChunk` 除 `Content` 外还带 `ReasoningContent`（思考链内容）。对接 **reasoning（思考链）类模型**时，流式响应里模型会先吐 `reasoning_content` 再吐正文，插件应把两者都回传，否则宿主/前端会丢失思考过程：

```go
emit(pluginapi.StreamChunk{
    Content:          delta.Content,
    ReasoningContent: delta.ReasoningContent,
})
```

> 注意：`ChatResult`（非流式）和 `StreamChunk`（流式）都**必须**保留 `ReasoningContent`，且多轮对话中要把上一轮 assistant 的 `reasoning_content` 原样回传（部分供应商要求，否则以 400 拒绝）。

### 思考开关（`ChatOptions.Thinking`）

模型请求带 `ChatRequest.Options.Thinking`（`bool`）——宿主的统一思考开关，用户在对话页开启「深度思考」时为 `true`。插件据此决定是否给上游开启思考（参数格式由插件自行转换），并把上游返回的思考内容经 `ReasoningContent` 回传。配合 `model_ui.features` 声明 `thinking`，前端才会显示思考开关。

## 5. `plugin.yaml` 示例

```yaml
api_version: weknora.plugin/v1
id: example.mymodel
name: My Model Provider
version: 1.0.0
extension_type: model
protocol_version: v1
weknora_version: ">=0.7 <1.0"
entrypoint: ./mymodel

capabilities:
  - chat
  - embedding
  - rerank
  - vllm
  - asr

# model_ui 可选：声明附加能力（features）与宿主公共字段显示策略（host_fields）。
# 详见第 5.1 节。
model_ui:
  features:
    - thinking                     # 支持思考（reasoning）
    - streaming                    # 支持流式输出
  host_fields:
    base_url:
      mode: required               # 必填
      default: https://api.example.com/v1
    api_key:
      mode: required
    custom_headers:
      mode: optional               # 显示、可不填
    supports_vision:
      mode: hidden                 # 不显示
    max_concurrency:
      mode: optional

metadata:
  provider: mymodel                # 宿主路由用的 provider 名（与 id 不同时用这个）

config_schema:
  type: object
  properties:
    settings:
      type: object
      properties:
        response_format:            # 插件专属配置字段（示例，前端自动渲染成下拉框）
          type: string
          enum: [text, json]
          default: text
    credentials:
      type: object
      required: [api_key]
      properties:
        api_key:
          type: string
          secret: true
          description: 模型服务 API Key

permissions:
  network: allowlist                # 模型插件通常需要出站调用模型服务
  allowed_destinations:
    - "api.example.com"
```

### 运行方式与网络声明

插件通过 `entrypoint` 声明启动方式：可执行文件，或 `docker://镜像`。通过 `permissions.network` 声明网络范围：`none` 或 `allowlist` + `allowed_destinations`（模型插件通常需要出站调用模型服务，用 `allowlist`）。出站请求必须经 `pluginapi.NewPluginHTTPClient()` 发起（读取宿主注入的网络策略），不要用裸 `http.Client`，否则白名单不生效。

实际运行隔离方式（进程 / 容器、断网、出口代理）由宿主部署环境决定；插件作者不需要实现或配置 Runtime Agent / Docker Socket / 镜像校验。

### 5.1 `model_ui`：声明附加能力与公共字段显示策略

`model_ui` 是模型插件**可选**的 UI 声明块，让插件在不改动前端的前提下，控制自己在模型编辑页上呈现出的「能力」与「公共字段」。它由宿主在插件装载时读取，经 `/models/providers` 接口透传给前端。

#### `features` —— 附加能力（区别于 `capabilities`）

- `capabilities`：这个插件**提供哪些模型类型**（`chat` / `embedding` / `rerank` / `vllm` / `asr`）。
- `features`：这个模型**额外支持哪些能力**，与模型类型正交。

常见取值：

| feature | 含义 |
|---|---|
| `thinking` | 支持思考（reasoning / 思考链） |
| `streaming` | 支持流式输出 |
| `vision` | 支持视觉/多模态输入 |
| `tools` | 支持工具调用 |

#### `host_fields` —— 宿主公共字段的显示策略

宿主模型表单里有一批「公共字段」：Base URL、API Key、自定义 Header、视觉能力、并发上限。内置 provider 的显隐由前端硬编码，而**插件可以通过 `host_fields` 自行声明每个字段的显示方式**，无需修改前端。

支持的 key（snake_case，与 `config_schema` 约定一致）：

| key | 对应表单字段 |
|---|---|
| `base_url` | Base URL |
| `api_key` | API Key |
| `custom_headers` | 自定义 HTTP Header |
| `supports_vision` | 视觉能力开关 |
| `max_concurrency` | 并发上限 |

每个字段的 `mode` 取值为：

| mode | 含义 |
|---|---|
| `hidden` | 不显示 |
| `optional` | 显示，可不填 |
| `required` | 显示，参与必填校验（label 带红色星号） |
| `readonly` | 显示但不可修改 |

`default` 用于给字段注入默认值（当前仅 `base_url` 在新建模型、且该字段为空时自动填入）。

**行为约定：**

- 未声明 `host_fields` 的插件，公共字段沿用默认行为（`base_url` / `api_key` 显示、`custom_headers` 隐藏、`supports_vision` 随模型类型显示、`max_concurrency` 显示），**向后兼容已有插件**。
- `host_fields` 只对 `source=plugin` 的插件生效；内置 provider 的显隐逻辑保持不变，不受影响。
- 插件自定义字段仍走 `config_schema.settings` → `configFields` → 前端 `SchemaFieldInput` 动态渲染，与 `host_fields` 互不重叠。

**透传链路**：`Manifest.model_ui` → `ExternalModelInfo` → `ModelProviderDTO`（`features` + `hostFields`）→ 前端 `ModelProviderOption`。插件只需在 manifest 的 `model_ui` 中声明，宿主会把这些元数据随 `/models/providers` 接口一并透传给前端。

## 6. 宿主如何调用模型插件（重要：调用期解析机制）

模型插件与其他四类的一个关键差异是：**宿主不会缓存一个固定的 gRPC client**，而是**每次调用时动态解析**。

原因是模型插件承载有状态的长连接能力，且可能被健康监控重启。宿主为插件注册的是一个「调用期解析回调」，每次模型调用时：

1. 通过 admission control 申请一次调用租约（并发限制 + drain 检查）；
2. 读取**当前**运行时连接（插件重启后自动拿到新连接，不会用到已关闭的旧 client）；
3. 注入租户上下文（`WithInvocationContext`）；
4. 调用结束后释放租约（`release`，流式场景在流结束后释放）。

> 对你的意义：插件进程重启后，宿主会自动恢复调用，**插件作者无需做任何特殊处理**；但也意味着插件实现应当是**无状态或能承受重启**的（重启期间的请求会失败一次并走宿主重试）。

### 6.1 模型实例上下文（ModelContext）：一个进程服务多个模型

一个插件进程通常会服务**同一个 provider 下的多个模型实例**——例如一个「OpenAI 兼容」插件（`base_url` + `api_key` 只有一份），用户却在 WeKnora 里同时配置了 `gpt-4o`（chat）、`text-embedding-3-large`（embedding）、`text-embedding-3-small`（embedding）。这些实例共享同一份插件级配置，但 **model_id 各不相同**，插件必须能区分「这次调用是给哪个模型实例」。

为此，宿主在每次调用时通过 gRPC metadata 注入模型身份（与 `InvocationContext` 同一种机制，不修改 v1 protobuf）：

| 字段 | 含义 |
|---|---|
| `ModelID` | 宿主库中该模型记录的**唯一 ID**（全局稳定，可用作日志/审计/计费的分组键） |
| `ModelName` | 模型名（如 `gpt-4o`、`text-embedding-3-large`） |

插件侧读取方式分两类：

- **`Chat` / `ChatStream`**：SDK 已自动读取并填入 `ChatRequest.ModelID` / `ChatRequest.ModelName`，直接使用即可。
- **`Embed` / `Rerank` / `PredictVLM` / `Transcribe`**：这些回调签名里没有 model 参数，需要插件自己调用 `pluginapi.ModelContextFromContext(ctx)` 读取：

```go
func (myModel) Embed(ctx context.Context, text string) ([]float32, error) {
    mc := pluginapi.ModelContextFromContext(ctx)
    // mc.ModelID / mc.ModelName —— 例如按 ModelName 路由到不同的底层模型
    _ = mc
    return embedWith(ctx, mc.ModelName, text)
}
```

> **边界**：`ModelContext` 只传模型身份（id/name），**不传 `base_url` / `api_key`**——那两者是插件级 `config`（见第 5 节），由插件自己在进程内维护。因此 v1 的「一个进程多实例」是「共享同一 `base_url` + `api_key`、`model_id` 不同」的场景（即同一厂商的多模型）；「不同租户各自持有不同 key」这类更细粒度隔离不在 v1 范围内。

### 6.2 配置传递与缓存（重要）

`base_url` / `api_key` 只在**保存模型时**通过 `ValidateConfig` RPC 传入**一次**，`Chat` / `ChatStream` 调用时**不携带**。因此插件必须在 `OnValidateConfig` 里缓存，后续调用时按 `ModelID` / `ModelName` 取回。宿主传入的 config 是一个**扁平 map**：

```go
map[string]any{
    "model_id":   model.ID,               // 宿主库中该模型记录的唯一 ID（可用于精确缓存）
    "model_name": model.Name,             // 用户填的「模型名称」
    "base_url":   model.Parameters.BaseURL,
    "api_key":    model.Parameters.APIKey,
    // ... 其余是 extra_config 的扁平键值
}
```

缓存建议：以 `model_id` 为主 key、`model_name` 为辅 key，并保留一份「最近一次」作为全局兜底（同一 provider 下多个模型通常共享同一份 `base_url` + `api_key`）。

SDK 已提供现成的 `pluginapi.ConfigStore` 帮你完成这段缓存，无需手写：

```go
type myModel struct {
    configs *pluginapi.ConfigStore
}

func (m *myModel) validateConfig(_ context.Context, cfg map[string]any) error {
    m.configs.PutFromValidate(cfg) // 一行缓存
    return nil
}

func (m *myModel) chat(_ context.Context, req pluginapi.ChatRequest) (pluginapi.ChatResult, error) {
    apiKey := m.configs.GetString(req.ModelID, req.ModelName, "api_key")
    baseURL := m.configs.GetString(req.ModelID, req.ModelName, "base_url")
    // ... 用 apiKey / baseURL 调用上游模型服务
}
```

`ConfigStore` 提供 `NewConfigStore` / `PutFromValidate` / `Get` / `GetString` 四个方法，内部线程安全。`Embed` / `Rerank` / `PredictVLM` / `Transcribe` 等回调签名里没有 model 参数时，先用 `pluginapi.ModelContextFromContext(ctx)` 拿到 `ModelID` / `ModelName`，再传入 `Get` / `GetString`。

> 注意：`ConfigStore` 是进程内缓存，**插件进程重启后会清空**。此时需等待宿主再次「保存模型」触发 `ValidateConfig` 恢复缓存——插件无需特殊处理，宿主侧 resolver 会保证重启后的首次调用能拿到新的运行时连接。

### 6.3 模型名 ≠ provider 名（常见误区）

- `metadata.provider`（如 `openai`）是**宿主路由用**的 provider 标识，宿主按它找到对应的插件 resolver，**不会**发给上游模型服务。
- **模型名**是用户在前端「模型名称」输入框填写的，经 `ModelContext.ModelName` 传给插件。插件应把它**原样**作为 OpenAI 兼容请求的 `model` 字段发给上游（如 `gpt-4o`、`text-embedding-3-large`）。

若用户把 provider 名误填成模型名（例如填了 `openai` 而非 `gpt-4o`），上游 API 会返回 400「invalid model」。插件可自行校验模型名，或在文档/前端提示用户填写真实模型名。

## 7. 校验与验证

SDK 提供 `RunModelConformance`，可对模型插件做协议级冒烟测试（与 DataSource / Parser / WebSearch 三者对齐）。它依次：

1. 握手回报 `extension_type=model` 且 `protocol_version=v1`；
2. 健康检查返回非空 state；
3. 读取 `ModelInfo` 声明的能力列表；
4. 对**每个声明的能力**发一个空请求，校验响应信封无 error。

要点：

- **只测声明的能力**：未声明的能力会被跳过，不会误判为失败；
- **声明了但返回 error 记为失败**：例如声明 `embedding` 却没实现 `OnEmbed`（返回「not implemented」），该能力会被标记失败并写入 `Errors`。

用法：

```go
report := pluginapi.RunModelConformance(ctx, controlClient, modelClient)
// report.HandshakeOK / report.HealthOK / report.ChatOK / report.EmbeddingOK ...
// report.RerankOK / report.VLMOK / report.ASROK / report.Errors
```

建议流程：本地起 server（`ServeModel`）→ 跑 `RunModelConformance` → 全部通过后再放入 `WEKNORA_PLUGIN_DIR_MODEL` 交给宿主装载。

补充：还需额外验证**重启恢复**（kill 进程 → 重启 → 再次调用）。插件作者只需确保自己的实现无状态或能承受重启，宿主会在插件重启后自动恢复调用。

## 8. 与宿主的关系（边界）

Model 插件**只负责「模型能力的实际调用」**（把请求发给模型服务、把结果返回），不负责：

- 模型配置的存储与管理——宿主管理；
- 知识库检索、融合、切分——其他扩展点 / 宿主的职责；
- 租户校验、权限、限流——宿主在处理调用时统一注入。

插件内部可自行管理模型服务的连接池、重试、密钥轮换等，宿主不关心。

## 9. 部署注意

### 9.1 双平台二进制

`plugin.yaml` 的 `entrypoint` 通常写**无后缀**名字（如 `./my-model-plugin`）。宿主在 Linux / WSL 下执行这个无后缀文件，在 Windows 下执行 `.exe`。因此插件目录里建议**同时提供两个二进制**：

```
my-model-plugin/          ← Linux 可执行文件（无后缀）
my-model-plugin.exe       ← Windows 可执行文件
```

### 9.2 交叉编译

纯 Go 插件（只依赖 `pkg/pluginapi`）在 Windows 上交叉编译 Linux 二进制时，务必加 `CGO_ENABLED=0`，否则 `runtime/cgo` 会因缺 Linux 头文件而失败：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o my-model-plugin .
```

### 9.3 `go.mod` 的 `replace` 相对路径

独立插件仓库通过 `replace github.com/Tencent/WeKnora => ../path/to/weknora` 指向本地源码。**该相对路径是相对于插件 `go.mod` 所在目录**——插件目录层级一旦变化（例如从 `plugins/model/` 移到 `plugins/model/my-plugin/`），`../..` 就要相应变成 `../../..`，否则 `go build` 报 `replacement directory ... does not exist`。
