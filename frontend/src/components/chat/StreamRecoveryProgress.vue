<template>
  <div class="stream-recovery-progress" role="status" aria-live="polite">
    <span class="stream-recovery-progress__spinner" aria-hidden="true" />
    <span class="stream-recovery-progress__label">
      {{ t('chat.restoringGenerationProgress') }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<style scoped lang="less">
.stream-recovery-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 28px;
  padding-left: 4px;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 22px;
}

.stream-recovery-progress__spinner {
  width: 14px;
  height: 14px;
  flex: 0 0 14px;
  box-sizing: border-box;
  border: 1.5px solid var(--td-component-stroke);
  border-top-color: var(--td-text-color-secondary);
  border-radius: 50%;
  animation: streamRecoverySpin 0.8s linear infinite;
}

@keyframes streamRecoverySpin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .stream-recovery-progress__spinner {
    animation: none;
  }
}
</style>
