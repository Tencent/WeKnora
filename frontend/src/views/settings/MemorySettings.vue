<template>
  <div class="memory-settings">
    <div class="section-header">
      <div class="section-header-titlewrap">
        <h2>{{ t('memorySettings.title') }}</h2>
        <t-popup
          placement="bottom-start"
          trigger="hover"
          overlay-class-name="memory-usage-popup-overlay"
        >
          <button
            type="button"
            class="usage-trigger-btn"
            :aria-label="t('memorySettings.usage.iconHint')"
            :title="t('memorySettings.usage.iconHint')"
          >
            <t-icon name="info-circle" size="16px" />
          </button>
          <template #content>
            <div class="usage-popup">
              <div class="usage-popup-title">{{ t('memorySettings.usage.title') }}</div>
              <p class="usage-popup-intro">{{ t('memorySettings.usage.intro') }}</p>
              <div class="usage-popup-rows">
                <div v-for="key in usageRowKeys" :key="key" class="usage-popup-row">
                  <span class="usage-popup-label">{{ t(`memorySettings.usage.rows.${key}.label`) }}</span>
                  <span class="usage-popup-text">{{ t(`memorySettings.usage.rows.${key}.text`) }}</span>
                </div>
              </div>
            </div>
          </template>
        </t-popup>
      </div>
      <p class="section-description">{{ t('memorySettings.description') }}</p>
    </div>

    <!-- Workspace switch is off: say so plainly instead of showing a personal
         toggle that would appear to work and change nothing. -->
    <div v-if="settings && !settings.workspace_enabled" class="notice">
      <t-icon name="info-circle" />
      <span>{{ t('memorySettings.workspaceDisabled') }}</span>
    </div>

    <div class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('memorySettings.enableLabel') }}</label>
          <p class="desc">{{ t('memorySettings.enableDescription') }}</p>
          <!-- An agent can opt out on its own, so this switch being on is not a
               promise that every conversation uses memory. Say so here rather
               than letting someone conclude the page is broken. -->
          <p v-if="userEnabled && settings?.workspace_enabled" class="desc">
            {{ t('memorySettings.agentDisabledHint') }}
          </p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="userEnabled"
            :disabled="!settings || !settings.workspace_enabled"
            @change="handleEnabledChange"
          />
        </div>
      </div>
    </div>

    <div class="list-section">
      <div class="list-toolbar">
        <div class="list-title">
          <h3>{{ t('memorySettings.listTitle') }}</h3>
          <span class="list-count">{{ t('memorySettings.listCount', { count: totalAll }) }}</span>
        </div>
        <div class="list-actions">
          <t-popup
            v-model="addVisible"
            trigger="click"
            placement="bottom-end"
            destroy-on-close
            overlay-class-name="memory-add-popup-overlay"
          >
            <t-button size="small" variant="text" :disabled="!canWrite">
              <template #icon><t-icon name="add" /></template>
              {{ t('memorySettings.add') }}
            </t-button>
            <template #content>
              <div class="add-popup" @click.stop>
                <div class="add-popup-title">{{ t('memorySettings.addTitle') }}</div>
                <label class="add-field">
                  <span class="add-label">{{ t('memorySettings.addKindLabel') }}</span>
                  <t-select
                    v-model="draftKind"
                    size="small"
                    :popup-props="{ overlayClassName: 'memory-add-kind-popup' }"
                  >
                    <t-option v-for="kind in kinds" :key="kind" :value="kind" :label="kindLabel(kind)" />
                  </t-select>
                  <span class="add-kind-hint">{{ kindHint(draftKind) }}</span>
                </label>
                <label class="add-field">
                  <span class="add-label">{{ t('memorySettings.addContentLabel') }}</span>
                  <t-textarea
                    v-model="draftContent"
                    :placeholder="t('memorySettings.addPlaceholder')"
                    :maxlength="300"
                    :autosize="{ minRows: 3, maxRows: 6 }"
                  />
                </label>
                <div class="add-popup-footer">
                  <t-button size="small" variant="outline" @click="addVisible = false">
                    {{ t('common.cancel') }}
                  </t-button>
                  <t-button size="small" theme="primary" :disabled="!draftContent.trim()" @click="handleCreate">
                    {{ t('memorySettings.add') }}
                  </t-button>
                </div>
              </div>
            </template>
          </t-popup>
          <t-button size="small" variant="text" @click="handleExport">
            <template #icon><t-icon name="download" /></template>
            {{ t('memorySettings.export') }}
          </t-button>
          <t-popconfirm
            :content="t('memorySettings.consolidateConfirm')"
            :confirm-btn="{ content: t('memorySettings.consolidate') }"
            :cancel-btn="t('common.cancel')"
            placement="bottom"
            @confirm="handleConsolidate"
          >
            <t-button
              size="small"
              variant="text"
              :loading="consolidating"
              :disabled="!canWrite || totalAll === 0"
            >
              <template #icon><t-icon name="swap" /></template>
              {{ t('memorySettings.consolidate') }}
            </t-button>
          </t-popconfirm>
          <t-popconfirm
            theme="danger"
            :content="t('memorySettings.clearConfirm')"
            :confirm-btn="{ content: t('memorySettings.clear'), theme: 'danger' }"
            :cancel-btn="t('common.cancel')"
            placement="left"
            @confirm="handleClear"
          >
            <t-button size="small" theme="danger" variant="text" :disabled="totalAll === 0 && trackingCount === 0 && documentCount === 0">
              <template #icon><t-icon name="delete" /></template>
              {{ t('memorySettings.clear') }}
            </t-button>
          </t-popconfirm>
        </div>
      </div>

      <t-tabs :value="tab" class="status-tabs" @change="handleTabChange">
        <t-tab-panel v-for="value in tabs" :key="value" :value="value">
          <template #label>
            <span class="status-tab-label">
              <t-icon :name="tabIcon(value)" size="14px" />
              <span>{{ tabLabel(value) }}</span>
            </span>
          </template>
        </t-tab-panel>
      </t-tabs>

      <t-loading :loading="loading">
        <div v-if="listIsEmpty" class="empty">
          <p class="empty-title">
            {{ emptyTitle }}
          </p>
          <p class="empty-desc">
            {{ emptyDescription }}
          </p>
        </div>
        <p v-if="statusHint" class="status-hint">
          {{ statusHint }}
        </p>
        <ul v-if="isDocuments && documents.length > 0" class="memory-list">
          <li v-for="doc in documents" :key="doc.id" class="memory-item">
            <div class="memory-main">
              <p class="memory-content">{{ doc.title || t('memorySettings.untitledDocument') }}</p>
              <div class="memory-meta">
                <span>{{ t('memorySettings.documentsHits', { hits: doc.hits }) }}</span>
                <span>{{ formatTime(doc.last_used_at) }}</span>
              </div>
            </div>
            <div class="memory-actions">
              <t-button
                size="small"
                theme="primary"
                variant="text"
                :disabled="!doc.knowledge_base_id"
                :title="doc.knowledge_base_id ? t('memorySettings.openDocument') : t('memorySettings.openDocumentUnavailable')"
                @click="handleOpenDocument(doc)"
              >
                <template #icon><t-icon name="jump" /></template>
                {{ t('memorySettings.openDocument') }}
              </t-button>
              <t-popconfirm
                theme="danger"
                :content="t('memorySettings.stopTrackingDocumentConfirm')"
                :confirm-btn="{ content: t('memorySettings.stopTrackingDocument'), theme: 'danger' }"
                :cancel-btn="t('common.cancel')"
                placement="left"
                @confirm="handleStopTrackingDocument(doc)"
              >
                <t-button size="small" theme="default" variant="text">
                  {{ t('memorySettings.stopTrackingDocument') }}
                </t-button>
              </t-popconfirm>
            </div>
          </li>
        </ul>
        <ul v-else-if="isTracking && topics.length > 0" class="memory-list">
          <li v-for="topic in topics" :key="topic.id" class="memory-item">
            <div class="memory-main">
              <p class="memory-content">{{ topic.topic }}</p>
              <div class="topic-progress">
                <t-progress :percentage="topicProgress(topic)" size="small" :label="false" />
                <span>{{ topicProgressText(topic) }}</span>
              </div>
              <div class="memory-meta">
                <span>{{ t('memorySettings.kinds.interest') }}</span>
                <span
                  v-if="topic.aliases && topic.aliases.length > 0"
                  class="memory-topic"
                  :title="topic.aliases.join(', ')"
                >
                  {{ t('memorySettings.trackingAliases', { aliases: topic.aliases.join(', ') }) }}
                </span>
                <span>{{ formatTime(topic.last_seen_at) }}</span>
              </div>
            </div>
            <div class="memory-actions">
              <t-button
                size="small"
                theme="primary"
                variant="text"
                :disabled="!canWrite"
                @click="handlePromoteTopic(topic)"
              >
                <template #icon><t-icon name="star" /></template>
                {{ t('memorySettings.promoteTopic') }}
              </t-button>
              <t-popconfirm
                theme="danger"
                :content="t('memorySettings.dismissTopicConfirm')"
                :confirm-btn="{ content: t('memorySettings.dismissTopic'), theme: 'danger' }"
                :cancel-btn="t('common.cancel')"
                placement="left"
                @confirm="handleDismissTopic(topic)"
              >
                <t-button size="small" theme="default" variant="text">
                  {{ t('memorySettings.dismissTopic') }}
                </t-button>
              </t-popconfirm>
            </div>
          </li>
        </ul>
        <ul v-else-if="isItems && items.length > 0" class="memory-list">
          <li v-for="item in items" :key="item.id" class="memory-item">
            <div class="memory-main">
              <div v-if="editingId === item.id" class="memory-edit">
                <t-textarea
                  v-model="editingContent"
                  :autosize="{ minRows: 2, maxRows: 6 }"
                  @keydown.enter.ctrl="handleSaveEdit(item)"
                />
                <div class="memory-edit-actions">
                  <t-button size="small" variant="outline" @click="editingId = ''">
                    {{ t('common.cancel') }}
                  </t-button>
                  <t-button size="small" theme="primary" @click="handleSaveEdit(item)">
                    {{ t('common.save') }}
                  </t-button>
                </div>
              </div>
              <p v-else class="memory-content" :class="{ inactive: isRetired(item) }">
                {{ item.content }}
              </p>
              <div class="memory-meta">
                <span :title="kindHint(item.kind)">{{ kindLabel(item.kind) }}</span>
                <span
                  v-if="item.topic && item.topic !== item.content"
                  class="memory-topic"
                  :title="item.topic"
                >
                  {{ item.topic }}
                </span>
                <span>{{ originLabel(item.origin) }}</span>
                <span>{{ formatTime(item.valid_from) }}</span>
              </div>
            </div>
            <div class="memory-actions">
              <template v-if="item.status === 'pending'">
                <t-button
                  size="small"
                  theme="primary"
                  variant="text"
                  :disabled="!canWrite"
                  @click="handleConfirm(item)"
                >
                  <template #icon><t-icon name="check" /></template>
                  {{ t('memorySettings.confirmGuess') }}
                </t-button>
                <t-button size="small" theme="default" variant="text" @click="handleReject(item)">
                  <template #icon><t-icon name="close" /></template>
                  {{ t('memorySettings.rejectGuess') }}
                </t-button>
              </template>
              <t-button
                v-if="item.status === 'active'"
                size="small"
                theme="default"
                variant="text"
                shape="square"
                :disabled="!canWrite"
                :title="t('common.edit')"
                @click="startEdit(item)"
              >
                <template #icon><t-icon name="edit" /></template>
              </t-button>
              <!-- Rejecting a guess already drops it and remembers the refusal, so a
                   delete button on a pending row is a weaker duplicate of "No". -->
              <t-popconfirm
                v-if="item.status !== 'pending'"
                theme="danger"
                :content="t('memorySettings.deleteConfirm')"
                :confirm-btn="{ content: t('common.delete'), theme: 'danger' }"
                :cancel-btn="t('common.cancel')"
                placement="left"
                @confirm="handleDelete(item)"
              >
                <t-button
                  size="small"
                  theme="danger"
                  variant="text"
                  shape="square"
                  :title="t('common.delete')"
                >
                  <template #icon><t-icon name="delete" /></template>
                </t-button>
              </t-popconfirm>
            </div>
          </li>
        </ul>
      </t-loading>

      <t-pagination
        v-if="listTotal > pageSize"
        class="memory-pagination"
        :total="listTotal"
        :page-size="pageSize"
        :current="page"
        :show-jumper="false"
        :show-page-size="false"
        @current-change="handlePageChange"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  clearMemoryItems,
  consolidateMemory,
  createMemoryItem,
  confirmMemoryItem,
  deleteMemoryDocument,
  deleteMemoryItem,
  deleteMemoryTopic,
  exportMemoryItems,
  getMemorySettings,
  listMemoryDocuments,
  listMemoryItems,
  listMemoryTopics,
  promoteMemoryTopic,
  rejectMemoryItem,
  updateMemoryEnabled,
  updateMemoryItem,
  type MemoryDoc,
  type MemoryItem,
  type MemoryKind,
  type MemorySettings,
  type MemoryStatus,
  type MemoryTopic,
} from '@/api/memory'

const { t } = useI18n()
const router = useRouter()

type MemoryListTab = MemoryStatus | 'tracking' | 'documents'

const settings = ref<MemorySettings | null>(null)
const userEnabled = ref(false)
const items = ref<MemoryItem[]>([])
const topics = ref<MemoryTopic[]>([])
const documents = ref<MemoryDoc[]>([])
const total = ref(0)
const trackingCount = ref(0)
const documentCount = ref(0)
const tab = ref<MemoryListTab>('active')
const loading = ref(false)
const consolidating = ref(false)
const page = ref(1)
const pageSize = 20

const draftKind = ref<MemoryKind>('fact')
const draftContent = ref('')
const addVisible = ref(false)
const editingId = ref('')
const editingContent = ref('')
const editingImportance = ref(3)

const kinds: MemoryKind[] = ['profile', 'preference', 'fact', 'task', 'interest']
const usageRowKeys = ['alwaysOn', 'situational', 'interest', 'tracking', 'documents', 'pending', 'inactive'] as const

const statuses: MemoryStatus[] = ['active', 'pending', 'superseded', 'archived']
const tabs: MemoryListTab[] = ['active', 'pending', 'tracking', 'documents', 'superseded', 'archived']
const statusLabelKeys: Record<MemoryStatus, string> = {
  active: 'memorySettings.statusActive',
  pending: 'memorySettings.statusPending',
  superseded: 'memorySettings.statusSuperseded',
  archived: 'memorySettings.statusArchived',
}
const counts = ref<Record<MemoryStatus, number>>({
  active: 0,
  pending: 0,
  superseded: 0,
  archived: 0,
})

const isTracking = computed(() => tab.value === 'tracking')
const isDocuments = computed(() => tab.value === 'documents')
const isItems = computed(() => !isTracking.value && !isDocuments.value)
const listTotal = computed(() => {
  if (isTracking.value) return trackingCount.value
  if (isDocuments.value) return documentCount.value
  return total.value
})
const listIsEmpty = computed(() => {
  if (isTracking.value) return topics.value.length === 0
  if (isDocuments.value) return documents.value.length === 0
  return items.value.length === 0
})

const tabLabel = (value: MemoryListTab) => {
  if (value === 'tracking') {
    return `${t('memorySettings.statusTracking')}(${trackingCount.value})`
  }
  if (value === 'documents') {
    return `${t('memorySettings.statusDocuments')}(${documentCount.value})`
  }
  return `${t(statusLabelKeys[value])}(${counts.value[value]})`
}

const tabIcons: Record<MemoryListTab, string> = {
  active: 'check-circle',
  pending: 'help-circle',
  tracking: 'chart-bubble',
  documents: 'file',
  superseded: 'history',
  archived: 'folder',
}

const tabIcon = (value: MemoryListTab) => tabIcons[value]

const totalAll = computed(() => statuses.reduce((sum, value) => sum + counts.value[value], 0))

const emptyTitle = computed(() => {
  if (tab.value === 'pending') return t('memorySettings.pendingEmptyTitle')
  if (tab.value === 'tracking') return t('memorySettings.trackingEmptyTitle')
  if (tab.value === 'documents') return t('memorySettings.documentsEmptyTitle')
  if (tab.value === 'superseded') return t('memorySettings.supersededEmptyTitle')
  if (tab.value === 'archived') return t('memorySettings.archivedEmptyTitle')
  return t('memorySettings.emptyTitle')
})

const emptyDescription = computed(() => {
  if (tab.value === 'pending') return t('memorySettings.pendingEmptyDescription')
  if (tab.value === 'tracking') return t('memorySettings.trackingEmptyDescription')
  if (tab.value === 'documents') return t('memorySettings.documentsEmptyDescription')
  if (tab.value === 'superseded') return t('memorySettings.supersededEmptyDescription')
  if (tab.value === 'archived') return t('memorySettings.archivedEmptyDescription')
  return t('memorySettings.emptyDescription')
})

const statusHint = computed(() => {
  if (isTracking.value) {
    return topics.value.length === 0 ? '' : t('memorySettings.trackingHint')
  }
  if (isDocuments.value) {
    return documents.value.length === 0 ? '' : t('memorySettings.documentsHint')
  }
  if (items.value.length === 0) return ''
  if (tab.value === 'pending') return t('memorySettings.pendingHint')
  if (tab.value === 'superseded') return t('memorySettings.supersededHint')
  if (tab.value === 'archived') return t('memorySettings.archivedHint')
  return ''
})

// Writing requires both switches; the list itself stays readable either way so
// a user who just turned memory off can still review and delete what is stored.
const canWrite = computed(() => settings.value?.effective === true)

// A pending guess is not in use yet, but it is not retired either: the greyed-out
// strike-through reads as "thrown away" and only fits superseded and archived rows.
const isRetired = (item: MemoryItem) =>
  item.status === 'superseded' || item.status === 'archived'

const kindLabel = (kind: MemoryKind) => t(`memorySettings.kinds.${kind}`)

const kindHint = (kind: MemoryKind) => t(`memorySettings.kindHints.${kind}`)

const originLabel = (origin: MemoryItem['origin']) => t(`memorySettings.origins.${origin}`)

const formatTime = (value: string) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const now = new Date()
  const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (date.toDateString() === now.toDateString()) {
    return time
  }
  if (date.getFullYear() === now.getFullYear()) {
    return `${date.getMonth() + 1}/${date.getDate()} ${time}`
  }
  return `${date.getFullYear()}/${date.getMonth() + 1}/${date.getDate()}`
}

const topicProgress = (topic: MemoryTopic) => {
  const threshold = Math.max(topic.threshold, 1)
  return Math.min(100, Math.round((topic.hits / threshold) * 100))
}

const topicProgressText = (topic: MemoryTopic) => {
  if (topic.hits >= topic.threshold) {
    return t('memorySettings.trackingReady')
  }
  return t('memorySettings.trackingProgress', { hits: topic.hits, threshold: topic.threshold })
}

const loadSettings = async () => {
  try {
    const response = await getMemorySettings()
    settings.value = response.data
    userEnabled.value = response.data.user_enabled
  } catch (error: any) {
    console.error('Failed to load memory settings:', error)
  }
}

const loadItems = async () => {
  loading.value = true
  try {
    const response = await listMemoryItems({
      status: tab.value as MemoryStatus,
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    })
    items.value = response.data || []
    total.value = response.total || 0
  } catch (error: any) {
    console.error('Failed to load memories:', error)
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

const loadTopics = async () => {
  loading.value = true
  try {
    const response = await listMemoryTopics({
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    })
    topics.value = response.data || []
    trackingCount.value = response.total || 0
  } catch (error: any) {
    console.error('Failed to load topics:', error)
    topics.value = []
    trackingCount.value = 0
  } finally {
    loading.value = false
  }
}

const loadDocuments = async () => {
  loading.value = true
  try {
    const response = await listMemoryDocuments({
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    })
    documents.value = response.data || []
    documentCount.value = response.total || 0
  } catch (error: any) {
    console.error('Failed to load documents:', error)
    documents.value = []
    documentCount.value = 0
  } finally {
    loading.value = false
  }
}

const loadList = async () => {
  if (isTracking.value) {
    await loadTopics()
    return
  }
  if (isDocuments.value) {
    await loadDocuments()
    return
  }
  await loadItems()
}

// Counts are loaded per status so every tab carries its own size, which is how
// a user finds out an inference is waiting for them without switching tabs.
const loadCounts = async () => {
  const [totals, topicResponse, documentResponse] = await Promise.all([
    Promise.all(
      statuses.map(async (value) => {
        try {
          const response = await listMemoryItems({ status: value, limit: 1 })
          return response.total || 0
        } catch (error: any) {
          return 0
        }
      }),
    ),
    listMemoryTopics({ limit: 1 }).catch(() => ({ total: 0 })),
    listMemoryDocuments({ limit: 1 }).catch(() => ({ total: 0 })),
  ])
  statuses.forEach((value, index) => {
    counts.value[value] = totals[index]
  })
  trackingCount.value = topicResponse.total || 0
  documentCount.value = documentResponse.total || 0
}

const reload = async () => {
  page.value = 1
  await Promise.all([loadList(), loadCounts()])
}

const handlePromoteTopic = async (topic: MemoryTopic) => {
  try {
    await promoteMemoryTopic(topic.id)
    MessagePlugin.success(t('memorySettings.promoteSuccess'))
    tab.value = 'active'
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.promoteFailed'))
  }
}

const handleDismissTopic = async (topic: MemoryTopic) => {
  try {
    await deleteMemoryTopic(topic.id)
    MessagePlugin.success(t('memorySettings.dismissSuccess'))
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.dismissFailed'))
  }
}

const handleOpenDocument = (doc: MemoryDoc) => {
  if (!doc.knowledge_base_id) {
    MessagePlugin.warning(t('memorySettings.openDocumentUnavailable'))
    return
  }
  router.push({
    name: 'knowledgeBaseDetail',
    params: { kbId: doc.knowledge_base_id },
    query: { knowledge_id: doc.knowledge_id },
  })
}

const handleStopTrackingDocument = async (doc: MemoryDoc) => {
  try {
    await deleteMemoryDocument(doc.id)
    MessagePlugin.success(t('memorySettings.stopTrackingDocumentSuccess'))
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.stopTrackingDocumentFailed'))
  }
}

const handleConfirm = async (item: MemoryItem) => {
  try {
    await confirmMemoryItem(item.id)
    MessagePlugin.success(t('memorySettings.confirmSuccess'))
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.confirmFailed'))
  }
}

const handleReject = async (item: MemoryItem) => {
  try {
    await rejectMemoryItem(item.id)
    MessagePlugin.success(t('memorySettings.rejectSuccess'))
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.rejectFailed'))
  }
}

const handleTabChange = async (value: string | number) => {
  tab.value = value as MemoryListTab
  await reload()
}

const handlePageChange = async (current: number) => {
  page.value = current
  await loadList()
}

const handleEnabledChange = async (value: boolean) => {
  try {
    const response = await updateMemoryEnabled(value)
    settings.value = response.data
    userEnabled.value = response.data.user_enabled
    MessagePlugin.success(
      value ? t('memorySettings.toasts.enabled') : t('memorySettings.toasts.disabled'),
    )
  } catch (error: any) {
    userEnabled.value = !value
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

watch(addVisible, (visible) => {
  if (visible) draftContent.value = ''
})

const handleCreate = async () => {
  const content = draftContent.value.trim()
  if (!content) return
  try {
    await createMemoryItem({ kind: draftKind.value, content })
    draftContent.value = ''
    addVisible.value = false
    tab.value = 'active'
    await reload()
    await loadSettings()
    MessagePlugin.success(t('memorySettings.toasts.added'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

const startEdit = (item: MemoryItem) => {
  editingId.value = item.id
  editingContent.value = item.content
  editingImportance.value = item.importance
}

const handleSaveEdit = async (item: MemoryItem) => {
  const content = editingContent.value.trim()
  if (!content) return
  try {
    await updateMemoryItem(item.id, { content, importance: editingImportance.value })
    editingId.value = ''
    await Promise.all([loadItems(), loadCounts()])
    MessagePlugin.success(t('memorySettings.toasts.updated'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

const handleDelete = async (item: MemoryItem) => {
  try {
    await deleteMemoryItem(item.id)
    await Promise.all([loadItems(), loadCounts()])
    await loadSettings()
    MessagePlugin.success(t('memorySettings.toasts.deleted'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

const handleClear = async () => {
  try {
    const response = await clearMemoryItems()
    await reload()
    await loadSettings()
    MessagePlugin.success(t('memorySettings.toasts.cleared', { count: response.removed || 0 }))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

// A review that merges nothing is the normal outcome, so saying only "nothing
// to do" leaves the person unable to tell a tidy store from a broken button.
const consolidateSkipMessage: Record<string, string> = {
  too_few_items: 'memorySettings.consolidateTooFewItems',
  no_candidates: 'memorySettings.consolidateNoCandidates',
  model_declined: 'memorySettings.consolidateModelDeclined',
  too_soon: 'memorySettings.consolidateTooSoon',
}

const handleConsolidate = async () => {
  if (consolidating.value) return
  consolidating.value = true
  try {
    const response = await consolidateMemory()
    const result = response.data
    if (result?.merged || result?.demoted || result?.expired) {
      MessagePlugin.success(
        t('memorySettings.consolidateSuccess', {
          merged: result.merged || 0,
          demoted: result.demoted || 0,
          expired: result.expired || 0,
        }),
      )
    } else if (result?.skipped === 'model_unavailable') {
      // Nothing merged because nothing could be judged, which is a problem to
      // fix rather than a tidy store.
      MessagePlugin.warning(t('memorySettings.consolidateModelUnavailable'))
    } else {
      MessagePlugin.info(t(consolidateSkipMessage[result?.skipped ?? ''] ?? 'memorySettings.consolidateNothing'))
    }
    await reload()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.consolidateFailed'))
  } finally {
    consolidating.value = false
  }
}

const handleExport = async () => {
  try {
    const response = await exportMemoryItems()
    const blob = new Blob([JSON.stringify(response.data || [], null, 2)], {
      type: 'application/json',
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'weknora-memories.json'
    link.click()
    URL.revokeObjectURL(url)
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

onMounted(async () => {
  await loadSettings()
  await reload()
})
</script>

<style lang="less" scoped>
.memory-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 8px 0 0;
    line-height: 1.5;
  }
}

.section-header-titlewrap {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.usage-trigger-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  line-height: 0;
  transition: background-color 0.2s ease, color 0.2s ease;

  :deep(.t-icon) {
    display: block;
  }

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}

.notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  margin-bottom: 16px;
  border-radius: 8px;
  background: var(--td-warning-color-1);
  color: var(--td-text-color-primary);
  font-size: 13px;
}

.status-hint {
  margin: 12px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 18px;
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);
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
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.list-section {
  margin-top: 28px;
}

.list-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.list-title {
  display: flex;
  align-items: baseline;
  gap: 8px;

  h3 {
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }
}

.list-count {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.list-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

.status-tabs {
  margin-top: 0;

  :deep(.t-tabs__header) {
    margin: 0;
    background: transparent;
  }

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
  }

  .status-tab-label {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }

  /* Spacing must live in padding, not margin: TDesign sums item widths
     (excluding margin) to place the active underline. */
  :deep(.t-tabs__nav-item-wrapper) {
    padding: 0 12px;
    margin: 0;
  }

  :deep(.t-tabs__bar + .t-tabs__nav-item .t-tabs__nav-item-wrapper) {
    padding-left: 0;
  }

  :deep(.t-tabs__bar) {
    height: 2px;
  }

  :deep(.t-tabs__operations) {
    display: none;
  }

  :deep(.t-tabs__content) {
    display: none;
  }

  :deep(.t-tabs__nav-container) {
    padding: 0;
  }
}

.memory-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.memory-item {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.memory-main {
  flex: 1;
  min-width: 0;
}

.memory-content {
  margin: 0 0 4px;
  font-size: 14px;
  line-height: 1.6;
  color: var(--td-text-color-primary);
  word-break: break-word;

  &.inactive {
    color: var(--td-text-color-placeholder);
    text-decoration: line-through;
  }
}

.memory-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);

  > span:not(:last-child)::after {
    content: '·';
    margin: 0 6px;
    color: var(--td-text-color-placeholder);
  }
}

.memory-topic {
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.topic-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 6px 0 4px;
  max-width: 360px;

  :deep(.t-progress) {
    flex: 1;
    min-width: 80px;
  }

  > span {
    flex-shrink: 0;
    font-size: 12px;
    line-height: 18px;
    color: var(--td-text-color-placeholder);
  }
}

.memory-edit {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 8px;
}

.memory-edit-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.memory-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  margin-top: -2px;
}

.memory-pagination {
  margin-top: 16px;
}

.empty {
  padding: 32px 0;
  text-align: center;
}

.empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin: 0 0 4px 0;
}

.empty-desc {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  margin: 0;
}
</style>

<!-- t-popup renders into body, so the add form and usage popover have to be styled globally. -->
<style lang="less">
.memory-usage-popup-overlay {
  z-index: 3050 !important;

  .t-popup__content {
    padding: 0 !important;
    width: 380px;
    max-width: calc(100vw - 24px);
    border-radius: 12px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
  }

  .usage-popup {
    padding: 14px 16px 12px;
  }

  .usage-popup-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .usage-popup-intro {
    margin: 4px 0 12px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }

  .usage-popup-rows {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .usage-popup-row {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    line-height: 1.5;
  }

  .usage-popup-label {
    flex: 0 0 88px;
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .usage-popup-text {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }
}

:root[theme-mode='dark'] .memory-usage-popup-overlay .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 4px rgba(0, 0, 0, 0.12),
    0 8px 32px rgba(0, 0, 0, 0.28) !important;
}

.memory-add-popup-overlay {
  z-index: 3050;

  .t-popup__content {
    padding: 14px 16px !important;
    width: 320px;
    max-width: calc(100vw - 24px);
    border-radius: 12px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
  }

  .add-popup {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .add-popup-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .add-field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .add-label {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  .add-kind-hint {
    font-size: 12px;
    line-height: 18px;
    color: var(--td-text-color-placeholder);
  }

  .add-popup-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
}

/* The kind dropdown mounts to body too, above the popup that opened it. */
.memory-add-kind-popup {
  z-index: 6200;
}

:root[theme-mode='dark'] .memory-add-popup-overlay .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 4px rgba(0, 0, 0, 0.12),
    0 8px 32px rgba(0, 0, 0, 0.28) !important;
}
</style>
