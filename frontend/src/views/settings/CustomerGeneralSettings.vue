<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useTheme, type ThemeMode } from '@/composables/useTheme'
import BrandThemeToggle from '@/components/customer/BrandThemeToggle.vue'

/**
 * 客户版「常规」设置。
 *
 * 只保留语言和主题两项：字体、自动检查更新属于桌面端/管理员习惯，客户纯问答用不到；
 * 其余 RAG 微调分区由 Settings.vue 的白名单过滤，这里不重复判断（C4：入口裁剪不是权限隔离）。
 *
 * 语言沿用 GeneralSettings.vue 的持久化方式（localStorage 'locale'），主题交给 useTheme，
 * 避免客户版和管理员版各存一套偏好。
 */
const { t, locale } = useI18n()
const { currentTheme, setTheme } = useTheme()

const localLanguage = ref('zh-CN')
const localTheme = ref<ThemeMode>(currentTheme.value)

// 偏好可能在别处被改（如切换用户后重新加载），表单跟着同步。
watch(currentTheme, (val) => {
  localTheme.value = val
})

onMounted(() => {
  const savedLocale = localStorage.getItem('locale')
  if (savedLocale) {
    localLanguage.value = savedLocale
    locale.value = savedLocale
  } else {
    localLanguage.value = locale.value
  }
})

function handleLanguageChange(): void {
  locale.value = localLanguage.value
  localStorage.setItem('locale', localLanguage.value)
  MessagePlugin.success(t('language.languageSaved'))
}

function handleThemeChange(val: ThemeMode): void {
  if (setTheme(val)) return
  // setTheme 会拒绝非法值；回滚表单，避免界面和实际主题不一致。
  localTheme.value = currentTheme.value
}
</script>

<template>
  <div class="customer-general-settings">
    <div class="section-header">
      <h2>{{ $t('brand.settings.title') }}</h2>
      <p class="section-description">{{ $t('brand.settings.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- 语言：6 种与管理员端一致，文案复用 language.* -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('language.language') }}</label>
          <p class="desc">{{ $t('language.languageDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localLanguage"
            :placeholder="$t('language.selectLanguage')"
            style="width: 280px;"
            @change="handleLanguageChange"
          >
            <t-option value="zh-CN" :label="$t('language.zhCN')">{{ $t('language.zhCN') }}</t-option>
            <t-option value="zh-TW" :label="$t('language.zhTW')">{{ $t('language.zhTW') }}</t-option>
            <t-option value="en-US" :label="$t('language.enUS')">{{ $t('language.enUS') }}</t-option>
            <t-option value="ru-RU" :label="$t('language.ruRU')">{{ $t('language.ruRU') }}</t-option>
            <t-option value="ko-KR" :label="$t('language.koKR')">{{ $t('language.koKR') }}</t-option>
            <t-option value="ja-JP" :label="$t('language.jaJP')">{{ $t('language.jaJP') }}</t-option>
          </t-select>
        </div>
      </div>

      <!-- 主题：三态与 useTheme 一致，文案复用 theme.* -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('theme.theme') }}</label>
          <p class="desc">{{ $t('theme.themeDescription') }}</p>
        </div>
        <div class="setting-control">
          <BrandThemeToggle v-model="localTheme" @change="handleThemeChange" />
        </div>
      </div>
    </div>
  </div>
</template>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.customer-general-settings {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  .setting-row();
}

.setting-info {
  .setting-info();
}

.setting-control {
  .setting-control();
}
</style>
