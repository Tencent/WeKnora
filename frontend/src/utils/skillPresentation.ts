import type { DiscoverySkill } from '@/api/skill'

export function skillTone(item: Pick<DiscoverySkill, 'name' | 'category'>): string {
  if (item.category === 'browser') return 'cyan'
  if (item.category === 'analysis') return 'violet'
  if (item.name.includes('xlsx')) return 'green'
  if (item.name.includes('pdf')) return 'rose'
  if (item.name.includes('ppt') || item.name === 'powerpoint') return 'amber'
  return 'blue'
}

export function skillIcon(item: Pick<DiscoverySkill, 'name' | 'category'>): string {
  if (item.category === 'browser') return 'internet'
  if (item.category === 'analysis') return 'chart-bar'
  if (item.name.includes('xlsx')) return 'table'
  if (item.name.includes('ppt') || item.name === 'powerpoint') return 'slideshow'
  return 'file'
}

export function localizedSkillText(value: Record<string, string> | undefined, locale: string): string {
  return value?.[locale] || value?.['en-US'] || ''
}
