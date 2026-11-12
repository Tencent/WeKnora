# 子知识来源关联 sub_knowledge

普通父子知识上传/按层查询/级联删除使用[最小接口扩展](./11-api-knowledge-hierarchy.md)：只需顶层 parent_knowledge_id。以下来源命名空间/分组接口为可选的外部来源映射能力。

每份内容独立创建 Knowledge、独立解析和向量化，通过来源组关联；适用于文档与附录、问题与补充材料、主内容与讨论等。接口不限定业务类型，不需要外部关系服务。

## 字段

| 字段 | 含义 |
| --- | --- |
| source_namespace | 来源命名空间，最多 32 字节；源系统/实例可能复用 ID 时应区分命名空间 |
| root_id | 外部组 ID，最多 128 字符；不强制等于根节点 source_id |
| source_id | 当前节点的外部来源 ID，最多 128 字符 |
| node_type | root 或 child；同组最多一个 active root，允许尚未导入根节点 |
| parent_id | 可选，结构父节点的 source_id；根节点不设 parent_id |
| reference_id | 可选，非结构引用的来源 ID；不等于父子归属，可引用暂未导入目标 |
| sort_order | 非负整数排序键，默认 0；不是自动推断的源时间 |
| knowledge_id | WeKnora 返回的内部知识 ID，不由 sub_knowledge 输入指定 |

所有来源 ID 都是字符串。parent_id/reference_id 不能指向自身，可先导入子节点，暂不要求目标已存在；不自动补齐目标，也不验证跨节点环路。该结构用于同组归集和保留来源关系，不是强一致的完整树数据库。

## 创建独立文本知识

现有 `POST /api/v1/knowledge-bases/{kb_id}/knowledge/manual` 增加可选 sub_knowledge；认证及 KB 写权限不变。不带字段的普通请求行为不变；携带时必须明确 title。

根知识示例：

```json
{
  "title": "操作指南",
  "content": "完整正文\n\n![配图](https://images.example/guide.jpg)",
  "status": "publish",
  "sub_knowledge": {
    "source_namespace": "game-docs",
    "root_id": "collection-100",
    "source_id": "guide-100",
    "node_type": "root",
    "sort_order": 0
  }
}
```

子知识调用相同接口：

```json
{
  "title": "指南补充",
  "content": "补充正文及自己的图片引用",
  "status": "publish",
  "sub_knowledge": {
    "source_namespace": "game-docs",
    "root_id": "collection-100",
    "source_id": "supplement-2",
    "node_type": "child",
    "parent_id": "guide-100",
    "reference_id": "supplement-1",
    "sort_order": 2
  }
}
```

每次响应 data.id 是这条内容自己的 knowledge_id，data.sub_knowledge 是其来源关系。不同节点不必拼成一个文档。省略 status 默认为 draft，不索引；publish 异步处理，HTTP 200 不代表索引完成或图片完整。

Knowledge、关联和标签在同一事务中创建。同一 tenant+KB+namespace+source_id 只有一个 active 关联，node_type 不参与唯一键；同组的 root 另有唯一约束。不同 KB 可分别导入同一来源，不跨库暴露已有知识 ID。

完全相同的标题、Markdown、发布状态和关系字段重试返回原 ID，不重复排队。上述指纹字段变化均返回 409，error.details.existing_knowledge_id 指向应更新的知识；不会在重复创建时静默覆盖。source_hash 是请求快照指纹，包含图片 URL 与关系字段，不是模型哈希、向量版本或纯正文哈希；tag_ids/channel/process_config 不参与该去重指纹；重复创建不更新这些选项，返回当前持久化标签。选项调整要走显式更新。已有失败记录的相同重试不自动重启任务。

## 给已有知识设置关联

```http
PUT /api/v1/knowledge/{knowledge_id}/sub-knowledge
```

请求为 `{"sub_knowledge": {...完整关系对象...}}`。使用现有内容写权限、API key ingest 能力与 KB allow-list。适用于已有 manual、file、URL 等 Knowledge，只设置关联，不重写内容、不重新解析、不触发模型调用。响应 success/data，data 含 knowledge_id 和 sub_knowledge。

可反复更新 parent_id、reference_id 和 sort_order；source_namespace/root_id/source_id/node_type 身份不可变。完整对象中省略可选关系字段会清空对应关系。相同 source_id 已属于其他知识、或已有不同根节点时返回 409，并提供受作用域限制的 existing_knowledge_id。

文本正文更新继续用 `PUT /api/v1/knowledge/manual/{knowledge_id}`；省略 sub_knowledge 会保留关联并刷新请求指纹。仅改关系优先使用上述独立入口，避免重跑索引。

## 命中后读取同组知识

```http
GET /api/v1/knowledge/{knowledge_id}/sub-knowledge?page_size=10&max_content_bytes=524288
```

知识 ID 可是根或任一子知识。先验证 Viewer/KB read、共享授权与 API key allow-list，服务端固定使用已授权的 KB，即使节点并发移动也不跟随到另一库。仅归集当前同一 tenant+KB+namespace+root_id 的知识。

| 参数 | 说明 |
| --- | --- |
| page_size | 默认 10，范围 1–50 |
| cursor | 下一页传上次 next_cursor；不可跨作用域复用 |
| max_content_bytes | 本页 Markdown 的 UTF-8 字节预算，默认 524288，范围 1024–2097152；不是模型 token 窗口，也不是整个 JSON 响应大小 |
| agent_id / agent_source_tenant_id | 共享 Agent 及源租户选择沿用原有授权；内部内容/图片链接保留这两个参数 |

返回 success/data，data 含 knowledge_base_id、source_namespace、root_id、items、total、page_size、has_more、next_cursor、has_root、snapshot_at、consistency、completeness。普通 GET /knowledge/{id} 不额外加载关联，应使用本专用接口。items 含 knowledge_id、来源关系、type/title、content/content_available/content_source/content_version/content_truncated/content_url、images/images_status、parse_status/enable_status/retrieval_ready、updated_at。

- manual：content 是完整原始 Markdown 快照，不依赖向量召回片段，content_available=true。编辑 chunk 不会改写这个快照。预算耗尽时 content_truncated=true，继续读取对应受权限保护的 content_url。
- file/URL 等：保留知识与关系，content_available=false，content_source=original_resource_link；content_url 指向已有 preview 文件接口，实际可取性取决于是否保存了原件。没有把 PDF 原件或 URL 自动变成单次解析全文 API。
- images：去重后的已保存 chunk 图片引用；不是图片字节/Base64，也不保证源图片覆盖完整。内部存储使用现有 KB 文件代理并实时校验绑定，外部 HTTP(S) 标 external。待处理/失败知识不混用旧索引图片，images_status 标 INDEX_PENDING_OR_FAILED。原始 Markdown 中的图片地址依然保留。被禁用的图片块不投影；坏 image_info 不拖垮整页，images_status=PARTIAL_METADATA_UNAVAILABLE。

坏正文元数据只使该项 content_available=false/content_source=unavailable，其他正常项仍返回；不会伪造缺失正文。原件链接本身的可取性仍需单独检查。

内部图片 URL 沿用现有文件代理协议，query 中包含编码后的存储 locator，不是匿名公开资源 ID。文件请求仍实时校验绑定/租户。KB 受限 API key 受现有文件代理门禁限制；此时 images 项不返回不可用 URL，而标 access=unavailable_for_api_key_scope。不会自动放宽全局文件访问策略。

本接口不调用模型、不重解析、不下载图片，不受搜索 TopK 限制；仍须按 has_more 翻页，并检查正文截断和图片状态。默认检索/Agent 工具不会自动调用它；业务在召回 knowledge_id 后显式回取整组，再按模型 token 预算选择输入。

## 完整性、生命周期和部署

不返回 draft/deleting/已删除知识；pending/failed 的已发布知识仍可读原文，但 retrieval_ready=false，不算召回成功。只统计当前已导入、发布、可见集合，completeness=IMPORTED_VISIBLE_KNOWLEDGE_ONLY，根未导入时 has_root=false，不等于源端缺失。

enable_status 只控制是否参与检索，不代替 KB 读取权限；disabled 的原文在有 KB read 授权时仍可读取，retrieval_ready=false，沿用现有 preview 语义。撤回原文可见性应同步删除知识或调整 KB 权限。

根优先，再按 sort_order/source_id/knowledge_id 确定性排序。游标保留首次导入时间截点，后来新增的数据需重新开始读取。游标是未签名分页状态，不是授权令牌；每次仍校验访问范围。跨页更新、删除、移动不构成数据库冻结快照，consistency=BEST_EFFORT_IMPORT_SNAPSHOT。

映射表独立于 manual metadata。移动单条知识时只同步该条映射的 tenant/KB，不自动搬迁其他组员；同组内容分属多个 KB 时分别受各自权限控制，不跨库合并。目标库已有相同来源/根时唯一约束拒绝移动。软删除释放 active 唯一键，硬删级联清理；克隆/复制不复制 active 关联。源端更新/撤回由同步程序显式更新/删除，不自动轮询外部系统。

PostgreSQL 迁移 000137、SQLite 000053，新增 knowledge_sub_knowledge 表、索引和生命周期触发器。复用已有正文、图片、分块、向量存储。升级前应备份数据库，并按标准升级流程执行迁移。
