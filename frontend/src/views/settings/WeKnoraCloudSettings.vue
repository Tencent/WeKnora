<template>
  <div class="weknoracloud-settings">
    <div class="section-header">
      <h2>{{ $t('settings.weknoraCloud.title') }}</h2>
      <p class="section-description">{{ $t('settings.weknoraCloud.description') }}</p>
      <a
        class="doc-link"
        href="https://developers.weixin.qq.com/doc/aispeech/knowledge/atomic_capability/atomic_interface.html"
        target="_blank"
        rel="noopener noreferrer"
      >
        {{ $t('settings.weknoraCloud.viewDocs') }}
        <t-icon name="link" class="link-icon" />
      </a>
    </div>

    <!-- 未配置 -->
    <div v-if="credentialState === 'unconfigured'" class="credential-status unconfigured">
      <t-icon name="info-circle" style="font-size: 16px; flex-shrink: 0;" />
      <span>{{ $t('settings.weknoraCloud.unconfigured') }}</span>
    </div>

    <!-- 凭证失效 -->
    <div v-else-if="credentialState === 'expired'" class="credential-warning">
      <t-icon name="error-circle" style="font-size: 16px; color: #f97316; flex-shrink: 0; margin-top: 1px;" />
      <div class="warning-text">
        <strong>{{ $t('settings.weknoraCloud.expired') }}</strong><br />
        {{ reinitReason || $t('settings.weknoraCloud.expiredDefault') }}
      </div>
    </div>

    <!-- 已配置正常 -->
    <div v-else-if="credentialState === 'configured'" class="credential-status success">
      <t-icon name="check-circle" style="font-size: 16px; color: var(--td-success-color); flex-shrink: 0;" />
      <span class="status-text">{{ $t('settings.weknoraCloud.configured') }}</span>
      <t-button
        v-if="!formExpanded"
        variant="text"
        size="small"
        theme="primary"
        @click="formExpanded = true"
      >
        {{ $t('settings.weknoraCloud.reconfigure') }}
      </t-button>
    </div>

    <!-- 配置表单 -->
    <div v-if="formExpanded" class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label class="setting-label">{{ $t('settings.weknoraCloud.appIdLabel') }}</label>
          <p class="setting-desc">{{ $t('settings.weknoraCloud.appIdDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-input
            v-model="form.appId"
            :placeholder="$t('settings.weknoraCloud.appIdPlaceholder')"
            autocomplete="off"
            style="width: 280px;"
          />
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label class="setting-label">{{ $t('settings.weknoraCloud.appSecretLabel') }}</label>
          <p class="setting-desc">{{ $t('settings.weknoraCloud.appSecretDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-input
            v-model="form.appSecret"
            type="password"
            :placeholder="$t('settings.weknoraCloud.appSecretPlaceholder')"
            autocomplete="new-password"
            style="width: 280px;"
          />
        </div>
      </div>

      <div class="setting-row action-row">
        <div class="setting-info">
          <p class="setting-desc">{{ $t('settings.weknoraCloud.saveHint') }}</p>
        </div>
        <div class="setting-control">
          <t-button
            theme="primary"
            :loading="saving"
            :disabled="!form.appId || !form.appSecret"
            @click="handleSave"
          >
            {{ $t('settings.weknoraCloud.saveBtn') }}
          </t-button>
        </div>
      </div>
    </div>

    <!-- 使用说明 -->
    <div class="usage-hint">
      <p class="hint-title">{{ $t('settings.weknoraCloud.usageTitle') }}</p>
      <p class="hint-text" v-html="$t('settings.weknoraCloud.usageSteps').replace(/\n/g, '<br />')" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { saveWeKnoraCloudCredentials, getWeKnoraCloudStatus } from '@/api/model'

const { t } = useI18n()

const form = ref({ appId: '', appSecret: '' })
const saving = ref(false)
const needsReinit = ref(false)
const reinitReason = ref('')
const hasCredentials = ref(false)
const formExpanded = ref(true)

const credentialState = computed(() => {
  if (needsReinit.value) return 'expired'
  if (hasCredentials.value) return 'configured'
  return 'unconfigured'
})

const handleSave = async () => {
  if (!form.value.appId || !form.value.appSecret) {
    MessagePlugin.warning(t('settings.weknoraCloud.fillRequired'))
    return
  }
  saving.value = true
  try {
    await saveWeKnoraCloudCredentials({
      app_id: form.value.appId,
      app_secret: form.value.appSecret,
    })
    MessagePlugin.success(t('settings.weknoraCloud.saveSuccess'))
    form.value.appId = ''
    form.value.appSecret = ''
    needsReinit.value = false
    reinitReason.value = ''
    hasCredentials.value = true
    formExpanded.value = false
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('settings.weknoraCloud.saveFailed'))
  } finally {
    saving.value = false
  }
}

const checkStatus = async () => {
  try {
    const status = await getWeKnoraCloudStatus()
    needsReinit.value = status.needs_reinit
    reinitReason.value = status.reason || ''
    hasCredentials.value = status.has_models && !status.needs_reinit
    if (hasCredentials.value) {
      formExpanded.value = false
    }
  } catch {
    // silent
  }
}

onMounted(() => {
  checkStatus()
})
</script>

<style lang="less" scoped>
.weknoracloud-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0 0 10px 0;
    line-height: 1.5;
  }
}

.credential-warning {
  margin-bottom: 20px;
  background: #fff7ed;
  border: 1px solid #fed7aa;
  border-left: 3px solid #f97316;
  border-radius: 6px;
  padding: 12px 16px;
  display: flex;
  align-items: flex-start;
  gap: 10px;

  .warning-text {
    font-size: 13px;
    color: #9a3412;
    line-height: 1.5;
  }
}

.credential-status {
  margin-bottom: 20px;
  padding: 10px 14px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;

  &.unconfigured {
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    color: var(--td-text-color-secondary);
  }

  &.success {
    background: var(--td-success-color-light);
    border: 1px solid var(--td-success-color-focus);
    color: var(--td-success-color);
  }

  .status-text {
    flex: 1;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin-bottom: 24px;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }

  &.action-row {
    padding-top: 20px;
  }
}

.setting-info {
  flex: 1;
  min-width: 0;

  .setting-label {
    display: block;
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    margin-bottom: 4px;
  }

  .setting-desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
}

.usage-hint {
  padding: 14px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;

  .hint-title {
    margin: 0 0 8px 0;
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-placeholder);
  }

  .hint-text {
    margin: 0;
    font-size: 13px;
    color: var(--td-text-color-secondary);
    line-height: 1.8;
  }
}
</style>
