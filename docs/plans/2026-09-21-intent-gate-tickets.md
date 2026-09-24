# IntentGate 细粒度 ticket 拆分（v2，待确认）

> 验收标准分层规范见 AGENTS.md「E2E 测试与验收标准」：[unit] go test / [cli] docker exec 直查中间件 / [api] curl 本地接口 / [e2e-ui] Playwright 浏览器。

## Wave 0 — 地基

### T00 开发验收环境就绪
**Blocked by**: 无
**交付**: 中间件容器、可启动的 app、可用的浏览器验收能力。
- [ ] [cli] `docker exec WeKnora-postgres-dev pg_isready` 通过；`redis-cli ping` 返回 PONG；postgres 内 `CREATE EXTENSION pg_search/vector` 成功
- [ ] [api] `curl localhost:8080/health` 返回 200，启动日志显示迁移完成
- [ ] [e2e-ui] Playwright 脚本打开前端首页并截图成功

## Wave 1 — P0 spike（纯只读，无表无 UI）

### T01 intentgate 包骨架：Gate 接口 + Verdict 类型
**Blocked by**: T00
**交付**: `intentgate` 包存在，`Evaluate(ToolCallInput) → Verdict` 契约定义，NoopGate 默认实现。
- [ ] [unit] `go test ./internal/agent/intentgate/...` 通过：NoopGate 恒返回 allow
- [ ] [unit] Verdict 四种取值（allow/deny/require_approval/uncertain）序列化往返一致

### T02 engine 接缝：执行点前调用 Gate
**Blocked by**: T01
**交付**: 所有工具调用执行前过 Gate；nil Gate 时行为与现状完全一致。
- [ ] [unit] fake Gate 返回 allow → 工具被执行
- [ ] [unit] fake Gate 返回 observe 模式 deny → 工具仍执行且 verdict 被记录
- [ ] [unit] Gate 为 nil → 现有 engine 测试全部保持绿（零行为变化）

### T03 规则引擎 v0：3 条硬编码 spike 规则
**Blocked by**: T02
**交付**: ①wiki_delete_page 而会话无删除意图→deny ②database_query 含 DROP/DELETE 而用户未提及→deny ③shell 含 `rm -rf` 家目录→deny，全部 observe。
- [ ] [unit] 每条规则：命中场景返回 deny + 理由，非命中场景返回 allow（双向断言）
- [ ] [unit] 三条规则各 10 组对抗样本（近似但不应命中的输入）零误报

### T04 Verdict 观测面：Langfuse span + 结构化日志
**Blocked by**: T03
**交付**: 每次判定在 `agent.tool.<name>` span 上写 `intent.verdict/layer/policy_id/latency_ms` metadata，同时写结构化日志。
- [ ] [unit] span metadata 字段完整性断言
- [ ] [cli] 触发一次工具调用后，日志中出现含 verdict 字段的结构化记录

### T05 spike 数据采集与误报率报告
**Blocked by**: T04
**交付**: 真实会话 ≥200 次工具调用的 verdict 分布报告 + 人工抽查结论。
- [ ] [cli] 统计脚本输出：verdict 分布、rule 命中率
- [ ] 人工抽查报告：deny 样本逐条判对错，误报率数字（>20% 则路线止损）

## Wave 2 — 持久化

### T10 intent_verdict 表迁移 + repository
**Blocked by**: T00
**交付**: postgres 与 sqlite 双版本迁移；VerdictRecord 的 CRUD repository。
- [ ] [cli] postgres `psql -c "\d intent_verdicts"` 结构与设计文档 §6.2 一致；sqlite 同构
- [ ] [unit] repository 测试：写入后可按 session_id/policy_id 查询
- [ ] [unit] args_digest 脱敏：敏感参数（SQL 类）不存原文只存 digest

### T11 VerdictWriter 异步落库接入
**Blocked by**: T01, T10
**交付**: Gate 判定后异步写 verdict 表，不阻塞工具调用热路径。
- [ ] [unit] 落库失败不影响 verdict 返回（fail-open 于观测面）
- [ ] [cli] 触发判定后 `SELECT count(*) FROM intent_verdicts` 递增；`mode_at_decision` 字段正确
- [ ] [unit] 异步写入 p99 不增加工具调用延迟（基准测试对比）

## Wave 3 — 策略

### T20 intent_policy 表 + scope 解析器
**Blocked by**: T10
**交付**: 策略表（双迁移）；解析顺序 tool>service>agent>workspace>tenant；per-tenant 缓存 + 变更失效。
- [ ] [unit] scope 解析矩阵：同级取最新 version、跨级取最具体、租户间隔离
- [ ] [unit] 策略变更后缓存按 tenant 失效，其他租户缓存不受影响
- [ ] [cli] `\d intent_policies` 结构与设计文档 §6.1 一致

### T21 策略 CRUD REST API
**Blocked by**: T20
**交付**: 策略的创建/读取/更新（version+1，旧版本保留）/停用接口，走租户鉴权。
- [ ] [api] curl 全流程：POST 创建 → GET 读取 → PUT 更新（version 从 1 变 2，v1 仍可查）→ 停用
- [ ] [api] 跨租户访问返回 403/404
- [ ] [api] `mode` 缺省为 observe；创建 enforce 策略需显式字段

### T22 规则编译器：NLC 的可编译子集
**Blocked by**: T01
**交付**: `rule_expr` 的校验/编译/执行（受限表达式，无函数调用无网络，运行超时 1ms）。
- [ ] [unit] 合法表达式编译执行：`$.amount <= 75` 类参数边界命中/未命中双向断言
- [ ] [unit] 危险表达式（函数调用、死循环、超长）编译期拒绝
- [ ] [unit] 执行超时 1ms 熔断返回"不适用"而非 panic

### T23 Gate 切换为 PolicyStore 驱动
**Blocked by**: T11, T20, T22
**交付**: 判定输入从硬编码规则改为读策略库；无策略命中时走 baseline 判定并记 `layer=baseline`。
- [ ] [api] 创建策略后，下一次工具调用的 verdict 记录 `policy_id` 与 `policy_version`
- [ ] [unit] 策略 mode=observe 时 verdict 只记录不拦截

## Wave 4 — judge 语义层

### T30 Judge 实现：租户模型 + 强 schema + uncertain
**Blocked by**: T23
**交付**: 规则层未决时调用租户自配 chat 模型；输入=原始 prompt+历史窗口+参数（数据/指令隔离）；输出强 schema，解析失败=uncertain。
- [ ] [unit] fake 模型返回合法 JSON → 正确解析为 verdict
- [ ] [unit] fake 模型返回垃圾/超时 3s → uncertain，observe 下放行
- [ ] [unit] judge 输入构造不含任何工具能力（judge 无工具）

### T31 模型能力档与降级
**Blocked by**: T30
**交付**: 租户模型弱于能力档时 judge 不启用，降级只跑规则层并记 `layer=rule`。
- [ ] [unit] 弱档模型配置 → Evaluate 不发起 judge 调用，verdict 记 rule 层
- [ ] [cli] 降级事件有结构化日志可查

### T32 判定缓存与成本指标
**Blocked by**: T30
**交付**: 同 session 内 `(policy_id, args_digest)` 判定缓存 5 分钟；`latency_ms`/`judge_tokens` 落 verdict 表。
- [ ] [unit] 同参数二次判定不重复调用模型
- [ ] [cli] verdict 表 `latency_ms`、`judge_tokens` 非空且合理

## Wave 5 — Enforce

### T40 逐租户 enforce 开关 + 内置工具 deny 通道
**Blocked by**: T23
**交付**: mode=enforce 的策略生效：deny 走工具错误路径（Success=false+原因），agent 可见并可自我纠错。
- [ ] [api] 租户 A 开 enforce、租户 B observe：同一策略同一调用，A 拦截 B 放行
- [ ] [unit] deny 时工具未被执行（执行次数断言）且错误文本含命中理由
- [ ] [e2e-ui] 对话中触发 deny → agent 回复中解释被策略拒绝的原因

### T41 MCP 工具 require_approval 复用审批通道
**Blocked by**: T40
**交付**: enforce 下 verdict=require_approval 的 MCP 工具调用走现有审批卡片（阻塞等待+跨实例广播）。
- [ ] [e2e-ui] Playwright：触发需审批调用 → 审批卡片出现 → 点批准 → 工具执行成功
- [ ] [e2e-ui] 点拒绝 → 工具未执行，agent 收到拒绝原因
- [ ] [cli] 审批决策（含 ModifiedArgs）可从审批存储查出

### T42 失败语义：fail-open 默认 + high-tier fail-close
**Blocked by**: T40
**交付**: enforce 下 judge/规则故障默认放行并告警；`risk_tier=high` 策略故障时拦截；`WEKNORA_INTENTGATE_FAIL_CLOSE=true` 全局切换。
- [ ] [unit] 故障注入：judge 超时 → 普通策略放行 + warn 日志
- [ ] [unit] 故障注入：high 策略 → 拦截
- [ ] [unit] 环境变量开启 → 全部拦截

### T43 enforce-deny 进 audit log
**Blocked by**: T40
**交付**: enforce 模式的 deny 写 audit_log（action=intent_policy.enforced_deny）；observe 不写。
- [ ] [cli] 触发一次 enforce-deny 后 audit 表可查，含 actor/policy/原因
- [ ] [cli] observe 模式 deny 不出现在 audit 表（只进 verdict 表）

## Wave 6 — 管理 UI

### T50 策略管理界面
**Blocked by**: T21
**交付**: 策略列表/新建/编辑/停用/版本历史页面。
- [ ] [e2e-ui] Playwright 全流程：新建策略（表单含 scope/NLC/mode）→ 列表可见 → 编辑后版本 +1 → 版本历史可见 v1/v2
- [ ] [e2e-ui] mode 默认值展示为 observe，切 enforce 有二次确认

### T51 Verdict 报表页
**Blocked by**: T11, T21
**交付**: 每策略的 verdict 分布（allow/deny 率）、时间趋势、可下钻到单条 verdict 理由。
- [ ] [e2e-ui] 报表页展示分布数字，与 `psql` 直查 verdict 表的结果一致
- [ ] [e2e-ui] 点击单条 verdict 展开 reason 与命中的约束

## Wave 7 — 飞轮接线

### T60 human_override 回写
**Blocked by**: T41
**交付**: 审批人的批准/拒绝/改参数（ModifiedArgs）回写对应 verdict 行的 `human_override`。
- [ ] [cli] 完成一次审批后，对应 verdict 行 human_override=approved/modified/rejected
- [ ] [unit] 无关联 verdict 的历史审批不受影响（兼容性）

### T61 语料导出
**Blocked by**: T60
**交付**: verdict 语料按 judge 模型分层导出为结构化文件（坏调用/正确调用/理由三元组）。
- [ ] [cli] 导出命令产出文件，schema 含 prompt/tool_call/verdict/human_override/judge_model 字段
- [ ] [cli] 按 judge 模型过滤导出结果正确

---

依赖图：
```
T00 ──► T01 ──► T02 ──► T03 ──► T04 ──► T05
 │       │                   
 │       └──────► T10 ──► T11 ──────────────┐
 │                 │                        │
 │                 └──► T20 ──► T21 ──► T50 │
 │                       │                  │
 │              T01 ──► T22                 │
 │                       │                  │
 │              T11+T20+T22 ──► T23 ──► T30 ──► T31
 │                              │       └──► T32
 │                              └──► T40 ──► T41 ──► T60 ──► T61
 │                                      ├──► T42
 │                                      └──► T43
 └──────────────── T11+T21 ──► T51
```
