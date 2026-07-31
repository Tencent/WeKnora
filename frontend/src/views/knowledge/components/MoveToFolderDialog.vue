<template>
  <!-- Folder picker used by both the batch bar and the single-document menu.
       It re-fetches on every open because folders can be created from the
       sidebar while the dialog is closed, and a stale list would silently
       file documents into a folder the user just renamed or removed. -->
  <t-dialog :visible="visible" :footer="false" width="420px" dialog-class-name="move-folder-dialog"
    :close-on-overlay-click="false" destroy-on-close @close="handleClose">
    <template #header>
      <div class="move-folder-heading">
        <div class="move-folder-heading-row">
          <t-icon name="folder" size="16px" class="move-folder-heading-icon" aria-hidden="true" />
          <span class="move-folder-title">{{ t('knowledgeBase.moveToFolderHeading') }}</span>
        </div>
        <p class="move-folder-subtitle">{{ t('knowledgeBase.moveToFolderSubtitle', { count }) }}</p>
      </div>
    </template>

    <div class="move-folder-body">
      <div class="move-folder-search">
        <t-input v-model.trim="keyword" size="small" clearable
          :placeholder="t('knowledgeBase.folderSearchPlaceholder')">
          <template #prefix-icon>
            <t-icon name="search" size="14px" />
          </template>
        </t-input>
      </div>

      <div class="move-folder-list">
        <t-loading v-if="loading" size="small" class="move-folder-loading" />
        <template v-else>
          <!-- Root is always offered, including while searching: "take these
               out of any folder" is a destination, not a search result. -->
          <button type="button" class="move-folder-row" :class="{ 'is-active': target === '' }" @click="target = ''">
            <t-icon name="folder-open" class="move-folder-row__icon" />
            <span class="move-folder-row__label">{{ t('knowledgeBase.folderUnfiled') }}</span>
            <t-icon v-if="target === ''" name="check" class="move-folder-row__check" />
          </button>

          <button v-for="row in visibleRows" :key="row.id" type="button" class="move-folder-row"
            :class="{ 'is-active': target === row.id }" :style="{ '--kb-folder-depth': keyword ? 0 : row.depth - 1 }"
            :title="row.namePath" @click="target = row.id">
            <t-icon name="folder" class="move-folder-row__icon" />
            <span class="move-folder-row__label">{{ keyword ? row.namePath : row.name }}</span>
            <t-icon v-if="target === row.id" name="check" class="move-folder-row__check" />
          </button>

          <p v-if="!visibleRows.length" class="move-folder-empty">
            {{ keyword ? t('knowledgeBase.folderSearchEmpty') : t('knowledgeBase.folderEmptyHint') }}
          </p>
        </template>
      </div>
    </div>

    <div class="move-folder-footer">
      <span class="move-folder-target">{{ targetLabel }}</span>
      <div class="move-folder-footer-right">
        <t-button variant="outline" size="small" :disabled="submitting" @click="handleClose">
          {{ t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="submitting" :disabled="target === null" @click="handleConfirm">
          {{ t('common.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeFolders, moveKnowledgeToFolder, type KnowledgeFolderNode } from '@/api/knowledge-base'

const props = defineProps<{
  visible: boolean
  kbId: string
  /** Documents to file. The dialog owns the request so callers stay thin. */
  knowledgeIds: string[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  /** Emitted after a successful move so the caller can refresh its list. */
  (e: 'moved', payload: { folderId: string; count: number }): void
}>()

const { t } = useI18n()

const loading = ref(false)
const submitting = ref(false)
const keyword = ref('')
const folders = ref<KnowledgeFolderNode[]>([])
/** null = nothing picked yet, '' = unfiled root, otherwise a folder id. */
const target = ref<string | null>(null)

const count = computed(() => props.knowledgeIds.length)

interface FolderRow extends KnowledgeFolderNode {
  namePath: string
}

const allRows = computed<FolderRow[]>(() =>
  folders.value.map((folder) => ({
    ...folder,
    namePath: (folder.name_path || [folder.name]).join(' / '),
  })),
)

const visibleRows = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return allRows.value
  // Match on the full path so "docs/api" narrows the same way a breadcrumb reads.
  return allRows.value.filter((row) => row.namePath.toLowerCase().includes(q))
})

const targetLabel = computed(() => {
  if (target.value === null) return t('knowledgeBase.moveToFolderPickHint')
  if (target.value === '') return t('knowledgeBase.folderUnfiled')
  const row = allRows.value.find((item) => item.id === target.value)
  return row ? row.namePath : ''
})

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    keyword.value = ''
    target.value = null
    load()
  },
)

async function load() {
  if (!props.kbId) return
  loading.value = true
  try {
    const res: any = await listKnowledgeFolders(props.kbId, { recursive: true })
    folders.value = (res?.data || res)?.folders || []
  } catch {
    folders.value = []
  } finally {
    loading.value = false
  }
}

function handleClose() {
  emit('update:visible', false)
}

async function handleConfirm() {
  if (target.value === null || !props.knowledgeIds.length) return
  submitting.value = true
  try {
    const res: any = await moveKnowledgeToFolder(props.kbId, props.knowledgeIds, target.value)
    const moved = res?.moved ?? props.knowledgeIds.length
    MessagePlugin.success(t('knowledgeBase.folderMoveDocsSuccess', { count: moved }))
    emit('moved', { folderId: target.value, count: moved })
    handleClose()
  } catch (err: any) {
    MessagePlugin.error(
      err?.response?.data?.error?.message || err?.message || t('knowledgeBase.folderMoveDocsFailed'),
    )
  } finally {
    submitting.value = false
  }
}
</script>

<style lang="less" scoped>
.move-folder-heading-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.move-folder-heading-icon {
  color: var(--td-brand-color);
}

.move-folder-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.move-folder-subtitle {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.move-folder-search {
  margin-bottom: 8px;
}

.move-folder-list {
  max-height: 320px;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 4px;
}

.move-folder-loading,
.move-folder-empty {
  padding: 16px 8px;
  margin: 0;
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.move-folder-row {
  --kb-folder-depth: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  height: 32px;
  padding-right: 8px;
  padding-left: calc(8px + var(--kb-folder-depth) * 14px);
  border: none;
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
  font-size: 13px;
  color: var(--td-text-color-primary);
  text-align: left;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.move-folder-row__icon {
  flex: 0 0 auto;
  font-size: 15px;
  color: var(--td-text-color-secondary);
}

.move-folder-row__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.move-folder-row__check {
  flex: 0 0 auto;
  color: var(--td-brand-color);
}

.move-folder-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 16px;
}

.move-folder-target {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.move-folder-footer-right {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}
</style>
