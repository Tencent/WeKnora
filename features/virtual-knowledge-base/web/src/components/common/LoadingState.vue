<template>
  <div class="loading-state">
    <t-loading size="small" />
    <span v-if="label">{{ label }}</span>
  </div>
</template>

<script setup lang="ts">
const props = defineProps<{ label?: string }>();
</script>

<style scoped>
.loading-state {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  justify-content: center;
  color: var(--td-text-color-secondary);
}
</style>
