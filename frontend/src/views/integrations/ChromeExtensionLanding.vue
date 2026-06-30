<template>
  <IntegrationLandingLayout
    :title="$t('integrations.chrome.title')"
    :subtitle="$t('integrations.chrome.subtitle')"
    variant="chrome"
  >
    <template #icon>
      <t-icon name="extension" size="22px" />
    </template>

    <template #tags>
      <span v-for="key in scenarioKeys" :key="key" class="scenario-tag">
        {{ $t(`integrations.chrome.scenarios.${key}`) }}
      </span>
    </template>

    <template #actions>
      <IntegrationExternalCta
        variant="chrome"
        :label="$t('integrations.chrome.installCta')"
        :hint="$t('integrations.chrome.installCtaHint')"
        @click="openChromeStore"
      >
        <template #icon>
          <t-icon name="extension" size="18px" />
        </template>
      </IntegrationExternalCta>
    </template>

    <template #main>
      <div class="landing-group">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">
            {{ $t('integrations.chrome.capabilitiesTitle') }}
            <span class="section-head-extra">{{ capabilityKeys.length }}</span>
          </h4>
          <div class="capability-grid">
            <div v-for="key in capabilityKeys" :key="key" class="capability-card">
              <div class="capability-card__icon">
                <t-icon :name="capabilityIcons[key]" />
              </div>
              <h5 class="capability-card__title">{{ $t(`integrations.chrome.capabilities.${key}.title`) }}</h5>
              <p class="capability-card__desc">{{ $t(`integrations.chrome.capabilities.${key}.desc`) }}</p>
            </div>
          </div>
        </section>
      </div>
    </template>

    <template #aside>
      <div class="landing-group">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('integrations.chrome.stepsTitle') }}</h4>
          <ol class="landing-steps">
            <li v-for="(step, index) in stepKeys" :key="step" class="landing-step">
              <span class="landing-step-num">{{ index + 1 }}</span>
              <div class="landing-step-body">
                <div class="landing-step-title">{{ $t(`integrations.chrome.steps.${step}.title`) }}</div>
                <p class="landing-step-desc">{{ $t(`integrations.chrome.steps.${step}.desc`) }}</p>
                <t-button
                  v-if="step === 'api'"
                  size="small"
                  variant="outline"
                  class="landing-step-action"
                  @click="openApiSettings"
                >
                  {{ $t('integrations.chrome.openApiSettings') }}
                </t-button>
                <div v-if="step === 'connect'" class="landing-step-embed credential-row">
                  <div class="api-key-control landing-api-control">
                    <t-input :model-value="apiBaseUrlDisplay" readonly class="mono-text-input" />
                    <t-button
                      size="small"
                      variant="text"
                      :title="$t('integrations.chrome.copy')"
                      @click="copyApiUrl"
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
      <span class="landing-meta">{{ $t('integrations.chrome.storeMeta') }}</span>
    </template>
  </IntegrationLandingLayout>
</template>

<script setup lang="ts">
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { CHROME_EXTENSION_URL } from '@/config/integrations'
import { useApiBaseUrlDisplay } from '@/composables/useApiBaseUrlDisplay'
import { useUIStore } from '@/stores/ui'
import IntegrationLandingLayout from './IntegrationLandingLayout.vue'
import IntegrationExternalCta from './IntegrationExternalCta.vue'

const { t } = useI18n()
const router = useRouter()
const uiStore = useUIStore()
const { apiBaseUrlDisplay } = useApiBaseUrlDisplay()

const capabilityKeys = ['qa', 'clip', 'notes', 'shortcuts'] as const
const scenarioKeys = ['research', 'learning', 'tech', 'work'] as const
const stepKeys = ['api', 'port', 'install', 'connect'] as const

const capabilityIcons: Record<(typeof capabilityKeys)[number], string> = {
  qa: 'chat-bubble',
  clip: 'file-copy',
  notes: 'edit',
  shortcuts: 'jump',
}

const openChromeStore = () => {
  window.open(CHROME_EXTENSION_URL, '_blank', 'noopener,noreferrer')
}

const openApiSettings = () => {
  router.push('/platform/knowledge-bases')
  uiStore.openSettings('api')
}

const copyApiUrl = async () => {
  const text = apiBaseUrlDisplay.value
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    MessagePlugin.success(t('integrations.chrome.copySuccess'))
  } catch {
    MessagePlugin.success(t('integrations.chrome.copySuccess'))
  }
}
</script>
