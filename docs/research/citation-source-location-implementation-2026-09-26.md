# 引用原文定位：实现与测试记录

日期：2026-09-26。配套调研：[竞品与 MinerU 调研](citation-source-location-2026-09-26.md)。

## 已实现

1. **完整证据校验。** 删除用开头/中间片段猜测整段位置的逻辑，禁止把未匹配内容归入前一个来源块。保留小数、负号、百分比、比较符号；支持全角字符、HTML 实体和 Unicode 字符偏移。重复文本必须有原始位置或完整上下文才能消歧。
2. **保留证据范围。** 引用的原文片段不再截断为 300 字，位置列表不再截断为 64 个。分块存在未映射内容时保存 `partial`，预览不得把局部成功报告为完整精确定位。
3. **来源身份。** 定位 JSON 增加 `mapping`、`source_id`、`source_hash` 和 `partial`。使用知识记录的现有 FileHash 识别来源版本。内容编辑时清除分块及受影响父块的旧位置，并在同一个版本写入事务中持久化。
4. **PDF 解析修复。** 未匹配行保留页级位置，不继承其他段落的框；稀疏页面不再合并相距很远的段落；旋转/裁剪页按未旋转的 PDF 用户坐标判断字形可见性；排版重建如果改变数值，保留原生文本。
5. **PDF 预览修复。** 每个文档单独拥有 worker，避免快速切换时复用正在销毁的 worker。兼容真实 DOMRect 的属性访问方式。逐一处理带框和不带框的来源，保留跨页证据，支持上一个/下一个位置。旧位置经全篇文本核验，取消 500 页上限。
6. **明确显示精度。** 区分文字匹配、来源区域、页级定位、重复内容、失效版本和未找到。扫描页只有块坐标时显示区域级结果，不称为文字级精确定位。
7. **其他格式。** Word 去掉邻近段落相似度猜测；PPTX、EPUB、表格只使用经过验证的结构位置，旧记录通过完整原文匹配；缺失工作表不再默认选择第一张表；文本偏移使用原文验证；Markdown 重复内容不选第一个。
8. **MinerU 兼容。** 支持 `docvortex.middle` / `schema_version=2.0` 的 `middle_json.json`，读取原始页索引、块标识、嵌套子块和 `[0,1]` 坐标。旧 `content_list.json` 保留 `[0,1000]` 转换。旧包含不兼容 middle JSON 时仍可回退旧格式，未知协议不猜坐标单位。

## 测试结果

| 范围 | 结果 | 重点 |
|---|---|---|
| Go：sourceloc、docparser、service、repository 全包普通测试 | 通过 | 对齐、改写、分块、持久化、编辑失效 |
| Go：sourceloc、docparser、repository 全包 race 测试，以及 service 来源/编辑相关测试 | 通过 | 本次改动相关并发检查 |
| 前端定位与多语言测试 | 79 项通过 | 数值差异、重复段落、501 页、跨页、取消、部分映射、DOMRect、表格行列对齐 |
| Python PDF 相关测试 | 68 项：65 通过、3 跳过 | 实际 PDFium 解析、旋转裁剪、重复段落、源位置传输 |
| 生成文档的浏览器预览 | 30/30 通过 | PDF、Word、Excel、EPUB、Markdown、纯文本 |
| Vue 类型检查与生产构建 | 通过 | 构建仍提示既有的大体积 bundle |

浏览器测试的 5 个 PDF 场景使用真实 Python 解析器输出的位置，再交给 PDF.js 渲染，检查文字框中心位于解析器的来源区域内。覆盖 0°、90°、180°、270° 旋转和非零 CropBox。另验证旋转页在 120% 缩放时高亮随内容缩放。

测试入口与重现方法：[浏览器回归说明](../../frontend/tests/source-locate/README.md)。测试夹具只含生成的示例文字。

### 未通过的扩大检查

对整个 service 包运行 `-race` 时，技能安装模块的测试报告数据竞争，例如：

- `TestRegisterCatalogDoesNotMoveTheDefinitionWhenPinFails`
- `TestHostInstallActivatesVersionAndMarksReady`
- `TestHostRemoveDeletesFilesAndRow`

栈指向 `tenant_skill_install_test.go` 的 `installSkillRepo.UpdateSkill` 与技能安装测试/后台 goroutine 同时读写。上述文件不在本次改动中。service 普通测试通过，本次来源定位与编辑相关的 race 测试通过；不能把整个仓库的并发检查称为全部通过。

Python 跳过的 3 项依赖未提供的 `resnet.pdf`、`gpt3.pdf`、`scan_en_dict.pdf`。新增的实际 PDF 文件测试没有跳过。

## 历史数据与验收范围

- 已有原生 PDF 引用会通过完整原文重新核验，不直接信任旧版推测的框。
- 历史扫描 PDF 若只有旧坐标或没有可搜索文字，需要重新解析以生成经过验证的新位置。前端无法从缺失的数据恢复精确坐标。
- 新增 JSON 字段兼容旧数据，无需新增数据库列。来源修复涉及 Go、docreader 和前端，更新时需要同步发布。
- `source_hash` 使用现有 FileHash，不等于浏览器对下载字节重新计算并校验，也没有新增不可变原文件快照。
- MinerU 4.0 已做协议夹具与适配测试，尚未连接真实 MinerU 4.0 服务运行生产文档。
- 初轮只有生成文档测试，未覆盖真实 Agent 合并引用、AnyDoc 列序表格和图片 OCR。后续已按本地日志重放真实引用，结果见下方；样本通过不等于任意文档都能精确定位。
- 尚未实施调研中更大的“模型输出证据 ID / 引用到回答的逐句绑定 / 原文件快照”方案。这轮解决的是已引用来源到原文的位置映射与展示。

## 主要复测命令

```sh
go test ./internal/sourceloc ./internal/infrastructure/docparser ./internal/application/service ./internal/application/repository -count=1
go test -race ./internal/sourceloc ./internal/infrastructure/docparser ./internal/application/repository -count=1
go test -race ./internal/application/service -run 'Test(AttachStructureBlocks|TranscriptWithSegments|BuildSourceIndex|UpdateDocumentChunk|RebuildParentContent|ValidateEditedChunkImages|SyncEditedChunkImages)' -count=1

docreader/.venv/bin/python -m unittest docreader.tests.test_source_locator_pdf docreader.tests.test_source_locator docreader.tests.test_pdf_router docreader.tests.test_pdf_embedded_images

cd frontend
npm run type-check
npm test -- src/utils/sourceLocator.test.ts src/utils/pdfSourceLocate.test.ts src/i18n/localeKeyAudit.test.ts
npm run build
```


## 按本地真实 query 日志复测

用户反馈后，读取 19:45–19:47 的本地 query、分块接口返回值和原始 PDF/DOCX，
重放其中 13 条不同引用，并在实际聊天页面点击正文及截图引用。
原文件和分块正文仅保存在本地被 Git 忽略的回放目录中。

### 根因与修复

- Agent 工具结果按文档合并时把 chunk ID 换成 knowledge ID，且没有保存成员 ID。
  统一使用保留 `chunk_ids` 的聚合函数，点击仍解析到原始引用分块。
- 引用文件名中的 `&amp;` 再次转义，文件名兜底失效。属性解析先解码一次，再安全转义；
  共享点击处理器传递文档名、知识库 ID。重复文件名不默认选择第一项。
- 图片占位符 `![descript](...)` 让本来完整的文字映射被误标为 partial。
  后端计算覆盖率前去掉图片占位；前端对旧 partial 记录仍逐段验证，
  引用句已经完整匹配时可以使用该段的位置。历史回答中缓存的位置由当前 chunk 覆盖，
  已编辑分块或文档身份不符时不能继续使用旧坐标。
- AnyDoc 表格按列拼接，与 PDF 的按行文本顺序不同。完整匹配失败后，按真实单元格/
  完整句子独立验证片段，以多个原文证据确定页面；重复项需要唯一页面上下文。
  高亮仅覆盖逐字核验的范围，保留数值差异检查。引用句只影响已核验位置的导航顺序。
  未覆盖整个 chunk 时明确显示“部分引用内容尚未匹配”。
- 三条 DOCX 引用来自截图 OCR，无法在 Word 正文搜到。用 OCR 图片的资源 ID
  在父 chunk 中找到图片，再以原文相邻段落确定唯一图片区域。chunk 边界缺少一侧
  文字时，以原文件中下一个/上一个非空段落限制范围；有多张候选图片时拒绝猜测。
  这种结果显示区域定位，不宣称截图内部文字坐标已精确识别。

### 验收结果与边界

- 13/13 条真实引用找到了可核验的位置：6 条完整文字匹配、4 条 PDF 部分文字匹配、
  3 条 DOCX 图片区域。PDF 高亮页面分别为第 1、2、7、5、11–12 页，已断言页码。
- 生成文档浏览器测试 28/28 通过，包含重复段落、错页、小数差异、跨页、旋转裁剪、
  旧版 partial 标记、截图唯一性以及 chunk 边界截图。
- 对应前端测试、i18n 审计、Vue 类型检查、生产构建以及 Go sourceloc race 测试通过。
- 课程表解析顺序、OCR 错字等未覆盖部分仍无法声称完整精确；新解析数据应优先保存
  原始块/单元格坐标。此次恢复历史引用不需要批量重解析或修改用户文档。

本地开发页曾保留热更新前的旧组件，整页刷新后再次核验通过。验收请刷新页面；无需重新上传文档。

## 两张后续截图的回归修复

继续检查 20:58、21:00 的真实 query、当前分块及原始 PDF。两条新分块均没有
可直接使用的位置数据；重新解析后的 Markdown 仍存在列序和字体符号差异。

- **五年级课程表：** 课程按列连在一起，多数课程名又出现在其他年级，无法以
  唯一短语确定行。新增双列顺序对齐：同一原文行必须同时满足两个不同列的值，
  完整序列必须唯一，并受年级标题约束。不能跳过已识别的中间数据行来拼凑序列。
  当前实例定位到第 7 页，40 处高亮覆盖验证通过的课程和地点；实际聊天点击已核验。
  逐单元格兜底也必须遵守年级范围，不能因为整体表格未匹配而跳到其他年级。
- **电脑端选课步骤：** 分块跨第 12–15 页，混有图片说明，箭头在 Markdown 中变成
  `à`，PDF 字体提取为私用字符。按中文上下文中的箭头边界拆分，逐段验证完整文字；
  要求多个独立证据保持原文顺序并位于相邻页面。引用句只给已核验的片段排序。
  当前实例首次跳到第 13 页，高亮“选修课、选课报名、申请选修”对应的原文步骤；
  其他已核验片段位于第 12、14 页。实际聊天点击已核验。

两种结果都显示“已高亮核验通过的原文片段，部分引用内容尚未匹配”。未核验的
单元格、图片和乱码不进入高亮，也不报告整个分块已经完整精确匹配。

本轮最终测试：79 项前端测试、30/30 个生成文档浏览器案例、15/15 条真实引用回放，
Vue 类型检查、生产构建和 `git diff --check` 通过。新增反例包含其他年级的同名课程、
列值错行、重复表格、跳行拼接及数值变化。私有原文件和引用正文仍只保留在忽略目录。
