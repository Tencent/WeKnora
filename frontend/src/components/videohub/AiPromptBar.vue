<template>
  <div class="ai-prompt-bar">
    <t-input v-model="value" placeholder="向知识库提问（建设中）" disabled />
    <t-button disabled>发送</t-button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
const value = ref('')
</script>

<style scoped>
.ai-prompt-bar { position: sticky; bottom: 16px; z-index: 3; display: grid; grid-template-columns: 1fr auto; gap: 8px; max-width: 960px; margin: 32px auto 0; padding: 8px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: color-mix(in srgb, var(--td-bg-color-container) 92%, transparent); box-shadow: var(--td-shadow-1); backdrop-filter: blur(8px); }
.ai-prompt-bar:focus-within { border-color: var(--td-brand-color); }
</style>
