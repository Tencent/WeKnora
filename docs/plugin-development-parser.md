# 文档解析（Parser）插件制作指南

> 本文面向要独立开发一个 **文档解析（Parser）扩展点插件** 的开发者。
> 它只依赖本仓库的 `pkg/pluginapi` SDK 和主仓的插件扫描机制，无需改动 WeKnora 主仓源码。
>
> 五类扩展点共用的插件骨架（`plugin.yaml` 字段、`PluginControl` 握手/健康、`WEKNORA_PLUGIN_ADDR`、目录环境变量、构建与装载）见本文第 2 节。本文只讲 Parser 特有的部分。

## 1. Parser 插件是做什么的

Parser 插件负责把一种或多种文件格式解析成结构化文本，供知识库切分、向量化。它对应主仓的 `ParserRegistry`，一个插件进程可以同时声明支持多种文件类型。

宿主在「文档解析」阶段，按文件扩展名 / 类型找到对应的 Parser 插件，把文件原始字节交给插件，插件返回可读的文本结果。

## 2. 五类扩展点的统一骨架（速览）

Parser 插件与其他四类一样，遵循同一套发现与生命周期协议：

| 项 | Parser 插件取值 |
|---|---|
| `api_version` | `weknora.plugin/v1`（必填） |
| `id` | 必填，插件唯一 ID |
| `name` | 必填，插件显示名称 |
| `version` | 必填，插件版本 |
| `extension_type` | `parser`（必填） |
| `protocol_version` | `v1`（必填） |
| `weknora_version` | 可选，宿主兼容范围，如 `>=0.7 <1.0` |
| `entrypoint` | 必填，可执行文件或命令 |
| 环境变量 | `WEKNORA_PLUGIN_ADDR`（宿主分配的监听地址） |

> 完整字段说明（含 `capabilities`、`config_schema`、`permissions`、`metadata`）见 [数据源插件指南第 1 节](plugin-development-datasource.md#1-插件包结构)。

启动后必须实现两个统一服务：

- `PluginControl`：`Handshake`（回报插件 ID、`extension_type=parser`、能力、版本）+ `Health`（健康上报）。
- `Parser`：本扩展点特有的解析协议（见下）。

> 用 SDK 的 `ServeParser` 一行即可同时挂起这两个服务，无需手写 gRPC 注册。

## 3. 核心协议：`ParserPlugin` 服务

Parser 扩展点只有一个核心 RPC：

```text
rpc Parse(ParserRequest) returns (ParserResponse)
```

### 3.1 请求 `ParserRequest`（SDK 结构体字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| `FileContent` | `[]byte` | 文件原始字节 |
| `FileName` | `string` | 文件名 |
| `FileType` | `string` | 文件类型 / 扩展名 |
| `URL` | `string` | 可选，文件来源 URL |
| `Title` | `string` | 可选，文档标题 |
| `ParserEngine` | `string` | 宿主要求使用的解析引擎名 |
| `RequestID` | `string` | 请求追踪 ID |
| `ParserEngineOverrides` | `map[string]string` | 引擎级覆盖配置 |

### 3.2 响应 `ParserResponse`（SDK 结构体字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| `MarkdownContent` | `string` | **主输出**：解析出的 Markdown 文本 |
| `ImageRefs` | `[]ParserImageRef` | 文档中的图片引用（含图片数据/存储键） |
| `ImageDirPath` | `string` | 图片目录路径 |
| `Metadata` | `map[string]string` | 附加元数据 |
| `IsAudio` | `bool` | 是否为音频文档 |
| `AudioData` | `[]byte` | 音频数据（当 `IsAudio=true`） |
| `Error` | `string` | 解析失败信息，非空表示失败 |

`ParserImageRef` 字段（`ImageRefs` 的元素，文档包含图片时使用）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Filename` | `string` | 图片文件名 |
| `OriginalRef` | `string` | 原始引用（如 Markdown 中的图片链接） |
| `MIMEType` | `string` | 图片 MIME 类型 |
| `StorageKey` | `string` | 存储键（图片已存入对象存储时） |
| `ImageData` | `[]byte` | 图片原始数据 |
| `IsOriginal` | `bool` | 是否为原始图片 |

关键语义约束（需插件自觉遵守，宿主不强制校验）：

宿主在下游只认非空 `Error`（`Error != ""` 即判定解析失败并透出错误），不会自动拦截「未声明类型」「空内容」或「非 Markdown 残留」。因此这三条由插件自己保证：

1. **只认 `metadata.file_types` 声明的类型**：对未声明的类型应返回错误，而不是静默输出错误文本。
2. **主输出是 Markdown**：`MarkdownContent` 必须是可直接切分、向量化的 Markdown 文本，不能是二进制或富格式残留。
3. **错误要显式**：解析失败必须返回非空 `Error`（或让 `OnParse` 返回 error），不能返回空文本让宿主误以为「成功解析了空文档」。

> 建议把这三条写成插件的 `main_test.go` 自测，写法见第 6 节。

## 4. SDK 侧如何实现：`ServeParser`

SDK 提供了 `ParserHandler` 结构体，你只需填充回调，再调用 `ServeParser`：

```go
package main

import (
    "context"
    "os"

    "github.com/Tencent/WeKnora/pkg/pluginapi"
)

type myParser struct{}

func (myParser) Parse(ctx context.Context, req pluginapi.ParserRequest) (pluginapi.ParserResponse, error) {
    // req.FileContent: 原始文件字节；req.FileName: 文件名；req.FileType: 扩展名
    md, err := extractMarkdown(req.FileContent, req.FileName)
    if err != nil {
        return pluginapi.ParserResponse{}, err // 显式报错，宿主会透传给用户
    }
    return pluginapi.ParserResponse{MarkdownContent: md}, nil
}

func main() {
    handler := pluginapi.ParserHandler{
        PluginID: "example.mypdfparser",
        OnParse:  (myParser{}).Parse,
    }
    // WEKNORA_PLUGIN_ADDR 由宿主注入，例如 unix:///tmp/plugin.sock 或 127.0.0.1:port
    addr := os.Getenv("WEKNORA_PLUGIN_ADDR")
    if err := pluginapi.ServeParser(context.Background(), addr, handler); err != nil {
        panic(err)
    }
}
```

要点：

- `ParserHandler.PluginID` 必须与 `plugin.yaml` 的 `id` 一致。
- `OnParse` 为 nil 时，对应调用返回 `Error: "parser handler is not implemented"`。
- `Handshake` / `Health` 由 `ParserHandler` 自动实现，你无需手写。

## 5. `plugin.yaml` 示例

```yaml
api_version: weknora.plugin/v1
id: example.mypdfparser
name: My PDF Parser
version: 1.0.0
extension_type: parser
protocol_version: v1
weknora_version: ">=0.7 <1.0"
entrypoint: ./mypdfparser            # 可执行文件路径

capabilities:
  - parse                            # 能力标识（握手回显用）；支持的文件类型放 metadata.file_types

metadata:
  engine_name: mypdfparser           # 供宿主注册与展示的引擎名
  file_types:                        # 必填：声明支持的文件扩展名，缺省会校验失败
    - pdf
    - docx

config_schema:                       # 可选；外部 Parser 的设置页会按此生成表单
  type: object
  properties:
    settings:
      type: object
      properties:
        extract_images:
          type: boolean
          title: 是否提取图片中的文字
          default: false
        timeout:
          type: integer
          title: 解析超时时间（秒）
          default: 60
    credentials:
      type: object
      properties:
        api_key:
          type: string
          title: 服务 API Key
          secret: true

permissions:
  network: none                      # 解析插件一般无需联网，用 none 最安全
```

### 运行方式与网络声明

插件通过 `entrypoint` 声明自己的启动方式：可执行文件（相对 `plugin.yaml` 目录），或 `docker://镜像` 走容器运行。插件通过 `permissions.network` 声明自己需要的最大网络范围：`none`（默认，离线）或 `allowlist` + `allowed_destinations`。解析插件通常无需联网，用 `none` 最安全；确需出站时，请求必须经 `pluginapi.NewPluginHTTPClient()` 发起，不要用裸 `http.Client`——否则 `permissions.network` 白名单不生效。

插件的实际运行隔离方式（进程 / 容器、是否断网、出口代理、Socket 权限交接）由宿主部署环境统一负责。插件作者不需要实现或配置 Runtime Agent、Docker Socket 或镜像校验，只需正确声明 Manifest 并使用 SDK 的受控客户端。

### 必填校验

- `metadata.file_types` **不能为空**——宿主装载校验明确要求 parser 插件必须声明至少一个文件类型，否则插件无法通过装载。

### 关于 `engine_name` 与配置 Schema

- **`metadata.engine_name` 是全局唯一注册键**：不能与内置引擎（`simple`、`builtin`、`anydoc`、`weknoracloud`、`mineru`、`mineru_cloud`、`paddleocr_vl`、`paddleocr_vl_cloud`）或其他插件的 `engine_name` 重名，否则 `RegisterEngine` 会以 `already registered` 拒绝装载。不填 `engine_name` 时回退为插件 `id`（如 `example.mypdfparser`），可用但建议显式声明语义化引擎名。
- **外部 Parser 使用 `config_schema` 声明配置**：宿主会把 Schema 随引擎元数据返回，Parser 设置页据此动态渲染 `settings` 和 `credentials`。配置按稳定的插件 `id` 保存，多个插件可以使用同名字段而不会冲突。
- **运行时仍复用 v1 `ParserEngineOverrides`**：宿主仅在调用当前外部 Parser 时，取该插件自己的已保存配置并写入 `ParserRequest.ParserEngineOverrides`。标量会转成字符串（`true`、`60`），数组/对象使用 JSON 字符串；插件应按自身 Schema 解析。每次上传提供的 per-upload overrides 优先级更高，可以覆盖租户保存值。
- **统一使用 `config_schema`**：插件用 `settings`、`credentials`（Retriever 额外允许 `index_config`）分区表达配置，字段用 `type`、`title`、`description`、`default`、`enum`、`secret` 描述。`secret` 标记只允许出现在 `credentials` 分区，否则装载校验会拒绝。

## 6. 校验与验证

SDK 提供 conformance 测试入口 `RunParserConformance`，做协议级**冒烟自检**，覆盖三项：

- 握手回报 `extension_type=parser`、`protocol_version=v1`；
- 健康检查返回非空 `state`；
- 一次 `Parse` 调用（请求由调用方自己构造）能成功返回。

它**不会**自动校验「未声明类型报错」「空内容不静默」等语义约束——SDK 侧拿不到 `plugin.yaml`，不知道插件声明了哪些类型。这三条应写进插件自己的单元测试。以第 4 节的 `myParser` 为例，`main_test.go` 至少覆盖三个断言：

```go
func TestParseRejectsUndeclaredType(t *testing.T) {
    _, err := (myParser{}).Parse(context.Background(), pluginapi.ParserRequest{
        FileContent: []byte("raw"), FileType: "xyz",
    })
    if err == nil { t.Fatal("expected error for undeclared file type") }
}

func TestParseRejectsEmptyContent(t *testing.T) {
    _, err := (myParser{}).Parse(context.Background(), pluginapi.ParserRequest{
        FileContent: []byte("  \n"), FileType: "pdf",
    })
    if err == nil { t.Fatal("expected error for empty content") }
}

func TestParseReturnsMarkdown(t *testing.T) {
    resp, err := (myParser{}).Parse(context.Background(), pluginapi.ParserRequest{
        FileContent: []byte("# hi"), FileType: "pdf",
    })
    if err != nil || resp.MarkdownContent == "" { t.Fatal("expected markdown output") }
}
```

建议流程：本地起 server → 跑 conformance → 跑插件单测 → 都通过后再放入 `WEKNORA_PLUGIN_DIR_PARSER` 指向的目录交给宿主装载。

## 7. 宿主侧如何被使用

加载后，宿主会把 Parser 插件注册为一个普通的文档解析引擎，解析链路无需按插件 ID 增加分支。

## 8. 与宿主的关系（边界）

Parser 插件**只负责「字节 → 文本」**，不负责：

- 文本切分（chunking）——宿主在解析后统一处理；
- 向量化 / 索引——那是 Retriever / embedding 的职责；
- 数据源抓取——那是 DataSource 插件的职责。

插件内部可以自行决定解析实现（调用 PDF 库、OCR、调用自己的解析服务等），宿主不关心，只要协议输出是纯文本。
