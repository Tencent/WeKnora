# 课题三最终验收记录

## 验收结论

截至 2026 年 9 月 9 日，课题三的功能、正式实验、回归门禁和干净环境复现均已完成。正式实验不需要重复运行。

| 验收项 | 结果 |
|---|---|
| 评测持久化 | 已完成；任务、逐题结果、指标和配置写入 PostgreSQL |
| 模型用量统计 | 已完成；支持模型与时间筛选，未知字段不按零处理 |
| Embedding 缓存 | 9/9 真实实验完成；真实调用由 36 次降至 3 次，减少 91.7% |
| 检索质量 | 9轮 Recall/MRR 均为 1.0 |
| Wiki 对照 | 8/8 真实调用有效；本次未观察到厂商缓存命中 |
| 回归负例 | Recall/MRR 从 1.0 降为 0，0.03 门禁正确拒绝 |
| GitHub CI | PR #1 最新候选版本 3 项成功、2 项按设计跳过 |
| Required Check | 已验证正常通过、退化失败及失败时禁止合并 |
| 空数据库复现 | 独立 Compose、迁移 83、1/1 完成，重启后仍可查询 |
| 密钥检查 | 干净复现结果实际匹配 0 条 API Key |

## 版本与证据

- Fork：`https://github.com/szt1107/WeKnora`
- PR：`https://github.com/szt1107/WeKnora/pull/1`
- 候选分支：`codex/topic3-initial`
- 当前候选 Commit：`8ccaabda22437d38d978585f016fda86456e9c4c`
- 正式本地实验：`codex/topic3/results/batch-20260908-102705/`
- 干净复现：`codex/topic3/results/clean-reproduction-20260908-210144/`
- 正式基线：`codex/topic3/baseline.json`

最终代码以合并后的 `rhino-2026-final-3` Tag 为准。`submission.yaml` 在 Tag 固定后生成，并记录该 Tag 对应的完整 Commit SHA。

## 已知边界

- Wiki 隐式缓存满足调用条件，但8次调用均为 `miss`，因此不宣称取得缓存收益。
- Embedding 和 ReRank 没有可靠的精确 Token 计费字段，相关精确费用保持未知。
- 上游基线的 handler 测试存在已在纯净快照复现的异常；课题三直接相关专项测试均已通过。
