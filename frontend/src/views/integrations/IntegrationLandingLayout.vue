<template>
  <div class="integration-landing" :class="`integration-landing--${variant}`">
    <header class="landing-hero">
      <div class="landing-hero__top">
        <h2 class="landing-hero__title">{{ title }}</h2>
        <div v-if="$slots.actions" class="landing-hero__actions">
          <slot name="actions" />
        </div>
      </div>
      <p v-if="subtitle" class="landing-hero__subtitle">{{ subtitle }}</p>
      <div v-if="$slots.tags" class="landing-hero__tags">
        <slot name="tags" />
      </div>
    </header>

    <div class="landing-body">
      <section class="landing-main">
        <slot name="main" />
      </section>
      <section class="landing-aside">
        <slot name="aside" />
      </section>
    </div>

    <footer v-if="$slots.footer" class="landing-footer">
      <slot name="footer" />
    </footer>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    variant?: 'chrome' | 'claw' | 'cli'
  }>(),
  { variant: 'chrome' },
)
</script>

<style lang="less">
@import './integration-landing.less';
</style>
