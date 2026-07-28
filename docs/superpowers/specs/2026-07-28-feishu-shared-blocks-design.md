# 飞书 connector 共享逻辑提取与云盘 docx Blocks 路径设计

日期：2026-07-28
分支：feat/datasource-feishu-drive
范围：`internal/datasource/connector/feishu/`

## 背景与目标

`connector.go`（wiki）已 1100+ 行，wiki/drive 公共逻辑（错误分类、配置解析、统计器、checkpoint 常量、工具函数）定义在 wiki 文件里，drive 跨文件复用。同时 wiki 的 docx 走 Blocks API 转 Markdown（PR #2087，`6492bf6a`），而 drive 的 docx 仍只走导出 API。

目标（最小范围）：

1. 拆出 `shared.go`，纯搬运 wiki/drive 公共逻辑，不改行为。
2. `fetchDocxWithBlocks` 从 `*Connector` 方法改为包级函数，drive 的 docx 也走「Blocks API 优先、导出回退」路径。

非目标：不改 `doc/sheet/bitable` 的导出路径；不写存量数据迁移；不动 `client.go`/`blocks.go`/`markdown.go`/`region.go`/`types.go`。

## 关键决策

| 决策点 | 结论 |
|---|---|
| 范围 | 最小范围：shared.go 拆分 + drive docx Blocks 路径 |
| 子项 metadata 父级 key | 保守沿用 `parent_node_token`（drive 值 = file.Token），不碰下游依赖 |
| 存量迁移策略 | 跟随知识库 PR #2087 的做法：不写迁移代码；未变更文档保持旧形态，编辑/全量重同步时自然切换 |
| 分隔符常量 | `feishuWikiNodeResourceSeparator` 保留原名，避免连带改名 |

## 架构：文件结构

```
internal/datasource/connector/feishu/
├── client.go            # API client（不动）
├── blocks.go            # Blocks API 拉取（不动）
├── markdown.go          # blocks → Markdown（不动）
├── region.go            # Region 定义（不动）
├── types.go             # 类型定义（不动，导出映射表留在这里）
├── shared.go            # 【新增】wiki/drive 公共方法
├── connector.go         # 【瘦身】wiki 专有逻辑
└── drive_connector.go   # 【瘦身】drive 专有逻辑
```

### shared.go 内容（从 connector.go 纯搬运）

| 分组 | 内容 |
|---|---|
| 流式同步常量 | `feishuStreamCheckpointInterval`、`feishuStreamCheckpointMaxInterval` |
| 统计器 | `fetchTally`、`newFetchTally` |
| 错误分类 | `reFeishuErrorCode`、`feishuErrorCode`、`feishuFailure`、`feishuErrorItemMeta` |
| 文档类型判断 | `isSupportedDocType` |
| 配置解析 | `parseFeishuConfig` |
| 工具函数 | `parseFeishuTimestamp`、`sanitizeFileName`、`truncateUTF8` |
| 分隔符常量 | `feishuWikiNodeResourceSeparator` |
| 附件/图片规则 | `parseableAttachmentExts`、`minAttachmentBytes`、`supportedImageExt` |
| docx Blocks 拉取 | `docxFetchInput` + 改造后的 `fetchDocxWithBlocks` |

### connector.go（wiki 专有）保留

`Connector`、`NewConnector`、`ListResources`、`ResolveResourceAncestors`、`FetchAll`/`FetchIncremental`/`FetchStream`、`fetchNodeContent`、`fetchViaExport`、`fetchDriveFile`、`makeWikiNodeResourceID`/`parseWikiResourceID`、`wikiNodeToResource`、`appendWikiNodeListFailureItems`、`toSyncCursor`（feishuCursor）。

### drive_connector.go 保留

`DriveConnector` 及全部现有逻辑；仅 `fetchDriveFileContent` 的 `docx` 分支从批量导出 case 拆出，改调 shared 的 `fetchDocxWithBlocks`。另需新增 `multimodalEnabled` 参数传递（对齐 wiki 的 `fetchNodeContent`）。

## 组件契约

```go
// docxFetchInput 是 wiki / drive 两种来源对一个 docx 文档的统一描述。
type docxFetchInput struct {
    docToken          string // WeKnora 侧 external_id：wiki=node.NodeToken，drive=file.Token
    objToken          string // 飞书 docx 文档 token：wiki=node.ObjToken，drive=file.Token
    title             string
    url               string
    resourceID        string
    editTime          time.Time
    baseMeta          map[string]string
    multimodalEnabled bool
}

func fetchDocxWithBlocks(ctx context.Context, client *Client, in docxFetchInput) ([]*types.FetchedItem, error)
```

### wiki 调用点（connector.go `fetchNodeContent`，case "docx"）

- `docToken: node.NodeToken`，`objToken: node.ObjToken`，`title: node.Title`，`url: c.region.wikiURL(node.NodeToken)`，其余原样传入。

### drive 调用点（drive_connector.go `fetchDriveFileContent`，docx 单独 case）

- `docToken: file.Token`，`objToken: file.Token`，`title: file.Name`，`url: file.URL`，`baseMeta` 复用现有 drive 的（`channel=feishu_drive` / `lark_drive`），`multimodalEnabled` 由 `config.MultimodalEnabled` 传入（签名需新增该参数，调用方 FetchAll/FetchIncremental/FetchStream 三处同步更新）。

### 函数体行为（与现 wiki 实现一致，仅字段来源换成 in）

1. `ListDocumentBlocks(in.objToken)` 失败 → 回退导出路径，**不**设置 `ReplacesSubtree`。
2. 渲染为空 Markdown → 回退导出路径。
3. 成功 → 主项 `ExternalID=in.docToken`、`ReplacesSubtree=true`；附件子项 `SubtreeChildID(in.docToken, "file", token)`、图片子项 `SubtreeChildID(in.docToken, "image", token)`；子项 metadata 沿用 `parent_node_token`（值=in.docToken）。
4. `SubtreeKeep` 语义不变；服务侧 `sweepStaleSubtree` 按 `in.docToken#` 前缀清扫，wiki/drive 通用（已验证 connector 无关）。

### 回退导出实现

`fetchDocxWithBlocks` 内部回退时直接调 `client.ExportAndDownload(in.objToken, "docx")` 并组装 item（逻辑同现有 `fetchViaExport`，字段来自 `in`）。现有 `fetchViaExport`（接收 wikiNode）保留在 connector.go 供 `doc/sheet/bitable` 使用。

## 错误处理与兼容性

错误处理完全沿用 wiki 现有语义：

- Blocks API 失败 → 日志 + 回退导出，不设置 `ReplacesSubtree`（避免清扫上一轮成功的子项）；
- 导出失败 → 返回 error，FetchStream 记为 failed item，不推进 cursor，下轮重试；
- 附件/图片下载失败 → 降级为 error item，主项与其他附件不受影响，`keep` 保留下轮不误删；
- 空渲染 → 回退导出。

兼容性：

- wiki 行为零变化：纯搬运 + 签名变化，现有测试全绿是回归底线；
- drive 的 ExternalID 不变（`file.Token`），已同步知识不会因 external_id 变化产生重复；
- drive 子项 ID 为 `fileToken#file#xxx` / `fileToken#image#xxx`，与 wiki 子项（`nodeToken#...`）天然不冲突；
- `feishuDriveCursor` 格式不变，无需迁移 cursor。

## 测试

回归（必须全绿，不动用例）：

- `go test ./internal/datasource/connector/feishu/...`（golden/stream/convergence/error_reason 等）；
- `make fmt && make lint && make test`。

新增用例（沿用该包 table-driven + mock HTTP server 风格）：

| 用例 | 断言点 |
|---|---|
| drive docx Blocks 成功路径 | 主项 ContentType=text/markdown、ExternalID=file.Token、ReplacesSubtree=true；子项 ID 前缀正确、channel=feishu_drive |
| drive docx Blocks API 报错 → 回退导出 | 主项 ContentType=application/octet-stream、.docx 后缀；不设置 ReplacesSubtree |
| drive docx Blocks 渲染为空 → 回退导出 | 同上 |
| drive docx multimodal 关闭 | 图片子项不进 items，但 external_id 在 SubtreeKeep 中 |

手动验证：本地 drive 数据源跑一次同步，确认 docx 以 Markdown 形态入库、附件子项出现。
