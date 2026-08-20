import type { KnowledgeType, RelationType } from '@/types/videohub'

export interface KnowledgeTypeStyle {
  label: string
  colorVar: string
  icon: string
}

export const KNOWLEDGE_TYPES: KnowledgeType[] = ['entity', 'concept', 'case', 'method', 'insight']

export const KNOWLEDGE_TYPE_STYLES: Record<KnowledgeType, KnowledgeTypeStyle> = {
  entity: { label: '实体', colorVar: 'var(--color-data-1)', icon: 'institution' },
  concept: { label: '概念', colorVar: 'var(--color-data-2)', icon: 'book' },
  case: { label: '案例', colorVar: 'var(--color-data-3)', icon: 'file' },
  method: { label: '方法', colorVar: 'var(--color-data-4)', icon: 'tools' },
  insight: { label: '洞察', colorVar: 'var(--color-data-5)', icon: 'lightbulb' },
}

export function getNaturalRelationPhrase(type: KnowledgeType, relation: RelationType): string {
  if (relation === '相同') return '观点一致'
  if (relation === '相似') return '视角与判断相似'
  if (relation === '对比') return '形成对比视角'
  if (relation === '补充') {
    if (type === 'concept') return '从概念定义角度补充'
    if (type === 'case') return '通过实际案例补充说明'
    if (type === 'method') return '从工程实践方法角度补充'
    if (type === 'entity') return '补充相关人物与组织背景'
    return '补充相关洞察与论据'
  }
  if (type === 'case') return '进一步延伸到应用案例'
  if (type === 'method') return '进一步延伸到落地方法'
  if (type === 'entity') return '进一步延伸到相关主体'
  if (type === 'concept') return '进一步延伸概念边界'
  return '进一步延伸探讨'
}
