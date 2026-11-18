<template>
  <span class="resource-card-avatar" aria-hidden="true">
    <span v-if="emoji" class="avatar-emoji">{{ emoji }}</span>
    <ResourceIcon v-else-if="kind === 'agent'" type="agent" :size="16" />
    <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
        <g class="space-member-left"><circle cx="5" cy="9" r="2.5" /><path d="M1 19v-2a4 4 0 0 1 4-4" /></g>
        <g class="space-member-right"><circle cx="19" cy="9" r="2.5" /><path d="M23 19v-2a4 4 0 0 0-4-4" /></g>
        <g class="space-member-center"><circle cx="12" cy="6" r="3" /><path d="M7 20v-4a5 5 0 0 1 10 0v4" /></g>
    </svg>
  </span>
</template>

<script setup lang="ts">
import ResourceIcon from '@/components/icons/ResourceIcon.vue'

defineProps<{ kind: 'agent' | 'space'; emoji?: string }>()
</script>

<style scoped lang="less">
.resource-card-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 16px;
  width: 16px;
  height: 16px;
  color: var(--td-text-color-secondary);
  transform: var(--resource-avatar-transform, none);
  transition: transform var(--app-motion-slow) cubic-bezier(.2, .8, .2, 1), color var(--app-motion-fast);
  svg { display: block; overflow: visible; flex-shrink: 0; }
}
.avatar-emoji { font-size: var(--app-text-2xl); line-height: 1; }
.space-member-left, .space-member-right, .space-member-center {
  transform-box: fill-box;
  transform-origin: center bottom;
  transition: transform var(--app-motion-slow) cubic-bezier(.34, 1.56, .64, 1);
}
.space-member-left { transform: translateX(var(--resource-member-left, 0px)); }
.space-member-right { transform: translateX(var(--resource-member-right, 0px)); }
.space-member-center { transform: translateY(var(--resource-member-up, 0px)); }
@media (prefers-reduced-motion: reduce) {
  .resource-card-avatar, .space-member-left, .space-member-right, .space-member-center {
    transform: none;
    transition: none;
  }
}
</style>
