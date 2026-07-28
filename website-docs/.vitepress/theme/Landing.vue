<script setup lang="ts">
const stats = [
  { value: '13', unit: '', label: '种文档解析器' },
  { value: '4', unit: '', label: '路索引并行' },
  { value: '24', unit: '', label: '个 Agent 工具' },
  { value: '12', unit: '+', label: '种数据源连接' },
  { value: '27', unit: '', label: '家模型厂商' },
]

const schema = [
  { name: '接入', items: ['文件上传', 'URL 抓取', '飞书', 'Notion', '语雀', 'RSS'] },
  { name: '理解', items: ['版式分析', '扫描件 OCR', '表格抽取', '图片描述', '音频转写'] },
  { name: '索引', items: ['自适应分块', '向量', '关键词', 'Wiki', '知识图谱'] },
  { name: '应用', items: ['知识问答', '智能推理', 'Wiki 站点', '数据分析', 'FAQ'] },
]

const chain = [
  {
    step: '01',
    title: '文档理解',
    desc: '独立的 docreader 服务承接 PDF、Office、网页、图片与音频：版式分析、扫描件 OCR、表格抽取，视觉模型描述插图，语音模型转写录音。',
    href: '/03-features/03-document-parsing',
  },
  {
    step: '02',
    title: '知识索引',
    desc: '自适应分块在标题、启发式与递归策略间取舍，父子分块保留上下文；同一份知识可同时进入向量、关键词、Wiki 与知识图谱四路索引。',
    href: '/02-architecture/03-document-pipeline',
  },
  {
    step: '03',
    title: '混合检索',
    desc: '先做意图识别与查询改写，再让向量与 BM25 并行召回，RRF 融合后交给 Rerank 重排；开启图谱时补充实体关系证据。',
    href: '/03-features/05-retrieval-engines',
  },
  {
    step: '04',
    title: '回答与推理',
    desc: '快速问答走经典 RAG 管线直出答案；智能推理进入 ReAct 循环，自行决定检索、读页、跑 SQL 还是调用外部工具，流式返回并逐句标注引用。',
    href: '/03-features/07-agent',
  },
]

const surfaces = [
  { name: 'Web 控制台', desc: '知识库管理、对话、Wiki 浏览与系统配置的完整界面。' },
  { name: 'Chrome 插件', desc: '任意网页侧边栏提问，一键剪藏正文，Markdown 速记直接入库。' },
  { name: '网页嵌入挂件', desc: '一段 script 就能给自家站点加上悬浮问答，访客无需登录。' },
  { name: '桌面客户端', desc: 'Wails 打包的单机版，进程内自带后端与 SQLite，双击即用。' },
  { name: 'IM 机器人', desc: '企业微信、飞书、钉钉、Slack 等十个平台，在聊天里直接问知识库。' },
  { name: '微信小程序', desc: '移动端轻量入口，随手把网页收进知识库并发起提问。' },
  { name: '命令行 weknora', desc: '终端里管理文档、跑检索、做带引用的流式问答，默认 JSON 输出。' },
  { name: 'REST API 与 Go SDK', desc: '全量 /api/v1 接口，API Key 可按权限级别与知识库范围授权。' },
  { name: 'MCP Server', desc: '把 WeKnora 反向暴露为工具，让 Claude、Cursor 等智能体检索你的知识。' },
]

const features = [
  {
    title: 'Wiki 自动成书',
    desc: '让 LLM 把整个知识库改写成有层级的百科站点：自动分类、生成页面与互链，读者可就地提 issue，修复员 Agent 负责回改。',
    href: '/03-features/14-wiki',
    tag: '知识组织',
  },
  {
    title: '智能推理 Agent',
    desc: 'ReAct 多步推理搭配 24 个内置工具，内置快速问答、智能推理、数据分析、Wiki 研究员等预设，也可自定义提示词与工具集。',
    href: '/03-features/07-agent',
    tag: '推理',
  },
  {
    title: '知识图谱增强',
    desc: '从分块中抽取实体与带强度的关系存入 Neo4j，检索时以图谱补齐跨文档的间接线索，即 GraphRAG。',
    href: '/03-features/09-knowledge-graph',
    tag: '检索增强',
  },
  {
    title: '工具与技能',
    desc: 'MCP 三种传输接入外部工具并支持 OAuth 授权；Agent Skills 在一次性 Docker 沙箱里跑脚本；WeKnora 自身也能反向暴露为 MCP Server。',
    href: '/03-features/08-mcp',
    tag: '扩展',
  },
  {
    title: '数据源常态同步',
    desc: '飞书、Notion、语雀、Confluence、GitHub、RSS 等十余种连接器，按 Cron 增量同步，凭据 AES-256 落盘加密。',
    href: '/03-features/10-datasource',
    tag: '接入',
  },
  {
    title: '私有部署与协作',
    desc: '全栈可私有化：多租户隔离、四级 RBAC、审计日志、OIDC 单点登录，组织维度支持跨租户共享知识库与 Agent。',
    href: '/03-features/01-tenant-auth',
    tag: '安全',
  },
]

const map = [
  {
    index: '01',
    title: '快速开始',
    brief: '四篇读完，即可完成部署并跑通第一次问答。',
    items: [
      { text: '产品介绍', link: '/01-getting-started/01-introduction' },
      { text: '安装部署', link: '/01-getting-started/02-installation' },
      { text: '快速上手', link: '/01-getting-started/03-quickstart' },
      { text: '配置详解', link: '/01-getting-started/04-configuration' },
    ],
  },
  {
    index: '02',
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
    title: '功能模块',
    brief: '十七项能力的配置项、实现路径与源码位置。',
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
    ],
  },
  {
    index: '04',
    title: 'API 参考',
    brief: '以 router.go 为事实来源，每个端点含权限、参数与 curl 示例。',
    items: [
      { text: 'API 总览', link: '/04-api/01-api-overview' },
      { text: 'Agent、MCP 与技能', link: '/04-api/02-api-agent-mcp' },
      { text: '认证与用户', link: '/04-api/02-api-auth' },
      { text: 'IM、Embed 与文件', link: '/04-api/02-api-channels' },
      { text: '会话、消息与聊天', link: '/04-api/02-api-chat' },
      { text: 'FAQ 与 Wiki', link: '/04-api/02-api-faq-wiki' },
      { text: '基础设施与数据源', link: '/04-api/02-api-infra' },
      { text: '知识库与知识', link: '/04-api/02-api-knowledge' },
      { text: '模型、初始化与系统', link: '/04-api/02-api-model-system' },
      { text: '组织与共享', link: '/04-api/02-api-org' },
      { text: '租户与成员', link: '/04-api/02-api-tenant' },
    ],
  },
  {
    index: '05',
    title: '客户端',
    brief: '五种接入形态，从浏览器到命令行、SDK 与桌面端。',
    items: [
      { text: 'Web 前端', link: '/05-clients/01-frontend' },
      { text: '命令行工具 CLI', link: '/05-clients/02-cli' },
      { text: 'Go SDK', link: '/05-clients/03-go-sdk' },
      { text: '微信小程序', link: '/05-clients/04-miniprogram' },
      { text: '桌面客户端', link: '/05-clients/05-desktop' },
    ],
  },
  {
    index: '06',
    title: '开发指南',
    brief: '本地环境、数据库迁移，以及九个可插拔扩展点。',
    items: [
      { text: '开发指南', link: '/06-development/01-dev-guide' },
      { text: '数据库与迁移', link: '/06-development/02-database-schema' },
      { text: '扩展点指南', link: '/06-development/03-extension-points' },
    ],
  },
]

const deployments = [
  { name: 'Docker Compose', desc: '标准部署，12 个可选 profile 组合基础设施' },
  { name: 'Helm', desc: 'Kubernetes 集群编排，适用于生产多副本' },
  { name: 'Lite 单二进制', desc: 'SQLite + 内存队列，离线与低资源环境' },
  { name: 'macOS 桌面端', desc: 'Wails 应用内嵌 Lite 后端，开箱即用' },
]
</script>

<template>
  <div class="landing">
    <!-- ============================= Hero ============================= -->
    <section class="hero">
      <div class="shell hero-grid">
        <div class="hero-copy">
          <p class="eyebrow">Tencent Open Source · WeKnora v0.7.1</p>
          <h1 class="display">
            让散落各处的文档，<br />
            成为可以被追问的知识。
          </h1>
          <p class="lede">
            WeKnora（维娜拉）是腾讯开源的一站式知识库与检索增强生成系统。它把 PDF、Word、网页、飞书、Notion
            等非结构化内容，沿「文档理解 → 知识索引 → 混合检索 → 大模型问答 / Agent 推理」的主链路，
            转化为可检索、可问答、可推理的私有知识资产。
          </p>
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

        <aside class="schema" aria-label="能力概览">
          <div v-for="layer in schema" :key="layer.name" class="layer">
            <span class="layer-name">{{ layer.name }}</span>
            <div class="layer-items">
              <span v-for="n in layer.items" :key="n" class="chip">{{ n }}</span>
            </div>
          </div>
          <p class="schema-note">每一层的实现都可替换或按需省略，模型全部支持本地部署。</p>
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
          <span class="marker">主链路</span>
          <h2 class="chapter-title">四个环节，一条从文档到答案的通路</h2>
          <p class="chapter-sub">
            每个环节都是可替换的实现：解析器、分块策略、检索引擎、模型 Provider 与 Agent 工具均以注册表方式接入。
          </p>
        </header>

        <ol class="chain">
          <li v-for="c in chain" :key="c.step" class="chain-item">
            <a :href="c.href">
              <span class="chain-step">{{ c.step }}</span>
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
          <span class="marker">特色能力</span>
          <h2 class="chapter-title">除了问答，知识还能被组织与推理</h2>
          <p class="chapter-sub">
            同一批文档，既可以拿来一问一答，也可以编成一部可导航的百科，或者交给会自己找证据的智能体。
          </p>
        </header>

        <div class="features">
          <a v-for="f in features" :key="f.title" class="feature" :href="f.href">
            <span class="feature-tag">{{ f.tag }}</span>
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
          <span class="marker">触达</span>
          <h2 class="chapter-title">知识要出现在工作发生的地方</h2>
          <p class="chapter-sub">
            同一套知识库与智能体，可以从浏览器、聊天软件、自家网站或终端里被调用，无需为每个入口重复搭建。
          </p>
        </header>

        <div class="surfaces">
          <div v-for="s in surfaces" :key="s.name" class="surface">
            <h3 class="surface-name">{{ s.name }}</h3>
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
          <h2 class="chapter-title">六个部分，四十五篇</h2>
          <p class="chapter-sub">
            全部内容基于仓库源码整理：路径、字段、端点与默认值均可回溯到具体文件。
          </p>
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
              <h3 class="map-title">{{ m.title }}</h3>
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
          <span class="marker">上手</span>
          <h2 class="chapter-title">四种部署形态，同一套能力</h2>
          <p class="chapter-sub">
            标准部署三条命令即可拉起全套服务；若只想在本机试用，Lite 模式不依赖 PostgreSQL 与 Redis。
          </p>
          <ul class="deploy-list">
            <li v-for="d in deployments" :key="d.name">
              <span class="deploy-name">{{ d.name }}</span>
              <span class="deploy-desc">{{ d.desc }}</span>
            </li>
          </ul>
          <a class="btn btn-ghost" href="/01-getting-started/02-installation">查看安装部署</a>
        </div>

        <div class="deploy-code">
          <div class="code-bar">
            <span>shell</span>
          </div>
          <pre><code><span class="c">// 拉起标准部署</span>
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
cp .env.example .env
make start-all

<span class="c">// 浏览器打开 http://localhost，完成初始化向导</span></code></pre>
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
        <p class="closing-note">
          文档基于仓库 v0.7.1 源码整理。源码路径均相对仓库根目录，API 路径默认带
          <code>/api/v1</code> 前缀，配置示例中的密钥均为占位符。
        </p>
        <div class="closing-links">
          <a href="/01-getting-started/01-introduction">快速开始</a>
          <a href="/04-api/01-api-overview">API 总览</a>
          <a href="/06-development/03-extension-points">扩展点</a>
          <a href="https://github.com/Tencent/WeKnora" target="_blank" rel="noreferrer">GitHub</a>
        </div>
        <p class="closing-copy">© Tencent WeKnora · Apache-2.0 License</p>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.landing {
  --shell: 1260px;
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

.schema {
  border-top: 1px solid var(--wk-rule);
  padding-top: 4px;
}

.layer {
  display: grid;
  grid-template-columns: 68px 1fr;
  gap: 18px;
  align-items: start;
  padding: 18px 0;
  border-bottom: 1px solid var(--wk-rule-soft);
  position: relative;
}

.layer:not(:last-of-type)::after {
  content: "";
  position: absolute;
  left: 30px;
  bottom: -4px;
  width: 7px;
  height: 7px;
  border-right: 1px solid var(--wk-gold);
  border-bottom: 1px solid var(--wk-gold);
  transform: rotate(45deg);
  opacity: 0.5;
}

.layer-name {
  font-family: var(--wk-font-mono);
  font-size: 10.5px;
  letter-spacing: 0.14em;
  color: var(--wk-ink-mute);
  padding-top: 5px;
}

.layer-items {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
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
  margin: 16px 0 0;
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
  grid-template-columns: repeat(5, 1fr);
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
  margin-top: 12px;
  font-size: 12.5px;
  letter-spacing: 0.06em;
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
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 8px;
  letter-spacing: 0.01em;
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
  font-family: var(--wk-font-serif);
  font-size: 24px;
  font-weight: 500;
  margin: 0;
  letter-spacing: -0.01em;
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
  grid-template-columns: 150px 1fr;
  gap: 16px;
  padding: 15px 0;
  border-bottom: 1px solid var(--wk-rule-soft);
}

.deploy-name {
  font-size: 14px;
  font-weight: 600;
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
