export type GuidePlacement = 'right' | 'left' | 'bottom' | 'top'

export interface SpotlightGuideStep {
  key: string
  /** 高亮目标的 CSS 选择器；缺省表示居中卡片 */
  target?: string
  placement?: GuidePlacement
  before?: () => void | Promise<void>
  /** 目标不存在时是否跳过该步骤 */
  optional?: boolean
  /** 为 true 时引导用户直接点击高亮区域，不展示「下一步」 */
  interact?: boolean
}
