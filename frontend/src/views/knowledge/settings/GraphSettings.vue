<template>
  <div class="graph-settings" :class="{ 'graph-settings--embedded': embedded }">
    <div v-if="!embedded" class="section-header">
      <h2>{{ t('graphSettings.title') }}</h2>
      <p class="section-description">{{ t('graphSettings.description') }}</p>
    </div>
    <div
      v-if="graphAvailability !== 'enabled'"
      class="graph-disabled"
      :class="{ 'graph-disabled--loading': graphAvailability === 'loading' }"
      :role="graphAvailability === 'loading' ? 'status' : 'alert'"
    >
      <span class="graph-disabled-icon">
        <t-icon :name="graphAvailability === 'loading' ? 'info-circle' : 'chart-bubble'" />
      </span>
      <div class="graph-disabled-text">
        <p>{{ graphAvailabilityMessage }}</p>
      </div>
      <t-button
        v-if="graphAvailability === 'error'"
        size="small"
        theme="primary"
        variant="outline"
        @click="loadSystemInfo(true)"
      >
        {{ t('common.retry') }}
      </t-button>
      <t-button
        v-if="graphAvailability === 'disabled' && !embedded"
        size="small"
        theme="primary"
        variant="outline"
        @click="handleOpenGraphGuide"
      >
        <template #icon><t-icon name="help-circle" /></template>
        {{ t('graphSettings.howToEnable') }}
      </t-button>
    </div>

    <div v-if="isGraphDatabaseEnabled" class="settings-group">
      <!-- 启用实体关系提取 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('graphSettings.enableLabel') }}</label>
          <p class="desc">{{ t('graphSettings.enableDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localGraphExtract.enabled"
            @change="handleEnabledChange"
          />
        </div>
      </div>

      <div v-if="localGraphExtract.enabled" class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('graphSettings.customInstructionsLabel') }}</label>
          <p class="desc">{{ t('graphSettings.customInstructionsDescription') }}</p>
        </div>
        <div class="setting-control full-width">
          <t-textarea
            v-model="localGraphExtract.customInstructions"
            :placeholder="t('graphSettings.customInstructionsPlaceholder')"
            :maxlength="4000"
            :autosize="{ minRows: 3, maxRows: 8 }"
            @change="handleConfigChange"
          />
        </div>
      </div>

      <div v-if="localGraphExtract.enabled" class="setting-row">
        <div class="setting-info">
          <label>
            {{ t('graphSettings.schema.label') }}
            <t-tooltip :content="t('graphSettings.schema.modeDescription')" placement="top-left">
              <t-button variant="text" shape="square" size="small"
                :aria-label="t('graphSettings.schema.modeDescription')">
                <template #icon><t-icon name="help-circle" /></template>
              </t-button>
            </t-tooltip>
          </label>
        </div>
        <div class="setting-control schema-mode-control">
          <t-button v-if="localGraphExtract.schema.enabled" variant="text"
            @click="schemaPreviewVisible = true">
            <template #icon><t-icon name="browse" /></template>
            {{ t('graphSettings.schema.previewAction') }}
          </t-button>
          <t-switch v-model="localGraphExtract.schema.enabled" @change="handleConfigChange" />
        </div>
      </div>
      <GraphSchemaEditor v-if="localGraphExtract.enabled && localGraphExtract.schema.enabled"
        :schema="localGraphExtract.schema" @update:schema="handleSchemaChange" />
      <GraphSchemaPreview v-if="schemaPreviewVisible && localGraphExtract.enabled && localGraphExtract.schema.enabled"
        v-model:visible="schemaPreviewVisible" :schema="localGraphExtract.schema" />

      <!-- 关系类型配置 -->
      <div v-if="legacyMode" class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('graphSettings.tagsLabel') }}</label>
          <p class="desc">{{ t('graphSettings.tagsDescription') }}</p>
        </div>
        <div class="setting-control full-width">
          <div class="tags-control-group">
            <t-button
              v-if="canRunGraphExtract"
              theme="default"
              size="medium"
              :disabled="!modelStatus.llm.available"
              :loading="tagFabring"
              @click="handleFabriTag"
              class="gen-tags-btn"
            >
              {{ t('graphSettings.generateRandomTags') }}
            </t-button>
            <t-select
              v-model="localGraphExtract.tags"
              multiple
              :placeholder="t('graphSettings.tagsPlaceholder')"
              clearable
              creatable
              filterable
              @change="handleTagsChange"
              style="flex: 1; min-width: 400px;"
            />
          </div>
          <div v-if="!modelStatus.llm.available" class="control-tip">
            <t-icon name="info-circle" class="tip-icon" />
            <span>{{ t('graphSettings.completeModelConfig') }}</span>
          </div>
        </div>
      </div>

      <!-- 示例文本 -->
      <div v-if="localGraphExtract.enabled" class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('graphSettings.sampleTextLabel') }}</label>
          <p class="desc">{{ t('graphSettings.sampleTextDescription') }}</p>
        </div>
        <div class="setting-control full-width">
          <div class="text-control-group">
            <t-button
              v-if="canRunGraphExtract"
              theme="default"
              size="medium"
              :disabled="!modelStatus.llm.available || !!schemaError"
              :loading="textFabring"
              @click="handleFabriText"
              class="gen-text-btn"
            >
              {{ t('graphSettings.generateRandomText') }}
            </t-button>
            <t-textarea
              v-model="localGraphExtract.text"
              :placeholder="t('graphSettings.sampleTextPlaceholder')"
              :autosize="{ minRows: 6, maxRows: 12 }"
              show-word-limit
              maxlength="5000"
              @change="handleTextChange"
              style="width: 100%;"
            />
          </div>
          <div v-if="!modelStatus.llm.available" class="control-tip">
            <t-icon name="info-circle" class="tip-icon" />
            <span>{{ t('graphSettings.completeModelConfig') }}</span>
          </div>
        </div>
      </div>

      <!-- 实体列表 -->
      <div v-if="localGraphExtract.enabled && localGraphExtract.nodes.length > 0" class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('graphSettings.entityListLabel') }}</label>
          <p class="desc">{{ t('graphSettings.entityListDescription') }}</p>
        </div>
        <div class="setting-control full-width">
          <div class="node-list">
            <div v-for="(node, nodeIndex) in localGraphExtract.nodes" :key="nodeIndex" class="node-item">
              <div class="node-header">
                <t-icon name="user" class="node-icon" />
                <t-input
                  :model-value="node.name"
                  :placeholder="t('graphSettings.nodeNamePlaceholder')"
                  @change="renameExampleNode(nodeIndex, String($event))"
                  class="node-name-input"
                />
                <t-select v-if="localGraphExtract.schema.enabled" :model-value="node.entity_type"
                  :options="exampleTypeOptions" :placeholder="t('graphSettings.schema.entityTypes')"
                  :aria-label="t('graphSettings.schema.entityTypes')" class="node-type-select"
                  @change="changeExampleNodeType(nodeIndex, String($event))" />
                <t-button
                  theme="default"
                  size="small"
                  @click="removeNode(nodeIndex)"
                >
                  <t-icon name="delete" />
                </t-button>
              </div>
              <div v-if="legacyMode" class="node-attributes">
                <div v-for="(attribute, attrIndex) in node.attributes" :key="attrIndex" class="attribute-item">
                  <t-input
                    v-model="node.attributes[attrIndex]"
                    :placeholder="t('graphSettings.attributePlaceholder')"
                    @change="handleNodesChange"
                    class="attribute-input"
                  />
                  <t-button
                    theme="default"
                    size="small"
                    @click="removeAttribute(nodeIndex, attrIndex)"
                  >
                    <t-icon name="close" />
                  </t-button>
                </div>
                <t-button
                  theme="default"
                  size="small"
                  @click="addAttribute(nodeIndex)"
                  class="add-attr-btn"
                >
                  {{ t('graphSettings.addAttribute') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 添加实体按钮 -->
      <div v-if="localGraphExtract.enabled" class="setting-row">
        <div class="setting-info">
          <label>{{ t('graphSettings.manageEntitiesLabel') }}</label>
          <p class="desc">{{ t('graphSettings.manageEntitiesDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-button
            theme="primary"
            @click="addNode"
          >
            {{ t('graphSettings.addEntity') }}
          </t-button>
        </div>
      </div>

      <!-- 关系列表 -->
      <div v-if="localGraphExtract.enabled && localGraphExtract.relations.length > 0" class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('graphSettings.relationListLabel') }}</label>
          <p class="desc">{{ t('graphSettings.relationListDescription') }}</p>
        </div>
        <div class="setting-control full-width">
          <div class="relation-list">
            <div v-for="(relation, index) in localGraphExtract.relations" :key="index" class="relation-item">
              <t-select
                v-if="legacyMode"
                v-model="relation.node1"
                :placeholder="t('graphSettings.selectEntity')"
                @change="handleRelationsChange"
                class="relation-select"
              >
                <t-option
                  v-for="node in localGraphExtract.nodes"
                  :key="node.name"
                  :value="node.name"
                  :label="node.name"
                />
              </t-select>
              <t-select v-else :model-value="exampleEndpointValue(relation.node1, relation.source_type)"
                :options="exampleNodeOptions(relation.source_type)" class="relation-select"
                :placeholder="t('graphSettings.schema.source')"
                @change="changeExampleEndpoint(index, 'source', String($event))" />
              <t-icon name="arrow-right" class="relation-arrow" />
              <t-select
                v-if="legacyMode"
                v-model="relation.type"
                :placeholder="t('graphSettings.selectRelationType')"
                clearable
                creatable
                filterable
                @change="handleRelationsChange"
                class="relation-select"
              >
                <t-option
                  v-for="tag in localGraphExtract.tags"
                  :key="tag"
                  :value="tag"
                  :label="tag"
                />
              </t-select>
              <t-select v-else :model-value="exampleRelationKey(relation)"
                :options="exampleRelationOptions" class="relation-select"
                :placeholder="t('graphSettings.selectRelationType')"
                @change="changeExampleRelationType(index, String($event))" />
              <t-icon name="arrow-right" class="relation-arrow" />
              <t-select
                v-if="legacyMode"
                v-model="relation.node2"
                :placeholder="t('graphSettings.selectEntity')"
                @change="handleRelationsChange"
                class="relation-select"
              >
                <t-option
                  v-for="node in localGraphExtract.nodes"
                  :key="node.name"
                  :value="node.name"
                  :label="node.name"
                />
              </t-select>
              <t-select v-else :model-value="exampleEndpointValue(relation.node2, relation.target_type)"
                :options="exampleNodeOptions(relation.target_type)" class="relation-select"
                :placeholder="t('graphSettings.schema.target')"
                @change="changeExampleEndpoint(index, 'target', String($event))" />
              <t-button
                theme="default"
                size="small"
                @click="removeRelation(index)"
              >
                <t-icon name="delete" />
              </t-button>
            </div>
          </div>
        </div>
      </div>

      <!-- 添加关系按钮 -->
      <div v-if="localGraphExtract.enabled" class="setting-row">
        <div class="setting-info">
          <label>{{ t('graphSettings.manageRelationsLabel') }}</label>
          <p class="desc">{{ t('graphSettings.manageRelationsDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-button
            theme="primary"
            @click="addRelation"
          >
            {{ t('graphSettings.addRelation') }}
          </t-button>
        </div>
      </div>

      <!-- 提取操作按钮 -->
      <p v-if="localGraphExtract.enabled && invalidSchemaExample" class="schema-example-error" role="alert">
        {{ t('graphSettings.schema.invalidExample') }}
      </p>
      <div v-if="localGraphExtract.enabled" class="setting-row">
        <div class="setting-info">
          <label>{{ t('graphSettings.extractActionsLabel') }}</label>
          <p class="desc">{{ t('graphSettings.extractActionsDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="action-buttons">
            <t-button
              v-if="canRunGraphExtract"
              theme="primary"
              :disabled="!modelStatus.llm.available || !localGraphExtract.text || !!schemaError"
              :loading="extracting"
              @click="handleExtract"
            >
              {{ extracting ? t('graphSettings.extracting') : t('graphSettings.startExtraction') }}
            </t-button>
            <t-button
              theme="default"
              :disabled="!!schemaError"
              @click="defaultExtractExample"
            >
              {{ t('graphSettings.defaultExample') }}
            </t-button>
            <t-button
              theme="default"
              @click="clearExtractExample"
            >
              {{ t('graphSettings.clearExample') }}
            </t-button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { extractTextRelations, fabriText, fabriTag, type Node, type Relation } from '@/api/initialization'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { useAuthStore } from '@/stores/auth'
import { docsUrl } from '@/utils/docsUrl'
import GraphSchemaEditor from './GraphSchemaEditor.vue'
import GraphSchemaPreview from './GraphSchemaPreview.vue'
import { cloneGraphSchema, graphSchemaError, type GraphSchema } from '@/types/graphSchema'

const { t } = useI18n()
const authStore = useAuthStore()

// canRunGraphExtract 对应后端 POST /initialization/extract/{fabri-tag,fabri-text,
// text-relation} 的 g.Admin() 守卫——这三个都是会调用大模型 + 写库的 admin
// 工具。Contributor 看到按钮点了只会撞 403。
const canRunGraphExtract = computed(() => authStore.hasRole('admin'))

interface GraphExtractConfig {
  enabled: boolean
  text: string
  tags: string[]
  nodes: Node[]
  relations: Relation[]
  customInstructions?: string
  schema?: GraphSchema
}

interface Props {
  graphExtract: GraphExtractConfig
  modelId: string
  allModels?: any[]
  embedded?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  embedded: false,
})

const emit = defineEmits<{
  'update:graphExtract': [value: GraphExtractConfig]
}>()

const modelStatus = computed(() => ({
  llm: {
    available: !!props.modelId
  }
}))

// 本地状态
const cloneConfig = (value: GraphExtractConfig): GraphExtractConfig & { schema: GraphSchema } => ({
  ...value,
  tags: [...(value.tags || [])],
  nodes: (value.nodes || []).map(node => ({ ...node, attributes: [...(node.attributes || [])] })),
  relations: (value.relations || []).map(relation => ({ ...relation })),
  customInstructions: value.customInstructions || '',
  schema: cloneGraphSchema(value.schema),
})
const localGraphExtract = ref(cloneConfig(props.graphExtract))
const legacyMode = computed(() => localGraphExtract.value.enabled && !localGraphExtract.value.schema.enabled)
const schemaPreviewVisible = ref(false)
const schemaError = computed(() => graphSchemaError(localGraphExtract.value.schema))
const invalidSchemaExample = computed(() => {
  const { schema, nodes, relations } = localGraphExtract.value
  if (!schema.enabled || schemaError.value || (!nodes.length && !relations.length)) return false
  const types = new Set(schema.entity_types.map(entity => entity.name))
  const identities = new Set<string>()
  for (const node of nodes) {
    const key = JSON.stringify([node.name, node.entity_type])
    if (!node.name.trim() || !node.entity_type || !types.has(node.entity_type) || identities.has(key)) return true
    identities.add(key)
  }
  return relations.some(relation =>
    !identities.has(JSON.stringify([relation.node1, relation.source_type])) ||
    !identities.has(JSON.stringify([relation.node2, relation.target_type])) ||
    !schema.relation_types.some(type => type.name === relation.type &&
      type.source_type === relation.source_type && type.target_type === relation.target_type))
})
watch(() => localGraphExtract.value.enabled && localGraphExtract.value.schema.enabled, active => {
  if (!active) schemaPreviewVisible.value = false
})
const exampleTypeOptions = computed(() => localGraphExtract.value.schema.entity_types
  .filter(entity => entity.name).map(entity => ({ label: entity.name, value: entity.name })))
const exampleNodeKey = (name: string, type?: string) => name ? JSON.stringify([name, type || '']) : ''
const exampleNodeOptions = (type?: string) => localGraphExtract.value.nodes
  .filter(node => node.name && node.entity_type && (!type || node.entity_type === type))
  .map(node => ({ label: `${node.name} (${node.entity_type})`, value: exampleNodeKey(node.name, node.entity_type) }))
const exampleEndpointValue = (name: string, type?: string) =>
  type && exampleNodeOptions(type).some(option => option.value === exampleNodeKey(name, type))
    ? exampleNodeKey(name, type) : ''
const exampleRelationKey = (relation: Relation) =>
  localGraphExtract.value.schema.relation_types.some(type => type.name === relation.type &&
    type.source_type === relation.source_type && type.target_type === relation.target_type)
    ? JSON.stringify([relation.type, relation.source_type, relation.target_type]) : ''
const exampleRelationOptions = computed(() => localGraphExtract.value.schema.relation_types
  .filter(relation => relation.name && relation.source_type && relation.target_type)
  .map(relation => ({
    label: `${relation.source_type} → ${relation.name} → ${relation.target_type}`,
    value: JSON.stringify([relation.name, relation.source_type, relation.target_type]),
  })))
const changeExampleEndpoint = (index: number, direction: 'source' | 'target', value: string) => {
  const [name, type] = JSON.parse(value) as string[]
  const relation = localGraphExtract.value.relations[index]
  if (direction === 'source') { relation.node1 = name; relation.source_type = type }
  else { relation.node2 = name; relation.target_type = type }
  handleConfigChange()
}
const changeExampleRelationType = (index: number, value: string) => {
  const [name, source, target] = JSON.parse(value) as string[]
  const relation = localGraphExtract.value.relations[index]
  if (relation.source_type !== source) relation.node1 = ''
  if (relation.target_type !== target) relation.node2 = ''
  Object.assign(relation, { type: name, source_type: source, target_type: target })
  handleConfigChange()
}
const updateExampleNode = (index: number, name: string, type?: string) => {
  const node = localGraphExtract.value.nodes[index]
  for (const relation of localGraphExtract.value.relations) {
    if (relation.node1 === node.name && (!localGraphExtract.value.schema.enabled || relation.source_type === node.entity_type)) {
      relation.node1 = name
      if (localGraphExtract.value.schema.enabled) relation.source_type = type
    }
    if (relation.node2 === node.name && (!localGraphExtract.value.schema.enabled || relation.target_type === node.entity_type)) {
      relation.node2 = name
      if (localGraphExtract.value.schema.enabled) relation.target_type = type
    }
  }
  Object.assign(node, { name, entity_type: type })
  handleConfigChange()
}
const renameExampleNode = (index: number, name: string) =>
  updateExampleNode(index, name, localGraphExtract.value.nodes[index].entity_type)
const changeExampleNodeType = (index: number, type: string) =>
  updateExampleNode(index, localGraphExtract.value.nodes[index].name, type)

// 加载状态
const tagFabring = ref(false)
const textFabring = ref(false)
const extracting = ref(false)

const graphAvailability = ref<'loading' | 'enabled' | 'disabled' | 'error'>('loading')
const isGraphDatabaseEnabled = computed(() => graphAvailability.value === 'enabled')
const graphAvailabilityMessage = computed(() => {
  if (graphAvailability.value === 'loading') return t('graphSettings.checkingAvailability')
  if (graphAvailability.value === 'error') return t('graphSettings.checkAvailabilityFailed')
  return t('graphSettings.disabledWarning')
})

// Watch for prop changes
watch(() => props.graphExtract, (newVal) => {
  localGraphExtract.value = cloneConfig(newVal)
}, { deep: true })

// 处理配置变更
const handleConfigChange = () => {
  emit('update:graphExtract', cloneConfig(localGraphExtract.value))
}
const handleSchemaChange = (schema: GraphSchema) => {
  localGraphExtract.value.schema = cloneGraphSchema(schema)
  handleConfigChange()
}

// 处理启用/禁用切换
const handleEnabledChange = () => {
  handleConfigChange()
}

const handleTagsChange = () => {
  handleConfigChange()
}

const handleTextChange = () => {
  handleConfigChange()
}

const handleNodesChange = () => {
  handleConfigChange()
}

const handleRelationsChange = () => {
  handleConfigChange()
}

// 节点操作
const addNode = () => {
  if (!localGraphExtract.value.nodes) {
    localGraphExtract.value.nodes = []
  }
  localGraphExtract.value.nodes.push({
    name: '',
    attributes: [],
    ...(localGraphExtract.value.schema.enabled ? { entity_type: exampleTypeOptions.value[0]?.value || '' } : {}),
  })
  handleNodesChange()
}

const removeNode = (index: number) => {
  const [removed] = localGraphExtract.value.nodes.splice(index, 1)
  localGraphExtract.value.relations = localGraphExtract.value.relations.filter(relation =>
    !(relation.node1 === removed.name && (!localGraphExtract.value.schema.enabled || relation.source_type === removed.entity_type)) &&
    !(relation.node2 === removed.name && (!localGraphExtract.value.schema.enabled || relation.target_type === removed.entity_type)))
  handleNodesChange()
}

const addAttribute = (nodeIndex: number) => {
  localGraphExtract.value.nodes[nodeIndex].attributes.push('')
  handleNodesChange()
}

const removeAttribute = (nodeIndex: number, attrIndex: number) => {
  localGraphExtract.value.nodes[nodeIndex].attributes.splice(attrIndex, 1)
  handleNodesChange()
}

// 关系操作
const addRelation = () => {
  if (!localGraphExtract.value.relations) {
    localGraphExtract.value.relations = []
  }
  localGraphExtract.value.relations.push({
    node1: '',
    node2: '',
    type: ''
  })
  handleRelationsChange()
}

const removeRelation = (index: number) => {
  localGraphExtract.value.relations.splice(index, 1)
  handleRelationsChange()
}

// 生成随机标签
const handleFabriTag = async () => {
  tagFabring.value = true
  try {
    const response = await fabriTag({})
    localGraphExtract.value.tags = response.tags || []
    handleTagsChange()
    MessagePlugin.success(t('graphSettings.tagsGenerated'))
  } catch (error: any) {
    console.error('Failed to generate tags:', error)
    MessagePlugin.error(t('graphSettings.tagsGenerateFailed'))
  } finally {
    tagFabring.value = false
  }
}

// 生成随机文本
const handleFabriText = async () => {
  if (!props.modelId) {
    MessagePlugin.warning(t('graphSettings.completeModelConfig'))
    return
  }
  if (schemaError.value) {
    MessagePlugin.warning(t(`graphSettings.schema.errors.${schemaError.value}`))
    return
  }
  
  textFabring.value = true
  try {
    const response = await fabriText({
      tags: localGraphExtract.value.tags,
      model_id: props.modelId,
      ...(localGraphExtract.value.schema.enabled ? { schema: cloneGraphSchema(localGraphExtract.value.schema) } : {}),
    })
    localGraphExtract.value.text = response.text || ''
    handleTextChange()
    MessagePlugin.success(t('graphSettings.textGenerated'))
  } catch (error: any) {
    console.error('Failed to generate text:', error)
    MessagePlugin.error(t('graphSettings.textGenerateFailed'))
  } finally {
    textFabring.value = false
  }
}

// 提取实体关系
const handleExtract = async () => {
  if (!props.modelId) {
    MessagePlugin.warning(t('graphSettings.completeModelConfig'))
    return
  }
  
  if (!localGraphExtract.value.text) {
    MessagePlugin.warning(t('graphSettings.pleaseInputText'))
    return
  }
  if (schemaError.value) {
    MessagePlugin.warning(t(`graphSettings.schema.errors.${schemaError.value}`))
    return
  }
  
  extracting.value = true
  try {
    const response = await extractTextRelations({
      text: localGraphExtract.value.text,
      tags: localGraphExtract.value.tags,
      model_id: props.modelId,
      custom_instructions: localGraphExtract.value.customInstructions,
      ...(localGraphExtract.value.schema.enabled ? { schema: cloneGraphSchema(localGraphExtract.value.schema) } : {}),
    })
    localGraphExtract.value.nodes = response.nodes || []
    localGraphExtract.value.relations = response.relations || []
    handleNodesChange()
    MessagePlugin.success(t('graphSettings.extractSuccess'))
  } catch (error: any) {
    console.error('Failed to extract relations:', error)
    MessagePlugin.error(t('graphSettings.extractFailed'))
  } finally {
    extracting.value = false
  }
}

// 默认示例
const defaultExtractExample = () => {
  if (localGraphExtract.value.schema.enabled) {
    if (schemaError.value) return
    const schema = localGraphExtract.value.schema
    localGraphExtract.value.nodes = schema.entity_types.map((entity, index) => ({
      name: `${entity.name}_${index + 1}`, entity_type: entity.name, attributes: [],
    }))
    const names = new Map(localGraphExtract.value.nodes.map(node => [node.entity_type, node.name]))
    localGraphExtract.value.relations = schema.relation_types.map(relation => ({
      node1: names.get(relation.source_type)!, node2: names.get(relation.target_type)!,
      type: relation.name, source_type: relation.source_type, target_type: relation.target_type,
    }))
    localGraphExtract.value.text = [
      ...localGraphExtract.value.nodes.map(node => `${node.name}: ${node.entity_type}.`),
      ...localGraphExtract.value.relations.map(relation => `${relation.node1} --${relation.type}--> ${relation.node2}.`),
    ].join('\n')
    handleConfigChange()
    MessagePlugin.success(t('graphSettings.exampleLoaded'))
    return
  }
  localGraphExtract.value.text = `"Romeo and Juliet" is a tragedy written by William Shakespeare early in his career, and is one of the most frequently performed plays in world literature. The play follows two young lovers from feuding families in Verona, Italy — the Montagues and the Capulets. Written around 1594-1596, it was first published in quarto in 1597. The full title is "The Most Excellent and Lamentable Tragedy of Romeo and Juliet." The story has been adapted countless times for stage, film, and other media.`
  localGraphExtract.value.tags = ['Author', 'Alias']
  localGraphExtract.value.nodes = [
    {name: 'Romeo and Juliet', attributes: ['One of the most frequently performed plays', 'Written around 1594-1596', 'A tragedy']},
    {name: 'The Most Excellent and Lamentable Tragedy of Romeo and Juliet', attributes: ['Full title of Romeo and Juliet']},
    {name: 'William Shakespeare', attributes: ['English playwright', 'Author of Romeo and Juliet']},
    {name: 'Verona', attributes: ['City in Italy', 'Setting of the play']}
  ]
  localGraphExtract.value.relations = [
    {node1: 'Romeo and Juliet', node2: 'The Most Excellent and Lamentable Tragedy of Romeo and Juliet', type: 'Alias'},
    {node1: 'Romeo and Juliet', node2: 'William Shakespeare', type: 'Author'},
    {node1: 'Romeo and Juliet', node2: 'Verona', type: 'Setting'}
  ]
  handleNodesChange()
  MessagePlugin.success(t('graphSettings.exampleLoaded'))
}

// 清除示例
const clearExtractExample = () => {
  localGraphExtract.value.text = ''
  localGraphExtract.value.tags = []
  localGraphExtract.value.nodes = []
  localGraphExtract.value.relations = []
  handleNodesChange()
  MessagePlugin.success(t('graphSettings.exampleCleared'))
}

const editorResources = useEditorResourcesStore()

let systemInfoRequest = 0
const loadSystemInfo = async (force = false) => {
  const request = ++systemInfoRequest
  graphAvailability.value = 'loading'
  try {
    await editorResources.ensureSystemInfo(force)
    if (request !== systemInfoRequest) return
    const engine = editorResources.systemInfo?.graph_database_engine
    const name = typeof engine === 'string' ? engine.trim() : ''
    graphAvailability.value = !name ? 'error' : name === 'Not Enabled' ? 'disabled' : 'enabled'
  } catch (error: any) {
    if (request !== systemInfoRequest) return
    graphAvailability.value = 'error'
    console.error('Failed to load system info:', error)
  }
}

const graphGuideUrl =
  import.meta.env.VITE_KG_GUIDE_URL || docsUrl('knowledgeGraph')

// Open guide documentation to show how to enable graph database
const handleOpenGraphGuide = () => {
  window.open(graphGuideUrl, '_blank', 'noopener')
}

// 初始化
onMounted(async () => {
  await loadSystemInfo()
})
</script>

<style lang="less" scoped>
.graph-settings {
  width: 100%;
}
.schema-mode-control { display: flex; align-items: center; gap: 12px; }
.schema-example-error { color: var(--td-error-color); font-size: var(--app-text-md); line-height: 1.5; }
.node-type-select { width: 160px; flex-shrink: 0; }
@media (max-width: 600px) {
  .node-header { flex-wrap: wrap; }
  .node-type-select { width: 100%; }
  .relation-item { flex-wrap: wrap;
    .relation-select { min-width: 0; flex-basis: 100%; }
    .relation-arrow { display: none; }
  }
}

.section-header {
  margin-bottom: 20px;

  h2 {
    font-size: var(--app-text-3xl);
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 6px 0;
  }

  .section-description {
    font-size: var(--app-text-base);
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.graph-disabled {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.graph-disabled-icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--app-radius-md);
  font-size: var(--app-text-2xl);
  color: var(--td-warning-color);
  background: var(--td-warning-color-light);
}

.graph-disabled-text {
  flex: 1;
  min-width: 0;

  p {
    margin: 0;
    font-size: var(--app-text-md);
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.graph-disabled--loading .graph-disabled-icon {
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }

  &.vertical {
    flex-direction: column;
    gap: 12px;

    .setting-control {
      width: 100%;
      max-width: 100%;
    }
  }
}

.setting-info {
  flex: 0 0 40%;
  max-width: 40%;
  padding-right: 24px;

  label {
    font-size: var(--app-text-lg);
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex: 0 0 55%;
  max-width: 55%;
  display: flex;
  justify-content: flex-end;
  align-items: center;

  &.full-width {
    width: 100%;
    max-width: 100%;
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }
}

.tags-control-group,
.text-control-group {
  display: flex;
  gap: 12px;
  width: 100%;
  align-items: flex-start;
}

.text-control-group {
  flex-direction: column;
}

.control-tip {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);

  .tip-icon {
    color: var(--td-brand-color);
  }
}

.node-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  width: 100%;
}

.node-item {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  padding: 16px;
}

.node-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;

  .node-icon {
    font-size: var(--app-text-3xl);
    color: var(--td-brand-color);
  }

  .node-name-input {
    flex: 1;
  }
}

.node-attributes {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-left: 32px;
}

.attribute-item {
  display: flex;
  gap: 8px;
  align-items: center;

  .attribute-input {
    flex: 1;
  }
}

.add-attr-btn {
  align-self: flex-start;
}

.relation-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}

.relation-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);

  .relation-select {
    flex: 1;
    min-width: 150px;
  }

  .relation-arrow {
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xl);
  }
}

.action-buttons {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}

.graph-settings--embedded {
  .graph-disabled {
    margin-bottom: 12px;
  }

  .setting-row:not(.vertical) {
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
    padding: 12px 0;
  }

  .setting-row:not(.vertical) .setting-info {
    flex: none;
    max-width: none;
    padding-right: 0;
  }

  .setting-row:not(.vertical) .setting-control {
    align-self: flex-start;
  }
}
</style>
