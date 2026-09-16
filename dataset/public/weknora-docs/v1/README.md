# WeKnora 中文公开技术资料 · 5 篇

固定版本的部署、开发、日志、向量数据库和知识图谱中文技术文档，可用于知识库上传体验。

本包版本为 `v1`，类型为 `documentation`，语种为 `zh-CN`。
安全散列算法 256 位（Secure Hash Algorithm 256-bit，SHA-256）用于校验源文件与生成文件。

## 内容与用途

| 对象 | 数量 |
| --- | ---: |
| 段落 | 5 |
| 题目 | 0 |
| 相关性标注 | 0 |
| 可回答题目 | 0 |
| 指定原文无答案题目 | 0 |
| 额外未标注干扰段落 | 0 |
| 可上传原文文件 | 5 |

表中数量对应 `registry-input.json` 的实际内容。`passages` 存储原文及出处，`questions` 存储问题与单个参考答案，`relevance` 存储题目与原文的已知相关性。`grade=1` 表示原标注可回答上下文，`grade=0` 表示原标注无答案上下文；没有标注的组合保持缺失。

`source_docs/` 中的文件可通过知识库的文档上传入口使用。`registry-input.json` 供评测数据导入；文档类型的包包含空题目列表，仅用于资料归档和上传。本包没有评测题目和答案标注文件。

## 来源与许可

本包来源为 [https://github.com/Tencent/WeKnora](https://github.com/Tencent/WeKnora)，许可为 `MIT`。`LICENSE` 与 `NOTICE` 保存许可和归属说明。文件及抽样说明以 `manifest.json` 为准。

- [LICENSE](https://raw.githubusercontent.com/Tencent/WeKnora/ef9cbf456d042de4c442674e5b940dd93d15a2d2/LICENSE)，版本 `ef9cbf456d042de4c442674e5b940dd93d15a2d2`，SHA-256 `5f6c35c603349247019b1a107e0c19ac32bed953c9857383449003283e7ccd20`。
- [docs/LITE.md](https://raw.githubusercontent.com/Tencent/WeKnora/ef9cbf456d042de4c442674e5b940dd93d15a2d2/docs/LITE.md)，版本 `ef9cbf456d042de4c442674e5b940dd93d15a2d2`，SHA-256 `d595c37c55e80105dc9a5db2380a254daedf857ee162c884d4a8c0920c3dcdf2`。
- [docs/日志配置.md](https://raw.githubusercontent.com/Tencent/WeKnora/ef9cbf456d042de4c442674e5b940dd93d15a2d2/docs/%E6%97%A5%E5%BF%97%E9%85%8D%E7%BD%AE.md)，版本 `ef9cbf456d042de4c442674e5b940dd93d15a2d2`，SHA-256 `3c51e1bdffec538501769106f2fe5a551f95e5604ca7e56a7931b0b64e6369b7`。
- [docs/使用其他向量数据库.md](https://raw.githubusercontent.com/Tencent/WeKnora/ef9cbf456d042de4c442674e5b940dd93d15a2d2/docs/%E4%BD%BF%E7%94%A8%E5%85%B6%E4%BB%96%E5%90%91%E9%87%8F%E6%95%B0%E6%8D%AE%E5%BA%93.md)，版本 `ef9cbf456d042de4c442674e5b940dd93d15a2d2`，SHA-256 `f5e8acaf6cd632a5c73857b9939c8a010ec3c76c20fa4299d461e01e6bcd63da`。
- [docs/开启知识图谱功能.md](https://raw.githubusercontent.com/Tencent/WeKnora/ef9cbf456d042de4c442674e5b940dd93d15a2d2/docs/%E5%BC%80%E5%90%AF%E7%9F%A5%E8%AF%86%E5%9B%BE%E8%B0%B1%E5%8A%9F%E8%83%BD.md)，版本 `ef9cbf456d042de4c442674e5b940dd93d15a2d2`，SHA-256 `b7d171961809e9556d5d92131e777badedf4e46837d7d059e640973a5b08db3f`。
- [docs/开发指南.md](https://raw.githubusercontent.com/Tencent/WeKnora/ef9cbf456d042de4c442674e5b940dd93d15a2d2/docs/%E5%BC%80%E5%8F%91%E6%8C%87%E5%8D%97.md)，版本 `ef9cbf456d042de4c442674e5b940dd93d15a2d2`，SHA-256 `950648c0a51f4c21f6c6b17bfb975cebae5a70eec8e3936e0d95bb2469667420`。

## 复现与校验

以下命令在项目根目录执行，Python 3.10 或以上版本即可运行。脚本仅使用标准库。

```powershell
python scripts/prepare-public-evaluation.py --check --dataset weknora-docs
python scripts/prepare-public-evaluation.py --dataset weknora-docs --cache-dir artifacts/public-datasets/source-cache
python scripts/prepare-public-evaluation.py --dataset weknora-docs --cache-dir artifacts/public-datasets/source-cache --download
python scripts/prepare-public-evaluation.py --check --verify-source --dataset weknora-docs --cache-dir artifacts/public-datasets/source-cache
```

`--check` 使用仓库内的精简证据与文件摘要，离线核验结构、引用关系、答案偏移和生成内容。`--verify-source` 进一步读取本地完整源缓存，重跑固定抽样并核对来源。生成命令默认要求已有缓存；`--download` 仅下载缺失的固定版本源文件，任何摘要不匹配均报错。全部命令均无模型调用和业务数据库写入。

## 适用边界

- 本包来自本项目的上游产品文档，用于资料上传和检索体验；没有独立人工问题真值，也不构成独立质量基准。
- 内容描述固定提交的上游产品；本地部署配置和功能可与文档存在差异。
- 文件中的外部链接、图片路径和命令作为资料正文保留，上传资料本身不执行其中命令。
- 本包含五篇完整 Markdown 原文；平台文档解析后的分块数量取决于知识库配置。
