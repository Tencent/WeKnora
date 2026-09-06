/**
 * 内网定制入口。
 * 想隐藏/恢复功能、改品牌名或换 logo，只改本文件即可，无需改动组件代码。
 * 全部为前端配置，不涉及后端。
 */

// ---------- 品牌 ----------
export const BRANDING = {
  /** 品牌名称：登录页 / 侧边栏 logo 右侧文字，以及「新对话」欢迎语 */
  name: '灵枢·知识中台',
  /** 品牌名第一个字符的高亮色（例如「灵枢·知识中台」的首字「灵」显示为该色） */
  accentColor: '#e60012',
  /** 品牌名高亮前缀的字符数（2 = 高亮「灵枢」二字，其余字正常色） */
  accentLength: 2,
  /** 品牌 logo 文件名（放在 src/assets/img/ 目录下，构建时按名字匹配） */
  logoFile: 'smee.png',
  /** 登录页单独使用的 logo 文件名（可选；不填则跟随 logoFile） */
  loginLogoFile: 'smee_w.png',
} as const

/** 品牌名高亮前缀（默认首个字符；按 BRANDING.accentLength 截取） */
export const BRAND_ACCENT_LETTER: string = BRANDING.name.slice(0, BRANDING.accentLength)
/** 品牌名高亮前缀之后的其余部分（普通颜色展示） */
export const BRAND_NAME_TAIL: string = BRANDING.name.slice(BRANDING.accentLength)

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
  /** 登录页右上角是否显示「官方网站」「GitHub」外链（默认隐藏） */
  showLoginHeaderLinks: false,
  /** 是否显示指向原版 WeKnora GitHub 的文档/反馈等外链（默认隐藏；true 时恢复显示并可点击） */
  showGithubLinks: false,
} as const

// ---------- 登录页文案 ----------
/** 登录页两处固定文案（默认已去掉旧品牌名 WeKnora）。想改字直接改这里。 */
export const AUTH_TEXT = {
  /** 登录表单下方「首次使用？」 */
  firstTime: '首次使用？',
  /** 注册卡片副标题「创建账户并开始使用」 */
  registerSubtitle: '创建账户并开始使用',
} as const

// ---------- 新手引导 ----------
/** 新手引导里的品牌名（第一页「欢迎使用 X」/ 第二页「X 会自动解析…」），默认跟随 BRANDING.name */
export const GUIDE_BRAND_NAME: string = BRANDING.name

// ---------- logo 构建期解析 ----------
// assets/img 下的图片经 Vite 打进产物后按文件名取 logo，
const brandLogos = import.meta.glob('@/assets/img/*.{png,jpg,jpeg,svg,ico}', {
  eager: true,
  import: 'default',
}) as Record<string, string>

export const BRAND_LOGO_SRC: string =
  brandLogos[`/src/assets/img/${BRANDING.logoFile}`] ?? brandLogos['/src/assets/img/smee.png']

/** 登录页专用 logo（默认跟随 BRANDING.logoFile，可用 loginLogoFile 单独指定） */
export const LOGIN_LOGO_SRC: string =
  brandLogos[`/src/assets/img/${BRANDING.loginLogoFile ?? BRANDING.logoFile}`] ?? BRAND_LOGO_SRC
  