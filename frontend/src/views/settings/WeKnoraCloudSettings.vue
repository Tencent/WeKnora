<template>
  <div class="weknoracloud-settings">
    <div class="section-header">
      <h2>WeKnora Cloud</h2>
      <p class="section-description">
        配置 WeKnora Cloud 的 APPID 和 APPSECRET 凭证。配置完成后，可在模型管理中添加模型时选择 WeKnoraCloud 作为厂商。
      </p>
      <a
        class="doc-link"
        href="https://developers.weixin.qq.com/doc/aispeech/knowledge/atomic_capability/atomic_interface.html"
        target="_blank"
        rel="noopener noreferrer"
      >
        <t-icon name="link" style="font-size: 14px;" />
        查看文档
      </a>
    </div>

    <!-- 状态区域 -->
    <!-- 未配置 -->
    <div v-if="credentialState === 'unconfigured'" class="credential-status unconfigured">
      <t-icon name="info-circle" style="font-size: 16px; flex-shrink: 0;" />
      <span>尚未配置凭证，请填写 APPID 和 APPSECRET</span>
    </div>

    <!-- 凭证失效 -->
    <div v-else-if="credentialState === 'expired'" class="credential-warning">
      <t-icon name="error-circle" style="font-size: 16px; color: #f97316; flex-shrink: 0; margin-top: 1px;" />
      <div class="warning-text">
        <strong>WeKnora Cloud凭证已失效</strong><br />
        {{ reinitReason || '服务重启后加密密钥已变更，已保存的凭证无法解密。请重新填写凭证。' }}
      </div>
    </div>

    <!-- 已配置正常 -->
    <div v-else-if="credentialState === 'configured'" class="credential-status success">
      <t-icon name="check-circle" style="font-size: 16px; color: var(--td-success-color); flex-shrink: 0;" />
      <span class="status-text">凭证已配置，状态正常</span>
      <t-button
        v-if="!formExpanded"
        variant="text"
        size="small"
        theme="primary"
        @click="formExpanded = true"
      >
        重新配置
      </t-button>
    </div>

    <!-- 配置表单（折叠控制） -->
    <div v-if="formExpanded" class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label class="setting-label">APPID</label>
          <p class="setting-desc">WeKnora Cloud的应用 ID</p>
        </div>
        <div class="setting-control">
          <t-input
            v-model="form.appId"
            placeholder="请输入 APPID"
            autocomplete="off"
            style="width: 280px;"
          />
        </div>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <label class="setting-label">APPSECRET</label>
          <p class="setting-desc">WeKnora Cloud的应用密钥</p>
        </div>
        <div class="setting-control">
          <t-input
            v-model="form.appSecret"
            type="password"
            placeholder="请输入 APPSECRET"
            autocomplete="new-password"
            style="width: 280px;"
          />
        </div>
      </div>

      <div class="setting-row action-row">
        <div class="setting-info">
          <p class="setting-desc">保存后将验证服务可达性并加密存储凭证</p>
        </div>
        <div class="setting-control">
          <t-button
            theme="primary"
            :loading="saving"
            :disabled="!form.appId || !form.appSecret"
            @click="handleSave"
          >
            保存凭证
          </t-button>
        </div>
      </div>
    </div>

    <!-- 使用说明 -->
    <div class="usage-hint">
      <p class="hint-title">使用说明</p>
      <p class="hint-text">
        1. 在此页面填写并保存 APPID 和 APPSECRET<br />
        2. 前往「模型管理」添加模型，选择 Remote API 来源<br />
        3. 在厂商下拉中选择「WeKnoraCloud」<br />
        4. 填写模型名称后保存即可（Base URL 和认证信息会自动配置）
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { saveWeKnoraCloudCredentials, getWeKnoraCloudStatus } from '@/api/model'

const form = ref({ appId: '', appSecret: '' })
const saving = ref(false)
const needsReinit = ref(false)
const reinitReason = ref('')
const hasCredentials = ref(false)
const formExpanded = ref(true)

// 三态：unconfigured / configured / expired
const credentialState = computed(() => {
  if (needsReinit.value) return 'expired'
  if (hasCredentials.value) return 'configured'
  return 'unconfigured'
})

const handleSave = async () => {
  if (!form.value.appId || !form.value.appSecret) {
    MessagePlugin.warning('请填写 APPID 和 APPSECRET')
    return
  }
  saving.value = true
  try {
    await saveWeKnoraCloudCredentials({
      app_id: form.value.appId,
      app_secret: form.value.appSecret,
    })
    MessagePlugin.success('凭证保存成功')
    form.value.appId = ''
    form.value.appSecret = ''
    needsReinit.value = false
    reinitReason.value = ''
    hasCredentials.value = true
    formExpanded.value = false
  } catch (err: any) {
    MessagePlugin.error(err?.message || '凭证保存失败')
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
    // 已配置且未失效时默认收起表单
    if (hasCredentials.value) {
      formExpanded.value = false
    }
  } catch {
    // 静默失败
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

.doc-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--td-brand-color);
  text-decoration: none;
  transition: opacity 0.2s;

  &:hover {
    opacity: 0.8;
    text-decoration: underline;
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
