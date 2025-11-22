<template>
  <div class="error-state">
    <t-icon name="error-circle" size="24px" />
    <span>{{ message }}</span>
    <t-button v-if="onRetry" theme="primary" variant="outline" size="small" @click="onRetry">
      重试
    </t-button>
  </div>
</template>

<script setup lang="ts">
const props = defineProps<{ message: string; onRetry?: () => void }>();
const { message, onRetry } = props;
</script>

<style scoped>
.error-state {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  justify-content: center;
  color: var(--td-error-color);
}
</style>
