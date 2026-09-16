# CMRC 2018 中文阅读理解 · 24 题

中文机器阅读理解开发集固定抽样，24 道可回答题与 8 段额外候选原文。

本包版本为 `v1`，类型为 `evaluation`，语种为 `zh-CN`。
安全散列算法 256 位（Secure Hash Algorithm 256-bit，SHA-256）用于校验源文件与生成文件。

## 内容与用途

| 对象 | 数量 |
| --- | ---: |
| 段落 | 32 |
| 题目 | 24 |
| 相关性标注 | 24 |
| 可回答题目 | 24 |
| 指定原文无答案题目 | 0 |
| 额外未标注干扰段落 | 8 |
| 可上传原文文件 | 32 |

表中数量对应 `registry-input.json` 的实际内容。`passages` 存储原文及出处，`questions` 存储问题与单个参考答案，`relevance` 存储题目与原文的已知相关性。`grade=1` 表示原标注可回答上下文，`grade=0` 表示原标注无答案上下文；没有标注的组合保持缺失。

`source_docs/` 中的文件可通过知识库的文档上传入口使用。`registry-input.json` 供评测数据导入；文档类型的包包含空题目列表，仅用于资料归档和上传。`annotations.json` 保留评测题目的原始多答案标注；`source-samples.json` 保留抽样题目、原文与上游位置，支持独立核对。

## 来源与许可

本包来源为 [https://github.com/ymcui/cmrc2018](https://github.com/ymcui/cmrc2018)，许可为 `CC-BY-SA-4.0`。`LICENSE` 与 `NOTICE` 保存许可和归属说明。文件及抽样说明以 `manifest.json` 为准。

- [squad-style-data/cmrc2018_dev.json](https://raw.githubusercontent.com/ymcui/cmrc2018/c0eb1b6ba219847457e6af3180da722bbeb656af/squad-style-data/cmrc2018_dev.json)，版本 `c0eb1b6ba219847457e6af3180da722bbeb656af`，SHA-256 `e9ff74231f05c230c6fa88b84441ee334d97234cbb610991cd94b82db00c7f1f`。
- [LICENCE](https://raw.githubusercontent.com/ymcui/cmrc2018/c0eb1b6ba219847457e6af3180da722bbeb656af/LICENCE)，版本 `c0eb1b6ba219847457e6af3180da722bbeb656af`，SHA-256 `7abe19ec9bb73b36141b999b861d24ad855e808bafe0f81e84cce28556f6c297`。

## 复现与校验

以下命令在项目根目录执行，Python 3.10 或以上版本即可运行。脚本仅使用标准库。

```powershell
python scripts/prepare-public-evaluation.py --check --dataset cmrc2018-dev
python scripts/prepare-public-evaluation.py --dataset cmrc2018-dev --cache-dir artifacts/public-datasets/source-cache
python scripts/prepare-public-evaluation.py --dataset cmrc2018-dev --cache-dir artifacts/public-datasets/source-cache --download
python scripts/prepare-public-evaluation.py --check --verify-source --dataset cmrc2018-dev --cache-dir artifacts/public-datasets/source-cache
```

`--check` 使用仓库内的精简证据与文件摘要，离线核验结构、引用关系、答案偏移和生成内容。`--verify-source` 进一步读取本地完整源缓存，重跑固定抽样并核对来源。生成命令默认要求已有缓存；`--download` 仅下载缺失的固定版本源文件，任何摘要不匹配均报错。全部命令均无模型调用和业务数据库写入。

## 适用边界

- 固定开发集子集用于导入、检索和生成链路试验；样本分数不能解释为官方完整基准分数或总体能力估计。
- 原始数据为给定上下文的阅读理解任务。跨段落候选语料是本项目的检索适配；仅原始题目与上下文之间存在标注，其余组合保持未标注。
- 额外候选段落属于未标注干扰段落，可能包含答案；不将其转换为可靠负例。检索指标仅描述命中已标注原文的情况。
- 注册格式只保存一个参考答案，取原始 answers 中第一个答案；annotations.json 保留全部原始答案及字符偏移。平台指标与官方多参考答案评分规则不同。
- 原文来自上游收录的维基百科快照，可能含时效性、文本噪声及内容偏差；不作为当前事实核验来源。
- 抽样候选要求全部参考答案具备有效字符偏移并与原文逐字匹配。上游不满足条件的题目数量记录在 sampling.upstream_counts.invalid_questions_excluded；筛选会改变样本分布。
