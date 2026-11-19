# v0.9.0 发布通知

计划发布日期：2026-10-29。以下正文已按本轮发布内容整理，供正式 tag、镜像和官网均可访问后使用；通知尚未发送。

## 中文群通知

WeKnora v0.9.0 来了！这一版新增助手、多智能体协作、自动化任务和执行权限控制：

1. **WeKnora 助手**：解答 WeKnora 的版本、功能和配置问题，结合当前页面，以你的身份创建智能体、设置定时任务、整理知识和调整配置。
2. **多智能体协作**：把搜集、整理、核对分给多个智能体同时处理，分别查看进度和结果。
3. **自动化**：设置一次性或周期计划，定时生成摘要、整理资料；从运行记录查看结果和待审批操作。
4. **执行权限与审批**：选择只读、修改前确认或自动执行，确认前查看具体操作；删除等高影响操作仍须批准。
5. **沙箱与知识处理**：新增按用户复用沙箱的可选方式、后台 Shell，并完善图像处理、飞书 / Lark 同步、表格解析和检索。空间管理员还可集中设置默认模型与联网搜索服务。

升级前请备份数据库与文件，重点核对数据库迁移、飞书重同步和沙箱复用条件。全量变化和升级要求：

- 版本说明：https://weknora.weixin.qq.com/docs/07-releases/v0.9.0
- 官网：https://weknora.weixin.qq.com/
- GitHub：https://github.com/Tencent/WeKnora

正式镜像可用后，将 `.env` 的 `WEKNORA_VERSION` 设为 `0.9.0`，更新同版本部署文件，再执行：

```bash
docker compose --profile full pull --policy always
docker compose --profile full up -d
```

欢迎通过 GitHub Issues 反馈问题，也欢迎在开发者群交流使用体验。

## GitHub Release

Title: WeKnora v0.9.0 — Assistant, multi-agent collaboration and automations

WeKnora v0.9.0 adds a workspace assistant. Open it beside the logo to ask about WeKnora or work from the page you are on: create agents, schedule recurring tasks, organize knowledge and adjust settings through conversation. Multiple agents can work on separate tasks at the same time, and automations run tasks on a schedule. Choose read-only, confirm-before-write or automatic execution, and review operations that require approval. This release also adds workspace model defaults, optional per-user sandbox reuse, managed background shell jobs, configurable image pipelines and broader datasource improvements.

https://github.com/user-attachments/assets/de0e490a-e738-405e-bb3a-64b63edd7278

- [Release notes and upgrade guide](https://weknora.weixin.qq.com/docs/07-releases/v0.9.0)
- [Full changelog](https://github.com/Tencent/WeKnora/blob/v0.9.0/CHANGELOG.md)
- [Changes since v0.8.3](https://github.com/Tencent/WeKnora/compare/v0.8.3...v0.9.0)

Back up your database, files and sandbox deliverables before upgrading. Review PostgreSQL migrations 000119–000134 or SQLite 000037–000051, Feishu/Lark resync, permission ceilings and the explicit gate for shared sandbox allocation.

## 发布顺序

1. 合入本轮发布收尾 PR，确认 v0.9.0 的最终 commit；发布日期统一为 2026-10-29。如 main 在审查基线之后继续变化，追加差异核对。
2. 完成项目版本发布流程，确认 v0.9.0 tag、各平台产物与镜像可用。
3. 在测试部署完成迁移和核心功能验收，再按站点 README 发布静态包。
4. 检查官网版本页、截图、视频与 README 锚点，发布 Release 正文和群通知。

本文只准备发布材料，不代表上述操作已经执行。
