# WeKnora

自托管、多租户的 RAG 知识库与 Agent 平台。

## Language

### 语义门禁（IntentGate）

**IntentGate**:
Agent 执行循环内部的策略执行点，在工具调用前判定"该不该做"（意图对齐 + 约束合规），产出 Verdict。与回答"能不能做"的 RBAC/审批门是两层。
_Avoid_: 网关、gateway（本仓库"网关"已指路由中间件、BrowserSkill 协议、企业 LLM 网关、IM 平台网关、sandbox 数据面网关五种事物）

**Verdict**:
IntentGate 对一次工具调用的判定结果：`allow` / `deny` / `require_approval` / `uncertain`，附理由与出处层级（规则层 / 语义层）。
_Avoid_: decision、result（审批门的 Decision 是人工审批结果，不是同一物）

**IntentPolicy**:
一条版本化的意图策略，绑定 scope（tool/service/agent/workspace/tenant），含自然语言约束原文、可编译规则表达式和 mode。属于配置资产，按租户隔离。
_Avoid_: rule、config（过于宽泛）

**自然语言约束（NLC）**:
用自然语言写的业务约束（如"单笔退款不得超过 $75"）。可编译成确定性表达式的走规则层判定，否则走语义层。
_Avoid_: policy text、约束描述

**Observe 模式 / Enforce 模式**:
IntentPolicy 的两种生效方式。Observe 只记录 Verdict 日志、绝不拦截；Enforce 按 Verdict 实际阻断或转人工审批。新策略一律 Observe 起步。
_Avoid_: dry run（dry run 是一次性试跑，Observe 是持续状态）

### 审批（既有概念，与门禁区分）

**审批门（Approval Gate）**:
按 `(tenant, service, tool)` 布尔配置决定是否暂停执行等人工确认的通道，目前只覆盖 MCP 工具。IntentGate 的 `require_approval` Verdict 在 MCP 工具上复用此通道。
_Avoid_: 门禁（那是 IntentGate）
