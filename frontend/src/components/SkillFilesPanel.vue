<template>
  <div
    class="skill-files-panel"
    :class="{ 'skill-files-panel--splitting': splitting }"
    :style="{ '--skill-files-nav-width': `${navWidth}px` }"
  >
    <aside ref="navEl" class="skill-files-panel__nav">
      <t-loading :loading="listLoading" size="small">
        <p v-if="listError" class="skill-files-panel__hint">{{ listError }}</p>
        <p v-else-if="!listLoading && !visibleRows.length" class="skill-files-panel__hint">
          {{ $t('settings.sandbox.skillFilesEmpty') }}
        </p>
        <ul v-else-if="visibleRows.length" class="skill-files-panel__list">
          <li v-for="row in visibleRows" :key="row.path">
            <button
              type="button"
              class="skill-files-panel__item"
              :class="{
                'is-active': selectedPath === row.path,
                'is-dir': row.isDir,
              }"
              :title="row.path"
              @click="row.isDir ? toggleDir(row.path) : selectFile(row.path)"
            >
              <span class="skill-files-panel__indent" :style="{ width: `${row.depth * 12}px` }" />
              <t-icon
                class="skill-files-panel__icon"
                :name="row.isDir ? (expandedDirs.has(row.path) ? 'folder-open' : 'folder') : fileIcon(row.name)"
                size="16px"
              />
              <span class="skill-files-panel__name">{{ row.name }}</span>
            </button>
          </li>
        </ul>
      </t-loading>
    </aside>

    <section class="skill-files-panel__main">
      <div v-if="selectedPath" class="skill-files-panel__bar">
        <span class="skill-files-panel__path" :title="selectedPath">{{ selectedPath }}</span>
        <button
          v-if="canToggleMarkdown"
          type="button"
          class="skill-files-panel__link"
          @click="markdownMode = markdownMode === 'preview' ? 'source' : 'preview'"
        >
          {{ markdownMode === 'preview' ? $t('settings.sandbox.skillFilesSource') : $t('settings.sandbox.skillFilesPreview') }}
        </button>
        <button
          v-if="canCopy"
          type="button"
          class="skill-files-panel__link"
          @click="copyContent"
        >
          {{ $t('common.copy') }}
        </button>
      </div>

      <div class="skill-files-panel__view">
        <t-loading v-if="fileLoading" size="small" />
        <p v-else-if="fileError" class="skill-files-panel__hint">{{ fileError }}</p>
        <p v-else-if="!selectedPath" class="skill-files-panel__hint">
          {{ $t('settings.sandbox.skillFilesSelectHint') }}
        </p>
        <template v-else>
          <p v-if="file?.truncated" class="skill-files-panel__warn">
            {{ $t('settings.sandbox.skillFilesTruncated') }}
          </p>
          <img
            v-if="imageSrc"
            class="skill-files-panel__image"
            :src="imageSrc"
            :alt="selectedPath"
          />
          <template v-else-if="showMarkdown">
            <dl v-if="frontmatterFields.length" class="skill-files-panel__meta">
              <div
                v-for="field in frontmatterFields"
                :key="field.key"
                class="skill-files-panel__meta-row"
              >
                <dt>{{ field.key }}</dt>
                <dd :class="{ 'is-code': field.code }">{{ field.value }}</dd>
              </div>
            </dl>
            <div
              v-if="markdownHtml"
              ref="markdownEl"
              class="skill-files-panel__markdown markdown-content"
              v-html="markdownHtml"
            />
          </template>
          <pre v-else-if="file?.encoding === 'utf-8'" class="skill-files-panel__code"><code class="hljs" v-html="highlightedHtml" /></pre>
          <p v-else-if="file?.binary" class="skill-files-panel__hint">
            {{ $t('settings.sandbox.skillFilesBinary') }}
          </p>
        </template>
      </div>
    </section>
  </div>
  <teleport to="body">
    <div
      v-if="drawerWidth > 0 && splitLeft > 0"
      class="skill-files-split-handle"
      :class="{ 'skill-files-split-handle--active': splitting }"
      :style="{ left: `${splitLeft}px` }"
      role="separator"
      aria-orientation="vertical"
      @mousedown.prevent="onSplitStart"
    >
      <div class="skill-files-split-handle__line" />
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import hljs from 'highlight.js'
import 'katex/dist/katex.min.css'
import { copyWithToast } from '@/utils/clipboard'
import { getFileIcon } from '@/utils/files'
import { sanitizeMarkdownHTML, safeMarkdownToHTML } from '@/utils/security'
import {
  createChatMarkdownRenderer,
  renderChatMarkdown,
} from '@/utils/chatMarkdownRenderer'
import {
  createMermaidCodeRenderer,
  enhanceMarkdownContainer,
} from '@/utils/mermaidShared'
import {
  getConfigSkillFile,
  listConfigSkillFiles,
  type ConfigSkillFileContent,
  type ConfigSkillFileEntry,
} from '@/api/system'

interface FileNode {
  name: string
  path: string
  isDir: boolean
  children?: FileNode[]
}

interface VisibleRow {
  name: string
  path: string
  isDir: boolean
  depth: number
}

interface FrontmatterField {
  key: string
  value: string
  code: boolean
}

const props = defineProps<{
  configId: string
  skillId: string
  drawerWidth: number
}>()

const { t } = useI18n()

const NAV_WIDTH_KEY = 'skill-files-panel:nav-width'
const DEFAULT_NAV_WIDTH = 208
const MIN_NAV_WIDTH = 156
const MAX_NAV_WIDTH = 360

const navWidth = ref(DEFAULT_NAV_WIDTH)
const splitting = ref(false)
const navEl = ref<HTMLElement | null>(null)
const splitLeft = ref(0)
let splitStartX = 0
let splitStartWidth = 0
let splitObserver: ResizeObserver | null = null

function clampNavWidth(width: number) {
  return Math.max(MIN_NAV_WIDTH, Math.min(MAX_NAV_WIDTH, Math.round(width)))
}

function syncSplitHandle() {
  const el = navEl.value
  if (!el) return
  splitLeft.value = Math.round(el.getBoundingClientRect().right - 12)
}

function loadNavWidth() {
  try {
    const raw = localStorage.getItem(NAV_WIDTH_KEY)
    const parsed = raw ? parseInt(raw, 10) : NaN
    if (!Number.isNaN(parsed)) navWidth.value = clampNavWidth(parsed)
  } catch {
    /* ignore */
  }
}

function persistNavWidth() {
  try {
    localStorage.setItem(NAV_WIDTH_KEY, String(navWidth.value))
  } catch {
    /* ignore */
  }
}

function onSplitStart(e: MouseEvent) {
  splitting.value = true
  splitStartX = e.clientX
  splitStartWidth = navWidth.value
  document.addEventListener('mousemove', onSplitMove)
  document.addEventListener('mouseup', onSplitEnd)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onSplitMove(e: MouseEvent) {
  navWidth.value = clampNavWidth(splitStartWidth + (e.clientX - splitStartX))
  syncSplitHandle()
}

function onSplitEnd() {
  document.removeEventListener('mousemove', onSplitMove)
  document.removeEventListener('mouseup', onSplitEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  splitting.value = false
  persistNavWidth()
}

function cleanupSplit() {
  document.removeEventListener('mousemove', onSplitMove)
  document.removeEventListener('mouseup', onSplitEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  splitting.value = false
}

onMounted(() => {
  loadNavWidth()
  nextTick(() => {
    syncSplitHandle()
    if (navEl.value && typeof ResizeObserver !== 'undefined') {
      splitObserver = new ResizeObserver(() => syncSplitHandle())
      splitObserver.observe(navEl.value)
    }
  })
  window.addEventListener('resize', syncSplitHandle, { passive: true })
})

onUnmounted(() => {
  window.removeEventListener('resize', syncSplitHandle)
  splitObserver?.disconnect()
  splitObserver = null
  cleanupSplit()
})

watch(
  () => [navWidth.value, props.drawerWidth],
  () => {
    nextTick(syncSplitHandle)
  },
)

const listLoading = ref(false)
const fileLoading = ref(false)
const listError = ref('')
const nodes = ref<FileNode[]>([])
const expandedDirs = ref<Set<string>>(new Set())
const selectedPath = ref('')
const file = ref<ConfigSkillFileContent | null>(null)
const fileError = ref('')
const markdownHtml = ref('')
const highlightedHtml = ref('')
const markdownMode = ref<'preview' | 'source'>('preview')
const markdownEl = ref<HTMLElement | null>(null)
const frontmatterFields = ref<FrontmatterField[]>([])
const markdownRenderer = createChatMarkdownRenderer({
  codeRenderer: createMermaidCodeRenderer('skill-files-md'),
})

const langMap: Record<string, string> = {
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  jsx: 'javascript',
  py: 'python',
  rb: 'ruby',
  sh: 'bash',
  bash: 'bash',
  yml: 'yaml',
  yaml: 'yaml',
  md: 'markdown',
  markdown: 'markdown',
  rs: 'rust',
  kt: 'kotlin',
  go: 'go',
  json: 'json',
  xml: 'xml',
  html: 'xml',
  css: 'css',
  sql: 'sql',
  toml: 'ini',
  ini: 'ini',
  conf: 'ini',
}

function fileIcon(name: string): string {
  return getFileIcon(name)
}

function extOf(path: string): string {
  const base = path.split('/').pop() || path
  const idx = base.lastIndexOf('.')
  if (idx < 0) return ''
  return base.slice(idx + 1).toLowerCase()
}

function isMarkdown(path: string): boolean {
  const ext = extOf(path)
  return ext === 'md' || ext === 'markdown'
}

function buildTree(entries: ConfigSkillFileEntry[]): FileNode[] {
  const root: FileNode = { name: '', path: '', isDir: true, children: [] }
  for (const entry of entries) {
    const parts = entry.path.split('/').filter(Boolean)
    let cur = root
    parts.forEach((part, i) => {
      const isLast = i === parts.length - 1
      const nodePath = parts.slice(0, i + 1).join('/')
      if (!cur.children) cur.children = []
      let next = cur.children.find((child) => child.name === part)
      if (!next) {
        next = {
          name: part,
          path: nodePath,
          isDir: !isLast,
          children: isLast ? undefined : [],
        }
        cur.children.push(next)
      } else if (!isLast) {
        next.isDir = true
        if (!next.children) next.children = []
      }
      cur = next
    })
  }
  const sortNodes = (list: FileNode[]) => {
    list.sort((a, b) => {
      if (a.path === 'SKILL.md') return -1
      if (b.path === 'SKILL.md') return 1
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
    list.forEach((node) => node.children && sortNodes(node.children))
  }
  sortNodes(root.children || [])
  return root.children || []
}

function collectDirs(list: FileNode[], out: Set<string>) {
  for (const node of list) {
    if (!node.isDir) continue
    out.add(node.path)
    if (node.children) collectDirs(node.children, out)
  }
}

function flattenVisible(list: FileNode[], depth: number, expanded: Set<string>, out: VisibleRow[]) {
  for (const node of list) {
    out.push({ name: node.name, path: node.path, isDir: node.isDir, depth })
    if (node.isDir && expanded.has(node.path) && node.children?.length) {
      flattenVisible(node.children, depth + 1, expanded, out)
    }
  }
}

function toggleDir(path: string) {
  const next = new Set(expandedDirs.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expandedDirs.value = next
}

const visibleRows = computed(() => {
  const out: VisibleRow[] = []
  flattenVisible(nodes.value, 0, expandedDirs.value, out)
  return out
})
const canToggleMarkdown = computed(() =>
  Boolean(selectedPath.value && isMarkdown(selectedPath.value) && file.value?.encoding === 'utf-8'),
)
const canCopy = computed(() => Boolean(file.value?.content && file.value.encoding === 'utf-8'))
const showMarkdown = computed(() => canToggleMarkdown.value && markdownMode.value === 'preview')
const imageSrc = computed(() => {
  const current = file.value
  if (!current || current.encoding !== 'base64' || !current.content || !current.media_type) return ''
  return `data:${current.media_type};base64,${current.content}`
})

function highlightText(text: string, path: string): string {
  const ext = extOf(path)
  const lang = langMap[ext] || ext
  if (lang && hljs.getLanguage(lang)) {
    try {
      return hljs.highlight(text, { language: lang }).value
    } catch {
      /* fall through */
    }
  }
  try {
    return hljs.highlightAuto(text).value
  } catch {
    return text.replace(/[&<>]/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[ch] || ch))
  }
}

function formatFrontmatterValue(raw: string): { value: string; code: boolean } {
  let value = raw.trim()
  if (
    (value.startsWith('"') && value.endsWith('"') && value.length >= 2) ||
    (value.startsWith("'") && value.endsWith("'") && value.length >= 2)
  ) {
    value = value.slice(1, -1)
  }
  if (
    (value.startsWith('{') && value.endsWith('}')) ||
    (value.startsWith('[') && value.endsWith(']'))
  ) {
    try {
      return { value: JSON.stringify(JSON.parse(value), null, 2), code: true }
    } catch {
      return { value, code: true }
    }
  }
  return { value, code: false }
}

function parseFrontmatterFields(raw: string): FrontmatterField[] {
  const fields: FrontmatterField[] = []
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const idx = trimmed.indexOf(':')
    if (idx <= 0) continue
    const key = trimmed.slice(0, idx).trim()
    if (!/^[A-Za-z0-9_-]+$/.test(key)) continue
    const formatted = formatFrontmatterValue(trimmed.slice(idx + 1))
    fields.push({ key, ...formatted })
  }
  return fields
}

function splitMarkdownFrontmatter(text: string): { fields: FrontmatterField[]; body: string } {
  const src = text.replace(/^\uFEFF/, '')
  const match = src.match(/^(?:[ \t]*\r?\n)*---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*(?:\r?\n|$)/)
  if (!match) return { fields: [], body: src }
  return {
    fields: parseFrontmatterFields(match[1]),
    body: src.slice(match[0].length),
  }
}

function renderMarkdown(text: string) {
  const { fields, body } = splitMarkdownFrontmatter(text)
  frontmatterFields.value = fields
  markdownHtml.value = body.trim()
    ? renderChatMarkdown(body, {
        renderer: markdownRenderer,
        escapeMarkdown: safeMarkdownToHTML,
        sanitizeHtml: sanitizeMarkdownHTML,
      })
    : ''
}

async function enhancePreview() {
  if (!showMarkdown.value || fileLoading.value || !markdownHtml.value) return
  await nextTick()
  await enhanceMarkdownContainer(markdownEl.value)
}

async function selectFile(path: string) {
  if (!props.configId || !props.skillId) return
  selectedPath.value = path
  fileLoading.value = true
  fileError.value = ''
  file.value = null
  markdownHtml.value = ''
  highlightedHtml.value = ''
  frontmatterFields.value = []
  markdownMode.value = 'preview'
  try {
    const res = await getConfigSkillFile(props.configId, props.skillId, path)
    const data = res?.data
    file.value = data || null
    if (!data) {
      fileError.value = t('settings.sandbox.skillFilesFileLoadFailed')
      return
    }
    if (data.encoding === 'utf-8' && data.content != null) {
      highlightedHtml.value = highlightText(data.content, path)
      if (isMarkdown(path)) renderMarkdown(data.content)
    }
  } catch (e: any) {
    fileError.value = e?.message || t('settings.sandbox.skillFilesFileLoadFailed')
  } finally {
    fileLoading.value = false
  }
}

async function copyContent() {
  const text = file.value?.content
  if (!text) return
  await copyWithToast(text, 'common.copied')
}

async function loadFiles() {
  if (!props.configId || !props.skillId) return
  listLoading.value = true
  listError.value = ''
  nodes.value = []
  selectedPath.value = ''
  file.value = null
  fileError.value = ''
  try {
    const res = await listConfigSkillFiles(props.configId, props.skillId)
    const list = res?.data || []
    const tree = buildTree(list)
    nodes.value = tree
    const dirs = new Set<string>()
    collectDirs(tree, dirs)
    expandedDirs.value = dirs
    const initial = list.some((item) => item.path === 'SKILL.md') ? 'SKILL.md' : list[0]?.path
    if (initial) await selectFile(initial)
  } catch (e: any) {
    listError.value = e?.message || t('settings.sandbox.skillFilesLoadFailed')
  } finally {
    listLoading.value = false
  }
}

watch(
  () => [props.configId, props.skillId],
  () => {
    void loadFiles()
  },
  { immediate: true },
)

watch(
  () => [markdownHtml.value, markdownMode.value, fileLoading.value],
  () => {
    void enhancePreview()
  },
)
</script>

<style lang="less" scoped>
@import '@/components/css/chat-markdown.less';

.skill-files-panel {
  position: relative;
  display: flex;
  min-height: 0;
  height: 100%;
  flex: 1;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.skill-files-panel__nav {
  width: var(--skill-files-nav-width, 208px);
  flex-shrink: 0;
  min-width: 0;
  min-height: 0;
  height: 100%;
  border-right: 1px solid var(--td-component-stroke);
  overflow: auto;
  padding: 8px 8px 12px;
  background: var(--td-bg-color-settings-modal);

  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-thumb {
    background: var(--td-bg-color-component-disabled);
    border-radius: 3px;
  }
}

.skill-files-panel__list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.skill-files-panel__item {
  display: flex;
  align-items: center;
  width: 100%;
  margin-bottom: 2px;
  padding: 6px 12px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  font-size: 13px;
  color: var(--td-text-color-primary);
  cursor: pointer;
  text-align: left;
  user-select: none;
  transition: background-color 0.2s ease, color 0.2s ease;

  &:hover {
    background-color: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    font-weight: 500;

    &:hover {
      background-color: var(--td-bg-color-secondarycontainer);
      color: var(--td-brand-color);
    }
  }
}

.skill-files-panel__indent {
  flex-shrink: 0;
  height: 1px;
}

.skill-files-panel__icon {
  flex-shrink: 0;
  margin-right: 9px;
  color: inherit;
}

.skill-files-panel__name {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: inherit;
  line-height: 1.4;
  color: inherit;
}

.skill-files-panel__main {
  min-width: 0;
  min-height: 0;
  height: 100%;
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.skill-files-panel__bar {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  min-height: 40px;
  padding: 0 12px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.skill-files-panel__path {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.skill-files-panel__link {
  flex-shrink: 0;
  height: 24px;
  padding: 0 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  font-size: 12px;
  line-height: 24px;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.skill-files-panel__view {
  min-height: 0;
  flex: 1;
  overflow: auto;
  overscroll-behavior: contain;
  padding: 16px 20px 32px;

  &::-webkit-scrollbar {
    width: 8px;
    height: 8px;
  }

  &::-webkit-scrollbar-thumb {
    background: var(--td-bg-color-component-disabled);
    border-radius: 4px;
  }
}

.skill-files-panel__hint {
  margin: 24px 8px;
  text-align: center;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.skill-files-panel__warn {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--td-warning-color);
}

.skill-files-panel__meta {
  margin: 0 0 20px;
  padding: 8px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.skill-files-panel__meta-row {
  display: grid;
  grid-template-columns: 108px minmax(0, 1fr);
  gap: 4px 12px;
  padding: 6px 0;

  + .skill-files-panel__meta-row {
    border-top: 1px solid var(--td-component-stroke);
  }

  dt {
    margin: 0;
    font-size: 12px;
    font-weight: 500;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }

  dd {
    margin: 0;
    min-width: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-primary);
    word-break: break-word;
    white-space: pre-wrap;

    &.is-code {
      font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
      font-size: 11px;
    }
  }
}

.skill-files-panel__image {
  max-width: 100%;
  height: auto;
}

.skill-files-panel__code {
  margin: 0;
  padding: 0;
  background: transparent;
  overflow: visible;
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.skill-files-panel__markdown {
  min-width: 0;
  .chat-markdown-typography();
}
</style>

<style lang="less">
.skill-files-split-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  z-index: 2602;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: col-resize;
}

.skill-files-split-handle__line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  pointer-events: none;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.skill-files-split-handle:hover .skill-files-split-handle__line,
.skill-files-split-handle--active .skill-files-split-handle__line {
  opacity: 1;
  background: var(--td-brand-color);
}
</style>
