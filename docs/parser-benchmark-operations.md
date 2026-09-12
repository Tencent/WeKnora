# 解析引擎测评运行说明

八个生产解析适配器共用固定的 100 页公开样本清单。命令行程序验证输入文件与清单摘要，并分别保存成功、错误、空输出和超时记录。样本来源、参考标注与评分定义见[公开基准子集说明](parser-benchmark-dataset.md)。工程代码和某次实验输出具有独立版本标识；已有输出的二进制、配置或输入身份不匹配时，程序拒绝恢复。

## 环境与构建

在干净源码目录使用 Go 1.26、C/C++ 编译器和 Git 构建。Linux 和 Windows Subsystem for Linux（WSL，适用于 Linux 的 Windows 子系统）可运行以下命令：

```bash
sh scripts/build-parser-benchmark.sh
export JIEBA_DICT_DIR="$PWD/artifacts/parser-benchmark/bin/jieba"
python3 scripts/run-parser-benchmark.py --native \
  --manifest dataset/parser-benchmark/manifest-full.json \
  --binary artifacts/parser-benchmark/bin/parser-benchmark \
  --output artifacts/parser-benchmark/runs/candidate-v1 --engine builtin
```

构建脚本使用实际完整提交标识，不改写源码，拒绝覆盖已有二进制或改变已冻结的分词字典。`PARSER_BENCHMARK_BINARY` 可以指定新的输出路径。预检要求清单中的公开输入已按数据准备说明下载；预检不读取凭据、不调用解析服务。

## 服务配置

本地适配器通过远程过程调用（Remote Procedure Call，RPC）或超文本传输协议（Hypertext Transfer Protocol，HTTP）访问专用解析服务。下表列出默认示例入口，具体端点由执行者显式配置。

| 服务 | 宿主模式入口 | Docker 网络入口 |
| --- | --- | --- |
| DocReader：Builtin、MarkItDown、OpenDataLoader | `127.0.0.1:50051` | `weknora-parser-docreader:50051` |
| MinerU | `http://127.0.0.1:18081` | `http://weknora-parser-mineru:8000` |
| PaddleOCR-VL | `http://127.0.0.1:18082` | `http://weknora-parser-paddle:8080` |

`--native` 使用宿主模式，`--docreader` 覆盖 DocReader 入口。Docker 模式使用 `--network` 或 `PARSER_BENCHMARK_NETWORK` 指定网络，默认为 `weknora-parser-benchmark`；`--runtime-image` 或 `PARSER_BENCHMARK_RUNTIME_IMAGE` 指定包含所需动态库的运行镜像。默认镜像固定为部署说明中的公开 DocReader 镜像摘要。

PowerShell 辅助脚本在项目相对目录保存状态。使用前显式建立专用网络，并按 [MinerU](../deploy/parser-benchmark/mineru/README.md) 和 [PaddleOCR-VL](../deploy/parser-benchmark/paddle/README.md) 说明准备模型和依赖。脚本中的 Install、Start、Stop、build 和 prepare 操作会改变所指定的测试服务；实际使用前应核对容器名称和正在运行的任务。

```powershell
docker network create weknora-parser-benchmark
pwsh -NoProfile -File scripts/parser-benchmark-mineru.ps1 -Action Status
pwsh -NoProfile -File scripts/parser-benchmark-paddle.ps1 -Action status
```

## 凭据与实际执行

启动器仅读取执行者显式提供的配置，不读取应用容器、业务数据库或当前租户。使用 `PARSER_BENCHMARK_CREDENTIALS_FILE` 指向仓库外的私有 JSON 文件，或在执行时传入 `--credentials-stdin`。配置通过标准输入传给 Go 程序；命令行与公开报告保留脱敏信息。

以下样例只包含公开本地端点，可以作为本地解析的配置文件。云端字段应按 `cmd/parser-benchmark/main.go` 中的配置结构在私有文件中填写。云调用需要核对实际凭据、供应商限制和同一累计预算。

```json
{
  "parser_config": {
    "mineru_endpoint": "http://127.0.0.1:18081",
    "paddleocr_vl_endpoint": "http://127.0.0.1:18082"
  }
}
```

```bash
# This command submits parsing requests to the explicitly configured service.
python3 scripts/run-parser-benchmark.py --native --execute \
  --manifest dataset/parser-benchmark/manifest-full.json \
  --output artifacts/parser-benchmark/runs/candidate-v1 --engine mineru
```

`--execute` 才会发出解析请求。八个可选名称为 `builtin`、`markitdown`、`opendataloader`、`weknoracloud`、`mineru`、`mineru_cloud`、`paddleocr_vl` 和 `paddleocr_vl_cloud`。`--shard-count` 和 `--shard-index` 保持分片清单冻结，每个分片仍受完整清单样本上限约束。

## 长批次恢复

`scripts/run-parser-benchmark-cpu-batch.py` 提供 PaddleOCR-VL 的逐页串行执行。它读取显式配置，核对模型、容器、输入、构建身份与后处理计划，并保存提交意图、进程身份、逐页收据及完成标志。该工具要求事先生成 `baseline-build-identity.json` 和对应源码证据；简单独立运行可直接使用上述启动器。

恢复仅复用摘要完整一致的成功、错误、空输出或超时记录。已经提交但缺少完整结果的页面保留为 `indeterminate`，不会自动重复提交。存在活动客户端或进程身份不明确时，批次停止后续提交。超时恢复可能重启专用 Paddle 容器，使用前须保证该容器只服务该批次。

`execution-complete.json` 表示每页执行证据齐备，`batch-complete.json` 还要求所有配置的后处理命令成功。两者均不表示所有页面解析成功。后处理命令使用 JSON 参数数组，恢复时要求相同计划摘要；命令的绝对路径由执行机器生成，不随源码预置。

## 评分与报告

```bash
python3 -B scripts/test_parser_benchmark.py ScoringIntegrityTests OfficialDenominatorAuditTests ReviewBindingTests ManifestMetadataTests
python3 -B scripts/test_parser_benchmark_launcher.py
python3 -B scripts/test_parser_benchmark_cpu_batch.py
python3 scripts/score-parser-benchmark.py --manifest dataset/parser-benchmark/manifest-full.json \
  --runs artifacts/parser-benchmark/runs/candidate-v1 --official-container weknora-parser-official-eval
python3 scripts/build-parser-benchmark-report.py --help
```

评分依赖、固定官方评测源码、Chromium 与独立 OmniDocBench 环境按[数据集说明](parser-benchmark-dataset.md)安装。普通工程测试使用合成输入，不执行云请求。正式评分分别报告适用参考页、实际评分页、服务错误页与空输出页；公式字符检测匹配（Character Detection Matching，CDM）只有实际执行并产生有效结果后才提供分数。

报告生成目录可通过本地 HTTP 服务访问，并将地址配置为 `VITE_PARSER_BENCHMARK_URL`。人工审核者、时间、决定和签名仅由实际审核者填写；缺失评分、签名和云端费用保持缺失。

完整公开样本检查使用单独入口。按数据准备说明安装 `requirements-prepare.txt` 并准备 100 页 PDF 及参考标注后，执行以下命令核对全部输入和参考摘要，并确认 OmniDocBench 输入不含参考答案文本层。缺少任何输入、参考标注或 PDF 读取依赖都会导致该检查失败。

```bash
python3 -B scripts/test_parser_benchmark.py DatasetIntegrityTests
```

`make evaluation-verify` 与持续集成执行合成评分、分母、审核绑定及冻结清单元数据检查；它们不需要下载选做 PDF 语料，也不把这些检查称为八引擎实际运行。
