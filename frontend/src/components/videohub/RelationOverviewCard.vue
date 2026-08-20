<template>
  <section class="relation-overview">
    <p>
      当前视频共发现 <strong>{{ overview.relation_count }}</strong> 条关联知识，关联
      <strong>{{ overview.related_video_count }}</strong> 个其他视频
    </p>
  </section>
</template>

<script setup lang="ts">
import type { RelationOverview } from '@/types/videohub'
defineProps<{ overview: RelationOverview }>()
</script>

<style scoped>
.relation-overview { display: flex; align-items: center; gap: calc(var(--td-comp-margin-s) * 1.5); padding: calc(var(--td-comp-margin-s) * 2); border: 1px solid color-mix(in srgb, var(--td-brand-color) 20%, transparent); border-radius: var(--td-radius-extraLarge); background: var(--td-brand-color-light); color: var(--td-text-color-primary); }
.relation-overview p { margin: 0; font-size: var(--td-font-size-body-medium); line-height: 1.65; }
.relation-overview strong { color: var(--td-brand-color); font-weight: 700; }
</style>
