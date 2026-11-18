import type { ElementDefinition } from 'cytoscape'
import { graphSchemaError, type GraphSchema } from './graphSchema'

export const schemaGraphColors = ['#3579b8', '#27846c', '#b65371', '#8460ac', '#ae6b23', '#377e91']

export function schemaGraphElements(schema: GraphSchema): ElementDefinition[] {
  if (graphSchemaError(schema)) return []
  const ids = new Map(schema.entity_types.map((entity, index) => [entity.name, `entity-${index}`]))
  return [
    ...schema.entity_types.map((entity, index) => ({
      data: { id: `entity-${index}`, name: entity.name, description: entity.description,
        color: schemaGraphColors[index % schemaGraphColors.length] },
      position: { x: Math.cos(index * 2 * Math.PI / schema.entity_types.length) * 180,
        y: Math.sin(index * 2 * Math.PI / schema.entity_types.length) * 180 },
    })),
    ...schema.relation_types.map((relation, index) => ({
      data: { id: `relation-${index}`, source: ids.get(relation.source_type), target: ids.get(relation.target_type),
        name: relation.name, description: relation.description,
        sourceName: relation.source_type, targetName: relation.target_type },
    })),
  ]
}
