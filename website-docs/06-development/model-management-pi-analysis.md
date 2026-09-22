# 模型管理与 PI 实现对照

分析日期：2026-09-22。PI 参考提交：`d192bd6dcae78fb87cf5c1c5e2af2790a0296278`；WeKnora 参考提交：`b57c362667e2436645e29066c9d91fd716a2e58b`。

本文记录重构前的源码分析；后续实现及验证见 [重构说明](model-management-refactor.md)。分析阶段没有运行 PI 或连接模型服务。范围为模型配置、目录、认证与请求装配，不是整个仓库的正确性审计。

## 判断

当前问题不只是模型列表需要手工维护。WeKnora 已按目录划分协议、厂商和 catalog，但数据模型与运行时的责任仍交叉：catalog 同时包含 UI 描述、部署凭据、协议配置和执行钩子；多个模型工厂再次装配认证与端点；业务配置、兼容旧字段与协议推断交织在 Resolve 内。

PI 的主要借鉴点是 Provider 的执行边界、配置组合与认证解析的分工，以及可替换的存储接口。PI 并非完全解耦：Model 自身也携带 API、地址和 compat；OpenAI Completions 的协议实现仍有厂商/URL 识别分支。因此，不应把“模型条目含 compat”或“每厂商一个数据文件”单独当作架构错误。

## PI 当前如何工作

以下路径均相对于 PI 仓库。

### 1. Model、Provider、API 的含义不同

- `packages/ai/src/types.ts:982`：`Model<TApi>` 是可供调用的模型描述，包括 `id`、`provider`、`api`、`baseUrl`、能力、价格、思考映射及按 API 类型约束的 `compat`。它不是数据库实体，也不是模型客户端。
- `packages/ai/src/models.ts:99`：`Provider` 是接入单元，提供 `auth`、`getModels()`、可选 `refreshModels()` / `filterModels()`，以及 `stream()` / `streamSimple()`。
- `packages/ai/src/models.ts:784`：`createProvider()` 将模型数据、认证策略和协议实现组合起来。协议实现可以是一个对象，也可以是按 `model.api` 索引的映射。

DeepSeek 的 Provider 工厂很小：导入生成的目录，配置 `envApiKeyAuth`，选择 `openAICompletionsApi()`，然后调用 `createProvider()`。OpenAI 工厂则明确选择 `openAIResponsesApi()`。

这并不意味着 Provider 不依赖协议；Provider 正是有意放置组合依赖的地方。统一运行时通过 Provider 接口工作，不需要知道如何分别装配 DeepSeek 和 OpenAI。

### 2. 内置目录与目录更新

`packages/ai/scripts/generate-models.ts:2647` 读取 models.dev、OpenRouter、AI Gateway、Radius 等来源，做筛选与人工修正，生成按厂商组织的数据及类型入口。生成脚本本身有大量厂商规则，并不是零维护的通用同步器。

`packages/coding-agent/src/core/model-runtime.ts:173` 创建内置 Provider；除 Radius 等特殊路径外，使用 `withRemoteCatalog()` 包装内置 Provider。

`packages/coding-agent/src/core/remote-catalog-provider.ts`：

- 内置模型是基线，远端模型按同 ID 替换或追加，不是用远端列表整体删除本地条目。
- 从 `ModelsStore` 恢复缓存，再按刷新选项决定是否访问 pi.dev。
- 使用刷新时间、Last-Modified 和 ETag 控制更新；缓存不比本地生成数据新时不覆盖。
- 临时网络失败保留缓存；不应把这一点理解成所有配置错误都会保留上一次有效配置。

`packages/ai/src/models.ts:398` 统一组织刷新：先恢复缓存，再解析认证后进行允许的网络刷新。发布按 Provider 校验刷新代次，过期刷新不能覆盖新状态。

### 3. 配置解析和组合

`packages/coding-agent/src/core/model-config.ts` 只负责读取、解析、校验并深度冻结 `models.json`。其中可以保存凭据表达式，但这个阶段不解析凭据、不运行凭据命令。

`packages/coding-agent/src/core/provider-composer.ts:459` 负责配置组合。对普通内置 Provider，模型列表的顺序是：

```text
内置 Provider 的当前目录（可能已有远端覆盖）
  → models.json 的 Provider 配置及 models 条目
  → 扩展配置（显式 models 可以替换列表）
  → 旧扩展 OAuth 的模型投影（若提供）
  → models.json.modelOverrides
```

原生扩展 Provider 可以替代内置 Provider 成为基线。认证、请求头和模型列表有各自的合并规则，不能用一个“后来的配置总是赢”的通用深度合并描述整个系统。

特别需要区分：

| 配置 | PI 的语义 | WeKnora 当前语义 |
| --- | --- | --- |
| `models` 中已有 ID | 构造新条目并替换旧条目；未提供的能力字段可能采用默认值 | 合并到旧条目，保留未指定字段 |
| `modelOverrides` / `model_overrides` | 只修改已有模型；未知 ID 忽略 | 未知 ID 会创建条目 |
| 删除本地覆盖后重新加载 | 从基线重新组合 | 当前 ApplyOverlay 修改现有全局注册表，不是从原始基线重建 |

PI 的替换语义由 `modelFromJson()`、`applyModelsJson()` 体现；未知覆盖 ID 被忽略还有 `test/model-registry.test.ts:981` 的现有测试。WeKnora 的差异见 `catalog/overlay.go:170`、`:416`。我们的文档中“与 PI 相同”的表述需要收紧。

### 4. 一次请求如何执行

Coding Agent 的实际链路为：

```text
ModelRuntime.streamSimple(model, context, options)
  → prepareRequest()
      → 根据 model.provider 找 Provider
      → getAuth() 解析认证并合并请求头
      → 得到请求模型和请求选项
  → Provider.streamSimple()
  → 配置的 API 实现
  → 上游请求
```

对应 `model-runtime.ts:574`。直接使用 pi-ai 的 `Models` 时，`models.ts:648` 的 `applyAuth()` 承担相似工作。

组合 Provider 的分派顺序也很明确：匹配的扩展 `streamSimple` 优先，其次是支持该 API 的基线 Provider，最后通过兼容 API 注册表查找实现（`provider-composer.ts:493`）。

`ModelRegistry` 在这个版本里只是供扩展使用的兼容门面，不能把它当成当前全部实现的核心。Coding Agent 内部使用 `ModelRuntime`。

### 5. 认证独立解析

`packages/ai/src/auth/resolve.ts` 处理显式请求凭据、存储凭据、Provider 的认证策略，以及 OAuth 刷新；存储通过 `CredentialStore` 接口注入。

对普通 API Key Provider，组合层可以在没有存储凭据时解析 `models.json` 中的 key，再回退到 Provider 的环境认证策略。已有存储凭据但认证失败时，不会无声切换到环境中的另一个身份。

因此，修改目录条目与修改凭据不需要写成同一套存储操作。`getModels()` 返回已知模型，`getAvailable()` 再检查认证和 Provider 的可用性过滤；这里的“可用”不等于已向厂商验证模型一定调用成功。

### 6. PI 仍有的耦合

`packages/ai/src/api/openai-completions.ts:1585` 的 `detectCompat()` 根据 provider、URL，部分情况下还根据 model ID 判断 DeepSeek、Moonshot、OpenRouter 等行为。`:1688` 的 `getCompat()` 再用显式 `model.compat` 覆盖推断结果。

此外，生成脚本负责大量厂商修正，配置 Schema 再次描述 compat 字段；ModelRuntime 也有 Radius 专用分支。PI 有清晰的组合机制，但并不是没有特殊逻辑。我们无需把这些分支重新搬回协议实现。

## WeKnora 的具体耦合及后果

以下路径相对于 WeKnora 仓库。

### A. 协议实现依赖目录管理包

`internal/models/api/openaicompletions/request.go:18` 的配置直接引用 `catalog.OpenAICompletionsSettings`。其余多个协议实现也导入 catalog。catalog 自身负责文件加载、全局注册表、UI 元数据、业务 ModelType 和覆盖规则。

这不是 Go 的循环导入，但协议层依赖了过宽的包。更合理的方向是让协议选项和请求/响应契约属于协议或一个小型公共契约包，再由 catalog / Provider 使用这些类型。

### B. Vendor 同时表示厂商说明、部署配置和行为

`internal/models/catalog/types.go:233` 的 Vendor 同时包括名称/图标/表单字段、模型列表、默认 API Key、协议配置、端点计算、协议选择、签名和验证函数。

组合是必要的，但把部署密钥与原始厂商目录放在同一个全局对象里，会让目录更新、配置重载和租户接入难以独立管理。界面描述可以继续数据驱动，不必让协议工厂依赖这些描述。

### C. 请求装配散落在各模型类型工厂

`chat/chat.go:173`、`embedding/protocol.go:25`、`rerank/reranker.go`、`asr/protocol.go` 分别处理 Resolve、密钥回退、Headers、Endpoint 钩子与协议分派。修改共同认证规则需要追踪多个入口。

业务服务中的 `resolveWeKnoraCloudCredentials()` 直接认识特定厂商；`vlm/vlm.go` 又有专门的 WeKnoraCloud 分派。这表明特殊接入行为尚未完全收敛到同一个 Provider 边界。

这里已经存在可观察的参数传递不一致：`catalog.ValidateRow()` 会把 `params.Spec` 传给 Resolve，Chat/VLM 工厂也传递 Spec，但当前 Embedding/Rerank/ASR 的 Resolve 调用没有传 Override。以 Embedding 为例，`embedding.Config` 和 `ConfigFromModel()` 根本没有 Spec 字段。这是静态调用链确认的差异，尚未通过端到端请求测试复现。

### D. 模型行为需要多处推断

`catalog/resolve.go:94` 同时执行 URL 厂商识别、模型精确/别名/通配匹配、默认值、行覆盖、URL 协议推断、厂商 PreferAPI 与旧 extra_config 兼容。

例如 OpenAI 条目默认是 Completions，但官方域名会在 `vendors/openai/vendor.go` 的 PreferAPI 中切到 Responses；改成代理地址又可能走 Completions。条目上的 API 与最终 API 不能直接等同。

模型 compat 根据 `spec.API` 选择应用对象，行 override compat 则根据最终 API 选择应用对象。维护者需要知道这两种 API 的区别，才能判断某个兼容字段是否生效。

### E. 全局注册表不是可重建的配置快照

`catalog/registry.go:17` 保存进程级 `map[string]*Vendor`，内置厂商通过 init 注册；`overlay.go:128` 从当前 Vendor 复制、应用补丁，再逐个 Register。

这套方式目前用于启动加载（`internal/container/container.go:119`），不是完整的热更新机制。直接反复调用 ApplyOverlay 不会自动撤销已从文件删除的覆盖，也没有整份配置成功后一次性发布的边界。要支持热更新，应保留不可变基线，构造并校验候选快照后再切换。

## 建议的调整方向

优先确定责任与运行结果，再决定文件放在哪个目录。

| 对象 | 职责 |
| --- | --- |
| 模型目录 | 已知模型描述、能力、来源和版本；可以包含明确归属某 API 的兼容数据，不含部署密钥 |
| 接入配置 | 租户/系统范围内的一次连接：Provider 类型、端点、凭据引用及自定义模型 |
| Provider 实现 | 组合认证、端点策略、模型来源与已有协议；封装特殊签名和 SDK 行为 |
| 有效模型配置 | 用明确优先级组合目录和用户覆盖，供校验、预览、实际调用共同使用 |
| 协议实现 | 消费确定的模型/端点/协议选项，编码请求、解析响应，不读取目录文件或数据库 |

WeKnora 的多租户、Embedding/Rerank/ASR、知识库模型引用不能直接套用 PI 的个人 CLI 文件存储。接入配置应有独立连接身份与作用域；同一厂商可以配置多套地址和凭据，旧 Model ID 的业务引用需要保留或显式迁移。

建议实施顺序：

1. 抽出协议契约和选项类型，去除协议实现对 catalog 管理包的依赖。
2. 收敛配置解析及认证/Endpoint 装配，使保存校验、编辑预览、Chat/VLM/Embedding/Rerank/ASR 使用同一有效配置入口；补上有意义的一致性测试。
3. 区分不可变厂商/模型目录与接入配置，明确新增、替换、覆盖和移除语义；把旧字段翻译集中到兼容入口。
4. 改为可重建的目录快照，然后再做生成、导入、刷新和管理界面。常规新增模型只更改数据；新协议或特殊认证才增加实现。

验收可以围绕实际维护任务：同协议新增模型无需改 Go 工厂；同厂商两套连接不互相污染；删除覆盖恢复基线；预览、测试连接和实际请求得到一致协议及能力；目录刷新不改变用户凭据及业务 Model ID；新协议的编译依赖不触及租户模型实体和 UI 描述。
