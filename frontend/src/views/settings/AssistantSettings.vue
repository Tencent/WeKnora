<template>
  <div class="assistant-settings">
    <div class="section-header">
      <h2>{{ t('assistantSettings.title') }}</h2>
      <p class="section-description">{{ t('assistantSettings.description', { name: assistantName }) }}</p>
    </div>

    <div class="intro">
      <t-icon name="secured" class="intro-icon" />
      <div>
        <p class="intro-title">{{ t('assistantSettings.introTitle') }}</p>
        <p class="intro-desc">{{ t('assistantSettings.introDescription') }}</p>
      </div>
    </div>

    <h3 class="group-title">{{ t('assistantSettings.personalGroup') }}</h3>
    <div class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('assistantSettings.accessLabel') }}</label>
          <p class="desc">{{ t('assistantSettings.accessDescription') }}</p>
          <p class="desc hint">{{ t(`assistant.access.levels.${assistant.effectiveAccessLevel}.desc`) }}</p>
          <p v-if="assistant.accessCeiling !== 'auto_write'" class="desc hint">
            {{ t('assistantSettings.accessCeilingHint', {
              level: t(`assistant.access.levels.${assistant.accessCeiling}.label`),
            }) }}
          </p>
        </div>
        <div class="setting-control">
          <t-radio-group :value="assistant.effectiveAccessLevel" @change="onAccessChange">
            <t-radio-button v-for="level in ASSISTANT_ACCESS_LEVELS" :key="level" :value="level"
              :disabled="!withinCeiling(level)">
              {{ t(`assistant.access.levels.${level}.label`) }}
            </t-radio-button>
          </t-radio-group>
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('assistantSettings.entryLabel') }}</label>
          <p class="desc">{{ t('assistantSettings.entryDescription', { shortcut: `${modKey}K` }) }}</p>
        </div>
        <div class="setting-control">
          <t-radio-group :value="assistant.entry" @change="(value: AssistantEntry) => assistant.setEntry(value)">
            <t-radio-button value="sidebar">{{ t('assistantSettings.entries.sidebar') }}</t-radio-button>
            <t-radio-button value="launcher">{{ t('assistantSettings.entries.launcher') }}</t-radio-button>
            <t-radio-button value="palette">{{ t('assistantSettings.entries.palette') }}</t-radio-button>
          </t-radio-group>
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('assistantSettings.layoutLabel') }}</label>
          <p class="desc">{{ t('assistantSettings.layoutDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-radio-group :value="assistant.layout" @change="(value: AssistantLayout) => assistant.setLayout(value)">
            <t-radio-button value="docked">{{ t('assistantSettings.layouts.docked') }}</t-radio-button>
            <t-radio-button value="floating">{{ t('assistantSettings.layouts.floating') }}</t-radio-button>
          </t-radio-group>
        </div>
      </div>
    </div>

    <h3 class="group-title">
      {{ t('assistantSettings.workspaceGroup') }}
      <span v-if="!canEdit" class="group-note">{{ t('assistantSettings.adminOnly') }}</span>
    </h3>
    <div class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('assistantSettings.nameLabel') }}</label>
          <p class="desc">{{ t('assistantSettings.nameDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-input v-model="draft.display_name" :maxlength="MAX_ASSISTANT_DISPLAY_NAME_LENGTH" show-limit-number
            :placeholder="t('assistant.title')" :disabled="!canEdit" @blur="save" @enter="save" />
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('assistantSettings.ceilingLabel') }}</label>
          <p class="desc">{{ t('assistantSettings.ceilingDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-radio-group v-model="draft.max_access_level" :disabled="!canEdit" @change="save">
            <t-radio-button v-for="level in ASSISTANT_ACCESS_LEVELS" :key="level" :value="level">
              {{ t(`assistant.access.levels.${level}.label`) }}
            </t-radio-button>
          </t-radio-group>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAssistantStore, type AssistantEntry, type AssistantLayout } from '@/stores/assistant'
import {
  ASSISTANT_ACCESS_LEVELS,
  DEFAULT_ASSISTANT_ACCESS_LEVEL,
  MAX_ASSISTANT_DISPLAY_NAME_LENGTH,
  capAssistantAccessLevel,
  isAssistantAccessLevel,
  updateTenantAssistantConfig,
  type AssistantAccessLevel,
  type AssistantConfig,
} from '@/api/assistant'

const { t } = useI18n()
const authStore = useAuthStore()
const assistant = useAssistantStore()
const workspaceOwner = computed(() => `${authStore.user?.id}:${authStore.effectiveTenantId}`)

const canEdit = computed(() => authStore.canAccessAllTenants || authStore.hasRole('admin'))
const assistantName = computed(() => assistant.displayName || t('assistant.title'))
const modKey = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'

const withinCeiling = (level: AssistantAccessLevel) =>
  capAssistantAccessLevel(level, assistant.accessCeiling) === level
const onAccessChange = (value: AssistantAccessLevel) => {
  if (withinCeiling(value)) assistant.setAccessLevel(value)
}

const draft = reactive<AssistantConfig>({ display_name: '', max_access_level: DEFAULT_ASSISTANT_ACCESS_LEVEL })
const syncDraft = () => {
  const config = assistant.workspaceConfig
  const level = config?.max_access_level
  draft.display_name = config?.display_name || ''
  draft.max_access_level = isAssistantAccessLevel(level) ? level : DEFAULT_ASSISTANT_ACCESS_LEVEL
}
watch(() => assistant.workspaceConfig, syncDraft)

const save = async () => {
  if (!canEdit.value) return
  const next: AssistantConfig = { display_name: draft.display_name.trim(), max_access_level: draft.max_access_level }
  const current = assistant.workspaceConfig
  if (current && current.display_name === next.display_name && current.max_access_level === next.max_access_level) return
  const owner = workspaceOwner.value
  try {
    const res = await updateTenantAssistantConfig(next)
    // A save can finish after switching workspace or signing in as another user.
    if (workspaceOwner.value !== owner) return
    assistant.setWorkspaceConfig(res?.data || next)
    MessagePlugin.success(t('assistantSettings.saved'))
  } catch (error: any) {
    if (workspaceOwner.value !== owner) return
    syncDraft()
    MessagePlugin.error(t('assistantSettings.saveFailed', { message: error?.message || '' }))
  }
}

onMounted(() => {
  syncDraft()
  void assistant.loadWorkspaceConfig()
})
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.assistant-settings {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.intro {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 14px 16px;
  margin-bottom: 8px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.intro-icon {
  color: var(--td-brand-color);
  margin-top: 2px;
  flex-shrink: 0;
}

.intro-title {
  margin: 0 0 4px 0;
  font-size: var(--app-text-base);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.intro-desc {
  margin: 0;
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.group-title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin: 24px 0 0;
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.group-note {
  font-size: var(--app-text-sm);
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.setting-row {
  .setting-row();
}

.setting-info {
  .setting-info();

  .hint {
    margin-top: 4px !important;
    color: var(--td-text-color-placeholder);
  }
}

.setting-control {
  .setting-control();
}
</style>
