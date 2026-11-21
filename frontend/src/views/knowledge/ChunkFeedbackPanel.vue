<template>
  <section class="chunk-feedback">
    <header class="chunk-feedback__header">
      <div>
        <h3>{{ t('chunkFeedback.title') }}</h3>
        <p>{{ t('chunkFeedback.description') }}</p>
      </div>
      <div class="chunk-feedback__filters">
        <t-select v-model="filter" :options="filterOptions" @change="reload(1)" />
        <label class="chunk-feedback__max">
          <span>{{ t('chunkFeedback.maxApproval') }}</span>
          <t-input-number
            v-model="maxApproval"
            :min="0"
            :max="1"
            :step="0.05"
            :decimal-places="2"
            theme="column"
            @change="reload(1)"
          />
        </label>
      </div>
    </header>
    <t-loading :loading="loading">
      <div v-if="!items.length && !loading" class="chunk-feedback__empty">{{ t('chunkFeedback.empty') }}</div>
      <table v-else class="chunk-feedback__table">
        <thead>
          <tr>
            <th>{{ t('chunkFeedback.document') }}</th>
            <th>{{ t('chunkFeedback.preview') }}</th>
            <th>{{ t('chunkFeedback.likes') }}</th>
            <th>{{ t('chunkFeedback.dislikes') }}</th>
            <th>{{ t('chunkFeedback.approvalRate') }}</th>
            <th>{{ t('chunkFeedback.recallWeight') }}</th>
            <th>{{ t('chunkFeedback.sessions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in items" :key="item.chunk_id" @click="openDetail(item.chunk_id)">
            <td>{{ item.knowledge_title || item.knowledge_id }}</td>
            <td>
              {{ item.content_preview }}
              <t-tag v-if="item.needs_optimization" size="small" theme="warning" variant="light">
                {{ t('chunkFeedback.needsOptimization') }}
              </t-tag>
            </td>
            <td>{{ item.like_count }}</td>
            <td>{{ item.dislike_count }}</td>
            <td>{{ formatRate(item.approval_rate) }}</td>
            <td>{{ item.recall_weight.toFixed(2) }}</td>
            <td>{{ item.session_count }}</td>
          </tr>
        </tbody>
      </table>
    </t-loading>
    <t-pagination
      v-if="total > pageSize"
      :current="page"
      :page-size="pageSize"
      :total="total"
      @current-change="reload"
    />
    <t-drawer v-model:visible="detailOpen" :header="t('chunkFeedback.title')" size="420px" :footer="false">
      <t-loading :loading="detailLoading">
        <template v-if="detail">
          <p class="chunk-feedback__preview">{{ detail.content_preview }}</p>
          <dl class="chunk-feedback__stats">
            <div><dt>{{ t('chunkFeedback.likes') }}</dt><dd>{{ detail.like_count }}</dd></div>
            <div><dt>{{ t('chunkFeedback.dislikes') }}</dt><dd>{{ detail.dislike_count }}</dd></div>
            <div><dt>{{ t('chunkFeedback.approvalRate') }}</dt><dd>{{ formatRate(detail.approval_rate) }}</dd></div>
            <div><dt>{{ t('chunkFeedback.recallWeight') }}</dt><dd>{{ detail.recall_weight.toFixed(2) }}</dd></div>
          </dl>
          <h4>{{ t('chunkFeedback.reasons') }}</h4>
          <p v-if="!detail.dislike_reasons.length">{{ t('chunkFeedback.noReasons') }}</p>
          <ul v-else>
            <li v-for="(reason, index) in detail.dislike_reasons" :key="index">
              {{ reason.reason || t('chunkFeedback.noReason') }} · {{ reason.count }}
            </li>
          </ul>
          <h4>{{ t('chunkFeedback.sessions') }}</h4>
          <p v-if="!detail.sessions.length">{{ t('chunkFeedback.empty') }}</p>
          <ul v-else>
            <li v-for="session in detail.sessions" :key="session.message_id">
              {{ session.session_id }}
            </li>
          </ul>
          <h4>{{ t('chunkFeedback.weightLogs') }}</h4>
          <p v-if="!detail.weight_logs.length">{{ t('chunkFeedback.noLogs') }}</p>
          <ul v-else>
            <li v-for="log in detail.weight_logs" :key="log.id">
              {{ triggerLabel(log.trigger_source) }} · {{ log.old_weight.toFixed(2) }} → {{ log.new_weight.toFixed(2) }}
            </li>
          </ul>
          <t-popconfirm v-if="canReset" :content="t('chunkFeedback.resetConfirm')" @confirm="reset">
            <t-button theme="danger" variant="outline">{{ t('chunkFeedback.reset') }}</t-button>
          </t-popconfirm>
        </template>
      </t-loading>
    </t-drawer>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  getChunkFeedback,
  listChunkFeedback,
  resetChunkFeedback,
  type ChunkFeedbackDetail,
  type ChunkFeedbackSummary,
} from '@/api/answerFeedback'

const props = defineProps<{
  knowledgeBaseId: string
  canReset?: boolean
}>()

const { t } = useI18n()
const filter = ref<'rated' | 'low' | 'optimize'>('rated')
const maxApproval = ref<number | undefined>(undefined)
const page = ref(1)
const pageSize = 20
const total = ref(0)
const items = ref<ChunkFeedbackSummary[]>([])
const loading = ref(false)
const detailOpen = ref(false)
const detailLoading = ref(false)
const detail = ref<ChunkFeedbackDetail | null>(null)
const detailId = ref('')

const filterOptions = computed(() => [
  { label: t('chunkFeedback.filterAll'), value: 'rated' },
  { label: t('chunkFeedback.filterLowQuality'), value: 'low' },
  { label: t('chunkFeedback.filterNeedsOptimization'), value: 'optimize' },
])

const formatRate = (rate: number | null) => (rate == null ? t('chunkFeedback.unrated') : `${Math.round(rate * 100)}%`)

const triggerLabel = (source: string) => {
  if (source === 'like') return t('chunkFeedback.triggerLike')
  if (source === 'dislike') return t('chunkFeedback.triggerDislike')
  if (source === 'cancel') return t('chunkFeedback.triggerCancel')
  if (source === 'admin_reset') return t('chunkFeedback.triggerReset')
  return source
}

const reload = async (nextPage = page.value) => {
  if (!props.knowledgeBaseId) return
  page.value = nextPage
  loading.value = true
  try {
    const response = await listChunkFeedback(props.knowledgeBaseId, {
      page: page.value,
      page_size: pageSize,
      max_approval_rate: maxApproval.value,
      low_quality: filter.value === 'low',
      needs_optimization: filter.value === 'optimize',
    })
    const data = response.data as ChunkFeedbackPageSafe
    items.value = data?.items || []
    total.value = data?.total || 0
  } catch (error) {
    items.value = []
    total.value = 0
    MessagePlugin.error(t('chunkFeedback.loadFailed'))
  } finally {
    loading.value = false
  }
}

type ChunkFeedbackPageSafe = { items?: ChunkFeedbackSummary[]; total?: number }

const openDetail = async (chunkId: string) => {
  detailId.value = chunkId
  detailOpen.value = true
  detailLoading.value = true
  try {
    const response = await getChunkFeedback(props.knowledgeBaseId, chunkId)
    detail.value = response.data as ChunkFeedbackDetail
  } catch (error) {
    detail.value = null
    MessagePlugin.error(t('chunkFeedback.detailFailed'))
  } finally {
    detailLoading.value = false
  }
}

const reset = async () => {
  if (!detailId.value) return
  try {
    const response = await resetChunkFeedback(props.knowledgeBaseId, detailId.value)
    detail.value = response.data as ChunkFeedbackDetail
    MessagePlugin.success(t('chunkFeedback.resetDone'))
    await reload(page.value)
  } catch (error) {
    MessagePlugin.error(t('chunkFeedback.resetFailed'))
  }
}

watch(() => props.knowledgeBaseId, () => reload(1), { immediate: true })
</script>

<style scoped>
.chunk-feedback {
  padding: 16px 20px 32px;
}
.chunk-feedback__header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-end;
  margin-bottom: 16px;
}
.chunk-feedback__header h3 {
  margin: 0 0 4px;
}
.chunk-feedback__header p,
.chunk-feedback__empty,
.chunk-feedback__preview {
  margin: 0;
  color: var(--td-text-color-secondary);
}
.chunk-feedback__filters {
  display: flex;
  gap: 12px;
  align-items: flex-end;
}
.chunk-feedback__max {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}
.chunk-feedback__table {
  width: 100%;
  border-collapse: collapse;
}
.chunk-feedback__table th,
.chunk-feedback__table td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--td-component-border);
  vertical-align: top;
}
.chunk-feedback__table tbody tr {
  cursor: pointer;
}
.chunk-feedback__stats {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin: 12px 0;
}
.chunk-feedback__stats div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}
.chunk-feedback__stats dt,
.chunk-feedback__stats dd {
  margin: 0;
}
</style>
