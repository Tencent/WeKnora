# @wxg-prc-cpg/opencode-weknora

[English](./README.md)

[npm](https://www.npmjs.com/package/@wxg-prc-cpg/opencode-weknora)

一个 [opencode](https://opencode.ai) 插件：把 WeKnora 知识库能力（混合检索、整篇阅读、带引用的 RAG 问答）一等公民地接入编码智能体。

opencode 自身没有知识库检索能力——它的工具只读工作区和互联网。本插件用你组织的文档补上这块，移植自 WeKnora 官方的 [`dsh-weknora`](https://github.com/Tencent/WeKnora/tree/main/packages/dsh-weknora)（DeepSeek Harness 插件）。

## 安装

在 opencode 配置（`opencode.json`）中添加插件并指向你的部署：

```json
{
  "plugin": [
    ["@wxg-prc-cpg/opencode-weknora", { "baseUrl": "https://weknora.example.com", "apiKey": "sk-..." }]
  ]
}
```

最快上手是环境变量（插件选项优先于环境变量）：

```sh
export WEKNORA_BASE_URL=https://weknora.example.com   # 或 http://localhost:8080
export WEKNORA_API_KEY=sk-...                          # 需要 retrieve 权限的 WeKnora API key（ask 需再加 chat）
export WEKNORA_KNOWLEDGE_BASE_IDS=kb-123,kb-456        # 可选的默认检索范围
```

## 工具

| 工具 | WeKnora 端点 | 模型得到什么 |
|---|---|---|
| `weknora_list_knowledge_bases` | `GET /knowledge-bases` | 知识库名称与 id |
| `weknora_search` | `POST /knowledge-search` + `GET /knowledge/search` | 按序排列的原文段落（含 knowledge_id、得分、chunk 序号），查询像标题时还会返回命名的文档 |
| `weknora_read_document` | `GET /chunks/:knowledge_id` + `GET /knowledge/:id` | 整篇文档段落按序重组，首页带头部摘要，支持翻页 |
| `weknora_ask` | `POST /sessions` + `POST /knowledge-chat/:id` / `agent-chat/:id` | WeKnora 自己组织的回答、引用、服务端用过的工具、可续问的 session_id |

## 权限与数据流

插件只读不写。`retrieve` 权限的 API key 足够前三个工具；`weknora_ask` 需要再加 `chat`。密钥通过 `X-API-Key` 发送，平台级 key 需配 `tenantId`。错误信息只含 HTTP 状态与 WeKnora 的原因，绝不回显密钥。段落内容按 `maxChunkChars` 裁剪并显式标注 `truncated`。

## 配置参考

字段与默认值与 dsh-weknora 完全一致，见 [English README](./README.md#configuration-reference)。

环境变量：`WEKNORA_BASE_URL`、`WEKNORA_API_KEY`、`WEKNORA_TENANT_ID`、`WEKNORA_KNOWLEDGE_BASE_IDS`、`WEKNORA_AGENT_ID`、`WEKNORA_RESOURCE_URLS`、`WEKNORA_TOOL_PREFIX`。

## 开发

```sh
npm install
npm test   # 构建 + 针对模拟后端的单元与契约测试
```

API 契约由两侧共同固化：`test/contract.test.mjs` 锁定插件发出的 WeKnora 调用；`contract/contract_test.go`（在仓库根目录执行 `go test ./packages/opencode-weknora/contract/...`）校验 WeKnora 服务端仍接受这些请求体、仍返回插件读取的响应字段。

## 许可

MIT——同 [`dsh-weknora`](https://github.com/Tencent/WeKnora/tree/main/packages/dsh-weknora)，本插件移植了其客户端与工具设计。
