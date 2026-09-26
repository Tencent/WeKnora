<template>
  <ul v-if="lines.length" class="plugin-caps">
    <li v-for="c in lines" :key="c.id">
      <span class="plugin-caps__icon"><t-icon :name="POINT_ICON[c.point]" /></span>
      <div class="plugin-caps__text">
        <div class="plugin-caps__name">{{ c.name }}</div>
        <div class="plugin-caps__sub">
          {{ t(`pluginCenter.points.${c.point}`) }}
          <template v-if="show === 'where'"> · {{ t(`pluginCenter.where.${c.point}`) }}</template>
          <template v-else-if="c.detail"> · <code>{{ c.detail }}</code></template>
        </div>
      </div>
    </li>
  </ul>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { PluginManifest } from '@/api/plugin'
import { POINT_ICON, capabilityLines } from '@/views/settings/pluginCenterState'

// What a plugin adds, one row per contribution. Workspace admins see where to
// find each one ("where"); platform admins and install reviews see the MCP
// URL or package path it comes from ("detail").
const props = withDefaults(defineProps<{ manifest: PluginManifest; show?: 'where' | 'detail' }>(), { show: 'where' })

const { t, locale } = useI18n()
const lines = computed(() => capabilityLines(props.manifest, locale.value))
</script>

<style lang="less" scoped>
.plugin-caps {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);

  li {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;

    & + li {
      border-top: 1px solid var(--td-component-stroke);
    }
  }
}

.plugin-caps__icon {
  flex: none;
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xl);
}

.plugin-caps__text {
  min-width: 0;
}

.plugin-caps__name {
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
}

.plugin-caps__sub {
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);

  code {
    font-family: var(--app-font-family-mono);
  }
}
</style>
