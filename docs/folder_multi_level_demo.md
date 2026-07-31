# WeKnora 多级文件夹支持 —— Demo 与实测记录

> 对应 issue：#1311（犀牛鸟开源活动）｜需求：①多层级文件夹放置文件 ②对文件夹内容进行问答 ③Frontend UI

---

## 一、功能概览

| 能力 | 说明 | 状态 |
|---|---|---|
| 多级文件夹 | 文件夹树（邻接表 + 物化路径），支持任意层级嵌套、拖拽移动、重命名、批量归档 | ✅ 已实现并实测 |
| 文件夹范围问答 | 聊天输入框 `@文件夹` 提及 → 检索范围自动收窄为该文件夹（默认含子文件夹） | ✅ 已实现并实测 |
| 混合范围 | `@标签 在 @文件夹 内` 自动取交集；文件夹范围与全库/文件范围并存 | ✅ 已实现 |

### 后端改动（关键文件）
- `internal/types/knowledge_folder.go`、`internal/types/search.go`、`internal/types/qa_request.go`、`internal/types/message.go`：`KnowledgeFolderScope`、`SearchTarget.ScopeFolderIDs`、`QARequest.FolderScopes`、`MessageExecutionContext.FolderScopes`
- `internal/application/service/session_knowledge_qa.go`：`buildSearchTargets` 支持 folder scope → 解析为具体文档 ID；`mergeFolderScopesByKB`；folder-only 与 tag∩folder 取交集
- `internal/application/service/session.go` / `session_agent_qa.go`：注入 `knowledgeFolderService`、Agent QA 合并 folder 范围
- `internal/handler/session/qa.go` / `helpers.go` / `types.go`：`folderScopesFromMentionedItems`、folder 提及绑定知识库、空范围校验支持 folder-only
- 检索/rerank 层零改动（folder 解析为 KnowledgeIDs 后复用现有链路）

### 前端改动（关键文件）
- `frontend/src/components/Input-field.vue`：`@` 提及加载文件夹候选（`listKnowledgeFolders` 全树 + 关键词过滤）、选中/移除/展示
- `frontend/src/components/MentionSelector.vue`：新增「文件夹」分组
- `frontend/src/stores/settings.ts`：`selectedFolders` 状态 + 会话恢复
- `frontend/src/types/mention.ts`：`MentionItemType` 增加 `'folder'`
- 文件夹树 UI（拖拽/右键菜单/批量操作）：`KbFolderTree.vue`、`MoveToFolderDialog.vue`、`DocumentBatchBar.vue` 等

---

## 二、实测记录（2026-07-31，本地 docker 部署）

部署方式：`docker compose`（app 镜像本地构建，Go 后端在容器内编译通过）。

### 1. 多级文件夹（Phase 1）—— 通过 ✅

创建 `FolderA(depth=1) → FolderA1(depth=2) → FolderA1x(depth=3)` 三级结构：

```json
{ "id": "1329ad3f…", "name": "FolderA",   "depth": 1, "path": "/1329ad3f…/",
  "has_children": true }
{ "id": "10e7870e…", "name": "FolderA1",  "depth": 2, "parent_id": "1329ad3f…",
  "path": "/1329ad3f…/10e7870e…/", "has_children": false }
```

- ✅ `parent_id` 正确挂载、`path` 为 ID 链物化路径、`depth` 正确递增、`has_children` 正确
- ✅ `GET /knowledge-bases/:id/folders` 一次返回全树

### 2. 文件夹范围问答接口（Phase 3）—— 通过 ✅

`POST /api/v1/knowledge-search`（知识检索，无需 LLM，可直接验证范围逻辑）：

| 用例 | 请求要点 | 结果 |
|---|---|---|
| 仅 `@文件夹`（无任何 knowledge_base_ids） | `mentioned_items:[{type:"folder", id, kb_id}]` | ✅ HTTP 200 `{"data":[],"success":true}`（folder-only 请求被正确接受） |
| 完全无范围 | 空请求 | ✅ HTTP 400「At least one … scoped tag, **or scoped folder** must be provided」 |
| 知识库 + `@文件夹` | 二者并存 | ✅ HTTP 200（folder 范围生效，库内无文档 → 空结果） |

> 说明：folder-only 请求在改动前会被 400 拒绝（空范围校验不认 folder）；现在正确接受并进入检索链路 —— 验证了
> `folderScopesFromMentionedItems → mergeKnowledgeTargets(folder 分支) → buildSearchTargets → ListKnowledgeIDsByFolders` 全链路。

### 3. 前端类型检查 —— 通过 ✅

`vue-tsc --build`：0 错误。

### 4. 真实文档 + 文件夹范围检索 —— 通过 ✅（含递归子文件夹）

> 前置：markdown 解析依赖 docreader 服务。部署时若只启动了 app/postgres/redis，md 会报
> `DOCREADER_PARSE_FAILED: document read failed`（gRPC `no children to pick from`），而 JSON 走内置解析器不受影响。
> 启动 docreader 后批量重解析，**27/27 文档全部成功**。

文件夹范围检索实测（知识库 27 篇真实 md 文档，含用户手动建的三层嵌套 `FolderA → AA → dd`）：

| 检索范围 | 命中文档数 | 结果全部在范围内？ |
|---|---|---|
| 仅知识库（基线） | 9 篇 | — |
| `@FolderA`（含子文件夹 AA/dd/FolderA1 递归） | 6 篇 | ✅ 是（含第三层 `dd` 与子文件夹 `FolderA1` 中的文档） |
| `@FolderB`（无子文件夹） | 3 篇 | ✅ 是 |

- ✅ 文件夹范围将结果**正确收窄**（6/9、3/9），且**递归包含全部子文件夹**（三层嵌套下依然命中）
- ✅ 范围外文档零泄漏（FolderB 的文档不会出现在 @FolderA 结果中）

---

## 三、如何运行与使用

### 运行（docker compose）

```bash
# 1. 准备环境变量（含 DB/Redis/JWT/AES 等）
cp .env.example .env   # 并按注释填写 DB_USER/DB_PASSWORD/DB_NAME/JWT_SECRET/SYSTEM_AES_KEY/REDIS_PASSWORD

# 2. 构建并启动（app 镜像构建时会编译 Go 后端；网络受限时可用中国镜像源加速）
docker compose build --build-arg GOPROXY_ARG=https://goproxy.cn,direct app
docker compose up -d

# 3. 访问
#    前端 UI : http://localhost        （默认 80 端口）
#    后端 API: http://localhost:8080
curl http://localhost:8080/health   # → {"status":"ok"}
```

### 使用步骤

1. **注册/登录**：打开前端首页 → 注册账号 → 登录
2. **建知识库**：知识库页面 → 新建知识库（文档型）
3. **建多级文件夹**：进入知识库 → 左侧文件夹树「+」新建文件夹 → 在文件夹内继续新建子文件夹（支持任意层级）
4. **上传并归档文档**：上传文档 → 选中文档 → 批量操作「移动到文件夹」
5. **对文件夹内容问答**：进入聊天页 → 输入 `@` → 选择「文件夹」分组（或直接输入文件夹名搜索）→ 选中目标文件夹 → 输入问题发送
   - 检索范围自动限制在所选文件夹（含其全部子文件夹）
   - 可同时 `@` 多个知识库 / 文件夹 / 标签 / 文件组合提问

---

## 四、Demo 素材

### 视频
- `docs/demo/demo.webm`（**2.2MB，1 分 30 秒**）：本机录制的完整 demo（原 275MB 2.8K 录屏经 ffmpeg 压缩 127×：2878→1280，VP9 crf32 + Opus 64k）。
  内容：登录 → 知识库详情（多级文件夹树）→ 批量上传/重试解析 → 归档文档到文件夹 → 演示文件夹范围检索。

### 截图（实测：用户真实使用多级文件夹归档 27 篇文档）
- `docs/demo/01_kb_documents_all_parsed.png` — **知识库文档列表：27 篇全部解析成功**（验证 docreader 修复 + 批量重解析效果）
- `docs/demo/02_kb_folder_tree_with_docs.png` — **多级文件夹 + 文档归档实战**（FolderA → AA → dd 三层嵌套、FolderA1 → cc、FolderA1x 子树，文档已分类到各文件夹，验证 Phase 1/2 UI 真实可用）

### API 实测脚本（可复现）
```bash
# 1. 注册登录获取 token
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"...","password":"..."}' | python -c "import json,sys;print(json.load(sys.stdin)['token'])")

# 2. folder-only 范围检索
curl -X POST http://localhost:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"query":"...","mentioned_items":[{"type":"folder","id":"<folderId>","name":"FolderA","kb_id":"<kbId>"}]}'

# 3. 批量重解析失败文档
curl -X POST http://localhost:8080/api/v1/knowledge/batch-reparse \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"kb_id":"<kbId>","ids":["<knowledge_id>","..."]}'

# 4. 移动文档到文件夹
curl -X PUT http://localhost:8080/api/v1/knowledge/move-to-folder \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"kb_id":"<kbId>","knowledge_ids":["..."],"folder_id":"<folderId>"}'
```
