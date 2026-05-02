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

      <!-- 主题设置 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('theme.theme') }}</label>
          <p class="desc">{{ $t('theme.themeDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localTheme"
            style="width: 280px;"
            :placeholder="$t('theme.selectTheme')"
            @change="handleThemeChange"
          >
            <t-option value="light" :label="$t('theme.light')">{{ $t('theme.light') }}</t-option>
            <t-option value="dark" :label="$t('theme.dark')">{{ $t('theme.dark') }}</t-option>
            <t-option value="system" :label="$t('theme.system')">{{ $t('theme.system') }}</t-option>
          </t-select>
        </div>
      </div>

      <!-- 界面字体 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('font.uiFont') }}</label>
          <p class="desc">{{ $t('font.uiFontDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localSansFont"
            style="width: 280px;"
            :placeholder="$t('font.selectFont')"
            @change="handleSansFontChange"
          >
            <t-option
              v-for="opt in sansFontOptions"
              :key="opt.value"
              :value="opt.value"
              :label="opt.label"
            >
              <span :style="{ fontFamily: opt.preview }">{{ opt.label }}</span>
            </t-option>
          </t-select>
        </div>
      </div>

      <!-- 等宽字体 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('font.monoFont') }}</label>
          <p class="desc">{{ $t('font.monoFontDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localMonoFont"
            style="width: 280px;"
            :placeholder="$t('font.selectFont')"
            @change="handleMonoFontChange"
          >
            <t-option
              v-for="opt in monoFontOptions"
              :key="opt.value"
              :value="opt.value"
              :label="opt.label"
            >
              <span :style="{ fontFamily: opt.preview }">{{ opt.label }}</span>
            </t-option>
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

      <!-- 自动下载更新开关 (Lite edition only) -->
      <div class="setting-row" v-if="authStore.isLiteMode">
        <div class="setting-info">
          <label>{{ $t('settings.autoCheckUpdate') }}</label>
          <p class="desc">{{ $t('settings.autoCheckUpdateDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="isAutoCheckUpdateEnabled"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useSettingsStore } from '@/stores/settings'
import { useAuthStore } from '@/stores/auth'
import { getSystemInfo } from '@/api/system'
import { useTheme, type ThemeMode } from '@/composables/useTheme'
import {
  useFont,
  SANS_STACKS,
  MONO_STACKS,
  type FontKey,
  type MonoFontKey,
} from '@/composables/useFont'

const { t, locale } = useI18n()
const settingsStore = useSettingsStore()
const authStore = useAuthStore()
const { currentTheme, setTheme } = useTheme()
const { currentSans, currentMono, setSansFont, setMonoFont } = useFont()

// 本地状态
const localLanguage = ref('zh-CN')
const localTheme = ref<ThemeMode>(currentTheme.value)
const localSansFont = ref<FontKey>(currentSans.value)
const localMonoFont = ref<MonoFontKey>(currentMono.value)

const sansFontOptions = computed<{ value: FontKey; label: string; preview: string }[]>(() => [
  { value: 'system', label: t('font.sans.system'), preview: SANS_STACKS.system },
  { value: 'pingfang', label: t('font.sans.pingfang'), preview: SANS_STACKS.pingfang },
  { value: 'inter', label: t('font.sans.inter'), preview: SANS_STACKS.inter },
  { value: 'helvetica', label: t('font.sans.helvetica'), preview: SANS_STACKS.helvetica },
  { value: 'segoe', label: t('font.sans.segoe'), preview: SANS_STACKS.segoe },
  { value: 'roboto', label: t('font.sans.roboto'), preview: SANS_STACKS.roboto },
  { value: 'sans-serif', label: t('font.sans.sansSerif'), preview: SANS_STACKS['sans-serif'] },
])

const monoFontOptions = computed<{ value: MonoFontKey; label: string; preview: string }[]>(() => [
  { value: 'system', label: t('font.mono.system'), preview: MONO_STACKS.system },
  { value: 'cascadia', label: t('font.mono.cascadia'), preview: MONO_STACKS.cascadia },
  { value: 'jetbrains', label: t('font.mono.jetbrains'), preview: MONO_STACKS.jetbrains },
  { value: 'fira', label: t('font.mono.fira'), preview: MONO_STACKS.fira },
  { value: 'monaco', label: t('font.mono.monaco'), preview: MONO_STACKS.monaco },
  { value: 'consolas', label: t('font.mono.consolas'), preview: MONO_STACKS.consolas },
  { value: 'monospace', label: t('font.mono.monospace'), preview: MONO_STACKS.monospace },
])

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

// 自动检查更新状态
const isAutoCheckUpdateEnabled = computed({
  get: () => settingsStore.isAutoCheckUpdateEnabled,
  set: (val) => {
    settingsStore.toggleAutoCheckUpdate(val)
    if (val) {
      // @ts-ignore
      if (window.go && window.go.main && window.go.main.App && window.go.main.App.AutoCheckForUpdates) {
        // @ts-ignore
        window.go.main.App.AutoCheckForUpdates()
      }
    }
  }
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
const handleThemeChange = (val: ThemeMode) => {
  setTheme(val)
  MessagePlugin.success(t('common.success'))
}

// 处理字体变化
const handleSansFontChange = (val: FontKey) => {
  setSansFont(val)
  MessagePlugin.success(t('common.success'))
}

const handleMonoFontChange = (val: MonoFontKey) => {
  setMonoFont(val)
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
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
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
  border-bottom: 1px solid var(--td-component-stroke);

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
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
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