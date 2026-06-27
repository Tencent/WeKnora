<template>
  <IntegrationLandingLayout
    :title="$t('integrations.claw.title')"
    :subtitle="$t('integrations.claw.subtitle')"
    variant="claw"
  >
    <template #icon>
      <span role="img" :aria-label="$t('common.clawhubSkill')">🦞</span>
    </template>

    <template #actions>
      <IntegrationExternalCta
        variant="claw"
        :label="$t('integrations.claw.installCta')"
        :hint="$t('integrations.claw.installCtaHint')"
        @click="openClawHub"
      >
        <template #icon>
          <span class="ext-cta-emoji" role="img" :aria-label="$t('common.clawhubSkill')">🦞</span>
        </template>
      </IntegrationExternalCta>
    </template>

    <template #main>
      <div class="landing-group">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">
            {{ $t('integrations.claw.capabilitiesTitle') }}
            <span class="section-head-extra">{{ capabilityKeys.length }}</span>
          </h4>
          <div class="capability-grid capability-grid--claw">
            <div v-for="key in capabilityKeys" :key="key" class="capability-card">
              <div class="capability-card__icon">
                <t-icon :name="capabilityIcons[key]" />
              </div>
              <h5 class="capability-card__title">{{ $t(`integrations.claw.capabilities.${key}.title`) }}</h5>
              <p class="capability-card__desc">{{ $t(`integrations.claw.capabilities.${key}.desc`) }}</p>
            </div>
          </div>
        </section>
      </div>
    </template>

    <template #aside>
      <div class="landing-group">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('integrations.claw.stepsTitle') }}</h4>
          <ol class="landing-steps">
            <li v-for="(step, index) in stepKeys" :key="step" class="landing-step">
              <span class="landing-step-num">{{ index + 1 }}</span>
              <div class="landing-step-body">
                <div class="landing-step-title">{{ $t(`integrations.claw.steps.${step}.title`) }}</div>
                <p class="landing-step-desc">{{ $t(`integrations.claw.steps.${step}.desc`) }}</p>
                <t-button
                  v-if="step === 'api'"
                  size="small"
                  variant="outline"
                  class="landing-step-action"
                  @click="openApiSettings"
                >
                  {{ $t('integrations.claw.openApiSettings') }}
                </t-button>
                <div v-if="step === 'env'" class="landing-step-embed">
                  <div class="code-toolbar">
                    <pre class="code-toolbar__code">{{ envExample }}</pre>
                    <t-button
                      class="code-toolbar__copy"
                      size="small"
                      variant="text"
                      shape="square"
                      :title="$t('integrations.claw.copy')"
                      @click="copyEnvExample"
                    >
                      <t-icon name="file-copy" size="16px" />
                    </t-button>
                  </div>
                </div>
                <div v-if="step === 'install'" class="landing-step-embed">
                  <div class="code-toolbar">
                    <pre class="code-toolbar__code">{{ installCommand }}</pre>
                    <t-button
                      class="code-toolbar__copy"
                      size="small"
                      variant="text"
                      shape="square"
                      :title="$t('integrations.claw.copy')"
                      @click="copyInstallCommand"
                    >
                      <t-icon name="file-copy" size="16px" />
                    </t-button>
                  </div>
                </div>
              </div>
            </li>
          </ol>
        </section>
      </div>
    </template>

    <template #footer>
      <div class="landing-footer-bar" role="note">
        <p class="landing-footer-bar__note">{{ $t('integrations.claw.ecosystemNote') }}</p>
        <span class="landing-meta">{{ $t('integrations.claw.hubMeta') }}</span>
      </div>
    </template>
  </IntegrationLandingLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { CLAWHUB_SKILL_URL } from '@/config/integrations'
import { useApiBaseUrlDisplay } from '@/composables/useApiBaseUrlDisplay'
import { useUIStore } from '@/stores/ui'
import IntegrationLandingLayout from './IntegrationLandingLayout.vue'
import IntegrationExternalCta from './IntegrationExternalCta.vue'

const { t } = useI18n()
const router = useRouter()
const uiStore = useUIStore()
const { apiBaseUrlDisplay } = useApiBaseUrlDisplay()

const capabilityKeys = ['upload', 'url', 'manual', 'search', 'browse'] as const
const stepKeys = ['api', 'env', 'install', 'verify'] as const

const capabilityIcons: Record<(typeof capabilityKeys)[number], string> = {
  upload: 'upload',
  url: 'link',
  manual: 'edit',
  search: 'search',
  browse: 'view-list',
}

const installCommand = 'openclaw skills install @lyingbug/weknora'

const envExample = computed(() => {
  const base = apiBaseUrlDisplay.value || 'https://your-server.com/api/v1'
  return `export WEKNORA_BASE_URL="${base}"\nexport WEKNORA_API_KEY="sk-your-api-key"`
})

const openClawHub = () => {
  window.open(CLAWHUB_SKILL_URL, '_blank', 'noopener,noreferrer')
}

const openApiSettings = () => {
  router.push('/platform/knowledge-bases')
  uiStore.openSettings('api')
}

const copyText = async (text: string, successKey: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    MessagePlugin.success(t(successKey))
  } catch {
    MessagePlugin.success(t(successKey))
  }
}

const copyEnvExample = () => copyText(envExample.value, 'integrations.claw.copyEnvSuccess')
const copyInstallCommand = () => copyText(installCommand, 'integrations.claw.copyCmdSuccess')
</script>
