# Outline 数据源接入实现与验证

日期：2026-09-11。代码基线：`5db13a131e10e8ee2105211f665412ebc13bd98e`，实现位于当前未提交工作区。

本文记录实际实现与本机验证，不将设计审查、模拟 API 测试或前端构建等同于真实 Outline 版本认证、端到端验收或生产发布准入。

## 1. 实现范围

| 设计阶段 | 本次交付 |
| --- | --- |
| A：协议与配置 | `outline` 独立类型；显式实例根地址与 API Key；只读 RPC；`X-API-Version: 2`；直接文档和 `data.document` 包装；确定性 httptest 数据。尚无真实实例录制的 fixture |
| B：公共能力 | 可选逐条接收确认、全量基线准备及逻辑任务身份；流式 partial；来源身份限定去重；检查解析接收结果；旧流式接口保留 |
| C：连接器 | Collection 发现、逐页 Markdown 同步、相对链接转换、源元数据、完整检查点、pending 补偿、全局 UUID 删除复核 |
| D：运行保障 | Redis 所有者租约与单进程 Lite 锁；执行/配置互斥；Lite 重试元数据；2 小时任务上限；暂停不覆盖游标；SQLite 表达式索引及 up/down |
| E：界面 | Outline 配置向导、必填地址/Key、资源多选、删除权限说明、保留策略说明、单次全量、渠道筛选、原文菜单、持久化资源错误状态、五语提示 |
| F：验证与发布 | 本地自动化回归、竞态检测、前端构建与类型检查、语言键审计、万篇游标大小测试；真实实例、解析检索端到端和吞吐压测仍待执行 |

核心新增目录：`internal/datasource/connector/outline/`。

## 2. 关键行为

- 成功指纹只在逐条接收确认后推进；抓取、入库、删除失败保留重试状态。接收成功不代表异步解析和索引已完成。
- 全量保留历史身份。任务使用 SyncLog ID 区分新全量请求与重试，避免入队锁竞争导致首次实际执行误用增量路径。
- 相同身份、相同指纹且已处于可接受解析状态时支持幂等确认；新的全量逻辑运行仍会重新处理内容。
- 同正文不同数据源/文档身份不合并；普通文件上传不传来源身份时保留原去重规则。
- 任一 Collection 扫描不完整时禁止整轮删除对账。跨选中集合移动先合并 UUID，再决定更新与删除。
- 明确删除、归档、撤回发布或移出有效范围后才允许清理。403、网关错误和未认证的 404 不推断删除。
- **原生 404 的“两轮缺失推断删除”保持关闭**：没有目标部署的错误契约认证，不能把权限问题当作不存在。
- 关闭删除会保留身份；重新开启必须重新验证删除端点和候选状态，不能直接执行历史删除标记。
- 取消选择 Collection 保留目标副本并移出活动基线；删除连接不清空已导入知识。
- 临时向导记录使用 paused、空 schedule，暂不要求删除权限；正式开启删除时验证 `documents.deleted`。
- 修改范围、凭据、删除策略或触发同步遇到执行占用时返回 `409 / datasource_sync_running`。租约失去后在接收和检查点边界停止。
- 正文、API Key 和响应体不写入连接器游标。统计包含扫描、未变化、交付、pending、请求、重试、限流等待、游标字节数及检查点耗时。

## 3. 已执行验证

工具链：Go 1.26.0，Node 26.8.1；前端按仓库 lockfile 安装。Go 使用独立临时缓存，未替换项目依赖版本。

| 验证 | 结果与边界 |
| --- | --- |
| `go test ./internal/datasource/...` | 通过；覆盖 Outline、语雀、飞书 core/drive/wiki、GitLab、Notion、IMA、RSS 及数据源公共包 |
| 仓储、Handler、types、container 包 | 通过；包含来源身份去重、暂停状态、SQLite 索引迁移及 EXPLAIN 查询计划、ForceFull JSON 校验 |
| 服务针对性测试 | 通过；接收失败/deferred、解析接收失败、严格查询错误、同版本幂等、全量重建及现有同步/流式入库测试 |
| 服务包全量测试 | 未达到无排除全绿：`TestSkillPythonVerifier/a_pyproject.toml_dependency_the_venv_does_not_carry` 期望错误但得到 nil；涉及文件未修改。排除 `TestSkillPythonVerifier` 后其余服务回归通过 |
| `go test -race ./internal/datasource ./internal/datasource/connector/outline` | 通过；包含 Lite/Redis 互斥和旧持有者不得释放新租约 |
| `npm run type-check` | 通过 |
| `npm run check-i18n` | 11 项通过；五种 locale 键一致，消息编译通过 |
| `npm run build` | 通过；构建仍提示部分 bundle 大于 500 kB，未进行无关拆包改造 |
| `git diff --check` | 通过 |
| 万篇游标 | 10,000 个文档身份、seen、applied 指纹及 pending 更新的模拟完整快照为 **4,050,393 字节**，低于 10 MiB；这不是万篇正文入库或内存峰值压测 |

连接器测试包含：失败不推进版本、无变化不交付、失败项补偿、删除失败重试、删除关闭保留身份、未知 404 保留副本、不完整扫描禁止删除、跨集合移动、取消选择、全量中断恢复、任务身份、实例绑定、详情包装、空 Markdown、中文文件名、正文哈希回退、相对链接/代码块、异常分页、限流重试上限和取消。

首次运行飞书日志测试受到本机 `LOG_FORMAT=json` 的影响：该变量在项目中是格式模板，导致输出只有字面量 `json`。将本次测试进程的 `LOG_FORMAT` 置空后通过，未修改全局环境或飞书代码。

建议复现命令：

```sh
LOG_FORMAT= go test ./internal/datasource/... ./internal/application/repository ./internal/handler ./internal/types/... ./internal/container
LOG_FORMAT= go test -skip '^TestSkillPythonVerifier$' ./internal/application/service
LOG_FORMAT= go test -race ./internal/datasource ./internal/datasource/connector/outline
go test -v -run TestCursorTenThousandSize ./internal/datasource/connector/outline
cd frontend
npm run type-check
npm run check-i18n
npm run build
```

## 4. 配置与运行

1. 在目标知识库的数据源设置选择 Outline。
2. 填写实例根地址和专用只读 API Key。地址不能包含 `/api`、其他子路径、userinfo、query 或 fragment。
3. 测试连接后选择 Collection；选择的是整个 Collection，包含多层文档，不提供文档子树选择。
4. 设置周期、长期 full/incremental 模式和删除开关。首期冲突策略仅为 overwrite。
5. 保存并同步；查看同步日志后，再检查知识解析状态和检索结果。菜单中的“重新全量同步”仅影响本次任务。

基础读取端点：`auth.info`、`collections.list`、`collections.info`、`documents.list`、`documents.info`。开启删除还需要 `documents.deleted`，不要求写入或恢复权限。

部署使用 HTTPS；私有 CA 加入系统信任链。显式 HTTP 仅用于被 SSRF 白名单允许的私网目标。没有关闭 SSRF 或 TLS 校验的连接器表单选项。

生产部署应设置 `SYSTEM_AES_KEY` 并验证凭据密文落库；项目原有“未设置密钥则不加密”的行为未被改写。导入后的访问权限由目标 WeKnora 知识库决定，不继承 Outline 用户 ACL。

前端本地预览：`http://127.0.0.1:5173/`。开发服务器只提供前端，完整流程仍依赖已配置的 WeKnora 后端及 Outline 实例。

## 5. 未完成的发布验收

以下仍是发布前条件，不能用上述单测代替：

- 固定目标 Outline 产品 tag/commit、镜像 digest，录制脱敏响应并执行真实 API 兼容验证；尚未认证任何真实产品版本。
- 在授权隔离空间验证 API Key scope 与账号角色、私有 CA、网关错误和 DNS/SSRF 配置。
- PostgreSQL/Redis/Asynq 与单实例 Lite 的部署级任务恢复、长暂停、租约丢失、主动取消和连接删除演练。
- 真实解析、分块、索引、检索命中、问答引用、原文访问权限及浏览器端到端流程。
- 1,000/10,000 篇真实正文分布下的峰值内存、吞吐、限流、检查点延迟与故障收敛压测。
- PostgreSQL 生产规模 EXPLAIN、凭据密文/RBAC 验收及发布回退演练。
- 修复或确认独立的 Python 技能校验测试失败，不能将排除后的测试结果表述为仓库全量全绿。

首期仍采用删除旧知识再创建的替换方式，存在短暂检索空窗和知识 ID 变化。租约不是数据库 fencing，不承诺任意长停顿下的线性一致性。私有附件不下载，不提供 OAuth、Webhook、双向写回或源端用户权限映射。

回退前先暂停 Outline 并等待在途任务结束。保留配置、游标和已导入知识；旧版本不得继续调度未注册的 Outline 任务。SQLite down 仅移除新增索引。
