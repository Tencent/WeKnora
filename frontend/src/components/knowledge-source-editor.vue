<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { getKnowledgeSource, saveKnowledgeSource, MAX_SOURCE_BYTES } from '@/api/knowledge-base/knowledgeSource'

const props = defineProps<{ knowledgeId: string }>()
const emit = defineEmits<{ close: []; saved: [knowledge: { id: string; parse_status: string }] }>()
const { t } = useI18n()
const content = ref('')
const original = ref('')
const revision = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const dirty = computed(() => content.value !== original.value)
const byteCount = computed(() => new TextEncoder().encode(content.value).length)
const canSave = computed(() => revision.value && dirty.value && content.value.trim()
  && byteCount.value <= MAX_SOURCE_BYTES && !loading.value && !saving.value)
let generation = 0

async function load() {
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const result = await getKnowledgeSource(props.knowledgeId)
    if (current !== generation) return
    content.value = original.value = result.data.content
    revision.value = result.data.revision
  } catch (cause: any) {
    if (current === generation) error.value = cause?.message || t('sourceEditor.loadFailed')
  } finally {
    if (current === generation) loading.value = false
  }
}

function discardThen(action: () => void, cancel: () => void = () => {}) {
  if (saving.value) { cancel(); return }
  if (!dirty.value) { action(); return }
  const dialog = DialogPlugin.confirm({
    zIndex: 2600,
    header: t('sourceEditor.discardTitle'), body: t('sourceEditor.discardBody'),
    confirmBtn: t('sourceEditor.discard'), cancelBtn: t('common.cancel'),
    onConfirm: () => { dialog.destroy(); action() },
    onClose: () => { dialog.destroy(); cancel() },
  })
}

function requestClose() { discardThen(() => emit('close')) }

async function save() {
  if (!canSave.value) return
  const current = generation
  const id = props.knowledgeId
  saving.value = true
  error.value = ''
  try {
    const result = await saveKnowledgeSource(id, content.value, revision.value)
    if (current !== generation) return
    original.value = content.value
    emit('saved', result.data)
    MessagePlugin.success(t('sourceEditor.saved'))
    emit('close')
  } catch (cause: any) {
    // Preserve the draft on both conflicts and storage/queue failures.
    if (current === generation) error.value = cause?.message || t('sourceEditor.saveFailed')
  } finally {
    if (current === generation) saving.value = false
  }
}

function beforeUnload(event: BeforeUnloadEvent) {
  if (!dirty.value && !saving.value) return
  event.preventDefault()
  event.returnValue = ''
}

watch(() => props.knowledgeId, () => {
  content.value = original.value = revision.value = ''
  saving.value = false
  void load()
}, { immediate: true })
window.addEventListener('beforeunload', beforeUnload)
onBeforeRouteLeave(() => new Promise<boolean>(resolve => {
  discardThen(() => resolve(true), () => resolve(false))
}))
onBeforeUnmount(() => { generation++; window.removeEventListener('beforeunload', beforeUnload) })
defineExpose({ requestClose })
</script>

<template>
  <t-dialog :visible="true" :header="t('sourceEditor.title')" width="min(900px, 94vw)" :z-index="2500" placement="center"
    :footer="false" :close-btn="!saving" :close-on-overlay-click="false" :close-on-esc-keydown="false"
    @close="requestClose">
    <div class="source-editor">
      <p class="source-editor-hint">{{ t('sourceEditor.hint') }}</p>
      <t-alert v-if="error" theme="error" :message="error" />
      <t-loading :loading="loading" :text="t('common.loading')">
        <label for="knowledge-source-content">{{ t('sourceEditor.content') }}</label>
        <textarea id="knowledge-source-content" v-model="content" class="source-editor-input" rows="12"
          :disabled="loading || saving || !revision" :aria-label="t('sourceEditor.content')" />
      </t-loading>
      <p class="source-editor-size" :class="{ 'source-editor-size--invalid': byteCount > MAX_SOURCE_BYTES }"
        aria-live="polite">{{ t('sourceEditor.size', { size: byteCount.toLocaleString() }) }}</p>
      <div class="source-editor-actions">
        <t-button variant="outline" :disabled="loading || saving" @click="discardThen(() => void load())">
          {{ t('sourceEditor.reload') }}
        </t-button>
        <span class="source-editor-spacer" />
        <t-button variant="outline" :disabled="saving" @click="requestClose">{{ t('common.cancel') }}</t-button>
        <t-button :loading="saving" :disabled="!canSave" @click="save">{{ t('sourceEditor.save') }}</t-button>
      </div>
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.source-editor {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-height: calc(100dvh - 160px);
  overflow-y: auto;
  label { display: block; margin-bottom: 8px; }
}
.source-editor-hint { margin: 0; color: var(--td-text-color-secondary); line-height: 1.6; }
.source-editor-input {
  box-sizing: border-box;
  width: 100%;
  min-height: 160px;
  max-height: 50dvh;
  padding: 8px 12px;
  border: 1px solid var(--td-border-level-2-color);
  border-radius: var(--td-radius-default);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font: inherit;
  line-height: 1.6;
  resize: vertical;
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
  &:disabled { background: var(--td-bg-color-component-disabled); color: var(--td-text-color-disabled); }
}
.source-editor-size { margin: 0; color: var(--td-text-color-secondary); font-variant-numeric: tabular-nums; }
.source-editor-size--invalid { color: var(--td-error-color); }
.source-editor-actions {
  position: sticky;
  bottom: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 8px;
  background: var(--td-bg-color-container);
}
.source-editor-spacer { flex: 1; }
</style>
