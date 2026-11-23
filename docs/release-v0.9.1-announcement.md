# WeKnora v0.9.1 发布通知

以下文案供版本标签、镜像和官网图文均上线后使用。

## 中文用户群

📢 WeKnora v0.9.1 发布了！这次有三处变化可以直接试。

1. **对着输入框说话。** 给智能体开启「语音上传」、配好语音识别模型，按住麦克风说话，松开就会得到文字。人名、数字可以改好再发。
2. **你登录，智能体接着办。** 沙箱桌面和智能体共用当前会话的浏览器。遇到登录页，你在桌面上登录，它接着用刚才的页面完成任务。需要提供账号密码时，也可以填写对话中的安全表单。
3. **查资料更容易找到出处。** 在全局搜索中点击命中的片段，直接跳到原文对应位置。跨库问答的引用会标出来源知识库。开发者还可以通过检索 API 传一张图，查找知识库里的相似图片。

从 v0.9.0 升级前，请先备份数据库、文件存储和部署配置。图文里也写清了批量重解析时旧内容会保留到哪一步，以及 PostgreSQL 如何为实际使用的向量维度建索引。[图文与升级说明](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1) · [GitHub Release](https://github.com/Tencent/WeKnora/releases/tag/v0.9.1)

使用 Compose 完整部署时，换到 v0.9.1 对应的部署文件，在 `.env` 中设置 `WEKNORA_VERSION=v0.9.1`，再执行下面两行。其他部署组合沿用原来的 profile。

```bash
docker compose --profile full pull --policy always
docker compose --profile full up -d
```

遇到问题欢迎在群内交流，或到 GitHub 反馈。也欢迎分享你们怎么用 WeKnora。[GitHub Issues](https://github.com/Tencent/WeKnora/issues) · [社区使用案例](https://github.com/Tencent/WeKnora/issues/4801)

## GitHub Release 标题

WeKnora v0.9.1 · Voice input, sandbox browser handoff and image search

## GitHub Release 正文

### English

- **Dictate a chat message.** Enable audio uploads and select a speech recognition model for the agent. Hold the microphone button to speak, then release it to insert the transcript into your draft. You can edit it before sending.
- **Sign in and let the agent continue.** On supported sandbox desktops, you and the agent share a browser within the session. Sign in on the desktop and the agent can continue on the same page. Secure forms can save credentials to a personal vault scoped to the domains you choose.
- **Search with an image or jump to a passage.** The retrieval API accepts an image, with optional text, for knowledge bases with image embeddings. In the web app, click a matching passage in global search to open it in the source document. Answer citations identify the source knowledge base.

During a batch reparse, existing content stays searchable while queued and while the document is being read. Old chunks are cleared when index replacement starts. On PostgreSQL, automatic migrations can build HNSW indexes on demand for embedding dimensions such as 1536 and 3072.

Back up your database, storage and configuration before upgrading. PostgreSQL advances from migration 134 to 137 and SQLite from 51 to 53. Keep the same 32-byte `SYSTEM_AES_KEY` when using the personal vault. [Upgrade guide](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1#upgrade-notes) · [English changelog](https://github.com/Tencent/WeKnora/blob/v0.9.1/CHANGELOG.md)

### 中文

- **聊天支持按住说话。** 给智能体开启「语音上传」、配置语音识别模型后，在输入框按住麦克风，松开就能编辑识别出的文字，再发送。
- **你和智能体可以接着用同一个沙箱浏览器。** 在当前会话的桌面上登录网站，智能体随后可以继续操作。通过安全表单填写的账号密码，也可以按域名保存到个人凭据保险库。
- **搜索可以直达原文。** 点击全局搜索里命中的文档片段，就能核对上下文。回答引用会标出来源知识库。启用了图片向量索引的知识库还支持通过检索 API 以图查图。

批量重解析排队和读取文档时仍可检索旧内容，开始替换索引时会清理旧分块。PostgreSQL 开启自动迁移后，会为实际使用的向量维度按需创建 HNSW 索引。

从 v0.9.0 升级前请备份数据库、文件存储与部署配置。使用凭据库时保留原有加密密钥。[完整图文与升级步骤](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1) · [本版提交对比](https://github.com/Tencent/WeKnora/compare/v0.9.0...v0.9.1)

Tell us how your team uses WeKnora. 欢迎分享你们的使用场景。[社区使用案例 · Community showcase](https://github.com/Tencent/WeKnora/issues/4801)

## 社区 Issue 跟帖短版

WeKnora v0.9.1 adds hold-to-talk dictation, lets you sign in on a sandbox desktop and hand the browser back to the agent, and opens global search hits at the matching passage. The retrieval API also accepts image queries. How does your team use WeKnora? [Release notes](https://github.com/Tencent/WeKnora/releases/tag/v0.9.1)

WeKnora v0.9.1 可以按住说话写消息，在沙箱登录后让智能体继续操作，也可以从搜索结果直接跳到原文。做集成的朋友还可以试试以图查图的检索接口。欢迎分享你们的使用场景。[中文图文](https://weknora.weixin.qq.com/docs/07-releases/v0.9.1)
