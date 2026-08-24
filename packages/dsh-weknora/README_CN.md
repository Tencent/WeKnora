# @wxg-prc-cpg/dsh-weknora

[English](./README.md) | 简体中文

一个 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)（`dsh`）插件，让 harness 里的 Agent 直接用上
[WeKnora](https://github.com/Tencent/WeKnora) 知识库：混合检索、按文档通读、以及 WeKnora 自己带引用的成稿答案。

dsh 本身没有任何检索、向量或知识库能力——它的 `grep`、`glob` 只看工作区文件，`web_search` 只看互联网。这个插件把缺的这一
块换成你自己的文档。

## 安装

```sh
# 从 npm 安装（推荐：装到的是构建产物，用户无需授权执行构建脚本）
dsh plugin --profile web add @wxg-prc-cpg/dsh-weknora

# 或者从本仓库的检出目录安装
dsh plugin --profile web add ./packages/dsh-weknora
```

然后把它指向你的部署。插件自带的配置层会读环境变量，所以最快的起步方式是：

```sh
export WEKNORA_BASE_URL=https://weknora.example.com   # 或 http://localhost:8080
export WEKNORA_API_KEY=sk-...                          # 需要 `retrieve` 能力（用 ask 还需要 `chat`）
export WEKNORA_KNOWLEDGE_BASE_IDS=kb-123,kb-456        # 可选的默认知识库范围
dsh web
```

超出这个范围的配置，在 profile 自己的 `cordis.patch.yml`（`$DSH_HOME/profiles/<name>/cordis.patch.yml`）里覆盖这一行：

```yaml
- id: weknora
  config:
    baseUrl: https://weknora.example.com
    apiKey: !!js process.env.WEKNORA_API_KEY
    knowledgeBaseIds:
      - kb-product-docs
    agentId: ''            # 填 WeKnora 自定义 Agent id，`weknora_ask` 就走 ReAct 流水线
    maxResults: 8
    maxChunkChars: 1200
    requestTimeoutMs: 30000
    chatTimeoutMs: 300000
    resourceUrls: handle   # 改成 `public`，引用里的图片和文件会给可直接加载的直链
    toolPrefix: weknora    # 重命名工具，例如同时挂两个部署时
    tools:
      listKnowledgeBases: true
      search: true
      readDocument: true
      ask: true
```

patch 是整块替换该行的 `config`，所以要保留的字段需要一并写出。配置不合法会在插件加载阶段失败，并逐条列出问题，而不是等到
模型第一次调用工具时才报错。

同时挂两个部署，就是两行加两个前缀：

```yaml
- insert:
    - id: weknora-internal
      name: "@wxg-prc-cpg/dsh-weknora"
      config: { baseUrl: https://kb.internal.example.com, toolPrefix: internal_kb }
    - id: weknora-public
      name: "@wxg-prc-cpg/dsh-weknora"
      config: { baseUrl: https://kb.public.example.com, toolPrefix: public_kb }
```

## 工具

| 工具 | WeKnora 接口 | 模型拿到什么 |
|---|---|---|
| `weknora_list_knowledge_bases` | `GET /knowledge-bases` | 知识库名称与 id，用于说明有哪些库，或为后续检索缩小范围 |
| `weknora_search` | `POST /knowledge-search` + `GET /knowledge/search` | 原文片段与排序，每条带 `knowledge_id`、得分、分块序号，外加查询点名的文档 |
| `weknora_read_document` | `GET /chunks/:knowledge_id` + `GET /knowledge/:id` | 单个文档按序拼回的正文，开头给出标题与摘要，支持翻页 |
| `weknora_ask` | `POST /sessions` + `POST /knowledge-chat/:id` 或 `POST /agent-chat/:id` | WeKnora 自己的答案、引用、服务端用过的工具，以及可续聊的 `session_id` |

`weknora_search` 是主力：它把原文交给 Agent 自己推理，Agent 的结论因此是可审计的。它在同一次调用里同时匹配片段内容和
文档名，因为模型常常分不清自己要找的是「哪里讲了这件事」还是「那份叫 X 的文档在哪」。当查询读起来像个标题时，结果里会额外
列出被点名的文档——这样即便某份文档的正文用词与标题完全不同，也依然够得着。

范围不需要配置。没有指定知识库的调用，检索接口会直接拒绝，RAG 接口则会在什么都没检索到的情况下作答，因此当
`knowledgeBaseIds` 为空时，插件会自己解析出该凭据可见的全部知识库并一并使用；这份清单每个进程只解析一次，且由
`weknora_search` 与 `weknora_ask` 共用。让模型先去挑反而更糟：知识库的命名往往糟糕到无从选择。

`weknora_ask` 把整个问题委派给 WeKnora。它适合跨多篇文档、需要综合的宽泛问题，也就是自己检索要来回好几轮的场景；它在服务端
再跑一个模型，所以慢，而且返回的是结论而非支撑结论的证据。配了 `agentId` 时它不会自己填范围：自定义 Agent 会按自己的知识库
选择模式在服务端解析范围，从这里传 id 反而会把它覆盖掉。

`weknora_read_document` 的存在是因为检索返回的是碎片：一旦某个片段看起来对，Agent 通常还需要它的上下文。每条检索结果都带
着这次调用需要的 `knowledge_id`；第一页会先给出文档标题和 WeKnora 生成的摘要，长文档不必翻完才知道自己拿到的是什么。

在 [Code Mode](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/core/tools/README.md) 下，同样的工具
可以写成 `await tools.weknora_search({ query })`，特别适合多跳检索：一段程序里扇出十个子问题，而不是十次模型往返。

## 权限与数据流

插件只读。它不会写 WeKnora：不入库、不改分块、不删除。四个工具里有三个只需要 `retrieve` 能力的 API Key；`weknora_ask`
额外需要 `chat`，因为它要创建会话并流式取回答案。

Key 通过 `X-API-Key` 发送。如果用的是平台级 API Key，还需要配 `tenantId`，它会作为 `X-Tenant-ID` 选择工作空间。报错信息
只包含 HTTP 状态码和 WeKnora 自己给出的原因，不会带上 Key。

片段内容在进入模型前会按 `maxChunkChars` 截断，避免一份超大文档吃掉整个上下文窗口；截断这件事会明确告知模型
（`truncated: true`），而不是悄悄丢文本。

## 配置项

| 字段 | 默认值 | 说明 |
|---|---|---|
| `baseUrl` | `http://localhost:8080/api/v1` | 缺少 `/api/v1` 时自动补全 |
| `apiKey` | 未设置 | `X-API-Key`；不设表示部署无鉴权 |
| `tenantId` | 未设置 | `X-Tenant-ID`，平台级 Key 必填 |
| `knowledgeBaseIds` | `[]` | 调用未指定范围时的默认值；留空表示检索该凭据可见的全部知识库 |
| `agentId` | 未设置 | 让 `weknora_ask` 走 ReAct 流水线 |
| `maxResults` | `8` | 同时是 `max_results` 与引用条数的上限 |
| `maxChunkChars` | `1200` | 单个片段的字符预算 |
| `requestTimeoutMs` | `30000` | 检索与读文档 |
| `chatTimeoutMs` | `300000` | `weknora_ask`，要等 WeKnora 自己的模型调用 |
| `resourceUrls` | `handle` | `public` 返回可直接加载的文件直链 |
| `toolPrefix` | `weknora` | 工具名前缀，需匹配 `^[a-z][a-z0-9_]*$` |
| `tools.*` | 全部 `true` | 少注册几个工具就少占几分 prompt |

## 开发

```sh
npm install
npm test          # 先构建，再跑单元测试与契约测试
npm run typecheck
```

端到端检查会把这个包装进一个临时 dsh profile，用 mock 的 WeKnora 后端和一个确定性的 OpenAI 兼容模型启动 headless
profile，并断言 harness 真的调用了工具、并且答案来自工具返回的内容：

```sh
npm run build
node test/e2e/run-in-dsh.mjs                        # 首次运行会从 npm 装一个 dsh
DSH_BIN=/path/to/dsh node test/e2e/run-in-dsh.mjs   # 或者复用已有安装
```

dsh 的会话持久化需要带 zstd 的 Node（≥ 22.15 或 ≥ 24），并且 `PATH` 上要有 pnpm ≥ 10——`dsh plugin add` 通过 pnpm 装进
profile，而它写出的 profile 把设置放在 `pnpm-workspace.yaml` 里。插件自身只要求 Node ≥ 20.11，两者都不需要。

`test/fixtures/api-contract.json` 记录了本插件发出的每一个 WeKnora 调用。`test/contract.test.mjs` 断言插件仍然只发这些
调用，`contract/contract_test.go` 断言 WeKnora 真实的 Go 请求/响应类型仍然接受并提供这些字段——任何一侧改名都会在 CI 挂掉，
而不是在用户的 Agent 里挂掉。

## 兼容性

已针对 dsh `0.1.0-rc.8` 与 WeKnora `0.7.2` 验证。dsh 处于 developer preview 并明确会有破坏性变更；本包因此**没有任何运行
时依赖**，交给 `ctx.tools.register()` 的只是一个普通对象，不锁定任何 harness 包版本。如果 harness 的工具定义契约发生变化，
请在本仓库提 issue。

## 许可

MIT，与 WeKnora 和 dsh 一致。
