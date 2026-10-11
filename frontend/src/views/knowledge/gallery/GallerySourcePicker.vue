<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeFiles } from '@/api/knowledge-base'

// GallerySourcePicker is the "filter by source document" dialog of the image
// gallery: a paginated, keyword-searchable checkbox list of the knowledge
// base's documents. Confirming hands the selected ids to the gallery; the
// selection starts from what is already active so reopening shows the state
// the user last committed.
const props = defineProps<{
  kbId: string
  visible: boolean
  selected: string[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'confirm', knowledgeIds: string[]): void
}>()

const NS = 'knowledgeEditor.wikiBrowser.gallery'
const { t } = useI18n()

const PAGE_SIZE = 20

const keyword = ref('')
const page = ref(1)
const total = ref(0)
const loading = ref(false)
// Documents of the current page: { id, file_name }.
const docs = ref<Array<{ id: string; file_name: string }>>([])
// Draft selection while the dialog is open; committed on confirm.
const draft = ref<Set<string>>(new Set())
// Names of every id in draft, so chips and rows can label selected
// documents that scrolled onto an earlier page.
const namesById = ref<Record<string, string>>({})

let fetchToken = 0
async function loadDocs() {
  if (!props.kbId) return
  const token = ++fetchToken
  loading.value = true
  try {
    const res: any = await listKnowledgeFiles(props.kbId, {
      page: page.value,
      page_size: PAGE_SIZE,
      keyword: keyword.value.trim() || undefined,
    })
    if (token !== fetchToken) return
    const items: any[] = res?.data ?? []
    docs.value = items.map((it) => ({
      id: String(it.id),
      file_name: it.file_name || it.title || it.id,
    }))
    total.value = Number(res?.total) || 0
    for (const d of docs.value) namesById.value[d.id] = d.file_name
  } catch (_) {
    if (token === fetchToken) {
      docs.value = []
      total.value = 0
    }
  } finally {
    if (token === fetchToken) loading.value = false
  }
}

watch(
  () => props.visible,
  (open) => {
    if (!open) return
    draft.value = new Set(props.selected)
    keyword.value = ''
    page.value = 1
    void loadDocs()
  },
)

watch(keyword, () => {
  page.value = 1
  void loadDocs()
})

function onPageChange(next: number) {
  page.value = next
  void loadDocs()
}

function toggle(id: string, checked: boolean) {
  const next = new Set(draft.value)
  if (checked) next.add(id)
  else next.delete(id)
  draft.value = next
}

function onConfirm() {
  if (draft.value.size > 50) {
    MessagePlugin.warning(t(`${NS}.sourceFilterTooMany`))
    return
  }
  emit('confirm', Array.from(draft.value))
  emit('update:visible', false)
}

function onClear() {
  draft.value = new Set()
  emit('confirm', [])
  emit('update:visible', false)
}

const draftCount = () => draft.value.size
</script>

<template>
  <t-dialog
    :visible="props.visible"
    :header="t(`${NS}.sourceFilterTitle`)"
    width="520px"
    :confirm-btn="{
      content: draftCount() ? t(`${NS}.sourceFilterConfirmWithCount`, { count: draftCount() }) : t(`${NS}.sourceFilterConfirm`),
      disabled: !draftCount(),
    }"
    :cancel-btn="t(`${NS}.sourceFilterCancel`)"
    @confirm="onConfirm"
    @close="emit('update:visible', false)"
  >
    <div class="gsp-body">
      <div class="gsp-actions">
        <t-input
          v-model="keyword"
          clearable
          :placeholder="t(`${NS}.sourceFilterSearch`)"
          class="gsp-search"
        >
          <template #prefix-icon><t-icon name="search" size="16px" /></template>
        </t-input>
        <t-button v-if="props.selected.length" variant="text" theme="default" @click="onClear">
          {{ t(`${NS}.sourceFilterClear`) }}
        </t-button>
      </div>

      <t-loading :loading="loading" size="small" show-overlay>
        <div class="gsp-list">
          <t-checkbox
            v-for="d in docs"
            :key="d.id"
            :checked="draft.has(d.id)"
            @change="(checked: boolean) => toggle(d.id, checked)"
          >
            <span class="gsp-doc-name" :title="d.file_name">{{ d.file_name }}</span>
          </t-checkbox>
          <div v-if="!docs.length && !loading" class="gsp-empty">
            {{ t(`${NS}.sourceFilterEmpty`) }}
          </div>
        </div>
      </t-loading>

      <t-pagination
        v-if="total > PAGE_SIZE"
        size="small"
        :current="page"
        :total="total"
        :page-size="PAGE_SIZE"
        :show-page-size="false"
        @change="onPageChange"
      />
    </div>
  </t-dialog>
</template>

<style scoped>
.gsp-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.gsp-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.gsp-search {
  flex: 1;
}

.gsp-list {
  min-height: 160px;
  max-height: 320px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.gsp-doc-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 380px;
}

.gsp-empty {
  padding: 32px 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
