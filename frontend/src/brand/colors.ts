import { DEFAULT_BRAND } from './defaults'
import type { BrandConfig } from './types'

/**
 * 品牌色的深浅模式。和 theme.css 里 html[theme-mode] 的取值保持一致，
 * 不额外引入第二套主题概念。
 */
export type BrandMode = 'light' | 'dark'

/** 只接受 #RGB 或 #RRGGBB，与 runtime.ts 读取配置时的口径完全一致。 */
const HEX_PATTERN = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/

/**
 * 校验颜色字面量。
 * runtime.ts 和 colors.ts 共用这一份规则，避免两处校验口径漂移。
 */
export function isValidHex(value: unknown): value is string {
  return typeof value === 'string' && HEX_PATTERN.test(value.trim())
}

/** 把 #RGB 展开成 #RRGGBB；已是 6 位时保留调用方大小写。 */
function normalizeHex(value: string): string {
  const hex = value.trim()
  if (hex.length === 7) return hex
  const r = hex[1]
  const g = hex[2]
  const b = hex[3]
  return `#${r}${r}${g}${g}${b}${b}`
}

function hexToRgb(hex: string): [number, number, number] {
  const safe = normalizeHex(hex)
  return [
    Number.parseInt(safe.slice(1, 3), 16),
    Number.parseInt(safe.slice(3, 5), 16),
    Number.parseInt(safe.slice(5, 7), 16),
  ]
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

interface Hsl {
  h: number
  s: number
  l: number
}

function rgbToHsl(r: number, g: number, b: number): Hsl {
  const red = r / 255
  const green = g / 255
  const blue = b / 255
  const max = Math.max(red, green, blue)
  const min = Math.min(red, green, blue)
  const l = (max + min) / 2

  let h = 0
  let s = 0
  const delta = max - min
  if (delta !== 0) {
    s = l > 0.5 ? delta / (2 - max - min) : delta / (max + min)
    if (max === red) {
      h = (green - blue) / delta + (green < blue ? 6 : 0)
    } else if (max === green) {
      h = (blue - red) / delta + 2
    } else {
      h = (red - green) / delta + 4
    }
    h *= 60
  }

  return { h, s: s * 100, l: l * 100 }
}

function hslToRgb(h: number, s: number, l: number): [number, number, number] {
  const saturation = clamp(s, 0, 100) / 100
  const lightness = clamp(l, 0, 100) / 100
  const hue = ((h % 360) + 360) % 360

  if (saturation === 0) {
    const channel = lightness * 255
    return [channel, channel, channel]
  }

  const q =
    lightness < 0.5
      ? lightness * (1 + saturation)
      : lightness + saturation - lightness * saturation
  const p = 2 * lightness - q

  const channel = (t: number): number => {
    let value = t
    if (value < 0) value += 1
    if (value > 1) value -= 1
    if (value < 1 / 6) return p + (q - p) * 6 * value
    if (value < 1 / 2) return q
    if (value < 2 / 3) return p + (q - p) * (2 / 3 - value) * 6
    return p
  }

  return [
    channel(hue / 360 + 1 / 3) * 255,
    channel(hue / 360) * 255,
    channel(hue / 360 - 1 / 3) * 255,
  ]
}

function toHexChannel(value: number): string {
  return Math.round(clamp(value, 0, 255))
    .toString(16)
    .padStart(2, '0')
}

function rgbToHex(r: number, g: number, b: number): string {
  return `#${toHexChannel(r)}${toHexChannel(g)}${toHexChannel(b)}`
}

function hslToHex(h: number, s: number, l: number): string {
  const [r, g, b] = hslToRgb(h, s, l)
  return rgbToHex(r, g, b)
}

/** 按 weight 把 from 混向 to；weight=0.5 表示各占一半。 */
function mixHex(from: string, to: string, weight: number): string {
  const [r1, g1, b1] = hexToRgb(from)
  const [r2, g2, b2] = hexToRgb(to)
  const w = clamp(weight, 0, 1)
  return rgbToHex(r1 + (r2 - r1) * w, g1 + (g2 - g1) * w, b1 + (b2 - b1) * w)
}

function resolvePrimary(base: string): string {
  return isValidHex(base) ? normalizeHex(base) : normalizeHex(DEFAULT_BRAND.primaryColor)
}

/** 相对亮度（WCAG），用来判断白字按钮是否还看得清。 */
function relativeLuminance(hex: string): number {
  const channels = hexToRgb(hex).map((value) => {
    const channel = value / 255
    return channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

/** 白字在该底色上的对比度；白底本身按 1.05 / (L + 0.05) 计算。 */
function contrastWithWhite(hex: string): number {
  return 1.05 / (relativeLuminance(hex) + 0.05)
}

/**
 * 由主色派生「承载白字」的交互色（AC-4 / AC-1）。
 *
 * 算法只动 HSL 三个分量：色相基本不变、饱和度略增、明度压到主色的约 0.81。
 * 默认主色 #0EA5E9 派生结果正好是 #0284C7（与改造前的默认交互色一致）。
 * 极亮的主色（例如黄色）派生后仍可能白字发灰，所以再按 4:1 对比度逐档压暗。
 */
export function deriveActionColor(base: string): string {
  const primary = resolvePrimary(base)
  const { h, s, l } = rgbToHsl(...hexToRgb(primary))
  const hue = h + 1.8
  const saturation = clamp(s + 9.5, 0, 100)
  let lightness = clamp(l * 0.812, 6, 92)
  let candidate = hslToHex(hue, saturation, lightness)

  while (lightness > 8 && contrastWithWhite(candidate) < 4) {
    lightness = Math.max(8, lightness - 1.5)
    candidate = hslToHex(hue, saturation, lightness)
  }
  // 统一大写，和配置样例里的 #0284C7 写法一致。
  return candidate.toUpperCase()
}

/**
 * 登录页背景渐变，跟随 primaryColor（C5 / AC-1）。
 * 之前这段色阶写死在 brand.css 里，改主色后登录页仍是天蓝。
 */
export function buildLoginGradient(base: string, mode: BrandMode): string {
  const primary = resolvePrimary(base)
  const darkAnchor = mode === 'light' ? '#00121c' : '#000000'
  const stops: Array<[number, string]> = mode === 'light'
    ? [
        [0, mixHex(primary, darkAnchor, 0.88)],
        [15, mixHex(primary, darkAnchor, 0.78)],
        [25, mixHex(primary, darkAnchor, 0.68)],
        [38, mixHex(primary, darkAnchor, 0.54)],
        [50, mixHex(primary, darkAnchor, 0.38)],
        [65, primary],
        [78, mixHex(primary, '#ffffff', 0.35)],
        [90, mixHex(primary, '#ffffff', 0.65)],
        [100, mixHex(primary, '#ffffff', 0.8)],
      ]
    : [
        [0, mixHex(primary, darkAnchor, 0.93)],
        [15, mixHex(primary, darkAnchor, 0.86)],
        [25, mixHex(primary, darkAnchor, 0.79)],
        [38, mixHex(primary, darkAnchor, 0.71)],
        [50, mixHex(primary, darkAnchor, 0.62)],
        [65, mixHex(primary, darkAnchor, 0.48)],
        [78, mixHex(primary, darkAnchor, 0.33)],
        [90, mixHex(primary, darkAnchor, 0.18)],
        [100, mixHex(primary, darkAnchor, 0.08)],
      ]
  return `linear-gradient(225deg, ${stops.map(([position, color]) => `${color} ${position}%`).join(', ')})`
}

/**
 * 由主色派生 TDesign 的 --td-brand-color-1..10。
 *
 * 明度阶梯对齐 theme.css 里现有绿色色阶的观感：
 * - light：1 是接近白的浅底，4 是主色，3 比 4 亮（hover）、5 比 4 暗（active），
 *   6~10 依次加深，最深一档仍保留同色相，避免偏黑。
 * - dark：1 是主色 20% 透明的浅底，6 是主色，2~5 依次加深、7~10 依次提亮，
 *   保证深色背景下有足够的可达档位。
 *
 * 输入非法时回落到默认主色，保证运行时不会把坏值写进 DOM。
 */
export function deriveBrandScale(base: string, mode: BrandMode): Record<string, string> {
  const safeBase = resolvePrimary(base)
  const { h, s, l } = rgbToHsl(...hexToRgb(safeBase))
  const scale: Record<string, string> = {}

  if (mode === 'light') {
    // 1 档大幅降饱和，避免浅色底上出现荧光感。
    const tintSaturation = clamp(s * 0.6, 24, 92)
    scale['--td-brand-color-1'] = hslToHex(h, tintSaturation, 94)

    // 2~10 相对主色的明度偏移（百分比），与 theme.css 的绿色台阶同向。
    const offsets = [10, 5, 0, -5, -10, -15, -19, -23, -27]
    let previous = 94
    offsets.forEach((offset, index) => {
      // 强制单调递减，防止配置的主色过亮/过暗时出现同档或反超。
      const lightness = Math.min(previous - 1.2, clamp(l + offset, 4, 97))
      scale[`--td-brand-color-${index + 2}`] = hslToHex(h, s, lightness)
      previous = lightness
    })
    // 主色档用原始值，避免 HSL 往返取整产生 1 位色差。
    scale['--td-brand-color-4'] = safeBase
    return scale
  }

  // dark 的 1 档是主色 20% 透明，TDesign 用它做浅色底。
  scale['--td-brand-color-1'] = `${safeBase}20`
  const offsets = [-18, -13, -9, -4, 0, 4, 9, 14, 20]
  let previous = 0
  offsets.forEach((offset, index) => {
    const lightness = Math.max(previous + 1.2, clamp(l + offset, 6, 97))
    scale[`--td-brand-color-${index + 2}`] = hslToHex(h, s, lightness)
    previous = lightness
  })
  scale['--td-brand-color-6'] = safeBase
  return scale
}

/**
 * 生成客户版需要写到 document.documentElement 的全部品牌变量。
 *
 * 色阶由 primaryColor 派生，供浅底/深色等组件使用；
 * 真正承载白字的可点击元素（按钮、链接）用交互色：
 * 默认主色 #0EA5E9 派生出的 #0284C7 约 4.1:1，满足 AC-4。
 * 运营显式配置了 actionColor 时以配置为准（H3），其余情况自动派生。
 * 深色模式同样用交互色，避免亮蓝按钮配白字发灰。
 */
export function brandCssVars(
  config: Pick<BrandConfig, 'primaryColor'> & { actionColor?: string },
  mode: BrandMode,
): Record<string, string> {
  const primary = isValidHex(config.primaryColor)
    ? normalizeHex(config.primaryColor)
    : normalizeHex(DEFAULT_BRAND.primaryColor)
  const action = isValidHex(config.actionColor)
    ? normalizeHex(config.actionColor)
    : deriveActionColor(primary)

  const scale = deriveBrandScale(primary, mode)
  const vars: Record<string, string> = { ...scale }

  vars['--brand-main'] = mode === 'light' ? primary : scale['--td-brand-color-6']
  vars['--td-brand-color'] = action
  vars['--customer-login-gradient'] = buildLoginGradient(primary, mode)
  vars['--td-brand-color-light'] = scale['--td-brand-color-1']
  // 和 theme.css 的方向保持一致：浅色 hover 提亮、active 压暗；深色相反。
  vars['--td-brand-color-hover'] =
    mode === 'light' ? mixHex(action, '#ffffff', 0.12) : mixHex(action, '#000000', 0.1)
  vars['--td-brand-color-active'] =
    mode === 'light' ? mixHex(action, '#000000', 0.12) : mixHex(action, '#ffffff', 0.12)
  vars['--td-brand-color-disabled'] =
    mode === 'light' ? mixHex(primary, '#ffffff', 0.55) : mixHex(primary, '#181818', 0.55)
  vars['--td-brand-color-focus'] = `color-mix(in srgb, ${action} ${
    mode === 'light' ? 20 : 28
  }%, transparent)`

  return vars
}
