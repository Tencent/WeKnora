import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type DefaultTheme } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'
import './theme-config.d.ts'
import { repoVersionLabel } from './version'

const root = resolve(import.meta.dirname, '..')

const sections: { dir: string; label: string }[] = [
  { dir: '01-getting-started', label: '快速开始' },
  { dir: '02-architecture', label: '架构' },
  { dir: '03-features', label: '功能模块' },
  { dir: '04-api', label: 'API 参考' },
  { dir: '05-clients', label: '客户端' },
  { dir: '06-development', label: '开发指南' },
]

/** 侧边栏条目文字：取正文一级标题，去掉冗余前后缀 */
function itemText(dir: string, file: string): string {
  const raw = readFileSync(resolve(root, dir, file), 'utf-8')
  const heading = raw.match(/^#\s+(.+)$/m)?.[1] ?? file.replace(/\.md$/, '')
  return heading
    .replace(/^API 参考[:：]\s*/, '')
    .replace(/\s*[（(][^（()）]*[)）]\s*/g, ' ')
    .replace(/\s{2,}/g, ' ')
    .trim()
}

function itemsOf(dir: string): DefaultTheme.SidebarItem[] {
  return readdirSync(resolve(root, dir))
    .filter((f) => f.endsWith('.md'))
    .sort()
    .map((f) => ({
      text: itemText(dir, f),
      link: `/${dir}/${f.replace(/\.md$/, '')}`,
    }))
}

const sidebar: DefaultTheme.SidebarItem[] = sections.map((s) => ({
  text: s.label,
  collapsed: false,
  items: itemsOf(s.dir),
}))

/** 本地搜索默认按空白分词，中文整段会被当作一个词，这里退化为字粒度切分 */
function tokenize(text: string): string[] {
  const tokens: string[] = []
  for (const part of text.split(/[\s\n\r#%*,=/:;?[\]{}()&+\-!'"$·、，。：；？！（）【】《》…—]+/)) {
    if (!part) continue
    if (/[\u4e00-\u9fa5]/.test(part)) {
      tokens.push(part, ...part.split(''))
    } else {
      tokens.push(part)
    }
  }
  return tokens
}

const repo = 'https://github.com/Tencent/WeKnora'
const site = 'https://weknora.weixin.qq.com'

export default withMermaid(
  defineConfig({
    title: 'WeKnora',
    titleTemplate: ':title · WeKnora 文档',
    description: 'WeKnora（维娜拉）官方文档：部署、配置、功能说明、API 参考与二次开发',
    lang: 'zh-CN',
    base: '/docs/',
    cleanUrls: true,
    lastUpdated: true,
    srcExclude: ['README.md'],
    metaChunk: true,

    head: [
      ['link', { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' }],
      ['meta', { name: 'theme-color', content: '#101f38' }],
      ['meta', { property: 'og:type', content: 'website' }],
      ['meta', { property: 'og:title', content: 'WeKnora 文档' }],
      [
        'meta',
        {
          property: 'og:description',
          content: '把 PDF、Word、网页与飞书 / Notion / 语雀的资料收进知识库，做成答案带出处的问答系统',
        },
      ],
    ],

    markdown: {
      theme: { light: 'github-light', dark: 'github-dark' },
      lineNumbers: false,
      toc: { level: [2, 3] },
      config(md) {
        // 表格外面包一层滚动容器。默认主题把 <table> 本身设成 display:block 来做
        // 横向滚动，副作用是表格按内容收缩——列少的表比正文列窄一截，页面里宽窄不一。
        // 把滚动交给包裹层后，表格可以恢复 display:table + width:100%，统一撑满正文列。
        md.renderer.rules.table_open = () => '<div class="wk-table">\n<table>\n'
        md.renderer.rules.table_close = () => '</table>\n</div>\n'
      },
    },

    themeConfig: {
      logo: { light: '/logo-mark.svg', dark: '/logo-mark-dark.svg', alt: 'WeKnora' },
      siteTitle: 'WeKnora',

      nav: [
        { text: '快速开始', link: '/01-getting-started/01-introduction', activeMatch: '/01-getting-started/' },
        { text: '架构', link: '/02-architecture/01-overview', activeMatch: '/02-architecture/' },
        { text: '功能', link: '/03-features/01-tenant-auth', activeMatch: '/03-features/' },
        { text: 'API', link: '/04-api/01-api-overview', activeMatch: '/04-api/' },
        { text: '客户端', link: '/05-clients/01-frontend', activeMatch: '/05-clients/' },
        { text: '开发', link: '/06-development/01-dev-guide', activeMatch: '/06-development/' },
        { text: '官网', link: site },
      ],

      weknoraVersion: repoVersionLabel,

      sidebar,

      socialLinks: [{ icon: 'github', link: repo }],

      outline: { level: [2, 3], label: '本页目录' },

      docFooter: { prev: '上一篇', next: '下一篇' },
      returnToTopLabel: '回到顶部',
      sidebarMenuLabel: '目录',
      darkModeSwitchLabel: '外观',
      lightModeSwitchTitle: '切换到浅色',
      darkModeSwitchTitle: '切换到深色',

      lastUpdated: {
        text: '最后更新',
        formatOptions: { dateStyle: 'medium', timeStyle: undefined },
      },

      editLink: {
        pattern: `${repo}/edit/main/website-docs/:path`,
        text: '在 GitHub 上编辑此页',
      },

      search: {
        provider: 'local',
        options: {
          translations: {
            button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
            modal: {
              displayDetails: '展开详情',
              resetButtonTitle: '清除',
              backButtonTitle: '返回',
              noResultsText: '没有找到结果',
              footer: {
                selectText: '选择',
                navigateText: '切换',
                closeText: '关闭',
              },
            },
          },
          miniSearch: {
            options: { tokenize },
            searchOptions: {
              fuzzy: 0.2,
              prefix: true,
              boost: { title: 4, text: 2, titles: 1 },
            },
          },
        },
      },

      footer: {
        message: `基于 WeKnora ${repoVersionLabel} 源码整理 · MIT License`,
        copyright: '© Tencent WeKnora',
      },
    },

    mermaid: {
      theme: 'base',
      fontFamily:
        '"PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", ui-sans-serif, sans-serif',
      themeVariables: {
        primaryColor: '#eef1f6',
        primaryTextColor: '#101f38',
        primaryBorderColor: '#9aa8bd',
        lineColor: '#7d8ba1',
        secondaryColor: '#faf7ef',
        tertiaryColor: '#f6f7f9',
        fontSize: '14px',
      },
    },
  }),
)
