# M1 端到端验证报告

验证时间：2026-08-21（Asia/Shanghai）  
验证环境：本机 Docker 测试环境（非正式环境）  
测试视频：`vidsage-m1-smoke.mp4`，19.376 秒，中文培训类合成视频  
视频 ID：`80cbf560-e247-40ce-b658-af5187c22f72`

## 1. 结论

M1 当前为 **部分通过，不具备正式环境发布条件**。

- 方案 A 的核心链路已通过：字幕按句拆成 3 个块，逐条调用 WeKnora 手工知识接口，3 个 Knowledge 全部完成解析并可检索；本地 checkpoint 为 3/3 completed，Knowledge ID 为 3/3 唯一；视频成功切换为 ready，激活 revision=1。
- 上传、分块、方案 A 入库和 WeKnora 模型处理已真实运行。字幕生成及之后的链路使用了受控 ASR 夹具，因为现有听悟 client 调用了无效端点。
- 5 个 Skill 流水线未跑通。第一步 `extract-video-knowledge` 的 Agent 会话实际执行完成，但没有写出要求的 `type=knowledge_base` Wiki 页面，graph job 最终失败；后续 4 个 Skill 因依赖关系未启动。
- 5 个查询端点均可访问：关联知识返回 HTTP 200 空集合；大纲、概览、总结、转写页均因对应 Wiki 产物尚未生成返回 HTTP 404。

## 2. 业务影响

| 能力 | 现状 | 用户影响 |
|---|---|---|
| 视频上传与基础处理 | 上传成功；封面读取私有 MinIO 的签名修复已部署，最终非空结果待复核 | 视频可以进入处理队列；封面最后一次验证未完成 |
| 视频转写 | 未通过真实听悟调用 | 新视频无法自动获得真实字幕，当前只能通过测试夹具继续下游 |
| 字幕知识入库 | 通过 | 字幕可被拆分、检索，并保留视频时间定位信息 |
| 5 个内容 Skill | graph 未产出规定 Wiki 页面，后续 4 个未运行 | 大纲、概览、类型化总结和转写聚合页不可用 |
| 5 个查询端点 | 路由和降级响应正常，只有关联知识返回空数据 | 前端不会崩溃，但 4 个内容页暂无数据 |

## 3. 实测证据

### 3.1 基础设施与服务

- WeKnora app、frontend、PostgreSQL、Redis、MinIO、Neo4j、docreader 和 custom-backend 均已在本地 stack 启动。
- `custom-backend /healthz` 在 v0.7.5 镜像上返回 `{"db":"up","status":"ok"}`。
- 最终部署镜像：`weknora/custom-backend:custom-v0.7.6-20260821235500-thumbfix`。
- 旧容器均以 backup 名称保留，可回滚；未发布至正式环境。

### 3.2 冷启动视频与方案 A 入库

真实上传接口返回成功，并启动 thumbnail job。处理记录：

| Job | 结果 | 备注 |
|---|---|---|
| thumbnail | succeeded | 首轮发现容器内 localhost 和私有桶签名问题；最终签名 GET 修复已部署，最后一次产物非空检查受工具额度限制未完成 |
| transcription | 不通过（测试记录后由夹具替代） | 真实请求连续返回 HTTP 404，路径为 `/api/v1/tingwu/task/create`；该记录不能作为真实 ASR 成功证据 |
| subtitle_generate | succeeded | 使用受控中文转写夹具 |
| index | succeeded | 方案 A 逐条手工知识入库 |

方案 A 数据证据：

- 3 个字幕块全部建立本地 checkpoint。
- 3 个 checkpoint 全部为 `completed`。
- 3 个 `knowledge_id` 全部唯一。
- WeKnora 三条 Knowledge 均从 `finalizing` 进入 `completed`，向量化、问题生成、摘要和 Wiki 后处理均有执行记录。
- 每条正文包含视频定位 JSON 和原文；定位字段包含 video_id、video_type、source_filename、start_ms、end_ms、duration_seconds、speaker_id、sentence_id、paragraph_index、chunk_index、language、chunk_text 共 12 项。
- 视频最终状态为 `ready`，`transcript_active_revision=1`，generation 前缀为 `00046ab9f940`。

### 3.3 5 个 Skill

| 顺序 | Skill | 结果 |
|---|---|---|
| 1 | extract-video-knowledge | 失败：Agent 完成，但未写出 `type=knowledge_base` Wiki 页面 |
| 2 | generate-transcript-outline | 未启动：依赖 graph |
| 3 | summarize-transcript-content | 未启动：依赖 outline |
| 4 | generate-typed-transcript-summary | 未启动：依赖 overview |
| 5 | assemble-transcript-page | 未启动：依赖 summary |

graph 首次失败的持久化错误为：`after skill complete: 未找到 job=graph 的 wiki 页（type=knowledge_base）`。

验证过程中还修复了两项运行时问题：

1. Agent 缺少 `rerank_model_id`，已设置为现有 `builtin-rerank-zhipu`。
2. custom-backend 之前只等待 `[DONE]`，但 WeKnora 实际通过 JSON 中 `response_type=complete, done=true` 结束 SSE；现已兼容 complete/error 事件，避免 job 永久显示 running。

### 3.4 5 个查询端点

| 端点 | HTTP | 结果 |
|---|---:|---|
| `/related-knowledge` | 200 | 返回 video_id、kb_id、anchors、cross_video；当前集合为空 |
| `/outline` | 404 | `wiki_page_id not yet generated` |
| `/overview` | 404 | `wiki_page_id not yet generated` |
| `/summary` | 404 | `wiki_page_id not yet generated` |
| `/transcript-page` | 404 | `wiki_page_id not yet generated` |

## 4. 本轮修复

- 分块入库改为 WeKnora 真实 manual knowledge API。
- 增加逐块 checkpoint、内容 generation、完整 Knowledge ID 集合、重试幂等和旧 generation 清理。
- Knowledge 全部可检索后才将视频切换为 ready。
- graph 入队与字幕 generation 绑定，避免重转写复用旧 Skill 任务。
- 修复上传后只创建 thumbnail、没有进入 transcription 的断链。
- 分离 MinIO 内外部地址，并为 worker 私有对象读取生成签名 GET。
- 补齐 Agent rerank 模型配置。
- 修复 Agent SSE error/complete 事件识别。
- 保留旧 custom-backend 容器，支持本地回滚。

## 5. 剩余阻断与推荐方案

### 阻断 A：听悟适配契约错误

当前 client 的端点和鉴权方式未经真实听悟协议验证，实测 create 返回 404。应按当前购买/开通的阿里云听悟版本重新确认：正式 endpoint、Action/Version、签名鉴权方式、任务创建与查询响应结构，以及云端可访问的媒体 URL。完成前不能宣称冷启动全链通过。

### 阻断 B：Skill 产物契约未闭环

`extract-video-knowledge` 虽完成 Agent 会话，但没有生成编排器要求的 index Wiki 页面。建议：

1. 收紧 Skill 成功协议：结束前必须调用 Wiki 写入工具，并返回结构化 `artifact_type`、`page_id`、`video_id`。
2. custom-backend 不再靠“执行后扫描 Wiki + frontmatter 猜测”，而是直接读取 Skill 返回的 page_id；扫描仅作为兼容兜底。
3. 为每个 Skill 增加“成功但缺产物”的契约测试。
4. 修复工具参数中的 UUID 被误识别为模型 handle 的问题；本轮日志中 KB UUID 片段曾触发 unresolved model handles。

### 阻断 C：最终封面复核

最终 v0.7.6 已改为 MinIO 私有对象签名 GET 并重置 thumbnail job，但部署后的最后一次数据库检查因本地工具额度限制未执行。恢复工具后只需确认：healthz 为 OK、thumbnail job succeeded、`thumbnail_url` 非空且对象可读。

## 6. 发布判断

- 本地测试环境：可继续开发与修复。
- 集成测试环境：待修复听悟契约和 graph Skill 产物契约后再发布。
- 正式环境：当前禁止发布。

