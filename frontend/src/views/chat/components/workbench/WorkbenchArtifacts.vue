<template>
  <section class="workbench-records">
    <div class="workbench-toolbar">
      <span class="workbench-ellipsis">{{ t('workbench.artifacts') }}</span>
      <t-button size="small" shape="square" variant="text" :loading="loading" :title="t('workbench.refresh')" :aria-label="t('workbench.refresh')" @click="refresh">
        <template #icon><t-icon name="refresh" /></template>
      </t-button>
    </div>
    <div v-if="error" class="workbench-error" role="alert">{{ error }}</div>
    <div v-if="loading" class="workbench-empty"><t-loading size="small" /></div>
    <div v-else-if="!items.length && !error" class="workbench-empty">{{ t('workbench.noArtifacts') }}</div>
    <ul v-else class="workbench-list">
      <li v-for="(item, index) in items" :key="`${item.message_id}:${item.artifact_index}:${index}`" class="artifact-row">
        <t-icon :name="resolveArtifactPreview(item).icon" />
        <button class="artifact-name" :disabled="!artifactTarget(item)" @click="openPreview(item)">
          <span class="workbench-ellipsis" :title="item.file_name">{{ item.file_name }}</span>
          <small>{{ t(`workbench.artifactKinds.${resolveArtifactPreview(item).kind}`) }} · {{ formatWorkbenchBytes(item.file_size) }}<template v-if="!artifactTarget(item)"> · {{ t('workbench.missingArtifactIdentity') }}</template></small>
        </button>
        <t-button variant="text" size="small" shape="square" :loading="downloading === item" :disabled="!artifactTarget(item)" :title="t('workbench.download')" :aria-label="t('workbench.download')" @click="download(item)">
          <template #icon><t-icon name="download" /></template>
        </t-button>
      </li>
      <li v-if="hasMore" class="artifact-load-more">
        <t-button variant="text" :loading="loadingMore" @click="loadMore">{{ t('common.loadMore') }}</t-button>
      </li>
    </ul>
    <ChatArtifactsDrawer
      v-if="preview && artifactTarget(preview)" :visible="true" :session-id="sessionId" restricted-preview :request-signal="signal" :max-preview-bytes="maxBytes"
      :message-id="artifactTarget(preview)!.messageId" :artifacts="[{ ...preview, index: artifactTarget(preview)!.index }]"
      :preview-index="artifactTarget(preview)!.index" @update:visible="value => { if (!value) preview = undefined }"
    />
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { downloadArtifact, listSessionArtifacts, type ArtifactMeta } from '@/api/chat'
import ChatArtifactsDrawer from '../ChatArtifactsDrawer.vue'
import { resolveArtifactPreview } from '@/utils/artifactPreview'
import { artifactTarget, formatWorkbenchBytes, saveWorkbenchBlob, workbenchError } from '@/utils/sandboxWorkbench'

const props = defineProps<{ sessionId: string; signal: AbortSignal; active: boolean; revision: number; maxBytes: number }>()
const { t } = useI18n()
const items = ref<ArtifactMeta[]>([])
const loading = ref(false)
const error = ref('')
const downloading = ref<ArtifactMeta>()
const preview = ref<ArtifactMeta>()
const cursor = ref('')
const hasMore = ref(false)
const loadingMore = ref(false)
let request = 0
let alive = true
const current = () => alive && !props.signal.aborted

function openPreview(item: ArtifactMeta) {
  error.value = ''
  if (item.file_size > props.maxBytes) {
    error.value = t('workbench.fileTooLarge', { size: formatWorkbenchBytes(props.maxBytes) })
    return
  }
  preview.value = item
}

async function refresh() {
  const version = ++request
  loading.value = true
  cursor.value = ''
  hasMore.value = false
  error.value = ''
  try {
    const result = await listSessionArtifacts(props.sessionId, { signal: props.signal, limit: 50 })
    if (!current() || version !== request) return
    if (!result.success || !Array.isArray(result.data)) throw result
    items.value = result.data
    cursor.value = result.next_cursor || ''
    hasMore.value = result.has_more === true && !!cursor.value
  } catch (value) { if (current() && version === request) error.value = workbenchError(value) || t('workbench.requestFailed') }
  finally { if (current() && version === request) loading.value = false }
}

async function loadMore() {
  if (!hasMore.value || !cursor.value || loadingMore.value || !current()) return
  const version = request
  loadingMore.value = true
  error.value = ''
  try {
    const result = await listSessionArtifacts(props.sessionId, { signal: props.signal, cursor: cursor.value, limit: 50 })
    if (!current() || version !== request) return
    if (!result.success || !Array.isArray(result.data)) throw result
    const seen = new Set(items.value.map(item => `${item.message_id}:${item.artifact_index}`))
    items.value.push(...result.data.filter(item => !seen.has(`${item.message_id}:${item.artifact_index}`)))
    cursor.value = result.next_cursor || ''
    hasMore.value = result.has_more === true && !!cursor.value
  } catch (value) {
    if (current() && version === request) error.value = workbenchError(value) || t('workbench.requestFailed')
  } finally { if (current() && version === request) loadingMore.value = false }
}

async function download(item: ArtifactMeta) {
  const target = artifactTarget(item)
  if (!target || downloading.value || !current()) return
  downloading.value = item
  error.value = ''
  try {
    const blob = await downloadArtifact(props.sessionId, target.messageId, target.index, { signal: props.signal })
    if (current()) saveWorkbenchBlob(blob, item.file_name, props.signal)
  } catch (value) { if (current()) error.value = workbenchError(value) || t('workbench.requestFailed') }
  finally { if (current()) downloading.value = undefined }
}

watch(() => [props.active, props.revision], () => { if (props.active) void refresh(); else preview.value = undefined }, { immediate: true })
onBeforeUnmount(() => { alive = false; ++request; preview.value = undefined })
</script>

<style scoped lang="less">
.artifact-row { display: flex; align-items: center; gap: 8px; padding: 12px; border-bottom: 1px solid var(--td-component-stroke); }
.artifact-name { display: flex; flex-direction: column; flex: 1; min-width: 0; gap: 4px; text-align: left; color: var(--td-text-color-primary); border: 0; background: none; cursor: pointer; }
.artifact-name:disabled { cursor: default; }
.artifact-name small { color: var(--td-text-color-secondary); overflow-wrap: anywhere; }
.artifact-name .workbench-ellipsis { max-width: 100%; flex: auto; }
.artifact-load-more { display: flex; justify-content: center; padding: 8px; }
</style>
