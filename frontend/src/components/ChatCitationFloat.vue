<template>
  <Teleport to="body">
    <div v-if="float.visible" ref="floatRef" class="chat-citation-float" :style="floatStyle"
      @mouseenter="onEnter?.()" @mouseleave="onLeave?.()">
      <template v-if="float.type === 'web'">
        <div class="chat-citation-float__title">{{ float.title || float.url }}</div>
        <a v-if="float.url" class="chat-citation-float__link" :href="float.url" target="_blank"
          rel="noopener noreferrer">{{ float.url }}</a>
      </template>
      <template v-else>
        <div class="chat-citation-float__title">{{ float.title }}</div>
        <div v-if="float.loading" class="chat-citation-float__muted">{{ loadingText }}</div>
        <div v-else-if="float.error" class="chat-citation-float__error">{{ float.error }}</div>
        <div
          v-else
          ref="bodyRef"
          class="chat-citation-float__body markdown-content"
          v-stable-html="renderedContent"
        />
      </template>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch, type CSSProperties } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CitationFloatState } from '@/composables/useChatCitationPopover'
import { vStableHtml } from '@/directives/stableHtml'
import { renderCitationPreviewContent } from '@/utils/citationPreview'
import { hydrateProtectedFileImages } from '@/utils/security'
import {
  computeCitationFloatPosition,
  currentCitationViewport,
} from '@/utils/citationFloatPosition'

const props = defineProps<{
  float: CitationFloatState
  onEnter?: () => void
  onLeave?: () => void
}>()

const { t } = useI18n()
const loadingText = t('common.loading')
const floatRef = ref<HTMLElement | null>(null)
const bodyRef = ref<HTMLElement | null>(null)
const renderedContent = computed(() => renderCitationPreviewContent(props.float.content))
const resolvedTop = ref(props.float.top)
const resolvedLeft = ref(props.float.left)
const floatStyle = computed<CSSProperties>(() => ({
  top: `${resolvedTop.value}px`,
  left: `${resolvedLeft.value}px`,
}))

const updateFloatPosition = async () => {
  if (!props.float.visible) return
  await nextTick()
  const el = floatRef.value
  if (!el || !props.float.anchor) {
    resolvedTop.value = props.float.top
    resolvedLeft.value = props.float.left
    return
  }

  const rect = el.getBoundingClientRect()
  const position = computeCitationFloatPosition({
    anchor: props.float.anchor,
    floatSize: {
      width: rect.width || 320,
      height: rect.height || 80,
    },
    viewport: currentCitationViewport(),
    offsetY: props.float.offsetY,
  })
  resolvedTop.value = position.top
  resolvedLeft.value = position.left
}

watch(renderedContent, async () => {
  await nextTick()
  await hydrateProtectedFileImages(bodyRef.value)
  await updateFloatPosition()
}, { flush: 'post' })

watch(() => [
  props.float.visible,
  props.float.top,
  props.float.left,
  props.float.anchor?.top,
  props.float.anchor?.bottom,
  props.float.anchor?.left,
  props.float.offsetY,
  props.float.loading,
  props.float.error,
  props.float.title,
  renderedContent.value,
], () => {
  void updateFloatPosition()
}, { flush: 'post', immediate: true })
</script>

<style lang="less">
@import './css/chat-citations.less';
</style>
