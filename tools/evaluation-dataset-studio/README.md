# WeKnora Dataset Studio

本地评测数据集管理工具。通过浏览器维护问题、标准答案、语料和相关性关系，并导出 WeKnora 当前评测模块可直接读取的五个 Parquet 文件。

## 启动

在 WeKnora 仓库根目录运行：

```bash
go run ./tools/evaluation-dataset-studio \
  --workspace ./local/evaluation-datasets \
  --addr 127.0.0.1:8090
```

访问 <http://127.0.0.1:8090>。

工具只允许绑定 loopback 地址，页面资源全部来自本地。只有在“评测运行”页显式操作远程任务，或在“AI 生成”页启动/测试生成模型时，才会向对应配置地址发出请求。

## 使用流程

1. 创建数据集。
2. 在“语料”页面添加 passage。
3. 在“问题与答案”页面添加问题、标准答案并选择相关语料。
4. 保存项目。
5. 在“校验与导出”页面运行兼容校验。
6. 校验通过后下载 WeKnora ZIP。

也可以从侧边栏直接导入已有的 WeKnora ZIP，生成新的可编辑数据集。

## AI 自动生成评测问题（P0）

“AI 生成”页用于把已有语料批量转换成待审核的评测问题，不依赖生产 WeKnora 接口：

1. 在顶栏“配置中心”保存一个 OpenAI-compatible 生成模型配置，并按需设为新数据集默认项。Base URL 填到 `/v1`，例如 DashScope 兼容模式地址；填写 API Key、模型名称和 token 上限。“测试连接”默认以“回复1”为提示词并限制输出 1 token，只验证连通性，也可以在配置中修改测试提示词。
2. 勾选当前数据集的来源语料，设置每段题数、分类、难度和可选补充要求。
3. 核对预计候选数、token 上限和费用估算后启动异步任务。
4. 在“生成任务与候选审核”中查看进度、失败原因、缓存命中、token 用量和候选内容。
5. 编辑候选后勾选“批准所选”，或直接拒绝。只有批准的候选会写入“问题与答案”，并自动关联原始语料。
6. 保存并沿用现有“校验与导出”流程生成 WeKnora ZIP。

生成模型必须返回结构化的候选 JSON。工具会在本地检查空问题/答案、答案要点、难度、证据引用、重复和近似重复；存在阻断错误的候选不能批准。黄金语料 ID 始终由后端按照用户勾选的语料建立，不采用模型生成的 ID。

任务默认逐条语料、并发数为 1；单段失败会重试 1 次。取消任务会停止尚未发送的语料，已经发出的模型请求会等待完成。相同 Base URL、模型、语料、参数和 Prompt 版本默认复用本地缓存；勾选“忽略缓存”才会重新调用模型。

单次最大输出 token 和任务 token 上限不设置工具端固定最大值，可按模型能力和预算调整；任务上限必须不小于单次输出上限。模型服务商自身的上下文窗口、最大输出和账户额度仍然有效。

生成配置保存在 workspace 的 `generation-connections/`，文件权限为 `0600`。数据集项目只保存 `generation_profile_id` 引用；“AI 生成”页选择引用后直接调用公共配置，不再重复维护地址和 API Key。API Key 不会写入项目 JSON、快照、生成任务、缓存或导出 ZIP。生成任务保存在 `generation-jobs/<dataset-id>/`，缓存保存在 `generation-cache/`。

## CSV 批量导入

语料页和问题页均提供“批量导入 CSV”和模板下载。CSV 使用 UTF-8 编码；工具也兼容 UTF-8 BOM 和中文表头。

语料 CSV：

```csv
id,text,source,tags,review_state
,设备离线时先检查电源和网络,故障手册,离线|网络,approved
```

- `text` 必填，其余列可选。
- 不填写 `id` 时自动分配；填写时不能与现有 ID 重复。
- 多个标签使用 `|`、`;` 或逗号分隔。

问题 CSV：

```csv
id,text,answer,relevant_passage_ids,category,difficulty,tags,review_state,answer_key_points,answerable,expected_documents,forbidden_documents,test_role,retrieval_filters,dataset_version,annotation_source
,设备离线怎么排查？,先检查电源和网络,1|2,故障排查,easy,离线|网络,approved,检查电源|检查网络,true,故障手册|网络指南,,普通用户,department=售后|product=网关,0.1.0,业务专家
```

- `text`、`answer`、`relevant_passage_ids` 必填。
- 请先导入语料；`relevant_passage_ids` 必须引用当前数据集内已存在的语料 ID。
- 评测扩展字段均为可选：答案核心要点、是否可回答、期望/禁止召回文档、测试权限角色、检索过滤条件、测试集版本和标记来源。
- 多值字段使用 `|` 分隔；`answerable` 支持 `true/false`、`是/否` 或 `可回答/不可回答`；检索过滤条件使用 `key=value|key=value`。
- CSV 会追加到当前项目。任意一行失败时，本次导入不会写入项目。

问题编辑页中的“评测扩展字段”只保存在项目 JSON、快照和备份中，供后续离线报告使用。严格 WeKnora ZIP 仍然只包含五个标准 Parquet 文件，不增加列或 manifest。

字段关系：

| 评测字段 | Studio 维护位置 | 是否进入五 Parquet ZIP |
| --- | --- | --- |
| 测试问题、参考答案 | 问题与答案 | 是，分别进入 `queries`、`answers` |
| 召回片段 | 问题关联的“相关语料” | 是，通过 `qrels` 关联 `corpus` |
| 问题类别、难度等级 | 问题基础字段 | 否 |
| 答案核心要点、是否可回答 | 评测扩展字段 | 否 |
| 期望召回文档、禁止召回文档 | 评测扩展字段；保存文档标识或名称 | 否 |
| 测试权限角色、检索过滤条件 | 评测扩展字段 | 否 |
| 测试集版本、标记来源 | 项目版本及问题扩展字段 | 否 |

“相关语料”是可计算召回指标的黄金片段关系；“期望召回文档”是更高层的文档级标注，两者不能互相替代。

## 导入 WeKnora ZIP

侧边栏“导入 WeKnora ZIP”支持把已有五文件数据包转换成可继续编辑的项目。导入时会检查：

- ZIP 根目录恰好包含五个标准 Parquet 文件，不允许额外文件或嵌套目录。
- 字段名和字段类型与 WeKnora 契约一致。
- query、corpus 和 answer ID 唯一且非负。
- qrels、qas 的引用全部存在且不重复。
- 每个问题恰好映射一个标准答案，并至少关联一条语料。

当前项目模型只支持一问一答。如果同一个 `qid` 在 `qas.parquet` 中出现多次，导入会明确拒绝，避免静默丢失答案。

ZIP 文件最大为 48 MiB，解压后的五个 Parquet 总计不能超过 128 MiB。

项目编辑数据保存在 `--workspace` 指定目录的 `projects/` 中。每次覆盖保存前会在 `backups/` 中保留最近备份；页面删除的数据集会移动到 `trash/`。

## 导出格式

ZIP 根目录只包含：

```text
queries.parquet
corpus.parquet
answers.parquet
qrels.parquet
qas.parquet
```

字段契约：

| 文件 | 字段 |
| --- | --- |
| `queries.parquet` | `id: int64`, `text: string` |
| `corpus.parquet` | `id: int64`, `text: string` |
| `answers.parquet` | `id: int64`, `text: string` |
| `qrels.parquet` | `qid: int64`, `pid: int64` |
| `qas.parquet` | `qid: int64`, `aid: int64` |

导出器会把内部 passage ID 映射成从 0 开始的连续 PID，并在生成 ZIP 前重新读取五个 Parquet 文件校验内容。

每次成功导出的 ZIP 会独立保存在 `exports/<dataset-id>/`，并在“导出历史”中提供再次下载，不会覆盖同版本的旧导出。

当前导出契约登记为 `weknora-five-parquet-v1`。每条导出记录都会保存 ZIP 的 SHA-256、契约 profile 和部署状态；这些追溯信息保存在 ZIP 旁边的本地 JSON 元数据中，不会向严格五文件 ZIP 添加 manifest 或其他文件。旧导出记录在读取时会计算缺失的 SHA-256，并将部署状态标记为“未知”。

选择一个已保存的目标环境后，可在导出历史中点击“记录已部署”，关联环境、Adapter 和 dataset ID；也可以标记为未部署。启动评测时会再次记录所选导出的契约与 SHA-256，使评测结果能够追溯到具体数据包。

“校验与导出”页同时展示语料覆盖率、平均长度、平均相关语料数以及来源、分类、难度和审核状态分布。完全重复、未引用、长度异常和未审核记录会作为警告展示，但不会阻止导出。

## 部署到当前 WeKnora

当前 WeKnora 只支持内置的 `default` 数据集。使用导出包前先备份原目录，然后将 ZIP 中五个文件解压到：

```text
dataset/samples/
```

发起评测时使用：

```json
{
  "dataset_id": "default"
}
```

替换数据集文件后建议重启 WeKnora 服务，并先在非生产环境运行评测。

## 项目备份

页面中的“下载项目 JSON”会保存所有可编辑字段。使用侧边栏“导入 JSON 项目”可以恢复项目。项目 JSON 与 WeKnora 发布包相互独立，扩展字段不会写入 Parquet。

“校验与导出”页可以创建版本快照。快照保存在 `snapshots/<dataset-id>/`；恢复快照前，当前项目仍会写入最近备份。快照和导出记录使用创建时间排序。

## 评测运行模式

生产 WeKnora 自动对接当前已暂停：已观察到认证失败和任务状态查询 HTTP 500，且生产版本与本地仓库契约尚未确认。推荐生产环境统一使用 `manual-export`，完成数据包人工部署和外部结果回导；工具不会尝试修复、猜测或轮询生产接口。

`local-current` 只用于与当前仓库契约一致的本地 WeKnora。生产配置默认选择 `manual-export`；如果人工改为 `local-current`，页面会持续显示实验性警告，并在发送请求前再次确认。

### 本地同版本自动评测

“评测运行”页可以调用现有 WeKnora 评测 API，并保存任务状态和指标：

1. 导出数据集 ZIP，并将五个 Parquet 解压到 WeKnora 的 `dataset/samples/`。
2. 重启 WeKnora 服务，确保 `dataset_id=default` 使用的是所选导出版本。
3. 填写 WeKnora 地址和具有 `run_evaluations` 权限的空间 API Key，点击“读取 WeKnora 资源”选择知识库、对话模型和重排模型；资源读取失败时仍可手工填写 ID。
4. 选择对应导出记录，确认模型调用费用后启动任务。
5. 使用“更新状态”获取进度和最终指标；两个成功任务可以进行指标对比。

WeKnora 地址可以填写服务根地址，例如 `http://127.0.0.1:8080`，也可以填写以 `/api/v1` 结尾的 API 地址。当前页面使用 `local-current` Adapter；只有该 Adapter 持有 `/knowledge-bases`、`/models` 和 `/evaluation` 路径及响应解析规则。远程请求禁止 HTTP 重定向，避免 API Key 被转发到其他地址。资源列表只展示可用的 `KnowledgeQA` 与 `Rerank` 模型。

远程能力已经与页面和评测记录解耦。`manual-export` Adapter 明确声明为仅导出、不访问远程接口；生产 Adapter 必须在生产契约审计恢复后按版本新增，不能依据本地接口猜测。

### 环境配置与兼容性预检

顶栏“配置中心”可以保存本地、测试或生产环境配置。配置包含名称、Base URL、Adapter、dataset ID、知识库 ID、对话模型 ID、重排模型 ID、API Key 和部署说明，保存在 workspace 的 `connections/` 目录。“评测运行”页只选择当前数据集引用的环境。连接文件权限为 `0600`，API Key 不会进入项目、快照、评测记录、报告或导出包。

配置中心可以分别设置“默认生成模型”和“默认 WeKnora 环境”，保存在权限为 `0600` 的 `workspace-settings.json`。新建数据集会自动绑定默认项；已有数据集可以单独切换。项目 Schema v2 只保存配置 ID，不保存密钥。被默认项或数据集引用的配置不能删除，必须先解除引用或设置替代配置。

两类配置中的 API Key 会随配置长期保存在本机对应 JSON 文件中，重启后仍可使用。新建配置需要点击一次保存；已有配置修改 API Key 后会自动保存。更新其他字段时即使请求未携带 Key，服务端也会保留原有密钥，避免被空值误清除。历史环境配置如果从未写入 API Key，需要在配置中心重新填写并保存一次。

保存配置后可以运行“只读检查兼容性”。`local-current` Adapter 只发送 `GET` 请求：除 `/system/capabilities`、`/system/info`、`/knowledge-bases` 和 `/models` 外，还会按当前配置抽样读取 `/knowledge-bases/{kb_id}/knowledge` 与 `/chunks/{knowledge_id}`，验证后续分块导入所需的分页和字段契约；不会创建评测任务、修改知识库或调用模型。检查结果分项展示通过、无权限、不支持、服务失败和结构不兼容，并可下载脱敏 JSON 报告。报告不包含目标主机、响应正文、API Key、知识库名称、文档名称、分块正文或模型名称。

分块读取契约要求知识库和文档至少返回 `id`，分块至少返回 `id` 与非空 `content`；分页使用 `page` 和 `page_size`。HTTP 401/403 标记为“无权限”，404 标记为“不支持”，其他 5xx 标记为“失败”，成功但字段结构不符标记为“不兼容”。`manual-export` 不声明分块读取能力。兼容检查不通过只会阻止后续远程分块导入，不影响人工语料、CSV 导入、AI 生成或 WeKnora ZIP 导出。

### 从 WeKnora 分块导入语料

在数据集“语料”页展开“从 WeKnora 已解析分块导入语料”：

1. 选择配置中心已保存的 `local-current` 环境，依次读取知识库、已解析文档和文本分块。
2. 可跨分页勾选分块；页面每页显示序号、正文摘要、字符数和索引状态。
3. 点击“导入预检”，查看可导入、已存在、内容变化和无效数量。单次默认按 100 条读取，最多选择 1000 条。
4. 对相同环境配置和 chunk ID，正文 SHA-256 相同会跳过；正文变化默认跳过，也可选择“作为新语料导入”，绝不覆盖已有语料。
5. 确认后服务端重新读取真实分块并再次去重，再以一次原子写入保存项目。导入失败不会留下部分数据。
6. 导入完成可点击“用新增语料生成问题”，新增 passage 会自动成为 AI 生成的默认选择。

每条导入语料在项目 JSON 的 `metadata` 中记录来源环境、知识库、文档、chunk ID、chunk 序号、内容 SHA-256 和导入时间；不保存 API Key。来源 metadata 只用于 Studio 追溯，不会进入 WeKnora 五 Parquet ZIP。生产环境在确认导入前会再次执行三段只读契约检查，任一项未通过即拒绝写入。

安全约束：生产和测试环境必须使用 HTTPS；仅 `local` 环境的 loopback 地址允许 HTTP。生产自动对接恢复前应使用 `manual-export`；`local-current` 的生产入口只为已经人工确认契约完全一致的实验性联调保留。

远程错误只向页面返回经过压缩和脱敏的服务端错误消息；URL、疑似密钥、认证 Header 和底层网络地址不会写入项目、评测记录或兼容性报告。

API Key 随环境配置保存在本机 `connections/<profile-id>.json`，连接配置文件使用仅当前用户可读写的权限。API Key 不会写入项目 JSON、快照、评测记录、兼容性报告或日志。评测记录保存在 `evaluations/<dataset-id>/`，包含关联的导出版本、任务状态、模型 ID 和指标。

评测记录支持展开详情，查看任务与环境配置、完整检索/生成指标、导出问题数、远程执行题数和逐题结果数量。关联导出问题数与远程任务总数不一致时，详情会直接提示检查目标环境的数据集部署版本。

### 手工评测与外部结果导入

生产环境未开放评测 API 时，可以完成不依赖后端改造的手工闭环：

1. 新建环境配置，Adapter 选择 `manual-export`，填写生产认可的 dataset ID 和部署说明。该模式隐藏且不保存 API 地址、API Key、知识库 ID 和模型 ID。
2. 导出 ZIP、核对 SHA-256，并按生产流程部署数据包。
3. 在“评测运行”中选择该配置和导出版本，创建手工评测记录。此操作不需要 API Key，也不会访问远程接口。
4. 在外部系统完成评测后，选择手工记录和 JSON/CSV 格式，粘贴真实结果。内容只作为数据读取，不会被执行。
5. 先点击“校验结果”，查看匹配、缺失、未知和重复问题数量，以及导入内容 SHA-256。
6. 首次导入会完成等待中的记录；对已完成记录再次导入会创建修订记录，原记录不会被覆盖。
7. 在“离线评测报告”中选择记录，生成本地指标和逐题明细；可按零召回、低召回、执行失败和缺少结果筛选。

“导入外部评测结果”只在当前环境配置使用 `manual-export` 时显示。切换到 `local-current` 后完整表单会隐藏；如果数据集中仍有历史手工记录，页面只显示切换提示。离线报告和结果对比不受当前 Adapter 影响，仍可读取两种模式的历史完成记录。

结果 JSON 示例：

```json
{
  "schema_version": 1,
  "status": "success",
  "summary": {
    "total": 2,
    "finished": 2,
    "retrieval_metrics": {"precision": 0.75, "recall": 1.0},
    "generation_metrics": {"rouge1": 0.5}
  },
  "items": [{
    "question_id": 1,
    "actual_answer": "外部系统生成的答案",
    "retrieved_passage_ids": [0, 2],
    "retrieved_documents": ["产品手册"],
    "latency_ms": 1200,
    "error_message": "",
    "answer_judgement": "correct",
    "answer_score": 1.0,
    "assessment_source": "人工审核",
    "review_comment": "覆盖全部答案要点"
  }]
}
```

逐题 CSV 表头：

```csv
question_id,actual_answer,retrieved_passage_ids,retrieved_documents,latency_ms,error_message,answer_judgement,answer_score,assessment_source,review_comment
```

`question_id` 必须属于所选导出版本；`retrieved_passage_ids` 使用 ZIP 中 `corpus.parquet` 的连续导出 PID，而不是 Studio 内部语料 ID。工具在导出记录中保存 PID 映射，供后续报告转换。缺失问题允许导入但会在预览中列出；未知问题、重复问题、未知 passage ID 和格式错误会阻止导入。

旧版仅含 `status/total/finished/retrieval_metrics/generation_metrics` 的汇总 JSON 继续兼容。外部结果只允许关联 `manual-export` 记录，原始内容不落盘；记录只保存规范化结果、格式、Schema 版本和原始内容 SHA-256。

### 离线指标与报告

Studio 从评测记录关联的原始导出 ZIP 读取问题、标准答案、`qrels` 黄金关系和语料正文，再用逐题结果中的 `retrieved_passage_ids` 计算：

- `Precision@K`：前 K 条召回中黄金片段所占比例，K 为该题实际返回条数。
- `Recall@K`：已召回黄金片段占全部黄金片段的比例。
- `Hit@K`：是否至少召回一条黄金片段。
- `MRR`：第一条黄金片段所在名次的倒数。

报告同时展示总问题数、已运行、失败、缺失、不可计算和零召回数量，并按分类、难度、是否可回答、标签和黄金语料来源分组。逐题明细包含问题、标准答案、实际答案、黄金语料、实际召回、错误和耗时。

“下载 JSON 报告”用于机器处理，“下载 HTML 报告”可独立归档和查看，“下载失败样本 CSV”包含执行失败、缺失、不可计算或 Recall 为 0 的问题。两个运行的对比以逐题 Recall 为依据，区分改善、退化、不变、新增缺失和不可比。

空的 `retrieved_passage_ids: []` 表示已执行但零召回，可以计算为 0；完全不提供该字段则表示缺少可计算的召回信息。旧版仅汇总结果没有逐题数据，因此只能展示外部汇总指标，不能反推出本地检索指标。

工具不会用实际答案与标准答案的字符串相等判断答案正确性。外部导入的 ROUGE 等指标会保留展示，但只有人工审核或经确认的评分器才能形成答案正确性结论。

逐题结果可以显式提供答案评价：`answer_judgement` 取 `correct`、`partial` 或 `incorrect`，`answer_score` 是 0～1 的有限数值；`assessment_source` 记录“人工审核”或具体评分器，`review_comment` 保存依据。报告会统计已评价、正确、部分正确、错误、未评价、已评分数和平均分，并支持筛选错误/部分正确及未评价问题。未提供这些字段时保持“未评价”，不会根据答案文本自动推断。

### 失败样本回流

生成离线报告后，可勾选执行失败、缺少结果、不可计算、零召回或 `Recall < 1` 的问题，填写新数据集 ID、名称和版本并创建回流数据集。工具会：

- 从原始导出 ZIP 复制问题、标准答案和黄金语料正文，而不是依赖可能已被修改的当前文本。
- 保留问题的分类、难度、标签、答案核心要点、可回答性、文档约束、测试角色、过滤条件和标记来源。
- 保留运行时已快照的语料来源、标签、元数据和审核状态；旧记录没有快照时兼容使用当前项目值。
- 在问题中记录来源运行 ID、来源数据集版本和失败原因，编辑问题时会显示回流来源。
- 若问题缺少黄金语料、导出包语料不可读或 PID 映射失效，则拒绝创建，避免不完整样本进入下一发布版本。

回流会创建全新的本地数据集，不修改原数据集或评测记录。创建后可以继续补充语料、修正答案、创建快照并导出下一版 WeKnora ZIP。

## 查看 Parquet 内容

Parquet 是二进制列式格式，不能直接使用文本编辑器查看。可以用 DuckDB：

```sql
SELECT * FROM read_parquet('queries.parquet');
SELECT * FROM read_parquet('corpus.parquet') LIMIT 20;
SELECT * FROM read_parquet('qrels.parquet');
```

也可以使用 Python 的 pandas/pyarrow：

```python
import pandas as pd
print(pd.read_parquet("queries.parquet"))
```

## 测试

```bash
go test ./tools/evaluation-dataset-studio
```

测试覆盖项目校验、原子存储、回收站删除、PID 重映射、五文件 ZIP 契约、Parquet 回读、ZIP 反向导入、快照、导出历史、Adapter 契约 fixture、手工评测、外部结果导入、离线指标、报告、运行对比、失败样本回流、评测 API 代理、AI 生成/缓存/审核入库、密钥不落盘和主要 API。

`testdata/external-results/offline-lifecycle.json` 是 10 题脱敏逐题结果 fixture。端到端测试会在临时 workspace 中完成“创建 → 校验 → 快照 → 导出 → 手工运行 → 结果导入 → 报告 → 回流 → 重启复读”，不会访问生产 WeKnora，也不会修改本地真实数据集。

## 当前限制

- 当前只支持一个问题对应一个标准答案。
- 暂不支持 Excel；Parquet 反向导入只接受标准五文件 ZIP。
- CSV 导入为追加模式，暂不提供可视化字段映射和导入前预览。
- 快照和导出历史暂不提供页面删除及自动清理策略。
- AI 生成 P0 仅支持从已维护语料生成候选；暂不支持源文档自动切片、向量语义去重和二次模型评分。
- 当前 WeKnora 仍只支持 `dataset_id=default`，因此工具不能自动部署或并行注册多个远程数据集。
- 当前没有生产版本 Adapter；生产契约未确认前只能使用 `manual-export` 或经过人工确认的本地契约。
- 本工具是单机工具，不提供多人协作和权限管理。
