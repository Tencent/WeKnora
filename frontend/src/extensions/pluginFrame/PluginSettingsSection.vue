<template>
  <div class="plugin-section">
    <header class="section-header">
      <h2>{{ localizedText(page.name, locale) }}</h2>
      <p v-if="page.description" class="section-description">{{ localizedText(page.description, locale) }}</p>
      <div class="plugin-section__by">
        <PluginPageIcon :url="pluginPages.pageIcon(page)" size="14px" />
        {{ t('pluginPages.fromPlugin', { name: pluginPages.pageProvider(page, locale) }) }}
      </div>
    </header>
    <PluginFrame :page="page" />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { usePluginPagesStore } from '@/stores/pluginPages'
import { localizedText } from '@/utils/localizedText'

import PluginFrame from './PluginFrame.vue'
import PluginPageIcon from './PluginPageIcon.vue'
import type { PluginPage } from './pluginPages'

// A plugin's settings section: the same title block as the built-in
// sections, saying which plugin provides it, above the plugin's own page.
defineProps<{ page: PluginPage }>()

const { t, locale } = useI18n()
const pluginPages = usePluginPagesStore()
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.plugin-section {
  width: 100%;
}

.section-header {
  .settings-section-header();
  margin-bottom: 20px;
}

.plugin-section__by {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}
</style>
