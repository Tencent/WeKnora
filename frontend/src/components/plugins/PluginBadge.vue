<template>
  <span
    class="plugin-badge"
    :class="[`plugin-badge--${size}`, { 'plugin-badge--logo': logo?.mode === 'color', 'plugin-badge--mono': logo?.mode === 'mono' }]"
    :style="logo?.mode === 'mono' ? { '--logo-url': `url(${logo.url})` } : undefined"
    aria-hidden="true"
  >
    <img v-if="logo?.mode === 'color'" :src="logo.url" alt="" class="plugin-badge__img" />
    <t-icon v-else-if="!logo" name="extension" class="plugin-badge__icon" />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { PluginManifest } from '@/api/plugin'
import { pluginLogo } from '@/extensions/pluginIcon'

// A plugin's badge: its own icon, a builtin's brand logo, or the generic
// plugin glyph. Sizes follow the provider cards (36), compact tiles (28) and
// table rows (32).
const props = withDefaults(
  defineProps<{
    manifest?: Pick<PluginManifest, 'iconData' | 'builtin' | 'contributes'>
    size?: 'lg' | 'md' | 'sm'
  }>(),
  { manifest: undefined, size: 'lg' },
)

const logo = computed(() => pluginLogo(props.manifest))
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/provider-card.less';

.plugin-badge {
  .provider-card-badge();
  margin-top: 0;

  &--md {
    width: 32px;
    height: 32px;
    border-radius: var(--app-radius-md);
  }

  &--sm {
    width: 28px;
    height: 28px;
    border-radius: 7px;
  }
}

.plugin-badge__img {
  .provider-card-badge-img();

  .plugin-badge--sm & {
    width: 18px;
    height: 18px;
  }
}

.plugin-badge__icon {
  font-size: var(--app-text-3xl);

  .plugin-badge--sm & {
    font-size: var(--app-text-xl);
  }
}

.plugin-badge--sm.plugin-badge--mono::before {
  width: 16px;
  height: 16px;
}
</style>
