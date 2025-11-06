<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ConfigProvider } from 'tdesign-vue-next'
import enUS from 'tdesign-vue-next/es/locale/en_US'
import zhCN from 'tdesign-vue-next/es/locale/zh_CN'
import ruRU from 'tdesign-vue-next/es/locale/ru_RU'

const { locale } = useI18n()

const tdesignLocale = computed(() => {
  switch (locale.value) {
    case 'en-US':
      return enUS
    case 'ru-RU':
      return ruRU
    case 'zh-CN':
    default:
      return zhCN
  }
})
</script>
<template>
  <ConfigProvider :global-config="tdesignLocale">
    <div id="app">
      <RouterView />
    </div>
  </ConfigProvider>
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
    background: #f8f9fa;
}
</style>
