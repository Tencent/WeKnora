/** 一条产品线（本期两条产线共用同一个智能体，切换只影响界面状态）。 */
export interface ProductLine {
  /** 产线标识，用于持久化与去重 */
  id: string
  /** 产线显示名 */
  name: string
  /** 一句话说明 */
  description: string
  /** 提问时使用的智能体 id */
  agentId: string
  /** 示例问题，点击后直接填入输入框并发送 */
  samples: string[]
}

/** 品牌配置。所有字段都有内置默认值，运行时配置只覆盖需要的部分。 */
export interface BrandConfig {
  productName: string
  logoUrl: string
  /** logo 显示宽度（px），高度按原图比例缩放。
   *  仅作用于登录页 logo；侧栏 logo 固定 108px（.logo 类，与管理员一致），不受此值影响。 */
  logoWidth: number
  faviconUrl: string
  /** 主色，只接受 #RGB / #RRGGBB */
  primaryColor: string
  /** 可点击按钮等需要白字对比度的深一档颜色 */
  actionColor: string
  loginSubtitle: string
  chatDisclaimer: string
  productLines: ProductLine[]
}

/**
 * window.__RUNTIME_CONFIG__ 里与品牌相关的部分。
 * 这里用 unknown，具体校验交给 runtime.ts，避免把坏配置写进类型。
 */
export interface RuntimeBrandConfig {
  BRAND?: unknown
}

/**
 * window 与运行时品牌配置交叉后的类型。
 * 全局 window 的类型已在 src/utils/index.ts 声明过，
 * 这里用交叉类型补充 BRAND 字段，避免重复声明同一属性造成类型冲突。
 */
export type WindowWithBrandConfig = Window & {
  __RUNTIME_CONFIG__?: RuntimeBrandConfig
}
