<template>
  <div class="model-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div class="section-header__text">
          <h2>{{ $t('modelSettings.title') }}</h2>
          <p class="section-description">{{ $t('modelSettings.description') }}</p>
        </div>
        <t-dropdown
          :options="addModelOptions"
          placement="bottom-right"
          @click="(data: any) => openAddDialog(data.value)"
        >
          <t-button theme="primary" variant="outline" size="small">
            <template #icon><add-icon /></template>
            {{ $t('modelSettings.actions.addModel') }}
          </t-button>
        </t-dropdown>
      </div>

      <div class="builtin-models-hint" role="note">
        <p class="builtin-hint-label">{{ $t('modelSettings.builtinModels.title') }}</p>
        <p class="builtin-hint-text">{{ $t('modelSettings.builtinModels.description') }}</p>
        <a
          class="doc-link"
          href="https://github.com/Tencent/WeKnora/blob/main/docs/BUILTIN_MODELS.md"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ $t('modelSettings.builtinModels.viewGuide') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </div>
    </div>

    <t-tabs v-model="activeTypeFilter" class="model-type-tabs">
      <t-tab-panel value="all" :label="`${$t('common.all')}(${allLegacyModels.length})`" />
      <t-tab-panel value="chat" :label="`${$t('modelSettings.typeShort.chat')}(${countByType('chat')})`" />
      <t-tab-panel value="embedding" :label="`${$t('modelSettings.typeShort.embedding')}(${countByType('embedding')})`" />
      <t-tab-panel value="rerank" :label="`${$t('modelSettings.typeShort.rerank')}(${countByType('rerank')})`" />
      <t-tab-panel value="vllm" :label="`${$t('modelSettings.typeShort.vllm')}(${countByType('vllm')})`" />
      <t-tab-panel value="asr" :label="`${$t('modelSettings.typeShort.asr')}(${countByType('asr')})`" />
    </t-tabs>

    <div v-if="filteredModels.length > 0" class="model-grid">
      <SettingCard
        v-for="model in filteredModels"
        :key="`${model._modelType}-${model.id}`"
        :title="model.name"
        :disabled="model.isBuiltin"
        :actions="getModelOptions(model._modelType, model)"
        @action="(value: string) => handleMenuAction({ value }, model._modelType, model)"
      >
        <template #tags>
          <t-tag size="small" variant="light" :class="`model-type-tag model-type-tag--${model._modelType}`">
            {{ typeLabel(model._modelType) }}
          </t-tag>
          <t-tag size="small" variant="light-outline">
            {{ model.source === 'local' ? 'Ollama' : sourceLabel(model._modelType) }}
          </t-tag>
          <t-tag v-if="model.isBuiltin" theme="warning" size="small" variant="light">
            {{ $t('modelSettings.builtinTag') }}
          </t-tag>
        </template>
        <template #meta>
          <span v-if="model.baseUrl" class="model-meta-item" :title="model.baseUrl">
            <t-icon name="link" size="12px" />
            <span class="model-meta-text">{{ model.baseUrl }}</span>
          </span>
          <span v-else-if="model.source === 'local'" class="model-meta-item">
            <t-icon name="desktop" size="12px" />
            <span>Ollama local</span>
          </span>
          <span v-if="model._modelType === 'embedding' && model.dimension" class="model-meta-item">
            {{ $t('model.editor.dimensionLabel') }}: {{ model.dimension }}
          </span>
        </template>
      </SettingCard>
    </div>
    <div v-else class="empty-state">
      <t-empty :description="emptyHint">
        <t-dropdown
          :options="addModelOptions"
          placement="bottom"
          @click="(data: any) => openAddDialog(data.value)"
        >
          <t-button theme="primary" variant="outline" size="small">
            <template #icon><add-icon /></template>
            {{ $t('modelSettings.actions.addModel') }}
          </t-button>
        </t-dropdown>
      </t-empty>
    </div>

    <!-- 模型编辑器抽屉 -->
    <ModelEditorDialog
      v-model:visible="showDialog"
      :model-type="currentModelType"
      :model-data="editingModel"
      @confirm="handleModelSave"
    />

  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import ModelEditorDialog from '@/components/ModelEditorDialog.vue'
import SettingCard from '@/components/settings/SettingCard.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { listModels, createModel, updateModel as updateModelAPI, deleteModel as deleteModelAPI, type ModelConfig } from '@/api/model'

const { t } = useI18n()
const confirmDelete = useConfirmDelete()

type ModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'
type FilterType = 'all' | ModelType

const showDialog = ref(false)
const currentModelType = ref<ModelType>('chat')
const editingModel = ref<any>(null)
const loading = ref(true)
const activeTypeFilter = ref<FilterType>('all')

// 模型列表数据
const allModels = ref<ModelConfig[]>([])

// 后端 type → 前端分组 type 的映射
const backendTypeToModelType: Record<string, ModelType> = {
  KnowledgeQA: 'chat',
  Embedding: 'embedding',
  Rerank: 'rerank',
  VLLM: 'vllm',
  ASR: 'asr'
}

// 将后端模型格式转换为旧的前端格式（附带 _modelType 便于渲染）
function convertToLegacyFormat(model: ModelConfig) {
  return {
    id: model.id!,
    name: model.name,
    source: model.source,
    modelName: model.name,
    baseUrl: model.parameters.base_url || '',
    apiKey: model.parameters.api_key || '',
    provider: model.parameters.provider || '',
    dimension: model.parameters.embedding_parameters?.dimension,
    isBuiltin: model.is_builtin || false,
    supportsVision: model.parameters.supports_vision || false,
    customHeaders: model.parameters.custom_headers
      ? Object.entries(model.parameters.custom_headers).map(([key, value]) => ({ key, value: String(value) }))
      : [],
    _modelType: backendTypeToModelType[model.type] || 'chat' as ModelType
  }
}

// 平铺 + 过滤
const allLegacyModels = computed(() => allModels.value.map(convertToLegacyFormat))
const filteredModels = computed(() => {
  if (activeTypeFilter.value === 'all') return allLegacyModels.value
  return allLegacyModels.value.filter(m => m._modelType === activeTypeFilter.value)
})

const countByType = (type: ModelType) => allLegacyModels.value.filter(m => m._modelType === type).length

// "+新增模型" 下拉菜单
const addModelOptions = computed(() => ([
  { content: t('modelSettings.typeShort.chat'), value: 'chat' },
  { content: t('modelSettings.typeShort.embedding'), value: 'embedding' },
  { content: t('modelSettings.typeShort.rerank'), value: 'rerank' },
  { content: t('modelSettings.typeShort.vllm'), value: 'vllm' },
  { content: t('modelSettings.typeShort.asr'), value: 'asr' }
]))

const typeLabel = (type: ModelType) => {
  const map: Record<ModelType, string> = {
    chat: t('modelSettings.typeShort.chat'),
    embedding: t('modelSettings.typeShort.embedding'),
    rerank: t('modelSettings.typeShort.rerank'),
    vllm: t('modelSettings.typeShort.vllm'),
    asr: t('modelSettings.typeShort.asr')
  }
  return map[type]
}

const sourceLabel = (type: ModelType) => {
  // vllm / asr 的 remote 文案特殊，其余走通用 remote 文案
  if (type === 'vllm' || type === 'asr') {
    return t('modelSettings.source.openaiCompatible')
  }
  return t('modelSettings.source.remote')
}

const emptyHint = computed(() => {
  if (activeTypeFilter.value === 'all') return t('modelSettings.chat.empty')
  const map: Record<ModelType, string> = {
    chat: t('modelSettings.chat.empty'),
    embedding: t('modelSettings.embedding.empty'),
    rerank: t('modelSettings.rerank.empty'),
    vllm: t('modelSettings.vllm.empty'),
    asr: t('modelSettings.asr.empty')
  }
  return map[activeTypeFilter.value as ModelType]
})

// 加载模型列表
const loadModels = async () => {
  loading.value = true
  try {
    const models = await listModels()
    allModels.value = models
  } catch (error: any) {
    console.error('加载模型列表失败:', error)
    MessagePlugin.error(error.message)
  } finally {
    loading.value = false
  }
}

// 打开添加对话框
const openAddDialog = (type: ModelType) => {
  currentModelType.value = type
  editingModel.value = null
  showDialog.value = true
}

// 编辑模型
const editModel = (type: ModelType, model: any) => {
  if (model.isBuiltin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotEdit'))
    return
  }
  currentModelType.value = type
  editingModel.value = { ...model }
  showDialog.value = true
}

// 保存模型
const handleModelSave = async (modelData: any) => {
  try {
    if (!modelData.modelName || !modelData.modelName.trim()) {
      MessagePlugin.warning(t('modelSettings.toasts.nameRequired'))
      return
    }

    if (modelData.modelName.trim().length > 100) {
      MessagePlugin.warning(t('modelSettings.toasts.nameTooLong'))
      return
    }

    if (modelData.source === 'remote') {
      if (!modelData.baseUrl || !modelData.baseUrl.trim()) {
        MessagePlugin.warning(t('modelSettings.toasts.baseUrlRequired'))
        return
      }

      try {
        new URL(modelData.baseUrl.trim())
      } catch {
        MessagePlugin.warning(t('modelSettings.toasts.baseUrlInvalid'))
        return
      }
    }

    if (currentModelType.value === 'embedding') {
      if (!modelData.dimension || modelData.dimension < 128 || modelData.dimension > 4096) {
        MessagePlugin.warning(t('modelSettings.toasts.dimensionInvalid'))
        return
      }
    }

    const customHeadersMap: Record<string, string> = {}
    if (Array.isArray(modelData.customHeaders)) {
      for (const item of modelData.customHeaders) {
        const key = (item?.key ?? '').trim()
        const value = (item?.value ?? '').trim()
        if (key && value) {
          customHeadersMap[key] = value
        }
      }
    }

    const apiModelData: ModelConfig = {
      name: modelData.modelName.trim(),
      type: getModelType(currentModelType.value),
      source: modelData.source,
      description: '',
      parameters: {
        base_url: modelData.baseUrl?.trim() || '',
        api_key: modelData.apiKey?.trim() || '',
        provider: modelData.provider || '',
        ...(Object.keys(customHeadersMap).length > 0 ? { custom_headers: customHeadersMap } : {}),
        ...(currentModelType.value === 'embedding' && modelData.dimension ? {
          embedding_parameters: {
            dimension: modelData.dimension,
            truncate_prompt_tokens: 0
          }
        } : {}),
        ...(currentModelType.value === 'vllm' ? {
          supports_vision: true
        } : currentModelType.value === 'chat' ? {
          supports_vision: modelData.supportsVision ?? false
        } : {})
      }
    }

    if (editingModel.value && editingModel.value.id) {
      await updateModelAPI(editingModel.value.id, apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.updated'))
    } else {
      await createModel(apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.added'))
    }

    showDialog.value = false
    await loadModels()
  } catch (error: any) {
    console.error('保存模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.saveFailed'))
  }
}

// 删除模型
const deleteModel = async (_type: ModelType, modelId: string) => {
  const model = allModels.value.find(m => m.id === modelId)
  if (model?.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotDelete'))
    return
  }

  try {
    await deleteModelAPI(modelId)
    MessagePlugin.success(t('modelSettings.toasts.deleted'))
    await loadModels()
  } catch (error: any) {
    console.error('删除模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.deleteFailed'))
  }
}

// 获取模型操作菜单选项
const getModelOptions = (type: ModelType, model: any) => {
  const options: any[] = []

  if (model.isBuiltin) {
    return options
  }

  options.push({
    content: t('common.edit'),
    value: `edit-${type}-${model.id}`
  })

  options.push({
    content: t('common.copy'),
    value: `copy-${type}-${model.id}`
  })

  options.push({
    content: t('common.delete'),
    value: `delete-${type}-${model.id}`,
    theme: 'error'
  })

  return options
}

// 处理菜单操作
const handleMenuAction = (data: { value: string }, type: ModelType, model: any) => {
  const value = data.value

  if (value.indexOf('edit-') === 0) {
    editModel(type, model)
  } else if (value.indexOf('copy-') === 0) {
    copyModel(type, model.id)
  } else if (value.indexOf('delete-') === 0) {
    confirmDelete({
      body: t('modelSettings.confirmDelete'),
      onConfirm: () => deleteModel(type, model.id)
    })
  }
}

// 生成不重复的复制名称
const generateCopyName = (originalName: string): string => {
  const suffix = t('modelSettings.copySuffix')
  const existingNames = new Set(allModels.value.map(m => m.name))
  let candidate = `${originalName}${suffix}`
  let counter = 2
  while (existingNames.has(candidate)) {
    candidate = `${originalName}${suffix} ${counter}`
    counter += 1
  }
  return candidate
}

// 复制模型
const copyModel = async (_type: ModelType, modelId: string) => {
  const source = allModels.value.find(m => m.id === modelId)
  if (!source) {
    return
  }
  if (source.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotCopy'))
    return
  }

  try {
    const newModel: ModelConfig = {
      name: generateCopyName(source.name),
      type: source.type,
      source: source.source,
      description: source.description || '',
      parameters: JSON.parse(JSON.stringify(source.parameters || {}))
    }

    await createModel(newModel)
    MessagePlugin.success(t('modelSettings.toasts.copied'))
    await loadModels()
  } catch (error: any) {
    console.error('复制模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.copyFailed'))
  }
}

// 获取后端模型类型
function getModelType(type: ModelType): 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM' | 'ASR' {
  const typeMap = {
    chat: 'KnowledgeQA' as const,
    embedding: 'Embedding' as const,
    rerank: 'Rerank' as const,
    vllm: 'VLLM' as const,
    asr: 'ASR' as const
  }
  return typeMap[type]
}

onMounted(() => {
  loadModels()
})
</script>

<style lang="less" scoped>
.model-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;
}

.builtin-models-hint {
  margin-top: 4px;
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
}

.builtin-hint-label {
  margin: 0 0 4px 0;
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-placeholder);
  letter-spacing: 0.02em;
}

.builtin-hint-text {
  margin: 0 0 6px 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.section-header__top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 8px;

  .section-header__text {
    flex: 1;
    min-width: 0;
  }

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
    line-height: 1.6;
  }

  :deep(.t-button) {
    flex-shrink: 0;
    margin-top: 4px;
  }
}

.model-type-tabs {
  margin-bottom: 16px;

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
  }

  :deep(.t-tabs__nav-item-wrapper) {
    padding: 0 12px;
    margin: 0;
  }

  :deep(.t-tabs__operations) {
    display: none;
  }

  :deep(.t-tabs__nav-scroll) {
    overflow-x: auto;
    scrollbar-width: none;

    &::-webkit-scrollbar {
      display: none;
    }
  }

  :deep(.t-tabs__content) {
    display: none;
  }
}

.model-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.model-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  overflow: hidden;

  .model-meta-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

// 5 种模型类型各自的 tag 配色
:deep(.model-type-tag) {
  &--chat {
    background: #E8F3FF;
    color: #0052D9;
  }

  &--embedding {
    background: #F0E9FF;
    color: #6235BB;
  }

  &--rerank {
    background: #FEF3E6;
    color: #B85C00;
  }

  &--vllm {
    background: #FEECEC;
    color: #C93E3E;
  }

  &--asr {
    background: #E7F7F2;
    color: #118053;
  }
}

.empty-state {
  padding: 64px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

:deep(.t-tag) {
  border-radius: 3px;
  padding: 2px 8px;
  font-size: 11px;
  font-weight: 500;
  border: none;

  &.t-tag--theme-primary {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }

  &.t-size-s {
    height: 20px;
    line-height: 16px;
  }
}
</style>
