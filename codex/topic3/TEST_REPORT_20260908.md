# 课题三提交前无付费测试报告

测试日期：2026-09-08（北京时间）

## 结论

课题三直接相关的 Python、Go、前端、证据完整性、数据集和 Docker Compose 检查均通过。本轮没有调用任何远程模型，没有产生百炼费用。

## 已通过项目

| 检查 | 结果 |
|---|---|
| Python 语法编译 | 通过 |
| Python 安全与认证测试 | 6/6 通过 |
| Go `internal/agent` | 通过 |
| Go `internal/application/repository` | 通过 |
| Go `internal/application/service` | 通过 |
| Go `internal/router` | 通过 |
| 前端 `npm run type-check` | 通过 |
| 前端 `npm run build` | 通过；只有上游已有的大分块警告 |
| 正式证据及基线哈希 | `EVIDENCE_OK` |
| 实验核心源码哈希 | 与 `CORE_SOURCE_SNAPSHOT_20260908.json` 一致 |
| Docker Compose 配置 | 可正常解析 |
| 当前服务 | 5个容器运行；app、postgres、docreader 健康 |
| `topic3-10` 数据集 | source 10项；五份 Parquet 均10行 |
| `git diff --check` | 通过 |
| 正式结果密钥扫描 | 未发现疑似 API Key |

## 全量 Go 测试中的基线异常

`internal/handler` 的
`TestPutTenantParserConfigAdminPreservesRedactedSecrets` 返回：期望 HTTP 200，实际 HTTP 400。

为排除课题三改动的影响，已使用 `git checkout-index` 从当前基线 HEAD 生成完全不包含工作区改动的临时快照，并在相同 `golang:1.26` 容器内单独运行该测试。纯净快照得到完全相同的失败结果。

因此，该项被归类为基线代码已有异常。课题三没有修改相关 handler 文件，不为通过测试而改动无关的租户密钥安全逻辑。最终材料不能写“全量 Go 测试全部通过”，应写“课题三专项测试通过；基线 handler 存在一项已复现的既有异常”。

## 密钥扫描说明

全仓扫描会命中官方文档和既有测试中的示例字符串，例如 `sk-your-dashscope-api-key`。这些不是本次用户密钥。新增课题文件中只有安全单元测试使用的明显假值 `sk-example-secret-value-1234567890`；正式结果和证据清单未发现密钥。

## 尚未执行

- GitHub Fork 推送、Actions 运行及 required status check：等待用户提供 Fork 地址和 GitHub ID。
- 空数据库1题真实复现及一次 Wiki 固定前缀校准：必须在固定 Commit 的全新检出中进行，且需要用户在新环境输入密钥。
- 最终 Tag、`submission.yaml` 和邮件草稿：必须在上述验收完成后生成。
