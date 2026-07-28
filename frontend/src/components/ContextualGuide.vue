<template>
  <SpotlightGuide v-model:active="active" :steps="config.steps" :step-i18n-prefix="config.stepI18nPrefix"
    labels-prefix="contextualGuide" @finish="onFinish" @dismiss="onFinish" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import {
  CONTEXTUAL_GUIDE_TOURS,
  isContextualGuideDone,
  isGlobalUserGuideDone,
  type ContextualGuideTourConfig,
  type ContextualGuideTourId,
} from '@/config/contextualGuides'
import { useResponsiveViewport } from '@/composables/useResponsiveViewport'

const props = defineProps<{
  tour: ContextualGuideTourId
  /** 为 true 且未完成过该情境引导时，在满足全局引导已结束后自动打开 */
  when: boolean
}>()

const config: ContextualGuideTourConfig = CONTEXTUAL_GUIDE_TOURS[props.tour]
const active = ref(false)
const { isCompact } = useResponsiveViewport()
const COMPACT_EXIT_DEBOUNCE_MS = 300
let compactExiting = false
let compactExitTimer: number | null = null

const clearCompactExitTimer = () => {
  if (compactExitTimer !== null) {
    window.clearTimeout(compactExitTimer)
    compactExitTimer = null
  }
}

const canOpen = () => {
  if (!props.when) return false
  if (isCompact.value) return false
  return !compactExiting
}

let openTimer: ReturnType<typeof setTimeout> | null = null
let waitGlobalTimer: ReturnType<typeof setTimeout> | null = null

const clearTimers = () => {
  if (openTimer) {
    clearTimeout(openTimer)
    openTimer = null
  }
  if (waitGlobalTimer) {
    clearTimeout(waitGlobalTimer)
    waitGlobalTimer = null
  }
  clearCompactExitTimer()
}

const tryOpen = () => {
  if (active.value) return
  if (!canOpen()) return
  if (isContextualGuideDone(props.tour)) return
  if (!isGlobalUserGuideDone()) return

  openTimer = setTimeout(() => {
    if (!canOpen() || isContextualGuideDone(props.tour) || active.value) return
    active.value = true
  }, config.openDelayMs)
}

const scheduleOpen = () => {
  clearTimers()
  if (!canOpen() || isContextualGuideDone(props.tour)) return

  if (isGlobalUserGuideDone()) {
    tryOpen()
    return
  }

  // 等待全局新手引导结束后再展示情境引导，避免两层遮罩叠加
  const poll = () => {
    if (!canOpen() || isContextualGuideDone(props.tour)) {
      clearTimers()
      return
    }
    if (isGlobalUserGuideDone()) {
      waitGlobalTimer = null
      tryOpen()
      return
    }
    waitGlobalTimer = setTimeout(poll, 400)
  }
  waitGlobalTimer = setTimeout(poll, 400)
}

const onFinish = () => {
  localStorage.setItem(config.storageKey, '1')
  config.alsoCompleteTours?.forEach((id) => {
    localStorage.setItem(CONTEXTUAL_GUIDE_TOURS[id].storageKey, '1')
  })
}

watch(
  [() => props.when, isCompact],
  ([when, compact]) => {
    if (compact) {
      clearTimers()
      active.value = false
      compactExiting = false
      clearCompactExitTimer()
      return
    }
    if (!when) {
      clearTimers()
      active.value = false
      return
    }
    clearCompactExitTimer()
    compactExiting = true
    compactExitTimer = window.setTimeout(() => {
      compactExitTimer = null
      compactExiting = false
      if (!isCompact.value && props.when) scheduleOpen()
    }, COMPACT_EXIT_DEBOUNCE_MS)
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  clearTimers()
})
</script>
