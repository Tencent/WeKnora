<template>
  <div class="kb-parser-settings">
    <div class="section-header">
      <h2>解析引擎</h2>
      <p class="section-description">为不同文件类型选择文档解析引擎。未配置的文件类型将使用内置解析引擎。</p>
    </div>

    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>加载中...</span>
    </div>

    <div v-else-if="fileTypeGroups.length === 0" class="empty-hint">
      <p>暂无可用解析引擎，或文档解析服务未配置。</p>
    </div>

    <div v-else class="settings-group">
      <div
        v-for="group in fileTypeGroups"
        :key="group.key"
        class="setting-row"
      >
        <div class="setting-info">
          <label class="group-label">
            <t-icon :name="group.icon" class="group-icon" />
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
            style="width: 280px;"
            :status="hasAvailableEngine(group.extensions) ? 'default' : 'warning'"
            placeholder="无可用引擎"
          >
            <t-option
              v-for="opt in getEngineOptions(group.extensions)"
              :key="opt.value"
              :value="opt.value"
              :label="opt.selectLabel"
              :disabled="opt.disabled"
            >
              <t-tooltip
                :content="`支持格式: ${opt.fileTypes.map(t => '.' + t).join('  ')}`"
                placement="left"
                :show-arrow="false"
              >
                <div class="engine-option">
                  <div class="engine-option-top">
                    <span class="engine-option-name">{{ opt.value }}</span>
                    <t-tag
                      v-if="opt.isDefault"
                      theme="primary"
                      variant="light"
                      size="small"
                    >默认</t-tag>
                    <t-tag
                      v-if="opt.disabled"
                      theme="danger"
                      variant="light"
                      size="small"
                    >不可用</t-tag>
                  </div>
                  <div class="engine-option-desc">{{ opt.desc }}</div>
                  <div v-if="opt.disabled && opt.reason" class="engine-option-reason">
                    {{ opt.reason }}
                    <a class="go-settings" @click.stop.prevent="goToParserSettings">去设置 →</a>
                  </div>
                </div>
              </t-tooltip>
            </t-option>
          </t-select>
          <div v-if="!hasAvailableEngine(group.extensions)" class="no-engine-warning">
            <a class="go-settings" @click.prevent="goToParserSettings">前往配置 →</a>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed, onMounted, onUnmounted } from 'vue'
import { getParserEngines, type ParserEngineInfo } from '@/api/system'
import { useUIStore } from '@/stores/ui'
import { storeToRefs } from 'pinia'

export interface ParserEngineRule {
  file_types: string[]
  engine: string
}

interface EngineOption {
  value: string
  selectLabel: string
  desc: string
  fileTypes: string[]
  disabled: boolean
  isDefault: boolean
  reason?: string
}

interface Props {
  parserEngineRules?: ParserEngineRule[]
}

const props = withDefaults(defineProps<Props>(), {
  parserEngineRules: () => []
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
  const csvExts = ['csv'].filter(e => ft.has(e))
  const mdExts = ['md', 'markdown'].filter(e => ft.has(e))
  const txtExts = ['txt'].filter(e => ft.has(e))
  const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'tiff', 'webp'].filter(e => ft.has(e))

  if (pdfExts.length) groups.push({ key: 'pdf', label: 'PDF 文档', icon: 'file-pdf', extensions: pdfExts })
  if (officeExts.length) groups.push({ key: 'office', label: 'Word 文档', icon: 'file-word', extensions: officeExts })
  if (pptExts.length) groups.push({ key: 'ppt', label: '演示文稿', icon: 'file-powerpoint', extensions: pptExts })
  if (excelExts.length) groups.push({ key: 'excel', label: 'Excel 表格', icon: 'file-excel', extensions: excelExts })
  if (csvExts.length) groups.push({ key: 'csv', label: 'CSV 文件', icon: 'file-excel', extensions: csvExts })
  if (mdExts.length) groups.push({ key: 'markdown', label: 'Markdown', icon: 'file-code', extensions: mdExts })
  if (txtExts.length) groups.push({ key: 'text', label: '纯文本', icon: 'file', extensions: txtExts })
  if (imageExts.length) groups.push({ key: 'image', label: '图片', icon: 'image', extensions: imageExts })

  return groups
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
  return raw.map(e => ({
    value: e.name,
    selectLabel: `${e.name}  —  ${e.desc}`,
    desc: e.desc,
    fileTypes: e.fileTypes,
    disabled: !e.available,
    isDefault: defaultName !== '' && e.name === defaultName,
    reason: e.reason,
  }))
}

function hasAvailableEngine(extensions: string[]): boolean {
  return getEngineOptions(extensions).some(opt => !opt.disabled)
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

async function loadEngines() {
  loading.value = true
  try {
    const resp = await getParserEngines()
    if (resp?.data && Array.isArray(resp.data)) {
      parserEngines.value = resp.data
    }
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
    loadEngines()
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
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #333;
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: #666;
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
  color: #666;
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
  padding: 20px 0;
  border-bottom: 1px solid #e5e7eb;

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  .group-label {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .group-icon {
    font-size: 18px;
    color: #555;
    flex-shrink: 0;
  }

  label {
    font-size: 15px;
    font-weight: 500;
    color: #333;
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
    color: #555;
    background: #f3f4f6;
    padding: 3px 8px;
    border-radius: 4px;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }

  .desc {
    font-size: 13px;
    color: #666;
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
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
  color: #e37318;
  line-height: 1.4;

  .go-settings {
    color: #07C05F;
    cursor: pointer;
    white-space: nowrap;
    text-decoration: none;

    &:hover {
      text-decoration: underline;
    }
  }
}

// ---- 下拉选项样式 ----
.engine-option {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 3px 0;
}

.engine-option-top {
  display: flex;
  align-items: center;
  gap: 6px;
}

.engine-option-name {
  font-size: 13px;
  font-weight: 600;
  color: #333;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.engine-option-desc {
  font-size: 12px;
  color: #888;
  line-height: 1.4;
}

.engine-option-reason {
  font-size: 12px;
  color: #e34d59;
  line-height: 1.4;

  .go-settings {
    color: #07C05F;
    cursor: pointer;
    margin-left: 4px;
    font-size: 12px;
    text-decoration: none;

    &:hover {
      text-decoration: underline;
    }
  }
}
</style>

<style lang="less">
.t-select__dropdown .t-select-option {
  height: auto;
  align-items: flex-start;
  padding-top: 6px;
  padding-bottom: 6px;
}
.t-select__dropdown .t-select-option__content {
  white-space: normal;
}
</style>
