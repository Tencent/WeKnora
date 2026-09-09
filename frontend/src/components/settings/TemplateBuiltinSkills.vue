<template>
  <div v-if="item?.builtin_skills" class="template-builtins">
    <span class="template-builtins__title"><t-icon name="layers" /> {{ $t('skillDiscovery.templateBuiltins') }}</span>
    <span class="template-builtins__version">{{ item?.builtin_skills.version }}</span>
    <div class="template-builtins__chips">
      <span v-for="skill in item?.builtin_skill_names || []" :key="skill" class="template-builtins__chip">{{ skill }}</span>
    </div>
    <p v-if="item?.builtin_skill_names?.length" class="template-row__hint">{{ $t('skillDiscovery.preinstalledReady') }}</p>
    <p v-if="(item?.builtin_skill_names?.length || 0) !== item?.builtin_skills.skills.length" class="template-row__hint">
      {{ $t('skillDiscovery.preinstalledMismatch') }}
    </p>
  </div>
  <p v-else class="template-row__hint">{{ $t('skillDiscovery.preinstalledUnknown') }}</p>
</template>
<script setup lang="ts">
import type { SandboxTemplate } from '@/api/system'
defineProps<{ item?: SandboxTemplate }>()
</script>
<style scoped lang="less">
.template-row__hint { margin: 8px 0 0; font-size: 12px; line-height: 1.6; color: var(--td-text-color-placeholder); }
.template-builtins {
  margin-top: 12px;
  padding: 12px 14px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  border-left: 2px solid #7486d8;
  &__title { display: inline-flex; align-items: center; gap: 6px; font-weight: 500; color: var(--td-text-color-primary); }
  &__version { margin-left: 8px; font-size: 11px; color: var(--td-text-color-placeholder); }
  &__chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 9px; }
  &__chip { padding: 2px 7px; border: 1px solid #7486d840; border-radius: 5px; font-size: 11px; color: var(--td-text-color-secondary); }
}
</style>
