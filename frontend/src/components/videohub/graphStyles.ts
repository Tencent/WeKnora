export interface RelationStyle {
  lineStyle: 'solid' | 'dashed' | 'dotted'
  width: number
  opacity: number
  color: string
}

export const KNOWN_ATTRIBUTES: Record<string, string> = {
  '人物': '--td-brand-color',
  '地点': '--td-warning-color',
  '概念': '--td-text-color-link',
  '事件': '--td-error-color',
  '方法': '--td-success-color',
}

export const FALLBACK_ATTRIBUTE_COLOR = '--td-brand-color'

export const KNOWN_RELATION_TYPES: Record<string, RelationStyle> = {
  '相同': { lineStyle: 'solid', width: 2, opacity: 1, color: '--td-brand-color' },
  '相似': { lineStyle: 'solid', width: 1.5, opacity: .8, color: '--td-brand-color-light' },
  '补充': { lineStyle: 'dashed', width: 1.5, opacity: .8, color: '--td-text-color-link' },
  '对比': { lineStyle: 'dotted', width: 1.5, opacity: .6, color: '--td-warning-color' },
  '延伸': { lineStyle: 'dotted', width: 1, opacity: .5, color: '--td-text-color-secondary' },
}

export const FALLBACK_RELATION_STYLE: RelationStyle = {
  lineStyle: 'solid', width: 1, opacity: .4, color: '--td-component-stroke',
}

export function readThemeToken(token: string, fallback = '') {
  return getComputedStyle(document.documentElement).getPropertyValue(token).trim() || fallback
}
