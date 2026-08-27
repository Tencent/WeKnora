<template>
  <article class="smart-summary">
    <div v-if="loading" class="smart-summary__state"><t-loading text="正在加载智能总结" /></div>
    <t-alert v-else-if="error" class="smart-summary__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="sections.length === 0" :description="notGenerated ? '智能总结尚未生成' : '暂无智能总结'">
      <template #action><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-empty>
    <div v-else class="smart-summary__document">
      <section v-for="section in sections" :key="section.id" class="smart-summary__section">
        <h2>{{ section.title }}</h2>
        <ul>
          <li
            v-for="(line, lineIndex) in parseContent(section.content)"
            :key="`${section.id}-${lineIndex}`"
            :class="{ 'is-sub-item': line.isSubItem }"
          >
            <EvidencePopover v-if="lineIndex === 0 && hasEvidence(section)" :section="section" @seek="emit('seek', $event)">
              <div class="smart-summary__line" :aria-label="`查看 ${section.title} 的内容出处`">
                <span class="smart-summary__bullet" aria-hidden="true">{{ line.isSubItem ? '◦' : '•' }}</span>
                <p>
                  <strong v-if="line.prefix">{{ line.prefix }}：</strong>
                  <span>{{ line.text }}</span>
                </p>
                <t-icon name="search" size="14px" />
              </div>
            </EvidencePopover>
            <div v-else class="smart-summary__line">
              <span class="smart-summary__bullet" aria-hidden="true">{{ line.isSubItem ? '◦' : '•' }}</span>
              <p>
                <strong v-if="line.prefix">{{ line.prefix }}：</strong>
                <span>{{ line.text }}</span>
              </p>
            </div>
          </li>
        </ul>
      </section>
    </div>
  </article>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { fetchSummary } from '@/api/videohub/summary'
import EvidencePopover from './EvidencePopover.vue'
import type { SummarySection, VideoData } from '@/types/videohub'

interface ContentLine {
  isSubItem: boolean
  prefix: string
  text: string
}

const props = defineProps<{ video: VideoData }>()
const emit = defineEmits<{ seek: [seconds: number] }>()
const sections = ref<SummarySection[]>([])
const loading = ref(true)
const error = ref('')
const notGenerated = ref(false)

async function load() {
  const videoId = props.video.id
  loading.value = true
  error.value = ''
  notGenerated.value = false
  try {
    const response = await fetchSummary(videoId, props.video.category)
    if (props.video.id !== videoId) return
    sections.value = response.sections
  } catch (reason: any) {
    if (props.video.id !== videoId) return
    sections.value = []
    if (reason?.status === 404) notGenerated.value = true
    else error.value = reason?.message || '智能总结加载失败'
  } finally {
    if (props.video.id === videoId) loading.value = false
  }
}

function parseContent(content: string): ContentLine[] {
  return content.split('\n').filter(line => line.trim()).map((line) => {
    const trimmed = line.trim()
    const isSubItem = /^[-•◦]/.test(trimmed) || /^\s{2,}/.test(line)
    const cleanLine = trimmed.replace(/^[-•◦\d.]+\s*/, '')
    const colonIndex = cleanLine.indexOf('：')
    return colonIndex > 0
      ? { isSubItem, prefix: cleanLine.slice(0, colonIndex), text: cleanLine.slice(colonIndex + 1) }
      : { isSubItem, prefix: '', text: cleanLine }
  })
}

function hasEvidence(section: SummarySection) {
  return Boolean(section.evidenceTimestamp && section.transcriptSnippet && section.evidenceSeconds !== undefined)
}
watch(() => [props.video.id, props.video.category] as const, () => void load(), { immediate: true })
</script>

<style scoped>
.smart-summary { min-height: 360px; }
.smart-summary__state, .smart-summary > :deep(.t-empty) { min-height: 360px; display: grid; place-items: center; }
.smart-summary__document { padding: calc(var(--td-comp-margin-s) * 2) calc(var(--td-comp-margin-s) / 2) calc(var(--td-comp-margin-s) * 4); }
.smart-summary__section + .smart-summary__section { margin-top: calc(var(--td-comp-margin-s) * 3.5); }
.smart-summary h2 { display: flex; align-items: baseline; gap: var(--td-comp-margin-s); margin: 0 0 calc(var(--td-comp-margin-s) * 1.5); color: var(--td-text-color-primary); font-size: var(--td-font-size-title-medium); font-weight: 600; line-height: 1.4; }
.smart-summary ul { display: grid; gap: calc(var(--td-comp-margin-s) / 2); margin: 0; padding: 0; list-style: none; }
.smart-summary li { min-width: 0; margin: 0 calc(var(--td-comp-margin-s) * -1); border-radius: var(--td-radius-medium); }
.smart-summary li.is-sub-item .smart-summary__line { padding-left: calc(var(--td-comp-margin-s) * 3); }
.smart-summary__line { display: flex; align-items: flex-start; gap: var(--td-comp-margin-s); min-width: 0; padding: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); border-radius: var(--td-radius-medium); transition: background-color .15s ease; }
.smart-summary__line:hover { background: var(--td-bg-color-secondarycontainer); }
.smart-summary__bullet { flex: none; color: var(--td-text-color-placeholder); font-weight: 600; line-height: 1.7; }
.smart-summary p { flex: 1; min-width: 0; margin: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: 1.7; white-space: pre-wrap; word-break: break-word; }
.smart-summary p strong { color: var(--td-text-color-primary); font-weight: 600; }
.smart-summary__line > :deep(.t-icon) { flex: none; margin-top: calc(var(--td-comp-margin-s) / 2); color: var(--td-brand-color); opacity: 0; transition: opacity .15s ease; }
.smart-summary__line:hover > :deep(.t-icon), .smart-summary__line:focus-within > :deep(.t-icon) { opacity: 1; }
@media (hover: none) { .smart-summary__line > :deep(.t-icon) { opacity: 1; } }
</style>
