# DeepSeek Harness 插件

`packages/dsh-weknora` 让 DeepSeek Harness（dsh）里的 Agent 使用 WeKnora 知识库：检索原文、通读文档或把问题交给 WeKnora 生成带引用的答案。它通过 REST API 连接已有部署。

## 安装与连接

```sh
dsh plugin --profile web add @wxg-prc-cpg/dsh-weknora

export WEKNORA_BASE_URL=https://weknora.example.com
export WEKNORA_API_KEY='<api-key>'
export WEKNORA_KNOWLEDGE_BASE_IDS=kb-123,kb-456
export WEKNORA_RESOURCE_URLS=public
dsh web
```

也可在仓库根目录运行 `dsh plugin --profile web add ./packages/dsh-weknora`。插件自身要求 Node ≥20.11；仓库验证的 dsh 运行环境需要带 zstd 的 Node（≥22.15 或 ≥24）和 pnpm ≥10。已验证版本为 dsh 0.1.0-rc.8 / WeKnora 0.8.0，其他版本需按其插件接口确认兼容性。

base URL 可包含 /api/v1，未包含时自动补全。Key 使用 X-API-Key，需要 retrieve，调用 ask 额外需要 chat。平台 Key 还要在配置中提供 tenantId，作为 X-Tenant-ID。

## 四个工具

| 工具 | 用途 |
| --- | --- |
| `weknora_list_knowledge_bases` | 列出可见知识库及 ID |
| `weknora_search` | 混合检索片段，同时匹配文档标题；返回知识 ID、分块序号与得分 |
| `weknora_read_document` | 按文档读取有序正文，首屏含标题/摘要，长文可翻页 |
| `weknora_ask` | 在 WeKnora 创建或续接会话，返回答案、引用和服务端工具过程 |

search 把证据交给 dsh 自己推理；ask 会在服务端额外运行模型，适合需要综合多篇资料的问题。插件不修改知识库；ask 会创建会话和消息。

未指定知识库范围时，插件解析该凭据可见的全部知识库，进程内缓存并供 search/ask 共用。配置 agentId 后 ask 走 Agent 流程，知识范围由该 Agent 在服务端解析。

## Profile 配置

在 `$DSH_HOME/profiles/<name>/cordis.patch.yml` 中设置：

```yaml
- id: weknora
  config:
    baseUrl: https://weknora.example.com
    apiKey: !!js process.env.WEKNORA_API_KEY
    knowledgeBaseIds: [kb-product-docs]
    agentId: ''
    maxResults: 8
    maxChunkChars: 1200
    requestTimeoutMs: 30000
    chatTimeoutMs: 300000
    resourceUrls: public
    toolPrefix: weknora
    tools:
      listKnowledgeBases: true
      search: true
      readDocument: true
      ask: true
```

patch 整体替换该行 config，应包含需要保留的字段。tools.* 默认均启用；连接多个部署时设置不同 toolPrefix（小写字母开头，只含小写字母、数字和下划线）。

## 引用图片与排查

默认 resourceUrls=public，请求服务端把资源句柄转成可加载直链。限定知识库的 API Key 收到 403 时，插件自动退回 handle 并在后续调用保持该模式；返回 resource:// 时不能直接当作浏览器 URL。外链部署条件见[文件访问](../03-features/21-file-access.md)。

| 现象 | 检查 |
| --- | --- |
| 加载阶段配置错误 | 根据逐字段错误修正 baseUrl、参数范围、toolPrefix |
| 检索 401/403 | Key 是否有效、有 retrieve 能力及目标空间/知识库授权 |
| ask 被拒绝 | Key 是否另有 chat，平台 Key 是否提供 tenantId |
| 没有图片直链 | 是否为限定知识库 Key，或部署未配置可访问外链 |
| 长问题超时 | 调整 chatTimeoutMs，并检查 WeKnora 模型调用 |

实现参考：`packages/dsh-weknora/src/`、`test/fixtures/api-contract.json`、`contract/contract_test.go`。
