# 课题三空数据库复现结果

复现于 2026 年 9 月 8 日完成，使用独立 Compose 项目 `weknora-topic3-repro`、独立数据卷和初始为空的 PostgreSQL 数据库。

## 验收结果

| 检查项 | 真实结果 |
|---|---|
| 候选 Commit | `a8e4137067d5356ffe65abdeb679b14669e12fca` |
| 数据库迁移 | `83`, `dirty=false` |
| 初始评测数 | 0 |
| 真实冒烟评测 | 1/1 完成，状态 2 |
| 持久化 | 重启隔离 app 后仍可读取 |
| 未完成任务 | 0 |
| Wiki 探针 | 输出格式有效，固定实体检查通过 |
| Wiki 总输入 Token | 1293 |
| 厂商缓存状态 | `miss` |
| 密钥扫描 | 4 个结果文件，实际匹配 0 条 |
| 环境恢复 | 原 WeKnora 五个容器已恢复运行 |

Wiki 的 1293 是厂商报告的整条提示词 Token 数。厂商没有单独返回稳定前缀的精确 Token 数，因此不把它写成稳定前缀 Token 数。

首次收尾扫描出现 `secret-like text found in reproduction output`。这是 Windows PowerShell 的布尔数组转换问题：`Select-String -Quiet` 对四个文件返回 `False False False False`，非空数组在 `if` 中被转换为真。结果文件中没有 API Key。脚本现已改为统计实际 `MatchInfo` 数量。

## 原始证据 SHA-256

| 文件 | SHA-256 |
|---|---|
| `clean-reproduction-report.json` | `35E468CAB6F435AA10943C91FED282869B5D3C2AFEB6BE3F739ABB08E13222CA` |
| `clean-reproduction-report.md` | `EA9B3883C34F90DADE51C20E2692AF19CF3E17D7AF5D14A060F9ADD6C8D28635` |
| `evaluation.json` | `9E3706124187EB81B557A91B4AB3BF918B66A74EC7781E1644FA78A618BF314F` |
| `wiki-prefix-probe.json` | `DEEED5EC2522788B4B6C976F543C879CEE60511077F354B36294F8A894037E4E` |

原始结果位于 `codex/topic3/results/clean-reproduction-20260908-210144/`。
