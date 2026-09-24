<template>
  <div class="model-catalog">
    <header class="section-header">
      <div class="section-header__top">
        <div class="section-header__main">
          <div class="section-header__titlewrap">
            <h2>{{ t('modelCatalog.title') }}</h2>
            <t-popup placement="bottom-start" trigger="hover" :overlay-inner-style="{ maxWidth: '420px' }">
              <button type="button" class="hint-trigger" :aria-label="t('modelCatalog.howItWorks')">
                <t-icon name="info-circle" size="16px" />
              </button>
              <template #content>
                <div class="hint-popover">
                  <p class="hint-popover__title">{{ t('modelCatalog.howItWorks') }}</p>
                  <ol class="hint-popover__list">
                    <li>{{ t('modelCatalog.layers.builtin') }}</li>
                    <li>{{ t('modelCatalog.layers.deployment') }}</li>
                    <li>{{ t('modelCatalog.layers.console') }}</li>
                    <li>{{ t('modelCatalog.layers.explicit') }}</li>
                  </ol>
                </div>
              </template>
            </t-popup>
          </div>
          <p class="section-description">{{ t('modelCatalog.description') }}</p>
        </div>
        <div class="section-header__actions">
          <t-button theme="primary" variant="text" class="header-action" :disabled="!state" @click="openAdd">
            <template #icon><t-icon name="add" /></template>
            {{ t('modelCatalog.add') }}
          </t-button>
          <t-dropdown :options="moreOptions" placement="bottom-right" attach="body" trigger="click" :disabled="!state"
            @click="onMore">
            <t-button variant="text" class="header-action header-action--muted" :disabled="!state">
              {{ t('modelCatalog.more') }}
              <template #suffix><t-icon name="chevron-down" /></template>
            </t-button>
          </t-dropdown>
          <input ref="fileInput" type="file" accept=".json,application/json" hidden @change="importOverlay" />
        </div>
      </div>
    </header>

    <t-alert v-if="state?.deployment_error" theme="warning" class="catalog-alert"
      :message="t('modelCatalog.deploymentError', { error: state.deployment_error })" />
    <t-alert v-if="state?.sync_error" theme="warning" class="catalog-alert"
      :message="t('modelCatalog.syncError', { error: state.sync_error })" />

    <div v-if="loadError && !state" class="catalog-state">
      <t-alert theme="error" :message="loadError">
        <template #operation>
          <t-button size="small" @click="load">{{ t('common.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <div v-else-if="!state" class="catalog-state catalog-state--loading">
      <t-loading size="small" />
    </div>

    <template v-else>
      <div class="catalog-toolbar">
        <t-input v-model="search" clearable class="catalog-toolbar__search" :placeholder="t('modelCatalog.search')">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select v-model="providerFilter" clearable filterable class="catalog-toolbar__select"
          :placeholder="t('modelCatalog.allProviders')" :options="providerOptions" />
        <t-select v-model="typeFilter" clearable class="catalog-toolbar__select catalog-toolbar__select--type"
          :placeholder="t('modelCatalog.allTypes')" :options="typeOptions" />
        <t-checkbox v-model="onlyModified" class="catalog-toolbar__check">
          {{ t('modelCatalog.onlyModified') }}<span v-if="modifiedCount" class="catalog-toolbar__count">{{ modifiedCount }}</span>
        </t-checkbox>
        <div class="catalog-toolbar__meta">
          <span>{{ t('modelCatalog.summary', { count: filteredRows.length, version: state.version }) }}</span>
          <button type="button" class="catalog-refresh" :disabled="loading" :title="t('common.refresh')"
            :aria-label="t('common.refresh')" @click="load">
            <t-icon :name="loading ? 'loading' : 'refresh'" :class="{ 'catalog-refresh--spin': loading }" />
          </button>
        </div>
      </div>

      <div class="data-table-shell catalog-table-shell"
        :class="{ 'catalog-table-shell--single-page': filteredRows.length <= PAGE_SIZE }">
        <t-table row-key="key" :data="pageRows" :columns="columns" :pagination="pagination" disable-data-page
          size="medium" hover
          :row-class-name="rowClassName" @row-click="({ row }: { row: CatalogRow }) => openEdit(row)"
          @page-change="(info: { current: number }) => { page = info.current }">
          <template #model="{ row }">
            <div class="catalog-model">
              <div class="catalog-model__id">
                <code>{{ row.model.id || row.model.match }}</code>
                <t-tooltip v-if="!row.model.id" :content="t('modelCatalog.ruleTip')">
                  <t-tag size="small" variant="light">{{ t('modelCatalog.rule') }}</t-tag>
                </t-tooltip>
                <t-tag v-if="row.model.deprecated" size="small" variant="light">{{ t('modelCatalog.hidden') }}</t-tag>
              </div>
              <span class="catalog-model__name">
                {{ providerLabel(row.provider) }}<template v-if="row.model.name && row.model.name !== row.model.id"> · {{ row.model.name }}</template>
              </span>
            </div>
          </template>
          <template #type="{ row }">
            <span class="catalog-cell-text">{{ typeLabel(row.model.type) }}</span>
          </template>
          <template #tokens="{ row }">
            <span class="catalog-num"
              :title="`${row.model.context_window?.toLocaleString() || '—'} / ${row.model.max_output_tokens?.toLocaleString() || '—'}`">
              {{ formatTokens(row.model.context_window) || '—' }}<span class="catalog-muted"> / </span>{{ formatTokens(row.model.max_output_tokens) || '—' }}
            </span>
          </template>
          <template #capabilities="{ row }">
            <div class="catalog-caps">
              <span v-for="cap in capabilities(row.model)" :key="cap" class="catalog-cap">
                {{ t(`modelCatalog.capability.${cap}`) }}
              </span>
              <span v-if="capabilities(row.model).length === 0" class="catalog-muted">—</span>
            </div>
          </template>
          <template #source="{ row }">
            <t-tag v-if="sourceOf(row) !== 'builtin'" size="small" variant="light"
              :theme="sourceOf(row) === 'console' ? 'primary' : 'warning'">
              {{ t(`modelCatalog.source.${sourceOf(row)}`) }}
            </t-tag>
            <span v-else class="catalog-muted">{{ t('modelCatalog.source.builtin') }}</span>
          </template>
          <template #actions="{ row }">
            <t-button variant="text" theme="primary" size="small" @click.stop="openEdit(row)">
              {{ row.model.id ? t('modelCatalog.edit') : t('modelCatalog.view') }}
            </t-button>
          </template>
          <template #empty>
            <t-empty :description="t('modelCatalog.empty')" />
          </template>
        </t-table>
      </div>
    </template>

    <!-- 编辑单个模型：保存即发布 -->
    <SettingDrawer :visible="editVisible" :title="selected ? (selected.model.id || selected.model.match || '') : ''"
      :description="selected ? t('modelCatalog.editDescription', { provider: providerLabel(selected.provider), type: typeLabel(selected.model.type) }) : ''"
      icon="layers" width="560px" storage-key="setting-drawer:width:model-catalog-edit"
      :hide-footer="!selected?.model.id" :confirm-text="t('modelCatalog.save')" :confirm-loading="saving"
      :confirm-disabled="!editDirty" @update:visible="editVisible = $event" @confirm="saveEdit">
      <template v-if="selected">
        <section v-if="!selected.model.id" class="setting-drawer__section">
          <t-alert theme="info" :message="t('modelCatalog.ruleNotice')" />
        </section>

        <section v-else class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('modelCatalog.fieldsSection') }}</h4>
          <p class="drawer-hint">
            {{ t(selectedInherited ? 'modelCatalog.fieldsHint' : 'modelCatalog.customHint') }}
            <a v-if="selected.model.source" :href="String(selected.model.source)" target="_blank" rel="noopener noreferrer"
              class="drawer-link">{{ t('modelCatalog.sourceLink') }}<t-icon name="jump" /></a>
          </p>
          <div v-for="field in editFields" :key="field.key" class="drawer-row"
            :class="{ 'drawer-row--stacked': field.kind === 'levels' }">
            <div class="drawer-row__info">
              <div class="drawer-row__label">
                {{ field.label }}
                <t-tag v-if="selectedInherited && isOverridden(field.key)" size="small" theme="primary" variant="light">
                  {{ t('modelCatalog.modified') }}
                </t-tag>
              </div>
              <p class="drawer-row__desc">{{ field.desc }}</p>
              <p v-if="selectedInherited && field.kind !== 'levels'" class="drawer-row__inherited">
                {{ t('modelCatalog.inherited', { value: displayField(field.kind, fieldValue('deployment', selected, field.key)) }) }}
              </p>
            </div>
            <div class="drawer-row__control">
              <template v-if="field.kind === 'levels'">
                <p v-if="!form.reasoning" class="drawer-row__inherited">{{ t('modelCatalog.levelsNeedReasoning') }}</p>
                <p v-else-if="!levelsAvailable" class="drawer-row__inherited">{{ t('modelCatalog.levelsUnsupported') }}</p>
                <template v-else>
                  <t-checkbox-group v-model="form.thinking_levels" :options="levelOptions" class="drawer-levels" />
                  <p v-if="selectedInherited" class="drawer-row__inherited">
                    {{ t('modelCatalog.inherited', { value: displayField('levels', fieldValue('deployment', selected, 'thinking_levels')) }) }}
                  </p>
                </template>
              </template>
              <FieldControl v-else :kind="field.kind" :model-value="form[field.key]"
                :placeholder="t('modelCatalog.notSet')" @update:model-value="form[field.key] = $event" />
            </div>
          </div>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('modelCatalog.layersSection') }}</h4>
          <table class="layer-table">
            <thead>
              <tr>
                <th>{{ t('modelCatalog.layerField') }}</th>
                <th>{{ t('modelCatalog.layerBuiltin') }}</th>
                <th>{{ t('modelCatalog.layerDeployment') }}</th>
                <th>{{ t('modelCatalog.layerEffective') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="field in editFields" :key="field.key">
                <td>{{ field.label }}</td>
                <td v-for="layer in (['builtin', 'deployment', 'effective'] as const)" :key="layer"
                  :class="{ 'layer-table__changed': layer !== 'builtin' && layerValue(layer, field) !== layerValue(layer === 'effective' ? 'deployment' : 'builtin', field) }">
                  {{ layerValue(layer, field) }}
                </td>
              </tr>
            </tbody>
          </table>
        </section>
      </template>
      <template #footer-left>
        <t-popconfirm v-if="selected && hasModelOverride(state?.overlay, selected.provider, selected.model)"
          :content="t(selectedInherited ? 'modelCatalog.restoreConfirm' : 'modelCatalog.removeConfirm')"
          :confirm-btn="{ content: t(selectedInherited ? 'modelCatalog.restore' : 'common.delete'), theme: 'danger' }"
          :cancel-btn="{ content: t('common.cancel') }" placement="top-left" @confirm="restoreSelected">
          <t-button variant="text" theme="danger" :disabled="saving">
            {{ t(selectedInherited ? 'modelCatalog.restore' : 'modelCatalog.remove') }}
          </t-button>
        </t-popconfirm>
      </template>
    </SettingDrawer>

    <!-- 添加目录模型 -->
    <SettingDrawer :visible="addVisible" :title="t('modelCatalog.add')" :description="t('modelCatalog.addDescription')"
      icon="add" width="520px" storage-key="setting-drawer:width:model-catalog-add" :close-on-overlay-click="false"
      :confirm-text="t('modelCatalog.save')" :confirm-loading="saving" @update:visible="addVisible = $event"
      @confirm="saveAdd">
      <section class="setting-drawer__section">
        <div class="drawer-field">
          <label>{{ t('modelCatalog.columns.provider') }}</label>
          <t-select v-model="addForm.provider" filterable :options="providerOptions" />
        </div>
        <div class="drawer-field">
          <label>{{ t('modelCatalog.columns.type') }}</label>
          <t-select v-model="addForm.type" :options="addTypeOptions" />
        </div>
        <div class="drawer-field">
          <label>{{ t('modelCatalog.modelId') }}</label>
          <t-input v-model="addForm.id" :placeholder="t('modelCatalog.modelIdPlaceholder')" />
        </div>
      </section>
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('modelCatalog.fieldsSection') }}</h4>
        <div v-for="field in addFields" :key="field.key" class="drawer-row">
          <div class="drawer-row__info">
            <div class="drawer-row__label">{{ field.label }}</div>
            <p class="drawer-row__desc">{{ field.desc }}</p>
          </div>
          <div class="drawer-row__control">
            <FieldControl :kind="field.kind" :model-value="addForm[field.key]" :placeholder="t('modelCatalog.optional')"
              @update:model-value="addForm[field.key] = $event" />
          </div>
        </div>
      </section>
    </SettingDrawer>

    <!-- 高级：直接编辑管理员修改（models.json 格式） -->
    <SettingDrawer :visible="jsonVisible" :title="t('modelCatalog.jsonEditor')"
      :description="t('modelCatalog.jsonDescription')" icon="code" width="720px" :max-width="1200" maximizable
      storage-key="setting-drawer:width:model-catalog-json" :close-on-overlay-click="false"
      :confirm-text="jsonPreview ? t('modelCatalog.jsonPublish', { count: jsonChanges.length }) : t('modelCatalog.jsonCheck')"
      :confirm-loading="saving" :confirm-disabled="!!jsonPreview && jsonChanges.length === 0"
      @update:visible="jsonVisible = $event" @confirm="confirmJson">
      <section class="setting-drawer__section">
        <p class="drawer-hint">{{ t('modelCatalog.jsonHint') }}</p>
        <t-textarea v-model="draft" class="json-editor" :autosize="{ minRows: 16, maxRows: 40 }" spellcheck="false"
          :status="jsonError ? 'error' : 'default'" :tips="jsonError" />
      </section>
      <section v-if="jsonPreview" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">
          {{ t('modelCatalog.changesTitle') }}
          <span class="catalog-muted">{{ jsonChanges.length }}</span>
        </h4>
        <p v-if="jsonChanges.length === 0" class="drawer-hint">{{ t('modelCatalog.jsonUnchanged') }}</p>
        <template v-else>
          <p class="drawer-hint">{{ t('modelCatalog.publishHint') }}</p>
          <ul class="change-list">
            <li v-for="change in jsonChanges" :key="change.key" class="change-list__item">
              <t-tag size="small" variant="light" :theme="changeTheme(change.kind)">
                {{ t(`modelCatalog.change.${change.kind}`) }}
              </t-tag>
              <span class="change-list__target">
                <span class="catalog-muted">{{ providerLabel(change.provider) }}</span>
                <code v-if="change.kind !== 'provider'">{{ change.target }}</code>
              </span>
              <span v-if="change.fields.length" class="change-list__fields">
                {{ change.fields.map(fieldLabel).join('、') }}
              </span>
            </li>
          </ul>
        </template>
      </section>
      <template #footer-left>
        <t-button variant="text" :disabled="saving" @click="fileInput?.click()">{{ t('modelCatalog.import') }}</t-button>
        <t-button variant="text" :disabled="saving" @click="draft = emptyOverlay">{{ t('modelCatalog.jsonClear') }}</t-button>
      </template>
    </SettingDrawer>

    <!-- 版本历史：恢复即发布 -->
    <SettingDrawer :visible="historyVisible" :title="t('modelCatalog.history')"
      :description="t('modelCatalog.historyDescription')" icon="history" width="560px"
      storage-key="setting-drawer:width:model-catalog-history" hide-footer @update:visible="historyVisible = $event">
      <section v-if="state" class="setting-drawer__section">
        <div class="history-row">
          <div class="history-row__main">
            <span class="history-row__version">{{ t('modelCatalog.historyVersion', { version: state.version }) }}</span>
            <t-tag size="small" theme="success" variant="light">{{ t('modelCatalog.historyCurrent') }}</t-tag>
            <span class="history-row__meta">{{ overrideSummary(state.overlay) }}</span>
          </div>
        </div>
        <div v-for="revision in state.history" :key="revision.version" class="history-row">
          <div class="history-row__main">
            <span class="history-row__version">{{ t('modelCatalog.historyVersion', { version: revision.version }) }}</span>
            <span class="history-row__meta">{{ overrideSummary(revision.overlay) }}</span>
          </div>
          <div class="history-row__side">
            <span class="history-row__meta">{{ revision.updated_by || '—' }} · {{ formatDate(revision.updated_at) }}</span>
            <t-popconfirm :content="t('modelCatalog.historyRestoreConfirm', { version: revision.version })"
              :confirm-btn="{ content: t('modelCatalog.historyRestore'), theme: 'primary' }"
              :cancel-btn="{ content: t('common.cancel') }" placement="bottom-right"
              @confirm="restoreRevision(revision.overlay)">
              <t-button variant="text" theme="primary" size="small" :disabled="saving">
                {{ t('modelCatalog.historyRestore') }}
              </t-button>
            </t-popconfirm>
          </div>
        </div>
        <p v-if="state.history.length === 0" class="drawer-hint">{{ t('modelCatalog.historyEmpty') }}</p>
      </section>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, type TableProps } from 'tdesign-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { levelLabelKey } from '@/utils/reasoningEffort'
import FieldControl from './ModelCatalogFieldControl.vue'
import { getModelCatalog, previewModelCatalog, publishModelCatalog, type CatalogModel, type CatalogOverlay, type CatalogPreview, type CatalogState } from '@/api/system/modelCatalog'
import { useModelProvidersStore } from '@/stores/modelProviders'
import { useChatResourcesStore } from '@/stores/chatResources'
import {
  EDITABLE_INPUTS, THINKING_LEVELS, catalogChanges, catalogRows, catalogSource, composeInput, formatTokens,
  hasModelOverride, patchCatalogModel, removeCatalogModelOverride, sameMembers, stableJSON, summarizeChanges,
  thinkingLevelsPatch, type CatalogChangeSummary, type CatalogRow, type FieldKind,
} from './modelCatalogState'

type Layer = 'builtin' | 'deployment' | 'effective'
type FieldKey = 'name' | 'context_window' | 'max_output_tokens' | 'dimension' | 'input' | 'reasoning' | 'thinking_levels' | 'deprecated'
// `types` limits a field to the model types that use it; omitted means all.
interface FieldDef { key: FieldKey; kind: FieldKind; label: string; desc: string; types?: string[] }

const PAGE_SIZE = 20
const TYPE_LABEL_KEYS: Record<string, string> = {
  KnowledgeQA: 'chat', Embedding: 'embedding', Rerank: 'rerank', VLLM: 'vllm', ASR: 'asr',
}

const { t, locale } = useI18n()
const providersStore = useModelProvidersStore()
const chatResources = useChatResourcesStore()
const emptyOverlay = '{\n  "providers": {}\n}'

const state = ref<CatalogState>()
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const fileInput = ref<HTMLInputElement>()

const search = ref('')
const providerFilter = ref<string>()
const typeFilter = ref<string>()
const onlyModified = ref(false)
const page = ref(1)

// ---------- 目录列表 ----------

const layerIndexes = computed(() => Object.fromEntries((['builtin', 'deployment', 'effective'] as const).map(layer => [
  layer, new Map(catalogRows(state.value?.[layer] || []).map(row => [row.key, row.model])),
])) as Record<Layer, Map<string, CatalogModel>>)
// Concrete models first within each provider; patterns only supply defaults.
const allRows = computed(() => catalogRows(state.value?.effective || [])
  .map((row, index) => ({ row, index }))
  .sort((a, b) => a.row.provider.localeCompare(b.row.provider) || Number(!a.row.model.id) - Number(!b.row.model.id) || a.index - b.index)
  .map(({ row }) => row))
const sources = computed(() => new Map(allRows.value.map(row => [row.key, catalogSource(
  state.value?.overlay, row, layerIndexes.value.deployment.get(row.key), layerIndexes.value.builtin.get(row.key),
)])))
const sourceOf = (row: CatalogRow) => sources.value.get(row.key) || 'builtin'
const modifiedCount = computed(() => allRows.value.filter(row => sourceOf(row) === 'console').length)

const filteredRows = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  return allRows.value.filter(row => (!providerFilter.value || row.provider === providerFilter.value)
    && (!typeFilter.value || (row.model.type || 'KnowledgeQA') === typeFilter.value)
    && (!onlyModified.value || sourceOf(row) === 'console')
    && (!keyword || `${row.model.id} ${row.model.match || ''} ${row.model.name || ''}`.toLowerCase().includes(keyword)))
})
watch([search, providerFilter, typeFilter, onlyModified], () => { page.value = 1 })
// Slice pages here instead of relying on t-table's local paging, which keeps
// showing stale rows when the pagination prop toggles off after filtering.
const pageRows = computed(() => filteredRows.value.slice((page.value - 1) * PAGE_SIZE, page.value * PAGE_SIZE))
const pagination = computed(() => ({ current: page.value, pageSize: PAGE_SIZE, total: filteredRows.value.length, showPageSize: false }))

const providerLabel = (id: string) => {
  const provider = state.value?.effective.find(p => p.id === id)
  const names = provider?.settings?.names as Record<string, string> | undefined
  return names?.[String(locale.value)] || provider?.name || id
}
const typeLabel = (type?: string) => {
  const key = TYPE_LABEL_KEYS[type || 'KnowledgeQA']
  return key ? t(`modelSettings.typeShort.${key}`) : type || ''
}
const providerOptions = computed(() => (state.value?.effective || []).map(p => ({ label: providerLabel(p.id), value: p.id })))
const typeOptions = computed(() => [...new Set(allRows.value.map(row => row.model.type || 'KnowledgeQA'))]
  .map(type => ({ label: typeLabel(type), value: type })))

const capabilities = (model: CatalogModel) => [
  ...(model.reasoning ? ['reasoning'] : []),
  ...((model.input as string[] | undefined) || []).filter(kind => ['image', 'audio', 'video'].includes(kind)),
]
const rowClassName = ({ row }: { row: CatalogRow }) => (row.model.deprecated ? 'catalog-row--hidden' : '')

const columns = computed<TableProps['columns']>(() => [
  { colKey: 'model', title: t('modelCatalog.columns.model') },
  { colKey: 'type', title: t('modelCatalog.columns.type'), width: 100 },
  { colKey: 'tokens', title: t('modelCatalog.columns.tokens'), width: 124, align: 'right' },
  { colKey: 'capabilities', title: t('modelCatalog.columns.capabilities'), width: 148 },
  { colKey: 'source', title: t('modelCatalog.columns.source'), width: 108 },
  { colKey: 'actions', title: '', width: 64, align: 'right' },
])

// ---------- 加载与发布 ----------

async function load() {
  loading.value = true
  try {
    state.value = await getModelCatalog()
    loadError.value = ''
  } catch (e: any) {
    loadError.value = e?.message || t('modelCatalog.loadFailed')
    if (state.value) MessagePlugin.error(loadError.value)
  } finally {
    loading.value = false
  }
}

async function handleError(e: any) {
  if (e?.status === 409) {
    MessagePlugin.warning(t('modelCatalog.conflict'))
    await load()
    return
  }
  MessagePlugin.error(e?.message || String(e))
}

// Every edit publishes a whole overlay against the version it was based on;
// a concurrent publish returns 409 and the page reloads instead of merging.
async function commit(overlay: CatalogOverlay, message = t('modelCatalog.published')) {
  if (!state.value) return false
  saving.value = true
  try {
    state.value = await publishModelCatalog({ version: state.value.version, baseline: state.value.baseline, overlay })
    providersStore.reset()
    // Model responses contain resolved catalog capabilities as well.
    chatResources.invalidate('models')
    MessagePlugin.success(message)
    return true
  } catch (e: any) {
    await handleError(e)
    return false
  } finally {
    saving.value = false
  }
}

// ---------- 编辑单个模型 ----------

const CHAT = ['KnowledgeQA']
const fieldDefs = computed<FieldDef[]>(() => [
  { key: 'name', kind: 'text', label: t('modelCatalog.displayName'), desc: t('modelCatalog.nameDesc') },
  { key: 'context_window', kind: 'tokens', label: t('modelCatalog.context'), desc: t('modelCatalog.contextDesc') },
  { key: 'max_output_tokens', kind: 'tokens', label: t('modelCatalog.output'), desc: t('modelCatalog.outputDesc'), types: CHAT },
  { key: 'dimension', kind: 'number', label: t('modelCatalog.dimension'), desc: t('modelCatalog.dimensionDesc'), types: ['Embedding'] },
  { key: 'input', kind: 'modalities', label: t('modelCatalog.inputModes'), desc: t('modelCatalog.inputDesc'), types: CHAT },
  { key: 'reasoning', kind: 'bool', label: t('modelCatalog.reasoning'), desc: t('modelCatalog.reasoningDesc'), types: CHAT },
  { key: 'thinking_levels', kind: 'levels', label: t('modelCatalog.thinkingLevels'), desc: t('modelCatalog.thinkingLevelsDesc'), types: CHAT },
  { key: 'deprecated', kind: 'bool', label: t('modelCatalog.hide'), desc: t('modelCatalog.hideDesc') },
])
const fieldsFor = (type?: string) => fieldDefs.value.filter(field => !field.types || field.types.includes(type || 'KnowledgeQA'))
const editFields = computed(() => fieldsFor(selected.value?.model.type))
const levelOptions = computed(() => THINKING_LEVELS.map(level => ({ label: t(levelLabelKey(level)), value: level })))

const editVisible = ref(false)
const selected = ref<CatalogRow>()
const form = reactive<Record<FieldKey, any>>({
  name: '', context_window: undefined, max_output_tokens: undefined, dimension: undefined,
  input: [], reasoning: false, thinking_levels: [], deprecated: false,
})

// Values the model falls back to without an administrator override. Models
// added from this page have no lower layer, so there is nothing to restore.
const selectedInherited = computed(() => selected.value ? layerIndexes.value.deployment.get(selected.value.key) : undefined)
const selectedOverride = computed(() => {
  const row = selected.value, p = row && state.value?.overlay.providers[row.provider]
  if (!row || !p) return {}
  const id = row.model.id.toLowerCase(), type = row.model.type || 'KnowledgeQA'
  const entry = (p.models || []).find((m: CatalogModel) => m.id?.toLowerCase() === id && (!m.type || m.type === type))
  const extra = Object.entries(p.model_overrides || {}).find(([key]) => key.toLowerCase() === id)?.[1]
  return { ...(extra as object || {}), ...(entry || {}) } as Record<string, unknown>
})
const isOverridden = (key: FieldKey) => selectedOverride.value[key] !== undefined

const providerIn = (layer: Layer, id: string) => state.value?.[layer].find(p => p.id === id)
const kindOf = (key: FieldKey) => fieldDefs.value.find(f => f.key === key)!.kind
function normalize(kind: FieldKind, value: unknown) {
  if (kind === 'bool') return !!value
  if (kind === 'text') return String(value ?? '').trim() || undefined
  if (kind === 'modalities' || kind === 'levels') return [...(value as string[] | undefined || [])]
  return value || undefined
}
const sameValue = (kind: FieldKind, a: unknown, b: unknown) => (kind === 'modalities' || kind === 'levels')
  ? sameMembers(a as string[], b as string[]) : a === b

// Reads a field from one catalog layer in the shape the form edits. Thinking
// levels come from the server's resolution, since they merge the vendor map.
function fieldValue(layer: Layer, row: CatalogRow, key: FieldKey) {
  const model = layerIndexes.value[layer].get(row.key)
  if (!model) return undefined
  if (key === 'thinking_levels') return [...(providerIn(layer, row.provider)?.model_thinking_levels?.[row.model.id] || [])]
  if (key === 'input') return ((model.input as string[] | undefined) || []).filter(m => (EDITABLE_INPUTS as readonly string[]).includes(m))
  return normalize(kindOf(key), model[key])
}

const levelsAvailable = computed(() => {
  const row = selected.value
  if (!row) return false
  return ((fieldValue('effective', row, 'thinking_levels') as string[] | undefined)?.length || 0) > 0
    || (providerIn('effective', row.provider)?.vendor_thinking_levels?.length || 0) > 0
})

const editDirty = computed(() => {
  const row = selected.value
  return !!row && editFields.value.some(({ key, kind }) =>
    !sameValue(kind, normalize(kind, form[key]), fieldValue('effective', row, key)))
})

function openEdit(row: CatalogRow) {
  selected.value = row
  for (const { key, kind } of fieldDefs.value) form[key] = fieldValue('effective', row, key) ?? normalize(kind, undefined)
  form.name = form.name || ''
  editVisible.value = true
}

async function saveEdit() {
  const row = selected.value
  if (!row || !state.value || !editDirty.value) return
  const inherited = !!selectedInherited.value
  const vendor = providerIn('effective', row.provider)
  const patch: Record<string, unknown> = {}
  for (const { key, kind } of editFields.value) {
    const value = normalize(kind, form[key])
    if (sameValue(kind, value, fieldValue('effective', row, key))) continue
    if (key === 'thinking_levels') {
      // Levels only matter while reasoning is on; leave them alone otherwise.
      if (!form.reasoning) continue
      const base = inherited ? fieldValue('deployment', row, key) as string[] : vendor?.vendor_thinking_levels || []
      patch[key] = thinkingLevelsPatch(value as string[], base,
        selectedOverride.value.thinking_levels as Record<string, string | null> | undefined,
        vendor?.settings?.thinking_levels as Record<string, string | null> | undefined)
      continue
    }
    // Choosing the inherited value again drops the override instead of pinning it.
    if (inherited && sameValue(kind, value, fieldValue('deployment', row, key))) {
      patch[key] = undefined
      continue
    }
    patch[key] = key === 'input' ? composeInput(row.model.input as string[] | undefined, value as string[]) : value
  }
  if (await commit(patchCatalogModel(state.value.overlay, row.provider, row.model, patch))) editVisible.value = false
}

async function restoreSelected() {
  const row = selected.value
  if (!row || !state.value) return
  const message = t(selectedInherited.value ? 'modelCatalog.restored' : 'modelCatalog.removed')
  if (await commit(removeCatalogModelOverride(state.value.overlay, row.provider, row.model), message)) editVisible.value = false
}

function displayField(kind: FieldKind, value: unknown) {
  if (kind === 'modalities') {
    const extras = value as string[] | undefined
    return extras?.length ? extras.map(m => t(`modelCatalog.capability.${m}`)).join('、') : t('modelCatalog.textOnly')
  }
  if (kind === 'levels') {
    const levels = value as string[] | undefined
    return levels?.length ? levels.map(level => t(levelLabelKey(level))).join('、') : t('modelCatalog.noLevels')
  }
  if (value === undefined || value === null || value === '') return t('modelCatalog.notSet')
  if (kind === 'bool') return t(value ? 'modelCatalog.yes' : 'modelCatalog.no')
  if (kind === 'tokens') return formatTokens(value as number)
  return String(value)
}
function layerValue(layer: Layer, field: FieldDef) {
  if (!selected.value || !layerIndexes.value[layer].get(selected.value.key)) return '—'
  return displayField(field.kind, fieldValue(layer, selected.value, field.key))
}

// ---------- 添加模型 ----------

const addVisible = ref(false)
const emptyAddForm = () => ({
  provider: providerFilter.value || '', type: typeFilter.value || 'KnowledgeQA', id: '', name: '',
  context_window: undefined, max_output_tokens: undefined, dimension: undefined, input: [] as string[], reasoning: false,
})
const addForm = reactive<Record<string, any>>(emptyAddForm())
// Levels start from the vendor defaults and are tuned after the model exists.
const addFields = computed(() => fieldsFor(addForm.type).filter(field => field.key !== 'deprecated' && field.key !== 'thinking_levels'))
// VLM eligibility is derived from a chat model's input modalities, so the
// catalog has no separate VLLM entries to add.
const addTypeOptions = computed(() => {
  const provider = state.value?.effective.find(p => p.id === addForm.provider)
  if (!provider) return typeOptions.value
  return provider.model_types.filter(kind => kind !== 'VLLM').map(kind => ({ label: typeLabel(kind), value: kind }))
})
watch(() => addForm.provider, () => {
  if (!addTypeOptions.value.some(option => option.value === addForm.type)) addForm.type = addTypeOptions.value[0]?.value || 'KnowledgeQA'
})

function openAdd() {
  Object.assign(addForm, emptyAddForm())
  addVisible.value = true
}

async function saveAdd() {
  const id = addForm.id.trim()
  if (!state.value || !addForm.provider || !id) {
    MessagePlugin.warning(t('modelCatalog.required'))
    return
  }
  const key = `${addForm.provider}:${addForm.type}:`
  if (allRows.value.some(row => row.key.toLowerCase() === (key + id).toLowerCase())) {
    MessagePlugin.warning(t('modelCatalog.exists'))
    return
  }
  const fields: Record<string, unknown> = { deprecated: false }
  for (const { key, kind } of addFields.value) {
    const value = normalize(kind, addForm[key])
    fields[key] = key === 'input' ? ((value as string[]).length ? composeInput(undefined, value as string[]) : undefined) : value
  }
  const overlay = patchCatalogModel(state.value.overlay, addForm.provider, { id, type: addForm.type }, fields)
  if (await commit(overlay)) {
    addVisible.value = false
    providerFilter.value = addForm.provider
    search.value = id
  }
}

// ---------- JSON 编辑 ----------

const jsonVisible = ref(false)
const draft = ref(emptyOverlay)
const jsonError = ref('')
const jsonPreview = ref<CatalogPreview>()
const jsonChanges = computed<CatalogChangeSummary[]>(() => jsonPreview.value
  ? summarizeChanges(catalogChanges(state.value?.effective || [], jsonPreview.value.effective)) : [])
watch(draft, () => { jsonPreview.value = undefined; jsonError.value = '' })

function openJson(text?: string) {
  draft.value = text ?? stableJSON(state.value?.overlay || { providers: {} })
  jsonVisible.value = true
}

function parseDraft(): CatalogOverlay {
  const value = JSON.parse(draft.value)
  if (!value || typeof value !== 'object' || !value.providers || typeof value.providers !== 'object' || Array.isArray(value.providers)) {
    throw new Error(t('modelCatalog.invalid'))
  }
  return value
}

// First click validates on the server and lists the resulting changes; the
// second click publishes exactly the overlay that was checked.
async function confirmJson() {
  if (!state.value) return
  if (jsonPreview.value) {
    if (await commit(jsonPreview.value.overlay)) jsonVisible.value = false
    return
  }
  let overlay: CatalogOverlay
  try {
    overlay = parseDraft()
  } catch (e: any) {
    jsonError.value = e?.message || String(e)
    return
  }
  const text = draft.value
  saving.value = true
  try {
    const candidate = await previewModelCatalog({ version: state.value.version, baseline: state.value.baseline, overlay })
    if (draft.value === text) jsonPreview.value = candidate
  } catch (e: any) {
    if (e?.status === 409) await handleError(e)
    else jsonError.value = e?.message || String(e)
  } finally {
    saving.value = false
  }
}

const changeTheme = (kind: CatalogChangeSummary['kind']) => (
  { added: 'success', removed: 'danger', updated: 'primary', provider: 'warning' } as const)[kind]
const fieldLabel = (field: string) => {
  const def = fieldDefs.value.find(f => f.key === field)
  return def ? def.label : field
}

async function importOverlay(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > 1024 * 1024) {
    MessagePlugin.warning(t('modelCatalog.tooLarge'))
    return
  }
  try {
    openJson(stableJSON(JSON.parse(await file.text())))
  } catch (e: any) {
    MessagePlugin.error(e?.message || String(e))
  }
}

function exportOverlay() {
  const url = URL.createObjectURL(new Blob([stableJSON(state.value?.overlay || { providers: {} }) + '\n'], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'models.json'
  a.click()
  URL.revokeObjectURL(url)
}

// ---------- 版本历史 ----------

const historyVisible = ref(false)

const overrideSummary = (overlay: CatalogOverlay) => {
  const count = Object.values(overlay?.providers || {}).reduce((sum, p: any) =>
    sum + (p.models?.length || 0) + Object.keys(p.model_overrides || {}).length, 0)
  return count ? t('modelCatalog.historyModels', { count }) : t('modelCatalog.historyNoOverrides')
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

async function restoreRevision(overlay: CatalogOverlay) {
  if (await commit(overlay, t('modelCatalog.restored'))) historyVisible.value = false
}

// ---------- 更多菜单 ----------

const moreOptions = computed(() => [
  { content: t('modelCatalog.jsonEditor'), value: 'json' },
  { content: t('modelCatalog.history'), value: 'history', divider: true },
  { content: t('modelCatalog.import'), value: 'import' },
  { content: t('modelCatalog.export'), value: 'export' },
])

function onMore(data: { value?: string | number }) {
  if (data.value === 'json') openJson()
  else if (data.value === 'history') historyVisible.value = true
  else if (data.value === 'import') fileInput.value?.click()
  else if (data.value === 'export') exportOverlay()
}

onMounted(load)
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.model-catalog {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.section-header__main {
  min-width: 0;
}

.section-header__titlewrap {
  display: flex;
  align-items: center;
  gap: 6px;

  h2 {
    margin-bottom: 0;
  }
}

.section-header__titlewrap + .section-description {
  margin-top: 8px;
}

.section-header__actions {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-shrink: 0;
}

.header-action {
  --td-bg-color-container-hover: transparent;
  padding-left: 0;
  padding-right: 0;
  font-weight: 600;
}

.header-action--muted {
  color: var(--td-text-color-secondary);

  &:hover:not(:disabled) {
    color: var(--td-brand-color);
  }
}

.hint-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border: none;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: help;
  line-height: 1;

  &:hover,
  &:focus-visible {
    color: var(--td-brand-color);
  }
}

.hint-popover {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.hint-popover__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 600;
}

.hint-popover__list {
  margin: 0;
  padding: 0 0 0 18px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.6;

  li + li {
    margin-top: 4px;
  }
}

.catalog-alert {
  margin-bottom: 16px;
}

.catalog-state {
  min-height: 160px;
}

.catalog-state--loading {
  display: flex;
  align-items: center;
  justify-content: center;
}

.catalog-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.catalog-toolbar__search {
  width: 200px;
}

.catalog-toolbar__select {
  width: 160px;
}

.catalog-toolbar__select--type {
  width: 116px;
}

.catalog-toolbar__count {
  margin-left: 4px;
  color: var(--td-brand-color);
  font-weight: 600;
}

.catalog-toolbar__meta {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  white-space: nowrap;
}

.catalog-refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color var(--app-motion-base) ease, background var(--app-motion-base) ease;

  &:hover:not(:disabled) {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
  }

  &:disabled {
    cursor: default;
    opacity: 0.7;
  }
}

.catalog-refresh--spin {
  animation: wk-spin 0.8s linear infinite;
}

.data-table-shell {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    background-color: var(--td-bg-color-secondarycontainer);
    font-size: var(--app-text-md);
    font-weight: 600;
  }

  &:deep(.t-table td),
  &:deep(.t-table th) {
    padding-top: 12px;
    padding-bottom: 12px;
    vertical-align: middle;
  }

  &:deep(.t-table__pagination) {
    padding: 12px 16px;
  }
}

.catalog-table-shell {
  &:deep(.t-table tbody tr) {
    cursor: pointer;
  }

  &--single-page :deep(.t-table__pagination) {
    display: none;
  }

  &:deep(.catalog-row--hidden td) {
    color: var(--td-text-color-placeholder);
  }
}

.catalog-model {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.catalog-model__id {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-width: 0;

  code {
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family-mono);
    font-size: var(--app-text-md);
    font-weight: 500;
    overflow-wrap: anywhere;
  }
}

.catalog-row--hidden .catalog-model__id code {
  color: var(--td-text-color-placeholder);
}

.catalog-model__name {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.4;
}

.catalog-cell-text {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
}

.catalog-num {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  font-variant-numeric: tabular-nums;
}

.catalog-caps {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.catalog-cap {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 6px;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 20px;
  white-space: nowrap;
}

.catalog-muted {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-weight: 400;
}

// ---------- 抽屉内容 ----------

.drawer-hint {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

.drawer-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--td-component-stroke);
}

.drawer-row__info {
  flex: 1;
  min-width: 0;
}

.drawer-row__label {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 1.4;
}

.drawer-row__desc,
.drawer-row__inherited {
  margin: 2px 0 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

.drawer-row__inherited {
  color: var(--td-text-color-placeholder);
}

.drawer-row__control {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-shrink: 0;
  min-height: 32px;
}

.drawer-row--stacked {
  flex-direction: column;
  gap: 10px;

  .drawer-row__control {
    justify-content: flex-start;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
  }
}

.drawer-levels {
  flex-wrap: wrap;
  row-gap: 8px;
}

.drawer-link {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: 4px;
  color: var(--td-brand-color);
  text-decoration: none;

  &:hover {
    color: var(--td-brand-color-hover);
  }
}

.drawer-field {
  display: flex;
  flex-direction: column;
  gap: 8px;

  label {
    color: var(--td-text-color-primary);
    font-size: var(--app-text-base);
    font-weight: 500;
  }
}

.layer-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--app-text-sm);

  th,
  td {
    padding: 8px 10px;
    border-bottom: 1px solid var(--td-component-stroke);
    text-align: left;
    overflow-wrap: anywhere;
  }

  th {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-weight: 500;
  }

  td {
    color: var(--td-text-color-secondary);
  }

  td:first-child {
    color: var(--td-text-color-primary);
  }

  td.layer-table__changed {
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.json-editor :deep(textarea) {
  font-family: var(--app-font-family-mono);
  font-size: var(--app-text-sm);
  line-height: 1.6;
}

.change-list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
}

.change-list__item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  font-size: var(--app-text-sm);

  & + & {
    border-top: 1px solid var(--td-component-stroke);
  }
}

.change-list__target {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;

  code {
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family-mono);
    overflow-wrap: anywhere;
  }
}

.change-list__fields {
  color: var(--td-text-color-secondary);
}

.history-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 0;
  border-top: 1px solid var(--td-component-stroke);

  &:first-child {
    border-top: none;
    padding-top: 0;
  }
}

.history-row__main,
.history-row__side {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.history-row__main {
  flex-wrap: wrap;
}

.history-row__version {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 500;
}

.history-row__meta {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}
</style>
