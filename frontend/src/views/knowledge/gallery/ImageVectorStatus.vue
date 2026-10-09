<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchImageVectorCoverage, backfillImageVectors, type ImageVectorCoverage } from '@/api/image-gallery'

const props = defineProps<{ knowledgeBaseId: string; canEdit?: boolean }>()
const { t } = useI18n()
const coverage = ref<ImageVectorCoverage | null>(null)
const busy = ref(false)
const failed = ref(false)
let generation = 0
let timer: ReturnType<typeof setTimeout> | undefined
let submittedUntil = 0

async function refresh() {
  const token = ++generation
  if (timer) clearTimeout(timer)
  try {
    const result = await fetchImageVectorCoverage(props.knowledgeBaseId)
    if (token !== generation) return
    coverage.value = result
    failed.value = false
    if (result.pending > 0 || Date.now() < submittedUntil) timer = setTimeout(refresh, 3000)
  } catch {
    if (token === generation) failed.value = true
  }
}
async function rebuild() {
  const kbID = props.knowledgeBaseId
  busy.value = true
  failed.value = false
  try {
    await backfillImageVectors(kbID)
    if (kbID !== props.knowledgeBaseId) return
    submittedUntil = Date.now() + 60_000
    await refresh()
  } catch {
    if (kbID === props.knowledgeBaseId) failed.value = true
  } finally {
    if (kbID === props.knowledgeBaseId) busy.value = false
  }
}
watch(() => props.knowledgeBaseId, () => {
  coverage.value = null
  busy.value = false
  submittedUntil = 0
  void refresh()
}, { immediate: true })
onBeforeUnmount(() => { generation++; if (timer) clearTimeout(timer) })
</script>

<template>
  <div class="image-vector-status" role="status">
    <template v-if="coverage">
      <span v-if="!coverage.enabled">{{ t('imageVectorStatus.disabled') }}</span>
      <span v-else-if="!coverage.supported">{{ t('imageVectorStatus.unsupported') }}</span>
      <span v-else>{{ t('imageVectorStatus.coverage', { ...coverage }) }}</span>
      <t-button v-if="canEdit && coverage.enabled && coverage.supported" size="small" variant="text"
        :loading="busy" :disabled="busy" @click="rebuild">{{ t('imageVectorStatus.backfill') }}</t-button>
    </template>
    <span v-if="failed">{{ t('imageVectorStatus.failed') }}</span>
    <t-button size="small" variant="text" @click="refresh">{{ t('imageVectorStatus.refresh') }}</t-button>
  </div>
</template>

<style scoped>
.image-vector-status { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; color: var(--td-text-color-secondary); font-size: var(--app-text-xs); margin: 0 0 16px; }
</style>
