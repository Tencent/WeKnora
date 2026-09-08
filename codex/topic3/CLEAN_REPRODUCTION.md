# 空数据库最低费用复现说明

本步骤只在代码推送到用户 Fork、固定 Commit SHA 后执行。统一入口为：

```powershell
powershell -ExecutionPolicy Bypass -File .\codex\topic3\reproduce-clean.ps1 -Action Prepare
```

## 隔离原则

- 固定候选 Commit、新 Compose 项目名、新数据库卷、新缓存卷。
- 原环境只执行不删除卷的停止操作；禁止对原环境执行 `down -v`。
- 复现环境的容器统一使用 `WeKnora-topic3-repro-*` 名称；卷由 `weknora-topic3-repro` Compose 项目隔离。
- 新环境只跑1题评测和1次 Wiki 前缀校准，不重复10题、9轮缓存或8次 Wiki 实验。
- Key 只由用户在界面和隐藏终端提示中输入。

## 用户只需完成的操作

1. 在新环境首次登录。
2. 配置对话、Embedding、ReRank 三个模型。
3. 创建名称严格为 `topic3-reproduction` 的空知识库，并创建仅允许该知识库和评测权限的本地 API Key。
4. 在下面命令的隐藏提示中粘贴本地 Key 一次：

```powershell
powershell -ExecutionPolicy Bypass -File .\codex\topic3\reproduce-clean.ps1 -Action Run
```

脚本会直接从隔离数据库按固定模型名称和知识库名称读取 ID，不需要用户复制长 ID。成功或失败后都会停止隔离容器并恢复原 WeKnora；隔离卷保留以便审计，不会删除原数据库。

## 自动验收项目

- 五个容器状态和迁移83、`dirty=false`。
- 全部确定性专项测试。
- 1题数据导入、索引等待和1/1真实评测。
- 质量、用量、耗时和数据库持久化。
- app重启后评测历史仍可查询。
- 一次固定 Wiki 优化布局探针，记录厂商返回的总 `prompt_tokens`、公共前缀指纹、输出质量和缓存状态。
- Docker版本、Commit SHA、配置摘要、结果及日志哈希。
- 结束后恢复原 WeKnora，确认正式批次仍完整。

## 完成证据

复现报告必须明确记录：新环境不复用旧数据库或缓存、1题评测成功、重启后数据存在、固定Commit SHA、所有输出无 Key。厂商 usage 只能证明整条提示词的真实 Token 数，不能单独返回稳定前缀的精确 Token 数；报告必须保留这项边界，不得把总 Token 冒充成前缀 Token。正式实验不因此重跑。
