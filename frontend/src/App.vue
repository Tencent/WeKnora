<script setup lang="ts">
import ManualKnowledgeEditor from '@/components/manual-knowledge-editor.vue'
</script>
<template>
  <div id="app">
    <RouterView />
    <ManualKnowledgeEditor />
  </div>
</template>
<style>
body,
html,
#app {
    width: 100%;
    height: 100%;
    margin: 0;
    padding: 0;
    font-size: 14px;
    font-family: Helvetica Neue, Helvetica, PingFang SC, Hiragino Sans GB,
        Microsoft YaHei, SimSun, sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    background: var(--td-bg-color-page);
    color: var(--td-text-color-primary);
}
</style>
