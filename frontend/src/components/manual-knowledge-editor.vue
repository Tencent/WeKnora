<script setup lang="ts">
import { ref, reactive, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { marked } from 'marked'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
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
import { sanitizeHTML, safeMarkdownToHTML, hydrateProtectedFileImages } from '@/utils/security'
import { continueListOnEnter, countContent, indentOnTab } from '@/utils/markdownEditing'
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
  tags?: Array<{ id: string }>
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
const currentKnowledgeId = ref<string | null>(null)
const manualTagIds = ref<string[]>([])

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
const lastUpdatedAt = ref<string>('')

// ---------- view mode ----------
type ViewMode = 'edit' | 'split' | 'preview'
const VIEW_MODE_STORAGE_KEY = 'manual-editor:view-mode'
/** Below this the two panes would each be too narrow to write or read in. */
const SPLIT_MIN_WIDTH = 820

const readStoredViewMode = (): ViewMode => {
  try {
    const stored = window.localStorage.getItem(VIEW_MODE_STORAGE_KEY)
    if (stored === 'edit' || stored === 'split' || stored === 'preview') return stored
  } catch {
    // localStorage can throw in private mode / quota errors.
  }
  return 'split'
}

const viewMode = ref<ViewMode>(readStoredViewMode())
const editorAreaRef = ref<HTMLElement | null>(null)
const editorAreaWidth = ref(SPLIT_MIN_WIDTH)
const canSplit = computed(() => editorAreaWidth.value >= SPLIT_MIN_WIDTH)
/**
 * Narrow-layout switch, driven from the same measurement rather than a
 * `@container` query: this project's scoped-style pipeline drops `@container`
 * blocks, so those rules never reach the page.
 */
const NARROW_WIDTH = 620
const isNarrow = computed(() => editorAreaWidth.value > 0 && editorAreaWidth.value < NARROW_WIDTH)
/** Split silently falls back to edit-only while the drawer is too narrow for it. */
const activeView = computed<ViewMode>(() =>
  viewMode.value === 'split' && !canSplit.value ? 'edit' : viewMode.value,
)
const showEditor = computed(() => activeView.value !== 'preview')
const showPreview = computed(() => activeView.value !== 'edit')

const setViewMode = (mode: ViewMode) => {
  if (mode === 'split' && !canSplit.value) return
  viewMode.value = mode
  try {
    window.localStorage.setItem(VIEW_MODE_STORAGE_KEY, mode)
  } catch {
    // Ignore: remembering the layout is a convenience, not a requirement.
  }
}

let editorAreaObserver: ResizeObserver | null = null

watch(editorAreaRef, (el) => {
  editorAreaObserver?.disconnect()
  editorAreaObserver = null
  if (!el) return
  // Measure straight away: waiting for the observer's first callback would
  // paint a split editor for a frame before collapsing it on a narrow drawer.
  editorAreaWidth.value = el.clientWidth
  if (typeof ResizeObserver === 'undefined') return
  editorAreaObserver = new ResizeObserver((entries) => {
    editorAreaWidth.value = entries[0]?.contentRect.width ?? 0
  })
  editorAreaObserver.observe(el)
})

// ---------- textarea selection ----------
const textareaEl = ref<HTMLTextAreaElement | null>(null)
const selectionRange = reactive({ start: 0, end: 0 })

const syncSelection = () => {
  const textarea = textareaEl.value
  if (!textarea) return
  selectionRange.start = textarea.selectionStart ?? 0
  selectionRange.end = textarea.selectionEnd ?? 0
}

const setSelectionRange = (start: number, end: number) => {
  selectionRange.start = start
  selectionRange.end = end
  nextTick(() => {
    const textarea = textareaEl.value
    if (!textarea || !showEditor.value) {
      return
    }
    // Initialization can finish while the drawer is still sliding in. A plain
    // focus() makes the browser scroll the transformed textarea into view,
    // which intermittently shifts the drawer away from the right edge for a
    // frame. Keep keyboard focus without letting it move the viewport.
    textarea.focus({ preventScroll: true })
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
/**
 * A group of related actions behind one labelled trigger. Headings and the
 * block inserts are one-per-document choices, so they cost more toolbar room
 * than they earn as separate icons.
 */
type ToolbarMenu = {
  key: string
  label: string
  options: Array<{ content: string; value: string }>
  actions: Record<string, ToolbarAction>
}
type ToolbarGroup = {
  key: string
  buttons: ToolbarButton[]
  menu?: ToolbarMenu
}

const toolbarGroups = computed<ToolbarGroup[]>(() => {
  const headingMenu: ToolbarMenu = {
    key: 'heading',
    label: t('manualEditor.toolbar.headingGroup'),
    options: [
      { content: t('manualEditor.toolbar.heading1'), value: 'h1' },
      { content: t('manualEditor.toolbar.heading2'), value: 'h2' },
      { content: t('manualEditor.toolbar.heading3'), value: 'h3' },
    ],
    actions: {
      h1: () => applyHeading(1),
      h2: () => applyHeading(2),
      h3: () => applyHeading(3),
    },
  }

  const insertMenu: ToolbarMenu = {
    key: 'insert',
    label: t('manualEditor.toolbar.insertGroup'),
    options: [
      { content: t('manualEditor.toolbar.blockquote'), value: 'quote' },
      { content: t('manualEditor.toolbar.codeBlock'), value: 'codeblock' },
      { content: t('manualEditor.toolbar.table'), value: 'table' },
      { content: t('manualEditor.toolbar.image'), value: 'image' },
      { content: t('manualEditor.toolbar.horizontalRule'), value: 'hr' },
    ],
    actions: {
      quote: applyBlockquote,
      codeblock: insertCodeBlock,
      table: insertTable,
      image: insertImage,
      hr: insertHorizontalRule,
    },
  }

  return [
    {
      key: 'format',
      buttons: [
        { key: 'bold', icon: 'textformat-bold', tooltip: t('manualEditor.toolbar.bold'), action: () => wrapSelection('**', '**', t('manualEditor.placeholders.bold')) },
        { key: 'italic', icon: 'textformat-italic', tooltip: t('manualEditor.toolbar.italic'), action: () => wrapSelection('*', '*', t('manualEditor.placeholders.italic')) },
        { key: 'strike', icon: 'textformat-strikethrough', tooltip: t('manualEditor.toolbar.strike'), action: () => wrapSelection('~~', '~~', t('manualEditor.placeholders.strike')) },
        { key: 'inline-code', icon: 'code', tooltip: t('manualEditor.toolbar.inlineCode'), action: () => wrapSelection('`', '`', t('manualEditor.placeholders.inlineCode')) },
      ],
    },
    { key: 'heading', buttons: [], menu: headingMenu },
    {
      key: 'list',
      buttons: [
        { key: 'ul', icon: 'view-list', tooltip: t('manualEditor.toolbar.bulletList'), action: applyBulletList },
        { key: 'ol', icon: 'list-numbered', tooltip: t('manualEditor.toolbar.orderedList'), action: applyOrderedList },
        { key: 'task', icon: 'check-rectangle', tooltip: t('manualEditor.toolbar.taskList'), action: applyTaskList },
      ],
    },
    {
      key: 'insert',
      buttons: [
        { key: 'link', icon: 'link', tooltip: t('manualEditor.toolbar.link'), action: insertLink },
      ],
      menu: insertMenu,
    },
  ]
})

/** TDesign hands the clicked item's `value` (or the item itself in older builds). */
const handleToolbarMenuSelect = (menu: ToolbarMenu, selected: unknown) => {
  const key =
    selected && typeof selected === 'object'
      ? String((selected as { value?: unknown }).value ?? '')
      : String(selected ?? '')
  const action = menu.actions[key]
  if (action) handleToolbarAction(action)
}

const previewRoot = ref<HTMLElement | null>(null)

const viewOptions = computed(() => [
  { value: 'edit' as ViewMode, icon: 'edit-1', label: t('manualEditor.view.edit'), disabled: false, tooltip: t('manualEditor.view.edit') },
  {
    value: 'split' as ViewMode,
    icon: 'column-layout',
    label: t('manualEditor.view.split'),
    disabled: !canSplit.value,
    tooltip: canSplit.value ? t('manualEditor.view.split') : t('manualEditor.view.splitUnavailable'),
  },
  { value: 'preview' as ViewMode, icon: 'browse', label: t('manualEditor.view.preview'), disabled: false, tooltip: t('manualEditor.view.preview') },
])

const handleToolbarAction = (action: ToolbarAction) => {
  if (saving.value) {
    return
  }
  // Formatting acts on the textarea, so bring it back into view first.
  if (!showEditor.value) {
    setViewMode(canSplit.value ? 'split' : 'edit')
    nextTick(action)
  } else {
    action()
  }
}

// ---------- keyboard ----------
const isApplePlatform =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)
const modifierLabel = computed(() => (isApplePlatform ? '⌘' : 'Ctrl+'))

const SHORTCUT_ACTIONS: Record<string, () => void> = {
  b: () => wrapSelection('**', '**', t('manualEditor.placeholders.bold')),
  i: () => wrapSelection('*', '*', t('manualEditor.placeholders.italic')),
  k: insertLink,
}

const applyPatch = (patch: { value: string; start: number; end: number }) => {
  form.content = patch.value
  setSelectionRange(patch.start, patch.end)
}

/** Read the live caret off the event target — it is ahead of our cached range. */
const readEditorState = (event: KeyboardEvent) => {
  const textarea = event.currentTarget instanceof HTMLTextAreaElement ? event.currentTarget : null
  return {
    value: form.content ?? '',
    start: textarea?.selectionStart ?? selectionRange.start,
    end: textarea?.selectionEnd ?? selectionRange.end,
  }
}

/**
 * Shortcuts an author expects from any Markdown editor, plus list/indent
 * behaviour a bare <textarea> does not provide.
 */
const handleEditorKeydown = (event: KeyboardEvent) => {
  // Never steal a key from an IME mid-composition (Chinese/Japanese input).
  if (event.isComposing || event.keyCode === 229) return

  const modifier = isApplePlatform ? event.metaKey : event.ctrlKey
  if (modifier && !event.altKey) {
    const key = event.key.toLowerCase()
    if (key === 'enter') {
      event.preventDefault()
      handleSave('publish')
      return
    }
    if (key === 's') {
      event.preventDefault()
      handleSave('draft')
      return
    }
    if (saving.value) return
    const shortcut = SHORTCUT_ACTIONS[key]
    if (shortcut) {
      event.preventDefault()
      syncSelection()
      shortcut()
      return
    }
  }

  if (saving.value) return
  const state = readEditorState(event)

  if (event.key === 'Enter' && !event.shiftKey && !modifier) {
    const patch = continueListOnEnter(state)
    if (patch) {
      event.preventDefault()
      applyPatch(patch)
    }
    return
  }

  if (event.key === 'Tab') {
    const patch = indentOnTab(state, event.shiftKey)
    if (patch) {
      event.preventDefault()
      applyPatch(patch)
    }
  }
}

const contentStats = computed(() => countContent(form.content))
const counterText = computed(() =>
  t('manualEditor.status.counter', {
    chars: contentStats.value.characters,
    lines: contentStats.value.lines,
  }),
)
/** Rows behind the status bar's keyboard button — a full list, not a cropped line. */
const shortcutRows = computed(() => [
  { keys: `${modifierLabel.value}B`, label: t('manualEditor.toolbar.bold') },
  { keys: `${modifierLabel.value}I`, label: t('manualEditor.toolbar.italic') },
  { keys: `${modifierLabel.value}K`, label: t('manualEditor.toolbar.link') },
  { keys: `${modifierLabel.value}S`, label: t('manualEditor.actions.saveDraft') },
  { keys: `${modifierLabel.value}\u21B5`, label: t('manualEditor.actions.publish') },
  { keys: 'Enter', label: t('manualEditor.shortcuts.continueList') },
  { keys: 'Tab', label: t('manualEditor.shortcuts.indent') },
])

marked.use({})

const previewHTML = computed(() => {
  if (!form.content) {
    return `<p class="empty-preview">${t('manualEditor.preview.empty')}</p>`
  }
  const safeMarkdown = safeMarkdownToHTML(form.content)
  const html = marked.parse(safeMarkdown, { async: false })
  return sanitizeHTML(html)
})

watch([previewHTML, activeView, () => form.kbId], async () => {
  await nextTick()
  if (showPreview.value) await hydrateProtectedFileImages(previewRoot.value,
    mode.value === 'edit' && form.kbId ? { mode: 'knowledgeBase', kbId: form.kbId } : undefined)
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
  if (!currentKnowledgeId.value) {
    return
  }
  contentLoading.value = true
  try {
    const res: any = await getKnowledgeDetails(currentKnowledgeId.value)
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
    manualTagIds.value = (data.tags || []).map(tag => String(tag.id))
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
  currentKnowledgeId.value = knowledgeId.value || null
  form.kbId = uiStore.manualEditorKBId || ''
  form.title = uiStore.manualEditorInitialTitle || ''
  form.content = uiStore.manualEditorInitialContent || ''
  form.status = uiStore.manualEditorInitialStatus || 'draft'
  lastUpdatedAt.value = ''
  initialLoaded.value = false
  manualTagIds.value = mode.value === 'create' ? [...uiStore.selectedTagIds] : []
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
  markPristine()
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
    const payload: {
      title: string
      content: string
      status: string
      tag_ids?: string[]
      process_config?: KnowledgeProcessOverrides
    } = {
      title: form.title.trim(),
      content: form.content,
      status: targetStatus,
    }
    payload.tag_ids = [...manualTagIds.value]

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
            knowledgeId: currentKnowledgeId.value || undefined,
            title: payload.title,
            content: payload.content,
            tagIds: [...manualTagIds.value],
          },
        })
        payload.process_config = confirmResult.processConfig
        manualTagIds.value = [...(confirmResult.tagIds || [])]
        payload.tag_ids = [...manualTagIds.value]
      } catch {
        return
      }
    }

    let response: any
    let knowledgeID = currentKnowledgeId.value
    let kbId = form.kbId

    if (mode.value === 'edit' && currentKnowledgeId.value) {
      response = await updateManualKnowledge(currentKnowledgeId.value, payload)
    } else {
      response = await createManualKnowledge(form.kbId, payload)
      knowledgeID = response?.data?.id || knowledgeID
      currentKnowledgeId.value = knowledgeID || null
      uiStore.manualEditorKnowledgeId = currentKnowledgeId.value
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
      markPristine()
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

// ---------- unsaved-changes guard ----------
const pristineSnapshot = ref('')

const snapshot = () => JSON.stringify({ kbId: form.kbId, title: form.title, content: form.content })
const markPristine = () => {
  pristineSnapshot.value = snapshot()
}
const isDirty = () => initialLoaded.value && snapshot() !== pristineSnapshot.value

const handleClose = () => {
  if (!isDirty()) {
    uiStore.closeManualEditor()
    return
  }
  // An accidental overlay click used to throw the whole draft away.
  const dialog = DialogPlugin.confirm({
    header: t('common.unsavedChanges.title'),
    body: t('common.unsavedChanges.body'),
    theme: 'warning',
    confirmBtn: { content: t('common.unsavedChanges.discard'), theme: 'danger' },
    cancelBtn: t('common.unsavedChanges.keepEditing'),
    onConfirm: () => {
      dialog.destroy()
      uiStore.closeManualEditor()
    },
    onClose: () => dialog.destroy(),
  })
}

watch(visible, async (val) => {
  if (val) {
    await nextTick()
    await initialize()
    await nextTick()
    const length = form.content ? form.content.length : 0
    setSelectionRange(length, length)
  } else {
    resetForm()
  }
})

onBeforeUnmount(() => {
  editorAreaObserver?.disconnect()
  editorAreaObserver = null
})
</script>

<template>
  <SettingDrawer
    class="manual-editor-drawer"
    :visible="visible"
    :title="dialogTitle"
    icon="edit-1"
    width="960px"
    :min-width="600"
    :max-width="1440"
    maximizable
    storage-key="setting-drawer:width:manual-markdown-editor"
    :hide-footer="!initialLoaded"
    @update:visible="(v: boolean) => { visible = v }"
  >
    <template #footer-left>
      <div class="manual-editor-footer-meta">
        <t-tag size="small" theme="warning" variant="light" v-if="form.status === 'draft'">
          {{ $t('manualEditor.status.draftTag') }}
        </t-tag>
        <t-tag size="small" theme="success" variant="light" v-else>
          {{ $t('manualEditor.status.publishedTag') }}
        </t-tag>
        <span class="manual-editor-footer-count">{{ counterText }}</span>
      </div>
    </template>

    <template #footer-right>
      <div class="manual-editor-footer-actions">
        <t-button
          theme="default"
          variant="outline"
          class="manual-editor-cancel-btn"
          :disabled="saving"
          @click="handleClose"
        >
          {{ $t('manualEditor.actions.cancel') }}
        </t-button>
        <t-button
          variant="outline"
          theme="default"
          @click="handleSave('draft')"
          :loading="saving && savingAction === 'draft'"
          :disabled="saving && savingAction !== 'draft'"
        >
          {{ $t('manualEditor.actions.saveDraft') }}
        </t-button>
        <t-button
          theme="primary"
          @click="handleSave('publish')"
          :loading="saving && savingAction === 'publish'"
          :disabled="saving && savingAction !== 'publish'"
        >
          {{ $t('manualEditor.actions.publish') }}
        </t-button>
      </div>
    </template>

    <div class="manual-editor" :class="{ 'manual-editor--narrow': isNarrow }" v-if="initialLoaded">
      <!-- Document head: the title reads as the document's title, not as a form
           field, and the destination is a sentence instead of a labelled row. -->
      <div class="doc-head">
        <t-input
          id="manual-editor-title"
          class="doc-title"
          v-model="form.title"
          maxlength="100"
          borderless
          :placeholder="$t('manualEditor.form.titlePlaceholder')"
          :aria-label="$t('manualEditor.form.titleLabel')"
        />
        <div class="doc-meta">
          <t-tooltip :content="$t('manualEditor.form.knowledgeBaseLabel')" placement="top">
            <t-icon name="folder" class="doc-meta__icon" />
          </t-tooltip>
          <t-select
            class="doc-meta__kb"
            v-model="form.kbId"
            size="small"
            auto-width
            :disabled="kbDisabled"
            :loading="kbLoading"
            :options="kbOptions"
            :placeholder="$t('manualEditor.form.knowledgeBasePlaceholder')"
            :aria-label="$t('manualEditor.form.knowledgeBaseLabel')"
            :popup-props="{ attach: 'body', zIndex: 2600 }"
          >
            <template #empty>
              <div class="kb-empty">{{ $t('manualEditor.noDocumentKnowledgeBases') }}</div>
            </template>
          </t-select>
          <span v-if="lastUpdatedText" class="doc-meta__time">{{ lastUpdatedText }}</span>
        </div>
      </div>

      <div class="editor-area" ref="editorAreaRef" :class="`editor-area--${activeView}`">
        <div class="editor-toolbar">
          <div class="editor-toolbar__format">
            <template v-for="(group, groupIndex) in toolbarGroups" :key="group.key">
              <div class="toolbar-group">
                <template v-for="btn in group.buttons" :key="btn.key">
                  <t-tooltip :content="btn.tooltip" placement="top">
                    <button
                      type="button"
                      class="toolbar-btn"
                      :aria-label="btn.tooltip"
                      @mousedown.prevent
                      @click="handleToolbarAction(btn.action)"
                    >
                      <t-icon :name="btn.icon" size="18px" />
                    </button>
                  </t-tooltip>
                </template>
                <t-dropdown
                  v-if="group.menu"
                  :options="group.menu.options"
                  trigger="click"
                  :min-column-width="132"
                  :popup-props="{ attach: 'body', zIndex: 2600 }"
                  @click="(selected: unknown) => handleToolbarMenuSelect(group.menu!, selected)"
                >
                  <button type="button" class="toolbar-menu-btn" @mousedown.prevent>
                    <span>{{ group.menu.label }}</span>
                    <t-icon name="chevron-down" class="toolbar-menu-btn__caret" />
                  </button>
                </t-dropdown>
              </div>
              <div
                v-if="groupIndex < toolbarGroups.length - 1"
                class="toolbar-divider"
              ></div>
            </template>
          </div>
          <div class="editor-toolbar__view">
            <t-tooltip placement="top-right" :show-arrow="false">
              <template #content>
                <div class="shortcut-sheet">
                  <div class="shortcut-sheet__title">{{ $t('manualEditor.shortcuts.title') }}</div>
                  <div v-for="row in shortcutRows" :key="row.keys" class="shortcut-sheet__row">
                    <kbd>{{ row.keys }}</kbd>
                    <span>{{ row.label }}</span>
                  </div>
                </div>
              </template>
              <button
                type="button"
                class="toolbar-shortcuts"
                :aria-label="$t('manualEditor.shortcuts.title')"
                @mousedown.prevent
              >
                <t-icon name="keyboard" />
              </button>
            </t-tooltip>
            <div class="view-switch" role="group" :aria-label="$t('manualEditor.view.groupLabel')">
              <t-tooltip v-for="option in viewOptions" :key="option.value" :content="option.tooltip" placement="top">
                <!-- aria-disabled, not disabled: a disabled button swallows hover,
                     and the tooltip is the only place that explains why split is off. -->
                <button
                  type="button"
                  class="view-switch__btn"
                  :class="{ 'is-active': activeView === option.value, 'is-disabled': option.disabled }"
                  :aria-disabled="option.disabled"
                  :aria-pressed="activeView === option.value"
                  @mousedown.prevent
                  @click="setViewMode(option.value)"
                >
                  <t-icon :name="option.icon" />
                  <span class="view-switch__label">{{ option.label }}</span>
                </button>
              </t-tooltip>
            </div>
          </div>
        </div>

        <div class="editor-body">
          <div class="editor-pane editor-pane--edit" v-show="showEditor">
            <textarea
              v-if="!contentLoading"
              ref="textareaEl"
              v-model="form.content"
              class="editor-textarea"
              spellcheck="false"
              :placeholder="$t('manualEditor.form.contentPlaceholder')"
              :aria-label="$t('manualEditor.section.content')"
              @keydown="handleEditorKeydown"
              @select="syncSelection"
              @click="syncSelection"
              @keyup="syncSelection"
              @input="syncSelection"
            ></textarea>
            <div v-else class="loading-placeholder">
              <t-loading size="small" :text="$t('manualEditor.loading.content')" />
            </div>
          </div>
          <div class="editor-pane editor-pane--preview" v-show="showPreview">
            <div ref="previewRoot" class="preview-container" v-html="previewHTML" />
          </div>
        </div>

      </div>
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
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.manual-editor-footer-meta {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--td-text-color-placeholder);

  /* 状态标签保持整行，窄抽屉里让后面的时间先省略 */
  :deep(.t-tag) {
    flex-shrink: 0;
    white-space: nowrap;
  }
}

.manual-editor-footer-count {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.manual-editor-footer-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;

  :deep(.t-button) {
    min-width: 88px;
  }
}

.manual-editor-cancel-btn {
  border-color: transparent;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  transition: background var(--app-motion-base) ease, border-color var(--app-motion-base) ease,
    color var(--app-motion-base) ease;

  &:hover {
    border-color: var(--td-component-stroke);
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

/* ---------- 文档头：标题 + 存入位置 ---------- */
.doc-head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 12px;
}

/* 标题按“文档标题”排版，不做成表单控件；hover/聚焦才显出输入框的边界 */
:deep(.doc-title) {
  flex: 1 1 auto;
  min-width: 0;
  padding: 0;
  background: transparent;
  /* 负外边距抵掉内边距：文字和下方编辑卡左对齐，hover 底色仍有呼吸空间 */
  margin: 0 -8px;

  .t-input {
    padding: 4px 8px;
    border-radius: var(--app-radius-sm);
    background: transparent;
    transition: background var(--app-motion-fast) ease;
  }

  .t-input__inner {
    font-size: var(--app-text-xl);
    font-weight: 600;
    line-height: 1.4;
    color: var(--td-text-color-primary);
  }

  &:hover .t-input {
    background: var(--td-bg-color-container-hover);
  }

  .t-input.t-is-focused {
    background: var(--td-bg-color-container-hover);
  }
}

.doc-meta {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.doc-meta__icon {
  flex-shrink: 0;
  font-size: var(--app-text-md);
}

:deep(.doc-meta__kb) {
  /* TDesign 的 select 根节点是块级；这里让它收到内容宽度，
     否则框比文字宽，箭头够不到右边缘，看着就是没对齐 */
  flex: 0 1 auto;
  width: auto;
  min-width: 0;
  max-width: 260px;

  .t-input {
    border-color: transparent;
    background: transparent;
    padding-left: 4px;
    padding-right: 4px;
  }

  .t-input__inner {
    color: var(--td-text-color-secondary);
  }

  &:hover .t-input {
    border-color: var(--td-component-border);
    background: var(--td-bg-color-container);
  }
}

.doc-meta__time {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-empty {
  padding: 20px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}

/* 窄抽屉：与其让工具栏折行（视图切换会悬在第二行），不如把视图切换收成图标，
   一行仍然装得下。文字含义由 tooltip 兜底。 */
.manual-editor--narrow {
  .view-switch__btn {
    padding: 0 6px;
  }

  .view-switch__label {
    display: none;
  }

  /* 极窄时的兜底：真放不下才换行，而不是把按钮藏进隐藏的滚动条里 */
  .editor-toolbar {
    flex-wrap: wrap;
  }

  .editor-toolbar__format {
    flex-wrap: wrap;
    overflow-x: visible;
  }
}

/* ---------- 编辑区 ---------- */
.editor-area {
  flex: 1;
  /* 视口很矮时不再继续压缩，改由抽屉主体滚动 */
  min-height: 260px;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  overflow: hidden;
  background: var(--td-bg-color-container);
  transition: border-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease;

  &:focus-within {
    border-color: var(--td-component-border);
  }
}

.editor-toolbar {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 8px;
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
  overflow: hidden;
  flex-shrink: 0;
}

.editor-toolbar__format {
  min-width: 0;
  display: flex;
  flex: 1;
  align-items: center;
  gap: 6px;
  overflow-x: auto;

  &::-webkit-scrollbar {
    height: 0;
  }
}

.editor-toolbar__view {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: 8px;
  border-left: 1px solid var(--td-component-stroke);
}

/* 编辑 / 分屏 / 预览：一组分段控件，当前视图一眼可见 */
.view-switch {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-component-disabled);
  border: 1px solid var(--td-component-stroke);
}

.view-switch__btn {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 8px;
  border: none;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover:not(.is-disabled) {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background: var(--td-bg-color-container);
    color: var(--td-text-color-primary);
    box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
  }

  &.is-disabled {
    color: var(--td-text-color-disabled);
    cursor: not-allowed;
  }

  &:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 25%, transparent);
  }
}

/* 快捷键速查：和视图切换同处工具栏右端，省掉一整条状态栏 */
.toolbar-shortcuts {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
  cursor: help;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-secondary);
  }

  &:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 25%, transparent);
  }
}

.toolbar-group {
  display: flex;
  align-items: center;
  gap: 2px;
}

.toolbar-menu-btn {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 28px;
  padding: 0 6px 0 8px;
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 25%, transparent);
  }
}

.toolbar-menu-btn__caret {
  font-size: var(--app-text-sm);
  opacity: 0.7;
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
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-secondary);
  border: none;
  background: transparent;
  cursor: pointer;
  transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;
  display: flex;
  align-items: center;
  justify-content: center;

  .t-icon {
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xl);
    width: 16px;
    height: 16px;
  }
}

.toolbar-btn:hover {
  background: var(--td-bg-color-container-hover);
  color: var(--td-text-color-primary);

  .t-icon {
    color: var(--td-text-color-primary);
  }
}

.toolbar-btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 25%, transparent);
}

.toolbar-btn:active {
  background: var(--td-bg-color-container-active);
  transform: translateY(0.5px);
}

.editor-body {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: stretch;
}

.editor-pane {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

/* 分屏时两栏之间给一道分隔线，预览栏底色略沉，区分“源码 / 成稿” */
.editor-area--split {
  .editor-pane--preview {
    border-left: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-secondarycontainer);
  }

  .preview-container {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.editor-textarea {
  flex: 1;
  min-height: 0;
  width: 100%;
  padding: 14px 16px;
  border: none;
  outline: none;
  resize: none;
  box-sizing: border-box;
  font-family: var(--app-font-family-mono);
  font-size: var(--app-text-base);
  line-height: 1.7;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  tab-size: 2;

  &::placeholder {
    color: var(--td-text-color-placeholder);
  }
}

.preview-container {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px;
  background: var(--td-bg-color-container);
  font-size: var(--app-text-base);
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
    border-radius: var(--app-radius-xs);
    font-family: var(--app-font-family-mono);
  }

  :deep(pre) {
    background: var(--td-bg-color-container-hover);
    padding: 12px;
    border-radius: var(--app-radius-sm);
    overflow: auto;
  }

  /* 与 chat-markdown.less 的引用块一致：中性描边、无底色 */
  :deep(blockquote) {
    border-left: 2px solid var(--td-component-stroke);
    padding: 6px 0 6px 14px;
    color: var(--td-text-color-secondary);
    margin: 12px 0;
  }

  :deep(a) {
    color: var(--td-brand-color);
  }

  :deep(img) {
    max-width: 100%;
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

<!--
  Non-scoped: the editor wants the drawer's full height instead of the body's
  natural scroll, so the flex chain has to start at TDesign's own body wrapper.
  Namespaced under .manual-editor-drawer so no other SettingDrawer is affected.
-->
<style lang="less">
/* Tooltip content is teleported out of the component, so its rules cannot be scoped. */
.shortcut-sheet {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 2px 0;
}

.shortcut-sheet__title {
  font-weight: 600;
  margin-bottom: 2px;
}

.shortcut-sheet__row {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;

  kbd {
    min-width: 42px;
    font-family: var(--app-font-family-mono);
    font-size: var(--app-text-xs);
    opacity: 0.9;
  }
}

.manual-editor-drawer {
  /* 没有副标题的抽屉不需要 65px 的头：收一圈内边距、缩小图标徽章，
     把高度还给正文。只作用于本抽屉，不动共用的 SettingDrawer。 */
  .t-drawer__header {
    /* TDesign 给 header 兜了 56px 的 min-height，不解开就只是白留一圈空 */
    min-height: 0;
    padding: 9px 18px;
  }

  .setting-drawer__header {
    padding: 0;
  }

  .setting-drawer__header-icon {
    width: 26px;
    height: 26px;
    border-radius: var(--app-radius-sm);
    font-size: var(--app-text-md);
  }

  .t-drawer__body {
    display: flex;
    flex-direction: column;
    overflow: auto;
  }

  .setting-drawer__body {
    flex: 1;
    min-height: 0;
  }
}
</style>
