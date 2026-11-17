export interface GraphEntityType {
  name: string
  description?: string
}

export interface GraphRelationType {
  name: string
  source_type: string
  target_type: string
  description?: string
}

export interface GraphSchema {
  enabled: boolean
  entity_types: GraphEntityType[]
  relation_types: GraphRelationType[]
}

export function cloneGraphSchema(schema?: GraphSchema | null): GraphSchema {
  return {
    enabled: schema?.enabled ?? false,
    entity_types: (schema?.entity_types ?? []).map(type => ({ ...type })),
    relation_types: (schema?.relation_types ?? []).map(type => ({ ...type })),
  }
}

export function graphSchemaError(schema: GraphSchema): string | null {
  if (!schema.enabled) return null
  if (!schema.entity_types.length || schema.entity_types.length > 50 || schema.relation_types.length > 100) {
    return 'limits'
  }
  const validName = (name: string) => !!name && name === name.trim() &&
    Array.from(name).length <= 128 && !/[\u0000\r\n]/.test(name)
  const names = new Set<string>()
  for (const entity of schema.entity_types) {
    if (!validName(entity.name) || Array.from(entity.description ?? '').length > 1000) return 'invalidName'
    if (names.has(entity.name)) return 'duplicate'
    names.add(entity.name)
  }
  const relations = new Set<string>()
  for (const relation of schema.relation_types) {
    if (!validName(relation.name) || Array.from(relation.description ?? '').length > 1000) return 'invalidName'
    if (!names.has(relation.source_type) || !names.has(relation.target_type)) return 'endpoints'
    const key = JSON.stringify([relation.name, relation.source_type, relation.target_type])
    if (relations.has(key)) return 'duplicate'
    relations.add(key)
  }
  return null
}

export function renameGraphEntity(schema: GraphSchema, index: number, name: string): GraphSchema {
  const next = cloneGraphSchema(schema)
  const oldName = next.entity_types[index]?.name
  if (oldName == null) return next
  next.entity_types[index].name = name
  if (oldName && !next.entity_types.some((entity, i) => i !== index && entity.name === oldName)) {
    next.relation_types = next.relation_types.map(relation => ({
      ...relation,
      source_type: relation.source_type === oldName ? name : relation.source_type,
      target_type: relation.target_type === oldName ? name : relation.target_type,
    }))
  }
  return next
}

export function removeGraphEntity(schema: GraphSchema, index: number): GraphSchema {
  const next = cloneGraphSchema(schema)
  const [removed] = next.entity_types.splice(index, 1)
  if (removed && !next.entity_types.some(entity => entity.name === removed.name)) {
    next.relation_types = next.relation_types.filter(relation =>
      relation.source_type !== removed.name && relation.target_type !== removed.name)
  }
  return next
}

// Only generated identifiers become Mermaid syntax. Escape label metacharacters.
export function graphSchemaMermaid(schema: GraphSchema): string {
  if (graphSchemaError(schema) || !schema.entity_types.length) return ''
  const label = (text: string) => text.replace(/[&<>"#`\\]/g, char => `#${char.codePointAt(0)};`)
  const ids = new Map(schema.entity_types.map((entity, i) => [entity.name, `e${i}`]))
  return [
    '%%{init: {"htmlLabels": false, "flowchart": {"htmlLabels": false}}}%%',
    'flowchart LR',
    ...schema.entity_types.map((entity, i) => `e${i}["${label(entity.name)}"]`),
    ...schema.relation_types.map(relation =>
      `${ids.get(relation.source_type)} -->|"${label(relation.name)}"| ${ids.get(relation.target_type)}`),
  ].join('\n')
}
