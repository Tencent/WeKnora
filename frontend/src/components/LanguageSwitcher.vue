<template>
  <div class="language-switcher">
    <t-select
      v-model="selectedLanguage"
      :options="languageOptions"
      @change="handleLanguageChange"
      :popup-props="{ overlayClassName: 'language-select-popup' }"
      size="small"
    >
      <template #prefixIcon>
        <t-icon name="translate" />
      </template>
    </t-select>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const { locale } = useI18n()

const languageOptions = [
  { label: 'Русский', value: 'ru-RU' },
  { label: 'English', value: 'en-US' },
  { label: '中文', value: 'zh-CN' }
]

const selectedLanguage = ref(localStorage.getItem('locale') || 'ru-RU')

const handleLanguageChange = (value: string) => {
  console.log('Язык изменен на:', value)
  if (value && ['ru-RU', 'en-US', 'zh-CN'].includes(value)) {
    locale.value = value
    localStorage.setItem('locale', value)
    // Перезагрузка страницы для применения нового языка
    setTimeout(() => {
      window.location.reload()
    }, 100)
  }
}

// Синхронизация с i18n при инициализации
watch(() => locale.value, (newLocale) => {
  if (selectedLanguage.value !== newLocale) {
    selectedLanguage.value = newLocale
  }
}, { immediate: true })
</script>

<style lang="less" scoped>
.language-switcher {
  .t-button {
    color: #666;
    font-size: 14px;

    &:hover {
      color: #333;
      background-color: rgba(0, 0, 0, 0.04);
    }
  }

  .t-icon {
    margin-right: 4px;
  }
}
</style>