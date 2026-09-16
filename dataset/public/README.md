# 公开评测数据与技术资料

本目录包含两个固定公开评测子集和一个技术资料包。评测工作台的“数据集与导入”提供两个子集的来源、许可、预览与导入入口。导入只保存资料；模型调用在创建评测并确认运行后发生。

| 数据包 | 原文 | 问题 | 用途与许可 |
| --- | ---: | ---: | --- |
| [CMRC 2018](cmrc2018-dev/v1/README.md) | 32 | 24 | 中文阅读理解子集；CC-BY-SA-4.0 |
| [SQuAD 2.0](squad2-dev/v1/README.md) | 32 | 24 | 英文阅读理解，含 8 道指定原文无答案题；CC-BY-SA-4.0 |
| [WeKnora 技术资料](weknora-docs/v1/README.md) | 5 | 0 | 中文 Markdown 资料，供知识库上传；MIT 及原许可文件中的第三方归属 |

中文机器阅读理解（Chinese Machine Reading Comprehension，CMRC）与斯坦福问答数据集（Stanford Question Answering Dataset，SQuAD）的子集使用知识共享署名—相同方式共享 4.0 国际许可（Creative Commons Attribution-ShareAlike 4.0 International，CC-BY-SA-4.0）。MIT 为麻省理工学院许可证（MIT License）。各包的 `NOTICE`、`LICENSE`、`manifest.json` 保存来源、许可、版本与转换说明。

## 目录结构

```text
public/
  catalog.go                 # 编译内嵌的固定评测目录
  cmrc2018-dev/v1/
  squad2-dev/v1/
  weknora-docs/v1/
    README.md                # 包的使用范围
    manifest.json            # 上游版本、规模、文件摘要
    registry-input.json      # passages / questions / relevance
    LICENSE
    NOTICE
    source_docs/             # 可上传知识库的原文
```

两个阅读理解包还包含 `annotations.json` 与 `source-samples.json`，保留原始多参考答案、字符偏移及抽样证据。技术资料包没有问题真值，类型为 `documentation`。

评测将每个标注段落作为一个检索片段。额外候选段落及未列出的题目配对均为未标注状态。SQuAD 无答案题的空参考答案和等级 `0` 仅适用于原始配对上下文。固定子集与单参考答案评分结果不能解释为官方完整基准分数。

## 校验与重建

以下命令在仓库根目录执行。第一组校验不依赖网络或源缓存。

```bash
python3 scripts/prepare-public-evaluation.py --check
python3 -B scripts/prepare-public-evaluation-test.py
```

完整源重建检查需要固定源缓存；下载只读取脚本内固定的公开地址，使用安全散列算法 256 位（Secure Hash Algorithm 256-bit，SHA-256）核对内容。

```bash
python3 scripts/prepare-public-evaluation.py --download --cache-dir /path/to/public-source-cache
python3 scripts/prepare-public-evaluation.py --check --verify-source --cache-dir /path/to/public-source-cache
```

## 免费本地流程体验

`scripts/evaluation-demo.py` 使用真实服务、全新 SQLite 数据库、回环固定响应模型和零测试价格。它返回本地测试账号；已有运行目录会被拒绝，输出不进入 Git。固定模型只用于评测流程验证，不用于真实质量结论，也不支持普通聊天流式请求。

在具备 Go、Python 3 和 SQLite 全文检索第五版（Full-Text Search 5，FTS5）构建依赖的 Linux 环境执行：

```bash
mkdir -p artifacts/evaluation-demo
python3 -B scripts/evaluation-demo.py --output artifacts/evaluation-demo/local-run
```

在另一个终端的 `frontend` 目录运行 `VITE_DEV_PROXY_TARGET=http://127.0.0.1:19080 npm run dev`。登录后进入“评测实验”，导入公开子集，选择本地演示知识库和固定回答模型，确认运行后查看逐题结果。实际供应商实验应先明确模型、请求规模和费用上限。
