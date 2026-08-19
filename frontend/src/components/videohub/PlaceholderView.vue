<template>
  <section class="placeholder-view" role="status">
    <div class="placeholder-view__icon">◌</div>
    <h2>{{ title }}</h2>
    <p>{{ description }}</p>
  </section>
</template>

<script setup lang="ts">
withDefaults(defineProps<{ title: string; description?: string }>(), {
  description: '该功能正在建设中，敬请期待。',
})
</script>

<style scoped>
.placeholder-view { min-height: 280px; display: grid; place-content: center; justify-items: center; gap: 12px; color: var(--td-text-color-secondary); text-align: center; }
.placeholder-view__icon { width: 56px; height: 56px; display: grid; place-items: center; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-round); background: var(--td-bg-color-component); color: var(--td-brand-color); font-size: 36px; }
.placeholder-view h2 { margin: 0; color: var(--td-text-color-primary); font-size: 20px; }
.placeholder-view p { margin: 0; }
</style>
