# 模型管理重构与验证

本次重构基于 `b57c362667e2436645e29066c9d91fd716a2e58b`，实施及验证日期为 2026-09-22 至 2026-09-23。PI 的参考分析见 [源码对照](model-management-pi-analysis.md)。

## 实际目录与职责

```text
internal/models/
├── model.go                         # 不含凭据的模型元数据
├── capabilities.go                  # 前端能力描述契约
├── catalog/
│   ├── data/
│   │   ├── models.generated.json     # 统一生成的模型目录
│   │   └── overrides.json           # 人工维护的协议及参数修正
│   ├── loader.go                    # 加载生成数据
│   ├── registry.go                  # 查询与按类型匹配
│   ├── overlay.go                   # 部署级覆盖
│   └── snapshot.go                  # 深复制与原子替换
├── providers/
│   ├── builtin.go                   # 显式注册，不靠厂商包 init
│   ├── openai.go / aliyun.go / …     # 一家厂商一份定义，涵盖多种能力
│   └── assets/*.svg
├── runtime/
│   ├── runtime.go                   # 初始化、校验与重载
│   ├── resolve.go                   # 合并厂商、目录、模型行配置
│   ├── auth.go                      # 单模型认证、请求头与端点装配
│   ├── legacy.go                    # 旧 URL / thinking_control 推断
│   └── validate.go                  # 保存模型前的公共校验
├── api/
│   ├── *_settings.go                # 协议拥有自己的参数契约
│   ├── openaicompletions/
│   ├── openairesponses/
│   ├── anthropicmessages/ / googlegenai/
│   ├── openaiembeddings/ / dashscopeembeddings/ / arkembeddings/ / googleembeddings/
│   ├── cohererank/ / dashscoperank/ / nimrerank/
│   ├── tencentlkeap/ / volcengineknowledge/  # SDK 签名和协议实现
│   └── openaitranscriptions/ / openaichataudio/
├── chat/ / embedding/ / rerank/ / vlm/ / asr/  # 业务接口、批处理、并发及观测包装
├── parity/                          # 跨厂商、跨模型回归基线
└── limiter/ / utils/

scripts/model-catalog/
├── generate.py
├── sources.json                     # 数据来源与 models.dev 厂商映射
└── sources/seed.json                # 固定版本的模型元数据及来源链接
```

协议包不再导入 catalog。厂商定义声明各能力的协议和默认行为，模型列表来自统一数据文件；同一厂商的 Chat、Embedding、Rerank、ASR 共用认证与端点装配。只有接口方言不同才增加协议实现。OpenAI Completions 与 Responses 各自保留独立协议实现。

模型行仍单独保存 URL、认证信息、额外参数及 `spec`；没有新增连接表，没有重建模型 ID，没有迁移知识库、Agent 或历史会话引用。旧版 `extra_config`、URL 推断、云凭据回退继续兼容。共享的是厂商规则和代码，不是用户凭据。

## 修复的配置一致性问题

- Embedding、Rerank、ASR 的 `ConfigFromModel` 及工厂完整传递 `parameters.spec`。
- 能力预览、测试连接接收当前表单的 `spec`，修改后使旧测试结果失效；测试连接仍可按模型 ID 获取原有凭据。预览新增 POST，避免将 compat 对象放进 URL；旧 GET 保留。
- Chat 的显式 `spec.api` 优先于 URL / 厂商推断。旧 `extra_config.api` 仍优先；切换协议时，目录 compat 按原协议解码，用户 compat 按选定协议解码。
- 部署覆盖从内置基线重新构建，删除覆盖项会恢复默认值；校验失败不会发布部分修改。支持同一个模型 ID 按能力类型分别配置。
- OpenAI Completions 在合并 `extra_body` 后强制 `max_tokens` 与 `max_completion_tokens` 互斥，按配置的预算字段保留一个；火山逐模型覆盖流式 / 非流式、思考开关与调用方预算缺省情况。
- 复制目录时同时复制价格、切片、思考映射、嵌套 compat 等引用字段，避免覆盖污染基线。

## 后续维护

```bash
make model-catalog-diff                 # 只读比较 models.dev；需要网络
# 审核官方资料，修改 sources/seed.json 或 catalog/data/overrides.json
make model-catalog-generate             # 离线、可重复生成
make model-catalog-check                # 检查生成文件及模型模块全部测试
```

元数据与协议修正都按厂商集中管理，不再为每个厂商维护单独的 `models.json`。生成脚本按 `(type, id, match)` 合并；覆盖项逐字段替换，`compat` 是一个完整字段。新增模型也可以直接在 overrides 中声明。生成文件不手改。CI 会检测生成文件过期以及重复模型、无效参数、请求格式回归。

不会根据 models.dev 自动覆盖协议行为或删除未收录的模型。当前来源是迁移后固定的数据快照，条目的 `source` 保留官方文档依据。修改部署 `config/models.json` 后仍需重启；没有增加文件监听或后台自动刷新。

## 验证证据及边界

迁移前先运行原模型测试并保存基线；迁移后沿用同一基线，没有更新预期来掩盖差异。

| 范围 | 验证内容 |
|---|---|
| 27 家厂商 | 注册、图标、默认协议、认证类型、凭据字段、端点、额外参数与整份定义的重载校验 |
| 343 个目录条目 / 系列规则 | 264 Chat、48 Embedding、16 Rerank、15 ASR；检查字段、来源、协议参数、匹配和继承 |
| 351 个模型 / 别名 / 系列样例 | 迁移前后解析结果、认证类型、端点，以及对话模型全部思考档位和流式 / 非流式请求体一致 |
| 49 个 Embedding 样例 | 真实工厂、本地 HTTP、query/document 模式、维度选择、分批与结果顺序 |
| 16 个 Rerank 样例 | 包含 SDK 签名链路，本地 HTTP 与结果解析；明确不支持的条目验证拒绝调用 |
| 15 个 ASR 样例 | multipart / Chat Audio、语言参数与结果解析；不支持条目验证拒绝调用 |
| 配置隔离 | 每个厂商连续组装不同模型凭据，确认认证、请求头、模型 ID 不串用 |
| 覆盖与并发 | 覆盖删除恢复、失败回滚、价格等字段不污染基线、重载期间并发解析 |
| 前后端一致性 | 保存配置 → 工厂 → 请求参数；未保存 spec → 预览 / 测试连接；修改参数使旧结果失效 |

通过的检查：`make model-catalog-check`；`LOG_FORMAT='' go test ./...`；模型模块竞态检查；模型管理、路由、业务服务及容器测试；前端 72 项相关测试；`npm run type-check`；golangci-lint v2.12.2 对改动涉及 Go 包的增量检查；`git diff --check`。

扩大竞态检查到 `internal/handler/...` 时，未改动的 `handler/session` 桌面会话测试 `TestDesktopSlotCancelsHoldWhenKeyExpires` 报告测试清理函数与续租 goroutine 竞争同一计时变量（`sandbox_desktop_ws_test.go:111` / `sandbox_desktop_ws.go:473`）。模型模块及模型管理 handler 的竞态检查通过，该问题不混入本次重构。已在临时目录安装仓库指定的 golangci-lint v2.12.2 完成增量检查，未替换机器原有的 v1 工具。

本次验证没有使用真实厂商凭据，也没有对所有模型进行付费线上调用。上述覆盖证明配置迁移与请求装配行为，不能替代账号权限、地区、模型下线等线上可用性验证。官方文档抽查包含 [百炼 Rerank](https://help.aliyun.com/zh/model-studio/text-rerank-api)、[Gemini Embeddings](https://ai.google.dev/gemini-api/docs/embeddings)、[NVIDIA Embeddings](https://docs.api.nvidia.com/nim/reference/nvidia-nemotron-3-embed-1b-infer)；全部条目保留原来源链接，并未宣称重新逐页审核了所有上游文档。
