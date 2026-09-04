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
            <li v-for="block in section.blocks" :key="block.id" :class="{ 'is-bullet': block.kind === 'bullet' }">
              <EvidencePopover v-slot="{ open, visible }" :evidence="block.evidence" @seek="emit('seek', $event)">
                <div class="smart-summary__line" :aria-label="`查看 ${section.title} 的内容出处`">
                  <span v-if="block.kind === 'bullet'" class="smart-summary__bullet" aria-hidden="true">•</span>
                  <p>{{ block.text }}</p>
                  <button
                    type="button"
                    class="smart-summary__evidence-button"
                    :aria-label="`查看 ${section.title} 的内容出处`"
                    :aria-expanded="visible"
                    @click.stop="open"
                  >
                    <t-icon name="search" size="14px" aria-hidden="true" />
                  </button>
                </div>
              </EvidencePopover>
            </li>
          </ul>
        </section>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import EvidencePopover from './EvidencePopover.vue'
import type { ContentState, SummarySection, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData; contentState: ContentState<SummarySection[]> }>()
const emit = defineEmits<{ seek: [seconds: number]; reload: [] }>()
const sections = computed(() => props.contentState.data)
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '智能总结加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
function load() { emit('reload') }

</script>

<style scoped>
.smart-summary { min-height: 360px; background: transparent; }
.smart-summary__state, .smart-summary > :deep(.t-empty) { min-height: 360px; display: grid; place-items: center; }
.smart-summary__document { padding: 16px 8px 32px; background: transparent; }
.smart-summary__section + .smart-summary__section { margin-top: calc(var(--td-comp-margin-s) * 3.5); }
.smart-summary h2 { display: flex; align-items: baseline; gap: var(--td-comp-margin-s); margin: 0 0 calc(var(--td-comp-margin-s) * 1.5); color: var(--td-text-color-primary); font-size: 14px; font-weight: 500; line-height: 1.4; }
.smart-summary ul { display: grid; gap: calc(var(--td-comp-margin-s) / 2); margin: 0; padding: 0; list-style: none; }
.smart-summary li { min-width: 0; margin: 0 calc(var(--td-comp-margin-s) * -1); border-radius: var(--td-radius-medium); }
.smart-summary li.is-bullet .smart-summary__line { padding-left: calc(var(--td-comp-margin-s) * 2); }
.smart-summary__line { display: flex; align-items: flex-start; gap: var(--td-comp-margin-s); min-width: 0; padding: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); border-radius: var(--td-radius-medium); transition: background-color .15s ease; }
.smart-summary__line:hover { background: rgba(255,255,255,.34); }
.smart-summary__bullet { flex: none; color: var(--td-text-color-placeholder); font-weight: 600; line-height: 1.7; }
.smart-summary p { flex: 1; min-width: 0; margin: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: 1.7; white-space: pre-wrap; word-break: break-word; }
.smart-summary__evidence-button { display: inline-grid; flex: none; width: 24px; height: 24px; margin-top: 1px; padding: 0; place-items: center; border: 0; border-radius: var(--td-radius-medium); background: transparent; color: var(--td-brand-color); opacity: 0; visibility: hidden; cursor: pointer; transition: opacity .15s ease, background-color .15s ease; }
.smart-summary__line:hover .smart-summary__evidence-button, .smart-summary__evidence-button:focus-visible { opacity: 1; visibility: visible; }
.smart-summary__evidence-button:hover { background: var(--td-brand-color-light); }
.smart-summary__evidence-button:focus-visible { outline: 2px solid var(--td-brand-color-focus); outline-offset: 1px; }
@media (hover: none) { .smart-summary__evidence-button { opacity: 1; visibility: visible; } }
</style>
