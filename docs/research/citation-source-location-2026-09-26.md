# 对话引用原文定位：缺陷复现、竞品调研与改造方案

日期：2026-09-26。代码基线：`9fcc2c674`。

## 结论

建议重构引用定位的数据链路，保留现有预览器和引用入口。当前实现能够展示原文，也有入库时保存位置的基础，但位置来源混合了结构数据、文本猜测和比例推算，最终却经常统一表现为“精准定位”。继续调整相似度阈值无法满足精准回溯的要求。

推荐目标：**每个引用绑定可验证的原文证据 ID，证据绑定确定版本的文件、原文块、文本范围和页面区域。点击引用时直接读取这些位置。**

本次完成代码审查式排查、最小复现、官方文档和公开源码调研。没有运行用户实际失败的会话，没有部署竞品做同文档准确率对比，也没有修改业务实现。下文区分已复现缺陷、静态确认的行为和待验证风险；不将竞品的功能说明当作准确率证明。

## 一、当前链路为什么不可靠

```mermaid
flowchart LR
    A[解析 Markdown 和页面结构] --> B[通过文本匹配补原文位置]
    B --> C[清洗改写后重新映射偏移]
    C --> D[切块并保存 source_locators]
    D --> E[模型引用 chunk]
    E --> F[前端根据回答句子猜对应段落]
    F --> G[画框或全文搜索]
```

当前已具备 `SourceBlock → chunk.source_locators → 引用 → PDF/Office 预览` 的贯通；应保留这部分基础。缺的是每一步对证据身份、范围、精度和版本的约束。

### 1. 已实际复现的缺陷

| 问题 | 复现与结果 | 用户影响 |
| --- | --- | --- |
| 对齐失败的段落被上一块吞并 | 三段分别来自第 1、2、3 页，第 2 段的解析文本与结构文本不一致。`Align` 跳过第 2 段后，把第 1 段的范围延伸到第 3 段；查询第 2 段得到 `page=1` | 点击后定位到错误页面，并可携带错误 bbox |
| MinerU 新结构未被消费 | 构造含 `markdown.md` 和带页面几何数据的 `middle_json.json` 的 ZIP；`minerUContentListFromZip` 返回 `nil` | 解析结果有位置信息，但我们没有取出来 |
| 数字含义被归一化抹掉 | 对原文“限值为15毫米，超过不得使用。”搜索“限值为1.5毫米，超过不得使用。”，`findInText` 返回成功范围 | 不同数值被当成完全相同的引用文本 |
| 重复内容直接取首次出现 | 文档目录和正文都有“设备验收合格后方可投入使用”，匹配返回目录处的偏移 | 无结构定位时容易跳到目录、前文重复段落 |
| 回答不能与原文词面匹配时仍选开头 | 中文 chunk 配英文改写回答，`selectQuoteForSentence` 回退到 chunk 首句；`selectLocatorsForSentence` 保留全部候选 | 可能显示无关开头，或把一大片内容当作支持该句的证据 |

相关实现：

- [Align 范围构造](../../internal/sourceloc/align.go)：只用前缀或中部锚点找起点，终点取下一个成功块的起点，最后一块延伸到文末。
- [MinerU ZIP 读取](../../internal/infrastructure/docparser/mineru_layout.go)、[V1 接入](../../internal/infrastructure/docparser/mineru_v1_client.go)：只取旧 `content_list.json`。
- [文本匹配](../../frontend/src/utils/sourceLocator.ts)：去除标点后取首次匹配，模糊候选只需 bigram 覆盖率达到 0.5。
- [引用句选择](../../frontend/src/utils/sourceLocator.ts)：词面相似度阈值和开头回退无法保证语义支持关系。

上述重复文本行为本身是搜索函数的既定行为；问题是上层把它用于缺少范围约束的原文证据定位。

### 2. 静态代码已确认的放大因素

**PDF 有 bbox 就标记 precise，而且不会进一步定位到句子。** [PDF applyLocate](../../frontend/src/components/source-preview/PdfSourceViewer.vue) 中，任何四元素 bbox 都直接画框并设 `precise=true`；没有区分“可靠原文段落”“推测的段落”“精确句子”。`sentence`、`scope` 没有在该 PDF 组件中进一步使用。

**多页精度混合时会漏掉部分目标。** 同一请求只要某个 locator 有 bbox，整组便跳过后面的无 bbox 页面文本定位。这意味着“第一页有框、第二页只有页码”的跨页证据可能只显示第一页。

**旧数据在浏览器搜索时有硬限制。** 没有页码时仅搜索前 500 页，找到第一个匹配就停止；有候选页但引用不匹配时直接降为页面，没有明确的失配原因。扫描 PDF 没有文本层时，这类文本兜底天然无效。

**内容改写的范围映射包含插值。** [Remapper](../../internal/sourceloc/remap.go) 对未匹配的行段按字符长度比例分配偏移。表格转换、图片 URL 改写、合成标题等情况下，长度比例不是来源证明。该点是设计风险，本次没有测量真实文档的漂移分布。

**入库时已经丢失部分细节。** [SourceLocator](../../internal/types/source_locator.go) 的 quote 限制 300 字符；[Index](../../internal/sourceloc/assign.go) 将相交文本赋予整个块的 bbox，每 chunk 最多 64 个定位项。截断后无法依靠这些摘要恢复全部证据范围。合并时同位置去重也不合并对应 quote，可能丢失后半段用于选句的信息。

**引用协议只确定 chunk。** [模型引用协议](../../internal/modelcontext/citations.go) 要求模型输出 `cN`，没有对应证据 span；前端拿回答句子推断“到底引用 chunk 的哪一部分”。检索块越大、回答改写越多，推断越不可靠。

**Office 也存在猜测成功的路径。** [DOCX 定位](../../frontend/src/components/document-preview.vue) 在块序号附近 ±4 个元素中选最相似项，低分时仍退回中心块，然后报告 `precise=true`。应改用与解析结构一致的节点标识；不同渲染器的元素序号不宜充当稳定身份。

**历史与编辑没有完整定位版本契约。** 当前 locator 没有文件哈希、解析版本或证据版本。[chunk 编辑](../../internal/application/service/chunk.go) 更新内容版本，但此路径没有同步重建或显式废止来源定位。编辑后的知识与上传原文可能不同，必须区分“原文证据”和“人工编辑内容”。具体受影响路径需集成测试确认。

## 二、竞品与行业方案

### RAGFlow：最接近的工程参考

检索结果携带 `positions`，预览组件按位置绘制高亮；可以借鉴“解析、分块、检索一路携带位置”的机制。[检索源码](https://github.com/infiniflow/ragflow/blob/313ca90f6abd7682fe8523e16fd67b3653a3fa84/rag/nlp/search.py)、[PDF 预览源码](https://github.com/infiniflow/ragflow/blob/main/web/src/components/document-preview/pdf-preview.tsx)。

它也有精度边界：2026-08-27 合并的修复处理了“子 chunk 继承整个段落 bbox，截图和文本不对应”，采用字符比例垂直切分区域。这能改善预览，但比例切框仍不能证明逐行、逐字精确。我们的目标应要求真实行/词几何信息，不直接照搬这个近似办法。[RAGFlow PR #18864](https://github.com/infiniflow/ragflow/pull/18864)。

### NotebookLM：交互参考

官方文档明确描述悬停显示引用文本、点击跳到引文并查看上下文。这适合作为用户体验标准。其内部证据映射算法没有公开，不能据此声称采用某种 bbox 或模糊匹配实现。调研时原 NotebookLM 帮助链接已转向 Gemini Notebook 页面。[官方说明](https://support.google.com/notebooklm/answer/16179559?hl=en)。

### Dify：基础归属能力，不能直接证明满足原版定位

官方支持 Citation and Attribution。此次核查的文档能够证明来源归属能力，没有给出足以验证 PDF 原版句级坐标精度的协议或准确率。可参考来源展示，但不能把开启引用功能等同于实现精确定位。[官方说明](https://docs.dify.ai/en/cloud/use-dify/knowledge/integrate-knowledge-within-application)。

### Claude Citations：引用协议参考

返回 `cited_text` 和位置；纯文本引用字符范围，PDF 引用页范围，自定义内容引用块范围。其价值是让生成结果带结构化证据指针。PDF 页引用仍需要我们自己的几何映射，且指针有效不等于回答必然被证据支持。[官方协议](https://platform.claude.com/docs/en/build-with-claude/citations)。

### Azure Document Intelligence：文本与几何映射参考

结构元素有全文 `spans`，页面有行/词，位置通过 polygon、boundingRegions 表达。这种“文本区间与页面几何同时保留”的数据模型适合借鉴，不要求切换到其云服务。[官方 Layout 文档](https://learn.microsoft.com/en-us/azure/ai-services/document-intelligence/prebuilt/layout?view=doc-intel-4.0.0)。

## 三、MinerU 4.0 能借鉴什么

### 已核实的能力与边界

1. **稳定块引用。** 定位形如 `doc:{short_id}/tier:{tier}/page:{page}/block:{block}`，块号基于 canonical 结构，不能因渲染时隐藏页眉页脚而重新编号。官方 ADR 同时说明最小公开引用单位是块；不能由此推断单元格、逐字几何定位均已提供。[Block Locator 设计](https://github.com/opendatalab/MinerU/blob/master/docs/next/decisions/0012-doclib-block-locator.md)。
2. **可解析的定位协议。** 当前代码还支持 `/char:{offset}` 游标；这是块内字符读取位置，不自动等价于 PDF 字符框。[实现](https://github.com/opendatalab/MinerU/blob/master/mineru/doclib/locators.py)。
3. **按位置读取视觉证据。** 有 bbox 的 PDF/image 块可以按 locator 从源页面裁图；缺失 bbox 或原文件时返回明确错误。Office 可走图片 sidecar，不能假定其与原文页面坐标一致。[视觉块设计](https://github.com/opendatalab/MinerU/blob/master/docs/next/decisions/0027-doclib-visual-block-locators.md)。
4. **统一结构输出。** 4.0 中间协议通过 `schema` 和 `schema_version` 识别，页与块都保留编号；PDF 几何扩展保存源页尺寸和方向相关信息。标准保存包包含 `markdown.md`、`middle_json.json`、`structured_content.json` 和资源文件，而不是保证附带旧 content list。[输出协议](https://opendatalab.github.io/MinerU/reference/output_files/)。

### 对本项目的具体建议

- **先修正确消费新协议。** 现有 V1 上传、任务轮询、下载链路可以复用。按产物 schema 选择新旧适配器，读取实际 ZIP 或声明支持的结构化 output；不能仅根据服务名称猜格式。
- **解析时生成统一原文结构。** 由同一份结构数据生成检索文本和来源映射，保留块 ID。避免分别拿 Markdown 和结构文本再次猜测对应关系。
- **坐标转换必须有单位。** 旧 content list 的 0–1000 坐标、新结构的页面尺寸/坐标不能共用硬编码 `/1000`。保存单位、页面几何和方向，在适配层明确转换并校验。
- **把块定位用作基础。** 文本 PDF 的句级高亮需要行/词位置；扫描 PDF 需要 OCR 行/词位置。表格没有单元格几何时先准确定位整表并标注粒度，不能按字符串长度分配单元格位置。
- **保留解析结果快照。** 来源身份绑定文件内容哈希和解析版本，引用不依赖某个服务当前缓存恰好存在，也不依赖重解析后块序号保持不变。

升级需检查真实返回包和服务能力；MinerU 官方说明 4.0 的 CLI、配置、API 与旧版存在迁移差异。这里可以沿用已有 V1 传输，不需要为了定位先替换所有解析入口。[迁移说明](https://opendatalab.github.io/MinerU/reference/migration_4/)。

**判断：非常值得借鉴并完善接入；仅升级 MinerU 无法自动修复 chunk 归属、回答证据选择、旧数据和前端精度误报。**

## 四、建议的目标设计

```mermaid
flowchart LR
    A[固定版本的原文件] --> B[原文块与行词几何]
    B --> C[带来源映射的检索 chunk]
    C --> D[模型选择证据 ID]
    D --> E[服务端校验引用]
    E --> F[读取原文证据范围]
    F --> G[预览器直接高亮]
```

### 1. 分清三个层次

- **检索上下文**：可以较大，可以拼接邻居、标题和摘要，服务于回答质量。
- **引用证据**：是具体原文块或其中的连续/不连续范围，不能把检索上下文整体当成证据。
- **显示位置**：是该证据在原版上的一个或多个区域。多栏、跨页、多句证据保留多个框。

模型只选择已提供的证据 ID。服务端检查该证据属于本次可用来源及指定文档版本，再生成公开引用。保留现在的 `cN` 引用兼容层，新增证据级句柄，不让模型编造坐标。

结构有效性校验与语义支持度分开：能验证 quote 来自原文，不代表它支持回答。对候选证据的语义选择可在生成时完成，必要时加入受限的回答后校验；代价、延迟和收益需单独评测。

### 2. 建议数据契约

以下为本项目的设计建议，不是 MinerU 的原生字段：

| 对象 | 关键字段 |
| --- | --- |
| DocumentRevision | knowledge_id、source_sha256、parser/version、parse_revision、artifact_uri |
| SourceBlock | block_id、page/结构路径、原文文本、阅读顺序、结构类型、geometry、mapping_method |
| EvidenceSpan | evidence_id、block_id、块内 code-point `[start,end)`、quote、多个 regions、granularity |
| ChunkSourceSpan | chunk_id/revision、chunk 内范围、source block 内范围；合成内容标记为无直接原文 |
| Citation | answer 范围、一个或多个 evidence_id、source revision |
| LocateResult | status、granularity、regions、reason、request_id |

原始 parser artifact 可放对象存储；数据库保留身份和映射索引。现有 `source_locators` 可以继续用于兼容，避免在一个热路径 JSON 字段中复制整篇行词几何数据。

### 3. 精度与失败状态必须真实

建议状态：`resolved / ambiguous / unavailable / stale`；粒度：`span / block / page / document`。

- 有可靠范围和几何才显示“已定位到原文片段”。
- 只有块框显示“已定位到来源段落”，允许查看上下文。
- 只有页码显示“已定位到第 N 页”，不冒充句级高亮。
- 多处重复且无法消歧时显示候选位置，不静默选择第一处。
- 原文件、解析版本或证据失效，明确告知原因；旧引用不能偷偷绑定到重解析后的其他内容。
- 一次引用支持多段证据，提供“1/3、上一处、下一处”，每处独立处理精度，不能由某一处成功跳过其他目标。

### 4. 文本搜索如何保留

搜索只作为旧数据和缺少结构信息时的兼容路径：先限制文档版本、页/块、上下文，再匹配。保留数值、正负号、比较运算符等有语义的信息。相似度得分不能充当“定位置信概率”。

匹配失败不将目标替换成 chunk 开头。OCR 容错需要独立标记并验证误匹配率。精确文本出现多次时用上下文和原文范围消歧；不能消歧则报告 ambiguous。

### 5. 不同格式的精度承诺

| 格式 | 目标 |
| --- | --- |
| 文本 PDF | 原文句子映射到行/词区域；覆盖多栏、旋转、CropBox、跨页 |
| 扫描 PDF | 使用解析时 OCR 的行/词几何；无足够数据时退到准确块框 |
| PDF 表格/公式/图片 | 用实际结构区域；句柄可引用整表/单元格/图片，粒度由解析能力决定 |
| DOCX | 基于 OOXML 稳定节点和文本范围；如要求分页原版，生成并保存同一版本的 PDF 预览及映射 |
| PPTX / XLSX | slide + shape，sheet + cell/range；保留表头与合并单元格上下文 |
| TXT / Markdown / HTML | 原文字符范围及渲染 source map；网页要求精确回溯时绑定抓取快照 |

## 五、落地顺序与验收

### 阶段 A：先阻止确定的错误定位

1. 将本次复现场景变成回归测试。
2. 修复 `Align` 对未匹配内容的范围吞并；无法证明的范围留空或降为可靠页级。
3. 完善 MinerU 新旧结构适配并引入真实脱敏产物 fixture。
4. 取消小数点等语义差异被忽略后的“精确匹配”；处理重复文本歧义。
5. PDF 每个目标独立定位，修复混合精度漏页；拆分 `precise` 的实际含义。
6. 补齐 legacy 定位失败原因与数据覆盖率记录。

这一阶段能减少误导，但还不能交付完整句级精度承诺。

### 阶段 B：建立证据身份与来源映射

新增原文结构 artifact、DocumentRevision、EvidenceSpan；文本转换逐步维护来源映射，chunk 合并、重排、父子块和人工编辑保留身份关系。接通模型证据级引用、服务端校验和历史消息持久化。前端直接消费解析好的目标。

优先把 PDF 做完整，再逐个接 Office 和网页，避免各格式同时扩张却没有一个达到验收标准。

### 阶段 C：旧数据迁移和评测

- 有旧结构 artifact：离线转换并校验文件哈希。
- 只有 Markdown/chunk：从原文件重新解析；记录待补齐、成功、失败、版本冲突。
- 保留旧引用与旧 chunk 的身份，不能无条件删旧重建。无法恢复时显示可用粒度。
- 新旧链路并行计算结果，仅在通过准确率门槛后切换展示。回滚只切换读路径，保留来源数据。

### 建议验收门槛（待实现目标，不是本次测得的结果）

先标注至少 30 份文档、300 个引用作为开发集，另留独立测试集；这只是起点，不足以证明低于 0.5% 的总体误定位率，应扩大测试量并报告置信区间。

| 指标 | 建议门槛与分母 |
| --- | --- |
| 标记为片段级成功的正确率 | ≥99%，分母为所有报告片段级成功的引用；错页、错段、范围包含明显无关证据分别统计 |
| 成功覆盖率 | 文本 PDF ≥98%，扫描 PDF ≥95%，分母为人工确认可定位的对应格式引用；同时报告全体引用覆盖率与不可定位原因 |
| 错页/错段 | ≤0.5% 的全部有效引用，并单列重复文本和数字近似负例 |
| 几何质量 | 人工证据行覆盖与无关高亮面积分开评估；不能只看“框与目标有交集” |
| 多证据完整性 | 跨页、多段目标全部可达；不能只验证第一处 |
| 交互延迟 | 已缓存文档点击至目标可见 P95 <1.5 秒；冷启动/下载耗时另报 |

必测：中文规范条款、双栏论文、重复页眉、目录与正文重名、表格跨页、合并单元格、OCR 错字、小数/负号/不等式、旋转与裁剪页面、500 页之后、快速切引用、缩放与窗口变化、历史会话、chunk 编辑、重解析、文件换版、无证据回答。

## 六、本次验证记录

- `frontend/src/utils/sourceLocator.test.ts`：14/14 通过。
- Go 定向测试：`internal/sourceloc`、`internal/infrastructure/docparser` 的 Source / Locator / MinerU 布局相关用例通过。
- 用 Go overlay 加载临时测试，不修改仓库业务代码：
  - `TestResearchDroppedBlockMisattribution` 观察到第二页文本返回第一页 locator。
  - `TestResearchV4ZipLayoutLost` 观察到带新结构数据的 ZIP 未被读取布局。
- 直接调用现有 TypeScript 函数确认数值误匹配、重复文本首次命中和低相关度回退。
- 未执行浏览器端真实 PDF 定位验收，也未启动 MinerU 模型解析服务。因此不能声称已测得端到端准确率。

可复查的前端最小用例，在 `frontend` 执行：

```sh
node --import tsx --input-type=module <<'JS'
import { findInText, selectQuoteForSentence } from './src/utils/sourceLocator.ts';
console.log(findInText('限值为15毫米，超过不得使用。', '限值为1.5毫米，超过不得使用。'));
console.log(findInText('目录：设备验收合格后方可投入使用。\n正文：设备验收合格后方可投入使用。', '设备验收合格后方可投入使用'));
console.log(selectQuoteForSentence('第一部分讨论项目背景。第二部分描述关键试验结果。', 'The experiment failed due to temperature.'));
JS
```

本次结果依次为成功范围 `{ start: 0, end: 14 }`、目录范围 `{ start: 3, end: 16 }`、以“第一部分讨论项目背景。”开头的回退候选。

**建议决策：以证据身份和原文映射为中心做一次完整改造；复用当前预览器、上传与解析调度、引用入口。交付以固定数据集的定位正确率和覆盖率为准。**
