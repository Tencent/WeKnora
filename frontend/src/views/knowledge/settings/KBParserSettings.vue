<template>
  <div class="kb-parser-settings" :class="{ 'kb-parser-settings--embedded': embedded }">
    <div v-if="!embedded" class="section-header">
      <h2>{{ $t('kbSettings.parser.title') }}</h2>
      <p class="section-description">{{ $t('kbSettings.parser.description') }}</p>
    </div>

    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('kbSettings.parser.loading') }}</span>
    </div>

    <div v-else-if="fileTypeGroups.length === 0" class="empty-hint">
      <p>{{ $t('kbSettings.parser.noEngineAvailable') }}</p>
    </div>

    <div v-else class="settings-group" :class="{ 'settings-group--embedded': embedded }">
      <div
        v-for="group in fileTypeGroups"
        :key="group.key"
        class="setting-row"
      >
        <div class="setting-info">
          <label class="group-label">
            <t-icon v-if="!embedded" :name="group.icon" class="group-icon" />
            {{ group.label }}
          </label>
          <div class="ext-tags">
            <span v-for="ext in group.extensions" :key="ext" class="ext-tag">.{{ ext }}</span>
          </div>
        </div>
        <div class="setting-control">
          <t-select
            :value="getEngineForGroup(group.extensions) || undefined"
            @change="(val: string) => handleEngineChange(group.extensions, val)"
            :style="embedded ? undefined : { width: '280px' }"
            :class="{ 'parser-engine-select--embedded': embedded }"
            :status="hasAvailableEngine(group.extensions) ? 'default' : 'warning'"
            :placeholder="$t('kbSettings.parser.noEngine')"
            :popup-props="{ overlayInnerStyle: { maxHeight: '240px' } }"
          >
            <t-option
              v-for="opt in getEngineOptions(group.extensions)"
              :key="opt.value"
              :value="opt.value"
              :label="opt.selectLabel"
            />
          </t-select>
          <div v-if="!hasAvailableEngine(group.extensions)" class="no-engine-warning">
            <a class="go-settings" @click.prevent="goToParserSettings">{{ $t('kbSettings.parser.goConfig') }}</a>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { type ParserEngineInfo } from '@/api/system'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { useUIStore } from '@/stores/ui'
import { storeToRefs } from 'pinia'

const { t } = useI18n()
const editorResources = useEditorResourcesStore()

function getEngineDisplayName(engineName: string): string {
  const key = `kbSettings.parser.engines.${engineName}.name`
  const translated = t(key)
  return translated !== key ? translated : engineName
}

export interface ParserEngineRule {
  file_types: string[]
  engine: string
}

interface EngineOption {
  value: string
  selectLabel: string
  isDefault: boolean
}

function buildOptionLabel(name: string, isDefault: boolean): string {
  const label = getEngineDisplayName(name)
  return isDefault ? `${label} (${t('kbSettings.parser.default')})` : label
}

interface Props {
  parserEngineRules?: ParserEngineRule[]
  /** Compact layout for upload-confirm dialog */
  embedded?: boolean
  /** When set, only show file-type groups matching these extensions */
  relevantExtensions?: string[]
}

const props = withDefaults(defineProps<Props>(), {
  parserEngineRules: () => [],
  embedded: false,
  relevantExtensions: () => [],
})

const emit = defineEmits<{
  'update:parserEngineRules': [value: ParserEngineRule[]]
}>()

const uiStore = useUIStore()
const localEngineRules = ref<ParserEngineRule[]>([...props.parserEngineRules])
const parserEngines = ref<ParserEngineInfo[]>([])
const loading = ref(true)

const allFileTypes = computed(() => {
  const s = new Set<string>()
  for (const engine of parserEngines.value) {
    for (const ft of engine.FileTypes || []) {
      s.add(ft)
    }
  }
  return s
})

const fileTypeGroups = computed(() => {
  const ft = allFileTypes.value
  const groups: { key: string; label: string; icon: string; extensions: string[] }[] = []

  const pdfExts = ['pdf'].filter(e => ft.has(e))
  const officeExts = ['docx', 'doc'].filter(e => ft.has(e))
  const pptExts = ['pptx', 'ppt'].filter(e => ft.has(e))
  const excelExts = ['xlsx', 'xls'].filter(e => ft.has(e))
  const ebookExts = ['epub'].filter(e => ft.has(e))
  const webArchiveExts = ['mhtml'].filter(e => ft.has(e))
  const csvExts = ['csv'].filter(e => ft.has(e))
  const mdExts = ['md', 'markdown'].filter(e => ft.has(e))
  const txtExts = ['txt'].filter(e => ft.has(e))
  const jsonExts = ['json'].filter(e => ft.has(e))
  const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'tiff', 'webp'].filter(e => ft.has(e))
  const audioExts = ['mp3', 'wav', 'm4a', 'flac', 'ogg'].filter(e => ft.has(e))
  const audiovisualExts = [...audioExts]

  if (pdfExts.length) groups.push({ key: 'pdf', label: t('kbSettings.parser.fileTypePdf'), icon: 'file-pdf', extensions: pdfExts })
  if (officeExts.length) groups.push({ key: 'office', label: t('kbSettings.parser.fileTypeWord'), icon: 'file-word', extensions: officeExts })
  if (pptExts.length) groups.push({ key: 'ppt', label: t('kbSettings.parser.fileTypePpt'), icon: 'file-powerpoint', extensions: pptExts })
  if (excelExts.length) groups.push({ key: 'excel', label: t('kbSettings.parser.fileTypeExcel'), icon: 'file-excel', extensions: excelExts })
  if (ebookExts.length) groups.push({ key: 'ebook', label: t('kbSettings.parser.fileTypeEbook'), icon: 'file', extensions: ebookExts })
  if (webArchiveExts.length) groups.push({ key: 'webarchive', label: t('kbSettings.parser.fileTypeWebArchive'), icon: 'file', extensions: webArchiveExts })
  if (csvExts.length) groups.push({ key: 'csv', label: t('kbSettings.parser.fileTypeCsv'), icon: 'file-excel', extensions: csvExts })
  if (mdExts.length) groups.push({ key: 'markdown', label: 'Markdown', icon: 'file-code', extensions: mdExts })
  if (txtExts.length) groups.push({ key: 'text', label: t('kbSettings.parser.fileTypeText'), icon: 'file', extensions: txtExts })
  if (jsonExts.length) groups.push({ key: 'json', label: t('kbSettings.parser.fileTypeJson'), icon: 'file-code', extensions: jsonExts })
  if (imageExts.length) groups.push({ key: 'image', label: t('kbSettings.parser.fileTypeImage'), icon: 'image', extensions: imageExts })
  if (audiovisualExts.length) {
    groups.push({
      key: 'audiovisual',
      label: t('kbSettings.parser.fileTypeAudiovisual'),
      icon: 'sound',
      extensions: audiovisualExts,
    })
  }

  // Keep the UI driven by the backend registry. New parser plugins can expose
  // file types without requiring another frontend release; known families get
  // friendly labels above and everything else gets a compact dynamic row.
  const grouped = new Set(groups.flatMap(group => group.extensions))
  for (const ext of [...ft].filter(ext => !grouped.has(ext) && ext !== 'url').sort()) {
    groups.push({ key: `dynamic-${ext}`, label: ext.toUpperCase(), icon: 'file-code', extensions: [ext] })
  }

  const rel = props.relevantExtensions
  if (!rel?.length) return groups
  const relSet = new Set(rel)
  const filtered = groups.filter(g => g.extensions.some(e => relSet.has(e)))
  return filtered.length > 0 ? filtered : groups
})

function getEngineOptions(extensions: string[]): EngineOption[] {
  const raw: { name: string; desc: string; fileTypes: string[]; available: boolean; reason: string }[] = []
  for (const engine of parserEngines.value) {
    const supports = extensions.some(ext => (engine.FileTypes || []).includes(ext))
    if (supports) {
      raw.push({
        name: engine.Name,
        desc: engine.Description || engine.Name,
        fileTypes: engine.FileTypes || [],
        available: engine.Available !== false,
        reason: engine.UnavailableReason || '',
      })
    }
  }
  const defaultName = raw.find(e => e.available)?.name ?? ''
  return raw
    .filter(e => e.available)
    .map(e => ({
      value: e.name,
      selectLabel: buildOptionLabel(e.name, defaultName !== '' && e.name === defaultName),
      isDefault: defaultName !== '' && e.name === defaultName,
    }))
}

function hasAvailableEngine(extensions: string[]): boolean {
  return getEngineOptions(extensions).length > 0
}

function getDefaultEngine(extensions: string[]): string {
  const opts = getEngineOptions(extensions)
  return opts.find(o => o.isDefault)?.value ?? ''
}

function getEngineForGroup(extensions: string[]): string {
  for (const rule of localEngineRules.value) {
    if (rule.file_types.some(ft => extensions.includes(ft))) {
      return rule.engine
    }
  }
  return getDefaultEngine(extensions)
}

function handleEngineChange(extensions: string[], engine: string) {
  const otherRules = localEngineRules.value.filter(
    r => !r.file_types.some(ft => extensions.includes(ft))
  )
  if (engine) {
    otherRules.push({ file_types: [...extensions], engine })
  }
  localEngineRules.value = otherRules
  emit('update:parserEngineRules', buildCompleteRules())
}

function buildCompleteRules(): ParserEngineRule[] {
  const rules: ParserEngineRule[] = []
  for (const group of fileTypeGroups.value) {
    const engine = getEngineForGroup(group.extensions)
    if (engine) {
      rules.push({ file_types: [...group.extensions], engine })
    }
  }
  return rules
}

function goToParserSettings() {
  uiStore.openSettings('parser')
}

async function loadEngines(force = false) {
  loading.value = true
  try {
    await editorResources.ensureParserEngines(force)
    parserEngines.value = editorResources.parserEngines as ParserEngineInfo[]
  } catch {
    parserEngines.value = []
  } finally {
    loading.value = false
    ensureCompleteRules()
  }
}

function ensureCompleteRules() {
  if (!parserEngines.value.length) return
  const complete = buildCompleteRules()
  if (complete.length && complete.length > localEngineRules.value.length) {
    localEngineRules.value = complete
    emit('update:parserEngineRules', complete)
  }
}

onMounted(loadEngines)

const { showSettingsModal } = storeToRefs(uiStore)
watch(showSettingsModal, (open, wasOpen) => {
  if (wasOpen && !open) {
    loadEngines(true)
  }
})

watch(() => props.parserEngineRules, (v) => {
  localEngineRules.value = v?.length ? [...v] : []
}, { deep: true })
</script>

<style lang="less" scoped>
.kb-parser-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 20px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 6px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.loading-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 0;
}

.empty-hint {
  padding: 24px 0;
  color: var(--td-text-color-secondary);
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 0 0 40%;
  max-width: 40%;
  padding-right: 24px;

  .group-label {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .group-icon {
    font-size: 18px;
    color: var(--td-text-color-secondary);
    flex-shrink: 0;
  }

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .ext-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 6px;
  }

  .ext-tag {
    display: inline-block;
    font-size: 12px;
    line-height: 1;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    padding: 3px 8px;
    border-radius: 4px;
    font-family: var(--app-font-family-mono);
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex: 0 0 55%;
  max-width: 55%;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.no-engine-warning {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--td-warning-color);
  line-height: 1.4;

  .go-settings {
    color: var(--td-brand-color);
    cursor: pointer;
    white-space: nowrap;
    text-decoration: none;

    &:hover {
      text-decoration: underline;
    }
  }
}

// ---- 下拉选项样式 ----
.kb-parser-settings--embedded {
  .settings-group {
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer, #f8f9fb);
    overflow: hidden;
  }

  .setting-row {
    flex-direction: row;
    align-items: center;
    gap: 16px;
    padding: 10px 14px;
    background: var(--td-bg-color-container, #fff);
    border-bottom: 1px solid var(--td-component-stroke);

    &:last-child {
      border-bottom: none;
    }
  }

  .setting-info {
    flex: 0 0 168px;
    max-width: 168px;
    padding-right: 0;
    display: block;
  }

  .setting-control {
    flex: 1;
    min-width: 0;
    max-width: none;
    align-items: stretch;
  }

  .group-label {
    font-size: 13px;
    font-weight: 500;
    margin-bottom: 4px;
  }

  .ext-tags {
    margin-top: 0;
    gap: 4px;
  }

  .ext-tag {
    font-size: 11px;
    padding: 2px 6px;
  }

  .parser-engine-select--embedded {
    width: 100%;
  }
}
</style>
