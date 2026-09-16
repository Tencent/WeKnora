import Image from "next/image";
import Link from "next/link";
import { BrandLogo } from "./brand-logo";
import { Icon } from "./ui";
import { WikiGallery } from "./wiki-gallery";
import { Header, ProductVideo } from "./interactive";
import { clients, dataSources, modelProviders, IntegrationMark } from "./brands";
import s from "./home.module.css";

const repo = "https://github.com/Tencent/WeKnora";
const docs = "/docs/";
const guide = (path: string) => `${docs}${path}.html`;
const modes = [
  { number: "01", icon: "search", label: "RAG", title: "回答有据可查", description: "结合语义与关键词检索查找相关资料，回答附带来源引用，可打开原文核对。", tags: ["混合检索", "多模态解析", "原文引用"], link: "03-features/05-retrieval-engines" },
  { number: "02", icon: "agent", label: "Agent", title: "用知识和工具完成任务", description: "智能体根据任务检索知识库、搜索网页、调用 MCP 工具与技能，并在沙箱中处理文件、运行脚本。", tags: ["多步推理", "工具调用", "技能执行"], link: "03-features/07-agent" },
  { number: "03", icon: "wiki", label: "Wiki", title: "把文档整理成 Wiki", description: "从原始文档生成相互链接的 Wiki 页面与知识图谱，支持浏览、编辑和版本回滚。", tags: ["自动组织", "知识图谱", "版本回滚"], link: "03-features/14-wiki" },
];
const release = [
  { icon: "skills", title: "安装、管理和复用技能", text: "从 ClawHub、SkillHub、Git 或 ZIP 安装技能。在空间内统一管理，查看安装进度，浏览与编辑技能文件。", link: "03-features/22-skills-sandbox", label: "空间技能目录" },
  { icon: "sandbox", title: "在同一沙箱中继续执行任务", text: "支持 Docker、E2B、Cube 三种后端。同一会话中的多轮任务共用一个沙箱工作区，生成的文件可预览与下载。", link: "03-features/22-skills-sandbox", label: "会话级沙箱" },
  { icon: "memory", title: "跨会话保留偏好和任务信息", text: "在后续对话中使用已保存的偏好、事实与任务信息。自动提取的记忆由你确认，也可随时管理或关闭。", link: "03-features/23-memory", label: "长期记忆" },
];

export default function Home() {
  return <div className={s.site}>
    <a className={s.skipLink} href="#main">跳至正文</a>
    <Header />
    <main id="main">
      <section className={`${s.shell} ${s.hero}`} aria-labelledby="hero-title">
        <a href="#release" className={s.releaseLink}><span>v0.8.0</span> 技能、沙箱与长期记忆 <Icon name="arrow" /></a>
        <div className={s.heroGrid}>
          <h1 id="hero-title">帮你找到答案，<br /><em>并将知识付诸实践。</em></h1>
          <div className={s.heroAside}>
            <p className={s.eyebrow}>TENCENT OPEN SOURCE · WEKNORA</p>
            <p className={s.heroDescription}>腾讯开源的企业级知识管理框架。<br />汇集团队资料，用于知识问答、任务执行和 Wiki 整理。</p>
            <div className={s.actions}><a className={s.primary} href={guide("01-getting-started/02-installation")}>开始使用 <Icon name="arrow" /></a><a className={s.secondary} href={repo} target="_blank" rel="noreferrer"><Icon name="github" /> GitHub</a></div>
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
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>02 / INTRODUCING v0.8.0</p><h2 id="release-title">智能体可以运行技能，<br />也能生成文件。</h2></div><a className={s.textLink} href={`${repo}/blob/main/CHANGELOG.md#080---2026-09-03`} target="_blank" rel="noreferrer">查看版本更新 <Icon name="arrow" /></a></div>
        <div className={s.releaseGrid}>
          <figure className={s.artifactFigure}><div className={s.artifactHeading}><span><Icon name="file" /> 在对话中预览生成的文件</span><span>AGENT → ARTIFACT</span></div><div className={s.artifactImage}><Image src="/product/skill-sandbox-chat.png" alt="WeKnora 实际界面：智能体根据知识库生成 Word 文档，并在对话旁打开产物预览" width={3840} height={2112} sizes="(max-width: 960px) 100vw, 60vw" /></div><figcaption><span>检索知识 → 执行任务 → 生成文档</span><span>产品实景</span></figcaption></figure>
          <div className={s.releaseFeatures}>{release.map(feature => <article key={feature.title}><Icon name={feature.icon} /><div><span className={s.featureLabel}>{feature.label}</span><h3>{feature.title}</h3><p>{feature.text}</p><a className={s.textLink} href={guide(feature.link)}>了解更多 <Icon name="arrow" /></a></div></article>)}</div>
        </div>
        <div className={s.releaseExtras}><span>本次更新还包括</span><p>GitLab / 腾讯 IMA 数据源</p><p>anydoc Office 解析</p><p>DeepSeek Harness 插件</p><p>LiteLLM 接入</p></div>
      </div></section>
      <section id="wiki" className={`${s.shell} ${s.section} ${s.wiki}`} aria-labelledby="wiki-title">
        <div className={s.sectionHeading}>
          <div><p className={s.eyebrow}>03 / AUTOMATIC WIKI</p><h2 id="wiki-title">把文档整理成可浏览的 Wiki。</h2></div>
          <a className={s.textLink} href={guide("03-features/14-wiki")}>了解 Wiki 的使用方式 <Icon name="arrow" /></a>
        </div>
        <WikiGallery />
      </section>
      <section id="ecosystem" className={`${s.shell} ${s.section}`} aria-labelledby="ecosystem-title">
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>04 / INTEGRATIONS</p><h2 id="ecosystem-title">数据源与工具集成</h2></div><p>同步飞书、GitLab 等平台的资料，<br />通过 IM、浏览器插件或 API 查询和使用。</p></div>
        <div className={s.ecosystem}>
          <div className={s.ecosystemColumn}><Icon name="sources" /><h3>导入与同步资料</h3><p>上传文件、导入网页，或连接外部数据源。</p><div className={s.integrations}>{dataSources.map(item => <span key={item.name}><IntegrationMark item={item} /></span>)}</div><a className={s.textLink} href={guide("03-features/10-datasource")}>数据源集成 <Icon name="arrow" /></a></div>
          <div className={s.ecosystemCore}><BrandLogo /><span>团队知识库与智能体</span><div>理解 · 检索 · 推理 · 行动</div></div>
          <div className={s.ecosystemColumn}><Icon name="channels" /><h3>在常用工具中访问</h3><p>支持 IM 问答、浏览器插件和开发工具集成。</p><div className={s.integrations}>{clients.map(item => <span key={item.name}><IntegrationMark item={item} /></span>)}</div><a className={s.textLink} href={guide("03-features/12-im-integration")}>客户端与渠道 <Icon name="arrow" /></a></div>
        </div>
        <div className={s.models}><span>模型由你选择</span>{modelProviders.map(item => <p key={item.name}><IntegrationMark item={item} /></p>)}<a href={guide("03-features/06-models")} aria-label="查看全部模型厂商"><Icon name="arrow" /></a></div>
      </section>
      <section id="enterprise" className={s.enterprise} aria-labelledby="enterprise-title"><div className={s.shell}>
        <div className={s.sectionHeading}><div><p className={s.eyebrow}>05 / BUILT FOR YOUR TEAM</p><h2 id="enterprise-title">私有化部署，<br />按团队需要管理权限。</h2></div><p>配置数据存储与成员权限，<br />查看操作记录和任务运行状态。</p></div>
        <div className={s.enterpriseGrid}><article><Icon name="server" /><h3>部署与存储</h3><p>支持 Docker、Kubernetes 与 Helm。模型、向量数据库和存储后端可按需替换，支持本地推理。</p></article><article><Icon name="shield" /><h3>空间与资源权限</h3><p>多空间隔离与四级角色矩阵。API Key 按能力和知识库限定范围，支持 OIDC 身份集成。</p></article><article><Icon name="trace" /><h3>审计与运行监控</h3><p>空间审计日志、运行时任务队列面板与 Langfuse 追踪，帮助团队定位问题、管理运行状态。</p></article></div>
      </div></section>
      <section className={`${s.shell} ${s.closing}`} aria-labelledby="closing-title"><div><p className={s.eyebrow}>GET STARTED</p><h2 id="closing-title">搭建团队自己的知识库。</h2></div><a className={s.primary} href={guide("01-getting-started/02-installation")}>部署 WeKnora <Icon name="arrow" /></a></section>
    </main>
    <footer className={`${s.shell} ${s.footer}`}><Link className={s.brand} href="/" aria-label="WeKnora 首页"><BrandLogo /></Link><p>Tencent Open Source · MIT License</p><nav aria-label="页脚导航"><a href={docs}>文档</a><a href={repo} target="_blank" rel="noreferrer">GitHub <Icon name="external" /></a><a href={`${repo}/blob/main/CHANGELOG.md`} target="_blank" rel="noreferrer">更新日志</a></nav></footer>
  </div>;
}
