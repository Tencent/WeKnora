<template>
  <t-drawer
    :visible="visible"
    :header="drawerTitle"
    size="min(620px, 100vw)"
    :footer="false"
    @close="$emit('update:visible', false)"
  >
    <div class="pd-drawer">
      <t-tabs v-model="activeState" @change="handleStateChange">
        <t-tab-panel value="running" :label="t('processingDashboard.running')" />
        <t-tab-panel value="queued" :label="t('processingDashboard.queued')" />
        <t-tab-panel value="retrying" :label="t('processingDashboard.retrying')" />
      </t-tabs>

      <div class="pd-drawer__body">
        <t-loading v-if="loading" />
        <div v-else-if="error && !items.length" class="pd-drawer__error">
          <t-alert theme="error" :message="error" />
          <t-button size="small" variant="outline" @click="loadFirstPage">
            {{ t('processingDashboard.drawer.retry') }}
          </t-button>
        </div>
        <template v-else-if="items.length">
          <button
            v-for="item in items"
            :key="`${item.knowledge_id}:${item.attempt}:${item.stage}`"
            class="pd-drawer-item"
            type="button"
            @click="$emit('openKnowledge', item)"
          >
            <span class="pd-drawer-item__main">
              <strong>{{ item.title }}</strong>
              <small>{{ item.knowledge_base_name }} · attempt {{ item.attempt }}</small>
            </span>
            <span class="pd-drawer-item__meta">
              <ProgressValue :progress="item.progress" :fallback="item.phase || stateLabel(item.state)" />
              <small>{{ item.last_progress_at ? formatTime(item.last_progress_at) : '' }}</small>
            </span>
            <t-tag v-if="item.failed_children" theme="danger" variant="light" size="small">
              {{ item.failed_children }} {{ t('processingDashboard.drawer.failedChildren') }}
            </t-tag>
          </button>
          <div v-if="error" class="pd-drawer__load-more-error">
            <t-alert theme="error" :message="error" />
            <t-button size="small" variant="outline" :loading="loadingMore" @click="retryLoadMore">
              {{ t('processingDashboard.drawer.retry') }}
            </t-button>
          </div>
          <t-button v-else-if="nextCursor" block variant="outline" :loading="loadingMore" @click="loadMore">
            {{ t('processingDashboard.drawer.loadMore') }}
          </t-button>
        </template>
        <div v-else class="pd-drawer__empty">{{ t('processingDashboard.drawer.empty') }}</div>
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listProcessingStageItems } from '@/api/processing-dashboard'
import type { ProcessingLogicalStage, ProcessingStageItem } from '@/types/processingDashboard'
import ProgressValue from './ProgressValue.vue'

const props = defineProps<{
  visible: boolean
  stage: ProcessingLogicalStage | ''
  kbId?: string
  keyword?: string
}>()

defineEmits<{
  'update:visible': [value: boolean]
  openKnowledge: [item: ProcessingStageItem]
}>()

const { t } = useI18n()
const activeState = ref<'running' | 'queued' | 'retrying'>('running')
const items = ref<ProcessingStageItem[]>([])
const nextCursor = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
let latestRequestId = 0
let activeController: AbortController | null = null

const drawerTitle = computed(() => props.stage ? t(`processingDashboard.stage.${props.stage}.title`) : t('processingDashboard.drawer.title'))

const invalidateRequests = () => {
  latestRequestId++
  activeController?.abort()
  activeController = null
}

const loadPage = async (cursor = '') => {
  if (!props.stage) return
  const requestId = ++latestRequestId
  const reqStage = props.stage
  const reqState = activeState.value
  const reqKbId = props.kbId
  const reqKeyword = props.keyword
  activeController?.abort()
  const controller = new AbortController()
  activeController = controller
  if (cursor) loadingMore.value = true
  else loading.value = true
  error.value = ''
  try {
    const res = await listProcessingStageItems({
      stage: reqStage,
      state: reqState,
      cursor,
      page_size: 20,
      kb_id: reqKbId,
      keyword: reqKeyword,
    }, controller.signal)
    if (requestId !== latestRequestId) return
    if (reqStage !== props.stage || reqState !== activeState.value || reqKbId !== props.kbId || reqKeyword !== props.keyword) return
    items.value = cursor ? [...items.value, ...res.data.items] : res.data.items
    nextCursor.value = res.data.next_cursor || ''
  } catch (e: any) {
    if (requestId !== latestRequestId || e?.name === 'CanceledError' || e?.name === 'AbortError') return
    error.value = e?.message || t('processingDashboard.drawer.loadFailed')
  } finally {
    if (requestId === latestRequestId) {
      loading.value = false
      loadingMore.value = false
      if (activeController === controller) activeController = null
    }
  }
}

const loadFirstPage = () => {
  invalidateRequests()
  nextCursor.value = ''
  items.value = []
  void loadPage('')
}

const loadMore = () => {
  if (nextCursor.value) void loadPage(nextCursor.value)
}

const retryLoadMore = () => {
  if (nextCursor.value) void loadPage(nextCursor.value)
  else void loadPage('')
}

const handleStateChange = () => {
  loadFirstPage()
}

watch(() => [props.visible, props.stage, props.kbId, props.keyword], () => {
  invalidateRequests()
  nextCursor.value = ''
  items.value = []
  error.value = ''
  if (props.visible && props.stage) void loadPage('')
})

onBeforeUnmount(() => {
  invalidateRequests()
})

const stateLabel = (state: string) => t(`processingDashboard.state.${state}`)
const formatTime = (value: string) => new Date(value).toLocaleString()
</script>

<style scoped>
.pd-drawer {
  min-height: 100%;
}

.pd-drawer__body {
  padding-top: 16px;
}

.pd-drawer__error,
.pd-drawer__load-more-error {
  margin-bottom: 12px;
  display: grid;
  gap: 8px;
}

.pd-drawer-item {
  width: 100%;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 12px;
  align-items: center;
  padding: 12px 0;
  border: 0;
  border-bottom: 1px solid #edf1ef;
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.pd-drawer-item:hover strong {
  color: #009966;
}

.pd-drawer-item__main,
.pd-drawer-item__meta {
  min-width: 0;
  display: grid;
  gap: 4px;
}

.pd-drawer-item strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #1f2d35;
}

.pd-drawer-item small {
  color: #71808a;
}

.pd-drawer__empty {
  padding: 48px 0;
  color: #8b99a3;
  text-align: center;
}

@media (max-width: 560px) {
  .pd-drawer-item {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }

  .pd-drawer-item__meta {
    justify-items: start;
  }
}
</style>
