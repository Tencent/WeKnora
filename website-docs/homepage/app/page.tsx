import { existsSync } from "node:fs";
import { join } from "node:path";
import Image from "next/image";
import { BrandLogo } from "./brand-logo";
import { Icon } from "./ui";
import { homeAssets } from "../../shared/header";
import { ProductGallery, type GalleryShot, type GallerySlide } from "./product-gallery";
import { Header, ProductVideo } from "./interactive";
import { clients, dataSources, modelProviders, IntegrationMark } from "./brands";
import s from "./home.module.css";

const repo = "https://github.com/Tencent/WeKnora";
const docs = "/docs/";
const guide = (path: string) => `${docs}${path}.html`;
const modes = [
  { number: "01", icon: "search", label: "RAG", title: "回答有据可查", description: "结合语义与关键词检索查找相关资料，回答附带来源引用，可打开原文核对。", tags: ["混合检索", "多模态解析", "原文引用"], link: "03-features/05-retrieval-engines" },
  { number: "02", icon: "agent", label: "Agent", title: "用知识和工具完成任务", description: "智能体根据任务检索知识库、搜索网页、调用 MCP 工具与技能，在沙箱中处理文件、运行脚本，并可跨会话记住你确认过的偏好。", tags: ["多步推理", "工具调用", "技能执行", "长期记忆"], link: "03-features/07-agent" },
  { number: "03", icon: "wiki", label: "Wiki", title: "把文档整理成 Wiki", description: "从原始文档生成相互链接的 Wiki 页面与知识图谱，支持浏览、编辑和版本回滚。", tags: ["自动组织", "知识图谱", "版本回滚"], link: "03-features/14-wiki" },
];
const releaseExtras = ["Confluence / 钉钉文档数据源", "27 家模型厂商目录", "博查 / Serply 联网搜索", "日语界面", "仅白名单出站"];

const productShotExists = (src: string) => existsSync(join(process.cwd(), "public", src));
type SlideSource = Omit<GallerySlide, "shots"> & ({ image: string; alt: string } | { shots: Omit<GalleryShot, "available">[] });
const withAvailability = (slide: SlideSource): GallerySlide => {
  const shots = "shots" in slide ? slide.shots : [{ image: slide.image, alt: slide.alt }];
  return { name: slide.name, icon: slide.icon, title: slide.title, description: slide.description, link: slide.link, external: slide.external, shots: shots.map(shot => ({ ...shot, available: productShotExists(`${homeAssets}/product/${shot.image}.png`) })) };
};
const releaseSlides = [
  { name: "本机浏览器", icon: "browser", shots: [
    { label: "执行任务", icon: "browser", image: "local-browser-task", alt: "WeKnora 实际界面：智能推理对话正在操作本机浏览器，对话中显示任务预览与暂停、继续、结束控制" },
    { label: "连接扩展", icon: "plug", image: "browser-connection", alt: "WeKnora 实际界面：工具箱的浏览器连接页，BrowserSkill 扩展已连接并列出智能体可执行的网页操作" },
  ], title: "操作你电脑上的浏览器", description: "借助腾讯开源的 BrowserSkill 扩展，智能体直接在你的 Chrome 或 Edge 中打开网页、填写表单；遇到登录或验证码时交给你。", link: guide("05-clients/09-local-browser"), external: { label: "BrowserSkill", href: "https://github.com/Tencent/BrowserSkill", logo: "browserskill.png" } },
  { name: "MCP Server", icon: "plug", image: "mcp-server-endpoint", title: "把知识库发布给其他 AI 工具", description: "为空间创建 MCP 端点，Claude、Cursor 等客户端连上即可检索知识库和提问。", alt: "WeKnora 实际界面：MCP 端点的连接信息，包含端点地址与 Cursor、Claude Desktop 的 mcpServers 配置", link: guide("03-features/08-mcp") },
  { name: "对话控制", icon: "branch", image: "chat-steer-queue", title: "随时调整进行中的对话", description: "回答中途可以补充要求，也能从任意一次提问分叉或回滚；生成的文件统一收在「产物」页。", alt: "WeKnora 实际界面：回答生成期间，输入框上方排队等待的补充要求", link: guide("03-features/18-chat-experience") },
].map(withAvailability);
const wikiSlides = [
  { name: "知识图谱", icon: "channels", image: "wiki-graph", title: "顺着链接，查看相关知识", description: "在知识图谱中查看页面之间的关系。点击条目，即可阅读相关内容。", alt: "Wiki 知识图谱：日常费用报销条目与相关页面的链接关系，右侧展示条目详情" },
  { name: "页面浏览", icon: "wiki", image: "wiki-browser", title: "按主题整理，保留出处", description: "开启 Wiki 后，从知识库文档中提取人物、产品和概念，生成带来源引用的页面，按目录浏览。", alt: "Wiki 浏览器：按主题组织的目录、年假页面、关联条目和原始文档引用" },
  { name: "版本历史", icon: "history", image: "wiki-revision-history", title: "随时编辑，改动可回溯", description: "直接修订页面，也可让智能体协助维护。查看版本差异，必要时恢复到历史内容。", alt: "Wiki 版本历史：年假页面的历史版本列表、内容差异和回滚入口" },
].map(withAvailability);
const sandboxSlides = [
  { name: "会话级沙箱", icon: "sandbox", image: "skill-sandbox-chat", title: "在同一沙箱中继续执行任务", description: "支持 Docker、E2B、Cube。同一会话的多轮任务共用一个工作区，生成的文件可预览和下载。", alt: "WeKnora 实际界面：智能体根据知识库生成 Word 文档，并在对话旁打开产物预览", link: guide("03-features/22-skills-sandbox") },
  { name: "图形桌面与终端", icon: "monitor", shots: [
    { label: "图形桌面", icon: "monitor", image: "sandbox-desktop", alt: "WeKnora 实际界面：对话右侧沙箱面板的桌面页签，显示沙箱内的 XFCE 图形桌面与文件管理器" },
    { label: "交互终端", icon: "terminal", image: "sandbox-terminal", alt: "WeKnora 实际界面：对话右侧的沙箱终端，列出工作区 output 目录中生成的 Word 文件" },
  ], title: "打开桌面和终端，看清每一步", description: "在对话旁打开图形桌面或交互终端，查看智能体的每一步操作，必要时亲自接手。", link: guide("03-features/22-skills-sandbox") },
  { name: "技能目录", icon: "skills", image: "skill-catalog", title: "安装、管理和复用技能", description: "从 ClawHub、SkillHub、Git 或 ZIP 安装技能，在空间内统一管理和复用。", alt: "WeKnora 实际界面：工具箱的技能管理页，列出 docx、pptx、pdf 技能及其安装到的沙箱", link: guide("03-features/22-skills-sandbox") },
].map(withAvailability);

export default function Home() {
  return <div className={s.site}>
    <a className={s.skipLink} href="#main">跳至正文</a>
    <Header />
    <main id="main">
      <section className={`${s.shell} ${s.hero}`} aria-labelledby="hero-title">
        <a href="#release" className={s.releaseLink}><span>v0.8.2</span> 本机浏览器、MCP Server 与对话分叉 <Icon name="arrow" /></a>
        <div className={s.heroGrid}>
          <h1 id="hero-title">帮你找到答案，<br /><em>并将知识付诸实践。</em></h1>
          <div className={s.heroAside}>
            <p className={s.eyebrow}>TENCENT OPEN SOURCE · WEKNORA</p>
            <p className={s.heroDescription}>腾讯开源的企业级知识管理框架。<br />汇集团队资料，用于知识问答、任务执行和 Wiki 整理。</p>
            <div className={s.actions}><a className={s.primary} href="#get-started">开始使用 <Icon name="arrow" /></a><a className={s.secondary} href={repo} target="_blank" rel="noreferrer"><Icon name="github" /> GitHub</a></div>
            <p className={s.heroNote}>RAG 问答 / Agent 推理 / 自动 Wiki</p>
          </div>
        </div>
        <ProductVideo />
        <div className={s.trustBar}><span><Icon name="github" /> 腾讯开源 · MIT License</span><span><Icon name="server" /> 支持私有化部署</span><span><Icon name="model" /> 自由选择模型与存储</span></div>
      </section>
      <section id="capabilities" className={`${s.shell} ${s.section}`} aria-labelledby="capabilities-title">
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>01 / KNOWLEDGE AT WORK</p><h2 id="capabilities-title">知识问答、任务执行与自动 Wiki</h2></div><p>查询资料用 RAG，处理多步任务用 Agent，<br />整理知识用 Wiki。三种能力共享同一知识库。</p></div>
        <div className={s.modeGrid}>{modes.map(mode => <article className={s.mode} key={mode.label}>
          <div className={s.modeTop}><Icon name={mode.icon} /><span>{mode.number} / {mode.label.toUpperCase()}</span></div>
          <h3>{mode.title}</h3><p>{mode.description}</p><ul className={s.tags}>{mode.tags.map(tag => <li key={tag}>{tag}</li>)}</ul>
          <a className={s.textLink} href={mode.label === "Wiki" ? "#wiki" : guide(mode.link)}>了解 {mode.label} <Icon name="arrow" /></a>
        </article>)}</div>
      </section>
      <section id="release" className={s.release} aria-labelledby="release-title"><div className={s.shell}>
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>02 / INTRODUCING v0.8.2</p><h2 id="release-title">智能体能操作浏览器，<br />知识库能接入其他 AI。</h2></div><a className={s.textLink} href={guide("07-releases/v0.8.2")}>查看版本说明 <Icon name="arrow" /></a></div>
        <ProductGallery id="release-gallery" label="v0.8.2" slides={releaseSlides} />
        <div className={s.releaseExtras}><span>本次更新还包括</span>{releaseExtras.map(item => <p key={item}>{item}</p>)}</div>
      </div></section>
      <section id="skills-sandbox" className={`${s.release} ${s.sandboxTopic}`} aria-labelledby="skills-sandbox-title"><div className={s.shell}>
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>03 / SKILLS &amp; SANDBOX</p><h2 id="skills-sandbox-title">智能体可以运行技能，<br />也能生成文件。</h2></div><a className={s.textLink} href={guide("03-features/22-skills-sandbox")}>了解技能与沙箱 <Icon name="arrow" /></a></div>
        <ProductGallery id="sandbox-gallery" label="技能与沙箱" slides={sandboxSlides} />
      </div></section>
      <section id="wiki" className={`${s.shell} ${s.section} ${s.wiki}`} aria-labelledby="wiki-title">
        <div className={s.sectionHeading}>
          <div><p className={s.eyebrow}>04 / AUTOMATIC WIKI</p><h2 id="wiki-title">把文档整理成可浏览的 Wiki。</h2></div>
          <a className={s.textLink} href={guide("03-features/14-wiki")}>了解 Wiki 的使用方式 <Icon name="arrow" /></a>
        </div>
        <ProductGallery id="wiki-gallery" label="Wiki" slides={wikiSlides} />
      </section>
      <section id="ecosystem" className={`${s.shell} ${s.section}`} aria-labelledby="ecosystem-title">
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>05 / INTEGRATIONS</p><h2 id="ecosystem-title">数据源与工具集成</h2></div><p>同步飞书、Confluence、GitLab 等平台的资料，<br />通过 IM、浏览器插件、MCP 或 API 查询和使用。</p></div>
        <div className={s.ecosystem}>
          <div className={s.ecosystemColumn}><Icon name="sources" /><h3>导入与同步资料</h3><p>上传文件、导入网页，或连接外部数据源。</p><div className={s.integrations}>{dataSources.map(item => <span key={item.name}><IntegrationMark item={item} /></span>)}</div><a className={s.textLink} href={guide("03-features/10-datasource")}>数据源集成 <Icon name="arrow" /></a></div>
          <div className={s.ecosystemCore}><BrandLogo /><span>团队知识库与智能体</span><div>理解 · 检索 · 推理 · 行动</div></div>
          <div className={s.ecosystemColumn}><Icon name="channels" /><h3>在常用工具中访问</h3><p>支持 IM 问答、浏览器插件、MCP 客户端和开发工具集成。</p><div className={s.integrations}>{clients.map(item => <span key={item.name}><IntegrationMark item={item} /></span>)}</div><a className={s.textLink} href={guide("03-features/12-im-integration")}>客户端与渠道 <Icon name="arrow" /></a></div>
        </div>
        <div className={s.models}><span>模型由你选择 · 内置 27 家厂商 <a className={s.textLink} href={guide("03-features/06-models")}>查看全部 <Icon name="arrow" /></a></span>{modelProviders.map(item => <p key={item.name}><IntegrationMark item={item} /></p>)}</div>
      </section>
      <section id="enterprise" className={s.enterprise} aria-labelledby="enterprise-title"><div className={s.shell}>
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>06 / BUILT FOR YOUR TEAM</p><h2 id="enterprise-title">私有化部署，<br />按团队需要管理权限。</h2></div><p>配置数据存储与成员权限，<br />查看操作记录和任务运行状态。</p></div>
        <div className={s.enterpriseGrid}><article><Icon name="server" /><h3>部署与存储</h3><p>支持 Docker、Kubernetes 与 Helm，也提供 Lite 单机版和桌面应用。模型、向量数据库和存储后端可按需替换，支持本地推理。</p></article><article><Icon name="shield" /><h3>空间与资源权限</h3><p>多空间隔离与四级角色矩阵。API Key 按能力和知识库限定范围，支持 OIDC 身份集成，可限制服务只访问白名单域名。</p></article><article><Icon name="trace" /><h3>审计与运行监控</h3><p>空间审计日志、运行时任务队列面板与 Langfuse 追踪，帮助团队定位问题、管理运行状态。</p></article></div>
      </div></section>
      <section id="get-started" className={`${s.shell} ${s.closing}`} aria-labelledby="closing-title">
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>GET STARTED</p><h2 id="closing-title">选择适合你的使用方式。</h2></div></div>
        <div className={s.startGrid}>
          <article className={s.startCard}>
            <div className={s.startLabel}><Image className={s.startBrand} src={`${homeAssets}/brands/wechat-dialog.png`} alt="微信对话开放平台 Logo" width={32} height={32} /><span>在线使用</span></div>
            <h3>微信对话开放平台</h3>
            <p>在线管理知识库，将问答服务接入公众号、小程序等微信场景。</p>
            <a className={s.textLink} href="https://chatbot.weixin.qq.com/login" target="_blank" rel="noreferrer">进入平台 <Icon name="external" /></a>
          </article>
          <article className={s.startCard}>
            <div className={s.startLabel}><Image className={s.startBrand} src={`${homeAssets}/brands/tencent-cloud.ico`} alt="腾讯云 Logo" width={32} height={32} /><span>云端部署</span></div>
            <h3>腾讯云轻量应用服务器</h3>
            <p>通过应用模板部署 WeKnora，在自己的云服务器上运行。</p>
            <a className={s.textLink} href="https://mc.tencent.com/s69nKCVz" target="_blank" rel="noreferrer">前往腾讯云部署 <Icon name="external" /></a>
          </article>
          <article className={s.startCard}>
            <div className={s.startLabel}><BrandLogo /><span>自行部署</span></div>
            <h3>部署到自己的环境</h3>
            <p>使用 Docker 或 Kubernetes 部署，自行配置模型、存储和网络。</p>
            <a className={s.textLink} href={guide("01-getting-started/02-installation")}>查看部署文档 <Icon name="arrow" /></a>
          </article>
        </div>
      </section>
    </main>
    <footer className={`${s.shell} ${s.footer}`}><a className={s.brand} href="/" aria-label="WeKnora 首页"><BrandLogo /></a><p>Tencent Open Source · MIT License</p><nav aria-label="页脚导航"><a href={docs}>文档</a><a href={repo} target="_blank" rel="noreferrer">GitHub <Icon name="external" /></a><a href={`${repo}/blob/main/CHANGELOG.md`} target="_blank" rel="noreferrer">更新日志</a></nav></footer>
  </div>;
}
