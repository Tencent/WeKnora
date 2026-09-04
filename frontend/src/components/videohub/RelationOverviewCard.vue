<template>
  <section class="relation-overview">
    <span class="relation-overview__icon" aria-hidden="true"><t-icon name="relation" /></span>
    <div class="relation-overview__metrics">
      <div class="relation-overview__metric">
        <strong>{{ overview.relation_count }}</strong>
        <span>个关联知识</span>
      </div>
      <span class="relation-overview__divider" aria-hidden="true"></span>
      <div class="relation-overview__metric">
        <strong>{{ overview.related_video_count }}</strong>
        <span>个关联视频</span>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { RelationOverview } from '@/types/videohub'
defineProps<{ overview: RelationOverview }>()
</script>

<style scoped>
.relation-overview { display: flex; align-items: center; gap: 12px; padding: 12px 14px; border: 1px solid color-mix(in srgb, var(--td-brand-color) 14%, transparent); border-radius: var(--td-radius-large); background: color-mix(in srgb, var(--td-brand-color-light) 38%, transparent); color: var(--td-text-color-secondary); }
.relation-overview__icon { display: grid; flex: none; width: 28px; height: 28px; place-items: center; border: 1px solid color-mix(in srgb, var(--td-brand-color) 16%, transparent); border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-bg-color-container) 64%, transparent); color: var(--td-brand-color); font-size: 15px; }
.relation-overview__metrics { display: flex; align-items: center; gap: 14px; min-width: 0; }
.relation-overview__metric { display: flex; align-items: baseline; gap: 5px; min-width: 0; }
.relation-overview__metric strong { color: var(--td-brand-color); font-size: 18px; font-weight: 600; line-height: 1.2; }
.relation-overview__metric span { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); white-space: nowrap; }
.relation-overview__divider { width: 1px; height: 18px; background: color-mix(in srgb, var(--td-component-stroke) 86%, transparent); }
@media (max-width: 420px) { .relation-overview { align-items: flex-start; }.relation-overview__metrics { display: grid; gap: 6px; }.relation-overview__divider { display: none; } }
</style>
