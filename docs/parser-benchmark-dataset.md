# 解析引擎公开基准子集

评测输入由 100 个单页可移植文档格式文件（Portable Document Format，PDF）组成。八个解析引擎使用同一份文件清单和相同字节。输入文件与参考标注分开保存，模型和解析服务只接收输入文件。安全散列算法 256 位摘要（Secure Hash Algorithm 256-bit，SHA-256）用于核对输入、参考标注、输出和评测代码。

## 数据来源与抽样

主基准采用 [OmniDocBench 官方数据集](https://huggingface.co/datasets/opendatalab/OmniDocBench)。固定数据版本为 `aa1ee96d106dbe53d0ae59474d75c6e6d9b53fec`，其 JSON 标注包含 1,651 个页面。参考信息包括文字、表格、公式、元素顺序、语言和版式属性。官方评测代码固定为 `193627ae9e97d89188468ed1ee3b7a856ff76044`。

补充基准采用 [Allen Institute for AI 发布的 olmOCR-bench](https://huggingface.co/datasets/allenai/olmOCR-bench)。固定数据版本为 `54a96a6fb6a2bd3b297e59869491db4d3625b711`，官方测试代码固定为 `f7cfe4c22098b154c76b6ec950d1c0a464eecf8d`。该数据集提供原始单页 PDF 和可执行断言。断言覆盖文字存在与缺失、阅读顺序、表格邻接关系和公式等属性。

下表说明各来源的输入形式和实验覆盖范围。

| 来源 | 正式子集 | 冒烟子集 | 输入形式 | 参考标注 |
|---|---:|---:|---|---|
| OmniDocBench | 80 页 | 6 页 | 官方图像无损封装的无文字层 PDF | 官方页面 JSON |
| olmOCR-bench | 20 页 | 2 页 | 官方原始 PDF，保留原有文字层 | 官方断言 JSON |
| 合计 | 100 页 | 8 页 | 82 页无文字层，18 页有文字层 | 来源分别评分 |

OmniDocBench 当前固定发布包提供页面图像和标注。准备程序使用 ReportLab 将原始图像嵌入单页 PDF，保留图像内容，不生成光学字符识别（Optical Character Recognition，OCR）文字层。200 DPI 只用于设置 PDF 页面坐标比例，并记录为包装参数。程序使用 pypdf 检查全部 80 个包装文件的文字提取结果为空，并核对 JPEG 原始压缩流或 PNG 解码像素流与原图一致；验证结果保存在 `wrapper-validation.json`。olmOCR-bench 的 PDF 字节直接下载，程序记录文字层是否存在及可提取字符数。

抽样使用固定种子 `weknora-topic3-parser-v1`。OmniDocBench 按语言、文档类型和版式分层，每层按样本名的带种子 SHA-256 排序，再轮流选取。冒烟集先固定英文论文、中文报告、英文书籍、中文报纸、中英混排教材和中文笔记各一页。olmOCR-bench 按七类测试文件分层，冒烟集含多栏和表格各一页。抽样程序不读取引擎输出或评分结果。

正式子集含英文 52 页、简体中文 30 页、中英混排 17 页、其他语言 1 页。两种来源的语言构成不同，源内指标和输入文字层分组用于解释结果。该实验属于公开基准的固定子集实验，不能作为完整官方排行榜成绩。

## 输出审计

评测范围为生产解析适配器返回的 Markdown 内容，位置处于分块和下游图片 OCR 之前。返回结果包含文档转成的文字、结构和图片引用。适配器成功返回只表示协议执行完成；图片引用可能仍需后续图片识别。

审计程序分别记录 `status` 和 `extraction_state`。前者区分成功、错误、超时、完整性错误和未执行；后者区分存在可用文字、仅图片和无文字。可用文字判定删除 Markdown 图片及其替代文字、图片路径和 HTML 图片标签，再检查是否存在字母或数字。该判定反映正文是否存在，不表示正文正确。

未执行记录以 `not_run` 保存，排除在已执行成功率分母之外。已完成的服务错误保留在已执行分母中，并以空输出参与对应的官方断言。评测工具异常使用 `evaluator_error` 单独记录，既不计作模型回答正确，也不冒充正常计算出的质量失败。

OpenDataLoader 的回退信息来自生产适配器返回的 `parser_fallback` 元数据。统计同时保留请求引擎、实际引擎和回退原因。程序不通过输出相似性猜测回退。

## 官方评分

olmOCR-bench 评分直接导入冻结的 `olmocr.bench.tests.load_single_test` 并执行每个官方测试对象的 `run` 方法。文字、顺序和表格测试使用官方归一化及判断逻辑。公式断言通过 KaTeX 渲染和 Chromium 浏览器计算符号结构。每条断言记录测试标识、类型、通过状态和失败解释。汇总提供测试微平均、页面宏平均与各测试类型分数。

OmniDocBench 评分输入由 `score-parser-benchmark.py` 导出。导出的 `ground_truth.json` 保留官方页面标注，预测 Markdown 使用原始图像文件名的主干命名。两个来源的评分副本均采用 `image-reference-cleanup-v1`：删除图片引用、图片替代文字和图片路径，保留正文、表格、公式和普通图注，并统一为 UTF-8 编码及 LF 换行；原始适配器 Markdown 独立保存。该清理防止图片文件名被匹配成已识别文字或公式。各引擎的 `*-input-manifest.json` 记录规范化函数版本及源码摘要、逐页原始输出摘要、评分输入摘要和运行记录摘要。评测算法采用冻结的官方实现，评分输入口径与完全原样 Markdown 的官方榜单提交存在区别。

`standard` 配置调用官方文字编辑距离、公式字符串编辑距离、表格树编辑距离相似度（Tree Edit Distance based Similarity，TEDS）和阅读顺序编辑距离。`cdm` 配置另包含公式字符检测匹配（Character Detection Matching，CDM）。CDM 配置需要 TeX Live、Ghostscript 和 ImageMagick 等官方渲染依赖。

评分状态以实际生成的结果为准。导出 YAML 配置表示输入准备完成；只有官方评测进程成功结束并产生结果文件，才表示相应指标执行完成。公式字符串编辑距离和 olmOCR 公式断言各自反映其定义的对象，不能替代 CDM 分数。

官方表格主指标采用 `table.page.TEDS.ALL`，先计算每页表格平均值，再计算页面均值；`table.all.TEDS.all` 是表格实例均值。文字、公式字符串和阅读顺序使用各自 `all.Edit_dist.ALL_page_avg`，其值越低表示差异越小。表格相似度值越高表示结构和内容越接近参考标注。缺少 CDM 时，官方 `overall_notebook` 保持空值。

下表列出固定 80 页主基准中具有各类有效参考内容的页面数量，用于核对官方指标分母。

| 指标 | 适用参考页面 | 官方分母机制 |
|---|---:|---|
| 文字编辑距离 | 78 | 非忽略正文类别的实际匹配页面 |
| 公式字符串编辑距离 | 9 | 公式实际评分页面；同时报告参考公式页数量 |
| 表格 TEDS | 17 | 全部非忽略表格参考页，官方对缺失评分页计入零分 |
| 阅读顺序编辑距离 | 79 | 具有有效顺序位置的实际匹配页面 |

主基准中，一页仅含页眉标注，另一页含表格及装饰文字。官方文字类别过滤规则使这两页不进入文字指标，仅含页眉的页面也不进入阅读顺序指标。页面是否具有适用标注与引擎是否成功执行分别记录。

`denominator-audit.json` 对每个已完成引擎保存适用参考页、实际评分页、官方报告分母、实际计算分母、空输出页及服务错误页的纳入数量。该审计还列出参考页被排除的标识和官方表格零分补位的标识。审计程序读取官方逐页结果，保持官方分数原值；发现分母不一致时直接暴露覆盖缺口。已完成标准评分的引擎及逐类分母以审计文件为准；相应类别的服务错误页和空输出页纳入质量评分。

## 复现命令

准备程序适用 Python 3.12，包装库版本记录在 `dataset/parser-benchmark/requirements-prepare.txt`。数据准备与宿主评分使用 Python 3.12。依赖中的二进制扩展绑定解释器版本和操作系统，应在目标机器的独立环境中安装。执行以下命令下载输入、参考标注及冻结的官方评测源码。

```powershell
$benchmarkPython = (Get-Command python3).Source
& $benchmarkPython -m pip install -r dataset/parser-benchmark/requirements-prepare.txt
& $benchmarkPython scripts/prepare-parser-benchmark.py --restore-evaluators --verify-wrappers
& $benchmarkPython -m unittest discover -s scripts -p test_parser_benchmark.py -v
```

准备程序检查 HTTP 响应的声明长度并完整解码图像。网络中断或损坏图像触发重试。参考 JSON 固定使用 UTF-8 编码及 CRLF 换行，以保持不同操作系统上的摘要一致。已有清单的样本标识、PDF 摘要和参考摘要固定；发现变化时，程序报告错误并要求检查包装环境。

以下命令安装官方 olmOCR 测试需要的小型运行依赖，并生成审计、评分和人工清单。Chromium 只承担公式渲染，不调用付费模型。

```powershell
$benchmarkPython = (Get-Command python3).Source
& $benchmarkPython -m pip install --target artifacts/parser-benchmark/data/runtime-deps -r dataset/parser-benchmark/requirements-score.txt
$env:PYTHONPATH = (Resolve-Path artifacts/parser-benchmark/data/runtime-deps).Path
$env:PLAYWRIGHT_BROWSERS_PATH = (Join-Path (Resolve-Path artifacts/parser-benchmark/data).Path chromium)
& $benchmarkPython -m playwright install chromium
& $benchmarkPython scripts/score-parser-benchmark.py --manifest dataset/parser-benchmark/manifest-full.json --runs artifacts/parser-benchmark/runs/baseline-v1
```

正式评分使用单独的 Python 3.10 环境安装冻结的 OmniDocBench 项目。`pyproject.toml` 包含官方依赖版本。若在容器中将项目根目录映射为 `/benchmark`，使用 `--official-path-prefix /benchmark` 生成对应路径的 YAML。进入冻结源码目录后运行 `python pdf_validation.py --config <引擎-standard.yaml>`；公式 CDM 环境完整时运行对应的 `-cdm.yaml`。

当前官方评测容器使用 `wechatopenai/weknora-docreader@sha256:b9c4636b65b5d4947d5e09cd311ba6cf37f1f2da37c51d4be2b911d432f12abe` 中的 Python 3.10.18。容器不开放端口，限制为 2 个 CPU 和 2 GiB 内存；虚拟环境保存在容器 Linux 文件系统的 `/opt/omni-venv`，与生产服务依赖隔离。输入和结果使用挂载目录，Python 依赖使用容器内文件系统，避免 Windows 文件共享的小文件安装延迟。`constraints-official-eval.txt` 固定 `evaluate` 的传递依赖，保留官方直接依赖版本。

新环境在完成数据准备后，可使用下面的 PowerShell 命令创建评测容器。已有同名评测容器时直接使用现有实例。

```powershell
$benchmarkArtifacts = (Resolve-Path artifacts/parser-benchmark).Path
docker run -d --name weknora-parser-official-eval --memory 2g --cpus 2 --mount "type=bind,source=$benchmarkArtifacts,target=/benchmark/artifacts/parser-benchmark" --entrypoint sleep wechatopenai/weknora-docreader@sha256:b9c4636b65b5d4947d5e09cd311ba6cf37f1f2da37c51d4be2b911d432f12abe infinity
docker exec weknora-parser-official-eval python -m venv --system-site-packages /opt/omni-venv
docker cp dataset/parser-benchmark/constraints-official-eval.txt weknora-parser-official-eval:/tmp/constraints-official-eval.txt
docker exec weknora-parser-official-eval /opt/omni-venv/bin/python -m pip install --no-compile --timeout 120 --retries 5 -e /benchmark/artifacts/parser-benchmark/data/upstream/omnidocbench-code -c /tmp/constraints-official-eval.txt
```

官方评测环境准备完成后，以下命令执行普通审计、olmOCR 官方断言和 OmniDocBench 标准指标。解析已完成且完整性核对通过的引擎进入正式评分，未完成引擎保留待运行状态。

```powershell
$benchmarkPython = (Get-Command python3).Source
& $benchmarkPython scripts/score-parser-benchmark.py --manifest dataset/parser-benchmark/manifest-full.json --runs artifacts/parser-benchmark/runs/baseline-v1 --official-container weknora-parser-official-eval
```

`official-omnidocbench/execution-receipts.json` 记录实际命令、退出码、耗时、输入指纹、配置指纹和结果摘要。重复执行时，仅复用退出成功且全部摘要仍匹配的结果。指纹覆盖规范化版本、逐页输出、运行记录、参考标注、配置和冻结评测代码。官方逐页结果和运行环境位于同目录的 `result/` 子目录，日志与各引擎分别保存。`summary.json` 的 `official_omnidocbench_runs` 和 `official_omnidocbench_denominators` 字段分别提供实际执行记录及分母审计，后续解析记录补齐后复用同一命令刷新评分。

## 人工核验

`human-review.json` 保存全部样本的待审核记录，`recommended_first_pass` 列出优先核验的最多 20 页。人工清单关注表格数字与单元格归属、公式符号与上下标、多栏阅读顺序、遗漏与重复、服务失败、回退记录及引擎差异。`human-review.html` 提供原始 PDF 和各引擎输出链接。

实际审核者填写 `reviewer`、`reviewed_at`、`decision` 和备注。审核指纹绑定输入、参考标注、该页全部引擎输出摘要、结果状态和原始运行记录摘要。只有指纹完全相同才继承签名。输出补齐、模型或运行记录变化时恢复 `pending`，过期签名移入 `review_history`；没有结果指纹的历史签名也进入历史记录。AI 生成的核验建议和程序完成的散列检查不计作人工签名。

## 数据使用说明

[OmniDocBench 官方说明](https://github.com/opendatalab/OmniDocBench#copyright-statement)将数据用于学术研究，原始文档权利归相应权利人。[olmOCR-bench 数据卡](https://huggingface.co/datasets/allenai/olmOCR-bench/blob/main/README.md)标记 Open Data Commons Attribution License（ODC-BY）许可；原始文档仍可能具有独立权利说明。项目保存来源与许可链接，Git 提交清单、摘要、程序和方法说明；下载的原始文档与参考内容保存在忽略目录。
