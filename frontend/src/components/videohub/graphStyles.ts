export interface RelationStyle {
  lineStyle: 'solid' | 'dashed' | 'dotted'
  width: number
  opacity: number
  color: string
}

export const KNOWN_ATTRIBUTES: Record<string, string> = {
  '实体': '--color-data-1',
  '概念': '--color-data-2',
  '案例': '--color-data-3',
  '方法论': '--color-data-4',
  methodology: '--color-data-4',
  '洞察': '--color-data-5',
}

export const FALLBACK_ATTRIBUTE_COLOR = '--color-data-1'

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
