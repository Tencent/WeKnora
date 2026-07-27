import fs from "node:fs/promises";
import { SpreadsheetFile, Workbook } from "@oai/artifact-tool";

const outputDir = "/Users/xiaoxie/Desktop/Project/WeKnora/outputs/weknora_function_list_20260723";
const outputPath = `${outputDir}/WeKnora_功能清单_v0.7.0.xlsx`;

const items = [];
const add = (端, 一级, 二级, 功能点, 功能说明, 重点 = false) =>
  items.push({ 端, 一级, 二级, 功能点, 功能说明, 重点 });

// PC 端：账号、对话、知识库、智能体与共享空间。
add("PC端", "账号与工作区", "登录与认证", "账号密码登录", "支持账号密码登录并建立用户会话，进入 WeKnora 工作台。");
add("PC端", "账号与工作区", "登录与认证", "OIDC 单点登录", "支持通过 OIDC 对接企业身份源，并处理登录回调与账号创建。", true);
add("PC端", "账号与工作区", "邀请与注册", "邀请链接注册", "通过带邀请码的注册链接完成用户注册并加入指定工作区。");
add("PC端", "账号与工作区", "工作区切换", "多工作区切换", "在已加入的工作区之间切换，隔离知识库、智能体、会话和配置数据。");
add("PC端", "账号与工作区", "工作区创建", "自助创建工作区", "按平台策略允许无工作区用户进入引导流程并自助创建工作区。", true);
add("PC端", "账号与工作区", "个人设置", "个人资料与偏好", "查看和维护用户信息、语言、主题、记忆开关等个人偏好。");

add("PC端", "智能对话", "新建会话", "快速问答", "选择知识库后使用 RAG 快速问答，适合日常知识查询。");
add("PC端", "智能对话", "新建会话", "智能推理", "启用 ReAct Agent，多步编排知识检索、网络搜索、内置工具和 MCP 工具。");
add("PC端", "智能对话", "资源选择", "知识库选择", "在会话中选择单个或多个可访问知识库作为检索范围。");
add("PC端", "智能对话", "资源选择", "智能体选择", "选择内置或自定义智能体，并显示模型、联网、多模态等就绪状态。");
add("PC端", "智能对话", "资源选择", "@Skill / @MCP 提及", "在单轮消息中提及 Skill 或 MCP 服务，临时限定本轮 Agent 可用能力范围。", true);
add("PC端", "智能对话", "消息输入", "文本与多轮上下文", "发送自然语言问题并保留多轮上下文，支持中止正在运行的任务。");
add("PC端", "智能对话", "消息输入", "临时附件", "上传图片或文档用于当前会话的一次性问答，异步解析并在后续轮次保留上下文。", true);
add("PC端", "智能对话", "消息输入", "图片上传", "在启用多模态的智能体中上传图片并参与视觉理解。");
add("PC端", "智能对话", "消息输入", "结构化信息采集", "Agent 按配置向用户提出结构化问题，校验并收集必需信息后继续运行。", true);
add("PC端", "智能对话", "回答展示", "流式 Markdown", "流式展示 Markdown、代码块、表格、Mermaid 等内容，并提供加载占位反馈。");
add("PC端", "智能对话", "回答展示", "思考与工具时间线", "展示 Agent 思考过程、知识检索、工具调用及执行结果的阶段时间线。");
add("PC端", "智能对话", "回答展示", "RAG 流水线进度", "按检索、重排、合并等阶段展示 RAG 处理进度与状态。");
add("PC端", "智能对话", "回答展示", "引用浮层与来源抽屉", "展示知识库与网页来源引用，可查看详情并从独立来源抽屉统一浏览。", true);
add("PC端", "智能对话", "回答展示", "推荐问题", "根据知识库内容生成会话起始推荐问题。");
add("PC端", "智能对话", "回答展示", "追问建议", "在回答结束后生成与当前知识范围相关的后续问题，点击即可继续提问。", true);
add("PC端", "智能对话", "回答操作", "复制与保存知识", "复制回答内容，或将有效回答保存为知识库条目。");
add("PC端", "智能对话", "工具安全", "MCP 工具人工审批", "对标记为需审批的 MCP 工具展示参数确认卡，允许批准或拒绝后再继续执行。");
add("PC端", "智能对话", "工具安全", "会话中 MCP OAuth", "MCP 服务缺少授权时在会话内发起 OAuth，授权完成后恢复 Agent 运行。", true);
add("PC端", "会话管理", "会话侧边栏", "会话列表与分组", "按日期或来源分组展示历史会话，支持折叠与搜索。");
add("PC端", "会话管理", "会话侧边栏", "来源筛选", "按 Web、IM、网站嵌入等来源筛选和切换会话。");
add("PC端", "会话管理", "会话操作", "重命名与置顶", "在侧边栏内直接修改会话标题，并按用户维度置顶重点会话。", true);
add("PC端", "会话管理", "会话操作", "批量删除", "选择多个历史会话并批量删除，减少无效会话占用。");

add("PC端", "知识库管理", "知识库列表", "列表与空间筛选", "按全部、收藏、最近、本空间及共享权限查看知识库。");
add("PC端", "知识库管理", "知识库列表", "搜索、置顶与收藏", "搜索知识库，并执行置顶、取消置顶或收藏等快捷操作。");
add("PC端", "知识库管理", "知识库列表", "知识库复制", "复制知识库配置与结构，快速创建同类知识库。", true);
add("PC端", "知识库管理", "知识库生命周期", "新建知识库", "创建文档型、FAQ 型或 Wiki 知识库并设置名称、描述和基础模型。");
add("PC端", "知识库管理", "知识库生命周期", "编辑、删除与分享", "修改知识库配置，删除知识库，或共享到指定工作区。");
add("PC端", "知识库管理", "内容导入", "文件与文件夹上传", "批量上传本地文件或文件夹，覆盖 PDF、Word、Excel、PPT、图片等格式。");
add("PC端", "知识库管理", "内容导入", "URL 导入", "通过网页 URL 抓取并导入内容，执行 SSRF 安全校验。");
add("PC端", "知识库管理", "内容导入", "在线知识录入", "手工创建和编辑 Markdown/文本知识内容。");
add("PC端", "知识库管理", "内容导入", "上传确认与批次配置", "上传前按批次覆盖解析器、分块、多模态、图谱和问题生成配置。");
add("PC端", "知识库管理", "数据源", "飞书数据源", "配置飞书文档或 Wiki 数据源并执行增量、全量同步。");
add("PC端", "知识库管理", "数据源", "Notion 数据源", "授权并选择 Notion 页面或数据库，周期性同步到知识库。");
add("PC端", "知识库管理", "数据源", "语雀数据源", "连接语雀知识库并同步文档内容。");
add("PC端", "知识库管理", "数据源", "RSS/Atom 订阅", "订阅 RSS 或 Atom 内容，支持增量抓取与部分失败提示。", true);
add("PC端", "知识库管理", "数据源", "同步日志", "查看数据源最近同步时间、状态、成功数量和失败原因。");
add("PC端", "知识库管理", "文档管理", "卡片/列表视图", "在卡片与列表视图间切换，按名称、状态、标签等条件筛选文档。");
add("PC端", "知识库管理", "文档管理", "框选与批量操作", "通过框选一次选择多篇文档，执行删除、重解析、标签等批量操作。");
add("PC端", "知识库管理", "文档管理", "多标签管理", "为文档添加多个标签，管理标签分组并按标签过滤。");
add("PC端", "知识库管理", "文档管理", "批量重新解析", "为多篇文档重新排队解析，可使用新的批次 process_config。", true);
add("PC端", "知识库管理", "文档管理", "文档预览", "在线预览原文、图片与解析后的内容，支持代码块深浅色显示。");
add("PC端", "知识库管理", "文档管理", "解析追踪时间线", "查看每次解析尝试及各阶段 Span、耗时、状态和错误信息。");
add("PC端", "知识库管理", "文档管理", "停止解析", "在解析时间线中终止进行中的解析任务并进入可靠收尾流程。");
add("PC端", "知识库管理", "文档管理", "分块查看与编辑", "查看文档切分结果、层级上下文和向量化状态，支持维护知识块。");
add("PC端", "知识库管理", "FAQ 管理", "问答对维护", "新增、编辑、删除 FAQ 问答对并跟踪导入进度。");
add("PC端", "知识库管理", "Wiki 模式", "Wiki 自动生成", "由 Agent 从原始文档生成结构化、相互链接的 Markdown Wiki 页面。");
add("PC端", "知识库管理", "Wiki 模式", "Wiki 文件夹与层级", "创建文件夹、移动页面并按分类层级浏览 Wiki 内容。", true);
add("PC端", "知识库管理", "Wiki 模式", "Wiki 知识图谱", "可视化 Wiki 页面及概念关联，辅助理解和导航。");
add("PC端", "知识库管理", "检索测试", "检索调试", "输入查询查看稀疏、稠密、图谱及重排后的召回结果。");
add("PC端", "知识库管理", "检索测试", "端到端评估", "运行检索与生成全链路测试，并查看命中率、BLEU、ROUGE 等指标。");
add("PC端", "知识库管理", "知识库设置", "模型配置", "配置 LLM、Embedding、Rerank 与视觉/语音模型。");
add("PC端", "知识库管理", "知识库设置", "向量存储", "选择工作区内可用的向量数据库实例与索引参数。");
add("PC端", "知识库管理", "知识库设置", "索引与解析策略", "配置 BM25、Dense、混合检索、多维索引及解析引擎。");
add("PC端", "知识库管理", "知识库设置", "分块设置与实时调试", "配置父子分块、自适应分块、重叠长度等，并实时预览分块效果。");
add("PC端", "知识库管理", "知识库设置", "图像与音频处理", "启用 VLM 图片描述、扫描 PDF OCR 和 ASR 音频转写。");
add("PC端", "知识库管理", "知识库设置", "知识图谱抽取", "配置实体关系抽取、GraphRAG 与 Neo4j 等图谱能力。");
add("PC端", "知识库管理", "知识库设置", "存储实例绑定", "为知识库绑定指定对象存储实例，或使用工作区默认实例。", true);
add("PC端", "知识库管理", "知识库设置", "高级与自定义指令", "配置问题生成、摘要、兜底回复、自定义指令及其他高级选项。", true);

add("PC端", "智能体管理", "智能体列表", "查看与搜索", "查看内置、自己创建和共享的智能体，并按名称搜索。");
add("PC端", "智能体管理", "智能体生命周期", "创建与复制", "从空白、预设类型或现有智能体复制创建自定义 Agent。");
add("PC端", "智能体管理", "智能体生命周期", "编辑、删除与分享", "修改智能体配置、删除自定义智能体或共享到工作区。");
add("PC端", "智能体管理", "基础配置", "名称、描述与运行模式", "设置智能体名称、说明、头像、快速问答/智能推理模式和 Agent 类型。");
add("PC端", "智能体管理", "提示词", "系统提示词与模板", "在线编辑系统提示词，或从提示词模板快速应用标准配置。");
add("PC端", "智能体管理", "模型配置", "LLM 与调用参数", "选择对话模型并配置超时、温度、思考模式和引用输出开关。", true);
add("PC端", "智能体管理", "知识检索", "知识库范围", "指定智能体可检索的知识库，并控制全部、指定或禁用范围。");
add("PC端", "智能体管理", "知识检索", "网络搜索", "启用网络搜索并配置搜索范围、结果数量与提供商能力。");
add("PC端", "智能体管理", "能力扩展", "多模态", "允许智能体接收图片及多模态输入。");
add("PC端", "智能体管理", "能力扩展", "内置工具", "选择数据库、代码执行、知识检索等内置工具范围。");
add("PC端", "智能体管理", "能力扩展", "MCP 服务", "按全部、指定或禁用配置智能体可调用的 MCP 服务。");
add("PC端", "智能体管理", "能力扩展", "Skills", "按全部、指定或禁用配置预装 Skills，扩展专业知识与工作流。");
add("PC端", "智能体管理", "能力扩展", "信息采集", "定义运行中需向用户收集的字段、问题、必填规则与校验方式。", true);
add("PC端", "智能体管理", "发布集成", "IM 渠道绑定", "将智能体绑定到企业微信、飞书、Slack、Telegram 等 IM 渠道。");
add("PC端", "智能体管理", "发布集成", "网站嵌入", "发布网站 Widget，配置域名白名单、限流、安全模式和在线预览。", true);
add("PC端", "智能体管理", "共享管理", "工作区共享", "将智能体共享到一个或多个工作区，并控制可见和可用范围。");

add("PC端", "共享空间", "空间列表", "我的空间与已加入空间", "查看拥有或加入的共享空间及其成员、资源数量和申请提醒。");
add("PC端", "共享空间", "空间生命周期", "创建、加入与退出", "创建共享空间，通过邀请码加入，或退出非本人拥有的空间。");
add("PC端", "共享空间", "空间生命周期", "删除工作区", "工作区所有者可删除空间并清理关联成员关系。", true);
add("PC端", "共享空间", "成员管理", "邀请与添加成员", "通过邀请码或账号添加成员，查看成员状态与加入时间。");
add("PC端", "共享空间", "成员管理", "角色与权限", "为成员分配 Owner、Admin、Contributor、Viewer 四级角色。");
add("PC端", "共享空间", "成员管理", "加入申请", "查看、批准或拒绝用户加入工作区的申请。");
add("PC端", "共享空间", "资源共享", "共享知识库", "查看共享到空间的知识库，并按权限访问或编辑。");
add("PC端", "共享空间", "资源共享", "共享智能体", "查看共享到空间的智能体，并在会话中使用。");
add("PC端", "全局效率", "命令面板", "全局搜索与快捷跳转", "通过 ⌘K 搜索知识库、智能体和文档，并快速执行导航或创建动作。");
add("PC端", "全局效率", "新手引导", "上下文引导", "为新用户提供知识库、智能体和模型配置的分步引导与聚光提示。");

// 平台后台：工作区配置、模型、中间件、权限、安全与运维控制台。
add("平台后台", "平台设置", "常规设置", "语言与主题", "配置界面语言、明暗主题及本地显示偏好。");
add("平台后台", "平台设置", "常规设置", "长期记忆", "配置用户级记忆开关并持久化到服务端偏好。");
add("平台后台", "平台设置", "会话设置", "历史会话策略", "配置会话历史、上下文及相关展示行为。");
add("平台后台", "模型管理", "模型列表", "多类型模型", "集中管理对话、Embedding、Rerank、视觉和语音模型。");
add("平台后台", "模型管理", "模型配置", "多厂商接入", "支持 OpenAI、DeepSeek、Qwen、智谱、混元、Gemini、MiniMax、NVIDIA、Ollama 等提供商。");
add("平台后台", "模型管理", "模型配置", "凭据与自定义请求头", "安全保存 API Key、自定义端点和 HTTP 请求头，读取时进行脱敏。");
add("平台后台", "模型管理", "模型配置", "思考模式与维度覆盖", "按模型配置思考格式、最大并发与 Embedding 维度等参数。", true);
add("平台后台", "模型管理", "模型调试", "交互式连接测试", "在绑定前测试对话、嵌入、重排和视觉模型配置并查看响应详情。", true);
add("平台后台", "模型管理", "内置模型", "YAML 声明式模型", "通过 YAML 定义平台内置模型并执行生命周期同步。");
add("平台后台", "模型管理", "托管能力", "WeKnora Cloud", "配置和使用 WeKnora Cloud 托管模型与文档解析能力。");

add("平台后台", "检索与存储", "网页搜索", "搜索提供商配置", "配置 DuckDuckGo、Bing、Google、Tavily、Baidu、SearXNG、Keenable 等提供商。", true);
add("平台后台", "检索与存储", "向量数据库", "向量库实例管理", "配置并测试 pgvector、Elasticsearch、OpenSearch、Milvus、Weaviate、Qdrant、Doris、腾讯 VectorDB。");
add("平台后台", "检索与存储", "对象存储", "多存储实例管理", "在工作区内新增、编辑、测试和删除多个本地或云对象存储实例。", true);
add("平台后台", "检索与存储", "对象存储", "默认存储实例", "指定工作区默认存储，并允许知识库单独覆盖绑定。", true);
add("平台后台", "检索与存储", "解析引擎", "解析器配置", "配置内置、OpenDataLoader、PaddleOCR-VL 等解析引擎及凭据。");

add("平台后台", "MCP 管理", "服务管理", "MCP 服务 CRUD", "创建、查看、编辑和删除 MCP 服务，支持内置与自定义服务。");
add("平台后台", "MCP 管理", "连接方式", "多传输协议", "支持 stdio、SSE 与 Streamable HTTP 等 MCP 传输方式。");
add("平台后台", "MCP 管理", "连接方式", "连接测试", "测试 MCP 服务连接并查看发现的工具、资源和错误信息。");
add("平台后台", "MCP 管理", "安全凭据", "凭据加密与自定义头", "加密保存 MCP 凭据，支持自定义 HTTP Header 和字段级清除。");
add("平台后台", "MCP 管理", "OAuth", "OAuth2 授权与撤销", "配置远程 MCP OAuth，查看授权状态并支持撤销、跳过和重新授权。", true);
add("平台后台", "MCP 管理", "工具策略", "工具人工审批规则", "按服务与工具配置是否需要人工审批，并在会话中执行批准流程。");

add("平台后台", "集成中心", "IM 渠道", "渠道统一管理", "统一查看并配置企业微信、飞书/Lark、QQBot、Slack、Telegram、钉钉、Mattermost、微信等渠道。", true);
add("平台后台", "集成中心", "网站嵌入", "嵌入渠道管理", "创建嵌入渠道、绑定智能体、配置白名单与访问限流。");
add("平台后台", "集成中心", "网站嵌入", "安全模式 Token", "使用发布 Token 交换短期会话 Token，隔离访客会话。", true);
add("平台后台", "集成中心", "API 集成", "细粒度 API Key", "创建独立 principal 的 API Key，配置角色、能力授权、知识库范围和最后使用时间。", true);
add("平台后台", "集成中心", "API 集成", "API 调试台", "在网页中创建、限定并测试 API Key 对应的接口能力。", true);

add("平台后台", "组织与权限", "成员管理", "成员与角色管理", "管理工作区成员、角色、禁用状态和资源归属。");
add("平台后台", "组织与权限", "RBAC", "四级角色矩阵", "提供 Owner、Admin、Contributor、Viewer 四级权限，并结合资源所有权校验。");
add("平台后台", "组织与权限", "RBAC", "知识库级权限", "对知识库执行所有者、编辑者、只读者等资源级授权控制。");
add("平台后台", "组织与权限", "平台管理员", "跨工作区超级管理员", "系统管理员可管理平台设置、管理员名册和跨工作区系统能力。");
add("平台后台", "组织与权限", "平台管理员", "管理员重置密码", "系统管理员可重置用户密码并撤销现有会话。", true);
add("平台后台", "审计与可观测", "审计日志", "工作区审计", "记录成员、角色、资源、配置和敏感操作日志，支持详情查看。");
add("平台后台", "审计与可观测", "Langfuse", "Agent 与检索追踪", "跟踪 ReAct 循环、Token、工具调用、检索与重排流水线。");
add("平台后台", "审计与可观测", "运行队列", "实时队列仪表盘", "系统管理员查看队列深度、阶段池、模型并发和任务处理状态。", true);
add("平台后台", "审计与可观测", "运行队列", "失败任务检查与重试", "分页检查失败任务、错误和载荷，并手动触发重试。", true);
add("平台后台", "审计与可观测", "系统信息", "版本与环境信息", "查看版本号、提交 ID、服务状态和关键运行环境信息。");

// 开放端：API、SDK、CLI、MCP、插件与小程序。
add("开放端", "开放接口", "RESTful API", "知识库与文档 API", "通过 API 创建知识库、上传/更新/删除文档、配置解析和执行检索。");
add("开放端", "开放接口", "RESTful API", "会话与 Agent API", "通过 API 创建会话、流式问答、管理 Agent 与处理工具审批。");
add("开放端", "开放接口", "RESTful API", "组织与平台 API", "通过 API 管理工作区、成员、共享资源、模型、MCP 和系统设置。");
add("开放端", "开发工具", "Go Client SDK", "类型化客户端", "提供知识库、Agent、会话、模型、组织、MCP、Skill 等类型化客户端。");
add("开放端", "开发工具", "CLI", "配置与认证", "管理 profile、登录、刷新认证、API Key 环境变量和输出格式。");
add("开放端", "开发工具", "CLI", "知识与会话命令", "通过 kb、doc、search、chat、session、model、message、skills 等命令操作平台。", true);
add("开放端", "开发工具", "CLI", "Agent 优先输出", "默认输出稳定 JSON envelope、类型化错误和退出码，适合 AI Agent 与 CI 使用。");
add("开放端", "开发工具", "MCP Server", "weknora mcp serve", "将 WeKnora 能力暴露为 MCP 工具，供支持 MCP 的 Agent 客户端调用。");
add("开放端", "开发工具", "预装 Skills", "RAG 搜索与共享 Skill", "随 CLI 提供知识检索、文档导入和共享资源相关 Skills。");
add("开放端", "客户端", "Chrome 扩展", "网页内容采集", "选中文本、图片或整页内容并一键保存到指定知识库。");
add("开放端", "客户端", "微信小程序", "移动知识问答", "配置 API、选择知识库、导入 URL，并在微信内进行知识问答。");
add("开放端", "客户端", "网站 Widget", "外部网站问答", "通过脚本嵌入独立聊天窗口或浮动入口，向网站访客提供 Agent 服务。");
add("开放端", "客户端", "IM 机器人", "多渠道问答", "在企业微信、飞书/Lark、QQBot、Slack、Telegram、钉钉等渠道直接问答。", true);

// 运维端：部署、基础设施、安全与后台任务。
add("运维端", "部署与升级", "本地与容器", "Docker Compose 部署", "通过 Docker Compose 启动核心服务，并按 profile 选择 Neo4j、MinIO、Langfuse 等组件。");
add("运维端", "部署与升级", "Kubernetes", "Helm Chart", "使用 Helm 在 Kubernetes 中部署应用、前端、Redis、PostgreSQL、Neo4j 和持久卷。");
add("运维端", "部署与升级", "轻量部署", "WeKnora Lite", "提供本地轻量服务、桌面 WebView 与相关打包脚本。");
add("运维端", "部署与升级", "版本升级", "数据库自动迁移", "服务启动或升级时执行数据库迁移，保持版本兼容。");
add("运维端", "任务与并发", "异步任务", "MQ 任务管理", "将文档解析、向量化、Wiki 等长任务异步化并支持状态恢复。");
add("运维端", "任务与并发", "Worker Pool", "分阶段工作池", "为 core、post-process、enrichment、maintenance、shared 和 Wiki 配置独立工作池。", true);
add("运维端", "任务与并发", "模型并发", "按模型并发治理", "为 Chat、Embedding、Rerank、VLM 等后台调用设置并发上限。", true);
add("运维端", "安全", "凭据保护", "AES-256-GCM 加密", "对 API Key、MCP 与数据源凭据进行静态加密并支持密钥轮换。");
add("运维端", "安全", "服务通信", "gRPC TLS 与 Token", "保护应用与 docreader 间的 gRPC 通信，支持 Redis TLS。", true);
add("运维端", "安全", "网络安全", "SSRF 防护", "对 URL 导入、数据源、模型、存储和重定向链路执行安全地址校验。");
add("运维端", "安全", "运行隔离", "Skill 沙箱", "在隔离环境中执行 Agent Skill 脚本，限制文件、命令和网络风险。");
add("运维端", "安全", "接口安全", "IDOR 与范围校验", "基于用户或 API Key principal 校验租户、知识库和能力范围，防止越权访问。", true);
add("运维端", "存储与检索", "向量数据库", "多后端适配", "适配 pgvector、Elasticsearch、OpenSearch、Milvus、Weaviate、Qdrant、Doris 和腾讯 VectorDB。");
add("运维端", "存储与检索", "对象存储", "多云存储适配", "适配本地、MinIO、COS、TOS、S3、OSS、KS3 和 OBS。");
add("运维端", "存储与检索", "检索优化", "混合检索与 HNSW", "组合 BM25、Dense、GraphRAG、父子分块与 pgvector HNSW 索引提升召回。");

const workbook = Workbook.create();
const sheet = workbook.worksheets.add("汇总");
const sourceSheet = workbook.worksheets.add("来源说明");
sheet.showGridLines = false;
sourceSheet.showGridLines = false;

const lastRow = items.length + 2;
sheet.mergeCells("A1:E1");
sheet.getRange("A1").values = [["WeKnora 平台 - 功能清单（基于 main / v0.7.0）"]];
sheet.getRange("A2:E2").values = [["操作端", "一级功能模块", "二级功能模块", "功能点", "功能说明"]];
sheet.getRange(`A3:E${lastRow}`).values = items.map((x) => [x.端, x.一级, x.二级, x.功能点, x.功能说明]);

const mergeGroups = (column, keyFn) => {
  let start = 0;
  while (start < items.length) {
    const key = keyFn(items[start]);
    let end = start;
    while (end + 1 < items.length && keyFn(items[end + 1]) === key) end += 1;
    if (end > start) sheet.mergeCells(`${column}${start + 3}:${column}${end + 3}`);
    start = end + 1;
  }
};
mergeGroups("A", (x) => x.端);
mergeGroups("B", (x) => `${x.端}|${x.一级}`);
mergeGroups("C", (x) => `${x.端}|${x.一级}|${x.二级}`);

sheet.getRange(`A1:E${lastRow}`).format = {
  font: { size: 10, color: "#222222" },
  verticalAlignment: "center",
  borders: { preset: "all", style: "thin", color: "#777777" },
};
sheet.getRange("A1:E1").format = {
  fill: "#2F5DA8",
  font: { bold: true, size: 16, color: "#FFFFFF" },
  horizontalAlignment: "center",
  verticalAlignment: "center",
  borders: { preset: "all", style: "medium", color: "#264A85" },
};
sheet.getRange("A2:E2").format = {
  fill: "#2F5DA8",
  font: { bold: true, size: 11, color: "#FFFFFF" },
  horizontalAlignment: "center",
  verticalAlignment: "center",
  borders: { preset: "all", style: "medium", color: "#264A85" },
};
sheet.getRange(`A3:D${lastRow}`).format.horizontalAlignment = "center";
sheet.getRange(`E3:E${lastRow}`).format.horizontalAlignment = "left";
sheet.getRange(`A3:E${lastRow}`).format.wrapText = true;
sheet.getRange("A:A").format.columnWidth = 13;
sheet.getRange("B:B").format.columnWidth = 24;
sheet.getRange("C:C").format.columnWidth = 24;
sheet.getRange("D:D").format.columnWidth = 31;
sheet.getRange("E:E").format.columnWidth = 86;
sheet.getRange("1:1").format.rowHeight = 30;
sheet.getRange("2:2").format.rowHeight = 24;

const opColors = {
  "PC端": "#DDEBF7",
  "平台后台": "#E4DFEC",
  "开放端": "#E2F0D9",
  "运维端": "#FCE4D6",
};
for (let i = 0; i < items.length; i += 1) {
  const row = i + 3;
  sheet.getRange(`A${row}`).format.fill = opColors[items[i].端];
  sheet.getRange(`${row}:${row}`).format.rowHeight = items[i].功能说明.length > 56 ? 36 : 27;
  if (items[i].重点) {
    sheet.getRange(`D${row}:E${row}`).format.fill = "#FFF200";
  }
}
sheet.freezePanes.freezeRows(2);
sheet.freezePanes.freezeColumns(1);

const sources = [
  ["1", "GitHub 仓库主页 / README", "https://github.com/Tencent/WeKnora", "项目定位、功能概览、支持的模型/存储/渠道及部署方式"],
  ["2", "中文 README", "https://github.com/Tencent/WeKnora/blob/main/README_CN.md", "中文功能说明、知识管理、智能对话、平台能力"],
  ["3", "CHANGELOG", "https://github.com/Tencent/WeKnora/blob/main/CHANGELOG.md", "v0.7.0 与历史版本已发布功能；黄色标注重点能力的主要依据"],
  ["4", "RBAC 说明", "https://github.com/Tencent/WeKnora/blob/main/docs/RBAC%E8%AF%B4%E6%98%8E.md", "工作区角色矩阵、资源所有权与权限控制"],
  ["5", "API 文档", "https://github.com/Tencent/WeKnora/tree/main/docs/api", "知识库、存储后端、Agent、会话等 API 能力"],
  ["6", "CLI 文档", "https://github.com/Tencent/WeKnora/blob/main/cli/README.md", "CLI 命令、认证、JSON 输出和 Agent 优先交互"],
  ["7", "MCP 配置", "https://github.com/Tencent/WeKnora/blob/main/mcp-server/MCP_CONFIG.md", "MCP Server 与传输方式配置"],
  ["8", "Agent Skills", "https://github.com/Tencent/WeKnora/blob/main/docs/agent-skills.md", "Skills 配置、执行和安全隔离"],
  ["9", "前端路由", "https://github.com/Tencent/WeKnora/blob/main/frontend/src/router/index.ts", "Web 页面入口、设置、知识库、智能体、共享空间路由"],
  ["10", "前端页面目录", "https://github.com/Tencent/WeKnora/tree/main/frontend/src/views", "页面与弹窗功能交叉核对"],
];
sourceSheet.mergeCells("A1:D1");
sourceSheet.getRange("A1").values = [["WeKnora 功能清单 - 来源与口径说明"]];
sourceSheet.getRange("A2:D2").values = [["序号", "资料", "URL", "用途"]];
sourceSheet.getRange(`A3:D${sources.length + 2}`).values = sources;
sourceSheet.mergeCells(`A${sources.length + 4}:D${sources.length + 4}`);
sourceSheet.getRange(`A${sources.length + 4}`).values = [[
  `整理日期：2026-07-23；口径：GitHub main 分支已发布/已落地功能，主版本参照 v0.7.0；汇总表共 ${items.length} 个功能点。黄色单元格表示 v0.7.0 新增或当前重点能力。`,
]];
sourceSheet.getRange(`A1:D${sources.length + 4}`).format = {
  font: { size: 10, color: "#222222" },
  verticalAlignment: "center",
  wrapText: true,
  borders: { preset: "all", style: "thin", color: "#B7B7B7" },
};
sourceSheet.getRange("A1:D1").format = {
  fill: "#2F5DA8",
  font: { bold: true, size: 15, color: "#FFFFFF" },
  horizontalAlignment: "center",
  borders: { preset: "all", style: "medium", color: "#264A85" },
};
sourceSheet.getRange("A2:D2").format = {
  fill: "#D9EAF7",
  font: { bold: true, size: 11, color: "#1F1F1F" },
  horizontalAlignment: "center",
};
sourceSheet.getRange("A:A").format.columnWidth = 8;
sourceSheet.getRange("B:B").format.columnWidth = 25;
sourceSheet.getRange("C:C").format.columnWidth = 78;
sourceSheet.getRange("D:D").format.columnWidth = 52;
sourceSheet.getRange(`A3:A${sources.length + 2}`).format.horizontalAlignment = "center";
sourceSheet.getRange(`A3:D${sources.length + 2}`).format.rowHeight = 30;
sourceSheet.getRange(`A${sources.length + 4}:D${sources.length + 4}`).format = {
  fill: "#FFF2CC",
  font: { italic: true, size: 10, color: "#7F6000" },
  horizontalAlignment: "left",
  verticalAlignment: "center",
  wrapText: true,
};
sourceSheet.getRange(`${sources.length + 4}:${sources.length + 4}`).format.rowHeight = 42;
sourceSheet.freezePanes.freezeRows(2);

await fs.mkdir(outputDir, { recursive: true });
const mainPreview = await workbook.render({ sheetName: "汇总", range: `A1:E${lastRow}`, scale: 1, format: "png" });
await fs.writeFile(`${outputDir}/weknora_function_list_preview.png`, new Uint8Array(await mainPreview.arrayBuffer()));
const sourcePreview = await workbook.render({ sheetName: "来源说明", autoCrop: "all", scale: 1, format: "png" });
await fs.writeFile(`${outputDir}/weknora_sources_preview.png`, new Uint8Array(await sourcePreview.arrayBuffer()));

const xlsx = await SpreadsheetFile.exportXlsx(workbook);
await xlsx.save(outputPath);
console.log(JSON.stringify({ outputPath, itemCount: items.length, lastRow }));
