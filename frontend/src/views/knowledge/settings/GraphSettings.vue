<template>
  <div class="graph-settings">
    <div class="section-header">
      <h2>{{ t('graphSettings.title') }}</h2>
      <p class="section-description">{{ t('graphSettings.description') }}</p>
      
      <!-- Warning message when graph database is not enabled -->
      <t-alert
        v-if="!isGraphDatabaseEnabled"
        theme="warning"
        style="margin-top: 16px;"
      >
        <template #message>
          <div>{{ t('graphSettings.disabledWarning') }}</div>
          <t-link class="graph-guide-link" theme="primary" @click="handleOpenGraphGuide">
            {{ t('graphSettings.howToEnable') }}
          </t-link>
        </template>
      </t-alert>
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

      <!-- 关系类型配置 -->
      <div v-if="localGraphExtract.enabled" class="setting-row vertical">
        <div class="setting-info">
          <label>{{ t('graphSettings.tagsLabel') }}</label>
          <p class="desc">{{ t('graphSettings.tagsDescription') }}</p>
        </div>
        <div class="setting-control full-width">
          <div class="tags-control-group">
            <t-button
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
              theme="default"
              size="medium"
              :disabled="!modelStatus.llm.available"
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
                  v-model="node.name"
                  :placeholder="t('graphSettings.nodeNamePlaceholder')"
                  @change="handleNodesChange"
                  class="node-name-input"
                />
                <t-button
                  theme="default"
                  size="small"
                  @click="removeNode(nodeIndex)"
                >
                  <t-icon name="delete" />
                </t-button>
              </div>
              <div class="node-attributes">
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
              <t-icon name="arrow-right" class="relation-arrow" />
              <t-select
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
              <t-icon name="arrow-right" class="relation-arrow" />
              <t-select
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
      <div v-if="localGraphExtract.enabled" class="setting-row">
        <div class="setting-info">
          <label>{{ t('graphSettings.extractActionsLabel') }}</label>
          <p class="desc">{{ t('graphSettings.extractActionsDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="action-buttons">
            <t-button
              theme="primary"
              :disabled="!modelStatus.llm.available || !localGraphExtract.text"
              :loading="extracting"
              @click="handleExtract"
            >
              {{ extracting ? t('graphSettings.extracting') : t('graphSettings.startExtraction') }}
            </t-button>
            <t-button
              theme="default"
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
import { extractTextRelations, fabriText, fabriTag, type Node, type Relation, type LLMConfig } from '@/api/initialization'
import { listModels } from '@/api/model'
import { getSystemInfo } from '@/api/system'

const { t } = useI18n()

interface GraphExtractConfig {
  enabled: boolean
  text: string
  tags: string[]
  nodes: Node[]
  relations: Relation[]
}

interface Props {
  graphExtract: GraphExtractConfig
  allModels?: any[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:graphExtract': [value: GraphExtractConfig]
}>()

// 本地状态
const localGraphExtract = ref<GraphExtractConfig>({
  ...props.graphExtract,
  nodes: props.graphExtract.nodes || [],
  relations: props.graphExtract.relations || []
})

// 加载状态
const tagFabring = ref(false)
const textFabring = ref(false)
const extracting = ref(false)

// 模型状态
const modelStatus = ref({
  llm: {
    available: false,
    config: null as LLMConfig | null
  }
})

// 系统信息
const systemInfo = ref<any>(null)

// 计算图数据库是否启用
const isGraphDatabaseEnabled = computed(() => {
  return systemInfo.value?.graph_database_engine && systemInfo.value.graph_database_engine !== 'Not Enabled'
})

// Watch for prop changes
watch(() => props.graphExtract, (newVal) => {
  localGraphExtract.value = {
    ...newVal,
    nodes: newVal.nodes || [],
    relations: newVal.relations || []
  }
}, { deep: true })

// 处理配置变更
const handleConfigChange = () => {
  emit('update:graphExtract', localGraphExtract.value)
}

// 处理启用/禁用切换
const handleEnabledChange = () => {
  // 当关闭提取功能时，清空所有数据
  if (!localGraphExtract.value.enabled) {
    localGraphExtract.value.text = ''
    localGraphExtract.value.tags = []
    localGraphExtract.value.nodes = []
    localGraphExtract.value.relations = []
  }
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
    attributes: []
  })
  handleNodesChange()
}

const removeNode = (index: number) => {
  localGraphExtract.value.nodes.splice(index, 1)
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
  if (!modelStatus.value.llm.available || !modelStatus.value.llm.config) {
    MessagePlugin.warning(t('graphSettings.completeModelConfig'))
    return
  }
  
  tagFabring.value = true
  try {
    const response = await fabriTag({
      llm_config: modelStatus.value.llm.config
    })
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
  if (!modelStatus.value.llm.available || !modelStatus.value.llm.config) {
    MessagePlugin.warning(t('graphSettings.completeModelConfig'))
    return
  }
  
  textFabring.value = true
  try {
    const response = await fabriText({
      tags: localGraphExtract.value.tags,
      llm_config: modelStatus.value.llm.config
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
  if (!modelStatus.value.llm.available || !modelStatus.value.llm.config) {
    MessagePlugin.warning(t('graphSettings.completeModelConfig'))
    return
  }
  
  if (!localGraphExtract.value.text) {
    MessagePlugin.warning(t('graphSettings.pleaseInputText'))
    return
  }
  
  extracting.value = true
  try {
    const response = await extractTextRelations({
      text: localGraphExtract.value.text,
      tags: localGraphExtract.value.tags,
      llm_config: modelStatus.value.llm.config
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

// 加载模型状态
const loadModelStatus = async () => {
  try {
    const models = await listModels()
    
    // 查找可用的KnowledgeQA模型（对话模型）
    const llmModels = models.filter((m: any) => m.type === 'KnowledgeQA')
    if (llmModels.length > 0) {
      const llmModel = llmModels[0]
      modelStatus.value.llm.available = true
      modelStatus.value.llm.config = {
        source: llmModel.source,
        model_name: llmModel.name,
        base_url: llmModel.parameters?.base_url || '',
        api_key: llmModel.parameters?.api_key || ''
      }
    }
  } catch (error: any) {
    console.error('Failed to load model status:', error)
  }
}

// 加载系统信息
const loadSystemInfo = async () => {
  try {
    const response = await getSystemInfo()
    systemInfo.value = response.data
  } catch (error: any) {
    console.error('Failed to load system info:', error)
  }
}

const graphGuideUrl =
  import.meta.env.VITE_KG_GUIDE_URL ||
  'https://github.com/Tencent/WeKnora/blob/main/docs/KnowledgeGraph.md'

// Open guide documentation to show how to enable graph database
const handleOpenGraphGuide = () => {
  window.open(graphGuideUrl, '_blank', 'noopener')
}

// 初始化
onMounted(async () => {
  await Promise.all([
    loadModelStatus(),
    loadSystemInfo()
  ])
})
</script>

<style lang="less" scoped>
.graph-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
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
  padding: 20px 0;
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
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
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
  font-size: 13px;
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
  border-radius: 8px;
  padding: 16px;
}

.node-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;

  .node-icon {
    font-size: 20px;
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
  border-radius: 8px;

  .relation-select {
    flex: 1;
    min-width: 150px;
  }

  .relation-arrow {
    color: var(--td-text-color-secondary);
    font-size: 16px;
  }
}

.action-buttons {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
</style>