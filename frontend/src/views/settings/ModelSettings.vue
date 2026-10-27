<template>
  <div class="model-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div class="model-title">
          <h2>{{ $t('modelSettings.title') }}</h2>
          <t-popup placement="bottom-left" trigger="hover" show-arrow>
            <button type="button" class="model-title-help" :aria-label="$t('modelSettings.builtinModels.title')">
              <t-icon name="help-circle" size="16px" />
            </button>
            <template #content>
              <div class="builtin-models-hint" role="note">
                <p class="builtin-hint-label">{{ $t('modelSettings.builtinModels.title') }}</p>
                <p class="builtin-hint-text">
                  {{ $t(authStore.isSystemAdmin
                    ? 'modelSettings.builtinModels.descriptionAdmin'
                    : 'modelSettings.builtinModels.description') }}
                </p>
                <a class="doc-link" :href="docsUrl('models')" target="_blank"
                  rel="noopener noreferrer">
                  {{ $t('modelSettings.builtinModels.viewGuide') }}
                  <t-icon name="link" class="link-icon" />
                </a>
              </div>
            </template>
          </t-popup>
        </div>
        <div class="model-header-actions">
          <t-button
            v-if="authStore.isSystemAdmin"
            type="button"
            theme="default"
            variant="text"
            size="small"
            class="model-header-action"
            @click="uiStore.openSettings('model-catalog')"
          >
            <template #icon><t-icon name="control-platform" /></template>
            {{ $t('modelCatalog.title') }}
          </t-button>
          <t-button
            v-if="authStore.hasRole('admin')"
            type="button"
            theme="default"
            variant="text"
            size="small"
            class="model-header-action"
            @click="showDebugDrawer = true"
          >
            <template #icon><play-circle-icon /></template>
            {{ $t('modelSettings.actions.debugModel') }}
          </t-button>
          <t-button v-if="authStore.hasRole('admin')" theme="default" variant="text" size="small"
            class="model-header-action" @click="openAddDialog">
            <template #icon><add-icon /></template>
            {{ $t('modelSettings.actions.addModel') }}
          </t-button>
        </div>
      </div>
      <p class="section-description">{{ $t('modelSettings.description') }}</p>
    </div>

    <div class="model-tabbar" data-guide="settings-models">
      <t-tabs v-model="activeTypeFilter" class="model-type-tabs">
        <t-tab-panel v-for="tab in modelTypeTabs" :key="tab.value" :value="tab.value">
          <template #label>
            <span class="model-type-tab">
              <t-icon :name="tab.icon" size="16px" />
              <span>{{ tab.label }}</span>
              <span class="model-tab-count">{{ tab.count }}</span>
            </span>
          </template>
        </t-tab-panel>
      </t-tabs>
      <t-input v-model="modelQuery" class="model-search" :placeholder="$t('menu.search')"
        :aria-label="$t('menu.search')" clearable>
        <template #prefix-icon><t-icon name="search" size="16px" /></template>
      </t-input>
    </div>

    <div class="model-list">
      <t-loading :loading="loading" size="small" class="model-list-loading">
        <div v-if="!loading && filteredModels.length === 0 && modelQuery.trim()" class="empty-state">
          <t-empty :description="$t('common.noResult')" />
          <t-button variant="outline" @click="modelQuery = ''">{{ $t('common.clear') }}</t-button>
        </div>
        <div v-else-if="!loading && filteredModels.length === 0 && !authStore.hasRole('admin')" class="empty-state">
          <t-empty :description="emptyHint" />
        </div>
        <div v-else-if="!loading" class="model-grid">
          <div v-for="model in filteredModels" :key="`${model._modelType}-${model.id}`" class="model-card" :class="[
            `model-card--${model._modelType}`,
            {
              'model-card--builtin': model.isBuiltin,
              'model-card--clickable': isModelCardClickable(model),
            },
          ]" :role="isModelCardClickable(model) ? 'button' : undefined"
            :tabindex="isModelCardClickable(model) ? 0 : undefined"
            @click="onModelCardClick($event, model._modelType, model)"
            @keydown.enter="onModelCardClick($event, model._modelType, model)">
            <div class="model-card__badge" :aria-label="typeLabel(model._modelType)">
              <t-icon :name="typeIcon(model._modelType)" size="16px" />
            </div>
            <div class="model-card__body">
              <div class="model-card__header">
                <h3 class="model-card__title" :title="modelDisplayName(model)">{{ modelDisplayName(model) }}</h3>
                <span v-if="model.isBuiltin" class="model-card__lock" :title="$t('modelSettings.builtinTag')"
                  :aria-label="$t('modelSettings.builtinTag')">
                  <t-icon :name="authStore.isSystemAdmin ? 'edit-1' : 'lock-on'" />
                </span>
                <div v-if="canManageModel(model)" class="model-card__actions" @click.stop>
                  <t-dropdown :options="getModelOptions(model._modelType, model)" placement="bottom-right" attach="body"
                    trigger="click"
                    @click="(data: any) => handleMenuAction({ value: data.value }, model._modelType, model)">
                    <t-button variant="text" shape="square" size="small" class="model-card__action-btn model-card__more">
                      <t-icon name="ellipsis" />
                    </t-button>
                  </t-dropdown>
                  <t-popconfirm
                    v-if="canDeleteModel(model)"
                    :content="$t('modelSettings.confirmDelete', { name: modelDisplayName(model) })"
                    :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                    :cancel-btn="{ content: $t('common.cancel') }"
                    placement="bottom-right"
                    @confirm="deleteModel(model._modelType, model.id)"
                  >
                    <t-tooltip :content="$t('common.delete')" placement="top">
                      <t-button
                        theme="danger"
                        shape="square"
                        variant="text"
                        size="small"
                        class="model-card__action-btn model-card__delete"
                        @click.stop
                      >
                        <template #icon><t-icon name="delete" /></template>
                      </t-button>
                    </t-tooltip>
                  </t-popconfirm>
                </div>
              </div>
              <p class="model-card__subtitle">
                <span class="model-card__vendor">
                  <img v-if="vendorIcon(model)" :src="vendorIcon(model)" class="model-card__vendor-icon" alt="" />
                  <span>{{ vendorLabel(model) }}</span>
                </span>
                <template v-if="model._modelType === 'embedding' && model.dimension">
                  <span class="model-card__sep">·</span>
                  <span>{{ $t('model.editor.dimensionLabel') }} {{ model.dimension }}</span>
                </template>
                <template v-if="model._modelType === 'chat' || model._modelType === 'vllm'">
                  <span class="model-card__sep">·</span>
                  <span
                    class="model-card__ctx"
                    :class="{ 'model-card__ctx--default': isDefaultContextWindow(model.contextWindow) }"
                    :title="contextWindowTitle(model.contextWindow)"
                  >{{ formatContextWindow(model.contextWindow) }}</span>
                </template>
                <template v-if="(model._modelType === 'chat' && model.supportsVision)
                  || (model._modelType === 'embedding' && model.capabilities?.input?.includes('image'))">
                  <span class="model-card__sep">·</span>
                  <span class="model-card__vision" :title="$t('model.editor.supportsVisionLabel')"
                    :aria-label="$t('model.editor.supportsVisionLabel')">
                    <t-icon name="image" size="12px" />
                  </span>
                </template>
              </p>
            </div>
          </div>
          <button
            v-if="authStore.hasRole('admin')"
            type="button"
            class="model-card model-card--add"
            data-guide="settings-add-model"
            @click="openAddDialog"
          >
            <span class="model-card--add__icon" aria-hidden="true">
              <add-icon />
            </span>
            <span class="model-card--add__label">{{ $t('modelSettings.actions.addModel') }}</span>
          </button>
        </div>
      </t-loading>
    </div>

    <t-dialog
      v-model:visible="showUsageDialog"
      :header="$t('modelSettings.usage.title')"
      :footer="false"
      width="680px"
      destroy-on-close
    >
      <div v-if="usageConflict" class="model-usage-dialog">
        <p class="model-usage-dialog__description">
          {{ $t('modelSettings.usage.description', { name: usageConflictModelName }) }}
        </p>

        <div class="model-usage-dialog__content">
          <section v-if="usageConflict.knowledge_bases.length" class="model-usage-group">
            <h3>
              {{ $t('modelSettings.usage.knowledgeBases', {
                count: modelUsageResourceCount(usageConflict.knowledge_bases, usageConflict.knowledge_base_total)
              }) }}
            </h3>
            <ul>
              <li v-for="resource in usageConflict.knowledge_bases" :key="resource.id">
                <div class="model-usage-resource">
                  <strong>{{ resource.name || resource.id }}</strong>
                  <div class="model-usage-bindings">
                    <t-tag
                      v-for="(binding, index) in resource.bindings"
                      :key="`${binding}-${index}`"
                      size="small"
                      variant="light"
                    >
                      {{ $t(modelUsageBindingI18nKey(binding)) }}
                    </t-tag>
                  </div>
                </div>
                <t-button
                  theme="primary"
                  variant="text"
                  size="small"
                  @click="openUsageResource('knowledge_base', resource.id, resource.bindings)"
                >
                  {{ $t('modelSettings.usage.openConfiguration') }}
                </t-button>
              </li>
            </ul>
            <p
              v-if="modelUsageListTruncated(usageConflict.knowledge_bases, usageConflict.knowledge_base_total)"
              class="model-usage-truncated"
            >
              {{ $t('modelSettings.usage.truncated', {
                shown: usageConflict.knowledge_bases.length,
                total: modelUsageResourceCount(usageConflict.knowledge_bases, usageConflict.knowledge_base_total)
              }) }}
            </p>
          </section>

          <section v-if="usageConflict.agents.length" class="model-usage-group">
            <h3>{{ $t('modelSettings.usage.agents', {
              count: modelUsageResourceCount(usageConflict.agents, usageConflict.agent_total)
            }) }}</h3>
            <ul>
              <li v-for="resource in usageConflict.agents" :key="resource.id">
                <div class="model-usage-resource">
                  <strong>{{ resource.name || resource.id }}</strong>
                  <div class="model-usage-bindings">
                    <t-tag
                      v-for="(binding, index) in resource.bindings"
                      :key="`${binding}-${index}`"
                      size="small"
                      variant="light"
                    >
                      {{ $t(modelUsageBindingI18nKey(binding)) }}
                    </t-tag>
                  </div>
                </div>
                <t-button
                  theme="primary"
                  variant="text"
                  size="small"
                  @click="openUsageResource('agent', resource.id, resource.bindings)"
                >
                  {{ $t('modelSettings.usage.openConfiguration') }}
                </t-button>
              </li>
            </ul>
            <p
              v-if="modelUsageListTruncated(usageConflict.agents, usageConflict.agent_total)"
              class="model-usage-truncated"
            >
              {{ $t('modelSettings.usage.truncated', {
                shown: usageConflict.agents.length,
                total: modelUsageResourceCount(usageConflict.agents, usageConflict.agent_total)
              }) }}
            </p>
          </section>

          <section v-if="usageConflict.long_term_memory.bindings.length" class="model-usage-group">
            <h3>{{ $t('modelSettings.usage.longTermMemory') }}</h3>
            <div class="model-usage-memory">
              <div class="model-usage-bindings">
                <t-tag
                  v-for="(binding, index) in usageConflict.long_term_memory.bindings"
                  :key="`${binding}-${index}`"
                  size="small"
                  variant="light"
                >
                  {{ $t(modelUsageBindingI18nKey(binding)) }}
                </t-tag>
              </div>
            </div>
          </section>
        </div>

        <div class="model-usage-dialog__actions">
          <t-button @click="showUsageDialog = false">{{ $t('common.close') }}</t-button>
        </div>
      </div>
    </t-dialog>

    <!-- 模型编辑器抽屉 -->
    <ModelEditorDialog v-model:visible="showDialog" :model-type="currentModelType" :model-data="editingModel"
      :draft="modelDraft" :save-model="handleModelSave" />
    <ModelDebugDrawer v-model:visible="showDebugDrawer" :models="allModels" />

  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon, PlayCircleIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAssistantPageContext } from '@/composables/useAssistantPageContext'
import { useAssistantStore, type AssistantModelDraft } from '@/stores/assistant'
import ModelEditorDialog from '@/components/ModelEditorDialog.vue'
import ModelDebugDrawer from '@/components/ModelDebugDrawer.vue'
import {
  listModels,
  createModel,
  copyModelConfig,
  updateModel as updateModelAPI,
  deleteModel as deleteModelAPI,
  ModelInUseError,
  modelUsageBindingI18nKey,
  modelUsageKnowledgeBaseSection,
  modelUsageListTruncated,
  modelUsageResourceCount,
  modelUsageResourceRoute,
  type ModelConfig,
  type ModelUsageDetails,
  type ModelUsageResourceKind,
} from '@/api/model'
import { useAuthStore } from '@/stores/auth'
import { generateCopyDisplayName, modelCopyLabel } from '@/utils/modelCopyName'
import { useUIStore } from '@/stores/ui'
import { focusKbEditorSection } from '@/config/contextualGuides'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useModelProvidersStore } from '@/stores/modelProviders'
import { docsUrl } from '@/utils/docsUrl'
import { matchesResourceQuery } from '@/utils/resourceListSearch'
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import {
  formatContextWindow,
  isDefaultContextWindow,
  effectiveContextWindow,
} from '@/utils/contextWindow'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const uiStore = useUIStore()
const chatResources = useChatResourcesStore()
const providersStore = useModelProvidersStore()
const router = useRouter()
const route = useRoute()
const assistant = useAssistantStore()
type ModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'
type FilterType = 'all' | ModelType

const showDialog = ref(false)
const showDebugDrawer = ref(false)
const showUsageDialog = ref(false)
const usageConflict = ref<ModelUsageDetails | null>(null)
const usageConflictModelName = ref('')
const currentModelType = ref<ModelType>('chat')
const editingModel = ref<any>(null)
const modelDraft = ref<Record<string, unknown> | null>(null)
const loading = ref(true)
const activeTypeFilter = ref<FilterType>('all')
const modelQuery = ref('')

const MODEL_TAB_TYPES: ModelType[] = ['chat', 'embedding', 'rerank', 'vllm', 'asr']
const KNOWLEDGE_BASE_EDITOR_HOST_ROUTES = new Set([
  'home',
  'knowledgeBaseList',
  'knowledgeBaseDetail',
  'globalCreatChat',
  'kbCreatChat',
  'chat',
])
watch(
  () => uiStore.settingsInitialSubSection,
  (sub) => {
    if (sub && MODEL_TAB_TYPES.includes(sub as ModelType)) {
      activeTypeFilter.value = sub as FilterType
    }
  },
  { immediate: true },
)

// 模型列表数据
const allModels = ref<ModelConfig[]>([])
const copyingModelId = ref<string | null>(null)

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
    supportsDimensionOverride: model.parameters.embedding_parameters?.supports_dimension_override || false,
    isBuiltin: model.is_builtin || false,
    supportsVision: model.parameters.supports_vision || false,
    contextWindow: model.parameters.context_window || undefined,
    maxConcurrency: model.parameters.max_concurrency,
    maxOutputTokens: model.parameters.max_output_tokens || undefined,
    customHeaders: model.parameters.custom_headers
      ? Object.entries(model.parameters.custom_headers).map(([key, value]) => ({ key, value: String(value) }))
      : [],
    // 厂商额外字段 / 高级覆盖原样带入编辑器（thinking_control 单独拆出，仅旧数据才有）
    extraConfig: Object.fromEntries(
      Object.entries(model.parameters.extra_config || {}).filter(([key]) => key !== 'thinking_control'),
    ) as Record<string, string>,
    thinkingControl: model.parameters.extra_config?.thinking_control || '',
    spec: model.parameters.spec || null,
    capabilities: model.capabilities,
    _modelType: backendTypeToModelType[model.type] || 'chat' as ModelType,
    // Preserve the credential metadata map so the editor dialog can render
    // the "Configured" state without an extra round-trip.
    credentials: model.credentials,
  }
}

// 平铺 + 过滤
const allLegacyModels = computed(() => allModels.value.map(convertToLegacyFormat))
const filteredModels = computed(() => allLegacyModels.value.filter(model =>
  (activeTypeFilter.value === 'all' || model._modelType === activeTypeFilter.value)
  && matchesResourceQuery({ name: modelDisplayName(model), description: vendorLabel(model) }, modelQuery.value),
))

const modelTypeTabs = computed(() => [
  { value: 'all', label: t('common.all'), icon: 'view-module', count: allLegacyModels.value.length },
  ...MODEL_TAB_TYPES.map(value => ({ value, label: typeLabel(value), icon: typeIcon(value), count: countByType(value) })),
])

const countByType = (type: ModelType) => allLegacyModels.value.filter(m => m._modelType === type).length

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
// to the vendor's localized name from the backend catalog (modelProviders
// store), so the model card and the editor dropdown always agree. Falls
// back to the raw id when the catalog has not loaded yet, and to '' when
// the backend didn't store a provider — caller falls back to sourceLabel().
const providerLabel = (model: any): string => {
  const id = model.provider
  if (!id) return ''
  return providersStore.labelFor(id, String(locale.value || '')) || id
}

const vendorIcon = (model: any): string => {
  if (model.source === 'local' || !model.provider) return ''
  return providersStore.iconFor(model.provider)
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

useAssistantPageContext(() => {
  const model = showDialog.value ? editingModel.value : null
  return {
    page: 'settings_models',
    ...(model?.id ? { modelId: model.id, modelName: modelDisplayName(model) } : {}),
  }
})

const contextWindowTitle = (tokens?: number) => {
  if (isDefaultContextWindow(tokens)) {
    return t('model.editor.contextWindowDefaultHint', { value: formatContextWindow(tokens) })
  }
  return t('model.editor.contextWindowTokens', { count: effectiveContextWindow(tokens) })
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
  // 厂商图标 / 本地化名称来自目录 store；与模型列表并行加载，失败不影响卡片渲染。
  void providersStore.ensureLoaded('').catch(() => {})
  try {
    const models = await listModels()
    allModels.value = models
    // 设置页自己 listModels 之后立刻写回空间级缓存。否则对话输入栏 /
    // 智能体编辑器会继续拿 60s TTL 里的旧 context_window，刷新页面才对。
    chatResources.replaceModels(models)
  } catch (error: any) {
    console.error('加载模型列表失败:', error)
    MessagePlugin.error(error.message)
  } finally {
    loading.value = false
  }
}

// 打开添加对话框；类型在抽屉内选择，此处仅按当前 Tab 预填默认值
const openAddDialog = () => {
  currentModelType.value = activeTypeFilter.value === 'all' ? 'chat' : activeTypeFilter.value
  editingModel.value = null
  showDialog.value = true
}

// Tenant Admin+ manages tenant models; only SystemAdmin manages shared
// built-in models. The backend repeats this distinction authoritatively.
const canEditModel = (model: any) =>
  model.isBuiltin ? authStore.isSystemAdmin : authStore.hasRole('admin')

const isModelCardClickable = (model: any) => canEditModel(model)

const canManageModel = (model: any) => canEditModel(model)

// Built-in lifecycle remains deployment-managed (YAML / SQL). The UI only
// exposes configuration and credential editing to SystemAdmin.
const canDeleteModel = (model: any) =>
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
  if (model.isBuiltin && !authStore.isSystemAdmin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotEdit'))
    return
  }
  if (!model.isBuiltin && !authStore.hasRole('admin')) {
    return
  }
  currentModelType.value = type
  editingModel.value = { ...model }
  showDialog.value = true
}

// 保存模型
const handleModelSave = async (modelData: any) => {
  const saveType: ModelType = modelData.modelType ?? currentModelType.value
  currentModelType.value = saveType

  try {
    if (!modelData.modelName || !modelData.modelName.trim()) {
      throw new Error(t('modelSettings.toasts.nameRequired'))
    }

    if (modelData.modelName.trim().length > 100) {
      throw new Error(t('modelSettings.toasts.nameTooLong'))
    }

    if (modelData.displayName && modelData.displayName.trim().length > 100) {
      throw new Error(t('modelSettings.toasts.displayNameTooLong'))
    }

    if (modelData.source === 'remote') {
      if (!modelData.baseUrl || !modelData.baseUrl.trim()) {
        throw new Error(t('modelSettings.toasts.baseUrlRequired'))
      }

      try {
        new URL(modelData.baseUrl.trim())
      } catch {
        throw new Error(t('modelSettings.toasts.baseUrlInvalid'))
      }
    }

    if (saveType === 'embedding') {
      if (!modelData.dimension || modelData.dimension < 128 || modelData.dimension > 4096) {
        throw new Error(t('modelSettings.toasts.dimensionInvalid'))
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
    const trimmedAppSecret = (modelData.appSecret ?? '').trim()
    const appSecretFields: { app_secret?: string } =
      !editingModel.value && trimmedAppSecret ? { app_secret: trimmedAppSecret } : {}
    // extra_config: vendor extra fields + advanced overrides (already trimmed
    // by the editor) plus the legacy thinking_control for rows that still
    // carry it. Empty values are dropped so cleared keys disappear.
    const extraConfig: Record<string, string> = {}
    if (modelData.source === 'remote') {
      for (const [key, value] of Object.entries(modelData.extraConfig || {})) {
        const trimmed = String(value ?? '').trim()
        if (key && trimmed) extraConfig[key] = trimmed
      }
      const legacyThinking = String(modelData.thinkingControl || '').trim()
      if (saveType === 'chat' && legacyThinking) {
        extraConfig.thinking_control = legacyThinking
      } else {
        delete extraConfig.thinking_control
      }
    }
    // Always send extra_config for remote models, even when it ends up empty:
    // PUT /models/:id restores the stored map when the field is absent
    // (internal/handler/model.go), so omitting it would make "clear the last
    // vendor field / protocol override" silently no-op. Local rows keep the
    // omit-when-empty behaviour — this form never edits their extra_config.
    const extraConfigFields = modelData.source === 'remote'
      ? { extra_config: extraConfig }
      : {}

    // parameters.spec: keep whatever the row already had, replace compat with
    // the (validated) JSON from the advanced textarea; drop spec when empty.
    const specText = String(modelData.specCompat ?? '').trim()
    const baseSpec = (modelData.spec && typeof modelData.spec === 'object') ? { ...modelData.spec } : {}
    if (specText) {
      const compat = JSON.parse(specText)
      if (!compat || typeof compat !== 'object' || Array.isArray(compat)) {
        throw new Error(t('model.editor.advanced.compat.invalid'))
      }
      baseSpec.compat = compat
    } else {
      delete baseSpec.compat
    }
    const specFields = Object.keys(baseSpec).length > 0 ? { spec: baseSpec } : {}

    const apiModelData: ModelConfig = {
      name: modelData.modelName.trim(),
      display_name: modelData.displayName?.trim() || '',
      type: getModelType(saveType),
      source: modelData.source,
      description: '',
      parameters: {
        base_url: modelData.baseUrl?.trim() || '',
        ...apiKeyFields,
        ...appSecretFields,
        provider: modelData.provider || '',
        ...extraConfigFields,
        ...(Object.keys(customHeadersMap).length > 0 ? { custom_headers: customHeadersMap } : {}),
        ...(saveType === 'embedding' && modelData.dimension ? {
          embedding_parameters: {
            dimension: modelData.dimension,
            truncate_prompt_tokens: 0,
            supports_dimension_override: modelData.supportsDimensionOverride ?? false
          }
        } : {}),
        ...(saveType === 'vllm' ? {
          supports_vision: true
        } : saveType === 'chat' ? {
          supports_vision: modelData.supportsVision ?? false
        } : {}),
        ...((saveType === 'chat' || saveType === 'vllm')
          && Number(modelData.contextWindow) >= 1024
          ? { context_window: Math.round(Number(modelData.contextWindow)) }
          : {}),
        ...((saveType === 'chat' || saveType === 'vllm')
          && Number(modelData.maxOutputTokens) > 0
          ? { max_output_tokens: Math.round(Number(modelData.maxOutputTokens)) }
          : {}),
        ...specFields,
        // 后台并发上限：仅 chat/embedding/vllm 受治理，>0 才写入（0/空沿用全局默认）。
        ...(['chat', 'embedding', 'vllm'].includes(saveType)
          && Number(modelData.maxConcurrency) > 0
          ? { max_concurrency: Number(modelData.maxConcurrency) }
          : {})
      }
    }

    if (editingModel.value && editingModel.value.id) {
      await updateModelAPI(editingModel.value.id, apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.updated'))
    } else {
      await createModel(apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.added'))
    }

    await loadModels()
  } catch (error: any) {
    console.error('保存模型失败:', error)
    throw error
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
    if (error instanceof ModelInUseError) {
      usageConflict.value = error.details
      usageConflictModelName.value = model?.display_name || model?.name || modelId
      showUsageDialog.value = true
      return
    }
    MessagePlugin.error(error.message || t('modelSettings.toasts.deleteFailed'))
  }
}

const openUsageResource = async (
  kind: ModelUsageResourceKind,
  id: string,
  bindings: readonly string[] = [],
) => {
  showUsageDialog.value = false
  const knowledgeBaseSection = modelUsageKnowledgeBaseSection(bindings)
  // The UI store can outlive the route component that owns the KB editor.
  // Only focus an existing editor when the current route still mounts one;
  // otherwise a stale visible/id pair would make a direct Settings page no-op.
  const routeName = router.currentRoute.value.name
  const canFocusExistingKnowledgeBase = kind === 'knowledge_base'
    && uiStore.showKBEditorModal
    && uiStore.currentKBId === id
    && typeof routeName === 'string'
    && KNOWLEDGE_BASE_EDITOR_HOST_ROUTES.has(routeName)

  uiStore.closeSettings()
  await nextTick()

  if (canFocusExistingKnowledgeBase) {
    // Keep unsaved edits when the referenced KB is already open underneath
    // global Settings; only move the existing editor to the relevant section.
    focusKbEditorSection(knowledgeBaseSection)
    return
  }

  if (kind === 'knowledge_base') {
    uiStore.closeKBEditor()
    await nextTick()
  }
  // Both the KB editor and global Settings can already be open underneath the
  // usage dialog. Flush their false state before reusing either component so
  // its visible watcher reloads the target object instead of keeping stale data.
  await router.push(modelUsageResourceRoute(kind, id, bindings))
  if (kind === 'knowledge_base') {
    uiStore.openKBSettings(id, knowledgeBaseSection)
  }
}

// 获取模型操作菜单选项
const getModelOptions = (type: ModelType, model: any) => {
  const options: any[] = []

  if (model.isBuiltin) {
    if (authStore.isSystemAdmin) {
      options.push({
        content: t('common.edit'),
        value: `edit-${type}-${model.id}`
      })
    }
    return options
  }

  // Models are tenant-wide infrastructure (LLM credentials); the
  // backend gates every mutation behind Admin+ (see RegisterModelRoutes).
  // Non-Admins get an empty action menu — viewing is fine, but editing,
  // copying (POST /models/:id/copy), and deleting are not.
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

  return options
}

// 处理菜单操作
const handleMenuAction = (data: { value: string }, type: ModelType, model: any) => {
  const value = data.value

  if (value.indexOf('edit-') === 0) {
    editModel(type, model)
  } else if (value.indexOf('copy-') === 0) {
    copyModel(type, model.id)
  }
}

// 复制模型。name 与凭证由服务端从源记录复制；这里只生成不重复的展示名。
const copyModel = async (_type: ModelType, modelId: string) => {
  const source = allModels.value.find(m => m.id === modelId)
  if (!source?.id || copyingModelId.value) {
    return
  }
  if (source.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotCopy'))
    return
  }

  copyingModelId.value = modelId
  try {
    const displayName = generateCopyDisplayName(
      modelCopyLabel(source),
      allModels.value.map(model => modelCopyLabel(model)),
      t('modelSettings.copySuffix'),
    )
    await copyModelConfig(source.id, displayName)
    MessagePlugin.success(t('modelSettings.toasts.copied'))
    await loadModels()
  } catch (error: any) {
    console.error('复制模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.copyFailed'))
  } finally {
    copyingModelId.value = null
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

// The assistant links here to edit a model (?model_id=) or to add one it
// filled in; the user enters the key and saves.
const openRequestedModel = () => {
  const id = typeof route.query.model_id === 'string' ? route.query.model_id : ''
  if (!id || loading.value) return
  const model = allLegacyModels.value.find(m => m.id === id)
  const { model_id: _id, ...rest } = route.query
  void router.replace({ path: route.path, query: rest })
  if (model) editModel(model._modelType, model)
  else MessagePlugin.warning(t('modelSettings.toasts.linkedModelMissing'))
}

const openModelDraft = (draft: AssistantModelDraft) => {
  if (!authStore.hasRole('admin')) return
  currentModelType.value = draft.type
  editingModel.value = null
  modelDraft.value = {
    ...(draft.source ? { source: draft.source } : {}),
    ...(draft.provider ? { provider: draft.provider } : {}),
    ...(draft.model_name ? { modelName: draft.model_name } : {}),
    ...(draft.display_name ? { displayName: draft.display_name } : {}),
    ...(draft.base_url ? { baseUrl: draft.base_url } : {}),
  }
  showDialog.value = true
}

watch(() => assistant.pageDraft, () => {
  const draft = assistant.takePageDraft('model')
  if (draft) openModelDraft(draft)
}, { immediate: true })
watch(showDialog, (open) => {
  if (!open) modelDraft.value = null
})
watch(() => route.query.model_id, openRequestedModel)

onMounted(async () => {
  await loadModels()
  openRequestedModel()
})
useAssistantDataChange(() => { void loadModels() }, { areas: ['model'] })
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/provider-card.less';
@import (reference) '@/components/css/settings-section.less';

.model-settings {
  width: 100%;
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  color: var(--td-text-color-primary);
  container-type: inline-size;
}

.section-header {
  .settings-section-header();
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 12px;
  flex-shrink: 0;

  &__top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px;
    // 为设置弹窗右上角的关闭按钮留出空间。
    padding-right: 32px;
  }

  h2 {
    margin: 0;
  }

}

.model-title {
  display: flex;
  align-items: center;
  gap: 6px;
}

.model-title-help {
  .settings-title-help();
}

.model-header-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.model-header-action.t-button {
  flex-shrink: 0;
  min-height: 32px;
  padding: 0 12px;
  gap: 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }

  :deep(.t-icon) {
    color: var(--td-brand-color);
  }
}

.builtin-models-hint {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-width: min(320px, calc(100vw - 64px));
  padding: 4px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 20px;
}

.builtin-hint-label {
  margin: 0;
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.builtin-hint-text {
  margin: 0;
}

.builtin-models-hint .doc-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--td-brand-color);
  text-decoration: none;

  &:hover { text-decoration: underline; }
}

.model-tabbar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 16px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.model-type-tabs {
  flex: 1;
  min-width: 0;
  background: transparent;

  :deep(.t-tabs__nav-container) { background: transparent; }
  :deep(.t-tabs__nav-container.t-is-top::after) { display: none; }
  :deep(.t-tabs__nav-item) {
    height: 44px;
    padding: 0;
    margin-right: 24px;
    font-size: var(--app-text-base);
    font-weight: 500;
    color: var(--td-text-color-secondary);

    &:last-child { margin-right: 0; }
    &:hover { background: transparent; color: var(--td-text-color-primary); }
  }
  :deep(.t-tabs__nav-item-wrapper) { padding: 0 2px; margin: 0; }
  :deep(.t-tabs__nav-item:not(.t-is-disabled):hover .t-tabs__nav-item-wrapper) { background: transparent; }
  :deep(.t-tabs__bar) { display: none; }
  :deep(.t-tabs__nav-item.t-is-active) {
    color: var(--td-text-color-primary);
    text-shadow: none;
    &::after {
      content: '';
      position: absolute;
      left: 0;
      right: 0;
      bottom: 0;
      height: 2px;
      border-radius: var(--app-radius-pill);
      background: var(--td-brand-color);
    }
    .t-icon { color: var(--td-brand-color); }
    .model-tab-count {
      background: var(--td-brand-color-light);
      color: var(--td-brand-color);
    }
  }
  :deep(.t-tabs__operations) { display: none; }
  :deep(.t-tabs__nav-scroll) {
    overflow-x: auto;
    scrollbar-width: none;
    &::-webkit-scrollbar { display: none; }
  }
  :deep(.t-tabs__content) { display: none; }
}

.model-type-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.model-tab-count {
  min-width: 20px;
  height: 20px;
  box-sizing: border-box;
  padding: 0 6px;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  font-weight: 500;
  line-height: 20px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.model-search {
  flex: 0 0 240px;
  width: 240px;
  :deep(.t-input) { border-radius: var(--app-radius-sm); }
}

.model-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: var(--app-space-4) 0 var(--app-space-8);
}

.model-list-loading {
  display: block;
  min-height: 120px;
}

.model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr));
  gap: 10px;
  align-items: stretch;

  .model-card--add { width: 100%; height: 100%; }
}

@container (max-width: 900px) {
  .model-tabbar { flex-wrap: wrap; gap: 0; }
  .model-type-tabs { flex-basis: 100%; }
  .model-search { flex: 1 1 100%; width: 100%; margin: 8px 0; }
}

// 模型卡片沿用工具箱的紧凑间距、圆角和悬浮反馈。
.model-card {
  .provider-card();
  align-items: center;
  min-height: 88px;
  padding: 12px;
  gap: 10px;
  box-sizing: border-box;
  border-radius: var(--app-radius-md);

  &--add {
    .provider-card-add();
    min-height: 88px;
    gap: 6px;

    &:hover,
    &:focus-visible {
      box-shadow: none;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: var(--app-radius-md);
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-secondary);
      font-size: var(--app-text-2xl);
    }

    &__label {
      font-size: var(--app-text-md);
      font-weight: 500;
      line-height: 1.4;
    }
  }

  &--clickable {
    .provider-card-interactive();

    &:hover {
      border-color: color-mix(in srgb, var(--td-brand-color) 40%, var(--td-component-stroke));
      box-shadow: 0 3px 12px color-mix(in srgb, var(--td-text-color-primary) 6%, transparent);
    }
  }
}

.model-card__badge {
  .provider-card-badge(28px, 8px);
  .provider-card-badge-color(#0052d9);
}

// 5 种类型的徽章配色 —— 比原 tag 配色饱和度低一档，避免炫光
.model-card--chat .model-card__badge {
  .provider-card-badge-color(#0052d9);
}

.model-card--embedding .model-card__badge {
  .provider-card-badge-color(#6235bb);
}

.model-card--rerank .model-card__badge {
  .provider-card-badge-color(#b85c00);
}

.model-card--vllm .model-card__badge {
  .provider-card-badge-color(#c93e3e);
}

.model-card--asr .model-card__badge {
  .provider-card-badge-color(#118053);
}

.model-card__body {
  .provider-card-body();
}

.model-card__header {
  .provider-card-header();
}

.model-card__title {
  .provider-card-title();
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
  transition: color var(--app-motion-fast) ease, opacity var(--app-motion-fast) ease;

  .t-icon {
    font-size: var(--app-text-md);
  }
}

.model-card:hover .model-card__lock {
  opacity: 1;
  color: var(--td-text-color-secondary);
}

.model-card__subtitle {
  // A flex row, not inline text: .model-card__vendor is an inline-flex box
  // whose baseline comes from its first item — the 14px icon, whose baseline
  // is its bottom edge — so as inline content it sat a couple of pixels off
  // the "· 200K" beside it. Aligning the row by centre instead of by
  // baseline puts every part of the line on one optical line.
  display: flex;
  align-items: center;
  min-width: 0;
  margin: 2px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.model-card__vendor {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;

  // The vendor name is the only part long enough to need truncating; the
  // context window and the badges after it must stay readable.
  > span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.model-card__sep,
.model-card__ctx,
.model-card__vision {
  flex: none;
  white-space: nowrap;
}

.model-card__vendor-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  border-radius: 3px;
  object-fit: contain;
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

.model-card__ctx {
  font-variant-numeric: tabular-nums;
}

.model-card__ctx--default {
  color: var(--td-text-color-placeholder);
}

.model-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
}

.model-card__action-btn {
  flex-shrink: 0;
  padding: 2px;
  opacity: 0;
  transition: opacity var(--app-motion-fast) ease;
}

.model-card__more {
  color: var(--td-text-color-placeholder);

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

// Hover / 键盘焦点 时显示操作按钮，避免静态卡片上有"杂物"。
.model-card:hover .model-card__action-btn,
.model-card:focus-within .model-card__action-btn,
.model-card__actions:focus-within .model-card__action-btn {
  opacity: 1;
}

.empty-state {
  padding: 64px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: var(--app-text-base);
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.model-usage-dialog__description {
  margin: 0 0 16px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
}

.model-usage-dialog__content {
  max-height: min(58vh, 560px);
  overflow-y: auto;
  padding-right: 4px;
}

.model-usage-group {
  & + & {
    margin-top: 20px;
  }

  h3 {
    margin: 0 0 8px;
    font-size: var(--app-text-base);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-md);
    overflow: hidden;
  }

  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 12px;

    & + li {
      border-top: 1px solid var(--td-component-stroke);
    }
  }
}

.model-usage-truncated {
  margin: 8px 0 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.model-usage-resource {
  min-width: 0;

  strong {
    display: block;
    margin-bottom: 6px;
    overflow: hidden;
    color: var(--td-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.model-usage-bindings {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.model-usage-memory {
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
}

.model-usage-dialog__actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 20px;
}
</style>
