import type { Node, Relation } from '@/api/initialization'

export interface GraphExtractConfig {
  enabled: boolean
  text: string
  tags: string[]
  nodes: Node[]
  relations: Relation[]
  customInstructions?: string
}

const hasValue = (value: string): boolean => value.trim().length > 0

export const isGraphExtractConfigComplete = (
  config: GraphExtractConfig | null | undefined
): boolean => {
  if (!config?.enabled) return true

  if (!hasValue(config.text)) return false
  if (config.tags.length === 0 || config.tags.some(tag => !hasValue(tag))) return false
  if (config.nodes.length === 0) return false

  const nodeNames = config.nodes.map(node => node.name)
  if (nodeNames.some(name => !hasValue(name))) return false
  if (new Set(nodeNames).size !== nodeNames.length) return false

  const knownNodeNames = new Set(nodeNames)
  if (config.relations.length === 0) return false

  return config.relations.every(relation =>
    hasValue(relation.node1) &&
    hasValue(relation.node2) &&
    hasValue(relation.type) &&
    knownNodeNames.has(relation.node1) &&
    knownNodeNames.has(relation.node2)
  )
}
