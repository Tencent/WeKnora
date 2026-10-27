import { brandCssVars, deriveActionColor, isValidHex } from './colors'
import type { BrandMode } from './colors'
import { DEFAULT_BRAND } from './defaults'
import type { BrandConfig, ProductLine, WindowWithBrandConfig } from './types'

function warn(message: string): void {
  console.warn(`[brand] ${message}`)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim() !== ''
}

/** 把配置值转成可读文本，避免对象打印成 [object Object]。 */
function describe(value: unknown): string {
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return String(value)
  }
}

function cloneLines(lines: ProductLine[]): ProductLine[] {
  return lines.map((line) => ({ ...line, samples: [...line.samples] }))
}

function cloneBrand(config: BrandConfig): BrandConfig {
  return { ...config, productLines: cloneLines(config.productLines) }
}

function pickString(raw: Record<string, unknown>, key: keyof BrandConfig, fallback: string): string {
  const value = raw[key]
  if (value === undefined || value === null) {
    warn(`BRAND.${key} 缺失，使用默认值「${fallback}」`)
    return fallback
  }
  if (!isNonEmptyString(value)) {
    warn(`BRAND.${key} 不是非空字符串（收到 ${describe(value)}），使用默认值「${fallback}」`)
    return fallback
  }
  return value.trim()
}

function pickLogoWidth(raw: Record<string, unknown>, fallback: number): number {
  const value = raw.logoWidth
  if (value === undefined || value === null) {
    warn(`BRAND.logoWidth 缺失，使用默认宽度 ${fallback}px`)
    return fallback
  }
  const width =
    typeof value === 'number'
      ? value
      : typeof value === 'string' && value.trim() !== ''
        ? Number(value)
        : Number.NaN
  if (!Number.isFinite(width) || width <= 0) {
    warn(`BRAND.logoWidth 必须是大于 0 的数字（收到 ${describe(value)}），使用默认宽度 ${fallback}px`)
    return fallback
  }
  return Math.round(width)
}

function pickColor(raw: Record<string, unknown>, key: keyof BrandConfig, fallback: string): string {
  const value = raw[key]
  if (value === undefined || value === null) {
    warn(`BRAND.${key} 缺失，使用默认颜色 ${fallback}`)
    return fallback
  }
  if (!isValidHex(value)) {
    warn(`BRAND.${key} 必须是 #RGB 或 #RRGGBB 格式（收到 ${describe(value)}），使用默认颜色 ${fallback}`)
    return fallback
  }
  return value.trim()
}

/**
 * 交互色是可选项：没配就由主色自动派生（H3），配了非法值则告警后派生。
 * 派生函数会保证白字对比度，默认主色得到 #0284C7，与改造前一致。
 */
function pickActionColor(raw: Record<string, unknown>, primaryColor: string): string {
  const value = raw.actionColor
  // 没配置是正常情况：默认按主色派生，不告警，避免每次启动都刷一条“配置缺失”。
  if (value === undefined || value === null) {
    return deriveActionColor(primaryColor)
  }
  if (!isValidHex(value)) {
    warn(`BRAND.actionColor 必须是 #RGB 或 #RRGGBB 格式（收到 ${describe(value)}），按 BRAND.primaryColor 自动派生`)
    return deriveActionColor(primaryColor)
  }
  return value.trim()
}

/** 校验并整理一条产线；缺 id/name/agentId 的整条丢弃。 */
function normalizeProductLine(value: unknown, index: number): ProductLine | null {
  if (!isRecord(value)) {
    warn(`BRAND.productLines[${index}] 不是对象（收到 ${describe(value)}），已丢弃`)
    return null
  }

  const missing = (['id', 'name', 'agentId'] as const).filter((key) => !isNonEmptyString(value[key]))
  if (missing.length > 0) {
    warn(`BRAND.productLines[${index}] 缺少可用字段 ${missing.join('/')}，已丢弃`)
    return null
  }

  let description = ''
  if (isNonEmptyString(value.description)) {
    description = value.description.trim()
  } else if (value.description !== undefined && value.description !== null) {
    warn(`BRAND.productLines[${index}].description 不是非空字符串，按空说明处理`)
  }

  let samples: string[] = []
  if (Array.isArray(value.samples)) {
    const cleaned = value.samples.filter(isNonEmptyString).map((sample) => sample.trim())
    // 同一产线里重复的示例问题去掉，避免界面出现两遍同样的按钮（L-13）。
    samples = Array.from(new Set(cleaned))
    if (samples.length !== value.samples.length) {
      warn(`BRAND.productLines[${index}].samples 中有空值、重复项或非字符串项，已忽略`)
    }
  } else if (value.samples !== undefined && value.samples !== null) {
    warn(`BRAND.productLines[${index}].samples 不是数组，按空示例问题处理`)
  }

  return {
    id: (value.id as string).trim(),
    name: (value.name as string).trim(),
    description,
    agentId: (value.agentId as string).trim(),
    samples,
  }
}

/**
 * 最近一次 readBrandConfig 是否因为产线配置整段不可用而回落到默认两条。
 * 用模块级变量而不是改返回值，是为了不破坏 readBrandConfig 的既有签名和调用方。
 */
let lastProductLinesFallback = false

function pickProductLines(raw: Record<string, unknown>): ProductLine[] {
  const value = raw.productLines
  if (value === undefined || value === null) {
    warn('BRAND.productLines 缺失，使用默认产线')
    lastProductLinesFallback = true
    return cloneLines(DEFAULT_BRAND.productLines)
  }
  if (!Array.isArray(value)) {
    warn(`BRAND.productLines 不是数组（收到 ${describe(value)}），使用默认产线`)
    lastProductLinesFallback = true
    return cloneLines(DEFAULT_BRAND.productLines)
  }

  // 按 id 去重：重复 id 会产生重复 Vue key / DOM id，第二条产线永远选不中（L-13）。
  const seenIds = new Set<string>()
  const lines = value
    .map((line, index) => normalizeProductLine(line, index))
    .filter((line): line is ProductLine => {
      if (line === null) return false
      if (seenIds.has(line.id)) {
        warn(`BRAND.productLines 中 id「${line.id}」重复，已忽略后出现的配置`)
        return false
      }
      seenIds.add(line.id)
      return true
    })

  if (lines.length === 0) {
    warn('BRAND.productLines 没有可用产线，使用默认产线')
    lastProductLinesFallback = true
    return cloneLines(DEFAULT_BRAND.productLines)
  }
  lastProductLinesFallback = false
  return lines
}

/**
 * 读取并校验一份品牌原始配置。
 * 整体缺失、字段缺失、字段非法、产线不可用时都回落到默认值，不抛错。
 * 这个函数不依赖 window，方便单元测试直接传对象。
 */
export function readBrandConfig(raw: unknown): BrandConfig {
  if (!isRecord(raw)) {
    warn('未找到 window.__RUNTIME_CONFIG__.BRAND，使用内置默认品牌')
    return cloneBrand(DEFAULT_BRAND)
  }

  // 交互色依赖主色，所以先定主色再派生/覆盖 actionColor。
  const primaryColor = pickColor(raw, 'primaryColor', DEFAULT_BRAND.primaryColor)

  return {
    productName: pickString(raw, 'productName', DEFAULT_BRAND.productName),
    logoUrl: pickString(raw, 'logoUrl', DEFAULT_BRAND.logoUrl),
    logoWidth: pickLogoWidth(raw, DEFAULT_BRAND.logoWidth),
    faviconUrl: pickString(raw, 'faviconUrl', DEFAULT_BRAND.faviconUrl),
    primaryColor,
    actionColor: pickActionColor(raw, primaryColor),
    loginSubtitle: pickString(raw, 'loginSubtitle', DEFAULT_BRAND.loginSubtitle),
    chatDisclaimer: pickString(raw, 'chatDisclaimer', DEFAULT_BRAND.chatDisclaimer),
    productLines: pickProductLines(raw),
  }
}

function readRawBrandFromWindow(): unknown {
  if (typeof window === 'undefined') return undefined
  return (window as WindowWithBrandConfig).__RUNTIME_CONFIG__?.BRAND
}

/** 运行时只读，防止调用方改到单例后影响其他页面。 */
function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    Object.values(value as unknown as Record<string, unknown>).forEach(deepFreeze)
    Object.freeze(value)
  }
  return value
}

/** 启动时读取一次的品牌配置单例。 */
const startupRawBrand = readRawBrandFromWindow()
export const brandConfig: Readonly<BrandConfig> = deepFreeze(readBrandConfig(startupRawBrand))

/**
 * 启动时产线配置是否整段回退到了默认两条。
 * 只反映启动那一次读取（欢迎区据此显示可读提示）；后续测试再调 readBrandConfig 不会改到这里。
 */
export const productLinesFallback: boolean = lastProductLinesFallback

/**
 * 浅色、深色各算一份品牌变量。
 * 配置只在启动时读取一次，两份结果提前算好，主题切换时直接套用，不重复计算。
 */
const brandVarsByMode: Record<BrandMode, Record<string, string>> = {
  light: brandCssVars(brandConfig, 'light'),
  dark: brandCssVars(brandConfig, 'dark'),
}

/** 当前生效的深浅模式以 theme.css 读取的 html[theme-mode] 为准。 */
function effectiveThemeMode(): BrandMode {
  return document.documentElement.getAttribute('theme-mode') === 'dark' ? 'dark' : 'light'
}

function writeBrandCssVars(mode: BrandMode): void {
  const style = document.documentElement.style
  for (const [name, value] of Object.entries(brandVarsByMode[mode])) {
    style.setProperty(name, value)
  }
}

function removeBrandCssVars(): void {
  const style = document.documentElement.style
  for (const name of Object.keys(brandVarsByMode.light)) {
    style.removeProperty(name)
  }
}

/**
 * 客户版主题开关，供登录页（T03）和登录后的角色判定（T04）调用。
 * 开：挂 data-customer，按当前深浅写入品牌变量；
 * 关：移除标记并清掉内联变量，管理员端继续用 theme.css 的绿色。
 *
 * 变量写在 documentElement 的内联样式上（优先级最高），所以关闭时必须显式清除，
 * 否则管理员端会残留天蓝。
 */
export function applyCustomerTheme(enabled: boolean): void {
  if (typeof document === 'undefined') return

  if (enabled) {
    document.documentElement.dataset.customer = 'true'
    writeBrandCssVars(effectiveThemeMode())
  } else {
    document.documentElement.removeAttribute('data-customer')
    removeBrandCssVars()
  }
}

let themeObserverInstalled = false

/**
 * 启动时调用一次：监听 theme-mode 与 data-customer 变化，
 * 客户版开着时重新写入对应模式的变量，关闭时清掉。
 * 这里只跟随 useTheme / 调用方已经维护的属性，不另建主题系统。
 */
export function initCustomerBrandTheme(): void {
  if (typeof document === 'undefined' || themeObserverInstalled) return
  themeObserverInstalled = true

  // 页面在 main.ts 执行前就带上 data-customer 时（例如服务端注入），先补一次。
  if (document.documentElement.dataset.customer === 'true') {
    writeBrandCssVars(effectiveThemeMode())
  }

  new MutationObserver(() => {
    if (document.documentElement.dataset.customer === 'true') {
      writeBrandCssVars(effectiveThemeMode())
    } else {
      // 外部直接移除 data-customer 时也要清掉内联变量，避免管理员端残留天蓝。
      removeBrandCssVars()
    }
  }).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['theme-mode', 'data-customer'],
  })
}

export interface ApplyDocumentBrandOptions {
  /** 客户版或登录页传 true；管理员端保持默认，不设置该属性 */
  customer?: boolean
  /** 是否覆盖浏览器标题；管理员端传 false 保持 WeKnora 默认标题 */
  title?: boolean
}

function applyFavicon(url: string): void {
  let link = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.href = url
}

/**
 * 把品牌应用到文档：标题、favicon、客户版标记与品牌色变量。
 * 颜色变量由 applyCustomerTheme 统一写入，关闭客户版时会一并清除。
 */
export function applyDocumentBrand(options: ApplyDocumentBrandOptions = {}): void {
  if (typeof document === 'undefined') return

  applyCustomerTheme(options.customer === true)

  if (options.title !== false) {
    document.title = brandConfig.productName
  }
  applyFavicon(brandConfig.faviconUrl)
}
