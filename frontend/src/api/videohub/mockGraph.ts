import type { GraphEdge, GraphNode, KnowledgeGraphPayload, VideoCategory, WikiGraphRequest } from '@/types/videohub'

const categories: VideoCategory[] = ['interview', 'training', 'salon', 'general']
const nodeSeeds: Array<[string, string, string]> = [
  ['人物', '张明', '知识工程负责人'], ['人物', '林晓', '企业学习顾问'],
  ['地点', '上海创新中心', '线下交流与实践基地'], ['地点', '远程协作空间', '跨团队协作场景'],
  ['概念', '知识网络', '把分散内容连接成可探索网络'], ['概念', '语义检索', '通过语义而非关键词召回内容'],
  ['概念', '组织记忆', '持续沉淀团队经验与上下文'], ['概念', '可信引用', '为结论保留可回溯的证据'],
  ['事件', '产品复盘会', '围绕阶段结果与问题展开复盘'], ['事件', '行业圆桌', '多个角色交换实践经验'],
  ['事件', '年度培训', '面向团队的系统能力建设'], ['事件', '用户访谈', '收集真实场景中的反馈'],
  ['方法', '分层摘要', '按层级组织长视频信息'], ['方法', '证据定位', '从总结快速回到原始视频'],
  ['方法', '主题聚类', '将相近观点归并为主题'], ['方法', '关系抽取', '识别实体和概念之间的联系'],
  ['工具', 'WeKnora', '统一管理并检索组织知识'], ['工具', 'GraphRAG', '结合图结构增强信息召回'],
  ['其他', '跨视频洞察', '从多个视频中发现共同模式'], ['其他', '知识治理', '维护知识质量与生命周期'],
]

export const mockGraphNodes: GraphNode[] = nodeSeeds.map(([attribute, label, name], index) => ({
  id: `graph-node-${index + 1}`,
  name,
  label,
  attributes: index === 19 ? [] : [attribute, index % 2 ? '实践' : '核心'],
  video_id: index === 19 ? undefined : `v-${String(index % 6 + 1).padStart(2, '0')}`,
  video_title: index === 19 ? undefined : `知识视频 ${index % 6 + 1}：${label}`,
  video_category: categories[index % categories.length],
  seconds: index === 18 ? undefined : 35 + index * 47,
  link_count: 3,
  type: 'entity',
}))

const relationTypes = ['相同', '相似', '补充', '对比', '延伸', '关联']
export const mockGraphEdges: GraphEdge[] = Array.from({ length: 30 }, (_, index) => ({
  id: `graph-edge-${index + 1}`,
  source: mockGraphNodes[index % 20].id,
  target: mockGraphNodes[(index * 7 + 3) % 20].id,
  type: relationTypes[index % relationTypes.length],
  weight: 1 + index % 3,
  confidence: index === 29 ? .42 : .68 + (index % 4) * .08,
}))

export function filterMockGraph(req: WikiGraphRequest = {}): KnowledgeGraphPayload {
  const allAttributes = [...new Set(mockGraphNodes.map(node => node.attributes[0]).filter((value): value is string => Boolean(value)))]
  const selected = req.types?.length ? new Set(req.types) : null
  let nodes = mockGraphNodes.filter(node => (!selected || selected.has(node.attributes[0])) && (!req.videoId || node.video_id === req.videoId))
  if (req.mode === 'ego' && req.center) {
    const neighborIds = new Set(mockGraphEdges.flatMap(edge => edge.source === req.center ? [edge.target] : edge.target === req.center ? [edge.source] : []))
    nodes = nodes.filter(node => node.id === req.center || neighborIds.has(node.id))
  }
  const total = nodes.length
  const limit = req.limit && req.limit > 0 ? req.limit : nodes.length
  nodes = nodes.slice(0, limit)
  const ids = new Set(nodes.map(node => node.id))
  const edges = mockGraphEdges.filter(edge => ids.has(edge.source) && ids.has(edge.target) && (edge.confidence ?? 1) >= .6)
  return {
    nodes,
    edges,
    attributes: allAttributes,
    meta: {
      mode: req.mode ?? 'overview', total, returned: nodes.length,
      truncated: nodes.length < total, center: req.center, depth: req.depth,
      familiar_count: edges.length,
    },
  }
}
