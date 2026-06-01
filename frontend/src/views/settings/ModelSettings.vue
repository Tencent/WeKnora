<template>
  <div class="model-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div class="section-header__text">
          <h2>{{ $t('modelSettings.title') }}</h2>
          <p class="section-description">{{ $t('modelSettings.description') }}</p>
        </div>
        <t-dropdown
          v-if="authStore.hasRole('admin')"
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
        <a class="doc-link" href="https://github.com/Tencent/WeKnora/blob/main/docs/BUILTIN_MODELS.md" target="_blank"
          rel="noopener noreferrer">
          {{ $t('modelSettings.builtinModels.viewGuide') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </div>
    </div>

    <t-tabs v-model="activeTypeFilter" class="model-type-tabs">
      <t-tab-panel value="all" :label="`${$t('common.all')}(${allLegacyModels.length})`" />
      <t-tab-panel value="chat" :label="`${$t('modelSettings.typeShort.chat')}(${countByType('chat')})`" />
      <t-tab-panel value="embedding"
        :label="`${$t('modelSettings.typeShort.embedding')}(${countByType('embedding')})`" />
      <t-tab-panel value="rerank" :label="`${$t('modelSettings.typeShort.rerank')}(${countByType('rerank')})`" />
      <t-tab-panel value="vllm" :label="`${$t('modelSettings.typeShort.vllm')}(${countByType('vllm')})`" />
      <t-tab-panel value="asr" :label="`${$t('modelSettings.typeShort.asr')}(${countByType('asr')})`" />
    </t-tabs>

    <t-loading :loading="loading" size="small" class="model-list-loading">
      <div v-if="filteredModels.length > 0" class="model-grid">
        <div
          v-for="model in filteredModels"
          :key="`${model._modelType}-${model.id}`"
          class="model-card"
          :class="[
            `model-card--${model._modelType}`,
            {
              'model-card--builtin': model.isBuiltin,
              'model-card--clickable': isModelCardClickable(model),
            },
          ]"
          :role="isModelCardClickable(model) ? 'button' : undefined"
          :tabindex="isModelCardClickable(model) ? 0 : undefined"
          @click="onModelCardClick($event, model._modelType, model)"
          @keydown.enter="onModelCardClick($event, model._modelType, model)"
        >
          <div class="model-card__badge" :aria-label="typeLabel(model._modelType)">
            <t-icon :name="typeIcon(model._modelType)" size="18px" />
          </div>
          <div class="model-card__body">
            <div class="model-card__header">
              <h3 class="model-card__title">{{ modelDisplayName(model) }}</h3>
              <span
                v-if="model.isBuiltin"
                class="model-card__lock"
                :title="$t('modelSettings.builtinTag')"
                :aria-label="$t('modelSettings.builtinTag')"
              >
                <t-icon name="lock-on" />
              </span>
              <div v-if="getModelOptions(model._modelType, model).length > 0" class="model-card__actions" @click.stop>
                <t-dropdown
                  :options="getModelOptions(model._modelType, model)"
                  placement="bottom-right"
                  attach="body"
                  trigger="click"
                  @click="(data: any) => handleMenuAction({ value: data.value }, model._modelType, model)"
                >
                  <t-button variant="text" shape="square" size="small" class="model-card__more">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
              </div>
            </div>
            <p class="model-card__subtitle">
              <span>{{ vendorLabel(model) }}</span>
              <template v-if="model._modelType === 'embedding' && model.dimension">
                <span class="model-card__sep">·</span>
                <span>{{ $t('model.editor.dimensionLabel') }} {{ model.dimension }}</span>
              </template>
              <template v-if="model._modelType === 'chat' && model.supportsVision">
                <span class="model-card__sep">·</span>
                <span
                  class="model-card__vision"
                  :title="$t('model.editor.supportsVisionLabel')"
                  :aria-label="$t('model.editor.supportsVisionLabel')"
                >
                  <t-icon name="image" size="12px" />
                </span>
              </template>
            </p>
          </div>
        </div>
      </div>
      <div v-else-if="!loading" class="empty-state">
        <t-empty :description="emptyHint">
          <t-dropdown
            v-if="authStore.hasRole('admin')"
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
    </t-loading>

    <!-- 模型编辑器抽屉 -->
    <ModelEditorDialog v-model:visible="showDialog" :model-type="currentModelType" :model-data="editingModel"
      @confirm="handleModelSave" />

  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import ModelEditorDialog from '@/components/ModelEditorDialog.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { listModels, createModel, updateModel as updateModelAPI, deleteModel as deleteModelAPI, type ModelConfig } from '@/api/model'
import { useAuthStore } from '@/stores/auth'

const { t, te } = useI18n()
const authStore = useAuthStore()
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
// apiKey is always blank here: the server's main GET response does not
// include it (see internal/handler/dto/model.go — ModelParametersDTO omits
// secret fields). Credential read/write happens inside the editor dialog
// via the dedicated /credentials subresource.
function convertToLegacyFormat(model: ModelConfig) {
  return {
    id: model.id!,
    name: model.name,
    displayName: model.display_name || '',
    source: model.source,
    modelName: model.name,
    baseUrl: model.parameters.base_url || '',
    apiKey: '',
    provider: model.parameters.provider || '',
    dimension: model.parameters.embedding_parameters?.dimension,
    isBuiltin: model.is_builtin || false,
    supportsVision: model.parameters.supports_vision || false,
    customHeaders: model.parameters.custom_headers
      ? Object.entries(model.parameters.custom_headers).map(([key, value]) => ({ key, value: String(value) }))
      : [],
    _modelType: backendTypeToModelType[model.type] || 'chat' as ModelType,
    // Preserve the credential metadata map so the editor dialog can render
    // the "Configured" state without an extra round-trip.
    credentials: model.credentials,
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

// 类型徽章图标。沿用 TDesign 自带 icon name，避免再引第三方图标包。
const typeIcon = (type: ModelType): string => {
  const map: Record<ModelType, string> = {
    chat: 'chat',
    embedding: 'chart-bubble',
    rerank: 'filter-sort',
    vllm: 'image',
    asr: 'sound',
  }
  return map[type]
}

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

// Maps a backend `provider` id (e.g. "openai", "aliyun", "weknoracloud")
// to its localized short label. Reuses the same i18n keys the editor's
// provider dropdown uses, so the model card and the editor stay in sync
// when a provider is renamed. Falls back to '' when the backend didn't
// store a provider — caller falls back to sourceLabel().
const providerLabel = (model: any): string => {
  const id = model.provider
  if (!id) return ''
  const key = `model.editor.providers.${id}.label`
  return te(key) ? t(key) : id
}

// What the vendor chip on a card shows. Keeps the chip text uniformly
// short so cards line up:
//   local  → "Ollama"
//   remote → provider's localized short name (e.g. "腾讯云 LKEAP",
//            "阿里云 DashScope"). For the catch-all "generic" provider
//            we render a single short word ("自定义" / "Custom") — the
//            editor dropdown's longer "自定义 (OpenAI兼容接口)" label
//            blows out the card chip row, and the "OpenAI 兼容" framing
//            isn't meaningful to most end users (they didn't pick "I
//            want OpenAI compatibility", they just pasted a base URL).
const vendorLabel = (model: any): string => {
  if (model.source === 'local') return 'Ollama'
  if (model.provider === 'generic') {
    return t('modelSettings.source.custom')
  }
  return providerLabel(model) || sourceLabel(model._modelType)
}

const modelDisplayName = (model: any) => {
  const displayName = typeof model.displayName === 'string' ? model.displayName.trim() : ''
  return displayName || model.name
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

// 可点击打开编辑抽屉：管理员 + 非内置模型
const isModelCardClickable = (model: any) =>
  authStore.hasRole('admin') && !model.isBuiltin

const onModelCardClick = (event: Event, type: ModelType, model: any) => {
  if (!isModelCardClickable(model)) return
  if (event.type === 'keydown') {
    const ke = event as KeyboardEvent
    if (ke.key !== 'Enter' && ke.key !== ' ') return
    ke.preventDefault()
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('.model-card__actions')) return
  editModel(type, model)
}

// 编辑模型
const editModel = (type: ModelType, model: any) => {
  if (model.isBuiltin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotEdit'))
    return
  }
  if (!authStore.hasRole('admin')) {
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

    if (modelData.displayName && modelData.displayName.trim().length > 100) {
      MessagePlugin.warning(t('modelSettings.toasts.displayNameTooLong'))
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

    // api_key flows in only on initial create (modelData.apiKey is wiped on
    // every edit-mode open). Edits to existing models commit credentials via
    // the /credentials subresource (handled inside ModelEditorDialog).
    const trimmedApiKey = (modelData.apiKey ?? '').trim()
    const apiKeyFields: { api_key?: string } =
      !editingModel.value && trimmedApiKey ? { api_key: trimmedApiKey } : {}

    const apiModelData: ModelConfig = {
      name: modelData.modelName.trim(),
      display_name: modelData.displayName?.trim() || '',
      type: getModelType(currentModelType.value),
      source: modelData.source,
      description: '',
      parameters: {
        base_url: modelData.baseUrl?.trim() || '',
        ...apiKeyFields,
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

  // Models are tenant-wide infrastructure (LLM credentials); the
  // backend gates every mutation behind Admin+ (see RegisterModelRoutes).
  // Non-Admins get an empty action menu — viewing is fine, but editing,
  // copying (also goes through createModel), and deleting are not.
  if (!authStore.hasRole('admin')) {
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
      display_name: source.display_name || '',
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

.builtin-models-hint .doc-link {
  font-size: 13px;
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

.model-list-loading {
  min-height: 120px;
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
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}

// 模型卡片 —— 可选类型徽章（仅「全部」Tab）+ 标题 + 一行副标题
.model-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease, transform 0.18s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }

  &--builtin {
    background: var(--td-bg-color-secondarycontainer);

    &:hover {
      box-shadow: none;
      border-color: var(--td-component-stroke);
    }
  }

  &--clickable {
    cursor: pointer;

    &:hover {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }
}

.model-card__badge {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  // 默认底色，被 type 修饰覆盖
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

// 5 种类型的徽章配色 —— 比原 tag 配色饱和度低一档，避免炫光
.model-card--chat .model-card__badge {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

.model-card--embedding .model-card__badge {
  background: rgba(98, 53, 187, 0.1);
  color: #6235BB;
}

.model-card--rerank .model-card__badge {
  background: rgba(184, 92, 0, 0.1);
  color: #B85C00;
}

.model-card--vllm .model-card__badge {
  background: rgba(201, 62, 62, 0.1);
  color: #C93E3E;
}

.model-card--asr .model-card__badge {
  background: rgba(17, 128, 83, 0.1);
  color: #118053;
}

.model-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
}

.model-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.model-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/*
  Built-in lock indicator. Most cards in a typical install ARE built-in,
  so loud styling everywhere becomes noise — instead the lock is muted
  and small by default, and lights up on hover. The signal that matters
  to users is "which models did I add" → user-added cards stand out by
  the absence of the lock.
*/
.model-card__lock {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  color: var(--td-text-color-placeholder);
  opacity: 0.6;
  transition: color 0.15s ease, opacity 0.15s ease;

  .t-icon {
    font-size: 13px;
  }
}

.model-card:hover .model-card__lock {
  opacity: 1;
  color: var(--td-text-color-secondary);
}

.model-card__subtitle {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-card__sep {
  margin: 0 4px;
  color: var(--td-text-color-placeholder);
}

.model-card__vision {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.model-card__actions {
  flex-shrink: 0;
}

.model-card__more {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  padding: 2px;
  opacity: 0;
  transition: opacity 0.15s ease;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

// Hover / 键盘焦点 时显示更多菜单，避免静态卡片上有"杂物"。
.model-card:hover .model-card__more,
.model-card:focus-within .model-card__more,
.model-card__actions:focus-within .model-card__more {
  opacity: 1;
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

</style>
