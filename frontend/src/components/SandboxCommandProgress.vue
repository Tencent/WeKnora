<template>
  <section class="sandbox-command" aria-live="off">
    <div class="sandbox-command-heading">
      <span><t-loading size="12px" />{{ $t('settings.sandbox.installCommandRunning') }}</span>
      <span class="sandbox-command-elapsed">{{ commandElapsed }}</span>
    </div>
    <code :title="progress.command">{{ progress.command }}</code>
    <pre v-if="progress.output">{{ progress.output }}</pre>
    <p v-else>{{ $t('settings.sandbox.installCommandWaiting') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'

const props = defineProps<{
  progress: { command: string; started_at: string; output: string; done: boolean }
}>()
const clock = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
const commandElapsed = computed(() => {
  const started = Date.parse(props.progress.started_at)
  const seconds = Number.isFinite(started) ? Math.max(0, Math.floor((clock.value - started) / 1000)) : 0
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
})
watch(() => props.progress.done, done => {
  clearInterval(timer)
  clock.value = Date.now()
  if (!done) timer = setInterval(() => { clock.value = Date.now() }, 1000)
}, { immediate: true })
onUnmounted(() => clearInterval(timer))
</script>

<style scoped lang="less">
.sandbox-command {
  min-width: 0;
  margin: 8px 0;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke, #e7e7e7);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer, #f7f7f7);
  color: var(--td-text-color-secondary, #666);
  font-size: 12px;
  line-height: 1.5;

  code, pre {
    font-family: var(--td-font-family-code, monospace);
    font-size: 11px;
    line-height: 1.6;
  }

  code {
    display: block;
    margin-top: 6px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  pre {
    max-height: 160px;
    margin: 8px 0 0;
    padding-top: 8px;
    overflow: auto;
    overscroll-behavior: contain;
    border-top: 1px solid var(--td-component-stroke, #e7e7e7);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  p { margin: 6px 0 0; font-size: 12px; line-height: 1.5; }
}
.sandbox-command-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;

  > span:first-child { display: inline-flex; align-items: center; gap: 6px; }
}
.sandbox-command-elapsed {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder, #999);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
</style>
