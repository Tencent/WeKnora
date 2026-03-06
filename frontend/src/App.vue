<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ManualKnowledgeEditor from '@/components/manual-knowledge-editor.vue'

// TDesign locale configs
import enUSConfig from 'tdesign-vue-next/esm/locale/en_US'
import zhCNConfig from 'tdesign-vue-next/esm/locale/zh_CN'
import koKRConfig from 'tdesign-vue-next/esm/locale/ko_KR'
import ruRUConfig from 'tdesign-vue-next/esm/locale/ru_RU'

const { locale } = useI18n()

const tdLocaleMap: Record<string, object> = {
  'en-US': enUSConfig,
  'zh-CN': zhCNConfig,
  'ko-KR': koKRConfig,
  'ru-RU': ruRUConfig,
}

const tdGlobalConfig = computed(() => tdLocaleMap[locale.value] || enUSConfig)
</script>
<template>
  <t-config-provider :globalConfig="tdGlobalConfig">
    <div id="app">
      <RouterView />
      <ManualKnowledgeEditor />
    </div>
  </t-config-provider>
</template>
<style>
body,
html,
#app {
    width: 100%;
    height: 100%;
    margin: 0;
    padding: 0;
    font-size: 14px;
    font-family: Helvetica Neue, Helvetica, PingFang SC, Hiragino Sans GB,
        Microsoft YaHei, SimSun, sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    background: var(--td-bg-color-page);
    color: var(--td-text-color-primary);
}
</style>
