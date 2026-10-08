<script setup lang="ts">
import { ref, useId } from 'vue'
const open = ref(false)
const panelId = useId()
</script>

<template>
  <div class="responsive-toolbar-group">
    <button type="button" class="responsive-toolbar-toggle" :aria-label="$t('common.more')"
      :aria-expanded="open" :aria-controls="panelId" @click="open = !open">
      <t-icon :name="open ? 'chevron-up' : 'more'" size="20px" />
    </button>
    <div :id="panelId" class="responsive-toolbar-panel" :class="{ 'is-open': open }"><slot /></div>
  </div>
</template>

<style scoped lang="less">
.responsive-toolbar-group, .responsive-toolbar-panel { display: contents; }
.responsive-toolbar-toggle { display: none; }
@media (max-width: 767px) {
  .responsive-toolbar-toggle {
    display: inline-flex; align-items: center; justify-content: center;
    width: 44px; height: 44px; flex-shrink: 0; border: 1px solid var(--td-component-border);
    border-radius: var(--app-radius-md); background: var(--td-bg-color-container); color: inherit;
  }
  .responsive-toolbar-panel { display: none; }
  .responsive-toolbar-panel.is-open {
    display: flex; flex: 1 0 100%; order: 10; flex-wrap: wrap; align-items: center; gap: 8px;
    max-height: 25dvh; overflow: auto;
  }
}
</style>
