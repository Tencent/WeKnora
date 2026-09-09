# GitHub CI 与合并门禁验收记录

验收日期：2026-09-08  
Fork：<https://github.com/szt1107/WeKnora>  
PR：<https://github.com/szt1107/WeKnora/pull/1>  
目标分支：`topic3-v0.7.2-base`

## 已完成配置

- 目标分支已经启用分支保护。
- Required status check 为 `topic3-regression / deterministic-checks`。
- PR 自动运行确定性测试，不调用真实模型。
- 真实模型评测只能由人工勾选 `run_real_evaluation` 后触发。
- 原验收版本未启用定时运行；2026-09-09 补充了每周一次、仅运行确定性检查的 schedule，绝不自动触发付费评测。该配置合并并位于默认分支后生效。

## 负例阻断证据

负例演示提交：`c159b29c test(topic3): demonstrate regression gate failure`

- 把确定性回归输入切换为已冻结的退化 fixture。
- Required 检查失败。
- 专用 `negative-demo` 检查失败。
- GitHub 合并按钮被禁用，证明不合格结果不能合入受保护分支。
- 运行记录：<https://github.com/szt1107/WeKnora/actions/runs/34194095065>

该步骤只使用本地 fixture，没有访问百炼，也没有产生模型费用。

## 恢复后的正常证据

恢复提交：`e18741f2 test(topic3): restore normal regression workflow`

- 正常基线检查通过。
- 退化 fixture 仍被检查器正确拒绝，但整个确定性任务保持绿色。
- Required 检查重新通过，PR 恢复为可合并状态。
- 运行记录：<https://github.com/szt1107/WeKnora/actions/runs/34194366880>

## 验收结论

本项已经同时证明：正常结果可以通过、不合格结果会失败、失败的 Required 检查能够阻止合并。PR #1 已于 2026-09-09 合并；提交材料 PR #2 也已合并。
