<template>
  <SpotlightGuide v-model:active="active" :steps="guideSteps" step-i18n-prefix="contextualGuide.agentCreate.steps"
    labels-prefix="contextualGuide" @finish="onFinish" @dismiss="onFinish" />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import {
  focusAgentEditorSection,
  markContextualGuideDone,
  isContextualGuideDone,
  isGlobalUserGuideDone,
} from '@/config/contextualGuides'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const props = defineProps<{
  when: boolean
  isAgentMode: boolean
}>()

const active = ref(false)

const guideSteps = computed<SpotlightGuideStep[]>(() => {
  const steps: SpotlightGuideStep[] = [
    {
      key: 'mode',
      target: '[data-guide="agent-create-mode"]',
      placement: 'right',
      before: () => focusAgentEditorSection('basic'),
    },
    {
      key: 'agentType',
      target: '[data-guide="agent-create-agent-type"]',
      placement: 'right',
      before: () => focusAgentEditorSection('basic'),
      optional: true,
    },
    {
      key: 'name',
      target: '[data-guide="agent-create-name"]',
      placement: 'right',
      before: () => focusAgentEditorSection('basic'),
    },
    {
      key: 'navModel',
      target: '[data-guide="agent-editor-nav-model"]',
      placement: 'right',
      before: () => focusAgentEditorSection('model'),
    },
    {
      key: 'model',
      target: '[data-guide="agent-create-model"]',
      placement: 'right',
      before: () => focusAgentEditorSection('model'),
    },
    {
      key: 'navKnowledge',
      target: '[data-guide="agent-editor-nav-knowledge"]',
      placement: 'right',
      before: () => focusAgentEditorSection('knowledge'),
    },
    {
      key: 'knowledge',
      target: '[data-guide="agent-create-knowledge"]',
      placement: 'right',
      before: () => focusAgentEditorSection('knowledge'),
    },
    {
      key: 'navWebsearch',
      target: '[data-guide="agent-editor-nav-websearch"]',
      placement: 'right',
      before: () => focusAgentEditorSection('websearch'),
      optional: true,
    },
    {
      key: 'navMultimodal',
      target: '[data-guide="agent-editor-nav-multimodal"]',
      placement: 'right',
      before: () => focusAgentEditorSection('multimodal'),
      optional: true,
    },
    {
      key: 'multimodal',
      target: '[data-guide="agent-create-multimodal"]',
      placement: 'right',
      before: () => focusAgentEditorSection('multimodal'),
      optional: true,
    },
  ]

  if (props.isAgentMode) {
    steps.push({
      key: 'navTools',
      target: '[data-guide="agent-editor-nav-tools"]',
      placement: 'right',
      before: () => focusAgentEditorSection('tools'),
      optional: true,
    })
  }

  steps.push({
    key: 'submit',
    target: '[data-guide="agent-create-submit"]',
    placement: 'top',
    before: () => focusAgentEditorSection('basic'),
    interact: true,
  })

  return steps
})

let openTimer: ReturnType<typeof setTimeout> | null = null
let waitGlobalTimer: ReturnType<typeof setTimeout> | null = null

const onFinish = () => {
  markContextualGuideDone('agentCreate')
}

const tryOpen = () => {
  if (!props.when || isContextualGuideDone('agentCreate') || active.value) return
  openTimer = setTimeout(() => {
    if (!props.when || isContextualGuideDone('agentCreate')) return
    active.value = true
  }, 450)
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
  if (!props.when || isContextualGuideDone('agentCreate')) return
  if (isGlobalUserGuideDone()) {
    tryOpen()
    return
  }
  const poll = () => {
    if (!props.when || isContextualGuideDone('agentCreate')) return
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
      return
    }
    scheduleOpen()
  },
  { immediate: true },
)
</script>
