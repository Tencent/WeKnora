<script setup lang="ts">
import { ref, reactive, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { marked } from 'marked'
import { MessagePlugin } from 'tdesign-vue-next'
import { useUIStore } from '@/stores/ui'
import { listKnowledgeBases, getKnowledgeDetails, createManualKnowledge, updateManualKnowledge } from '@/api/knowledge-base'
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

const uiStore = useUIStore()
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
const viewToggleTooltip = computed(() =>
  isPreviewMode.value
    ? t('manualEditor.view.toggleToEdit')
    : t('manualEditor.view.toggleToPreview'),
)
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

marked.use({
  mangle: false,
  headerIds: false,
})

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
    const res: any = await listKnowledgeBases()
    console.log('[ManualEditor] Raw knowledge bases response:', res?.data)
    
    const allKbs = Array.isArray(res?.data) ? res.data : []
    console.log('[ManualEditor] All knowledge bases:', allKbs)
    console.log('[ManualEditor] KB types:', allKbs.map((kb: any) => ({ name: kb.name, type: kb.type })))
    
    const list: KnowledgeBaseOption[] = allKbs
      .filter((item: any) => {
        const isDocument = !item.type || item.type === 'document'
        console.log(`[ManualEditor] KB "${item.name}" (type: ${item.type}): ${isDocument ? 'INCLUDED' : 'FILTERED OUT'}`)
        return isDocument
      })
      .map((item: any) => ({ label: item.name, value: item.id }))
    
    console.log('[ManualEditor] Filtered knowledge bases:', list)
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
    
    console.log('[ManualEditor] Final kbOptions:', kbOptions.value)
    console.log('[ManualEditor] Selected kbId:', form.kbId)
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
    form.status = meta?.status || (data.parse_status === 'completed' ? 'publish' : 'draft')
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
    const payload: { title: string; content: string; status: string; tag_id?: string } = {
      title: form.title.trim(),
      content: form.content,
      status: targetStatus,
    }
    let response: any
    let knowledgeID = knowledgeId.value
    let kbId = form.kbId

    if (mode.value === 'edit' && knowledgeId.value) {
      response = await updateManualKnowledge(knowledgeId.value, payload)
    } else {
      // 创建新知识时，从 store 获取当前选中的分类ID
      const tagIdToUpload = uiStore.selectedTagId !== '__untagged__' ? uiStore.selectedTagId : undefined
      if (tagIdToUpload) {
        payload.tag_id = tagIdToUpload
      }
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
  <t-dialog
    v-model:visible="visible"
    :header="dialogTitle"
    :closeBtn="true"
    :footer="false"
    width="880px"
    top="5%"
    class="manual-knowledge-editor"
    destroy-on-close
  >
    <div class="editor-body" v-if="initialLoaded">
      <div class="form-row">
        <label class="form-label">{{ $t('manualEditor.form.knowledgeBaseLabel') }}</label>
        <t-select
          v-model="form.kbId"
          :disabled="kbDisabled"
          :loading="kbLoading"
          :options="kbOptions"
          :placeholder="$t('manualEditor.form.knowledgeBasePlaceholder')"
          :popup-props="{ attach: 'body' }"
        >
          <template #empty>
            <div style="padding: 20px; text-align: center; color: #999;">
              {{ $t('manualEditor.noDocumentKnowledgeBases') || '暂无可用的文档型知识库，请先创建一个文档型知识库' }}
            </div>
          </template>
        </t-select>
      </div>

      <div class="form-row">
        <label class="form-label">{{ $t('manualEditor.form.titleLabel') }}</label>
        <t-input
          v-model="form.title"
          maxlength="100"
          :placeholder="$t('manualEditor.form.titlePlaceholder')"
          showLimitNumber
        />
      </div>

      <div class="status-row" v-if="mode === 'edit'">
        <t-tag theme="warning" v-if="form.status === 'draft'">{{ $t('manualEditor.status.draftTag') }}</t-tag>
        <t-tag theme="success" v-else>{{ $t('manualEditor.status.publishedTag') }}</t-tag>
        <span v-if="lastUpdatedText" class="status-timestamp">{{ lastUpdatedText }}</span>
      </div>

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

      <div class="editor-area">
        <div class="editor-pane" v-show="activeTab === 'edit'">
          <t-textarea
            ref="textareaComponent"
            v-if="!contentLoading"
            v-model="form.content"
            :placeholder="$t('manualEditor.form.contentPlaceholder')"
            :autosize="{ minRows: 16, maxRows: 24 }"
          />
          <div v-else class="loading-placeholder">
            <t-loading size="small" :text="$t('manualEditor.loading.content')" />
          </div>
        </div>
        <div class="editor-pane" v-show="activeTab === 'preview'">
          <div class="preview-container" v-html="previewHTML" />
        </div>
      </div>

      <div class="dialog-footer">
        <div class="footer-left">
          <t-button variant="outline" theme="default" @click="handleClose">
            {{ $t('manualEditor.actions.cancel') }}
          </t-button>
        </div>
        <div class="footer-right">
          <t-tooltip :content="viewToggleTooltip" placement="top">
            <t-button
              variant="outline"
              theme="default"
              class="toggle-view-btn"
              :class="{ active: isPreviewMode }"
              @click="toggleEditorView"
            >
              <t-icon :name="viewToggleIcon" size="16px" />
              <span>{{ viewToggleLabel }}</span>
            </t-button>
          </t-tooltip>
          <t-button
            variant="outline"
            theme="default"
            @click="handleSave('draft')"
            :loading="saving && savingAction === 'draft'"
          >
            {{ $t('manualEditor.actions.saveDraft') }}
          </t-button>
          <t-button
            theme="primary"
            @click="handleSave('publish')"
            :loading="saving && savingAction === 'publish'"
          >
            {{ $t('manualEditor.actions.publish') }}
          </t-button>
        </div>
      </div>
    </div>
    <div v-else class="loading-wrapper">
      <t-loading size="medium" :text="$t('manualEditor.loading.preparing')" />
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.manual-knowledge-editor {
  :deep(.t-dialog__body) {
    padding: 20px 24px 12px;
    max-height: 80vh;
    overflow-y: auto;
  }
}

.editor-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-label {
  font-size: 14px;
  font-weight: 500;
  color: #000000e6;
}

.status-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.editor-toolbar {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: #ffffff;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
  overflow-x: auto;
}

.toolbar-group {
  display: flex;
  align-items: center;
  gap: 4px;
}

.toolbar-divider {
  width: 1px;
  height: 24px;
  background: #e0e0e0;
  margin: 0 2px;
}

.toolbar-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  border-radius: 6px;
  color: #666;
  border: none;
  background: transparent;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  align-items: center;
  justify-content: center;
  
  .t-icon {
    color: #666;
    font-size: 16px;
    width: 16px;
    height: 16px;
  }
}

.toolbar-btn:hover {
  background: rgba(7, 192, 95, 0.08);
  color: #07c05f;
  
  .t-icon {
    color: #07c05f;
  }
}

.toolbar-btn.active {
  background: rgba(7, 192, 95, 0.12);
  color: #07c05f;
  
  .t-icon {
    color: #07c05f;
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

:deep(.toggle-view-btn) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 16px;
  height: 32px;
  line-height: 32px;
  transition: all 0.18s ease;
}

:deep(.toggle-view-btn .t-button__content) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

:deep(.toggle-view-btn .t-button__text) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

:deep(.toggle-view-btn .t-icon) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: 16px;
  width: 16px;
  height: 16px;
  vertical-align: middle;
}

:deep(.toggle-view-btn .t-icon svg) {
  display: block;
  width: 16px;
  height: 16px;
  vertical-align: middle;
}

:deep(.toggle-view-btn .t-button__text > span:not(.t-icon)) {
  font-size: 13px;
  line-height: 1.5;
  vertical-align: middle;
}

:deep(.toggle-view-btn.active),
:deep(.toggle-view-btn:hover) {
  background: rgba(7, 192, 95, 0.12) !important;
  color: #059669 !important;
  border-color: rgba(7, 192, 95, 0.4) !important;
  
  .t-icon {
    color: #059669;
  }
}

.status-timestamp {
  font-size: 12px;
  color: #00000066;
}

.editor-area {
  display: flex;
  flex-direction: column;
}

.editor-pane {
  padding: 0;
  overflow: hidden;
  background: #fff;
}

:deep(.t-textarea__inner) {
  font-family: 'JetBrains Mono', 'Fira Code', Consolas, monospace;
  line-height: 1.6;
}

.preview-container {
  min-height: 300px;
  max-height: 520px;
  overflow-y: auto;
  padding: 16px;
  background: #fafafa;
  font-size: 14px;
  line-height: 1.7;
  color: #222;

  :deep(h1),
  :deep(h2),
  :deep(h3),
  :deep(h4) {
    margin-top: 16px;
    margin-bottom: 8px;
  }

  :deep(code) {
    background: rgba(0, 0, 0, 0.05);
    padding: 2px 4px;
    border-radius: 4px;
    font-family: 'JetBrains Mono', 'Fira Code', Consolas, monospace;
  }

  :deep(pre) {
    background: rgba(0, 0, 0, 0.05);
    padding: 12px;
    border-radius: 6px;
    overflow: auto;
  }

  :deep(blockquote) {
    border-left: 4px solid #07c05f;
    padding-left: 12px;
    color: #555;
    margin: 16px 0;
    background: rgba(7, 192, 95, 0.08);
  }

  :deep(a) {
    color: #07c05f;
  }
}

.dialog-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}

.footer-right {
  display: flex;
  gap: 16px;
}

.loading-wrapper,
.loading-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 240px;
}

.empty-preview {
  color: #999;
}
</style>

<style lang="less">
// 全局样式：确保 select 下拉列表在 dialog 之上
.t-popup {
  z-index: 2600 !important;
}
</style>


