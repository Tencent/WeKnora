<template>
  <div ref="bar" class="child-agent-tabs" :class="{ 'in-header': headerMode }" role="tablist" :aria-label="t('agentStream.collaboration.tabs')">
    <button type="button" role="tab" class="child-agent-tab child-agent-tab-parent" :class="{ 'is-active': !activeId }"
      :aria-selected="!activeId" :tabindex="!activeId ? 0 : -1" @click="$emit('select', '')"
      @keydown="navigate($event, 0)">
      <span v-if="attention" class="child-agent-tab-dot waiting" :title="t('agentStream.collaboration.status.waiting')" />
      <slot name="parent">{{ t('agentStream.collaboration.mainConversation') }}</slot>
    </button>
    <div class="child-agent-tabs-scroll">
    <div v-for="(tab, index) in tabs" :key="tab.id" class="child-agent-tab-entry" :class="{ 'is-active': activeId === tab.id }">
      <button type="button" role="tab" class="child-agent-tab" :aria-selected="activeId === tab.id"
        :tabindex="activeId === tab.id ? 0 : -1" :title="`${tab.task.displayName} · ${t(`agentStream.collaboration.status.${tab.task.status}`)}`"
        @click="$emit('select', tab.id)" @keydown="navigate($event, index + 1)">
        <span class="child-agent-tab-dot" :class="tab.task.status" aria-hidden="true" />
        <span class="child-agent-tab-name">{{ tab.task.displayName || t('agentStream.collaboration.unnamed') }}</span>
      </button>
      <button type="button" class="child-agent-tab-close" :aria-label="`${t('common.close')} ${tab.task.displayName}`"
        @click="closeTab(tab.id)"><CloseIcon aria-hidden="true" /></button>
    </div>
    </div>
  </div>
</template>
<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CloseIcon } from 'tdesign-icons-vue-next'
import type { CollaborationTaskView } from '@/utils/collaborationDisplay'
const props = defineProps<{ tabs: Array<{ id: string; task: CollaborationTaskView }>; activeId: string; attention?: boolean; headerMode?: boolean }>()
const emit = defineEmits<{ select: [id: string]; close: [id: string] }>()
const { t } = useI18n()
const bar = ref<HTMLElement>()
function closeTab(id: string) {
  emit('close', id)
  void nextTick(() => bar.value?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus())
}
function navigate(event: KeyboardEvent, index: number) {
  const count = props.tabs.length + 1
  let target: number
  if (event.key === 'ArrowRight') target = (index + 1) % count
  else if (event.key === 'ArrowLeft') target = (index + count - 1) % count
  else if (event.key === 'Home') target = 0
  else if (event.key === 'End') target = count - 1
  else return
  event.preventDefault()
  emit('select', target === 0 ? '' : props.tabs[target - 1].id)
  bar.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[target]?.focus()
}
watch(() => props.activeId, () => nextTick(() => {
  if (props.activeId) bar.value?.querySelector('.child-agent-tabs-scroll [aria-selected="true"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
}))
</script>
<style scoped lang="less">
.child-agent-tabs { display: flex; align-items: center; flex-shrink: 0; gap: 4px; padding: 6px 12px; border-bottom: 1px solid var(--td-component-border); min-width: 0; }
.child-agent-tabs-scroll { display: flex; align-items: center; gap: 4px; min-width: 0; overflow-x: auto; scrollbar-width: none; }
.child-agent-tabs-scroll::-webkit-scrollbar { display: none; }
.child-agent-tab-entry { display: flex; align-items: center; flex-shrink: 0; border-radius: var(--app-radius-sm); }
.child-agent-tab { display: flex; align-items: center; gap: 7px; padding: 7px 10px; border: 0; border-radius: var(--app-radius-sm); background: transparent; color: var(--td-text-color-secondary); font: inherit; font-size: var(--app-text-md); line-height: 1.4; white-space: nowrap; cursor: pointer; flex-shrink: 0; }
.child-agent-tabs.in-header { flex: 1; min-width: 0; border: 0; padding: 0; gap: 4px; }
.in-header .child-agent-tab { padding: 6px 8px; font-size: var(--app-text-sm, 13px); }
.in-header .child-agent-tab-name { max-width: 130px; }
.child-agent-tab-name { max-width: 160px; overflow: hidden; text-overflow: ellipsis; }
.child-agent-tab.is-active, .child-agent-tab-entry.is-active { background: var(--td-bg-color-secondarycontainer); }
.child-agent-tab.is-active, .is-active .child-agent-tab { color: var(--td-text-color-primary); }
.child-agent-tab-entry { transition: background-color var(--app-motion-fast) ease; }
.child-agent-tab-entry:hover, .child-agent-tab-parent:hover { background: var(--td-bg-color-container-hover); }
.child-agent-tab-entry:hover .child-agent-tab, .child-agent-tab-close:hover { color: var(--td-text-color-primary); }
.in-header .child-agent-tab-parent { position: relative; padding: 0; gap: 4px; margin-right: 8px; background: transparent; color: var(--td-text-color-primary); }
.in-header .child-agent-tab-parent > .child-agent-tab-dot { position: absolute; right: 0; top: 0; }
.child-agent-tab:focus-visible, .child-agent-tab-close:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: -2px; }
.child-agent-tab-close { display: flex; align-items: center; justify-content: center; width: 22px; height: 22px; margin-right: 5px; padding: 0; border: 0; border-radius: var(--app-radius-xs); background: transparent; color: var(--td-text-color-placeholder); cursor: pointer; font-size: var(--app-text-sm); }
.child-agent-tab-dot { width: 5px; height: 5px; border-radius: 50%; background: var(--td-text-color-placeholder); flex-shrink: 0; }
.child-agent-tab-dot.running { background: var(--td-brand-color); }
.child-agent-tab-dot.waiting, .child-agent-tab-dot.needs_attention { background: var(--td-warning-color); }
.child-agent-tab-dot.failed { background: var(--td-error-color); }
</style>
