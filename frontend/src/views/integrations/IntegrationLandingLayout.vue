<template>
  <div class="integration-landing" :class="{ 'integration-landing--claw': variant === 'claw' }">
    <header class="landing-hero" :class="{ 'landing-hero--claw': variant === 'claw' }">
      <div class="landing-hero__icon" :class="`landing-hero__icon--${variant}`">
        <slot name="icon" />
      </div>
      <div class="landing-hero__content">
        <h2 class="landing-hero__title">{{ title }}</h2>
        <p v-if="subtitle" class="landing-hero__subtitle">{{ subtitle }}</p>
        <div v-if="$slots.tags" class="landing-hero__tags">
          <slot name="tags" />
        </div>
        <div v-if="$slots.actions" class="landing-hero__actions">
          <slot name="actions" />
        </div>
      </div>
    </header>

    <div class="landing-body">
      <section class="landing-main">
        <slot name="main" />
      </section>
      <aside class="landing-aside">
        <slot name="aside" />
      </aside>
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
    variant?: 'chrome' | 'claw'
  }>(),
  { variant: 'chrome' },
)
</script>

<style lang="less">
@import './integration-landing.less';
</style>
