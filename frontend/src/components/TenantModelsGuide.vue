<template>
  <SpotlightGuide v-model:active="active" :steps="steps" :step-i18n-prefix="stepI18nPrefix"
    labels-prefix="contextualGuide" @finish="onFinish" @dismiss="onFinish" @step-change="onStepChange" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import {
  isContextualGuideDone,
  isGlobalUserGuideDone,
  markContextualGuideDone,
} from '@/config/contextualGuides'
import { useUIStore } from '@/stores/ui'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const props = withDefaults(
  defineProps<{
    when: boolean
    /** documentKb：需对话+Embedding；agent：仅需对话模型 */
    variant?: 'documentKb' | 'agent'
  }>(),
  { variant: 'documentKb' },
)

const stepI18nPrefix = computed(() =>
  props.variant === 'agent'
    ? 'contextualGuide.tenantModels.stepsAgent'
    : 'contextualGuide.tenantModels.steps',
)

const uiStore = useUIStore()
const active = ref(false)
let settingsOpenedByGuide = false

const steps: SpotlightGuideStep[] = [
  { key: 'intro' },
  {
    key: 'addModel',
    target: '[data-guide="settings-add-model"], [data-guide="settings-models"]',
    placement: 'left',
    before: () => {
      uiStore.openSettings('models')
      settingsOpenedByGuide = true
    },
  },
  { key: 'done' },
]

let openTimer: ReturnType<typeof setTimeout> | null = null

const closeGuideSettings = () => {
  if (settingsOpenedByGuide) {
    uiStore.closeSettings()
    settingsOpenedByGuide = false
  }
}

const onFinish = () => {
  markContextualGuideDone('tenantModels')
  closeGuideSettings()
}

const onStepChange = ({ toKey }: { toKey: string }) => {
  if (toKey !== 'addModel') {
    closeGuideSettings()
  }
}

let waitGlobalTimer: ReturnType<typeof setTimeout> | null = null

const tryOpen = () => {
  if (active.value || !props.when || isContextualGuideDone('tenantModels')) return
  openTimer = setTimeout(() => {
    if (!props.when || isContextualGuideDone('tenantModels')) return
    active.value = true
  }, 500)
}

const scheduleOpen = () => {
  if (openTimer) {
    clearTimeout(openTimer)
    openTimer = null
  }
  if (waitGlobalTimer) {
    clearTimeout(waitGlobalTimer)
    waitGlobalTimer = null
  }
  if (!props.when || isContextualGuideDone('tenantModels')) return
  if (isGlobalUserGuideDone()) {
    tryOpen()
    return
  }
  const poll = () => {
    if (!props.when || isContextualGuideDone('tenantModels')) return
    if (isGlobalUserGuideDone()) {
      tryOpen()
      return
    }
    waitGlobalTimer = setTimeout(poll, 400)
  }
  waitGlobalTimer = setTimeout(poll, 400)
}

watch(
  () => props.when,
  (val) => {
    if (!val) {
      if (openTimer) clearTimeout(openTimer)
      if (waitGlobalTimer) clearTimeout(waitGlobalTimer)
      openTimer = null
      waitGlobalTimer = null
      active.value = false
      closeGuideSettings()
      return
    }
    scheduleOpen()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (openTimer) clearTimeout(openTimer)
  if (waitGlobalTimer) clearTimeout(waitGlobalTimer)
  closeGuideSettings()
})
</script>
