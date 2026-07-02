<template>
  <button
    type="button"
    class="ext-cta"
    :class="`ext-cta--${variant}`"
    @click="emit('click')"
  >
    <span class="ext-cta__badge" aria-hidden="true">
      <slot name="icon" />
    </span>
    <span class="ext-cta__body">
      <span class="ext-cta__label">{{ label }}</span>
      <span v-if="hint" class="ext-cta__hint">{{ hint }}</span>
    </span>
    <span class="ext-cta__arrow-wrap" aria-hidden="true">
      <t-icon name="jump" class="ext-cta__arrow" />
    </span>
  </button>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    hint?: string
    variant?: 'chrome' | 'claw'
  }>(),
  { variant: 'chrome' },
)

const emit = defineEmits<{
  click: []
}>()
</script>
