import type { KnowledgeType, RelationType } from '@/types/videohub'

export interface KnowledgeTypeStyle {
  label: string
  colorVar: string
  icon: string
}

export const KNOWLEDGE_TYPES: KnowledgeType[] = ['entity', 'concept', 'case', 'methodology', 'insight']

export const KNOWLEDGE_TYPE_STYLES: Record<KnowledgeType, KnowledgeTypeStyle> = {
  entity: { label: '实体', colorVar: 'var(--color-data-1)', icon: 'institution' },
  concept: { label: '概念', colorVar: 'var(--color-data-2)', icon: 'book' },
  case: { label: '案例', colorVar: 'var(--color-data-3)', icon: 'file' },
  methodology: { label: '方法论', colorVar: 'var(--color-data-4)', icon: 'tools' },
  insight: { label: '洞察', colorVar: 'var(--color-data-5)', icon: 'lightbulb' },
}

export const RELATION_TYPE_LABELS: Record<RelationType, string> = {
  contradicts: '对立 / 竞争',
  complements: '互补 / 协同',
  explains: '因果 / 解释',
  example_of: '实例',
  part_of: '子命题',
  derived_from: '由此推导',
  supports: '支持',
  related_to: '相关',
}

export function getRelationTypeLabel(relation: string): string {
  return RELATION_TYPE_LABELS[relation as RelationType] || relation
}

export function getNaturalRelationPhrase(type: KnowledgeType, relation: RelationType): string {
  if (relation === 'related_to') return type === 'entity' ? '关联实体' : '相关内容'
  return getRelationTypeLabel(relation)
}
