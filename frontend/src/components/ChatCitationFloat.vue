<template>
  <Teleport to="body">
    <div v-if="float.visible" class="chat-citation-float" :class="`chat-citation-float--${float.type}`"
      :style="{ top: `${float.top}px`, left: `${float.left}px` }"
      @mouseenter="onEnter?.()" @mouseleave="onLeave?.()">
      <template v-if="float.type === 'web'">
        <div class="chat-citation-float__header">
          <span class="chat-citation-float__icon chat-citation-float__icon--web" aria-hidden="true" />
          <div class="chat-citation-float__title">{{ float.title || float.url }}</div>
        </div>
        <a v-if="float.url" class="chat-citation-float__link" :href="float.url" target="_blank"
          rel="noopener noreferrer">{{ float.url }}</a>
      </template>
      <template v-else>
        <div class="chat-citation-float__header">
          <span class="chat-citation-float__icon chat-citation-float__icon--book" aria-hidden="true" />
          <div class="chat-citation-float__title">{{ float.title }}</div>
        </div>
        <div v-if="float.loading" class="chat-citation-float__muted">{{ loadingText }}</div>
        <div v-else-if="float.error" class="chat-citation-float__error">{{ float.error }}</div>
        <div v-else class="chat-citation-float__body">{{ plainContent }}</div>
      </template>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CitationFloatState } from '@/composables/useChatCitationPopover'

const props = defineProps<{
  float: CitationFloatState
  onEnter?: () => void
  onLeave?: () => void
}>()

const { t } = useI18n()
const loadingText = t('common.loading')

// 预览只放纯文本摘录，去掉 Markdown 标记和多余空行
const plainContent = computed(() => props.float.content
  .replace(/^\s*([-*_])\1{2,}\s*$/gm, '')
  .replace(/^#{1,6}\s+/gm, '')
  .replace(/^\s*>\s?/gm, '')
  .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
  .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
  .replace(/(\*\*|__|`)/g, '')
  .replace(/\n{2,}/g, '\n')
  .trim())
</script>

<style lang="less">
@import './css/chat-citations.less';
</style>
