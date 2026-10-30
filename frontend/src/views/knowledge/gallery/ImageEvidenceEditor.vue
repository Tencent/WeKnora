<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { get } from '@/utils/request'
import { getChunkByIdOnly, updateDocumentChunk } from '@/api/knowledge-base'
import type { ImageAsset } from '@/api/image-gallery'

const props = defineProps<{ image: ImageAsset; field: 'caption' | 'ocr_text' }>()
const emit = defineEmits<{ (e: 'saved'): void }>()
const { t } = useI18n()
const editing = ref(false)
const loading = ref(false)
const saving = ref(false)
const draft = ref('')
const error = ref('')
const chunk = ref<any>(null)
let sequence = 0

function reset() {
  sequence++
  editing.value = false
  loading.value = false
  saving.value = false
  chunk.value = null
  draft.value = ''
  error.value = ''
}
watch(() => [props.image.id, props.field], reset)
onBeforeUnmount(reset)

function matchesImage(row: any, image: ImageAsset): boolean {
  try {
    const images = JSON.parse(row.image_info || '[]')
    return Array.isArray(images) && images.some(info => (info?.url || info?.original_url) === image.url)
  } catch {
    return false
  }
}

async function beginEdit() {
  const token = ++sequence
  const image = props.image
  const kind = props.field === 'caption' ? 'image_caption' : 'image_ocr'
  editing.value = true
  loading.value = true
  error.value = ''
  chunk.value = null
  try {
    // Reuse the existing typed chunk listing; the gallery's winning copy
    // can itself be a source-text or visual-vector chunk and is not editable.
    for (let page = 1; ; page++) {
      const query = new URLSearchParams({ chunk_type: kind, page: String(page), page_size: '100' })
      const result: any = await get(`/api/v1/chunks/${encodeURIComponent(image.knowledge_id)}?${query}`)
      if (token !== sequence) return
      const rows = Array.isArray(result.data) ? result.data : []
      const found = rows.find((row: any) => matchesImage(row, image))
      if (found) {
        const response: any = await getChunkByIdOnly(found.id)
        if (token !== sequence) return
        const current = response.data
        if (current?.knowledge_id !== image.knowledge_id || current.chunk_type !== kind || !matchesImage(current, image)) {
          throw new Error(t('common.noData'))
        }
        chunk.value = current
        draft.value = current.content
        return
      }
      if (!rows.length || page * 100 >= result.total) throw new Error(t('common.noData'))
    }
  } catch (e: any) {
    if (token === sequence) error.value = e?.message || t('common.error')
  } finally {
    if (token === sequence) loading.value = false
  }
}

async function save() {
  if (!chunk.value || saving.value || chunk.value.knowledge_id !== props.image.knowledge_id) return
  if (!draft.value.trim()) {
    error.value = t('knowledgeBase.chunkContentRequired')
    return
  }
  const token = sequence
  const current = chunk.value
  saving.value = true
  error.value = ''
  try {
    const result: any = await updateDocumentChunk(current.knowledge_id, current.id, {
      content: draft.value, expected_revision: current.content_revision,
    })
    if (token !== sequence) return
    chunk.value = result.data
    if (result.data?.index_status === 'failed') {
      error.value = t('knowledgeBase.chunkSavedIndexFailed')
      return
    }
    editing.value = false
    emit('saved')
  } catch (e: any) {
    if (token !== sequence) return
    error.value = e?.message || t('common.saveFailed')
    if (e?.status === 409) chunk.value = null
  } finally {
    if (token === sequence) saving.value = false
  }
}
</script>

<template>
  <div class="image-evidence-editor">
    <t-button v-if="!editing" variant="text" size="small" @click="beginEdit">{{ t('common.edit') }}</t-button>
    <template v-else>
      <t-loading v-if="loading" size="small" />
      <textarea v-else-if="chunk" v-model="draft" :aria-label="t(`knowledgeEditor.wikiBrowser.gallery.${field === 'caption' ? 'caption' : 'ocr'}`)" :disabled="saving" />
      <p v-if="error" role="alert">{{ error }}</p>
      <div class="image-evidence-editor__actions">
        <t-button v-if="chunk" size="small" :loading="saving" @click="save">
          {{ t(chunk.index_status === 'failed' ? 'knowledgeBase.retryIndex' : 'common.save') }}
        </t-button>
        <t-button v-else-if="!loading" size="small" @click="beginEdit">{{ t('common.refresh') }}</t-button>
        <t-button size="small" variant="text" :disabled="saving" @click="reset">{{ t('common.cancel') }}</t-button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.image-evidence-editor { margin-top: 8px; }
.image-evidence-editor textarea { box-sizing: border-box; width: 100%; min-height: 140px; resize: vertical; padding: 8px; border: 1px solid var(--td-component-border); border-radius: 4px; color: inherit; background: var(--td-bg-color-container); font: inherit; }
.image-evidence-editor p { color: var(--td-error-color); }
.image-evidence-editor__actions { display: flex; gap: 8px; margin-top: 8px; }
</style>
