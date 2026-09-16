# OpenRouter 配置与验收

## 模型配置

模型管理支持配置远程对话模型和向量模型。OpenRouter 使用 `https://openrouter.ai/api/v1`，供应商选择 OpenRouter，模型名称采用账户实际可访问的完整标识。应用程序编程接口（Application Programming Interface，API）密钥只保存在执行者的私有配置或加密模型配置中。

知识库绑定一个向量模型；导入文档后生成分块与索引。更换向量模型需要重新生成对应索引。问答模型通过会话或评测配置选择。实验记录实际模型、向量维度、检索模式、重排参数及供应商路由。

## 价格与账本

模型价格页按美元建立生效时间明确的价格版本。OpenRouter 的目录价格来自 [对话模型目录](https://openrouter.ai/api/v1/models) 与 [向量模型目录](https://openrouter.ai/api/v1/embeddings/models)。目录价格可能是多个路由的最低报价，具体请求还记录供应商报告的费用。

成功请求包含 `usage.cost`、模型供应商为 OpenRouter、价格币种为美元时，账本以供应商报告金额结算。金额按四舍五入转换为微美元，即一美元的百万分之一。模型快照的 `billing_usage` 保留原始十进制费用、价格估算值及费用来源。模型身份与价格快照继续使用不可变校验。供应商未报告费用时，账本使用完整的价格及用量条件计算估算值；缺失价格的记录保留未定价状态。

## 零费用回归

```sh
make evaluation-verify
```

确定性评测使用固定供应商响应，验证指标计算、导出、缓存恢复和故障状态。真实质量实验单独记录模型、数据版本、费用及供应商请求信息。持续集成（Continuous Integration，CI）工作流执行确定性检查。仓库维护者需要将 `Deterministic golden evaluation` 设为目标分支的必需检查；定时触发依赖工作流存在于默认分支，实际安装状态由托管平台确认。

## 真实模型验收

真实验收脚本在独立 Linux 容器中使用 SQLite 数据库，API 密钥经标准输入传递。应用仅持有本地代理占位凭据，所有付费请求经过预算代理。运行前准备带 `sqlite_fts5` 构建标记的后端二进制，并配置 Jieba 词典路径。

```sh
BUILD_REVISION="$(git rev-parse HEAD)"
go build -buildvcs=false -tags sqlite_fts5 \
  -ldflags "-X github.com/Tencent/WeKnora/internal/buildinfo.CommitID=$BUILD_REVISION" \
  -o /private/bin/WeKnora ./cmd/server
python3 scripts/evaluation-openrouter-acceptance.py \
  --output /private/evidence/run-01 \
  --budget-file /private/evidence/budget.json \
  --server-binary /private/bin/WeKnora
```

脚本从标准输入读取包含 `openrouter_key` 和 `approved_usd: 20` 的 JSON 对象。输出目录必须不存在，父目录必须存在。`approved_usd` 是同一项目的累计额度，不是每批新增额度。已有预算文件必须沿用；不得通过新建账本清零历史费用。各批次复用同一个预算文件，跨进程排他锁限制同一时间仅运行一个付费批次。请求发出前写入保守费用预留；供应商报告费用后结算，未确定费用保留预留金额。禁止使用 Python 的 `-O` 参数。

公开数据实验分别使用中文阅读理解数据集 CMRC2018（Chinese Machine Reading Comprehension 2018）与斯坦福问答数据集 SQuAD2（Stanford Question Answering Dataset 2.0）的固定开发集子集。每个子集包含 24 个问题和 32 个候选段落。重启缓存实验使用固定的 32 个段落及其中 2 个问题，运行五组冷启动与重启后复用对照。完整开发集或生产流量上的性能需要对应的数据与实验。

`evaluation-openrouter-probes.py` 通过生产 Wiki 页面提示词和模型适配器执行内容变化失效及 30 组固定前缀对照。两组使用相同资料与生成参数，改变共享资料块的位置，交替请求顺序，并固定上游路由。供应商缓存收益按真实观察值报告。

模型辅助复核使用 `evaluation-openrouter-judge.py`，按问题交替交换匿名答案 A、B 的顺序，将 DeepSeek V4 Pro 的正确性及证据支持性判断单独保存。单个模型评分者的判断需要结合原始答案与证据审查。两个脚本通过 `--probe-binary` 指定使用生产适配器的探测程序：

自动复核意外中断后，使用相同参数加 `--resume` 继续。脚本验证源结果摘要和评分提示词，沿用冻结报价，并跳过已有逐题结果；无效评分保留原始输出，避免自动重复扣费。

```sh
go build -buildvcs=false -tags sqlite_fts5 \
  -ldflags "-X github.com/Tencent/WeKnora/internal/buildinfo.CommitID=$BUILD_REVISION" \
  -o /private/bin/evaluation-provider-probe ./cmd/evaluation-provider-probe
```

实验输出包括结果 JSON、逗号分隔值（Comma-Separated Values，CSV）文件、数据库、供应商收据与费用预留记录。自动指标和模型辅助复核保留各自身份；人工评分字段只接受真实评分者的结果。

## 扩展数据与实验复现

扩展数据准备脚本读取 CMRC2018、SQuAD 2.0 和多跳问答 HotpotQA 的固定公开版本，记录许可证、下载文件摘要、无效标注排除和来源分组。默认生成 96 道调试题、600 道正式题，以及两套各含 500 段和 50 道可回答题的检索数据。文件处理依赖 Python 与 `pyarrow`。

```sh
python3 scripts/prepare-expanded-evaluation.py --output /private/evidence/data
python3 scripts/prepare-expanded-retrieval.py --data /private/evidence/data
```

`evaluation-expanded-reader.py` 使用生产聊天适配器回答原始上下文问题。`--data` 指定上述数据目录，`--split` 选择 `tuning` 或 `holdout`，`--baseline` 指定保存的对照提示词 YAML 文件。`--source-commit` 必须与 `--probe-binary` 的构建标识一致；编译时用 `-ldflags` 写入 `internal/buildinfo.CommitID`。调试结果按预先冻结的选择规则处理。`holdout` 是数据分区标识；任何已经用于检查、调参或失败分析的样本都必须披露已观察状态，不能据此声称未见测试集。

完整检索流程在一个新建 Linux 容器中运行：

```sh
python3 scripts/evaluation-expanded-http.py \
  --data /private/evidence/data \
  --output /private/evidence/http-run \
  --budget-file /private/shared-budget/budget.json \
  --server-binary /private/bin/WeKnora
```

该脚本通过标准输入接收同一种凭据对象。SQLite 运行数据库位于容器内 `/tmp/weknora-expanded.sqlite`，应用停止后生成输出目录中的一致性备份。运行时通过 `progress.json` 查看任务进度；主机工具只读取已导出的数据库备份。供应商传输失败记录为费用未知，预算预留持续保留，JSON 与 CSV 对账只聚合已知金额。

页面缓存实验使用 `evaluation-openrouter-probes.py --batch-id <独立批次标识> --skip-embedding`，每批包含 30 对请求。各批次依次运行并复用上述预算文件。汇总图表依赖 `numpy` 与 `matplotlib`，报告生成命令为：

```sh
python3 scripts/build-excellence-report.py \
  --evidence /private/evidence/2026-09-11-excellence \
  --baseline /private/evidence/2026-09-09-final-acceptance \
  --output docs/reports/expanded-acceptance
```

报告生成器核对样本摘要、来源构建、成功调用费用及两个导出格式的一致性，并输出静态图表、逐题结果与文件摘要。输出报告记录实际样本数、置信区间、源码标识与质量限制；生成命令本身不构成已执行真实模型实验的证据。
