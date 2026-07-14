---
name: sandbox-test-python
version: 1.0.0
author: WeKnora Team
description: 用于快速验证云沙箱基础能力的诊断脚本：CPU 压测、磁盘 I/O、公网出口、一次性 HTTP 服务闭环验证，并在输出目录生成可下载的诊断产物（Markdown / HTML / JSON）。
tags: [sandbox, diagnostic, test, cpu, io, network, http-server, artifacts]
---

## WeKnora 云沙箱诊断 Skill

### 📌 概述

本 Skill 是一个用于 **Agent 云沙箱（Cloud Sandbox）能力自检** 的轻量诊断脚本。
Agent 将其上传到云沙箱内部并执行，可一次性验证沙箱的 **CPU 算力、磁盘 I/O、
公网出口、一次性 HTTP 服务闭环** 四项基础能力，并以实时日志、最终摘要和进程退出码
输出诊断结果，方便 Agent / CI 直接判断沙箱环境是否健康。

此外脚本会把结果落盘到 `WEKNORA_SKILL_OUTPUT_DIR` 指向的目录，WeKnora 平台会
自动扫描该目录并把生成的文件作为 **可下载产物** 呈现在会话气泡的下载抽屉中。

- **脚本名称**：`sandbox_test.py`
- **运行环境**：Python 3.6+（仅使用标准库，无需安装第三方依赖）
- **适用平台**：Linux 容器 / 云沙箱环境
- **退出码语义**：全部测试通过返回 `0`；任意测试失败返回 `1`

---

### 🎯 功能清单

| 编号 | 测试项 | 参数值 | 说明 |
|------|--------|--------|------|
| 1 | CPU 压测 | `cpu` | 随机选择递归深度计算 Fibonacci，验证基础 CPU 执行能力并输出耗时 |
| 2 | 磁盘 I/O | `io` | 在 `/tmp/weknora_io_test.data` 写入 50 × 1MB 随机数据，读回后清理 |
| 3 | 网络出口 | `net` | 顺序探测 5 个公网端点（`api.ipify.org` / `www.baidu.com` / `ifconfig.me` / `icanhazip.com` / `checkip.amazonaws.com`），至少 1 个连通即视为通过 |
| 4 | 后台服务 | `server` | 随机端口启动一次性 HTTP 服务，由脚本内部发起本地请求完成闭环验证 |
| 5 | 产物生成 | 无独立开关 | 每项测试成功后写单项产物；main 结尾统一生成 `summary.json` / `report.md` / `report.html` 三份汇总 |

---

### 🚀 使用方法

#### 1. 全量诊断（默认）

```bash
python3 sandbox_test.py
```

默认依次执行全部 4 项测试。脚本不会长期驻守：后台服务项只处理 1 次本地自测请求，
完成后立即退出并打印测试摘要。

#### 2. 只运行单项测试

```bash
python3 sandbox_test.py --test cpu       # 只测 CPU
python3 sandbox_test.py --test io        # 只测磁盘 I/O
python3 sandbox_test.py --test net       # 只测网络出口
python3 sandbox_test.py --test server    # 只测一次性 HTTP 服务闭环
```

---

### ⚙️ 命令行参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--test` | 枚举 | 无（全量） | 指定单项：`cpu` / `io` / `net` / `server` |
| `--cpu-depth` | 整数 | `28` | 当前脚本已声明该参数，但实际计算使用脚本顶部随机生成的 `CPU_RECURSION_DEPTH` |

---

### 📤 输出说明

#### 1. 实时日志

每一步均带时间戳输出，`[OK]` 表示通过，`[FAIL]` 表示失败：

```text
[10:30:01] [SKILL-TEST] === 🔍 WeKnora 云沙箱环境诊断 Skill 开始 ===
[10:30:01] [SKILL-TEST] 产物输出目录: /workspace/output
[10:30:01] [SKILL-TEST] 开始测试 1/4: CPU 密集型计算 ...
[10:30:02] [SKILL-TEST] [OK] CPU压测: Fib(28)=317811, 耗时 0.05s
[10:30:02] [SKILL-TEST] 开始测试 2/4: 磁盘 I/O 读写与持久化...
[10:30:02] [SKILL-TEST] [OK] 磁盘I/O: 写入 50.00MB 耗时 0.12s，读回 50.00MB 成功，已清理
[10:30:02] [SKILL-TEST] 开始测试 3/4: 公网出口连通性...
[10:30:03] [SKILL-TEST] [OK] 网络探测成功: https://api.ipify.org -> 203.0.113.42
[10:30:03] [SKILL-TEST] ▶️ 开始测试 4/4: 启动一次性 HTTP 服务 (端口 32123)...
[10:30:04] [SKILL-TEST] [OK] 后台服务: 服务已启动，监听 0.0.0.0:32123，本机IP 10.0.0.12，本地自测请求成功: 🎉 沙箱后台服务运行正常，本次请求完成后服务将退出！，服务已退出
```

#### 2. 测试摘要

脚本结束前打印各项汇总：

```text
============================================================
[10:30:04] [SKILL-TEST] 📋 测试摘要
  [OK] PASS  CPU压测: Fib(28)=317811, 耗时 0.05s
  [OK] PASS  磁盘I/O: 写入 50.00MB 耗时 0.12s，读回 50.00MB 成功，已清理
  [OK] PASS  网络出口: 连通 1/4；成功: https://api.ipify.org -> 203.0.113.42
  [OK] PASS  后台服务: 服务已启动，监听 0.0.0.0:32123，本机IP 10.0.0.12，本地自测请求成功: 🎉 沙箱后台服务运行正常，本次请求完成后服务将退出！，服务已退出
============================================================
[10:30:04] [SKILL-TEST] 📎 已生成产物: cpu_result.txt, io_metrics.json, network_probe.json, report.html, report.md, summary.json
[10:30:04] [SKILL-TEST] 全部测试通过，退出码=0
```

#### 3. 生成的产物文件

脚本会把结果写入 `WEKNORA_SKILL_OUTPUT_DIR`（未设置时回退到 `/workspace/output`，
再退化到 `/tmp/weknora-skill-output`）。产物分两类：

| 文件 | 类型 | 说明 |
|------|------|------|
| `cpu_result.txt`     | 单项 | CPU 递归深度、Fib 结果、耗时（仅 CPU 通过时生成） |
| `io_metrics.json`    | 单项 | 磁盘写入/读回大小与耗时（**不保存 50MB 随机数据本体**，避免超单文件上限） |
| `network_probe.json` | 单项 | 全部端点、成功列表、失败列表（无论通过与否都会生成，方便排障） |
| `summary.json`       | 汇总 | 机器可读的完整测试摘要（含 `results` / `failed`） |
| `report.md`          | 汇总 | Markdown 表格版报告 |
| `report.html`        | 汇总 | 自包含单文件 HTML，无外部 CSS/JS，离线可直接打开 |

> 磁盘 I/O 测试的原始 50MB 数据不会作为产物落盘，仅在 `/tmp` 中读写后立即清理。

#### 4. 退出码

脚本会通过 `sys.exit` 将测试结果传递给调用方：

| 场景 | 退出码 | 说明 |
|------|--------|------|
| 全部测试通过 | `0` | Agent / CI 可视为沙箱基础能力正常 |
| 任意测试失败 | `1` | 摘要中会列出失败测试项，调用方无需只依赖日志解析 |

---

### 🔧 关键配置常量

以下常量位于脚本顶部，可按需在源码中调整：

| 常量 | 当前行为 | 说明 |
|------|----------|------|
| `SERVER_PORT` | `random.randint(8080, 58080)` | 后台 HTTP 服务随机监听端口，降低端口冲突概率 |
| `CPU_RECURSION_DEPTH` | `random.randint(20, 35)` | Fibonacci 递归深度随机生成，避免每次压测完全固定 |
| `OUTPUT_DIR` | `_resolve_output_dir()` | 产物输出目录，优先取 `WEKNORA_SKILL_OUTPUT_DIR`，回退 `/workspace/output`，再回退 `/tmp/weknora-skill-output` |
| 磁盘写入大小 | `50 × 1MB` | `test_io_storage` 内循环写入 50MB 随机数据 |
| 网络探测超时 | `3` 秒 / 端点 | 单个公网端点请求超时时间 |
| HTTP 服务超时 | `8` 秒 | 一次性服务等待本地自测请求的最长时间 |

---

### 🤖 Agent 集成建议

1. **上传**：将 `sandbox_test.py` 写入沙箱工作目录，例如 `/workspace/sandbox_test.py`。
2. **执行**：推荐直接运行 `python3 sandbox_test.py`；脚本会自行完成 4 项测试并退出。
3. **判断**：优先使用进程退出码判断整体结果：`0` 表示通过，`1` 表示存在失败项。
4. **辅助解析**：如需展示细节，可从 stdout 中匹配 `[OK] PASS` / `[FAIL] FAIL` 行提取单项结果。
5. **单项排查**：当某项失败时，可使用 `--test cpu|io|net|server` 单独复现。

---

### ⚠️ 注意事项

- 磁盘测试文件位于 `/tmp/weknora_io_test.data`，正常路径下测试结束会自动清理；异常中断可能残留。
- 后台服务使用随机端口，并由脚本内部请求 `127.0.0.1:<端口>` 完成闭环验证，不用于长期驻守或人工访问。
- 网络出口测试会探测 `api.ipify.org`、`www.baidu.com`、`ifconfig.me/ip`、`icanhazip.com`、`checkip.amazonaws.com`，至少一个成功即通过。
- 若沙箱策略禁止公网外访，网络出口失败属预期结果，但脚本仍会以退出码 `1` 告知调用方存在失败项。
- 当前 `--cpu-depth` 参数只在命令行中声明，脚本实际使用顶部随机生成的 `CPU_RECURSION_DEPTH`。
- 产物目录由 `WEKNORA_SKILL_OUTPUT_DIR` 决定，WeKnora 后端调用时会自动注入；本地手动运行可显式 `export WEKNORA_SKILL_OUTPUT_DIR=/some/dir` 指定输出位置。
- 产物写入失败（如 sandbox 只读文件系统）不会中断测试，只在日志里以 `⚠️` 提示，脚本仍按原有逻辑返回退出码。
- 全部测试仅使用 **Python 标准库**（`sys` / `os` / `json` / `time` / `argparse` / `socket` /
  `threading` / `urllib` / `random` / `http.server` / `socketserver`），无需联网安装依赖。

---

### 📎 脚本入口摘要

```text
sandbox_test.py
├── _resolve_output_dir()     # 解析产物输出目录（模块加载时执行一次）
├── _write_artifact()         # 通用产物写入 helper，失败仅告警不中断
├── OUTPUT_DIR                # 已解析的输出目录（模块级常量）
├── test_cpu_isolation()      # 测试 1: CPU 递归计算 → cpu_result.txt
├── test_io_storage()         # 测试 2: /tmp 磁盘读写与清理 → io_metrics.json
├── test_network_egress()     # 测试 3: 多端点公网出口探测 → network_probe.json
├── start_background_server() # 测试 4: 一次性 HTTP 服务 + 本地请求闭环
└── main()                    # 参数解析、测试编排、摘要打印、汇总产物 (summary.json / report.md / report.html)、退出码返回
```