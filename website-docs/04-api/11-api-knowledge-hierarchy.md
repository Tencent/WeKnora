# 原生父子知识：现有接口的可选参数

通过顶层 parent_knowledge_id 创建知识之间的父子关系，并使用现有查找、召回和删除接口按层处理。sub_knowledge 外部来源映射可独立使用；其中 parent_id 表示外部来源 ID，与内部知识 ID 分开维护。

## 上传：增加一个顶层字段

```http
POST /api/v1/knowledge-bases/{kb_id}/knowledge/manual
Content-Type: application/json
```

```json
{
  "title": "补充材料",
  "content": "完整内容\n\n![配图](https://images.example/supplement.jpg)",
  "status": "publish",
  "parent_knowledge_id": "父知识的 knowledge_id"
}
```

- URL 接口 `POST /api/v1/knowledge-bases/{kb_id}/knowledge/url` 的 JSON 同样增加 parent_knowledge_id。
- 文件接口 `POST /api/v1/knowledge-bases/{kb_id}/knowledge/file` 用 multipart 文本字段 parent_knowledge_id。
- 不填就是普通顶层知识；父必须已经存在，属于同一租户、同一知识库且未删除/deleting/moving。沿用该知识库现有写权限。父本身也可以是子知识，因此可创建孙知识；最多 32 层父链。
- 子知识的 data.id 仍由服务器生成，data.parent_knowledge_id 返回实际父 ID；正文、图片、分块、向量仍按独立知识入库。不拼成长文、不调用额外模型。
- 创建知识、父关联和标签在同一事务内提交；数据库失败不留下只有关系或只有知识的记录。file 的文件存储失败清理由原有流程处理。
- manual 默认 status=draft，不建索引；publish 异步，HTTP 成功不代表检索 ready。普通 manual POST 重试仍可能创建独立记录，不按相同正文自动合并；需要来源去重时使用原有来源映射契约。
- 文件/URL 原有去重规则保持；重复来源若已属于其他父知识，返回 409，不隐式改挂父。
- parent_knowledge_id 为创建时字段；正文更新请省略它，更新不会清除原关系。本次不增加重新挂父/移动整棵树的接口。

## 查找：可选展开子孙

原有详情、列表、文档搜索均支持 query：

```http
GET /api/v1/knowledge/{knowledge_id}?children_depth=2&children_limit=200
GET /api/v1/knowledge/batch?ids={id1}&ids={id2}&children_depth=2
GET /api/v1/knowledge-bases/{kb_id}/knowledge?children_depth=1
GET /api/v1/knowledge/search?keyword=攻略&children_depth=2
```

| 参数 | 行为 |
| --- | --- |
| children_depth 缺省或 0 | 不展开，原有返回结构/查询行为不变 |
| children_depth=1 | 直接子知识 |
| children_depth=2 | 子知识与孙知识，depth 为相对于查询起点的层级 |
| children_depth=-1 或 query 中 all | 全后代，最多 32 层；不是无限遍历 |
| children_limit | 全响应最多展开项数，默认 200，上限 1000；超过明确报错，不悄悄返回部分 |

详情的 data.children、列表/文档搜索每条 data[i].children 是平铺子孙数组，均含 knowledge_id、parent_knowledge_id、anchor_knowledge_id、depth、title/type、处理状态和内容。基础 total/page/has_more 只统计原始结果，不把展开项计入。单条知识自身的 parent_knowledge_id 在存在原生父关系时正常返回；无父时省略。

层级从当前查询起点向下计算；命中子知识不会自动返回它的祖先或兄弟。已知根知识 ID 时，可用详情接口配合 children_depth=all 读取其后代。

在同一只读快照内展开已发布、未删除、没有未完成删除意图的节点，不沿未发布的中间节点继续展示；不把它们当作向量召回成功。每条边都检查同租户/KB，不跨库拼接。快照中的起点或子节点正在移动，或关系损坏时明确失败。

manual 子知识 content 来自原始 Markdown 快照，图片引用仍保留在 Markdown 中；file/URL 返回 content_available=false 和原有受权 preview 链接，不冒充解析全文。整次展开的文本预算为 512KiB UTF-8，超预算项标 content_truncated=true，须按 content_url 单独取回原文；不等于模型上下文预算。共享 Agent 的资源链接保留 agent_id/agent_source_tenant_id。

## 召回：补充内容与向量命中分开

```http
POST /api/v1/knowledge-search?children_depth=2
POST /api/v1/knowledge-bases/{kb_id}/hybrid-search?children_depth=2
```

也可在这两个 POST 原有 JSON 中加整数 children_depth、children_limit；JSON 中 -1 表示全部，query 支持 all。query 参数优先。原有 query/query_text、知识范围等必填项和鉴权不变。

原有 data 的 chunk 命中、score、顺序、Rerank meta 均保留。另增加顶层 expanded 子孙数组，不给这些补充内容伪造向量分数。每项带 anchor_knowledge_id、depth，already_hit=true 表示该知识已在原命中中出现；同一知识可能相对多个命中起点重复出现，调用方可按 knowledge_id 去重。展开只使用经过授权的搜索目标的租户/KB；不因 shared Agent 借用来源租户的其他权限。

这些参数只扩充 API 返回证据，不自动改变聊天/Agent 的 prompt 或把全文全部塞进模型。未传参数时不增加展开查询和 expanded 字段。

关键词、标签和显式知识 ID 等条件筛选原始命中；展开是按父子关系补充同一授权知识库内的内容，因此子孙不保证也匹配这些语义/标签条件。展开项带 enable_status；未建好索引或禁用索引不等于原文已删除，补充原文也不表示该项被向量检索命中。

## 删除：默认只删当前，可选级联

```http
DELETE /api/v1/knowledge/{knowledge_id}
DELETE /api/v1/knowledge/{knowledge_id}?cascade_children=true
```

批量删除仍用 `POST /api/v1/knowledge/batch-delete`，在原有 `{"kb_id":"...","ids":["..."]}` JSON 中可选加 `"cascade_children":true`。多个起点的后代会合并去重，总数上限同为 1000。

- 缺省/false：只删除当前知识，清除其自身父关联；直接子知识解除父关联成为顶层，子知识自己的后代保留。
- true：删除当前知识和全部后代（含 draft），最多 1000 条（含起点）、32 层；超限、环、跨库关系明确失败，不静默只删前面部分。
- 仍返回原有 task_id，表示异步任务已提交，不代表 HTTP 返回时已清理完成。入队前只检查；worker 会重新收集最新后代，并在知识库事务锁下原子标 deleting，再复用原有向量/chunks/图谱/Wiki/文件/图片/标签清理链。
- 已确认的入队 ID 保留在 payload，重试继续受同一租户/KB 约束；移动到别库的节点会使任务失败，不跨库删除。deleting 状态不能被旧解析任务复活，父节点 deleting 后不能新增子知识。
- 软删与清除原生父关联在同一数据库操作中完成；已有来源映射由原触发器同步 soft-delete。硬删通过自引用 FK SET NULL / SQLite 触发器解除子节点父关联。
- 有原生父/子关系的知识暂不允许单独跨 KB 移动，入队和执行均检查；不静默拆树。同 KB 的文件夹调整不受影响。
- 删除失败时，沿用后台恢复/死信流程显示 failed 并允许重新删除；内部 deletion_pending 标记继续阻止旧解析任务激活节点或把它作为新知识的父。此标记不暴露为 HTTP 字段。
- 批量响应的 deleted_count 是入队检查时确认的数量；worker 可能补收新的后代，实际完成以异步任务为准。文件等外部资源仍沿用既有清理/重试语义，数据库原子性不等于外部对象存储事务。

## 存储与验证边界

原生关系新增 knowledges.parent_knowledge_id、自引用 FK、父索引；另有仅由数据库维护的 deletion_pending 状态及生命周期触发器，不新增另一张原生关系表。后台全行保存忽略父字段，内部删除标记不在 ORM 保存字段中，避免旧快照清空关系或删除意图。外部来源映射 knowledge_sub_knowledge 继续承担来源去重与 provenance，不要求普通父子知识请求使用它。

数据源用新 ID 替换知识时，父关系与直接子知识的改挂在替换事务内完成，孙辈关系保留；失败整体回滚。克隆仍创建独立的顶层知识，不自动复制原树关系。本次不增加整棵树克隆接口。

PostgreSQL 迁移 000138、SQLite 000054。升级前应备份数据库并执行迁移。结构关系展开不代表向量命中，召回质量、容量与延迟需用实际语料和模型验证。
