<script setup lang="ts">
// /platform/settings 只是一个可分享、可刷新的 URL 状态。设置弹窗本身由
// views/platform/index.vue 常驻的 <Settings /> 渲染，其 visible 同时看
// route.path === '/platform/settings' 与 uiStore.showSettingsModal，
// 所以这个路由出口不需要再挂一份 Settings.vue。
</script>

<template>
  <!-- 设置弹窗由 platform 布局渲染 -->
</template>
