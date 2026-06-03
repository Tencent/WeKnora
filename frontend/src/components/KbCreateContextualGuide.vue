<template>
  <SpotlightGuide v-model:active="active" :steps="guideSteps" step-i18n-prefix="contextualGuide.kbCreate.steps"
    labels-prefix="contextualGuide" @finish="onFinish" @dismiss="onFinish" />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import {
  KB_EDITOR_FOCUS_SECTION_EVENT,
  markContextualGuideDone,
  isContextualGuideDone,
  isGlobalUserGuideDone,
} from '@/config/contextualGuides'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const props = defineProps<{
  when: boolean
  isFaq: boolean
  needsEmbedding: boolean
}>()

const active = ref(false)

const focusSection = (section: string) => {
  window.dispatchEvent(
    new CustomEvent(KB_EDITOR_FOCUS_SECTION_EVENT, { detail: { section } }),
  )
}

const guideSteps = computed<SpotlightGuideStep[]>(() => {
  const steps: SpotlightGuideStep[] = [
    {
      key: 'type',
      target: '[data-guide="kb-create-type"]',
      placement: 'right',
      before: () => focusSection('basic'),
    },
    {
      key: 'name',
      target: '[data-guide="kb-create-name"]',
      placement: 'right',
      before: () => focusSection('basic'),
    },
  ]

  if (!props.isFaq) {
    steps.push({
      key: 'indexing',
      target: '[data-guide="kb-create-indexing"]',
      placement: 'right',
      before: () => focusSection('basic'),
    })
  }

  steps.push(
    {
      key: 'navModels',
      target: '[data-guide="kb-editor-nav-models"]',
      placement: 'right',
      before: () => focusSection('models'),
    },
    {
      key: 'llm',
      target: '[data-guide="kb-create-llm"]',
      placement: 'right',
      before: () => focusSection('models'),
    },
  )

  if (props.needsEmbedding) {
    steps.push({
      key: 'embedding',
      target: '[data-guide="kb-create-embedding"]',
      placement: 'right',
      before: () => focusSection('models'),
      optional: true,
    })
  }

  if (!props.isFaq) {
    steps.push(
      {
        key: 'parser',
        target: '[data-guide="kb-editor-nav-parser"]',
        placement: 'right',
        before: () => focusSection('parser'),
        optional: true,
      },
      {
        key: 'chunking',
        target: '[data-guide="kb-editor-nav-chunking"]',
        placement: 'right',
        before: () => focusSection('chunking'),
        optional: true,
      },
      {
        key: 'storage',
        target: '[data-guide="kb-editor-nav-storage"]',
        placement: 'right',
        before: () => focusSection('storage'),
        optional: true,
      },
      {
        key: 'navMultimodal',
        target: '[data-guide="kb-editor-nav-multimodal"]',
        placement: 'right',
        before: () => focusSection('multimodal'),
        optional: true,
      },
      {
        key: 'multimodalToggle',
        target: '[data-guide="kb-create-multimodal-toggle"]',
        placement: 'right',
        before: () => focusSection('multimodal'),
        optional: true,
      },
      {
        key: 'multimodalVllm',
        target: '[data-guide="kb-create-multimodal-vllm"]',
        placement: 'right',
        before: () => focusSection('multimodal'),
        optional: true,
      },
    )
  } else {
    steps.push({
      key: 'faq',
      target: '[data-guide="kb-editor-nav-faq"]',
      placement: 'right',
      before: () => focusSection('faq'),
      optional: true,
    })
  }

  steps.push({
    key: 'submit',
    target: '[data-guide="kb-create-submit"]',
    placement: 'top',
    before: () => focusSection('basic'),
    interact: true,
  })

  return steps
})

let openTimer: ReturnType<typeof setTimeout> | null = null
let waitGlobalTimer: ReturnType<typeof setTimeout> | null = null

const onFinish = () => {
  markContextualGuideDone('kbCreate')
}

const tryOpen = () => {
  if (!props.when || isContextualGuideDone('kbCreate') || active.value) return
  openTimer = setTimeout(() => {
    if (!props.when || isContextualGuideDone('kbCreate')) return
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
  if (!props.when || isContextualGuideDone('kbCreate')) return
  if (isGlobalUserGuideDone()) {
    tryOpen()
    return
  }
  const poll = () => {
    if (!props.when || isContextualGuideDone('kbCreate')) return
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
