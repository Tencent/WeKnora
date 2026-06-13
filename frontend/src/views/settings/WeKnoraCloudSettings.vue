<template>
  <div class="weknoracloud-settings">
    <div class="section-header">
      <h2>{{ $t('settings.weknoraCloud.title') }}</h2>
      <p class="section-description">
        {{ $t('settings.weknoraCloud.description') }}
        <a
          class="doc-link"
          href="https://developers.weixin.qq.com/doc/aispeech/knowledge/atomic_capability/atomic_interface.html"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ $t('settings.weknoraCloud.viewDocs') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </p>
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
      <t-icon name="check-circle" style="font-size: 16px; color: var(--td-brand-color); flex-shrink: 0;" />
      <span class="status-text">{{ $t('settings.weknoraCloud.configured') }}</span>
      <t-button
        v-if="!formExpanded"
        variant="outline"
        theme="default"
        size="small"
        @click="formExpanded = true"
      >
        <template #icon><t-icon name="edit" /></template>
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

    <!-- 云模型：凭证就绪后原地展示接入状态 -->
    <section
      class="models-section"
      :class="{ 'models-section--disabled': credentialState !== 'configured' }"
    >
      <div class="models-section__header">
        <h3 class="models-section__title">{{ $t('settings.weknoraCloud.modelsSection.title') }}</h3>
        <p class="models-section__desc">
          {{
            credentialState === 'configured'
              ? $t('settings.weknoraCloud.modelsSection.descReady')
              : $t('settings.weknoraCloud.modelsSection.descPending')
          }}
        </p>
      </div>

      <div class="models-list">
        <div
          v-for="kind in WKC_MODEL_KINDS"
          :key="kind"
          class="model-row"
        >
          <div class="model-row__main">
            <span class="model-row__label">{{ kindLabel(kind) }}</span>
            <code class="model-row__id">{{ WKC_MODEL_NAME_BY_KIND[kind] }}</code>
          </div>
          <div class="model-row__action">
            <t-tag
              v-if="credentialState === 'configured' && existingKinds.has(kind)"
              theme="success"
              variant="light"
              size="small"
            >
              {{ $t('settings.weknoraCloud.modelsSection.statusAdded') }}
            </t-tag>
            <t-popconfirm
              v-else-if="credentialState === 'configured'"
              :content="$t('settings.weknoraCloud.modelsSection.confirmAddOne', {
                type: kindLabel(kind),
                name: WKC_MODEL_NAME_BY_KIND[kind],
              })"
              :confirm-btn="{ content: $t('settings.weknoraCloud.modelsSection.addOne'), theme: 'primary' }"
              :cancel-btn="{ content: $t('common.cancel') }"
              placement="left"
              @confirm="addModels([kind])"
            >
              <t-button
                size="small"
                variant="outline"
                theme="primary"
                :loading="addingKind === kind"
                :disabled="addingModels && addingKind !== kind"
                @click.stop
              >
                {{ $t('settings.weknoraCloud.modelsSection.addOne') }}
              </t-button>
            </t-popconfirm>
            <span v-else class="model-row__pending">
              {{ $t('settings.weknoraCloud.modelsSection.statusPending') }}
            </span>
          </div>
        </div>
      </div>

      <div
        v-if="credentialState === 'configured' && missingKinds.length > 1"
        class="models-section__batch"
      >
        <t-popconfirm
          :content="$t('settings.weknoraCloud.modelsSection.confirmAddAll', { count: missingKinds.length })"
          :confirm-btn="{
            content: $t('settings.weknoraCloud.modelsSection.addAllConfirm'),
            theme: 'primary',
          }"
          :cancel-btn="{ content: $t('common.cancel') }"
          placement="top"
          @confirm="addModels([...missingKinds])"
        >
          <t-button
            theme="primary"
            size="small"
            :loading="addingModels && !addingKind"
            :disabled="addingModels && !!addingKind"
          >
            {{ $t('settings.weknoraCloud.modelsSection.addAllBtn', { count: missingKinds.length }) }}
          </t-button>
        </t-popconfirm>
      </div>

      <p
        v-else-if="credentialState === 'configured' && missingKinds.length === 0"
        class="models-section__ready"
      >
        <t-icon name="check-circle-filled" class="models-section__ready-icon" />
        {{ $t('settings.weknoraCloud.modelsSection.allReady') }}
      </p>
    </section>

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
import { saveWeKnoraCloudCredentials, getWeKnoraCloudStatus, createModel, listModels } from '@/api/model'
import { testEmbeddingModel } from '@/api/initialization'
import {
  WKC_MODEL_KINDS,
  WKC_MODEL_NAME_BY_KIND,
  WEKNORA_CLOUD_BASE_URL,
  WEKNORA_CLOUD_PROVIDER,
  buildWkcModelConfig,
  existingWkcKinds,
  type WkcModelKind,
} from '@/utils/weknoraCloudModels'

const { t } = useI18n()

const form = ref({ appId: '', appSecret: '' })
const saving = ref(false)
const needsReinit = ref(false)
const reinitReason = ref('')
const hasCredentials = ref(false)
const formExpanded = ref(true)
const existingKinds = ref<Set<WkcModelKind>>(new Set())
const addingModels = ref(false)
const addingKind = ref<WkcModelKind | null>(null)

const credentialState = computed(() => {
  if (needsReinit.value) return 'expired'
  if (hasCredentials.value) return 'configured'
  return 'unconfigured'
})

const missingKinds = computed(() =>
  WKC_MODEL_KINDS.filter((kind) => !existingKinds.value.has(kind)),
)

const kindLabel = (kind: WkcModelKind) => {
  const key = `modelSettings.typeShort.${kind === 'vllm' ? 'vllm' : kind}` as const
  return t(key)
}

const kindDisplayName = (kind: WkcModelKind) =>
  t(`settings.weknoraCloud.addModelsDisplayName.${kind}`)

const refreshExistingKinds = async () => {
  try {
    const models = await listModels()
    existingKinds.value = existingWkcKinds(models)
  } catch {
    existingKinds.value = new Set()
  }
}

const resolveEmbeddingDimension = async (): Promise<number> => {
  const result = await testEmbeddingModel({
    source: 'remote',
    modelName: WKC_MODEL_NAME_BY_KIND.embedding,
    baseUrl: WEKNORA_CLOUD_BASE_URL,
    provider: WEKNORA_CLOUD_PROVIDER,
  })
  if (!result.available || !result.dimension) {
    throw new Error(result.message || t('settings.weknoraCloud.addModelsEmbeddingFailed'))
  }
  return result.dimension
}

const addModels = async (kinds: WkcModelKind[]) => {
  const targets = kinds.filter((kind) => !existingKinds.value.has(kind))
  if (targets.length === 0) {
    return
  }

  addingModels.value = true
  addingKind.value = targets.length === 1 ? targets[0] : null

  let success = 0
  let failed = 0
  let embeddingDimension: number | undefined

  try {
    for (const kind of targets) {
      try {
        if (kind === 'embedding') {
          embeddingDimension = embeddingDimension ?? await resolveEmbeddingDimension()
        }
        const payload = buildWkcModelConfig(
          kind,
          kindDisplayName(kind),
          kind === 'embedding' ? embeddingDimension : undefined,
        )
        await createModel(payload)
        existingKinds.value = new Set([...existingKinds.value, kind])
        success += 1
      } catch (err: any) {
        console.error(`Failed to create WeKnoraCloud ${kind} model:`, err)
        failed += 1
      }
    }

    if (success > 0 && failed === 0) {
      MessagePlugin.success(t('settings.weknoraCloud.addModelsSuccess', { count: success }))
    } else if (success > 0) {
      MessagePlugin.warning(t('settings.weknoraCloud.addModelsPartial', { success, failed }))
    } else {
      MessagePlugin.error(t('settings.weknoraCloud.addModelsFailed'))
    }
  } finally {
    addingModels.value = false
    addingKind.value = null
  }
}

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
    await refreshExistingKinds()
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
      await refreshExistingKinds()
    }
  } catch {
    // silent
  }
}

onMounted(async () => {
  await checkStatus()
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
    padding: 0;
    background: transparent;
    border: none;
    color: var(--td-text-color-secondary);
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

.models-section {
  margin-bottom: 24px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);

  &--disabled {
    opacity: 0.92;
    background: var(--td-bg-color-secondarycontainer);
  }

  &__header {
    margin-bottom: 14px;
  }

  &__title {
    margin: 0 0 6px;
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__desc {
    margin: 0;
    font-size: 13px;
    line-height: 1.55;
    color: var(--td-text-color-secondary);
  }

  &__batch {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px dashed var(--td-component-stroke);
  }

  &__ready {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 14px 0 0;
    font-size: 13px;
    color: var(--td-success-color, #2ba471);
  }

  &__ready-icon {
    font-size: 15px;
    flex-shrink: 0;
  }
}

.models-list {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.model-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }

  &:first-child {
    padding-top: 4px;
  }

  &__main {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }

  &__label {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    min-width: 88px;
  }

  &__id {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    padding: 2px 6px;
    border-radius: 4px;
  }

  &__action {
    flex-shrink: 0;
  }

  &__pending {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
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
