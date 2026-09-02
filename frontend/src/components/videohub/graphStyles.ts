export interface RelationStyle {
  lineStyle: 'solid' | 'dashed' | 'dotted'
  width: number
  opacity: number
  color: string
}

export const KNOWN_ATTRIBUTES: Record<string, string> = {
  '实体': '--td-brand-color',
  '概念': '--td-text-color-link',
  '案例': '--td-error-color',
  '方法论': '--td-success-color',
  methodology: '--td-success-color',
  '洞察': '--td-warning-color',
}

export const FALLBACK_ATTRIBUTE_COLOR = '--td-brand-color'

export const KNOWN_RELATION_TYPES: Record<string, RelationStyle> = {
	contradicts: { lineStyle: 'dotted', width: 2, opacity: .9, color: '--td-error-color' },
	complements: { lineStyle: 'solid', width: 1.5, opacity: .8, color: '--td-success-color' },
	explains: { lineStyle: 'solid', width: 1.5, opacity: .85, color: '--td-brand-color' },
	example_of: { lineStyle: 'dashed', width: 1.5, opacity: .8, color: '--td-text-color-link' },
	part_of: { lineStyle: 'dashed', width: 1.5, opacity: .7, color: '--td-warning-color' },
	derived_from: { lineStyle: 'dotted', width: 1.5, opacity: .7, color: '--td-text-color-secondary' },
	supports: { lineStyle: 'solid', width: 1.5, opacity: .75, color: '--td-success-color' },
	related_to: { lineStyle: 'dotted', width: 1, opacity: .5, color: '--td-text-color-secondary' },
}

export const FALLBACK_RELATION_STYLE: RelationStyle = {
  lineStyle: 'solid', width: 1, opacity: .4, color: '--td-component-stroke',
}

export function readThemeToken(token: string, fallback = '') {
  return getComputedStyle(document.documentElement).getPropertyValue(token).trim() || fallback
}
