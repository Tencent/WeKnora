/**
 * 知识图谱抽取配置的提交前校验（纯函数，不依赖组件是否挂载）。
 *
 * 规则与后端 validateExtractConfig
 * (internal/handler/knowledgebase.go) 严格对齐：enabled=true 时
 * text / tags / nodes / relations 四项均必填，且关系必须引用已存在的实体名，
 * 否则 PUT /initialization/config 会得到 400 "text cannot be empty" 等错误。
 * 前端在保存/确认入口先跑这里，把缺项变成可见引导而不是接口报错。
 */

export interface GraphExtractNodeLike {
  name?: string
  attributes?: string[]
}

export interface GraphExtractRelationLike {
  node1?: string
  node2?: string
  type?: string
}

export interface GraphExtractConfigLike {
  enabled?: boolean
  text?: string
  tags?: string[]
  nodes?: GraphExtractNodeLike[]
  relations?: GraphExtractRelationLike[]
  customInstructions?: string
}

export type GraphExtractIssueField = 'text' | 'tags' | 'nodes' | 'relations'

export interface GraphExtractIssue {
  field: GraphExtractIssueField
  /** i18n key，调用方用 t(issue.key) 渲染 */
  key: string
  params?: Record<string, unknown>
}

/**
 * 返回缺失/非法项列表；空数组表示可以提交。
 * 未开启图谱提取时永远返回空数组。
 */
export function getGraphExtractIssues(config: GraphExtractConfigLike | null | undefined): GraphExtractIssue[] {
  if (!config || !config.enabled) return []
  const issues: GraphExtractIssue[] = []

  if (!config.text || !config.text.trim()) {
    issues.push({ field: 'text', key: 'graphSettings.textRequired' })
  }

  const tags = config.tags || []
  if (tags.length === 0) {
    issues.push({ field: 'tags', key: 'graphSettings.tagsRequired' })
  } else if (tags.some(tag => !tag || !tag.trim())) {
    issues.push({ field: 'tags', key: 'graphSettings.tagsItemEmpty' })
  }

  const nodes = config.nodes || []
  // 空名称逐项报错；有效名称保留原值，与后端及提交数据中的关系引用一致。
  const nodeNames = nodes
    .filter(node => node && node.name && node.name.trim())
    .map(node => node.name as string)
  const validNodeNames = new Set(nodeNames)
  if (nodes.length === 0) {
    issues.push({ field: 'nodes', key: 'graphSettings.nodesRequired' })
  } else if (nodeNames.length !== nodes.length) {
    issues.push({ field: 'nodes', key: 'graphSettings.nodeNamePlaceholder' })
  } else if (nodeNames.length !== validNodeNames.size) {
    // 后端对同名实体直接 400（duplicate node name）
    issues.push({ field: 'nodes', key: 'graphSettings.nodesDuplicate' })
  }

  const relations = config.relations || []
  if (relations.length === 0) {
    issues.push({ field: 'relations', key: 'graphSettings.relationsRequired' })
  } else if (relations.some(rel => !rel || !rel.node1 || !rel.node2 || !rel.type)) {
    issues.push({ field: 'relations', key: 'graphSettings.relationIncomplete' })
  } else if (validNodeNames.size > 0
    && relations.some(rel => !validNodeNames.has(rel.node1 as string) || !validNodeNames.has(rel.node2 as string))) {
    issues.push({ field: 'relations', key: 'graphSettings.relationNodeMismatch' })
  }

  return issues
}

/** 有任一缺失项时为 true（enabled=false 恒为 false） */
export function hasGraphExtractIssues(config: GraphExtractConfigLike | null | undefined): boolean {
  return getGraphExtractIssues(config).length > 0
}
