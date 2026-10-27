import { brandConfig } from './runtime'
import type { BrandConfig } from './types'

export {
  applyCustomerTheme,
  applyDocumentBrand,
  brandConfig,
  initCustomerBrandTheme,
  productLinesFallback,
  readBrandConfig,
} from './runtime'
export type { ApplyDocumentBrandOptions } from './runtime'
export {
  brandCssVars,
  buildLoginGradient,
  deriveActionColor,
  deriveBrandScale,
  isValidHex,
} from './colors'
export type { BrandMode } from './colors'
export { DEFAULT_BRAND } from './defaults'
export type { BrandConfig, ProductLine } from './types'

/** 读取品牌配置。启动时读取一次，运行期间只读。 */
export function useBrand(): Readonly<BrandConfig> {
  return brandConfig
}
