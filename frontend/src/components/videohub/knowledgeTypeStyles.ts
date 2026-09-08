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
  applies_to: '应用于',
  part_of: '子命题',
  derived_from: '由此推导',
  supports: '支持',
  involves: '涉及',
  related_to: '相关',
}

export function getRelationTypeLabel(relation: string): string {
  return RELATION_TYPE_LABELS[relation as RelationType] || relation
}

export function getNaturalRelationPhrase(type: KnowledgeType, relation: RelationType): string {
  if (relation === 'related_to') return type === 'entity' ? '关联实体' : '相关内容'
  return getRelationTypeLabel(relation)
}

const GRAPH_RELATION_TYPE_LABELS: Record<string, string> = {
  explains: '解释',
  complements: '互补',
  contradicts: '矛盾',
  example_of: '案例',
  part_of: '组成',
  applies_to: '应用',
  supports: '支持',
  involves: '涉及',
}

export function getGraphRelationTypeLabel(relation: string): string {
  return GRAPH_RELATION_TYPE_LABELS[relation] || getRelationTypeLabel(relation)
}

export function getRelationDescription(title: string, relation: string, outgoing: boolean): string {
  const target = `「${title}」`
  switch (relation) {
    case 'explains': return outgoing ? `当前知识解释了${target}` : `${target}解释了当前知识`
    case 'complements': return `当前知识与${target}是互补知识`
    case 'contradicts': return `当前知识与${target}存在矛盾`
    case 'example_of': return outgoing ? `当前知识是${target}的案例` : `${target}是当前知识的案例`
    case 'part_of': return outgoing ? `当前知识是${target}的组成部分` : `${target}是当前知识的组成部分`
    case 'applies_to': return outgoing ? `当前知识适用于${target}` : `${target}适用于当前知识`
    case 'supports': return outgoing ? `当前知识支持${target}` : `${target}支持当前知识`
    case 'involves': return outgoing ? `当前知识涉及${target}` : `${target}涉及当前知识`
    default: return `当前知识与${target}是${getRelationTypeLabel(relation)}关系`
  }
}
