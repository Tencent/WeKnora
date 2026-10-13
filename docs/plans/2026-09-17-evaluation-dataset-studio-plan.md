# WeKnora 本地评测数据集管理工具开发计划

## 1. 文档信息

- 状态：P0、P1 已实现；本地版本 P2 首版已完成。生产自动评测因版本差异和服务端错误暂停，当前主线转为离线评测闭环。
- 当前执行计划：[Evaluation Dataset Studio 配置中心与知识库分块导入开发计划](./2026-09-23-evaluation-dataset-source-and-config-center-plan.md)
- 已完成离线闭环计划：[Evaluation Dataset Studio 离线优先开发计划](./2026-09-20-evaluation-dataset-studio-offline-first-plan.md)
- 历史兼容计划：[Evaluation Dataset Studio 生产 WeKnora 兼容开发计划](./2026-09-18-evaluation-dataset-studio-production-compatibility-plan.md)
- 目标用户：需要为 WeKnora 知识库维护离线评测数据集的产品、算法和研发人员
- 工具形态：运行在本机的轻量 Go 服务，使用 HTML/CSS/JavaScript 提供浏览器界面
- 核心目标：维护评测数据集，并导出当前 WeKnora 可直接读取的数据包
- 实施原则：优先交付可用闭环，控制开发时间、依赖数量和 Codex token 消耗

## 2. 背景与约束

WeKnora 当前通过 `internal/application/service/dataset.go` 从固定目录 `./dataset/samples/` 读取一个名为 `default` 的数据集。数据集由五个 Parquet 文件组成：

| 文件 | 字段 |
| --- | --- |
| `queries.parquet` | `id: int64`, `text: string` |
| `corpus.parquet` | `id: int64`, `text: string` |
| `answers.parquet` | `id: int64`, `text: string` |
| `qrels.parquet` | `qid: int64`, `pid: int64` |
| `qas.parquet` | `qid: int64`, `aid: int64` |

当前实现存在以下约束：

1. API 只接受 `dataset_id=default`，还没有数据集注册和上传能力。
2. 一个问题当前只会解析出一个标准答案映射；重复 `qid` 的 `qas` 记录会相互覆盖。
3. passage ID 会被用于创建 `maxPID + 1` 长度的数组，稀疏或异常大的 PID 会产生大量空项。
4. 评测程序需要问题、标准答案、语料和关系均保持引用完整。
5. 工具首期只负责数据集制作和兼容导出，不修改 WeKnora 评测后端。

因此，工具需要在内部提供更易维护的数据模型，并在导出阶段稳定转换为上述五表格式。

## 3. 范围定义

### 3.1 P0 必须交付

1. 在本地创建、查看、修改、复制和删除多个数据集。
2. 维护语料 passage。
3. 维护问题、标准答案以及问题与相关 passage 的关系。
4. 自动分配稳定的内部 ID。
5. 提供阻断式兼容校验。
6. 导出只包含五个必需 Parquet 文件的 ZIP 数据包。
7. 导出后重新读取 Parquet，检查 Schema、行数和引用完整性。
8. 导出、导入可继续编辑的 JSON 项目备份。
9. 数据集管理默认仅在本机运行；只有用户显式读取资源、确认发起评测或更新任务时，才向其配置的 WeKnora 地址发送 API Key，密钥不落盘。

### 3.2 P1 建议交付

1. CSV 批量导入语料和问答。
2. 导入已有 WeKnora 五文件数据包并继续编辑。
3. 问题分类、难度、标签、审核状态等内部字段。
4. 重复文本、无引用语料、长度异常等质量警告。
5. 数据集版本快照和导出历史。
6. 基础统计：数量、覆盖率、平均相关 passage 数、长度分布。

### 3.3 P2 延后

以下能力开发成本或 token 消耗较高，且不影响首期闭环：

1. 多用户协作、账号、权限和服务端数据库。
2. AI 自动生成问题、答案或相关性标注。
3. Embedding 相似度、语义去重和模型质量评分。
4. Excel 导入、复杂字段映射器和多格式附件管理。
5. 可视化图表库和复杂仪表盘。
6. ~~直接调用 WeKnora 发起评测、轮询结果和版本对比。~~ 已于 2026-09-18 完成首版：关联导出记录、密钥不落盘、读取知识库与可用模型、手动轮询和指标对比。
7. 拖拽式工作流、富文本编辑和国际化。
8. 独立桌面安装包。

## 4. 技术方案决策

### 4.1 推荐方案

采用“Go 本地服务 + 原生 HTML/CSS/JavaScript”结构：

```text
浏览器 HTML 页面
    │
    │ localhost JSON API
    ▼
轻量 Go 本地服务
    ├── JSON 项目存储
    ├── 数据校验
    ├── Parquet 生成与回读
    └── ZIP 导出
```

推荐启动方式：

```bash
go run ./tools/evaluation-dataset-studio \
  --workspace ./local/evaluation-datasets \
  --addr 127.0.0.1:8090
```

浏览器访问：

```text
http://127.0.0.1:8090
```

### 4.2 选择理由

1. 仓库已经依赖 `github.com/parquet-go/parquet-go`，无需增加 DuckDB-Wasm、Arrow、Vue 子项目或新的构建链。
2. 导出和 WeKnora 使用同一个 Parquet 库，Schema 兼容风险最低。
3. Go 标准库已经覆盖 HTTP、JSON、ZIP、文件和静态资源嵌入。
4. 原生 HTML/JS 足以支撑 CRUD、搜索、校验和导出，不需要前端框架。
5. 本地 JSON 文件比 IndexedDB 更容易备份、版本管理和故障恢复。
6. Codex 可以按后端、导出、页面三个边界清晰的任务逐步实现，减少重复读取上下文。

### 4.3 明确不采用的方案

- 不新建 Vue/Vite 子项目：会增加依赖、配置、构建和 UI 组件调试成本。
- 不使用纯浏览器 Parquet 方案：需要额外 WASM、Worker 和浏览器文件系统适配。
- 不接入现有 WeKnora 数据库：会增加迁移、权限和服务部署风险。
- 不直接修改 `dataset/samples/`：工具只导出数据包，由用户显式部署。

## 5. 目录规划

首期所有代码放在独立目录，避免影响主应用：

```text
tools/evaluation-dataset-studio/
├── main.go
├── model.go
├── store.go
├── validate.go
├── export.go
├── server.go
├── *_test.go
├── web/
│   ├── index.html
│   ├── app.js
│   └── styles.css
├── testdata/
│   ├── valid-project.json
│   └── invalid-project.json
└── README.md
```

初期保持单个 Go package，避免为了分层创建大量小文件和接口。只有当文件明显超过可维护规模时再拆包。

## 6. 内部数据模型

为降低 UI 和数据关系复杂度，首期不提供独立答案管理页面。每个问题直接持有一个标准答案，导出时再生成 `answers` 和 `qas`。

```go
type DatasetProject struct {
    SchemaVersion int        `json:"schema_version"`
    ID            string     `json:"id"`
    Name          string     `json:"name"`
    Description   string     `json:"description,omitempty"`
    Version       string     `json:"version"`
    CreatedAt     time.Time  `json:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at"`
    NextPassageID int64      `json:"next_passage_id"`
    NextQuestionID int64     `json:"next_question_id"`
    Passages      []Passage  `json:"passages"`
    Questions     []Question `json:"questions"`
}

type Passage struct {
    ID          int64             `json:"id"`
    Text        string            `json:"text"`
    Source      string            `json:"source,omitempty"`
    Tags        []string          `json:"tags,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
    ReviewState string            `json:"review_state,omitempty"`
}

type Question struct {
    ID                 int64    `json:"id"`
    Text               string   `json:"text"`
    Answer             string   `json:"answer"`
    RelevantPassageIDs []int64  `json:"relevant_passage_ids"`
    Category           string   `json:"category,omitempty"`
    Difficulty         string   `json:"difficulty,omitempty"`
    Tags               []string `json:"tags,omitempty"`
    ReviewState        string   `json:"review_state,omitempty"`
}
```

内部 ID 使用单调递增整数，不因删除记录而复用。导出时按内部 ID 排序，并生成连续的导出 PID 映射，保证 WeKnora 不会创建稀疏 passage 数组。

问题 ID 从 1 开始；导出时 `aid=qid`，从而避免维护额外的答案 ID 映射。passage 导出 ID 从 0 开始连续编号。

## 7. 本地存储设计

每个数据集保存为独立 JSON 文件：

```text
<workspace>/
├── index.json
├── customer-service-eval.json
├── product-manual-eval.json
├── backups/
└── exports/
```

存储规则：

1. 数据集 ID 只允许小写字母、数字和短横线，阻断路径穿越。
2. 保存时先写临时文件，再原子替换正式文件。
3. 每次覆盖前保留一个最近备份，P1 再增加多版本历史。
4. 服务端不接受任意文件系统路径；所有文件必须位于启动参数指定的 workspace。
5. API 请求体设置合理上限，防止误导入超大文件耗尽内存。

## 8. 本地 API

为控制开发量，前端编辑完整项目对象，后端负责持久化、校验和导出，不为 passage/question 分别设计大量 CRUD 接口。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/datasets` | 列出数据集摘要 |
| `POST` | `/api/datasets` | 创建空数据集 |
| `GET` | `/api/datasets/{id}` | 读取完整项目 |
| `PUT` | `/api/datasets/{id}` | 保存完整项目 |
| `DELETE` | `/api/datasets/{id}` | 删除数据集，移动到本地回收目录 |
| `POST` | `/api/datasets/{id}/validate` | 执行完整校验 |
| `POST` | `/api/datasets/{id}/export/weknora` | 导出 WeKnora ZIP |
| `GET` | `/api/datasets/{id}/backup` | 下载 JSON 项目备份 |
| `POST` | `/api/import/project` | 导入 JSON 项目备份 |
| `POST` | `/api/evaluation-resources` | 读取 WeKnora 知识库及可用对话/重排模型 |
| `GET` | `/api/datasets/{id}/evaluations` | 列出本地评测记录 |
| `POST` | `/api/datasets/{id}/evaluations` | 发起 WeKnora 评测并关联导出版本 |
| `POST` | `/api/datasets/{id}/evaluations/{runID}/poll` | 更新评测任务状态和指标 |

所有 API 仅接受 JSON，下载接口除外。服务只绑定 `127.0.0.1`，并检查 Host、Origin 和请求 Content-Type，降低其他网页向本机服务发请求的风险。

## 9. 页面与交互

### 9.1 数据集列表页

- 显示名称、版本、问题数、语料数、更新时间和校验状态。
- 支持新建、打开、复制、导入项目备份和删除。
- 删除默认移动到 workspace 下的回收目录，不直接物理删除。

### 9.2 数据集编辑页

使用一个页面和四个区域，不引入路由框架：

1. 基本信息：名称、描述、版本。
2. 语料管理：表格、搜索、新建、编辑、删除。
3. 问题管理：问题、标准答案、相关 passage 选择器。
4. 评测运行：读取 WeKnora 资源、发起与轮询任务、对比结果。

首期交互要求：

- 使用浏览器原生表单和对话框。
- 表格支持关键词过滤和简单分页。
- 编辑后显示“未保存”状态。
- 保存使用显式按钮；P1 再增加防抖自动保存。
- 删除 passage 时，如果仍被问题引用，必须阻止删除并列出引用问题。
- 相关 passage 使用搜索加复选框，不实现拖拽排序。

### 9.3 校验与导出区

- 展示错误数和警告数。
- 错误可以定位到具体问题或 passage。
- 存在阻断错误时禁用正式导出。
- 导出成功后显示文件名、大小、行数和生成时间。

## 10. 校验规则

### 10.1 P0 阻断错误

1. 数据集名称、版本为空。
2. passage 或 question ID 重复、为负数或超出 int64。
3. passage、问题或标准答案文本为空。
4. 问题没有相关 passage。
5. 问题引用不存在的 passage。
6. 相同问题重复引用同一个 passage。
7. 数据集中没有问题或没有语料。
8. 导出后的五个文件缺失。
9. Parquet 字段名、字段类型或列数不正确。
10. 回读后的行数、ID 或引用关系与导出前不一致。

### 10.2 P1 质量警告

1. 问题文本完全重复。
2. passage 文本完全重复。
3. 没有被任何问题引用的 passage。
4. 问题、答案或 passage 长度异常。
5. 单个问题关联的 passage 数量异常。
6. 未审核记录进入发布范围。
7. 分类、难度或来源分布明显失衡。

首期不实现语义相似度、LLM 评分或答案事实一致性判断，避免引入模型成本和不稳定结果。

## 11. WeKnora 导出实现

导出阶段生成与 WeKnora 当前 loader 一致的行类型：

```go
type TextRow struct {
    ID   int64  `parquet:"id"`
    Text string `parquet:"text"`
}

type QrelRow struct {
    QID int64 `parquet:"qid"`
    PID int64 `parquet:"pid"`
}

type QARow struct {
    QID int64 `parquet:"qid"`
    AID int64 `parquet:"aid"`
}
```

转换规则：

1. passages 按内部 ID 排序并映射为 `0..N-1` 的 PID。
2. questions 按内部 ID 排序，QID 保持内部 ID。
3. 每个问题生成一条 answer，`AID=QID`。
4. 每个问题与每个相关 passage 生成一条 qrel。
5. 每个问题生成一条 qas。
6. 所有输出按 ID 排序，保证逻辑结果可重复。
7. 使用仓库已有的 `parquet-go` 写入临时目录。
8. 使用 `parquet.ReadFile` 回读并复核 Schema 和数据。
9. ZIP 根目录只放五个 Parquet，不混入 README、manifest 或项目 JSON。
10. 全部成功后才把 ZIP 移动到 exports 目录并返回下载响应。

ZIP 文件名：

```text
<dataset-id>-<version>-weknora.zip
```

项目备份和 WeKnora 发布包保持独立，避免扩展字段误入生产数据包。

## 12. 开发优先级与里程碑

### M0：固定兼容契约

优先级：P0  
预计开发时间：0.5 天  
Codex token 成本：低

任务：

1. 建立工具目录和 README。
2. 定义项目模型、Parquet 行模型和校验结果模型。
3. 添加一个最小合法项目 fixture。
4. 添加契约测试，确认五种行结构可以被 `parquet-go` 写入和读取。

完成标准：模型和导出契约不再需要在后续任务中反复讨论。

### M1：本地存储与 API

优先级：P0  
预计开发时间：1 天  
Codex token 成本：中

任务：

1. 实现 workspace 初始化和数据集索引。
2. 实现项目 JSON 原子读写。
3. 实现数据集列表、创建、读取、保存和回收站删除 API。
4. 添加路径安全、请求大小和 Origin/Host 检查。
5. 使用 `httptest` 覆盖主要接口。

完成标准：不依赖前端即可通过 curl 完成数据集 CRUD。

### M2：数据编辑页面

优先级：P0  
预计开发时间：1.5 天  
Codex token 成本：中到高

任务：

1. 实现数据集列表页。
2. 实现基本信息编辑。
3. 实现 passage 表格、搜索和编辑。
4. 实现问题、标准答案和相关 passage 编辑。
5. 实现保存、未保存提示和删除引用保护。
6. 完成基本响应式样式，不追求复杂视觉效果。

完成标准：用户可以完全通过页面创建一套合法数据集。

### M3：校验与 WeKnora 导出

优先级：P0  
预计开发时间：1 天  
Codex token 成本：中

任务：

1. 实现全部 P0 校验规则。
2. 实现 PID 映射和五表转换。
3. 实现 Parquet 写入、回读和 ZIP 打包。
4. 页面展示校验错误并支持导出下载。
5. 添加 ZIP 文件列表、Schema、行数和关系一致性测试。

完成标准：导出的 ZIP 解压到 `dataset/samples/` 后，可被 WeKnora 的 `default` 数据集 loader 读取。

### M4：项目备份与交付文档

优先级：P0  
预计开发时间：0.5 天  
Codex token 成本：低

任务：

1. 实现项目 JSON 下载和导入。
2. 补充启动、备份、导出和部署说明。
3. 执行 Go 测试、静态检查和手工浏览器冒烟测试。
4. 记录当前限制和 P1 清单。

完成标准：非开发人员可以按 README 独立运行并完成数据包制作。

### M5：批量导入与质量能力

优先级：P1  
预计开发时间：1～2 天  
Codex token 成本：中到高

任务：

1. CSV 批量导入。
2. WeKnora 五 Parquet 反向导入。
3. 完全重复检测、无引用检查和基础统计。
4. 标签、分类、难度和审核状态。
5. 数据集快照和导出历史。

实施进度（2026-09-18）：

- [x] CSV 批量导入语料和问题（支持模板、BOM、中英文表头和原子失败）。
- [x] WeKnora 五 Parquet ZIP 反向导入（Schema、关系和一问一答兼容校验）。
- [x] 完全重复检测、无引用检查、长度/关联数量/审核状态警告和基础统计。
- [x] 标签、分类、难度和审核状态的维护及统计。
- [x] 数据集快照、恢复和可重复下载的导出历史。

M5 不阻塞首期工具上线，应在真实使用反馈后拆分实施。

### M6：WeKnora 评测运行闭环

优先级：P2  
预计开发时间：1～2 天  
Codex token 成本：中

实施进度（2026-09-18）：

- [x] 关联一次成功导出，发起 `dataset_id=default` 评测任务。
- [x] 手动轮询任务进度，保存指标并支持两个成功任务对比。
- [x] 从 WeKnora 读取知识库、`KnowledgeQA` 和 `Rerank` 模型，保留手工 ID 兜底。
- [x] API Key 仅用于单次代理请求，不写入项目、快照、评测记录或日志。
- [x] 固定远程 API 路径、禁止重定向并限制响应体大小。
- [ ] 自动轮询、任务取消和逐题失败分析；根据真实使用反馈再决定是否实施。

> 说明：M6 当前实现只代表本地当前 WeKnora 版本的 Adapter 原型，不作为生产兼容承诺。后续不再以修改本地 WeKnora 后端为方案，按生产兼容适配计划执行契约审计、版本 Adapter 和手工兜底。

## 13. Codex 实施策略

为了降低 token 消耗和返工，每个 Codex 任务只处理一个里程碑，并遵守以下规则：

1. 每个任务开始时只读取本计划、工具目录和必要的 WeKnora 数据集契约文件。
2. 不扫描整个仓库，不重构工具目录之外的代码。
3. 单次任务尽量控制在 4～8 个主要文件内。
4. M0 完成后冻结 JSON 模型和 Parquet Schema；除非测试证明有问题，不在后续反复调整。
5. M1 先通过 API 测试，再开始写页面，避免前后端同时调试。
6. M2 使用原生 DOM 和少量帮助函数，不引入组件框架或状态管理库。
7. M3 先写转换和导出测试，再连接 UI。
8. 浏览器自动化只在 M3 完成后进行一次完整流程验证；日常使用 Go 单元测试和 API 测试。
9. 每个里程碑结束后更新 README 的“已完成/限制”，让下一次 Codex 任务无需重新调研。
10. 如果某项需求会引入新运行时、数据库或前端框架，默认移入 P1/P2，除非它阻塞五文件导出闭环。

建议将实际开发拆成以下五个连续 Codex 请求：

1. “按计划完成 M0，只实现模型、fixture 和兼容契约测试。”
2. “按计划完成 M1，实现本地存储、API 和测试，不开发页面。”
3. “按计划完成 M2，实现原生 HTML 数据编辑页面，不做导出。”
4. “按计划完成 M3，实现校验、Parquet、ZIP 和导出页面。”
5. “按计划完成 M4，补齐项目备份、文档和端到端验证。”

## 14. 测试计划

### 14.1 单元测试

- ID、文本和引用校验。
- passage 删除引用保护。
- PID 连续映射。
- 项目到五表的确定性转换。
- 重复 qrel 去除或阻断。
- 空数据集和非法数据集拒绝导出。

### 14.2 存储测试

- 创建、读取、保存和回收站删除。
- 临时文件写入失败不破坏旧项目。
- 非法数据集 ID 和路径穿越被拒绝。
- 并发保存不会产生截断 JSON。

### 14.3 导出契约测试

- ZIP 恰好包含五个目标文件。
- 每个文件字段名称和类型正确。
- 每个文件均可被 `parquet.ReadFile` 回读。
- 回读行数符合项目数据。
- qrels/qas 的引用全部存在。
- PID 映射为连续的 `0..N-1`。
- 同一项目重复转换得到相同的逻辑行顺序。

### 14.4 API 测试

- CRUD 正常路径。
- 不存在的数据集返回 404。
- 非法请求返回 400，而不是 500。
- 校验失败时导出返回 422 和结构化错误。
- 超大请求和错误 Content-Type 被拒绝。

### 14.5 手工验收

1. 创建数据集。
2. 添加至少三个 passage。
3. 添加至少两个问题和标准答案。
4. 为问题选择相关 passage。
5. 保存并重新打开页面。
6. 执行校验并导出 ZIP。
7. 解压 ZIP 到临时 `dataset/samples/`。
8. 使用 WeKnora loader 读取数据并确认问题、语料、答案和关系数量。

## 15. MVP 验收标准

以下条件全部满足才视为 P0 完成：

1. 一条命令可以在本机启动工具。
2. 工具只监听 loopback 地址；除用户显式读取资源或操作评测外，页面不发出外部网络请求。
3. 用户可以通过 HTML 页面维护多个数据集。
4. 用户不需要理解 qrels/qas 表，也能维护问题和相关语料。
5. 非法引用、空文本和不兼容类型会阻止导出。
6. 导出的 ZIP 根目录恰好包含五个 Parquet。
7. 五个 Parquet 的字段和类型与 WeKnora 当前 loader 一致。
8. 导出结果经过自动回读校验。
9. 项目可以备份为 JSON 并重新导入。
10. 工具目录的 Go 测试全部通过。
11. README 明确说明如何把数据包部署到 `dataset/samples/` 并使用 `dataset_id=default` 发起评测。

## 16. 风险与控制

| 风险 | 控制措施 |
| --- | --- |
| WeKnora 数据集 Schema 后续变化 | 在项目文件中保存 `schema_version`，导出器使用独立兼容 profile |
| 稀疏 PID 导致大量空 passage | 导出时强制连续重映射 |
| 本地文件损坏 | 原子写入、最近备份、项目 JSON 下载 |
| 浏览器页面向本机服务发起恶意请求 | loopback 绑定、Host/Origin 校验、JSON Content-Type |
| 数据量过大导致页面卡顿 | P0 使用分页和搜索；P1 再做虚拟列表和流式导入 |
| UI 开发超过预算 | 原生控件、单页结构、不引入设计系统和复杂组件 |
| 导出看似成功但 WeKnora 无法读取 | 使用相同 parquet-go 库，并在 ZIP 前自动回读 |
| 范围持续膨胀 | 只有影响“维护 + 校验 + 五文件导出”的需求才能进入 P0 |

## 17. 后续演进接口

当本地工具稳定并积累真实数据集后，再考虑改造 WeKnora：

1. 增加数据集注册、上传、列表和版本 API。
2. 让 `dataset_id` 映射到持久化的数据集版本，而不是固定 `default`。
3. 保存逐题评测结果并回流到本地工具。
4. 支持从失败样本创建下一版数据集。
5. 将“数据集版本、模型配置、检索配置、评测结果”组成可追溯实验记录。

上述演进不进入本计划的首期实现，避免本地数据集工具被后端评测平台建设阻塞。
