<template>
  <div class="schema-editor">
    <div class="schema-heading">
      <label>{{ t('graphSettings.schema.entityTypes') }}</label>
      <t-button variant="text" size="small" :disabled="schema.entity_types.length >= 50" @click="addEntity">
        <template #icon><t-icon name="add" /></template>
        {{ t('graphSettings.schema.addEntity') }}
      </t-button>
    </div>
    <div v-for="(entity, index) in schema.entity_types" :key="index" class="schema-entity-row">
      <t-input :model-value="entity.name" :maxlength="128" :placeholder="t('graphSettings.schema.name')"
        :aria-label="t('graphSettings.schema.entityTypes')" @change="renameEntity(index, String($event))" />
      <t-input :model-value="entity.description || ''" :maxlength="1000"
        :placeholder="t('graphSettings.schema.description')" @change="changeEntityDescription(index, String($event))" />
      <t-tooltip :content="t('common.delete')">
        <t-button shape="square" variant="text" :aria-label="t('common.delete')" @click="deleteEntity(index)">
          <template #icon><t-icon name="delete" /></template>
        </t-button>
      </t-tooltip>
    </div>
    <div class="schema-heading">
      <label>{{ t('graphSettings.schema.relationTypes') }}</label>
      <t-button variant="text" size="small" :disabled="!schema.entity_types.length || schema.relation_types.length >= 100"
        @click="addRelation">
        <template #icon><t-icon name="add" /></template>
        {{ t('graphSettings.schema.addRelation') }}
      </t-button>
    </div>
    <div v-for="(relation, index) in schema.relation_types" :key="index" class="schema-relation-row">
      <t-select :model-value="relation.source_type" :options="entityOptions"
        :placeholder="t('graphSettings.schema.source')" :aria-label="t('graphSettings.schema.source')"
        @change="changeRelation(index, 'source_type', String($event))" />
      <t-input :model-value="relation.name" :maxlength="128" :placeholder="t('graphSettings.schema.name')"
        :aria-label="t('graphSettings.schema.relationTypes')" @change="changeRelation(index, 'name', String($event))" />
      <t-select :model-value="relation.target_type" :options="entityOptions"
        :placeholder="t('graphSettings.schema.target')" :aria-label="t('graphSettings.schema.target')"
        @change="changeRelation(index, 'target_type', String($event))" />
      <t-tooltip :content="t('common.delete')">
        <t-button shape="square" variant="text" :aria-label="t('common.delete')" @click="deleteRelation(index)">
          <template #icon><t-icon name="delete" /></template>
        </t-button>
      </t-tooltip>
      <t-input class="schema-relation-description" :model-value="relation.description || ''" :maxlength="1000"
        :placeholder="t('graphSettings.schema.description')" @change="changeRelation(index, 'description', String($event))" />
    </div>
    <p v-if="error" class="schema-error" role="alert">{{ t(`graphSettings.schema.errors.${error}`) }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  cloneGraphSchema, graphSchemaError, renameGraphEntity, removeGraphEntity,
  type GraphSchema, type GraphRelationType,
} from '@/types/graphSchema'

const props = defineProps<{ schema: GraphSchema }>()
const emit = defineEmits<{ 'update:schema': [schema: GraphSchema] }>()
const { t } = useI18n()
const error = computed(() => graphSchemaError(props.schema))
const entityOptions = computed(() => [...new Set(props.schema.entity_types.map(entity => entity.name).filter(Boolean))]
  .map(name => ({ label: name, value: name })))
const update = (edit: (schema: GraphSchema) => void) => {
  const next = cloneGraphSchema(props.schema)
  edit(next)
  emit('update:schema', next)
}
const addEntity = () => update(schema => schema.entity_types.push({ name: '', description: '' }))
const renameEntity = (index: number, name: string) => emit('update:schema', renameGraphEntity(props.schema, index, name))
const changeEntityDescription = (index: number, value: string) => update(schema => { schema.entity_types[index].description = value })
const deleteEntity = (index: number) => emit('update:schema', removeGraphEntity(props.schema, index))
const addRelation = () => update(schema => schema.relation_types.push({ name: '', source_type: '', target_type: '', description: '' }))
const changeRelation = (index: number, key: keyof GraphRelationType, value: string) =>
  update(schema => { schema.relation_types[index][key] = value })
const deleteRelation = (index: number) => update(schema => { schema.relation_types.splice(index, 1) })

</script>

<style lang="less" scoped>
.schema-editor { width: 100%; min-width: 0; }
.schema-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin: 16px 0 8px;
  label { font-size: var(--app-text-base); font-weight: 500; }
}
.schema-entity-row { display: grid; grid-template-columns: minmax(100px, 1fr) minmax(140px, 2fr) 36px; gap: 8px; margin-bottom: 8px; }
.schema-relation-row { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)) 36px; gap: 8px; margin-bottom: 16px; }
.schema-relation-description { grid-column: 1 / 4; }
.schema-error { color: var(--td-error-color); font-size: var(--app-text-md); }
@media (max-width: 600px) {
  .schema-entity-row { grid-template-columns: minmax(0, 1fr) 36px;
    > :nth-child(2) { grid-column: 1; grid-row: 2; }
    > :last-child { grid-column: 2; grid-row: 1; }
  }
  .schema-relation-row { grid-template-columns: minmax(0, 1fr) 36px;
    > :nth-child(-n+3) { grid-column: 1; }
    > :nth-child(4) { grid-column: 2; grid-row: 1; }
  }
  .schema-relation-description { grid-column: 1; }
}
</style>
