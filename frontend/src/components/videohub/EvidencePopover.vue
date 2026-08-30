<template>
  <t-popup
    v-model:visible="visible"
    trigger="hover"
    placement="bottom-right"
    :show-arrow="true"
    destroy-on-close
    overlay-class-name="video-evidence-popover-overlay"
    :overlay-inner-style="{ padding: 0 }"
  >
    <div
      class="video-evidence-popover__trigger"
      tabindex="0"
      aria-haspopup="dialog"
      :aria-expanded="visible"
      @focusin="visible = true"
      @click="visible = true"
      @keydown.esc="visible = false"
    >
      <slot />
    </div>
    <template #content>
      <div class="video-evidence-popover" role="dialog" aria-label="视频内容出处" @click.stop>
        <div class="video-evidence-popover__title">
          <span>原文出处</span>
          <span>共 {{ evidence.length }} 条</span>
        </div>
        <div class="video-evidence-popover__list">
          <div v-for="item in evidence" :key="item.chunkId" class="video-evidence-popover__item">
            <div class="video-evidence-popover__time">{{ item.timestamp }}</div>
            <blockquote>{{ item.transcriptSnippet }}</blockquote>
            <button type="button" class="video-evidence-popover__timestamp" @click="seek(item.startSeconds)">
              <t-icon name="play-circle" size="14px" />
              <span>定位到视频</span>
            </button>
          </div>
        </div>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SummaryEvidence } from '@/types/videohub'

const props = defineProps<{ evidence: SummaryEvidence[] }>()
const emit = defineEmits<{ seek: [seconds: number] }>()
const visible = ref(false)
const evidence = computed(() => props.evidence)

function seek(seconds: number) {
  emit('seek', seconds)
  visible.value = false
}
</script>

<style>
.video-evidence-popover-overlay .t-popup__content { padding: 0; border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--rounded-popup, 10px); background: var(--color-bg-popup, var(--td-bg-color-container)); box-shadow: var(--shadow-popup); backdrop-filter: blur(20px) saturate(180%); }
.video-evidence-popover__trigger { border-radius: var(--td-radius-medium); outline: none; }
.video-evidence-popover__trigger:focus-visible { box-shadow: 0 0 0 2px var(--td-brand-color-focus); }
.video-evidence-popover { width: min(360px, calc(100vw - 32px)); padding: calc(var(--td-comp-margin-s) * 2); color: var(--td-text-color-primary); }
.video-evidence-popover__title { display: flex; align-items: center; gap: var(--td-comp-margin-s); color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); font-weight: 600; }
.video-evidence-popover__list { display: grid; gap: calc(var(--td-comp-margin-s) * 1.5); max-height: 320px; overflow-y: auto; margin-top: calc(var(--td-comp-margin-s) * 1.5); }
.video-evidence-popover__item + .video-evidence-popover__item { padding-top: calc(var(--td-comp-margin-s) * 1.5); border-top: var(--border-width-hairline, .5px) solid var(--td-component-stroke); }
.video-evidence-popover blockquote { margin: calc(var(--td-comp-margin-s) / 2) 0 var(--td-comp-margin-s); padding: 0; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-medium); line-height: 1.65; }
.video-evidence-popover__footer { display: flex; align-items: center; justify-content: flex-end; }
.video-evidence-popover__time { color: var(--td-brand-color); font-family: monospace; font-size: var(--td-font-size-body-small); font-weight: 500; }
.video-evidence-popover__timestamp { display: inline-flex; align-items: center; gap: calc(var(--td-comp-margin-s) / 2); padding: calc(var(--td-comp-margin-s) / 2) var(--td-comp-margin-s); border: 0; border-radius: var(--td-radius-medium); background: var(--td-brand-color-light); color: var(--td-brand-color); font: inherit; font-size: var(--td-font-size-body-small); cursor: pointer; }
.video-evidence-popover__timestamp:hover { background: var(--td-brand-color-focus); color: var(--td-brand-color-hover); }
</style>
