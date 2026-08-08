<template>
  <main class="model-usage-page">
    <ModelUsageSettings />
  </main>
</template>

<script setup lang="ts">
import ModelUsageSettings from '@/views/settings/ModelUsageSettings.vue'
</script>

<style scoped lang="less">
.model-usage-page {
  width: 100%;
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  box-sizing: border-box;
  padding: 32px 36px 40px;
  background: var(--td-bg-color-page, #f5f7fa);
}

@media (max-width: 768px) {
  .model-usage-page {
    padding: 20px 16px 28px;
  }
}
</style>
