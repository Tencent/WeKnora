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

    <div v-if="filteredModels.length > 0" class="model-grid">
      <!--
        Model card. 我们刻意不复用 SettingCard：模型卡需要左侧类型徽章 + 多
        级元信息（chip 行 + monospace 原名 + baseUrl），SettingCard 还在
        Mcp / WebSearch 页用，加 prefix 槽属于过度抽象。
      -->
      <div
        v-for="model in filteredModels"
        :key="`${model._modelType}-${model.id}`"
        class="model-card"
        :class="[`model-card--${model._modelType}`, { 'model-card--builtin': model.isBuiltin }]"
      >
        <div class="model-card__badge" :aria-label="typeLabel(model._modelType)">
          <t-icon :name="typeIcon(model._modelType)" size="18px" />
        </div>
        <div class="model-card__body">
          <!--
            Title row. Display name primary; on hover the lock (builtin) and
            ellipsis menu fade in from the right. The lock badge is muted by
            default since most cards in a typical install ARE built-in —
            making it loud everywhere just produces visual noise. User-added
            cards stand out by NOT having a lock.
          -->
          <div class="model-card__header">
            <h3 class="model-card__title" :title="modelDisplayName(model)">{{ modelDisplayName(model) }}</h3>
            <span
              v-if="model.isBuiltin"
              class="model-card__lock"
              :title="$t('modelSettings.builtinTag')"
              :aria-label="$t('modelSettings.builtinTag')"
            >
              <t-icon name="lock-on" />
            </span>
            <t-dropdown
              v-if="getModelOptions(model._modelType, model).length > 0"
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

          <!--
            Compact identity row. Only rendered when there's actually
            something to show — most built-in models have no displayName
            AND no baseUrl, so an "always render" approach left them with
            either a blank line or a noisy pseudo-URL. CSS grid's default
            row stretching keeps cards in the same row aligned, so we
            don't need to fake content for visual symmetry.

            When both raw name and URL are present we show ONLY the URL
            here, because cramming both into one ellipsizing line — as
            seen in the screenshot — produced "deepseek-… · https://…tencen…"
            with both ends truncated and neither readable. The raw name
            is already accessible via the title attribute on the card.
          -->
          <div
            v-if="identityVisible(model)"
            class="model-card__identity"
            :title="identityTooltip(model)"
          >
            <!-- Show URL by preference (it's the more diagnostic of the
                 two for "is this model wired up correctly"); fall back to
                 the raw name when there's no URL (display-name-only case). -->
            <span class="model-card__identity-text">{{ identityText(model) }}</span>
          </div>

          <!--
            Meta chips, single row. We deliberately keep this line to a
            FIXED set of facts so every card renders to the same height,
            no matter what optional fields are filled out. Order: type →
            vendor → optional dim → optional vision flag. Type chip is
            text-only (the 36×36 badge on the left already shows the icon).
          -->
          <div class="model-card__meta">
            <span class="model-card__chip model-card__chip--type">
              {{ typeLabel(model._modelType) }}
            </span>
            <span class="model-card__chip">
              {{ vendorLabel(model) }}
            </span>
            <span v-if="model._modelType === 'embedding' && model.dimension" class="model-card__chip">
              {{ model.dimension }} dim
            </span>
            <span v-if="model._modelType === 'chat' && model.supportsVision"
              class="model-card__chip model-card__chip--icon-only"
              :title="$t('model.editor.supportsVisionLabel')"
              :aria-label="$t('model.editor.supportsVisionLabel')">
              <t-icon name="image" />
            </span>
          </div>
        </div>
      </div>
    </div>
    <div v-else class="empty-state">
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

// Hover tooltip for the whole card — shows the long-form details we
// removed from the visible card body so they're still one mouseover
// away. baseUrl is the most useful for debugging "why is this model
// failing" scenarios.
const cardTooltip = (model: any): string => {
  const lines: string[] = []
  if (model.displayName && model.displayName !== model.name) {
    lines.push(`${t('modelSettings.rawModelName')}: ${model.name}`)
  }
  if (model.baseUrl) {
    lines.push(model.baseUrl)
  } else if (model.source === 'local') {
    lines.push('Ollama (localhost)')
  }
  return lines.join('\n')
}

// Whether the raw model identifier is worth showing on the card. We hide
// it when the user did NOT set a display name, because then the title is
// already the raw name and printing it again is just noise.
const rawNameVisible = (model: any): boolean => {
  const displayName = typeof model.displayName === 'string' ? model.displayName.trim() : ''
  return Boolean(displayName) && displayName !== model.name
}

// What goes in the URL slot of the identity row. Local models intentionally
// return '' here — "ollama://localhost" is just noise on Ollama-only built-in
// cards. Only remote models with an explicit base URL get this row.
const urlText = (model: any): string => {
  return model.baseUrl || ''
}

// Single-line text shown in the identity row. Picks the more useful of
// the two (URL > raw name) — never crams both into one ellipsizing line
// because that produces double-end truncation that nobody can read.
const identityText = (model: any): string => {
  return urlText(model) || (rawNameVisible(model) ? model.name : '')
}

// Whether the identity row should render at all.
const identityVisible = (model: any): boolean => identityText(model).length > 0

// Identity-row tooltip — exposes BOTH name and URL when the user hovers,
// so the diagnostic info we hid from the visible row is still one mouse
// move away.
const identityTooltip = (model: any): string => {
  const parts: string[] = []
  if (rawNameVisible(model)) parts.push(model.name)
  if (urlText(model)) parts.push(urlText(model))
  return parts.join('\n')
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

// 模型卡片 —— 左侧类型徽章 + 标题 / identity / 元 chip 行（固定三行）
.model-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
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
  gap: 4px;
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
  Generic chip used for: built-in tag, type chip, source chip, dimension,
  vision flag. Same shape across all so the row reads as one consistent
  rhythm of pills. Variants tweak color only.
*/
.model-card__chip {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 7px 1px 6px;
  height: 20px;
  font-size: 11px;
  font-weight: 500;
  line-height: 18px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-component);
  border-radius: 4px;
  white-space: nowrap;

  .t-icon {
    font-size: 12px;
    flex-shrink: 0;
  }
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

/*
  Icon-only chip variant. Drops horizontal padding to a tight square so the
  chip reads as a status badge (vision flag) rather than a text pill that
  happens to start with an icon.
*/
.model-card__chip--icon-only {
  padding: 0;
  width: 20px;
  justify-content: center;

  .t-icon {
    font-size: 12px;
  }
}

/* Type chip in the meta row — slightly emphasized, picks up the type's
   accent color so it links to the left badge. */
.model-card__chip--type {
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.model-card--chat .model-card__chip--type {
  color: #0052D9;
  background: rgba(0, 82, 217, 0.08);
}

.model-card--embedding .model-card__chip--type {
  color: #6235BB;
  background: rgba(98, 53, 187, 0.08);
}

.model-card--rerank .model-card__chip--type {
  color: #B85C00;
  background: rgba(184, 92, 0, 0.08);
}

.model-card--vllm .model-card__chip--type {
  color: #C93E3E;
  background: rgba(201, 62, 62, 0.08);
}

.model-card--asr .model-card__chip--type {
  color: #118053;
  background: rgba(17, 128, 83, 0.08);
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

// Hover / 键盘焦点 / 菜单已展开 时显示，避免静态卡片上有"杂物"。
.model-card:hover .model-card__more,
.model-card:focus-within .model-card__more {
  opacity: 1;
}

.model-card__meta {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 4px;
  min-width: 0;
  // Truncate at the row level rather than within each chip — chips clip
  // off-screen if the card narrows below the chip set's natural width
  // (rare at 320px+ minmax but possible if grid recomputes).
  overflow: hidden;
}

/*
  Compact identity row: monospace one-liner showing whichever of
  baseUrl / raw name is more useful (URL preferred). Conditionally
  rendered — empty cards (most built-in ones) skip this row entirely
  and grid auto-sizing handles the height.
*/
.model-card__identity {
  display: flex;
  align-items: center;
  min-width: 0;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 11px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
}

.model-card__identity-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
