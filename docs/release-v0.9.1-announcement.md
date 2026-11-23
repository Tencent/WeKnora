# WeKnora v0.9.1 发布通知

以下文案供版本标签、镜像和官网图文均上线后使用。

## 中文用户群

📢 WeKnora v0.9.1 发布了！

1. 在画廊里可以直接改图片说明和 OCR 文字。PDF 图片支持按原文页码排序，全局搜索结果也能直接打开命中的原文段落。
2. 沙箱桌面和智能体现在使用同一会话的浏览器。遇到登录页，你可以自己登录，再交给智能体继续。也可以通过安全表单填写账号密码，按需保存到个人凭据保险库。
3. 重新解析文档时，原来的内容一直能搜到，新内容准备好后再替换。Wiki 可以关闭自动生成，让团队自己维护页面。
4. 新增 Nextcloud 数据源，可以选择文件或目录同步。Confluence 可以同步页面附件，导入表格时会保留行列结构。
5. 给智能体配好语音识别模型，就能按住说话，松开后检查文字再发送。嵌入聊天也可以在当前浏览器继续历史对话。

从 v0.9.0 升级的朋友，请先备份数据库和部署配置，再按升级说明更新镜像。[图文与升级说明](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1) · [GitHub Release](https://github.com/Tencent/WeKnora/releases/tag/v0.9.1)

使用 Compose 完整部署的朋友，换到 v0.9.1 对应的部署文件，在 `.env` 中设置 `WEKNORA_VERSION=v0.9.1`，再执行下面两行。使用其他部署组合时沿用原来的 profile。

```bash
docker compose --profile full pull --policy always
docker compose --profile full up -d
```

遇到问题欢迎在群内交流，或到 [GitHub Issues](https://github.com/Tencent/WeKnora/issues) 反馈。如果你的团队已经在用 WeKnora，也欢迎到社区留一句用在了什么场景。[谁在使用 WeKnora？](https://github.com/Tencent/WeKnora/issues/4801)

## GitHub Release 标题

WeKnora v0.9.1 · Image search, sandbox logins and Nextcloud sync

## GitHub Release 正文

### English

Use an image to search a knowledge base with the retrieval API, correct OCR text or captions directly in the gallery, and open global search hits at the matching passage. Previously indexed documents stay searchable while a reparse is in progress.

- **Sandbox logins.** Sign in on the session desktop and hand the same browser back to the agent. Use a secure form for credentials and optionally keep them in your personal, domain-scoped vault.
- **Knowledge maintenance.** Keep Wiki pages under manual control and choose a separate model for question generation.
- **Data sources.** Sync selected Nextcloud folders and files over WebDAV. Confluence imports page attachments and preserves tables.
- **Chat.** Hold to dictate with the agent’s speech recognition model. Embedded chat remembers conversations in the current browser, and signed visitors can delete their own sessions.

Back up your database, storage and configuration first. PostgreSQL advances from migration 134 to 137 and SQLite from 51 to 53. Keep the same 32-byte `SYSTEM_AES_KEY` when using the personal vault. [Upgrade guide](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1#upgrade-notes) · [English changelog](https://github.com/Tencent/WeKnora/blob/v0.9.1/CHANGELOG.md)

### 中文

在画廊直接改正图片说明和 OCR 文字，在全局搜索里跳到命中段落，或通过检索 API 用图片查找相似内容。文档重新解析时，原有索引会继续提供检索，直到新内容就绪。

- **沙箱登录。** 在会话桌面完成登录，智能体可以接着操作同一个浏览器。安全表单支持按需将登录信息保存到个人凭据保险库，并限定域名。
- **知识库维护。** Wiki 可以手动维护，生成检索问题也可以单独选择模型。
- **数据源。** 新增 Nextcloud 的目录和文件同步，Confluence 支持同步附件并保留页面表格。
- **对话。** 配置语音识别模型后即可按住说话。嵌入聊天能在当前浏览器找回历史，签名访客可删除自己的会话。

从 v0.9.0 升级前请备份数据库、存储与配置。使用凭据库时保留同一份加密密钥。[完整图文与升级步骤](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1) · [本版提交对比](https://github.com/Tencent/WeKnora/compare/v0.9.0...v0.9.1)

Using WeKnora in your team? Share a short example. 欢迎分享你们使用 WeKnora 的场景。[Community showcase · 用户征集](https://github.com/Tencent/WeKnora/issues/4801)

## 社区 Issue 跟帖短版

WeKnora v0.9.1 is available. It adds sandbox sign-in handoff, image queries in the retrieval API, and Nextcloud sync. If you use WeKnora at work or in a community project, we’d love to hear what you use it for. [Release notes](https://github.com/Tencent/WeKnora/releases/tag/v0.9.1)

WeKnora v0.9.1 增加了沙箱浏览器登录接力、以图检索接口和 Nextcloud 同步。如果你们已经在用，也欢迎在本帖分享一个具体场景。[中文图文](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1)
