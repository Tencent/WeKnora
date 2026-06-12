import { createI18n } from 'vue-i18n'

const messages = {
  "zh-CN": {
    "embedPublish": {
      "title": "网页嵌入",
      "description": "将智能体嵌入到您的网页，访客可通过页面内聊天窗口或右下角浮窗发起对话。",
      "create": "新建嵌入渠道",
      "empty": "暂无嵌入渠道",
      "unnamed": "未命名渠道",
      "agent": "智能体",
      "rateLimit": "限流",
      "rateLimitUnit": "次/分钟",
      "allowedOrigins": "域名白名单",
      "embedCode": "嵌入代码",
      "widgetCode": "浮窗脚本",
      "copyCode": "复制代码",
      "rotateToken": "轮换 Token",
      "delete": "删除",
      "edit": "编辑",
      "createTitle": "新建嵌入渠道",
      "editTitle": "编辑嵌入渠道",
      "name": "名称",
      "namePlaceholder": "例如：官网客服",
      "welcomeMessage": "欢迎语",
      "welcomePlaceholder": "你好，有什么可以帮您？",
      "originsLabel": "域名白名单（每行一个，留空表示不限制）",
      "originsPlaceholder": "https://shop.example.com",
      "rateLimitLabel": "每分钟请求上限",
      "debug": "调试预览",
      "createdDebugHint": "嵌入渠道已创建，可点击「调试预览」在新标签页体验",
      "primaryColor": "主题色",
      "pageTitle": "页面标题",
      "pageTitlePlaceholder": "AI 客服",
      "tokenHint": "Token 仅在创建或轮换时显示，请点击「轮换 Token」获取嵌入代码",
      "created": "嵌入渠道已创建",
      "updated": "嵌入渠道已更新",
      "deleted": "已删除",
      "tokenRotated": "Token 已轮换",
      "copied": "已复制嵌入代码",
      "loadError": "加载失败",
      "missingChannel": "缺少嵌入渠道或 Token",
      "invalidChannel": "无效的嵌入渠道",
      "sessionFailed": "无法创建对话会话，请稍后重试",
      "channelDisabled": "嵌入渠道已停用，请在 Agent 编辑器的「网页嵌入」中重新启用",
      "loading": "加载中...",
      "tabIframe": "iframe",
      "tabWidget": "浮窗",
      "widgetPosition": "浮窗位置",
      "widgetPreview": "浮窗预览",
      "positionBottomRight": "右下角",
      "positionBottomLeft": "左下角",
      "positionTopRight": "右上角",
      "positionTopLeft": "左上角",
      "publishToken": "发布 Token",
      "publishTokenHelp": "发布 Token（em_…）是嵌入渠道的长期密钥，相当于 API Key。创建或轮换时只显示一次，需写入第三方网站的浮窗脚本；列表接口不会返回，防止泄露。",
      "sessionTokenHelp": "访客打开聊天后，iframe 会用发布 Token 换取短期会话 Token（ems_…，约 30 分钟），后续请求使用会话 Token，避免在 URL 中长期暴露发布 Token。",
      "rotateTokenHelp": "轮换会作废旧发布 Token，所有已部署的嵌入代码需同步更新，否则第三方站点将无法访问。",
      "revealToken": "显示",
      "hideToken": "隐藏",
      "copyToken": "复制 Token",
      "tokenCopied": "Token 已复制",
      "awaitingToken": "等待宿主页面提供 Token…",
      "preview": "预览",
      "previewIframeHint": "模拟第三方网站用 iframe 嵌入的效果（与复制代码一致）。",
      "previewWidgetHint": "模拟第三方网站加载浮窗脚本后的效果；真实站点由宿主页面通过 postMessage 传递 Token。",
      "previewMockPage": "模拟宿主页面",
      "defaultChatTitle": "AI 助手",
      "rotateConfirmTitle": "确认轮换 Token？",
      "rotateConfirmBody": "轮换后旧 Token 立即失效，已发布的嵌入代码需全部更新。",
      "tokenRequiredForPreview": "需要发布 Token 才能预览。请先创建渠道，或点击「轮换 Token」获取新 Token。"
    },
    "chat": {
      "title": "对话",
      "newChat": "新对话",
      "suggestedQuestions": "你可以这样问我",
      "suggestedQuestionsLoading": "正在加载推荐问题...",
      "inputPlaceholder": "请输入您的消息...",
      "send": "发送",
      "thinking": "思考中...",
      "regenerate": "重新生成",
      "copy": "复制",
      "delete": "删除",
      "reference": "引用",
      "noMessages": "暂无消息",
      "waitingForAnswer": "等待回答...",
      "cannotAnswer": "抱歉，我无法回答这个问题。",
      "summarizingAnswer": "总结答案中...",
      "loading": "加载中...",
      "enterDescription": "输入描述",
      "referencedContent": "引用了 {count} 个相关资料",
      "deepThinking": "深度思考完成",
      "knowledgeBaseQandA": "知识库问答",
      "askKnowledgeBase": "向知识库提问",
      "sourcesCount": "{count} 个来源",
      "pleaseEnterContent": "请输入内容！",
      "pleaseUploadKnowledgeBase": "请先上传知识库！",
      "replyingPleaseWait": "正在回复，请稍后再试！",
      "createSessionFailed": "创建会话失败",
      "createSessionError": "创建会话出错",
      "unableToGetKnowledgeBaseId": "无法获取知识库ID",
      "summaryInProgress": "正在总结答案……",
      "thinkingAlt": "正在思考",
      "deepThoughtCompleted": "已深度思考",
      "deepThoughtAlt": "深度思考完成",
      "referencesTitle": "参考了{count}个相关内容",
      "referencesDocCount": "引用了{count}篇文档",
      "referencesDocAndWebCount": "引用了{docCount}篇文档和{webCount}条网页",
      "referenceChunkCount": "{count}个片段",
      "fallbackHint": "未从知识库中检索到相关内容，以上为模型直接回答",
      "requestInfoTitle": "请求信息",
      "requestInfoRequestId": "Request ID",
      "requestInfoMessageId": "消息 ID",
      "requestInfoSessionId": "会话 ID",
      "requestInfoUrl": "请求",
      "requestInfoSentAt": "发起时间",
      "requestInfoEmpty": "暂无请求信息",
      "channelWeb": "网页",
      "channelApi": "API",
      "channelIm": "IM",
      "chunkLabel": "片段{index}:",
      "navigateToDocument": "查看文档详情",
      "referenceIconAlt": "参考内容图标",
      "chunkIdLabel": "片段ID:",
      "documentIdLabel": "文档ID:",
      "faqIdLabel": "FAQ ID:",
      "faqContainerIdLabel": "所属文档ID:",
      "faqAnswersLabel": "答案:",
      "chunkOrdinal": "片段 {index}",
      "previewContent": "预览内容",
      "noPlanSteps": "未提供具体步骤",
      "chunkIndexLabel": "片段 #{index}",
      "chunkPositionLabel": "(位置: {position})",
      "noRelatedChunks": "没有找到相关片段",
      "noSearchResults": "没有找到搜索结果",
      "relevanceHigh": "高相关",
      "relevanceMedium": "中相关",
      "relevanceLow": "低相关",
      "relevanceWeak": "弱相关",
      "webSearchNoResults": "未找到搜索结果",
      "otherSource": "其他来源",
      "webGroupIntro": "以下 {count} 条内容来自",
      "graphConfigTitle": "图谱配置",
      "entityTypesLabel": "实体类型:",
      "relationTypesLabel": "关系类型:",
      "graphResultsHeader": "找到 {count} 条相关结果",
      "graphNoResults": "未找到相关的图谱信息",
      "unknownLink": "未知链接",
      "contentLengthLabel": "长度 {value}",
      "notProvided": "未提供",
      "promptLabel": "提示词",
      "errorMessageLabel": "错误信息",
      "summaryLabel": "总结",
      "rawTextLabel": "原始文本",
      "collapseRaw": "收起原文",
      "expandRaw": "展开原文",
      "noWebContent": "未获取到网页内容",
      "lengthChars": "{value} 字",
      "lengthThousands": "{value} 千字",
      "lengthTenThousands": "{value} 万字",
      "sqlQueryExecuted": "执行的 SQL 查询:",
      "sqlResultsLabel": "返回结果:",
      "rowsLabel": "行",
      "columnsLabel": "列",
      "noDatabaseRecords": "未找到匹配的记录",
      "nullValuePlaceholder": "<NULL>",
      "documentTitleLabel": "文档标题:",
      "chunkCountLabel": "片段数量:",
      "chunkCountValue": "{count} 个片段",
      "documentDescriptionLabel": "文档描述:",
      "documentStatusLabel": "处理状态:",
      "documentSourceLabel": "来源:",
      "documentFileLabel": "文件信息:",
      "documentMetadataLabel": "元数据",
      "documentInfoSummaryLabel": "文档信息",
      "documentInfoCount": "成功 {count} / 请求 {requested}",
      "documentInfoErrors": "错误详情",
      "documentInfoEmpty": "暂无文档信息",
      "statusDescription": "状态说明",
      "statusIndexed": "文档已索引并可搜索",
      "statusSearchable": "可使用搜索工具查找文档内容",
      "statusChunkDetailAvailable": "可使用 get_chunk_detail 查看片段详情",
      "positionLabel": "位置:",
      "chunkPositionValue": "第 {index} 个片段",
      "contentLengthLabelSimple": "内容长度:",
      "fullContentLabel": "完整内容",
      "copyContent": "复制内容",
      "knowledgeBaseCount": "共 {count} 个知识库",
      "noKnowledgeBases": "没有可用的知识库",
      "rawOutputLabel": "原始输出",
      "wikiWritePageTitle": "Wiki 页面写入",
      "wikiReplaceTextTitle": "Wiki 文本替换",
      "wikiRenamePageTitle": "Wiki 页面重命名",
      "wikiDeletePageTitle": "Wiki 页面删除",
      "wikiActionCreated": "已创建",
      "wikiActionUpdated": "已更新",
      "wikiActionRenamed": "已重命名",
      "wikiActionDeleted": "已删除",
      "wikiFieldSlug": "页面路径",
      "wikiFieldTitle": "标题",
      "wikiFieldPageType": "类型",
      "wikiFieldSummary": "摘要",
      "wikiFieldOldText": "原文本",
      "wikiFieldNewText": "新文本",
      "wikiFieldOldSlug": "原路径",
      "wikiFieldNewSlug": "新路径",
      "wikiFieldAffectedPages": "受影响页面",
      "wikiAffectedCount": "{count} 个页面的链接已更新",
      "selectKnowledgeBaseWarning": "请至少选择一个知识库",
      "processError": "处理出错",
      "sessionExcerpt": "会话摘录",
      "noAnswerContent": "（无回答内容）",
      "noMatchFound": "未找到匹配的内容",
      "deleteSessionFailed": "删除失败，请稍后再试！",
      "imageTooMany": "最多上传5张图片",
      "imageTypeSizeError": "仅支持 JPG/PNG/GIF/WEBP 格式，单张不超过 10MB",
      "imageReadFailed": "读取图片失败",
      "imageUploadTooltip": "上传图片（支持粘贴/拖拽）",
      "attachmentUploadTooltip": "上传附件（文档、音频等）",
      "attachmentWithCount": "已上传 {count} 个附件",
      "attachmentTooMany": "最多上传 {max} 个附件",
      "attachmentTooLarge": "文件 {name} 超过 {max}MB 限制",
      "attachmentTypeNotSupported": "不支持的文件类型：{name}",
      "copySuccess": "已复制到剪贴板",
      "copyFailed": "复制失败",
      "emptyContentWarning": "当前回答为空",
      "editorOpened": "已打开编辑器，请选择知识库后保存"
    },
    "common": {
      "loading": "加载中...",
      "confirm": "确认",
      "cancel": "取消",
      "copy": "复制",
      "copied": "已复制"
    },
    "error": {
      "tokenNotFound": "未找到登录令牌，请重新登录",
      "invalidImageLink": "无效的图片链接",
      "streamFailed": "流式连接失败"
    },
    "agent": {
      "taskLabel": "任务:",
      "think": "思考",
      "copy": "复制",
      "addToKnowledgeBase": "添加到知识库",
      "updatePlan": "更新计划",
      "webSearchFound": "找到 <strong>{count}</strong> 个网络搜索结果",
      "argumentsLabel": "参数",
      "toolFallback": "工具",
      "stepsCompleted": "已完成 <strong>{steps}</strong> 个步骤",
      "stepsCompletedWithDuration": "已完成 <strong>{steps}</strong> 个步骤，耗时 <strong>{duration}</strong>",
      "reasoningRounds": "思考 <strong>{rounds}</strong> 轮",
      "toolCalls": "调用 <strong>{tools}</strong> 次工具",
      "durationSuffix": "耗时 <strong>{duration}</strong>",
      "stepSummarySeparator": " · "
    },
    "agentStream": {
      "toolApproval": {
        "banner": "该 MCP 工具已标记为「需人工审核」，确认参数后再执行",
        "service": "服务",
        "tool": "工具",
        "argsLabel": "调用参数",
        "argsModified": "已修改",
        "countdown": "剩余约 {seconds} 秒",
        "approve": "通过并执行",
        "reject": "拒绝",
        "approvedTag": "已通过",
        "rejectedTag": "已拒绝",
        "invalidJson": "参数不是合法 JSON",
        "submitted": "已提交",
        "submitFailed": "提交失败",
        "userRejected": "用户拒绝"
      },
      "tools": {
        "searchKnowledge": "知识库检索",
        "grepChunks": "搜索关键词",
        "webSearch": "网络搜索",
        "webFetch": "网页抓取",
        "getDocumentInfo": "获取文档信息",
        "listKnowledgeChunks": "查看知识分块",
        "getRelatedDocuments": "查找相关文档",
        "getDocumentContent": "获取文档内容",
        "todoWrite": "计划管理",
        "knowledgeGraphExtract": "知识图谱抽取",
        "thinking": "思考",
        "imageAnalysis": "查看图片内容",
        "queryKnowledgeGraph": "知识图谱查询",
        "readSkill": "读取技能",
        "executeSkillScript": "执行技能脚本",
        "dataAnalysis": "数据分析",
        "dataSchema": "数据结构",
        "databaseQuery": "数据库查询"
      },
      "summary": {
        "searchKb": "检索知识库 <strong>{count}</strong> 次",
        "thinking": "思考 <strong>{count}</strong> 次",
        "callTool": "调用 {name}",
        "callTools": "调用工具 {names}",
        "intermediateSteps": "<strong>{count}</strong> 个中间步骤",
        "separator": "、",
        "comma": "，"
      },
      "citation": {
        "loading": "加载中...",
        "notFound": "未找到内容",
        "loadFailed": "加载失败",
        "chunkId": "片段ID",
        "noKbForWiki": "无法识别关联的知识库，无法打开 Wiki"
      },
      "toolSummary": {
        "getDocument": "获取文档：{title}",
        "document": "文档",
        "listChunks": "查看 {title}",
        "listFaqEntry": "查看 FAQ：{question}",
        "deepThinking": "深度思考"
      },
      "plan": {
        "inProgress": "进行中",
        "pending": "待处理",
        "completed": "已完成"
      },
      "search": {
        "noResults": "未找到匹配的内容",
        "foundResultsFromFiles": "找到 {count} 个结果，来自 {files} 个文件",
        "foundResults": "找到 {count} 个结果",
        "webResults": "找到 {count} 个网络搜索结果",
        "grepSummary": "找到 {chunks} 个匹配片段，来自 {docs} 个文档"
      },
      "grepResults": {
        "chunkHits": "{count} 片段",
        "keywordHits": "{count} 次",
        "titleMatch": "标题匹配",
        "faqEntry": "FAQ 条目"
      },
      "toolStatus": {
        "calling": "正在调用 {name}...",
        "searchKb": "检索知识库",
        "searchKbFailed": "检索知识库失败",
        "webSearch": "网络搜索",
        "webSearchFailed": "网络搜索失败",
        "grepSearch": "搜索关键词",
        "grepSearchFailed": "搜索关键词失败",
        "getDocInfo": "获取文档信息",
        "getDocInfoFailed": "获取文档信息失败",
        "thinkingDone": "完成思考",
        "thinkingFailed": "思考失败",
        "updateTodos": "更新任务列表",
        "updateTodosFailed": "更新任务列表失败",
        "imageAnalyzing": "正在查看图片内容...",
        "imageAnalysisDone": "已查看图片内容",
        "imageAnalysisFailed": "图片内容查看失败",
        "called": "调用 {name}",
        "calledFailed": "调用 {name} 失败"
      },
      "copy": {
        "emptyContent": "当前回答为空，无法复制",
        "success": "已复制到剪贴板",
        "failed": "复制失败，请手动复制"
      },
      "saveToKb": {
        "emptyContent": "当前回答为空，无法保存到知识库",
        "editorOpened": "已打开编辑器，请选择知识库后保存"
      }
    },
    "input": {
      "placeholder": "直接向模型提问",
      "stopGeneration": "停止生成",
      "send": "发送",
      "webSearch": {
        "label": "网络搜索",
        "toggleOn": "开启网络搜索",
        "toggleOff": "关闭网络搜索",
        "agentDisabled": "当前智能体未启用联网搜索"
      },
      "imageUpload": {
        "label": "上传图片",
        "tooltip": "上传图片",
        "agentDisabled": "当前智能体未启用图片上传"
      },
      "fileUpload": {
        "label": "上传附件",
        "tooltip": "上传文档等附件",
        "tooMany": "最多上传 5 个附件",
        "tooLarge": "附件超过 20MB 限制"
      },
      "messages": {
        "enterContent": "请先输入内容!",
        "selectKnowledge": "请先选择知识库!",
        "replying": "正在回复中，请稍后再试!",
        "agentSwitchedOn": "已切换到智能推理",
        "agentSwitchedOff": "已切换到快速问答",
        "agentSelected": "已选择智能体「{name}」",
        "agentEnabled": "Agent 模式已启用",
        "agentDisabled": "Agent 模式已禁用",
        "agentNotReadyDetail": "Agent 未就绪，需要配置以下内容：{reasons}",
        "webSearchNotConfigured": "未配置网络搜索引擎，请先在设置中完成搜索引擎选择与接口配置。",
        "webSearchEnabled": "网络搜索已开启",
        "webSearchDisabled": "网络搜索已关闭",
        "sessionMissing": "会话 ID 不存在",
        "messageMissing": "无法获取消息 ID，请刷新页面后重试",
        "stopSuccess": "已停止生成",
        "stopFailed": "停止失败，请重试"
      }
    },
    "knowledgeEditor": {
      "wikiBrowser": {
        "viewInGraph": "在图谱中查看",
        "version": "v{ver}",
        "filterSummary": "摘要",
        "filterEntity": "实体",
        "filterConcept": "概念",
        "filterSynthesis": "综合",
        "filterComparison": "对比"
      }
    }
  },
  "en-US": {
    "embedPublish": {
      "title": "Web Page Embed",
      "description": "Embed this agent on your website so visitors can chat via an in-page window or a floating launcher.",
      "create": "New embed channel",
      "empty": "No embed channels yet",
      "unnamed": "Unnamed channel",
      "agent": "Agent",
      "rateLimit": "Rate limit",
      "rateLimitUnit": "/min",
      "allowedOrigins": "Allowed origins",
      "embedCode": "Embed code",
      "widgetCode": "Widget script",
      "copyCode": "Copy code",
      "rotateToken": "Rotate token",
      "delete": "Delete",
      "edit": "Edit",
      "createTitle": "New embed channel",
      "editTitle": "Edit embed channel",
      "name": "Name",
      "namePlaceholder": "e.g. Website support",
      "welcomeMessage": "Welcome message",
      "welcomePlaceholder": "Hi! How can I help you?",
      "originsLabel": "Allowed origins (one per line, empty = allow all)",
      "originsPlaceholder": "https://shop.example.com",
      "rateLimitLabel": "Requests per minute",
      "debug": "Debug preview",
      "createdDebugHint": "Embed channel created — use Debug preview to open it in a new tab",
      "primaryColor": "Primary color",
      "pageTitle": "Page title",
      "pageTitlePlaceholder": "AI Assistant",
      "tokenHint": "Token is shown only on create or rotate — click Rotate token to get embed code",
      "created": "Embed channel created",
      "updated": "Embed channel updated",
      "deleted": "Deleted",
      "tokenRotated": "Token rotated",
      "copied": "Embed code copied",
      "loadError": "Failed to load",
      "missingChannel": "Missing embed channel or token",
      "invalidChannel": "Invalid embed channel",
      "sessionFailed": "Failed to create chat session, please try again",
      "channelDisabled": "This embed channel is disabled. Re-enable it under Agent editor → Web Page Embed",
      "loading": "Loading...",
      "tabIframe": "iframe",
      "tabWidget": "Widget",
      "widgetPosition": "Widget position",
      "widgetPreview": "Widget preview",
      "positionBottomRight": "Bottom right",
      "positionBottomLeft": "Bottom left",
      "positionTopRight": "Top right",
      "positionTopLeft": "Top left",
      "publishToken": "Publish token",
      "publishTokenHelp": "The publish token (em_…) is a long-lived secret for this embed channel—like an API key. It is shown only when created or rotated and must be placed in the host site widget script; list APIs never return it.",
      "sessionTokenHelp": "After a visitor opens chat, the iframe exchanges the publish token for a short-lived session token (ems_…, ~30 min). Later API calls use the session token so the publish token is not kept in the URL.",
      "rotateTokenHelp": "Rotating invalidates the previous publish token. Every deployed embed snippet must be updated or third-party sites will lose access.",
      "revealToken": "Reveal",
      "hideToken": "Hide",
      "copyToken": "Copy token",
      "tokenCopied": "Token copied",
      "awaitingToken": "Waiting for host page to provide token…",
      "preview": "Preview",
      "previewIframeHint": "Shows how the iframe embed looks on a third-party page (same as the copied snippet).",
      "previewWidgetHint": "Shows the floating widget on a mock host page. On a real site the host passes the token via postMessage.",
      "previewMockPage": "Mock host page",
      "defaultChatTitle": "AI Assistant",
      "rotateConfirmTitle": "Rotate publish token?",
      "rotateConfirmBody": "The old token stops working immediately. Update every deployed embed snippet.",
      "tokenRequiredForPreview": "A publish token is required to preview. Create a channel or rotate the token first."
    },
    "chat": {
      "title": "Chat",
      "newChat": "New Chat",
      "suggestedQuestions": "You can ask me",
      "suggestedQuestionsLoading": "Loading suggestions...",
      "inputPlaceholder": "Enter your message...",
      "send": "Send",
      "thinking": "Thinking...",
      "regenerate": "Regenerate",
      "copy": "Copy",
      "delete": "Delete",
      "reference": "Reference",
      "noMessages": "No messages",
      "waitingForAnswer": "Waiting for answer...",
      "cannotAnswer": "Sorry, I cannot answer this question.",
      "summarizingAnswer": "Summarizing answer...",
      "loading": "Loading...",
      "referencedContent": "{count} related materials used",
      "deepThinking": "Deep thinking completed",
      "knowledgeBaseQandA": "Knowledge Base Q&A",
      "askKnowledgeBase": "Ask the knowledge base",
      "sourcesCount": "{count} sources",
      "pleaseEnterContent": "Please enter content!",
      "pleaseUploadKnowledgeBase": "Please upload knowledge base first!",
      "replyingPleaseWait": "Replying, please try again later!",
      "createSessionFailed": "Failed to create session",
      "createSessionError": "Session creation error",
      "unableToGetKnowledgeBaseId": "Unable to get knowledge base ID",
      "summaryInProgress": "Summarizing answer…",
      "thinkingAlt": "Thinking in progress",
      "deepThoughtCompleted": "Deep thinking completed",
      "deepThoughtAlt": "Deep thinking finished",
      "referencesTitle": "Referenced {count} related item(s)",
      "referencesDocCount": "Referenced {count} document(s)",
      "referencesDocAndWebCount": "Referenced {docCount} document(s) and {webCount} web page(s)",
      "referenceChunkCount": "{count} chunk(s)",
      "fallbackHint": "No relevant content found in knowledge base. Above is a direct response from the model.",
      "requestInfoTitle": "Request info",
      "requestInfoRequestId": "Request ID",
      "requestInfoMessageId": "Message ID",
      "requestInfoSessionId": "Session ID",
      "requestInfoUrl": "Request",
      "requestInfoSentAt": "Sent at",
      "requestInfoEmpty": "No request info available",
      "channelWeb": "Web",
      "channelApi": "API",
      "channelIm": "IM",
      "chunkLabel": "Chunk {index}:",
      "navigateToDocument": "View document details",
      "referenceIconAlt": "Reference materials icon",
      "chunkIdLabel": "Chunk ID:",
      "documentIdLabel": "Document ID:",
      "faqIdLabel": "FAQ ID:",
      "faqContainerIdLabel": "Container ID:",
      "faqAnswersLabel": "Answers:",
      "chunkOrdinal": "Chunk {index}",
      "previewContent": "Preview content",
      "noPlanSteps": "No detailed steps provided",
      "chunkIndexLabel": "Chunk #{index}",
      "chunkPositionLabel": "(Position: {position})",
      "noRelatedChunks": "No related chunks found",
      "noSearchResults": "No search results found",
      "relevanceHigh": "High relevance",
      "relevanceMedium": "Medium relevance",
      "relevanceLow": "Low relevance",
      "relevanceWeak": "Weak relevance",
      "webSearchNoResults": "No web search results found",
      "otherSource": "Other sources",
      "webGroupIntro": "The following {count} items are from",
      "graphConfigTitle": "Graph Configuration",
      "entityTypesLabel": "Entity types:",
      "relationTypesLabel": "Relation types:",
      "graphResultsHeader": "{count} related results found",
      "graphNoResults": "No related graph information found",
      "unknownLink": "Unknown link",
      "contentLengthLabel": "Length {value}",
      "notProvided": "Not provided",
      "promptLabel": "Prompt",
      "errorMessageLabel": "Error message",
      "summaryLabel": "Summary",
      "rawTextLabel": "Raw text",
      "collapseRaw": "Collapse original",
      "expandRaw": "Expand original",
      "noWebContent": "No web content fetched",
      "lengthChars": "{value} characters",
      "lengthThousands": "{value}k characters",
      "lengthTenThousands": "{value} ten-thousand characters",
      "sqlQueryExecuted": "Executed SQL query:",
      "sqlResultsLabel": "Results:",
      "rowsLabel": "rows",
      "columnsLabel": "columns",
      "noDatabaseRecords": "No matching records found",
      "nullValuePlaceholder": "<NULL>",
      "documentTitleLabel": "Document title:",
      "chunkCountLabel": "Chunk count:",
      "chunkCountValue": "{count} chunks",
      "documentDescriptionLabel": "Description:",
      "documentStatusLabel": "Status:",
      "documentSourceLabel": "Source:",
      "documentFileLabel": "File:",
      "documentMetadataLabel": "Metadata",
      "documentInfoSummaryLabel": "Document info",
      "documentInfoCount": "{count} of {requested} documents retrieved",
      "documentInfoErrors": "Errors",
      "documentInfoEmpty": "No document information available",
      "statusDescription": "Status notes",
      "statusIndexed": "Document is indexed and searchable",
      "statusSearchable": "Search tools can locate document content",
      "statusChunkDetailAvailable": "Use get_chunk_detail to view chunk details",
      "positionLabel": "Position:",
      "chunkPositionValue": "Chunk #{index}",
      "contentLengthLabelSimple": "Content length:",
      "fullContentLabel": "Full content",
      "copyContent": "Copy content",
      "knowledgeBaseCount": "{count} knowledge bases",
      "noKnowledgeBases": "No knowledge bases available",
      "enterDescription": "Enter description",
      "rawOutputLabel": "Raw output",
      "wikiWritePageTitle": "Wiki Page Write",
      "wikiReplaceTextTitle": "Wiki Text Replace",
      "wikiRenamePageTitle": "Wiki Page Rename",
      "wikiDeletePageTitle": "Wiki Page Delete",
      "wikiActionCreated": "Created",
      "wikiActionUpdated": "Updated",
      "wikiActionRenamed": "Renamed",
      "wikiActionDeleted": "Deleted",
      "wikiFieldSlug": "Slug",
      "wikiFieldTitle": "Title",
      "wikiFieldPageType": "Type",
      "wikiFieldSummary": "Summary",
      "wikiFieldOldText": "Old text",
      "wikiFieldNewText": "New text",
      "wikiFieldOldSlug": "Old slug",
      "wikiFieldNewSlug": "New slug",
      "wikiFieldAffectedPages": "Affected pages",
      "wikiAffectedCount": "{count} page link(s) updated",
      "selectKnowledgeBaseWarning": "Please select at least one knowledge base",
      "processError": "Processing error",
      "sessionExcerpt": "Session Excerpt",
      "noAnswerContent": "(No answer content)",
      "noMatchFound": "No matching content found",
      "deleteSessionFailed": "Delete failed, please try again later!",
      "imageTooMany": "Maximum 5 images allowed",
      "imageTypeSizeError": "Only JPG/PNG/GIF/WEBP under 10MB supported",
      "imageReadFailed": "Failed to read image",
      "imageUploadTooltip": "Upload image (paste/drop supported)",
      "attachmentUploadTooltip": "Upload attachment (documents, audio, etc.)",
      "attachmentWithCount": "{count} attachment(s) uploaded",
      "attachmentTooMany": "Maximum {max} attachments allowed",
      "attachmentTooLarge": "File {name} exceeds {max}MB limit",
      "attachmentTypeNotSupported": "Unsupported file type: {name}",
      "copySuccess": "Copied to clipboard",
      "copyFailed": "Copy failed",
      "emptyContentWarning": "Content is empty",
      "editorOpened": "Editor opened, please select a knowledge base and save"
    },
    "common": {
      "loading": "Loading...",
      "confirm": "Confirm",
      "cancel": "Cancel",
      "copy": "Copy",
      "copied": "Copied"
    },
    "error": {
      "tokenNotFound": "Login token not found, please log in again",
      "invalidImageLink": "Invalid image link",
      "streamFailed": "Stream connection failed"
    },
    "agent": {
      "taskLabel": "Task:",
      "think": "Thinking",
      "copy": "Copy",
      "addToKnowledgeBase": "Add to Knowledge Base",
      "updatePlan": "Update Plan",
      "webSearchFound": "Found <strong>{count}</strong> web search result(s)",
      "argumentsLabel": "Arguments",
      "toolFallback": "Tool",
      "stepsCompleted": "Completed <strong>{steps}</strong> step(s)",
      "stepsCompletedWithDuration": "Completed <strong>{steps}</strong> step(s) in <strong>{duration}</strong>",
      "reasoningRounds": "<strong>{rounds}</strong> reasoning round(s)",
      "toolCalls": "<strong>{tools}</strong> tool call(s)",
      "durationSuffix": "<strong>{duration}</strong>",
      "stepSummarySeparator": " · "
    },
    "agentStream": {
      "toolApproval": {
        "banner": "This MCP tool requires human approval. Review parameters before execution.",
        "service": "Service",
        "tool": "Tool",
        "argsLabel": "Arguments",
        "argsModified": "Modified",
        "countdown": "About {seconds}s remaining",
        "approve": "Approve & run",
        "reject": "Reject",
        "approvedTag": "Approved",
        "rejectedTag": "Rejected",
        "invalidJson": "Arguments must be valid JSON",
        "submitted": "Submitted",
        "submitFailed": "Submit failed",
        "userRejected": "User rejected"
      },
      "tools": {
        "searchKnowledge": "Knowledge Search",
        "grepChunks": "Text Pattern Search",
        "webSearch": "Web Search",
        "webFetch": "Web Fetch",
        "getDocumentInfo": "Get Document Info",
        "listKnowledgeChunks": "List Knowledge Chunks",
        "getRelatedDocuments": "Find Related Documents",
        "getDocumentContent": "Get Document Content",
        "todoWrite": "Plan Management",
        "knowledgeGraphExtract": "Knowledge Graph Extraction",
        "thinking": "Thinking",
        "imageAnalysis": "Image Analysis",
        "queryKnowledgeGraph": "Knowledge Graph Query",
        "readSkill": "Read Skill",
        "executeSkillScript": "Execute Skill Script",
        "dataAnalysis": "Data Analysis",
        "dataSchema": "Data Schema",
        "databaseQuery": "Database Query"
      },
      "summary": {
        "searchKb": "Searched knowledge base <strong>{count}</strong> time(s)",
        "thinking": "Thought <strong>{count}</strong> time(s)",
        "callTool": "Called {name}",
        "callTools": "Called tools {names}",
        "intermediateSteps": "<strong>{count}</strong> intermediate step(s)",
        "separator": ", ",
        "comma": ", "
      },
      "citation": {
        "loading": "Loading...",
        "notFound": "Content not found",
        "loadFailed": "Failed to load",
        "chunkId": "Chunk ID",
        "noKbForWiki": "Unable to identify associated knowledge base. Cannot open Wiki."
      },
      "toolSummary": {
        "getDocument": "Get document: {title}",
        "document": "Document",
        "listChunks": "View {title}",
        "listFaqEntry": "View FAQ: {question}",
        "deepThinking": "Deep Thinking"
      },
      "plan": {
        "inProgress": "In Progress",
        "pending": "Pending",
        "completed": "Completed"
      },
      "search": {
        "noResults": "No matching content found",
        "foundResultsFromFiles": "Found {count} result(s) from {files} file(s)",
        "foundResults": "Found {count} result(s)",
        "webResults": "Found {count} web search result(s)",
        "grepSummary": "Found {chunks} matching chunk(s) across {docs} document(s)"
      },
      "grepResults": {
        "chunkHits": "{count} chunks",
        "keywordHits": "{count} hits",
        "titleMatch": "title",
        "faqEntry": "FAQ entry"
      },
      "toolStatus": {
        "calling": "Calling {name}...",
        "searchKb": "Searching knowledge base",
        "searchKbFailed": "Knowledge base search failed",
        "webSearch": "Web search",
        "webSearchFailed": "Web search failed",
        "grepSearch": "Keyword search",
        "grepSearchFailed": "Keyword search failed",
        "getDocInfo": "Getting document info",
        "getDocInfoFailed": "Failed to get document info",
        "thinkingDone": "Thinking complete",
        "thinkingFailed": "Thinking failed",
        "updateTodos": "Updating task list",
        "updateTodosFailed": "Failed to update task list",
        "imageAnalyzing": "Viewing image content...",
        "imageAnalysisDone": "Image content viewed",
        "imageAnalysisFailed": "Image viewing failed",
        "called": "Called {name}",
        "calledFailed": "Failed to call {name}"
      },
      "copy": {
        "emptyContent": "Current response is empty, cannot copy",
        "success": "Copied to clipboard",
        "failed": "Copy failed, please copy manually"
      },
      "saveToKb": {
        "emptyContent": "Current response is empty, cannot save to knowledge base",
        "editorOpened": "Editor opened, please select a knowledge base and save"
      }
    },
    "input": {
      "placeholder": "Ask questions directly to the model",
      "stopGeneration": "Stop Generation",
      "send": "Send",
      "webSearch": {
        "label": "Web search",
        "toggleOn": "Enable web search",
        "toggleOff": "Disable web search",
        "agentDisabled": "Web search is not enabled for this agent"
      },
      "imageUpload": {
        "label": "Upload image",
        "tooltip": "Upload image",
        "agentDisabled": "Image upload is not enabled for this agent"
      },
      "fileUpload": {
        "label": "Upload file",
        "tooltip": "Upload document attachments",
        "tooMany": "Maximum 5 attachments",
        "tooLarge": "Attachment exceeds 20MB limit"
      },
      "messages": {
        "enterContent": "Please enter content first!",
        "selectKnowledge": "Please select a knowledge base first!",
        "replying": "Currently replying, please try again later!",
        "agentSwitchedOn": "Switched to Intelligent Reasoning",
        "agentSwitchedOff": "Switched to Quick Q&A",
        "agentSelected": "Selected agent \"{name}\"",
        "agentEnabled": "Agent Mode enabled",
        "agentDisabled": "Agent Mode disabled",
        "agentNotReadyDetail": "Agent is not ready. Please configure the following: {reasons}",
        "webSearchNotConfigured": "Web search engine is not configured. Please configure a provider and credentials in settings.",
        "webSearchEnabled": "Web search enabled",
        "webSearchDisabled": "Web search disabled",
        "sessionMissing": "Session ID does not exist",
        "messageMissing": "Unable to get message ID. Please refresh the page and try again.",
        "stopSuccess": "Generation stopped",
        "stopFailed": "Failed to stop. Please try again."
      }
    },
    "knowledgeEditor": {
      "wikiBrowser": {
        "viewInGraph": "View in Graph",
        "version": "v{ver}",
        "filterSummary": "Summaries",
        "filterEntity": "Entities",
        "filterConcept": "Concepts",
        "filterSynthesis": "Synthesis",
        "filterComparison": "Comparisons"
      }
    }
  }
} as const

type MessageTree = Record<string, unknown>

function deepMerge<T extends MessageTree>(base: T, patch: MessageTree): T {
  const out: MessageTree = { ...base }
  for (const key of Object.keys(patch)) {
    const patchVal = patch[key]
    const baseVal = base[key]
    if (
      patchVal &&
      typeof patchVal === 'object' &&
      !Array.isArray(patchVal) &&
      baseVal &&
      typeof baseVal === 'object' &&
      !Array.isArray(baseVal)
    ) {
      out[key] = deepMerge(baseVal as MessageTree, patchVal as MessageTree)
    } else {
      out[key] = patchVal
    }
  }
  return out as T
}

const koEmbedPublish = {
  embedPublish: {
    title: '웹 페이지 임베드',
    description: '에이전트를 웹 페이지에 임베드하여 방문자가 페이지 내 채팅창 또는 플로팅 버튼으로 대화할 수 있게 합니다.',
    create: '새 임베드 채널',
    empty: '임베드 채널 없음',
    unnamed: '이름 없는 채널',
    loading: '로딩 중...',
    awaitingToken: '호스트 페이지에서 토큰 제공 대기 중…',
    defaultChatTitle: 'AI 어시스턴트',
    newChat: '새 대화',
    preview: '미리보기',
    previewIframeHint: 'iframe 임베드가 외부 페이지에서 어떻게 보이는지 시뮬레이션합니다.',
    previewWidgetHint: '모의 호스트 페이지에서 플로팅 위젯을 표시합니다.',
    previewMockPage: '모의 호스트 페이지',
    previewLoading: '미리보기 로딩 중…',
    channelDisabled: '임베드 채널이 비활성화되었습니다. 에이전트 편집기 → 웹 페이지 임베드에서 다시 활성화하세요',
    invalidChannel: '잘못된 임베드 채널',
    sessionFailed: '대화 세션을 생성할 수 없습니다. 나중에 다시 시도하세요',
    missingChannel: '임베드 채널 또는 토큰 없음',
    loadError: '로드 실패',
  },
  common: {
    loading: '로딩 중...',
    confirm: '확인',
    cancel: '취소',
    copy: '복사',
    copied: '복사됨',
  },
  error: {
    tokenNotFound: '로그인 토큰을 찾을 수 없습니다. 다시 로그인하세요',
    invalidImageLink: '잘못된 이미지 링크',
    streamFailed: '스트림 연결 실패',
  },
  chat: {
    suggestedQuestions: '이렇게 물어보세요',
    imageTooMany: '이미지는 최대 5장까지 업로드할 수 있습니다',
    imageTypeSizeError: 'JPG/PNG/GIF/WEBP만 지원하며, 각 파일은 10MB 이하여야 합니다',
    imageReadFailed: '이미지를 읽지 못했습니다',
  },
  input: {
    placeholder: '모델에 직접 질문하세요',
    stopGeneration: '생성 중지',
    send: '보내기',
    webSearch: {
      label: '웹 검색',
      toggleOn: '웹 검색 켜기',
      toggleOff: '웹 검색 끄기',
      agentDisabled: '이 에이전트에서는 웹 검색이 활성화되지 않았습니다',
    },
    imageUpload: {
      label: '이미지 업로드',
      tooltip: '이미지 업로드',
      agentDisabled: '이 에이전트에서는 이미지 업로드가 활성화되지 않았습니다',
    },
    messages: {
      webSearchEnabled: '웹 검색이 켜졌습니다',
      webSearchDisabled: '웹 검색이 꺼졌습니다',
      stopSuccess: '생성이 중지되었습니다',
      stopFailed: '중지에 실패했습니다. 다시 시도하세요',
    },
  },
} as const

const ruEmbedPublish = {
  embedPublish: {
    title: 'Встраивание на веб-страницу',
    description: 'Встройте агента на свою веб-страницу: посетители смогут общаться через встроенное окно чата или плавающую кнопку.',
    create: 'Новый канал встраивания',
    empty: 'Каналов встраивания пока нет',
    unnamed: 'Без названия',
    loading: 'Загрузка...',
    awaitingToken: 'Ожидание токена от страницы-хоста…',
    defaultChatTitle: 'AI-ассистент',
    newChat: 'Новый чат',
    preview: 'Предпросмотр',
    previewIframeHint: 'Как iframe выглядит на сторонней странице.',
    previewWidgetHint: 'Плавающий виджет на mock-странице.',
    previewMockPage: 'Mock-страница хоста',
    previewLoading: 'Загрузка предпросмотра…',
    channelDisabled: 'Канал встраивания отключён. Включите в редакторе агента → Встраивание на веб-страницу',
    invalidChannel: 'Недействительный канал встраивания',
    sessionFailed: 'Не удалось создать сессию чата, попробуйте позже',
    missingChannel: 'Отсутствует канал встраивания или токен',
    loadError: 'Не удалось загрузить',
  },
  common: {
    loading: 'Загрузка...',
    confirm: 'Подтвердить',
    cancel: 'Отмена',
    copy: 'Копировать',
    copied: 'Скопировано',
  },
  error: {
    tokenNotFound: 'Токен входа не найден, войдите снова',
    invalidImageLink: 'Недействительная ссылка на изображение',
    streamFailed: 'Ошибка потокового соединения',
  },
  chat: {
    suggestedQuestions: 'Вы можете спросить так',
    imageTooMany: 'Можно загрузить не более 5 изображений',
    imageTypeSizeError: 'Поддерживаются только JPG/PNG/GIF/WEBP, каждый файл до 10 МБ',
    imageReadFailed: 'Не удалось прочитать изображение',
  },
  input: {
    placeholder: 'Задайте вопрос модели',
    stopGeneration: 'Остановить генерацию',
    send: 'Отправить',
    webSearch: {
      label: 'Веб-поиск',
      toggleOn: 'Включить веб-поиск',
      toggleOff: 'Выключить веб-поиск',
      agentDisabled: 'Веб-поиск не включён для этого агента',
    },
    imageUpload: {
      label: 'Загрузить изображение',
      tooltip: 'Загрузить изображение',
      agentDisabled: 'Загрузка изображений не включена для этого агента',
    },
    messages: {
      webSearchEnabled: 'Веб-поиск включён',
      webSearchDisabled: 'Веб-поиск выключен',
      stopSuccess: 'Генерация остановлена',
      stopFailed: 'Не удалось остановить. Попробуйте снова.',
    },
  },
} as const

const SUPPORTED_LOCALES = ['zh-CN', 'en-US', 'ko-KR', 'ru-RU'] as const
export type EmbedLocale = (typeof SUPPORTED_LOCALES)[number]

/** Isolated from the main app `locale` key so embed preview never hijacks admin UI language. */
export const EMBED_LOCALE_STORAGE_KEY = 'weknora-embed-locale'

/** Map host-provided locale strings to a supported embed locale tag. */
export function normalizeEmbedLocale(raw: string): EmbedLocale {
  const s = raw.trim().toLowerCase()
  if (s.startsWith('en')) return 'en-US'
  if (s.startsWith('ko')) return 'ko-KR'
  if (s.startsWith('ru')) return 'ru-RU'
  if (s.startsWith('zh')) return 'zh-CN'
  const exact = SUPPORTED_LOCALES.find((l) => l.toLowerCase() === s)
  return exact || 'zh-CN'
}

export function readEmbedLocaleFromUrl(): string {
  if (typeof window === 'undefined') return ''
  return new URLSearchParams(window.location.search).get('locale')?.trim() || ''
}

function resolveBrowserEmbedLocale(): EmbedLocale {
  const nav = typeof navigator !== 'undefined' ? navigator.language : ''
  return nav ? normalizeEmbedLocale(nav) : 'zh-CN'
}

function resolveInitialEmbedLocale(): EmbedLocale {
  const fromUrl = readEmbedLocaleFromUrl()
  if (fromUrl) return normalizeEmbedLocale(fromUrl)

  try {
    const saved = typeof localStorage !== 'undefined'
      ? localStorage.getItem(EMBED_LOCALE_STORAGE_KEY)
      : null
    if (saved) return normalizeEmbedLocale(saved)
  } catch {
    // localStorage may be unavailable in private mode.
  }

  return resolveBrowserEmbedLocale()
}

const locale = resolveInitialEmbedLocale()

const i18n = createI18n({
  legacy: false,
  locale,
  fallbackLocale: 'en-US',
  globalInjection: true,
  warnHtmlMessage: false,
  messages: {
    'zh-CN': messages['zh-CN'],
    'en-US': messages['en-US'],
    'ko-KR': deepMerge(messages['en-US'], koEmbedPublish),
    'ru-RU': deepMerge(messages['en-US'], ruEmbedPublish),
  },
})

type LocaleRef = { value: string }

/** Apply locale for the embed surface (isolated storage + optional active vue-i18n ref). */
export function applyEmbedLocale(raw: string, localeRef?: LocaleRef) {
  const next = normalizeEmbedLocale(raw)
  try {
    localStorage.setItem(EMBED_LOCALE_STORAGE_KEY, next)
  } catch {
    // localStorage may be unavailable in private mode.
  }
  if (localeRef) {
    localeRef.value = next
  } else {
    i18n.global.locale.value = next
  }
}

/** Honor `?locale=` on the embed URL for the currently mounted vue-i18n instance. */
export function syncEmbedLocaleFromUrl(localeRef: LocaleRef): boolean {
  const fromUrl = readEmbedLocaleFromUrl()
  if (!fromUrl) return false
  applyEmbedLocale(fromUrl, localeRef)
  return true
}

export default i18n
