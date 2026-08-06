<script setup lang="ts">
import Illus from './Illus.vue'

const stats = [
  { value: '25', unit: '种', label: '文件格式：文档、网页、扫描件、图片、音频' },
  { value: '26', unit: '家+', label: '模型厂商，也可全部换成本地推理' },
  { value: '9', unit: '个', label: '使用入口：Web、IM、插件、命令行、MCP' },
  { value: '4', unit: '路', label: '索引同时生效：向量、关键词、Wiki、图谱' },
]

const schema = [
  {
    step: '01',
    name: '接入',
    hint: '资料从哪里来',
    items: ['文件上传', 'URL 抓取', '飞书', 'Notion', '语雀', 'RSS'],
  },
  {
    step: '02',
    name: '理解',
    hint: '转成结构化文本',
    items: ['版式分析', '扫描件 OCR', '表格抽取', '图片描述', '音频转写'],
  },
  {
    step: '03',
    name: '索引',
    hint: '四路可同时开启',
    items: ['自适应分块', '向量', '关键词', 'Wiki', '知识图谱'],
  },
  {
    step: '04',
    name: '应用',
    hint: '对外提供的能力',
    items: ['知识问答', 'Agent 推理', 'Wiki 站点', '数据分析', 'FAQ'],
  },
]

const chain = [
  {
    step: '01',
    icon: 'read',
    title: '读懂文档',
    desc: '扫描件走 OCR、插图交给视觉模型描述、音频由语音模型转写、Excel 合并单元格自动补齐。独立的 docreader 服务把 PDF、Office、网页、图片与音频还原成带版式的文本，而不是只抽出一段纯文字。',
    href: '/03-features/03-document-parsing',
  },
  {
    step: '02',
    icon: 'chunk',
    title: '切好知识',
    desc: '按文档特征在标题、启发式、递归三档策略间自动选择切法，父子分块让检索命中小块、送进模型的却是完整上下文。同一份文档可同时写入向量、关键词、Wiki 与知识图谱四路索引。',
    href: '/02-architecture/03-document-pipeline',
  },
  {
    step: '03',
    icon: 'retrieve',
    title: '找准证据',
    desc: '先做意图识别与查询改写，向量与 BM25 并行召回后用 RRF 融合，再由重排模型定序；开启知识图谱时补一批实体关系证据，覆盖「A 和 B 什么关系」这类问题。',
    href: '/03-features/05-retrieval-engines',
  },
  {
    step: '04',
    icon: 'answer',
    title: '给出可核对的回答',
    desc: '常规问题单轮检索直接生成；复杂任务交给 ReAct Agent 自行决定检索几轮、调哪些工具、要不要跑数据分析。回答流式返回，逐段标注出处，可点开原文核对。',
    href: '/03-features/07-agent',
  },
]

const surfaces = [
  { icon: 'console', name: 'Web 控制台', desc: '知识库管理、对话、Wiki 浏览与系统配置的完整界面。' },
  { icon: 'extension', name: 'Chrome 插件', desc: '网页侧边栏问答，支持正文剪藏与 Markdown 速记入库。' },
  { icon: 'embed', name: '网页嵌入挂件', desc: '一段 script 即可在自有站点提供悬浮问答，访客无需登录。' },
  { icon: 'desktop', name: '桌面客户端', desc: '单机运行的桌面应用，自带后端与本地存储；尚未正式发布，需自行构建。' },
  { icon: 'bot', name: 'IM 机器人', desc: '企业微信、飞书、钉钉、Slack 等 10 个平台的官方适配器。' },
  { icon: 'mobile', name: '微信小程序', desc: '移动端入口，支持网页收藏入库与提问。' },
  { icon: 'cli', name: '命令行 weknora', desc: '文档管理、检索与带引用的流式问答，默认 JSON 输出，便于脚本化。' },
  { icon: 'api', name: 'REST API 与 Go SDK', desc: '完整 /api/v1 接口；API Key 支持按能力与知识库范围授权。' },
  { icon: 'mcp', name: 'MCP Server', desc: '将 WeKnora 暴露为 MCP 工具，供 Claude、Cursor 等客户端检索。' },
]

const features = [
  {
    icon: 'wiki',
    title: 'Wiki 模式',
    desc: '由模型从文档中抽取实体与概念，生成互相链接且标注出处的页面，并自动组织为目录树与关系图谱。适用于资料分散、缺少整体索引的场景。',
    href: '/03-features/14-wiki',
    tag: '知识组织',
  },
  {
    icon: 'version',
    title: '分块级编辑与版本管理',
    desc: '解析结果可在分块粒度直接修正，保存后即时重建索引，每次修改保留历史版本并支持回滚。Wiki 页面同样具备版本历史，并区分管道、Agent 与人工三类编辑来源。',
    href: '/03-features/02-knowledge-base',
    tag: '可维护性',
  },
  {
    icon: 'channels',
    title: '多渠道统一接入',
    desc: '同一个 Agent 可同时发布到 10 个 IM 平台、自有站点的嵌入挂件与浏览器插件，会话、权限与知识范围沿用同一套配置。',
    href: '/03-features/12-im-integration',
    tag: '接入',
  },
  {
    icon: 'mcp',
    title: 'MCP 双向集成',
    desc: '作为客户端接入外部 MCP 服务，支持 OAuth 授权与工具级人工审批；同时可作为 MCP Server 对外提供检索能力。',
    href: '/03-features/08-mcp',
    tag: '工具生态',
  },
  {
    icon: 'govern',
    title: '多空间隔离与审计',
    desc: '工作空间级数据隔离与四级角色矩阵，API Key 可按能力与知识库范围收窄。操作行为写入审计日志，后台任务提供队列面板，问答链路支持 Langfuse 追踪。',
    href: '/03-features/20-platform-admin',
    tag: '企业部署',
  },
  {
    icon: 'pluggable',
    title: '可插拔架构',
    desc: '解析引擎、分块策略、检索引擎、模型厂商、搜索引擎与存储后端均以注册表接入，可按配置替换；知识库支持分别绑定不同的向量库与存储实例。',
    href: '/06-development/03-extension-points',
    tag: '架构',
  },
  {
    icon: 'graph',
    title: '知识图谱增强检索',
    desc: '入库时由模型抽取实体与关系存入图数据库，提问时顺着关系补充召回，用于回答「A 与 B 之间是什么关系」这类向量检索不擅长的问题。适合人物、组织、合同条款等关系密集的资料。',
    href: '/03-features/09-knowledge-graph',
    tag: '检索',
  },
  {
    icon: 'sync',
    title: '数据源持续同步',
    desc: '飞书、Notion、语雀与 RSS 绑定一次凭据后按计划自动同步：首次全量，之后按修改时间增量拉取，源端删除的文档同步下架，避免知识库随时间过期。',
    href: '/03-features/10-datasource',
    tag: '数据接入',
  },
  {
    icon: 'faq',
    title: 'FAQ 精确问答',
    desc: '退货政策、报销流程这类答案固定的问题可直接维护成问答对，按「标准问 + 相似问 + 反例问」匹配问题而非文档片段。可与文档库被同一 Agent 检索，形成先查标准答案再翻文档的顺序。',
    href: '/03-features/17-faq',
    tag: '答案质量',
  },
]

const map = [
  {
    index: '01',
    icon: 'start',
    title: '快速开始',
    brief: '按顺序读完四篇，可完成部署并跑通首次问答。',
    items: [
      { text: '产品介绍', link: '/01-getting-started/01-introduction' },
      { text: '安装部署', link: '/01-getting-started/02-installation' },
      { text: '快速上手', link: '/01-getting-started/03-quickstart' },
      { text: '配置详解', link: '/01-getting-started/04-configuration' },
    ],
  },
  {
    index: '02',
    icon: 'arch',
    title: '架构',
    brief: '系统全貌，以及文档入库与检索问答两条主干流水线。',
    items: [
      { text: '总体架构', link: '/02-architecture/01-overview' },
      { text: 'Go 后端设计', link: '/02-architecture/02-backend-design' },
      { text: '文档入库流程', link: '/02-architecture/03-document-pipeline' },
      { text: '检索问答流程', link: '/02-architecture/04-rag-pipeline' },
      { text: '异步任务系统', link: '/02-architecture/05-async-tasks' },
    ],
  },
  {
    index: '03',
    icon: 'modules',
    title: '功能模块',
    brief: '二十一项能力的配置项、行为约定与实现路径。',
    items: [
      { text: '租户、用户与认证授权', link: '/03-features/01-tenant-auth' },
      { text: '知识库与知识管理', link: '/03-features/02-knowledge-base' },
      { text: '文档解析服务 docreader', link: '/03-features/03-document-parsing' },
      { text: '分块机制', link: '/03-features/04-chunking' },
      { text: '检索引擎与向量存储', link: '/03-features/05-retrieval-engines' },
      { text: '模型管理', link: '/03-features/06-models' },
      { text: 'Agent 引擎', link: '/03-features/07-agent' },
      { text: 'MCP 集成', link: '/03-features/08-mcp' },
      { text: '知识图谱', link: '/03-features/09-knowledge-graph' },
      { text: '数据源导入', link: '/03-features/10-datasource' },
      { text: '网络搜索与网页抓取', link: '/03-features/11-web-search' },
      { text: 'IM 集成', link: '/03-features/12-im-integration' },
      { text: '网页嵌入 Embed', link: '/03-features/13-embed-channel' },
      { text: 'Wiki 能力', link: '/03-features/14-wiki' },
      { text: '评估能力', link: '/03-features/15-evaluation' },
      { text: '可观测性与审计', link: '/03-features/16-observability' },
      { text: 'FAQ 能力', link: '/03-features/17-faq' },
      { text: '会话与对话体验', link: '/03-features/18-chat-experience' },
      { text: '存储后端', link: '/03-features/19-storage-backends' },
      { text: '平台管理与系统管理员', link: '/03-features/20-platform-admin' },
      { text: '图片与文件的对外访问', link: '/03-features/21-file-access' },
    ],
  },
  {
    index: '04',
    icon: 'api',
    title: 'API 参考',
    brief: '约 360 个端点，含权限要求、参数表与 curl 示例。',
    items: [
      { text: 'API 总览', link: '/04-api/01-api-overview' },
      { text: 'Agent、MCP 与技能', link: '/04-api/02-api-agent-mcp' },
      { text: '认证与用户', link: '/04-api/02-api-auth' },
      { text: 'IM、Embed 与文件', link: '/04-api/02-api-channels' },
      { text: '会话、消息与聊天', link: '/04-api/02-api-chat' },
      { text: 'FAQ 与 Wiki', link: '/04-api/02-api-faq-wiki' },
      { text: '基础设施与数据源', link: '/04-api/02-api-infra' },
      { text: '知识库与知识', link: '/04-api/02-api-knowledge' },
      { text: '分块与标签', link: '/04-api/02-api-chunks' },
      { text: '模型与初始化', link: '/04-api/02-api-model-system' },
      { text: '系统与平台管理', link: '/04-api/02-api-system' },
      { text: '组织与共享', link: '/04-api/02-api-org' },
      { text: '租户与成员', link: '/04-api/02-api-tenant' },
    ],
  },
  {
    index: '05',
    icon: 'clients',
    title: '客户端',
    brief: '七种客户端：Web、CLI、SDK、小程序、桌面端、浏览器插件与 Skill。',
    items: [
      { text: 'Web 前端', link: '/05-clients/01-frontend' },
      { text: '命令行工具 CLI', link: '/05-clients/02-cli' },
      { text: 'Go SDK', link: '/05-clients/03-go-sdk' },
      { text: '微信小程序', link: '/05-clients/04-miniprogram' },
      { text: '桌面客户端', link: '/05-clients/05-desktop' },
      { text: 'Chrome 插件', link: '/05-clients/06-chrome-extension' },
      { text: 'Claw Skill', link: '/05-clients/07-claw-skill' },
    ],
  },
  {
    index: '06',
    icon: 'dev',
    title: '开发指南',
    brief: '本地开发环境、数据库迁移，以及九类可插拔扩展点。',
    items: [
      { text: '开发指南', link: '/06-development/01-dev-guide' },
      { text: '数据库与迁移', link: '/06-development/02-database-schema' },
      { text: '扩展点指南', link: '/06-development/03-extension-points' },
    ],
  },
]

const deployments = [
  { icon: 'compose', name: 'Docker Compose', desc: '标准部署，12 个可选 profile 组合基础设施', note: '' },
  { icon: 'helm', name: 'Helm', desc: 'Kubernetes 集群编排，适用于生产多副本', note: '' },
  {
    icon: 'lite',
    name: 'Lite（单二进制 / 桌面应用）',
    desc: 'SQLite + 进程内队列，无需 Docker 与外部数据库；提供命令行与图形界面两种形式',
    note: '桌面应用尚未正式发布，需自行构建',
  },
]
</script>

<template>
  <div class="landing">
    <!-- ============================= Hero ============================= -->
    <section class="hero">
      <div class="shell hero-grid">
        <div class="hero-copy">
          <p class="eyebrow">Tencent 开源 · WeKnora v0.7.1 · 官方文档</p>
          <h1 class="display">
            开源的知识库问答系统
          </h1>
          <p class="lede">WeKnora（维娜拉）将 PDF、Word、网页与飞书 / Notion / 语雀等来源的资料汇入知识库，提供检索增强的问答能力，回答标注可追溯的出处。除基础问答外，还提供 <strong>Wiki 自动成书</strong>、<strong>ReAct Agent 与 MCP 双向集成</strong>、<strong>知识图谱增强检索</strong>，以及面向团队的<strong>多空间隔离、四级 RBAC、作用域 API Key 与审计日志</strong>。支持完整私有部署，模型可全部替换为本地推理。</p>
          <p class="lede lede-sub">本文档覆盖部署与配置、功能说明、约 360 个 API 端点的接口参考，以及二次开发的扩展点。</p>
          <div class="actions">
            <a class="btn btn-solid" href="/01-getting-started/01-introduction">开始阅读</a>
            <a class="btn btn-ghost" href="/02-architecture/01-overview">系统架构</a>
            <a
              class="btn btn-text"
              href="https://github.com/Tencent/WeKnora"
              target="_blank"
              rel="noreferrer"
            >
              GitHub 仓库 ↗
            </a>
          </div>
        </div>

        <aside class="hero-side">
          <div class="schema" aria-label="能力分层">
            <p class="schema-title">能力分层</p>
            <ol class="schema-layers">
              <li v-for="layer in schema" :key="layer.name" class="layer">
                <span class="layer-step">{{ layer.step }}</span>
                <div class="layer-body">
                  <p class="layer-head">
                    <span class="layer-name">{{ layer.name }}</span>
                    <span class="layer-hint">{{ layer.hint }}</span>
                  </p>
                  <div class="layer-items">
                    <span v-for="n in layer.items" :key="n" class="chip">{{ n }}</span>
                  </div>
                </div>
              </li>
            </ol>
            <p class="schema-note">各层实现均可替换，未使用的能力可关闭；模型支持本地部署。</p>
          </div>
        </aside>
      </div>

      <svg class="wave" viewBox="0 0 1440 120" preserveAspectRatio="none" aria-hidden="true">
        <path
          d="M0 74C180 40 360 34 540 52c180 18 300 44 480 42s240-30 420-52"
          fill="none"
          stroke="var(--wk-ink)"
          stroke-opacity="0.16"
          stroke-width="1.5"
        />
        <path
          d="M0 92C200 62 380 58 560 74c180 16 300 40 470 36s250-26 410-44"
          fill="none"
          stroke="var(--wk-gold)"
          stroke-opacity="0.45"
          stroke-width="1.2"
        />
        <path
          d="M0 108C220 84 400 82 580 94c180 12 320 32 500 28s260-22 360-34"
          fill="none"
          stroke="var(--wk-ink)"
          stroke-opacity="0.08"
          stroke-width="1"
        />
      </svg>
    </section>

    <!-- ============================ 全景 ============================ -->
    <section class="panorama">
      <div class="shell">
        <Illus name="flow" class="panorama-illus" />
        <p class="panorama-note">
          资料从文件、网页、音频与图片进来，统一解析后并行写入向量、关键词、Wiki 与知识图谱四路索引；
          同一套知识库与 Agent 再展开成九种客户端，换入口不用换一套系统。
        </p>
      </div>
    </section>

    <!-- ============================ 数字 ============================ -->
    <section class="stats">
      <div class="shell stats-row">
        <div v-for="s in stats" :key="s.label" class="stat">
          <span class="stat-value">{{ s.value }}<i>{{ s.unit }}</i></span>
          <span class="stat-label">{{ s.label }}</span>
        </div>
      </div>
    </section>

    <!-- ============================ 主链路 ============================ -->
    <section class="chapter">
      <div class="shell">
        <header class="chapter-head">
          <span class="marker">处理流程</span>
          <h2 class="chapter-title">从一份 PDF，到一句带出处的回答</h2>
          <p class="chapter-sub">问答效果不好，问题往往不在模型，而在这条链路上：扫描件没读出文字、表格被切碎、召回的段落答非所问。下面四个环节各自对应一类失分点，且实现均可按需替换。</p>
        </header>

        <ol class="chain">
          <li v-for="c in chain" :key="c.step" class="chain-item">
            <a :href="c.href">
              <span class="chain-head">
                <Illus :name="c.icon" class="chain-icon" />
                <span class="chain-step">{{ c.step }}</span>
              </span>
              <h3 class="chain-title">{{ c.title }}</h3>
              <p class="chain-desc">{{ c.desc }}</p>
            </a>
          </li>
        </ol>
      </div>
    </section>

    <!-- ============================ 特色能力 ============================ -->
    <section class="chapter chapter-alt">
      <div class="shell">
        <header class="chapter-head">
          <span class="marker">核心能力</span>
          <h2 class="chapter-title">超出基础检索问答的部分</h2>
          <p class="chapter-sub">以下能力为 WeKnora 的主要投入方向，可作为技术选型时的对比维度。</p>
        </header>

        <div class="features">
          <a v-for="f in features" :key="f.title" class="feature" :href="f.href">
            <span class="feature-head">
              <Illus :name="f.icon" class="feature-icon" />
              <span class="feature-tag">{{ f.tag }}</span>
            </span>
            <h3 class="feature-title">{{ f.title }}</h3>
            <p class="feature-desc">{{ f.desc }}</p>
          </a>
        </div>
      </div>
    </section>

    <!-- ============================ 触达方式 ============================ -->
    <section class="chapter">
      <div class="shell">
        <header class="chapter-head">
          <span class="marker">接入方式</span>
          <h2 class="chapter-title">九种客户端与集成入口</h2>
          <p class="chapter-sub">同一套知识库与 Agent 配置，可从浏览器、IM、自有站点、终端与外部智能体访问，无需为各入口重复搭建。</p>
        </header>

        <div class="surfaces">
          <div v-for="s in surfaces" :key="s.name" class="surface">
            <h3 class="surface-name">
              <Illus :name="s.icon" class="surface-icon" />
              {{ s.name }}
            </h3>
            <p class="surface-desc">{{ s.desc }}</p>
          </div>
        </div>

        <div class="surfaces-actions">
          <a class="btn btn-ghost" href="/05-clients/01-frontend">查看客户端文档</a>
          <a class="btn btn-text" href="/03-features/13-embed-channel">网页嵌入 ↗</a>
          <a class="btn btn-text" href="/03-features/12-im-integration">IM 集成 ↗</a>
        </div>
      </div>
    </section>

    <!-- ============================ 文档地图 ============================ -->
    <section class="chapter chapter-alt">
      <div class="shell">
        <header class="chapter-head">
          <span class="marker">文档地图</span>
          <h2 class="chapter-title">六个部分，五十三篇</h2>
          <p class="chapter-sub">覆盖部署上手、系统架构、功能说明、接口参考、客户端与二次开发。</p>
        </header>

        <div class="map">
          <section
            v-for="m in map"
            :key="m.index"
            class="map-block"
            :class="{ 'map-block-wide': m.items.length > 8 }"
          >
            <div class="map-head">
              <span class="map-index">{{ m.index }}</span>
              <h3 class="map-title">
                <Illus :name="m.icon" class="map-icon" />
                {{ m.title }}
              </h3>
              <p class="map-brief">{{ m.brief }}</p>
            </div>
            <ul class="map-list">
              <li v-for="it in m.items" :key="it.link">
                <a :href="it.link">{{ it.text }}</a>
              </li>
            </ul>
          </section>
        </div>
      </div>
    </section>

    <!-- ============================ 部署 ============================ -->
    <section class="chapter">
      <div class="shell deploy">
        <div class="deploy-copy">
          <span class="marker">部署</span>
          <h2 class="chapter-title">部署形态</h2>
          <p class="chapter-sub">标准部署克隆代码、改两个密钥、一条命令拉起全套服务；本机试用可选 Lite 模式，不依赖 PostgreSQL 与 Redis。</p>
          <ul class="deploy-list">
            <li v-for="d in deployments" :key="d.name">
              <span class="deploy-name">
                <Illus :name="d.icon" class="deploy-icon" />
                {{ d.name }}
              </span>
              <span class="deploy-desc">
                {{ d.desc }}
                <span v-if="d.note" class="deploy-note">{{ d.note }}</span>
              </span>
            </li>
          </ul>
          <a class="btn btn-ghost" href="/01-getting-started/02-installation">查看安装部署</a>
        </div>

        <div class="deploy-code">
          <div class="code-bar">
            <span>标准部署 · Docker Compose</span>
          </div>
          <pre><code><span class="c"># 1 获取代码</span>
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora

<span class="c"># 2 准备配置：至少修改 JWT_SECRET 与 SYSTEM_AES_KEY</span>
cp .env.example .env

<span class="c"># 3 拉起全部服务（首次需拉取镜像）</span>
docker compose up -d --pull always

<span class="c"># 4 确认服务就绪</span>
docker compose ps
curl http://localhost:8080/health

<span class="c"># 5 打开前端（默认 80 端口，可用 FRONTEND_PORT 改）</span>
open http://localhost

<span class="c"># 停止：docker compose down</span></code></pre>
          <p class="deploy-code-note">
            首次打开前端会落到注册页，注册后在初始化向导里配置对话模型与向量模型，即可建库提问。后端 API 与前端同域，走 <code>http://localhost/api/v1</code>。
            完整步骤见<a href="/01-getting-started/03-quickstart">快速上手</a>，其余部署形态与参数见<a href="/01-getting-started/02-installation">安装部署</a>。
          </p>
        </div>
      </div>
    </section>

    <!-- ============================ 结尾 ============================ -->
    <footer class="closing">
      <div class="shell closing-inner">
        <div class="closing-brand">
          <svg width="34" height="26" viewBox="0 0 34 26" fill="none" aria-hidden="true">
            <path
              d="M20.6 3.2c.36-.5 1.16-.22 1.13.39l-.53 10.2-6.9-.05c-.6 0-.86-.75-.4-1.13L20.6 3.2z"
              fill="currentColor"
            />
            <path
              d="M1.5 18.4c6.4-1.9 12.2-1.1 18.1.35 4.3 1.05 8.2 1.6 12.9.1"
              stroke="currentColor"
              stroke-width="2.1"
              stroke-linecap="round"
            />
            <path
              d="M4.4 22.1c5.6-1.35 10.8-.7 16 .5 3.8.87 7.2 1.2 11.3.15"
              stroke="var(--wk-gold)"
              stroke-width="1.4"
              stroke-linecap="round"
            />
          </svg>
          <span>WeKnora</span>
        </div>
        <p class="closing-note">文档对应仓库 main 分支。源码路径均相对仓库根目录，API 路径默认带 <code>/api/v1</code> 前缀，配置示例中的密钥均为占位符。</p>
        <div class="closing-links">
          <a href="/01-getting-started/01-introduction">快速开始</a>
          <a href="/04-api/01-api-overview">API 总览</a>
          <a href="/06-development/03-extension-points">扩展点</a>
          <a href="https://github.com/Tencent/WeKnora" target="_blank" rel="noreferrer">GitHub</a>
        </div>
        <p class="closing-copy">© Tencent WeKnora · MIT License</p>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.landing {
  /* 与文档页铺满视口的正文列保持同一量级，避免首页明显更窄 */
  --shell: 1600px;
  color: var(--wk-ink);
}

.shell {
  max-width: var(--shell);
  margin: 0 auto;
  padding: 0 32px;
}

.marker {
  display: inline-block;
  font-family: var(--wk-font-mono);
  font-size: 10.5px;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  color: var(--wk-gold);
  margin-bottom: 22px;
}

.marker::before {
  content: "";
  display: inline-block;
  width: 22px;
  height: 1px;
  background: var(--wk-gold);
  vertical-align: middle;
  margin-right: 12px;
}

/* ------------------------------- Hero ------------------------------- */

.hero {
  position: relative;
  padding: clamp(80px, 13vw, 168px) 0 clamp(96px, 12vw, 150px);
  overflow: hidden;
}

.hero::before {
  content: "";
  position: absolute;
  inset: 0;
  background:
    radial-gradient(120% 80% at 78% -10%, rgba(184, 134, 59, 0.09), transparent 62%),
    radial-gradient(90% 70% at 8% 0%, rgba(16, 31, 56, 0.06), transparent 60%);
  pointer-events: none;
}

.hero-grid {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1.15fr) minmax(0, 0.85fr);
  gap: 76px;
  align-items: center;
}

.eyebrow {
  font-family: var(--wk-font-mono);
  font-size: 11px;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--wk-ink-mute);
  margin: 0 0 34px;
}

.display {
  font-family: var(--wk-font-serif);
  font-weight: 500;
  font-size: clamp(34px, 4.1vw, 58px);
  line-height: 1.2;
  letter-spacing: -0.025em;
  margin: 0;
}

.lede {
  margin: 34px 0 0;
  max-width: 60ch;
  font-size: 16.5px;
  line-height: 1.85;
  color: var(--wk-ink-soft);
}

/* 第二段说明这个站点本身是什么，弱化一档以免与定位句抢注意力 */
.lede-sub {
  margin-top: 14px;
  font-size: 15px;
  color: var(--wk-ink-mute);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  margin-top: 46px;
}

.btn {
  display: inline-flex;
  align-items: center;
  height: 46px;
  padding: 0 26px;
  border-radius: 2px;
  font-size: 14px;
  font-weight: 550;
  letter-spacing: 0.02em;
  text-decoration: none;
  transition: all 0.22s ease;
}

.btn-solid {
  background: var(--wk-ink);
  color: var(--wk-paper);
  border: 1px solid var(--wk-ink);
}

.btn-solid:hover {
  background: transparent;
  color: var(--wk-ink);
}

.btn-ghost {
  border: 1px solid var(--wk-rule);
  color: var(--wk-ink);
}

.btn-ghost:hover {
  border-color: var(--wk-gold);
  color: var(--wk-gold);
}

.btn-text {
  padding: 0 6px;
  color: var(--wk-ink-mute);
}

.btn-text:hover {
  color: var(--wk-ink);
}

/* 系统组件概览 */

/* 全景图：整条链路加九个客户端，横向铺得很开，
   塞进首屏右栏那张卡片里只会挤成一团，所以单独占一条通栏 */
.panorama {
  padding: clamp(18px, 3vw, 40px) 0 clamp(28px, 4vw, 56px);
}

.panorama-illus {
  display: block;
  width: 100%;
  max-width: 1180px;
  height: auto;
  margin: 0 auto;
  color: var(--wk-ink);
  opacity: 0.9;
}

.panorama-note {
  max-width: 860px;
  margin: 26px auto 0;
  text-align: center;
  font-size: 13.5px;
  line-height: 1.8;
  color: var(--wk-ink-mute);
}

/* 能力分层：序号列 + 贯穿的竖轴表达自上而下的流向，
   原来只靠层间一个小箭头，读者看不出这是一条流水线 */
.schema {
  padding: 26px 26px 22px;
  border: 1px solid var(--wk-rule);
  border-radius: 4px;
  background: color-mix(in srgb, var(--wk-paper-2) 60%, transparent);
}

.schema-title {
  margin: 0 0 20px;
  font-family: var(--wk-font-mono);
  font-size: 10.5px;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--wk-ink-mute);
}

.schema-layers {
  margin: 0;
  padding: 0;
  list-style: none;
}

.layer {
  position: relative;
  display: grid;
  grid-template-columns: 34px 1fr;
  gap: 14px;
  padding: 0 0 22px;
}

.layer:last-child {
  padding-bottom: 0;
}

/* 竖轴连接各层序号；最后一层不再向下延伸 */
.layer:not(:last-child)::before {
  content: "";
  position: absolute;
  left: 11px;
  top: 24px;
  bottom: 4px;
  width: 1px;
  background: var(--wk-rule);
}

.layer-step {
  z-index: 1;
  display: grid;
  place-items: center;
  width: 23px;
  height: 23px;
  border: 1px solid var(--wk-rule);
  border-radius: 50%;
  background: var(--wk-paper);
  font-family: var(--wk-font-mono);
  font-size: 10px;
  color: var(--wk-ink-mute);
}

.layer-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin: 3px 0 10px;
}

.layer-name {
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--wk-ink);
}

.layer-hint {
  font-size: 12px;
  color: var(--wk-ink-mute);
}

.layer-items {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.chip {
  font-size: 12px;
  line-height: 1.5;
  padding: 4px 10px;
  border: 1px solid var(--wk-rule);
  border-radius: 2px;
  color: var(--wk-ink-soft);
  background: color-mix(in srgb, var(--wk-paper) 70%, transparent);
  white-space: nowrap;
}

.schema-note {
  margin: 22px 0 0;
  padding-top: 16px;
  border-top: 1px solid var(--wk-rule-soft);
  font-size: 12px;
  line-height: 1.7;
  color: var(--wk-ink-mute);
}

.wave {
  position: absolute;
  left: 0;
  right: 0;
  bottom: -1px;
  width: 100%;
  height: 120px;
  pointer-events: none;
}

/* ------------------------------- 数字 ------------------------------- */

.stats {
  border-top: 1px solid var(--wk-rule-soft);
  border-bottom: 1px solid var(--wk-rule-soft);
  background: var(--wk-paper-2);
}

.stats-row {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
}

@media (max-width: 900px) {
  .stats-row {
    grid-template-columns: repeat(2, 1fr);
  }
}

.stat {
  padding: 34px 0;
  text-align: center;
  border-left: 1px solid var(--wk-rule-soft);
}

.stat:first-child {
  border-left: none;
}

.stat-value {
  display: block;
  font-family: var(--wk-font-serif);
  font-size: 34px;
  line-height: 1;
  font-weight: 500;
  letter-spacing: -0.02em;
}

.stat-value i {
  font-style: normal;
  font-size: 0.6em;
  color: var(--wk-gold);
  margin-left: 2px;
}

.stat-label {
  display: block;
  /* 中文可在任意字符间断行，宽度卡在词中间就会拆出「扫描 / 件」这种断法。
     放宽一档并让浏览器均衡两行长度，词组更容易落在同一行 */
  max-width: 26ch;
  /* keep-all 让中文只在顿号、冒号处断行，不再从词中间劈开 */
  word-break: keep-all;
  text-wrap: balance;
  margin: 12px auto 0;
  font-size: 12.5px;
  line-height: 1.7;
  letter-spacing: 0.04em;
  color: var(--wk-ink-mute);
}

/* ------------------------------ 章节通用 ------------------------------ */

.chapter {
  padding: clamp(72px, 9vw, 124px) 0;
}

.chapter-alt {
  background: var(--wk-paper-2);
  border-top: 1px solid var(--wk-rule-soft);
  border-bottom: 1px solid var(--wk-rule-soft);
}

.chapter-head {
  max-width: 780px;
  margin-bottom: 68px;
}

.chapter-title {
  font-family: var(--wk-font-serif);
  font-weight: 500;
  font-size: clamp(27px, 3.2vw, 40px);
  line-height: 1.3;
  letter-spacing: -0.02em;
  margin: 0;
  border: none;
  padding: 0;
}

.chapter-sub {
  margin: 20px 0 0;
  font-size: 15.5px;
  line-height: 1.85;
  color: var(--wk-ink-soft);
}

/* ------------------------------- 主链路 ------------------------------- */

.chain {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  border-top: 1px solid var(--wk-rule);
}

.chain-item {
  border-left: 1px solid var(--wk-rule-soft);
}

.chain-item:first-child {
  border-left: none;
}

.chain-item a {
  display: block;
  height: 100%;
  padding: 32px 28px 40px 0;
  text-decoration: none;
  color: inherit;
  transition: opacity 0.22s ease;
}

.chain-item:not(:first-child) a {
  padding-left: 28px;
}

.chain-item a:hover {
  opacity: 0.62;
}

.chain-head {
  display: flex;
  align-items: center;
  gap: 12px;
}

.chain-icon {
  width: 26px;
  height: 26px;
  flex: none;
  color: var(--wk-ink);
}

.chain-step {
  font-family: var(--wk-font-mono);
  font-size: 11px;
  letter-spacing: 0.18em;
  color: var(--wk-gold);
}

.chain-title {
  font-family: var(--wk-font-serif);
  font-size: 21px;
  font-weight: 500;
  margin: 16px 0 14px;
  letter-spacing: -0.01em;
}

.chain-desc {
  margin: 0;
  font-size: 14px;
  line-height: 1.8;
  color: var(--wk-ink-soft);
}

/* ------------------------------ 特色能力 ------------------------------ */

.features {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  border-top: 1px solid var(--wk-rule);
}

.feature {
  display: block;
  padding: 30px 30px 36px 0;
  border-bottom: 1px solid var(--wk-rule-soft);
  border-left: 1px solid var(--wk-rule-soft);
  text-decoration: none;
  color: inherit;
  transition: opacity 0.22s ease;
}

.feature:nth-child(3n + 1) {
  border-left: none;
}

.feature:not(:nth-child(3n + 1)) {
  padding-left: 30px;
}

.feature:nth-last-child(-n + 3) {
  border-bottom: none;
}

.feature:hover {
  opacity: 0.62;
}

.feature-head {
  display: flex;
  align-items: center;
  gap: 11px;
}

.feature-icon {
  width: 24px;
  height: 24px;
  flex: none;
  color: var(--wk-ink);
}

.feature-tag {
  font-family: var(--wk-font-mono);
  font-size: 11px;
  letter-spacing: 0.16em;
  color: var(--wk-gold);
}

.feature-title {
  font-family: var(--wk-font-serif);
  font-size: 20px;
  font-weight: 500;
  margin: 14px 0 12px;
  letter-spacing: -0.01em;
}

.feature-desc {
  margin: 0;
  font-size: 14px;
  line-height: 1.8;
  color: var(--wk-ink-soft);
}

/* ------------------------------ 触达方式 ------------------------------ */

.surfaces {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 28px 56px;
}

.surface {
  padding-top: 18px;
  border-top: 1px solid var(--wk-rule-soft);
}

.surface-name {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 8px;
  letter-spacing: 0.01em;
}

.surface-icon {
  width: 20px;
  height: 20px;
  flex: none;
  color: var(--wk-ink);
}

.surface-desc {
  margin: 0;
  font-size: 13.5px;
  line-height: 1.75;
  color: var(--wk-ink-soft);
}

.surfaces-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 18px;
  margin-top: 44px;
}

/* ------------------------------ 文档地图 ------------------------------ */

.map {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 72px;
}

.map-block {
  padding: 40px 0;
  border-top: 1px solid var(--wk-rule);
}

.map-head {
  display: grid;
  grid-template-columns: 46px 1fr;
  align-items: baseline;
  row-gap: 10px;
}

.map-index {
  font-family: var(--wk-font-mono);
  font-size: 11px;
  letter-spacing: 0.14em;
  color: var(--wk-gold);
}

.map-title {
  display: flex;
  align-items: center;
  gap: 12px;
  font-family: var(--wk-font-serif);
  font-size: 24px;
  font-weight: 500;
  margin: 0;
  letter-spacing: -0.01em;
}

.map-icon {
  width: 22px;
  height: 22px;
  flex: none;
  color: var(--wk-ink);
}

.map-brief {
  grid-column: 2;
  margin: 0;
  font-size: 13.8px;
  line-height: 1.75;
  color: var(--wk-ink-mute);
}

.map-list {
  list-style: none;
  margin: 26px 0 0 46px;
  padding: 0;
  columns: 1;
}

.map-block-wide {
  grid-column: 1 / -1;
}

.map-block-wide .map-list {
  columns: 3;
  column-gap: 72px;
}

.map-block-wide .map-brief {
  max-width: 52ch;
}

.map-list li {
  border-bottom: 1px solid var(--wk-rule-soft);
}

.map-list a {
  display: block;
  padding: 11px 0;
  font-size: 14.5px;
  color: var(--wk-ink-soft);
  text-decoration: none;
  transition: color 0.18s ease, padding-left 0.18s ease;
}

.map-list a:hover {
  color: var(--wk-ink);
  padding-left: 8px;
}

/* ------------------------------- 部署 ------------------------------- */

.deploy {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.05fr);
  gap: 72px;
  align-items: start;
}

.deploy-list {
  list-style: none;
  margin: 38px 0;
  padding: 0;
  border-top: 1px solid var(--wk-rule-soft);
}

.deploy-list li {
  display: grid;
  /* 名称列要装下图标 + 「Lite（单二进制 / 桌面应用）」，比原来放宽一档 */
  grid-template-columns: 178px 1fr;
  gap: 16px;
  padding: 15px 0;
  border-bottom: 1px solid var(--wk-rule-soft);
}

.deploy-name {
  display: grid;
  grid-template-columns: 20px 1fr;
  align-items: start;
  gap: 10px;
  font-size: 14px;
  font-weight: 600;
}

.deploy-icon {
  width: 20px;
  height: 20px;
  margin-top: 1px;
  color: var(--wk-ink);
}

/* 「尚未正式发布」这类限定跟在说明之后。放在名称列会被 150px 的窄列挤断，
   放这里既不断字，也仍然紧贴对应条目。 */
.deploy-note {
  display: inline-block;
  margin-left: 6px;
  padding: 1px 7px;
  border: 1px solid var(--wk-rule);
  border-radius: 2px;
  font-family: var(--wk-font-mono);
  font-size: 10.5px;
  line-height: 1.7;
  letter-spacing: 0.04em;
  white-space: nowrap;
  color: var(--wk-ink-mute);
}

.deploy-desc {
  font-size: 13.8px;
  line-height: 1.7;
  color: var(--wk-ink-mute);
}

.deploy-code {
  border: 1px solid var(--wk-rule);
  border-radius: 4px;
  overflow: hidden;
  background: #0d1626;
}

.code-bar {
  padding: 12px 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  font-family: var(--wk-font-mono);
  font-size: 10.5px;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: rgba(230, 236, 245, 0.45);
}

.deploy-code pre {
  margin: 0;
  padding: 24px 22px 28px;
  overflow-x: auto;
}

.deploy-code code {
  font-family: var(--wk-font-mono);
  font-size: 13px;
  line-height: 2;
  color: #dfe7f2;
}

.deploy-code .c {
  color: rgba(217, 169, 79, 0.75);
}

/* 代码块本身只到「服务起来了」，后续动作与延伸阅读放在下面这行，
   避免读者以为跑完命令就结束了 */
.deploy-code-note {
  margin: 0;
  padding: 16px 22px 20px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
  font-size: 12.5px;
  line-height: 1.9;
  color: rgba(223, 231, 242, 0.62);
}

.deploy-code-note code {
  font-family: var(--wk-font-mono);
  font-size: 12px;
  color: rgba(223, 231, 242, 0.85);
}

.deploy-code-note a {
  color: var(--wk-gold-light);
  text-decoration: none;
  border-bottom: 1px solid rgba(217, 169, 79, 0.4);
}

.deploy-code-note a:hover {
  border-bottom-color: var(--wk-gold-light);
}

/* ------------------------------- 结尾 ------------------------------- */

.closing {
  border-top: 1px solid var(--wk-rule-soft);
  background: var(--wk-paper-2);
  padding: 70px 0 60px;
}

.closing-inner {
  display: grid;
  gap: 26px;
  justify-items: center;
  text-align: center;
}

.closing-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  font-family: var(--wk-font-sans);
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.18em;
  text-transform: uppercase;
}

.closing-note {
  max-width: 58ch;
  margin: 0;
  font-size: 13.5px;
  line-height: 1.9;
  color: var(--wk-ink-mute);
}

.closing-note code {
  font-family: var(--wk-font-mono);
  font-size: 0.9em;
}

.closing-links {
  display: flex;
  flex-wrap: wrap;
  gap: 28px;
}

.closing-links a {
  font-size: 13.5px;
  color: var(--wk-ink-soft);
  text-decoration: none;
  border-bottom: 1px solid transparent;
  padding-bottom: 2px;
  transition: all 0.2s ease;
}

.closing-links a:hover {
  color: var(--wk-ink);
  border-bottom-color: var(--wk-gold);
}

.closing-copy {
  margin: 0;
  font-family: var(--wk-font-mono);
  font-size: 11px;
  letter-spacing: 0.1em;
  color: var(--wk-ink-mute);
}

/* ------------------------------ 响应式 ------------------------------ */

@media (max-width: 1080px) {
  .hero-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 56px;
  }
  .map-block-wide .map-list {
    columns: 2;
    column-gap: 48px;
  }
  .chain {
    grid-template-columns: repeat(2, 1fr);
  }
  .chain-item:nth-child(odd) {
    border-left: none;
  }
  .chain-item:nth-child(n + 3) {
    border-top: 1px solid var(--wk-rule-soft);
  }
  .chain-item a,
  .chain-item:not(:first-child) a {
    padding-left: 0;
  }
  .chain-item:nth-child(even) a {
    padding-left: 28px;
  }
  .features {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .feature,
  .feature:not(:nth-child(3n + 1)) {
    padding-left: 0;
    border-left: none;
  }
  .feature:nth-child(even) {
    padding-left: 30px;
    border-left: 1px solid var(--wk-rule-soft);
  }
  .feature:nth-last-child(-n + 3) {
    border-bottom: 1px solid var(--wk-rule-soft);
  }
  .feature:nth-last-child(-n + 2) {
    border-bottom: none;
  }
  .surfaces {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 24px 40px;
  }
  .map {
    grid-template-columns: minmax(0, 1fr);
    gap: 0;
  }
  .deploy {
    grid-template-columns: minmax(0, 1fr);
    gap: 48px;
  }
  .stats-row {
    grid-template-columns: repeat(3, 1fr);
  }
  .stat:nth-child(3n + 1) {
    border-left: none;
  }
  .stat:nth-child(n + 4) {
    border-top: 1px solid var(--wk-rule-soft);
  }
}

@media (max-width: 640px) {
  .shell {
    padding: 0 22px;
  }
  .display br {
    display: none;
  }
  .chain {
    grid-template-columns: minmax(0, 1fr);
  }
  .chain-item {
    border-left: none;
  }
  .chain-item:not(:first-child) {
    border-top: 1px solid var(--wk-rule-soft);
  }
  .chain-item:nth-child(even) a {
    padding-left: 0;
  }
  .surfaces {
    grid-template-columns: minmax(0, 1fr);
    gap: 20px;
  }
  .features {
    grid-template-columns: minmax(0, 1fr);
  }
  .feature,
  .feature:nth-child(even) {
    padding-left: 0;
    border-left: none;
    border-bottom: 1px solid var(--wk-rule-soft);
  }
  .feature:last-child {
    border-bottom: none;
  }
  .stats-row {
    grid-template-columns: repeat(2, 1fr);
  }
  .stat {
    border-left: 1px solid var(--wk-rule-soft);
  }
  .stat:nth-child(odd) {
    border-left: none;
  }
  .stat:nth-child(n + 3) {
    border-top: 1px solid var(--wk-rule-soft);
  }
  .map-block-wide .map-list {
    columns: 1;
  }
  .map-head {
    grid-template-columns: 1fr;
  }
  .map-brief {
    grid-column: 1;
  }
  .map-list {
    margin-left: 0;
  }
  .deploy-list li {
    grid-template-columns: 1fr;
    gap: 6px;
  }
}
</style>
