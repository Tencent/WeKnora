<template>
  <t-drawer
    :visible="visible"
    :header="drawerTitle"
    size="620px"
    :footer="false"
    @close="$emit('update:visible', false)"
  >
    <div class="pd-drawer">
      <t-tabs v-model="activeState" @change="loadFirstPage">
        <t-tab-panel value="running" :label="t('processingDashboard.running')" />
        <t-tab-panel value="queued" :label="t('processingDashboard.queued')" />
        <t-tab-panel value="retrying" :label="t('processingDashboard.retrying')" />
      </t-tabs>

      <div class="pd-drawer__body">
        <t-loading v-if="loading" />
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
          <t-button v-if="nextCursor" block variant="outline" :loading="loadingMore" @click="loadMore">
            {{ t('processingDashboard.drawer.loadMore') }}
          </t-button>
        </template>
        <div v-else class="pd-drawer__empty">{{ t('processingDashboard.drawer.empty') }}</div>
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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

const drawerTitle = computed(() => props.stage ? t(`processingDashboard.stage.${props.stage}.title`) : t('processingDashboard.drawer.title'))

const loadPage = async (cursor = '') => {
  if (!props.stage) return
  if (cursor) loadingMore.value = true
  else loading.value = true
  try {
    const res = await listProcessingStageItems({
      stage: props.stage,
      state: activeState.value,
      cursor,
      page_size: 20,
      kb_id: props.kbId,
      keyword: props.keyword,
    })
    items.value = cursor ? [...items.value, ...res.data.items] : res.data.items
    nextCursor.value = res.data.next_cursor || ''
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

const loadFirstPage = () => {
  nextCursor.value = ''
  void loadPage('')
}

const loadMore = () => {
  if (nextCursor.value) void loadPage(nextCursor.value)
}

watch(() => [props.visible, props.stage, props.kbId, props.keyword], () => {
  if (props.visible && props.stage) loadFirstPage()
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
</style>
