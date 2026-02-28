<template>
  <div class="general-settings">
    <div class="section-header">
      <h2>{{ $t('general.title') }}</h2>
      <p class="section-description">{{ $t('general.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- 语言选择 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('language.language') }}</label>
          <p class="desc">{{ $t('language.languageDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localLanguage"
            :placeholder="$t('language.selectLanguage')"
            @change="handleLanguageChange"
            style="width: 280px;"
          >
            <t-option value="zh-CN" :label="$t('language.zhCN')">{{ $t('language.zhCN') }}</t-option>
            <t-option value="en-US" :label="$t('language.enUS')">{{ $t('language.enUS') }}</t-option>
            <t-option value="ru-RU" :label="$t('language.ruRU')">{{ $t('language.ruRU') }}</t-option>
            <t-option value="ko-KR" :label="$t('language.koKR')">{{ $t('language.koKR') }}</t-option>
          </t-select>
        </div>
      </div>

      <!-- 记忆功能开关 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('settings.enableMemory') }}</label>
          <p class="desc">{{ $t('settings.enableMemoryDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="isMemoryEnabled"
            :disabled="!isNeo4jAvailable"
            @change="handleMemoryChange"
          />
        </div>
      </div>
      <t-alert
        v-if="!isNeo4jAvailable"
        theme="warning"
        style="margin-top: -8px; margin-bottom: 16px;"
      >
        <template #message>
          <div>{{ $t('settings.memoryRequiresNeo4j') }}</div>
          <t-link theme="primary" href="https://github.com/Tencent/WeKnora/blob/main/docs/KnowledgeGraph.md" target="_blank">
            {{ $t('settings.memoryHowToEnable') }}
          </t-link>
        </template>
      </t-alert>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useSettingsStore } from '@/stores/settings'
import { getSystemInfo } from '@/api/system'

const { t, locale } = useI18n()
const settingsStore = useSettingsStore()

// 本地状态
const localLanguage = ref('zh-CN')
const localTheme = ref('light')

// 系统信息
const systemInfo = ref<any>(null)

const isNeo4jAvailable = computed(() => {
  return systemInfo.value?.graph_database_engine && systemInfo.value.graph_database_engine !== '未启用'
})

// 记忆功能状态
const isMemoryEnabled = computed({
  get: () => settingsStore.isMemoryEnabled,
  set: (val) => settingsStore.toggleMemory(val)
})

// 初始化加载
onMounted(async () => {
  // 从 localStorage 加载语言设置
  const savedLocale = localStorage.getItem('locale')
  if (savedLocale) {
    localLanguage.value = savedLocale
    locale.value = savedLocale
  } else {
    localLanguage.value = locale.value
  }

  // 加载系统信息以检查 Neo4j 可用性
  try {
    const response = await getSystemInfo()
    systemInfo.value = response.data
    if (!isNeo4jAvailable.value && settingsStore.isMemoryEnabled) {
      settingsStore.toggleMemory(false)
    }
  } catch (error) {
    console.error('Failed to load system info:', error)
  }
})

// 处理语言变化
const handleLanguageChange = () => {
  locale.value = localLanguage.value
  localStorage.setItem('locale', localLanguage.value)
  MessagePlugin.success(t('language.languageSaved'))
    }

// 处理记忆功能变化
const handleMemoryChange = (val: boolean) => {
  if (val && !isNeo4jAvailable.value) {
    MessagePlugin.warning(t('settings.memoryRequiresNeo4j'))
    settingsStore.toggleMemory(false)
    return
  }
  settingsStore.toggleMemory(val)
  MessagePlugin.success(t('common.success'))
}

// 处理主题变化
const handleThemeChange = () => {
  const settings = {
    language: localLanguage.value,
    theme: localTheme.value
  }
  localStorage.setItem('WeKnora_general_settings', JSON.stringify(settings))
  MessagePlugin.success(t('common.success'))
}
</script>

<style lang="less" scoped>
.general-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #333333;
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid #e5e7eb;

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: #333333;
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}
</style>