<template>
  <span class="sandbox-badge" :class="badgeClass" :style="badgeStyle" :aria-hidden="true">
    <t-icon v-if="!logo" :name="iconName" />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { providerLogo } from '@/views/settings/providerLogos'

// The sandbox list and the config drawer must show the same mark for a backend,
// so both render this badge instead of each mapping types to icons themselves.
// Vendors we ship a logo for (Docker) win over the generic TDesign glyphs.
const props = withDefaults(defineProps<{
  type: string
  size?: 'xs' | 'sm' | 'md'
}>(), { size: 'md' })

const logo = computed(() => providerLogo('sandbox', props.type))

const iconName = computed(() => {
  if (props.type === 'cube') return 'server'
  if (props.type === 'disabled') return 'minus-circle'
  return 'cloud'
})

const badgeClass = computed(() => [
  `sandbox-badge--${props.type}`,
  `sandbox-badge--${props.size}`,
  { 'sandbox-badge--mono': logo.value?.mode === 'mono' },
])

const badgeStyle = computed((): Record<string, string> => (
  logo.value?.mode === 'mono' ? { '--logo-url': `url("${logo.value.url}")` } : {}
))
</script>

<style lang="less" scoped>
.sandbox-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border-radius: 9px;
  background: rgba(0, 82, 217, 0.1);
  color: #0052d9;
}

.sandbox-badge--md {
  width: 36px;
  height: 36px;
  font-size: 17px;
}

.sandbox-badge--sm {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  font-size: 14px;
}

.sandbox-badge--xs {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  font-size: 10px;
}

.sandbox-badge--e2b {
  background: rgba(98, 53, 187, 0.1);
  color: #6235bb;
}

.sandbox-badge--docker {
  background: rgba(29, 99, 237, 0.1);
  color: #1d63ed;
}

.sandbox-badge--mono::before {
  content: '';
  background-color: currentColor;
  -webkit-mask-image: var(--logo-url);
  -webkit-mask-position: center;
  -webkit-mask-repeat: no-repeat;
  -webkit-mask-size: contain;
  mask-image: var(--logo-url);
  mask-position: center;
  mask-repeat: no-repeat;
  mask-size: contain;
}

.sandbox-badge--md.sandbox-badge--mono::before {
  width: 22px;
  height: 22px;
}

.sandbox-badge--sm.sandbox-badge--mono::before {
  width: 16px;
  height: 16px;
}

.sandbox-badge--xs.sandbox-badge--mono::before {
  width: 10px;
  height: 10px;
}
</style>
