/**
 * 内网定制入口。
 * 想隐藏/恢复功能、改品牌名或换 logo，只改本文件即可，无需改动组件代码。
 * 全部为前端配置，不涉及后端。
 */

// ---------- 品牌 ----------
export const BRANDING = {
  /** 品牌名称：登录页 / 侧边栏 logo 右侧文字，以及「新对话」欢迎语 */
  name: 'SAI·灵玑',
  /** 品牌名第一个字符的高亮色（例如「SAI·灵玑」的 S 显示为该色） */
  accentColor: '#e60012',
  /** 品牌 logo 文件名（放在 src/assets/img/ 目录下，构建时按名字匹配） */
  logoFile: 'smee.png',
} as const

/** 品牌名第一个字符（高亮展示） */
export const BRAND_ACCENT_LETTER: string = BRANDING.name.charAt(0)
/** 品牌名其余部分（普通颜色展示） */
export const BRAND_NAME_TAIL: string = BRANDING.name.slice(1)

// ---------- 功能可见性开关 ----------
export const FEATURES = {
  /** 左下角用户菜单是否显示「帮助与文档」「GitHub」 */
  showHelpAndGithubMenu: false,
  /** 「全部设置 → 集成」是否显示「chrome 插件」「claw skill」 */
  showChromeClawIntegrations: false,
  /** 「全部设置 → 模型」是否显示「WeKnora Cloud」（默认隐藏） */
  showWeKnoraCloud: false,
  /** 登录页 logo 是否还原为 GitHub 外链（true=可点击跳转 GitHub，false=纯展示） */
  showLoginGithubLink: false,
} as const

// ---------- logo 构建期解析 ----------
// assets/img 下的图片经 Vite 打进产物后按文件名取 logo，
const brandLogos = import.meta.glob('@/assets/img/*.{png,jpg,jpeg,svg,ico}', {
  eager: true,
  import: 'default',
}) as Record<string, string>

export const BRAND_LOGO_SRC: string =
  brandLogos[`/src/assets/img/${BRANDING.logoFile}`] ?? brandLogos['/src/assets/img/smee.png']
  