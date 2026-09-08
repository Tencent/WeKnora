<template>
  <section class="learning-panel" :class="{ compact }" :aria-label="t('learning.title')">
    <header class="learning-toolbar">
      <button class="panel-toggle" type="button" :aria-expanded="expanded" @click="expanded = !expanded">
        <t-icon name="education" /><strong>{{ t('learning.title') }}</strong><t-icon :name="expanded ? 'chevron-up' : 'chevron-down'" />
      </button>
      <button type="button" class="t-switch learning-switch" role="switch" :aria-checked="state.settings?.enabled === true"
        :class="{ 't-is-checked': state.settings?.enabled, 't-is-loading': consentBusy, 't-is-disabled': consentBusy }"
        :disabled="consentBusy" :aria-label="t('learning.consent')" @click="toggleConsent(!state.settings?.enabled)">
        <span class="t-switch__handle"><t-loading v-if="consentBusy" size="small" /></span>
      </button>
      <span class="consent-label">{{ t(state.settings?.enabled ? 'learning.enabled' : 'learning.disabled') }}</span>
      <t-dropdown trigger="click" :options="privacyOptions" :hide-after-item-click="false" @click="privacyAction"
        :popup-props="{ visible: privacyMenuVisible, onVisibleChange: (visible: boolean) => privacyMenuVisible = visible }">
        <t-button size="small" variant="text" :disabled="state.busy || state.exporting" :aria-label="t('learning.privacy')">
          <template #icon><t-icon name="more" /></template>{{ t('learning.privacy') }}
        </t-button>
      </t-dropdown>
    </header>
    <div v-if="state.error" class="panel-message" role="alert">
      <span>{{ t(`learning.errors.${state.error}`) }}</span>
      <t-button size="small" variant="text" :disabled="state.busy" @click="controller.initialize()">{{ t('learning.retry') }}</t-button>
    </div>
    <div v-if="state.clearResult" class="panel-message" role="status">{{ t('learning.cleared', { attempts: state.clearResult.deleted_attempts, topics: state.clearResult.deleted_mastery, quizzes: state.clearResult.deleted_quizzes }) }}</div>
    <div v-if="expanded && state.settings?.enabled && !state.busy" class="learning-content">
      <div class="learning-overview">
        <div class="overview-heading">
          <h3>{{ t('learning.overview') }}</h3>
          <span v-if="state.overview">{{ t('learning.topicCount', { count: state.overview.total_nodes }) }}</span>
          <t-button size="small" variant="text" :loading="state.loading" :aria-label="t('learning.refresh')" @click="controller.overview()"><t-icon name="refresh" /></t-button>
        </div>
        <div v-if="state.overview" class="overview-counts">
          <div v-for="status in learningStates" :key="status"><strong>{{ state.overview.counts[status] }}</strong><LearningStateBadge :state="status" /></div>
        </div>
        <LearningRecommendations :items="state.recommendations" @open-page="$emit('open-page', $event)" />
      </div>
      <div v-if="page || state.quiz || state.quizLoading || state.quizError" class="learning-practice">
        <div v-if="state.node" class="page-state">
          <LearningStateBadge :state="state.node.mastery.state" :mastery="state.node.mastery" />
          <LearningStateBadge v-if="state.node.familiar" familiar />
        </div>
        <div v-if="state.pageError" class="panel-message" role="alert">
          {{ t(`learning.errors.${state.pageError}`) }}
          <t-button size="small" variant="text" @click="controller.loadNode()">{{ t('learning.retry') }}</t-button>
        </div>
        <LearningQuiz :controller="controller" :title="page?.title" :can-prepare="!!page"
          @open-page="$emit('open-page', $event)" @open-source="$emit('open-source', $event)" />
      </div>
    </div>
    <t-dialog :visible="!!confirmAction" :header="t('learning.clearTitle')" :confirm-btn="{ content: t('learning.confirmClear'), theme: 'danger' }"
      :cancel-btn="t('learning.cancel')" :close-on-overlay-click="false" @confirm="confirmClear" @close="confirmAction = ''">
      {{ t(confirmAction === 'clearAll' ? 'learning.clearAllConfirm' : 'learning.clearKBConfirm') }}
    </t-dialog>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownOption } from 'tdesign-vue-next'
import type { LearningController } from '@/composables/useLearningState'
import { learningStates } from '@/composables/learningHelpers'
import LearningQuiz from './LearningQuiz.vue'
import LearningStateBadge from './LearningStateBadge.vue'
import LearningRecommendations from './LearningRecommendations.vue'
const props = defineProps<{ controller: LearningController; page?: { title: string } | null; compact?: boolean; scopeKey: string }>()
defineEmits<{ 'open-page': [slug: string]; 'open-source': [knowledgeId: string] }>()
const { t } = useI18n()
const state = computed(() => props.controller.state)
const consentBusy = computed(() => state.value.busy || (!state.value.settings && state.value.loading))
const expanded = ref(true)
const privacyMenuVisible = ref(false)
const confirmAction = ref<'' | 'clearKB' | 'clearAll'>('')
watch(() => [props.scopeKey, state.value.busy, state.value.settings?.enabled], () => { confirmAction.value = ''; privacyMenuVisible.value = false }, { flush: 'sync' })
const privacyOptions = computed(() => [
  { content: t('learning.exportKB'), value: 'exportKB' },
  { content: t('learning.exportAll'), value: 'exportAll' },
  { content: t('learning.clearKB'), value: 'clearKB', theme: 'error' as const },
  { content: t('learning.clearAll'), value: 'clearAll', theme: 'error' as const },
])
function toggleConsent(value: boolean | string | number) {
  if (value === true) expanded.value = true
  void props.controller.privacy(value === true ? 'enable' : 'disable')
}
async function privacyAction(option: DropdownOption) {
  privacyMenuVisible.value = false
  if (option.value === 'clearKB' || option.value === 'clearAll') { confirmAction.value = option.value; return }
  const scope = props.scopeKey
  const epoch = state.value.epoch
  const result = await props.controller.exportData(option.value === 'exportAll')
  if (!result || scope !== props.scopeKey || epoch !== state.value.epoch) return
  const blob = new Blob([JSON.stringify(result, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url; link.download = 'guided-learning.json'; link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
function confirmClear() {
  const action = confirmAction.value
  confirmAction.value = ''
  if (action) void props.controller.privacy(action)
}
</script>
<style scoped>
.learning-panel { min-width: 0; color: var(--td-text-color-primary); border-bottom: 1px solid var(--td-component-stroke); background: var(--td-bg-color-container); }
.learning-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; padding: 8px 16px; }
.learning-switch { padding: 0; border: 0; flex: none; border-radius: 8px; }
.learning-switch .t-switch__handle { border-radius: 6px; }
.learning-switch:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 3px; }
.panel-toggle { display: flex; align-items: center; gap: 7px; border: 0; background: transparent; color: inherit; cursor: pointer; font: inherit; padding: 4px 0; }
.panel-toggle strong { font-size: 13px; font-weight: 500; }
.consent-label { color: var(--td-text-color-secondary); font-size: 12px; margin-right: auto; }
.learning-content { display: grid; grid-template-columns: minmax(220px, 1fr) minmax(280px, 1.5fr); gap: 24px; padding: 8px 16px 16px; max-height: 44vh; overflow: auto; scrollbar-gutter: stable; }
.learning-content:not(:has(.learning-practice)) { grid-template-columns: 1fr; }
.learning-overview, .learning-practice { min-width: 0; }
.overview-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; font-size: 12px; color: var(--td-text-color-secondary); }
h3 { margin: 0; font-size: 13px; color: var(--td-text-color-primary); font-weight: 600; }
.overview-counts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin: 12px 0 18px; }
.overview-counts > div { display: flex; align-items: center; flex-wrap: wrap; gap: 7px; }
.overview-counts strong { font-size: 18px; font-weight: 500; }
.page-state { display: flex; flex-wrap: wrap; gap: 12px; margin-bottom: 8px; }
.panel-message { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 8px 16px; font-size: 12px; color: var(--td-text-color-secondary); }
.compact .learning-content { grid-template-columns: 1fr; max-height: none; }
.compact .learning-toolbar { padding-inline: 0; }
.compact .learning-content { padding-inline: 0; }
@media (max-width: 760px) {
  .learning-content { grid-template-columns: minmax(0, 1fr); max-height: 42vh; gap: 16px; }
  .learning-practice { grid-row: 1; }
  .learning-toolbar { padding-inline: 10px; }
}
</style>
