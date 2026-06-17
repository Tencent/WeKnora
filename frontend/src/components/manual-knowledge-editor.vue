<script setup lang="ts">
import { ref, reactive, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { marked } from 'marked'
import { MessagePlugin } from 'tdesign-vue-next'
import { useUIStore } from '@/stores/ui'
import {
  listKnowledgeBases,
  getKnowledgeDetails,
  getKnowledgeBaseById,
  createManualKnowledge,
  updateManualKnowledge,
} from '@/api/knowledge-base'
import { useUploadConfirmStore } from '@/stores/uploadConfirm'
import { useOrganizationStore } from '@/stores/organization'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import { sanitizeHTML, safeMarkdownToHTML } from '@/utils/security'
import { useI18n } from 'vue-i18n'

interface KnowledgeBaseOption {
  label: string
  value: string
}

interface KnowledgeDetailResponse {
  id: string
  knowledge_base_id: string
  title?: string
  file_name?: string
  metadata?: any
  parse_status?: string
}

type ManualStatus = 'draft' | 'publish'

/** Derive editor status from metadata + parse_status (parse pipeline wins when indexed or in flight). */
const resolveManualKnowledgeStatus = (
  metaStatus: ManualStatus | undefined,
  parseStatus?: string,
): ManualStatus => {
  if (!parseStatus || parseStatus === 'draft') {
    return metaStatus === 'publish' ? 'publish' : 'draft'
  }
  if (
    parseStatus === 'completed' ||
    parseStatus === 'pending' ||
    parseStatus === 'processing' ||
    parseStatus === 'finalizing'
  ) {
    return 'publish'
  }
  return metaStatus === 'publish' ? 'publish' : 'draft'
}

const uiStore = useUIStore()
const uploadConfirmStore = useUploadConfirmStore()
const organizationStore = useOrganizationStore()
const { t } = useI18n()

const visible = computed({
  get: () => uiStore.manualEditorVisible,
  set: (val: boolean) => {
    if (!val) {
      handleClose()
    }
  },
})

const mode = computed(() => uiStore.manualEditorMode)
const knowledgeId = computed(() => uiStore.manualEditorKnowledgeId)

const form = reactive({
  kbId: '' as string,
  title: '',
  content: '',
  status: 'draft' as ManualStatus,
})

const initialLoaded = ref(false)
const kbOptions = ref<KnowledgeBaseOption[]>([])
const kbLoading = ref(false)
const contentLoading = ref(false)
const saving = ref(false)
const savingAction = ref<ManualStatus>('draft')
const activeTab = ref<'edit' | 'preview'>('edit')
const lastUpdatedAt = ref<string>('')

const textareaComponent = ref<any>(null)
const textareaElement = ref<HTMLTextAreaElement | null>(null)
const selectionRange = reactive({ start: 0, end: 0 })
const selectionEvents = ['select', 'keyup', 'click', 'mouseup', 'input']

const resolveTextareaElement = (): HTMLTextAreaElement | null => {
  const component = textareaComponent.value as any
  if (!component) return null
  if (component.textareaRef) {
    return component.textareaRef as HTMLTextAreaElement
  }
  if (component.$el) {
    const el = component.$el.querySelector('textarea')
    if (el) {
      return el as HTMLTextAreaElement
    }
  }
  return null
}

const handleTextareaSelectionEvent = () => {
  const textarea = textareaElement.value ?? resolveTextareaElement()
  if (!textarea) {
    return
  }
  selectionRange.start = textarea.selectionStart ?? 0
  selectionRange.end = textarea.selectionEnd ?? 0
}

const detachTextareaListeners = () => {
  if (!textareaElement.value) {
    return
  }
  selectionEvents.forEach((eventName) => {
    textareaElement.value?.removeEventListener(eventName, handleTextareaSelectionEvent)
  })
  textareaElement.value = null
}

const attachTextareaListeners = () => {
  nextTick(() => {
    const textarea = resolveTextareaElement()
    if (!textarea) {
      return
    }
    if (textareaElement.value === textarea) {
      return
    }
    detachTextareaListeners()
    textareaElement.value = textarea
    selectionEvents.forEach((eventName) => {
      textarea.addEventListener(eventName, handleTextareaSelectionEvent)
    })
    handleTextareaSelectionEvent()
  })
}

const setSelectionRange = (start: number, end: number) => {
  selectionRange.start = start
  selectionRange.end = end
  nextTick(() => {
    const textarea = resolveTextareaElement()
    if (!textarea || activeTab.value !== 'edit') {
      return
    }
    textarea.focus()
    textarea.setSelectionRange(start, end)
  })
}

const getSelectionRange = () => {
  return {
    start: selectionRange.start ?? 0,
    end: selectionRange.end ?? 0,
  }
}

const clampRange = (start: number, end: number, length: number) => {
  let safeStart = Math.max(0, Math.min(start, length))
  let safeEnd = Math.max(0, Math.min(end, length))
  if (safeEnd < safeStart) {
    ;[safeStart, safeEnd] = [safeEnd, safeStart]
  }
  return { safeStart, safeEnd }
}

const updateContentWithSelection = (content: string, start: number, end: number) => {
  form.content = content
  setSelectionRange(start, end)
}

const findLineStart = (value: string, index: number) => {
  if (index <= 0) return 0
  const lastNewline = value.lastIndexOf('\n', index - 1)
  return lastNewline === -1 ? 0 : lastNewline + 1
}

const findLineEnd = (value: string, index: number) => {
  if (index >= value.length) return value.length
  const newlineIndex = value.indexOf('\n', index)
  return newlineIndex === -1 ? value.length : newlineIndex
}

const transformSelectedLines = (transformer: (line: string, index: number) => string) => {
  const value = form.content ?? ''
  const { start, end } = getSelectionRange()
  const { safeStart, safeEnd } = clampRange(start, end, value.length)
  const lineStart = findLineStart(value, safeStart)
  const lineEnd = findLineEnd(value, safeEnd)
  const selected = value.slice(lineStart, lineEnd)
  const lines = selected.split('\n')
  const transformed = lines.map((line, index) => transformer(line, index))
  const result = transformed.join('\n')
  const newContent = value.slice(0, lineStart) + result + value.slice(lineEnd)
  updateContentWithSelection(newContent, lineStart, lineStart + result.length)
}

const wrapSelection = (prefix: string, suffix: string, placeholder: string) => {
  const value = form.content ?? ''
  const { start, end } = getSelectionRange()
  const { safeStart, safeEnd } = clampRange(start, end, value.length)
  const hasSelection = safeEnd > safeStart
  const selectedText = hasSelection ? value.slice(safeStart, safeEnd) : placeholder
  const result =
    value.slice(0, safeStart) + prefix + selectedText + suffix + value.slice(safeEnd)
  const selectionStart = safeStart + prefix.length
  const selectionEnd = selectionStart + selectedText.length
  updateContentWithSelection(result, selectionStart, selectionEnd)
}

const insertBlock = (
  text: string,
  selectionStartOffset?: number,
  selectionEndOffset?: number,
) => {
  const value = form.content ?? ''
  const { start, end } = getSelectionRange()
  const { safeStart, safeEnd } = clampRange(start, end, value.length)
  const before = value.slice(0, safeStart)
  const after = value.slice(safeEnd)
  const result = before + text + after
  const base = safeStart
  const selectionStart =
    selectionStartOffset !== undefined ? base + selectionStartOffset : base + text.length
  const selectionEnd =
    selectionEndOffset !== undefined ? base + selectionEndOffset : selectionStart
  updateContentWithSelection(result, selectionStart, selectionEnd)
}

const applyHeading = (level: number) => {
  const hashes = '#'.repeat(level)
  transformSelectedLines((line) => {
    const trimmed = line.replace(/^#+\s*/, '').trim()
    const content = trimmed || t('manualEditor.placeholders.heading', { level })
    return `${hashes} ${content}`
  })
}

const listPrefixPattern =
  /^(\s*(?:[-*+]|\d+\.)\s+|\s*-\s+\[[ xX]\]\s+)/

const applyBulletList = () => {
  transformSelectedLines((line) => {
    const trimmed = line.trim()
    const content = trimmed.replace(listPrefixPattern, '').trim()
    return `- ${content || t('manualEditor.placeholders.listItem')}`
  })
}

const applyOrderedList = () => {
  transformSelectedLines((line, index) => {
    const trimmed = line.trim()
    const content = trimmed.replace(listPrefixPattern, '').trim()
    return `${index + 1}. ${content || t('manualEditor.placeholders.listItem')}`
  })
}

const applyTaskList = () => {
  transformSelectedLines((line) => {
    const trimmed = line.trim()
    const content = trimmed.replace(listPrefixPattern, '').trim()
    return `- [ ] ${content || t('manualEditor.placeholders.taskItem')}`
  })
}

const applyBlockquote = () => {
  transformSelectedLines((line) => {
    const trimmed = line.trim().replace(/^>\s?/, '').trim()
    return `> ${trimmed || t('manualEditor.placeholders.quote')}`
  })
}

const insertCodeBlock = () => {
  const placeholder = t('manualEditor.placeholders.code')
  const block = `\n\`\`\`\n${placeholder}\n\`\`\`\n`
  const startOffset = block.indexOf(placeholder)
  insertBlock(block, startOffset, startOffset + placeholder.length)
}

const insertHorizontalRule = () => {
  insertBlock('\n---\n\n')
}

const insertTable = () => {
  const cell = t('manualEditor.table.cell')
  const template = `\n| ${t('manualEditor.table.column1')} | ${t('manualEditor.table.column2')} |\n| --- | --- |\n| ${cell} | ${cell} |\n`
  const placeholderIndex = template.indexOf(cell)
  insertBlock(template, placeholderIndex, placeholderIndex + cell.length)
}

const insertLink = () => {
  const value = form.content ?? ''
  const { start, end } = getSelectionRange()
  const { safeStart, safeEnd } = clampRange(start, end, value.length)
  const selectedText =
    safeEnd > safeStart ? value.slice(safeStart, safeEnd) : t('manualEditor.placeholders.linkText')
  const urlPlaceholder = 'https://'
  const result =
    value.slice(0, safeStart) +
    `[${selectedText}](${urlPlaceholder})` +
    value.slice(safeEnd)
  const urlStart = safeStart + selectedText.length + 3
  const urlEnd = urlStart + urlPlaceholder.length
  updateContentWithSelection(result, urlStart, urlEnd)
}

const insertImage = () => {
  const value = form.content ?? ''
  const { start, end } = getSelectionRange()
  const { safeStart, safeEnd } = clampRange(start, end, value.length)
  const altText = safeEnd > safeStart ? value.slice(safeStart, safeEnd) : t('manualEditor.placeholders.imageAlt')
  const urlPlaceholder = 'https://'
  const result =
    value.slice(0, safeStart) +
    `![${altText}](${urlPlaceholder})` +
    value.slice(safeEnd)
  const urlStart = safeStart + altText.length + 4
  const urlEnd = urlStart + urlPlaceholder.length
  updateContentWithSelection(result, urlStart, urlEnd)
}

type ToolbarAction = () => void
type ToolbarButton = {
  key: string
  tooltip: string
  action: ToolbarAction
  icon: string
}
type ToolbarGroup = {
  key: string
  buttons: ToolbarButton[]
}

const toolbarGroups = computed<ToolbarGroup[]>(() => [
  {
    key: 'format',
    buttons: [
      { key: 'bold', icon: 'textformat-bold', tooltip: t('manualEditor.toolbar.bold'), action: () => wrapSelection('**', '**', t('manualEditor.placeholders.bold')) },
      { key: 'italic', icon: 'textformat-italic', tooltip: t('manualEditor.toolbar.italic'), action: () => wrapSelection('*', '*', t('manualEditor.placeholders.italic')) },
      { key: 'strike', icon: 'textformat-strikethrough', tooltip: t('manualEditor.toolbar.strike'), action: () => wrapSelection('~~', '~~', t('manualEditor.placeholders.strike')) },
      { key: 'inline-code', icon: 'code', tooltip: t('manualEditor.toolbar.inlineCode'), action: () => wrapSelection('`', '`', t('manualEditor.placeholders.inlineCode')) },
    ],
  },
  {
    key: 'heading',
    buttons: [
      { key: 'h1', icon: 'numbers-1', tooltip: t('manualEditor.toolbar.heading1'), action: () => applyHeading(1) },
      { key: 'h2', icon: 'numbers-2', tooltip: t('manualEditor.toolbar.heading2'), action: () => applyHeading(2) },
      { key: 'h3', icon: 'numbers-3', tooltip: t('manualEditor.toolbar.heading3'), action: () => applyHeading(3) },
    ],
  },
  {
    key: 'list',
    buttons: [
      { key: 'ul', icon: 'view-list', tooltip: t('manualEditor.toolbar.bulletList'), action: applyBulletList },
      { key: 'ol', icon: 'list-numbered', tooltip: t('manualEditor.toolbar.orderedList'), action: applyOrderedList },
      { key: 'task', icon: 'check-rectangle', tooltip: t('manualEditor.toolbar.taskList'), action: applyTaskList },
      { key: 'quote', icon: 'quote', tooltip: t('manualEditor.toolbar.blockquote'), action: applyBlockquote },
    ],
  },
  {
    key: 'insert',
    buttons: [
      { key: 'codeblock', icon: 'code-1', tooltip: t('manualEditor.toolbar.codeBlock'), action: insertCodeBlock },
      { key: 'link', icon: 'link', tooltip: t('manualEditor.toolbar.link'), action: insertLink },
      { key: 'image', icon: 'image', tooltip: t('manualEditor.toolbar.image'), action: insertImage },
      { key: 'table', icon: 'table', tooltip: t('manualEditor.toolbar.table'), action: insertTable },
      { key: 'hr', icon: 'component-divider-horizontal', tooltip: t('manualEditor.toolbar.horizontalRule'), action: insertHorizontalRule },
    ],
  },
])

const isPreviewMode = computed(() => activeTab.value === 'preview')
const viewToggleIcon = computed(() => (isPreviewMode.value ? 'edit' : 'view-module'))
const viewToggleLabel = computed(() =>
  isPreviewMode.value ? t('manualEditor.view.editLabel') : t('manualEditor.view.previewLabel'),
)

const handleToolbarAction = (action: ToolbarAction) => {
  if (saving.value) {
    return
  }
  if (activeTab.value !== 'edit') {
    activeTab.value = 'edit'
    nextTick(() => {
      attachTextareaListeners()
      action()
    })
  } else {
    attachTextareaListeners()
    action()
  }
}

const toggleEditorView = () => {
  activeTab.value = isPreviewMode.value ? 'edit' : 'preview'
}

marked.use({})

const previewHTML = computed(() => {
  if (!form.content) {
    return `<p class="empty-preview">${t('manualEditor.preview.empty')}</p>`
  }
  const safeMarkdown = safeMarkdownToHTML(form.content)
  const html = marked.parse(safeMarkdown)
  return sanitizeHTML(html)
})

const kbDisabled = computed(() => mode.value === 'edit' && !!form.kbId)

const dialogTitle = computed(() =>
  mode.value === 'edit' ? t('manualEditor.title.edit') : t('manualEditor.title.create'),
)

const lastUpdatedText = computed(() =>
  lastUpdatedAt.value ? t('manualEditor.status.lastUpdated', { time: lastUpdatedAt.value }) : '',
)

const loadKnowledgeBases = async () => {
  kbLoading.value = true
  try {
    const [ownRes, sharedKbs] = await Promise.all([
      listKnowledgeBases() as Promise<any>,
      organizationStore.fetchSharedKnowledgeBases().catch(() => []),
    ])

    const isDocumentKb = (type?: string) => !type || type === 'document'

    const ownKbs = Array.isArray(ownRes?.data) ? ownRes.data : []
    const list: KnowledgeBaseOption[] = ownKbs
      .filter((item: any) => isDocumentKb(item.type))
      .map((item: any) => ({ label: item.name, value: item.id }))

    // Knowledge bases shared to the user with write access (editor/admin)
    // also accept manually-added content, so they must appear in the picker;
    // viewer-only shares are excluded since the backend would reject writes.
    const seen = new Set(list.map((o) => o.value))
    for (const share of sharedKbs) {
      const kb = share?.knowledge_base
      const canWrite = share?.permission === 'editor' || share?.permission === 'admin'
      if (!kb || !canWrite || !isDocumentKb(kb.type) || seen.has(kb.id)) continue
      seen.add(kb.id)
      list.push({ label: kb.name, value: kb.id })
    }

    kbOptions.value = list

    if (mode.value === 'create') {
      const presetKbId = uiStore.manualEditorKBId
      if (presetKbId) {
        const exists = list.find((item) => item.value === presetKbId)
        if (!exists) {
          kbOptions.value.unshift({
            label: t('manualEditor.labels.currentKnowledgeBase'),
            value: presetKbId,
          })
        }
        form.kbId = presetKbId
      } else {
        form.kbId = list[0]?.value ?? ''
      }
    }
  } catch (error) {
    console.error('[ManualEditor] Failed to load knowledge base list:', error)
    kbOptions.value = []
  } finally {
    kbLoading.value = false
  }
}

const parseManualMetadata = (
  metadata: any,
): { content: string; status: ManualStatus; updatedAt?: string } | null => {
  if (!metadata) {
    return null
  }
  try {
    let parsed = metadata
    if (typeof metadata === 'string') {
      parsed = JSON.parse(metadata)
    }
    if (parsed && typeof parsed === 'object') {
      const status = parsed.status === 'publish' ? 'publish' : 'draft'
      return {
        content: parsed.content || '',
        status,
        updatedAt: parsed.updated_at || parsed.updatedAt,
      }
    }
  } catch (error) {
    console.warn('[ManualEditor] Failed to parse manual metadata:', error)
  }
  return null
}

const loadKnowledgeContent = async () => {
  if (!knowledgeId.value) {
    return
  }
  contentLoading.value = true
  try {
    const res: any = await getKnowledgeDetails(knowledgeId.value)
    const data: KnowledgeDetailResponse | undefined = res?.data
    if (!data) {
      MessagePlugin.error(t('manualEditor.error.fetchDetailFailed'))
      return
    }

    form.kbId = data.knowledge_base_id || form.kbId
    const meta = parseManualMetadata(data.metadata)
    form.title =
      data.title ||
      data.file_name?.replace(/\.md$/i, '') ||
      uiStore.manualEditorInitialTitle ||
      ''
    form.content = meta?.content || uiStore.manualEditorInitialContent || ''
    form.status = resolveManualKnowledgeStatus(meta?.status, data.parse_status)
    if (meta?.updatedAt) {
      lastUpdatedAt.value = meta.updatedAt
    }

    if (form.kbId && !kbOptions.value.find((item) => item.value === form.kbId)) {
      kbOptions.value.unshift({
        label: t('manualEditor.labels.currentKnowledgeBase'),
        value: form.kbId,
      })
    }
  } catch (error) {
    console.error('[ManualEditor] Failed to load manual knowledge:', error)
    MessagePlugin.error(t('manualEditor.error.fetchDetailFailed'))
  } finally {
    contentLoading.value = false
  }
}

const resetForm = () => {
  form.kbId = uiStore.manualEditorKBId || ''
  form.title = uiStore.manualEditorInitialTitle || ''
  form.content = uiStore.manualEditorInitialContent || ''
  form.status = uiStore.manualEditorInitialStatus || 'draft'
  activeTab.value = 'edit'
  lastUpdatedAt.value = ''
  initialLoaded.value = false
  selectionRange.start = 0
  selectionRange.end = 0
}

const generateDefaultTitle = () => {
  if (uiStore.manualEditorInitialTitle) {
    return uiStore.manualEditorInitialTitle
  }
  return `${t('manualEditor.defaultTitlePrefix')}-${new Date().toLocaleString()}`
}

const initialize = async () => {
  resetForm()
  await loadKnowledgeBases()

  if (mode.value === 'edit') {
    await loadKnowledgeContent()
  } else {
    const presetKbId = uiStore.manualEditorKBId
    if (presetKbId) {
      form.kbId = presetKbId
    } else if (!form.kbId && kbOptions.value.length) {
      form.kbId = kbOptions.value[0].value
    }
    form.title = form.title || generateDefaultTitle()
    form.content = form.content || ''
  }

  initialLoaded.value = true
}

const validateForm = (targetStatus: ManualStatus): boolean => {
  if (!form.kbId) {
    MessagePlugin.warning(t('manualEditor.warning.selectKnowledgeBase'))
    return false
  }
  if (!form.title || !form.title.trim()) {
    MessagePlugin.warning(t('manualEditor.warning.enterTitle'))
    return false
  }
  if (!form.content || !form.content.trim()) {
    MessagePlugin.warning(t('manualEditor.warning.enterContent'))
    return false
  }
  if (targetStatus === 'publish' && form.content.trim().length < 10) {
    MessagePlugin.warning(t('manualEditor.warning.contentTooShort'))
    return false
  }
  return true
}

const handleSave = async (targetStatus: ManualStatus) => {
  if (saving.value || !validateForm(targetStatus)) {
    return
  }
  saving.value = true
  savingAction.value = targetStatus
  try {
    const tagIdToUpload = uiStore.selectedTagId !== '__untagged__' ? uiStore.selectedTagId : undefined
    const payload: {
      title: string
      content: string
      status: string
      tag_id?: string
      process_config?: KnowledgeProcessOverrides
    } = {
      title: form.title.trim(),
      content: form.content,
      status: targetStatus,
    }
    if (tagIdToUpload) {
      payload.tag_id = tagIdToUpload
    }

    if (targetStatus === 'publish') {
      let kbInfo: any
      try {
        const kbRes: any = await getKnowledgeBaseById(form.kbId)
        kbInfo = kbRes?.data
      } catch {
        MessagePlugin.error(t('manualEditor.error.fetchDetailFailed'))
        return
      }
      if (!kbInfo) {
        MessagePlugin.error(t('manualEditor.error.fetchDetailFailed'))
        return
      }
      try {
        const confirmResult = await uploadConfirmStore.open({
          mode: 'manual',
          kbInfo,
          manual: {
            kbId: form.kbId,
            knowledgeId: knowledgeId.value || undefined,
            title: payload.title,
            content: payload.content,
            tagId: tagIdToUpload,
          },
        })
        payload.process_config = confirmResult.processConfig
      } catch {
        return
      }
    }

    let response: any
    let knowledgeID = knowledgeId.value
    let kbId = form.kbId

    if (mode.value === 'edit' && knowledgeId.value) {
      response = await updateManualKnowledge(knowledgeId.value, payload)
    } else {
      response = await createManualKnowledge(form.kbId, payload)
      knowledgeID = response?.data?.id || knowledgeID
      kbId = form.kbId
    }

    if (response?.success) {
      MessagePlugin.success(
        targetStatus === 'draft'
          ? t('manualEditor.success.draftSaved')
          : t('manualEditor.success.published'),
      )
      if (knowledgeID) {
        uiStore.notifyManualEditorSuccess({
          kbId,
          knowledgeId: knowledgeID,
          status: targetStatus,
        })
      }
      uiStore.closeManualEditor()
    } else {
      const message = response?.message || t('manualEditor.error.saveFailed')
      MessagePlugin.error(message)
    }
  } catch (error: any) {
    const message = error?.error?.message || error?.message || t('manualEditor.error.saveFailed')
    MessagePlugin.error(message)
  } finally {
    saving.value = false
  }
}

const handleClose = () => {
  uiStore.closeManualEditor()
}

watch(visible, async (val) => {
  if (val) {
    await nextTick()
    await initialize()
    await nextTick()
    attachTextareaListeners()
    const length = form.content ? form.content.length : 0
    setSelectionRange(length, length)
  } else {
    detachTextareaListeners()
    resetForm()
  }
})

watch(activeTab, (val) => {
  if (val === 'edit') {
    nextTick(() => {
      attachTextareaListeners()
    })
  } else {
    detachTextareaListeners()
  }
})

onBeforeUnmount(() => {
  detachTextareaListeners()
})
</script>

<template>
  <SettingDrawer
    :visible="visible"
    :title="dialogTitle"
    :description="$t('manualEditor.description')"
    icon="edit-1"
    width="760px"
    :min-width="560"
    :max-width="1280"
    storage-key="setting-drawer:width:manual-markdown-editor"
    :destroy-on-close="false"
    :hide-footer="!initialLoaded"
    :confirm-loading="saving && savingAction === 'publish'"
    :confirm-text="$t('manualEditor.actions.publish')"
    :cancel-text="$t('manualEditor.actions.cancel')"
    @update:visible="(v: boolean) => { visible = v }"
    @confirm="handleSave('publish')"
  >
    <template #footer-left>
      <t-button
        variant="text"
        theme="primary"
        class="toggle-view-btn"
        @click="toggleEditorView"
      >
        <template #icon><t-icon :name="viewToggleIcon" /></template>
        {{ viewToggleLabel }}
      </t-button>
      <t-button
        variant="outline"
        theme="default"
        @click="handleSave('draft')"
        :loading="saving && savingAction === 'draft'"
      >
        {{ $t('manualEditor.actions.saveDraft') }}
      </t-button>
    </template>

    <div class="manual-editor" v-if="initialLoaded">
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('manualEditor.section.basic') }}</h4>

        <div class="form-item">
          <label class="form-label required">{{ $t('manualEditor.form.titleLabel') }}</label>
          <t-input
            v-model="form.title"
            maxlength="100"
            :placeholder="$t('manualEditor.form.titlePlaceholder')"
            showLimitNumber
          />
        </div>

        <div class="form-item">
          <label class="form-label required">{{ $t('manualEditor.form.knowledgeBaseLabel') }}</label>
          <div class="kb-row">
            <t-select
              v-model="form.kbId"
              :disabled="kbDisabled"
              :loading="kbLoading"
              :options="kbOptions"
              :placeholder="$t('manualEditor.form.knowledgeBasePlaceholder')"
              :popup-props="{ attach: 'body', zIndex: 2600 }"
            >
              <template #empty>
                <div style="padding: 20px; text-align: center; color: var(--td-text-color-placeholder);">
                  {{ $t('manualEditor.noDocumentKnowledgeBases') }}
                </div>
              </template>
            </t-select>
            <div class="status-row" v-if="mode === 'edit'">
              <t-tag size="small" theme="warning" variant="light" v-if="form.status === 'draft'">
                {{ $t('manualEditor.status.draftTag') }}
              </t-tag>
              <t-tag size="small" theme="success" variant="light" v-else>
                {{ $t('manualEditor.status.publishedTag') }}
              </t-tag>
            </div>
          </div>
          <p v-if="lastUpdatedText" class="form-desc">{{ lastUpdatedText }}</p>
        </div>
      </section>

      <section class="setting-drawer__section editor-section">
        <h4 class="setting-drawer__section-title">{{ $t('manualEditor.section.content') }}</h4>

        <div class="editor-area">
          <div class="editor-toolbar">
            <template v-for="(group, groupIndex) in toolbarGroups" :key="group.key">
              <div class="toolbar-group">
                <template v-for="btn in group.buttons" :key="btn.key">
                  <t-tooltip :content="btn.tooltip" placement="top">
                    <button
                      type="button"
                      class="toolbar-btn"
                      :class="`btn-${btn.key}`"
                      @mousedown.prevent
                      @click="handleToolbarAction(btn.action)"
                    >
                      <t-icon :name="btn.icon" size="18px" />
                    </button>
                  </t-tooltip>
                </template>
              </div>
              <div
                v-if="groupIndex < toolbarGroups.length - 1"
                class="toolbar-divider"
              ></div>
            </template>
          </div>

          <div class="editor-pane" v-show="activeTab === 'edit'">
            <t-textarea
              ref="textareaComponent"
              v-if="!contentLoading"
              v-model="form.content"
              :placeholder="$t('manualEditor.form.contentPlaceholder')"
              class="editor-textarea"
            />
            <div v-else class="loading-placeholder">
              <t-loading size="small" :text="$t('manualEditor.loading.content')" />
            </div>
          </div>
          <div class="editor-pane editor-pane--preview" v-show="activeTab === 'preview'">
            <div class="preview-container" v-html="previewHTML" />
          </div>
        </div>
      </section>
    </div>
    <div v-else class="loading-wrapper">
      <t-loading size="medium" :text="$t('manualEditor.loading.preparing')" />
    </div>
  </SettingDrawer>
</template>

<style scoped lang="less">
/* 复用模型管理同款 SettingDrawer：分组 section / header 图标 / footer 按钮 / 拖拽调宽。
   这里只负责本编辑器特有的内容样式。内容内联渲染（无 teleport），scoped 生效。 */
.manual-editor {
  display: flex;
  flex-direction: column;
}

.form-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);

  &.required::after {
    content: '*';
    margin-left: 4px;
    color: var(--td-error-color);
  }
}

.form-desc {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.kb-row {
  display: flex;
  align-items: center;
  gap: 12px;

  :deep(.t-select) {
    flex: 1;
    min-width: 0;
  }
}

.status-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  white-space: nowrap;
}

/* 内容分组：让编辑区占满，无需依赖父级 flex 链路，直接用视口高度，稳健 */
.editor-section {
  flex: 1;
  min-height: 0;
}

.editor-toolbar {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
  overflow-x: auto;
  flex-shrink: 0;

  &::-webkit-scrollbar {
    height: 0;
  }
}

.toolbar-group {
  display: flex;
  align-items: center;
  gap: 2px;
}

.toolbar-divider {
  width: 1px;
  height: 18px;
  background: var(--td-component-stroke);
  margin: 0 4px;
}

.toolbar-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  border: none;
  background: transparent;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  align-items: center;
  justify-content: center;
  
  .t-icon {
    color: var(--td-text-color-secondary);
    font-size: 16px;
    width: 16px;
    height: 16px;
  }
}

.toolbar-btn:hover {
  background: rgba(7, 192, 95, 0.08);
  color: var(--td-brand-color);
  
  .t-icon {
    color: var(--td-brand-color);
  }
}

.toolbar-btn.active {
  background: rgba(7, 192, 95, 0.12);
  color: var(--td-brand-color);
  
  .t-icon {
    color: var(--td-brand-color);
  }
}

.toolbar-btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.25);
}

.toolbar-btn:active {
  background: rgba(7, 192, 95, 0.15);
  transform: translateY(0.5px);
}

.editor-area {
  /* 抽屉为整屏高，减去 header/footer/基本信息分组的大致高度，
     让编辑区占据剩余空间且不必撑满父级 flex 链路。 */
  height: calc(100vh - 360px);
  min-height: 280px;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
  background: var(--td-bg-color-container);
  transition: border-color 0.2s ease, box-shadow 0.2s ease;

  &:focus-within {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.1);
  }
}

.editor-pane {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

:deep(.editor-textarea) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  height: 100%;

  .t-textarea__inner {
    flex: 1;
    height: 100% !important;
    resize: none;
    border: none;
    border-radius: 0;
    padding: 14px 16px;
    font-family: var(--app-font-family-mono);
    font-size: 14px;
    line-height: 1.7;
    background: var(--td-bg-color-container);

    &:focus {
      box-shadow: none;
    }
  }
}

.editor-pane--preview {
  background: var(--td-bg-color-container);
}

.preview-container {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px;
  background: var(--td-bg-color-container);
  font-size: 14px;
  line-height: 1.7;
  color: var(--td-text-color-primary);

  :deep(h1),
  :deep(h2),
  :deep(h3),
  :deep(h4) {
    margin-top: 16px;
    margin-bottom: 8px;
  }

  :deep(code) {
    background: var(--td-bg-color-container-hover);
    padding: 2px 4px;
    border-radius: 4px;
    font-family: var(--app-font-family-mono);
  }

  :deep(pre) {
    background: var(--td-bg-color-container-hover);
    padding: 12px;
    border-radius: 6px;
    overflow: auto;
  }

  :deep(blockquote) {
    border-left: 4px solid var(--td-brand-color);
    padding-left: 12px;
    color: var(--td-text-color-secondary);
    margin: 16px 0;
    background: rgba(7, 192, 95, 0.08);
  }

  :deep(a) {
    color: var(--td-brand-color);
  }
}

.loading-wrapper,
.loading-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
  min-height: 280px;
  padding: 20px;
}

.empty-preview {
  color: var(--td-text-color-placeholder);
}
</style>


