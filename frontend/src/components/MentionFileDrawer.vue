<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getKnowledgeDetails, getKnowledgeDetailsCon } from '@/api/knowledge-base'
import { useMentionFileDrawer } from '@/composables/useMentionFileDrawer'
import { formatStringDate } from '@/utils'

const DocContent = defineAsyncComponent(() => import('@/components/doc-content.vue'))
const { t } = useI18n()
const route = useRoute()
const drawer = useMentionFileDrawer()

const visible = computed(() => drawer?.visible.value ?? false)
const target = computed(() => drawer?.target.value ?? null)
const hasOpened = ref(false)
const loading = ref(false)
const loadError = ref('')
const emptyDetails = () => ({
  id: '',
  title: target.value?.fileName || '',
  knowledge_base_id: '',
  md: [] as unknown[],
  total: 0,
  chunkLoading: false,
  chunkLoadError: '',
})
const details = ref(emptyDetails())
const kbId = computed(() => target.value?.kbId || details.value.knowledge_base_id)
let requestGeneration = 0

const close = () => {
  requestGeneration++
  drawer?.close()
  details.value = emptyDetails()
}

async function loadChunks(page: number) {
  const doc = details.value
  if (!visible.value || !doc.id || doc.chunkLoading) return

  const generation = requestGeneration
  doc.chunkLoading = true
  doc.chunkLoadError = ''
  try {
    const result: any = await getKnowledgeDetailsCon(doc.id, page)
    if (generation !== requestGeneration) return
    if (result?.success && Array.isArray(result.data)) {
      // 使用文档抽屉的页码模型，每次替换当前页的分块。
      doc.md = result.data
      doc.total = result.total ?? result.data.length
    } else {
      doc.chunkLoadError = result?.message || result?.error?.message || t('knowledgeBase.chunkLoadFailed')
    }
  } catch (error: any) {
    if (generation === requestGeneration) {
      doc.chunkLoadError = error?.message || t('knowledgeBase.chunkLoadFailed')
    }
  } finally {
    if (generation === requestGeneration) doc.chunkLoading = false
  }
}

// 对话入口只负责把引用目标转换为文档抽屉需要的详情和分页数据。
watch([visible, () => target.value?.fileId], async ([open, fileId]) => {
  const generation = ++requestGeneration
  if (!open || !fileId) return

  hasOpened.value = true
  details.value = emptyDetails()
  loading.value = true
  loadError.value = ''
  try {
    const { agent_id, agent_source_tenant_id } = route.query
    const result: any = await getKnowledgeDetails(fileId, {
      agent_id: typeof agent_id === 'string' ? agent_id : undefined,
      agent_source_tenant_id: typeof agent_source_tenant_id === 'string' ? agent_source_tenant_id : undefined,
    })
    if (generation !== requestGeneration) return
    if (!result?.success || !result.data) {
      loadError.value = result?.message || result?.error?.message || t('knowledgeBase.chunkLoadFailed')
      return
    }

    const data = result.data
    details.value = {
      ...emptyDetails(),
      ...data,
      title: data.file_name || data.title || data.source || target.value?.fileName || t('knowledgeBase.untitledDocument'),
      type: data.type || 'file',
      time: data.updated_at ? formatStringDate(data.updated_at) : '',
    }
    void loadChunks(1)
  } catch (error: any) {
    if (generation === requestGeneration) loadError.value = error?.message || t('knowledgeBase.chunkLoadFailed')
  } finally {
    if (generation === requestGeneration) loading.value = false
  }
}, { immediate: true })

onBeforeUnmount(() => { requestGeneration++ })
</script>

<template>
  <DocContent
    v-if="hasOpened"
    :visible="visible"
    :details="details"
    :kb-id="kbId"
    read-only
    @close-doc="close"
    @get-doc="loadChunks"
  >
    <template v-if="loading || loadError" #status>
      <div v-if="loadError" class="mention-drawer-status" role="alert">
        <t-icon name="error-circle" size="20px" />
        <span>{{ loadError }}</span>
      </div>
      <div v-else class="mention-drawer-status" role="status">
        <t-loading size="small" />
        <span>{{ t('common.loading') }}</span>
      </div>
    </template>
  </DocContent>
</template>

<style scoped lang="less">
.mention-drawer-status {
  min-height: 120px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--td-text-color-secondary);

  &[role='alert'] {
    color: var(--td-error-color);
  }
}
</style>
