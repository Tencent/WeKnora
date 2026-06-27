<template>
  <div class="embed-panel">
    <div class="channels-section">
      <div class="channels-header">
        <span class="channels-title">{{ $t('embedPublish.channelsTitle') }}</span>
        <IntegrationsAgentFilter v-model="filterAgentId" :agents="agents" />
        <span class="channels-count">{{ channels.length }}</span>
      </div>

      <t-loading :loading="loading" size="small" class="channels-loading-wrap">
        <div v-if="!loading && channels.length === 0 && !authStore.hasRole('admin')" class="channels-empty">
          <t-empty :description="$t('embedPublish.empty')" />
        </div>

        <div v-else-if="!loading" class="channel-grid">
          <button v-for="ch in channels" :key="ch.id" type="button" class="channel-card channel-card--clickable"
            @click="openDrawer(ch)">
            <div class="channel-card__badge">
              <t-icon name="code" size="22px" />
            </div>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <h3 class="channel-card__title">{{ channelDisplayName(ch) }}</h3>
                <t-tag v-if="!ch.enabled" size="small" variant="light" theme="warning">
                  {{ $t('embedPublish.disabled') }}
                </t-tag>
              </div>
              <span v-if="agentDisplayName(ch)" class="channel-card__agent-name">
                {{ agentDisplayName(ch) }}
              </span>
            </div>
            <div v-if="authStore.hasRole('admin')" class="channel-card__actions" @click.stop>
              <t-dropdown trigger="click" placement="bottom-right" attach="body" :options="channelMenuOptions(ch)"
                @click="(data) => handleChannelMenuClick(data, ch)">
                <t-button variant="text" shape="square" size="small" class="channel-card__action-btn channel-card__more"
                  @click.stop>
                  <template #icon><t-icon name="ellipsis" /></template>
                </t-button>
              </t-dropdown>
              <t-popconfirm :content="$t('embedPublish.deleteConfirm')"
                :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }" placement="bottom-right"
                @confirm="() => removeChannel(ch.id)">
                <t-tooltip :content="$t('common.delete')" placement="top">
                  <t-button theme="danger" shape="square" variant="text" size="small"
                    class="channel-card__action-btn channel-card__delete" @click.stop>
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-tooltip>
              </t-popconfirm>
            </div>
          </button>

          <button v-if="authStore.hasRole('admin')" type="button" class="channel-card channel-card--add"
            @click="openCreate">
            <span class="channel-card__badge" aria-hidden="true">
              <t-icon name="add" />
            </span>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <span class="channel-card__title">{{ $t('embedPublish.create') }}</span>
              </div>
            </div>
            <span class="channel-card__actions channel-card__actions--spacer" aria-hidden="true" />
          </button>
        </div>
      </t-loading>
    </div>

    <SettingDrawer v-model:visible="showDrawer" class="embed-channel-drawer" :title="drawerTitle"
      :description="drawerStepDescription" icon="code" storage-key="setting-drawer:embed-channel" width="560px"
      :confirm-loading="saving" :confirm-text="drawerConfirmText" :hide-footer="!isAdmin" @confirm="handleDrawerConfirm"
      @cancel="closeDrawer">
      <template v-if="wizardStep > 0" #footer-left>
        <t-button variant="outline" @click="prevWizardStep">
          {{ $t('datasource.back') }}
        </t-button>
      </template>

      <div class="im-steps">
        <button v-for="(title, i) in stepTitles" :key="i" type="button"
          :class="['im-step', { active: wizardStep === i, done: wizardStep > i }]" @click="goToWizardStep(i)">
          <span class="im-step-num">
            <t-icon v-if="wizardStep > i" name="check" class="im-step-check" />
            <template v-else>{{ i + 1 }}</template>
          </span>
          <span class="im-step-title">{{ title }}</span>
        </button>
      </div>

      <!-- Step 1: Channel -->
      <div v-if="wizardStep === 0" class="im-step-body">
        <section class="setting-drawer__section embed-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('embedPublish.sectionChannel') }}</h4>

          <div class="form-item">
            <label class="form-label required">{{ $t('integrations.boundAgent') }}</label>
            <div class="agent-field-row">
              <t-select v-model="createAgentId" :options="agentOptions" filterable
                :placeholder="$t('integrations.selectAgentPlaceholder')" />
            </div>
          </div>

          <div v-if="editingId && isAdmin" class="setting-row setting-row--last">
            <div class="setting-info">
              <label>{{ $t('embedPublish.enabled') }}</label>
            </div>
            <div class="setting-control">
              <t-switch v-model="editingEnabled" size="small" />
            </div>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.name') }}</label>
            <t-input v-model="form.name" :disabled="!isAdmin" :placeholder="$t('embedPublish.namePlaceholder')"
              @focus="channelNameTouched = true" />
            <p v-if="!editingId" class="form-desc">{{ $t('embedPublish.nameDefaultHint') }}</p>
            <p v-else class="form-desc">{{ $t('embedPublish.nameDesc') }}</p>
          </div>

        </section>
      </div>

      <!-- Step 2: Security -->
      <div v-else-if="wizardStep === 1" class="im-step-body">
        <section class="setting-drawer__section embed-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('embedPublish.sectionSecurity') }}</h4>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.allowedOrigins') }}</label>
            <t-textarea v-model="originsText" :disabled="!isAdmin" :placeholder="$t('embedPublish.originsPlaceholder')"
              :status="originsError ? 'error' : 'default'" :autosize="{ minRows: 2, maxRows: 4 }"
              @change="originsError = ''" />
            <p v-if="originsError" class="form-desc form-desc--error">{{ originsError }}</p>
            <p v-else class="form-desc">{{ $t('embedPublish.originsHint') }}</p>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.rateLimitLabel') }}</label>
            <t-input-number v-model="form.rate_limit_per_minute" :disabled="!isAdmin" :min="1" :max="600" theme="column"
              class="form-number" />
            <p class="form-desc">{{ $t('embedPublish.rateLimitDesc') }}</p>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.rateLimitDayLabel') }}</label>
            <t-input-number v-model="form.rate_limit_per_day" :disabled="!isAdmin" :min="1" :max="1000000"
              theme="column" class="form-number" />
            <p class="form-desc">{{ $t('embedPublish.rateLimitDayDesc') }}</p>
          </div>
        </section>
      </div>

      <!-- Step 3: Capabilities -->
      <div v-else-if="wizardStep === 2" class="im-step-body">
        <section class="setting-drawer__section embed-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('embedPublish.sectionCapabilities') }}</h4>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.welcomeMessage') }}</label>
            <t-textarea v-model="form.welcome_message" :disabled="!isAdmin"
              :placeholder="$t('embedPublish.welcomePlaceholder')" :autosize="{ minRows: 2, maxRows: 4 }" />
            <p class="form-desc">{{ $t('embedPublish.welcomeMessageDesc') }}</p>
          </div>

          <div class="settings-group">
            <div class="setting-row">
              <div class="setting-info">
                <label>{{ $t('embedPublish.showSuggestedQuestions') }}</label>
                <p class="desc">{{ $t('embedPublish.showSuggestedQuestionsDesc') }}</p>
              </div>
              <div class="setting-control">
                <t-switch v-model="form.show_suggested_questions" :disabled="!isAdmin" size="small" />
              </div>
            </div>

            <div class="setting-row">
              <div class="setting-info">
                <label>{{ $t('embedPublish.allowWebSearch') }}</label>
                <p class="desc">{{ $t('embedPublish.allowWebSearchDesc') }}</p>
                <p v-if="form.allow_web_search && !agentWebSearchEnabledEffective" class="desc desc--warn">
                  {{ $t('embedPublish.agentWebSearchDisabledHint') }}
                </p>
              </div>
              <div class="setting-control">
                <t-switch v-model="form.allow_web_search" :disabled="!isAdmin" size="small" />
              </div>
            </div>

            <div class="setting-row">
              <div class="setting-info">
                <label>{{ $t('embedPublish.allowFileUpload') }}</label>
                <p class="desc">{{ $t('embedPublish.allowFileUploadDesc') }}</p>
                <p v-if="form.allow_file_upload && !agentImageUploadEnabledEffective" class="desc desc--warn">
                  {{ $t('embedPublish.agentImageUploadDisabledHint') }}
                </p>
              </div>
              <div class="setting-control">
                <t-switch v-model="form.allow_file_upload" :disabled="!isAdmin" size="small" />
              </div>
            </div>
          </div>
        </section>
      </div>

      <!-- Step 4: Appearance -->
      <div v-else-if="wizardStep === 3" class="im-step-body">
        <section class="setting-drawer__section embed-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('embedPublish.sectionAppearance') }}</h4>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.pageTitle') }}</label>
            <t-input v-model="form.page_title" :disabled="!isAdmin"
              :placeholder="$t('embedPublish.pageTitlePlaceholder')" />
            <p class="form-desc">{{ $t('embedPublish.pageTitleDesc') }}</p>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.headerTitleMode') }}</label>
            <t-select v-model="form.header_title_mode" :disabled="!isAdmin" :options="headerTitleModeOptions" />
            <p class="form-desc">{{ $t('embedPublish.headerTitleModeDesc') }}</p>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.widgetPosition') }}</label>
            <t-select v-model="form.widget_position" :disabled="!isAdmin" :options="positionOptions" />
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.defaultLocale') }}</label>
            <t-select v-model="form.default_locale" :disabled="!isAdmin" :options="defaultLocaleOptions" />
            <p class="form-desc">{{ $t('embedPublish.defaultLocaleDesc') }}</p>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.primaryColor') }}</label>
            <t-color-picker v-model="form.primary_color" :disabled="!isAdmin" format="HEX"
              :color-modes="['monochrome']" />
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('embedPublish.widgetPreview') }}</label>
            <div class="widget-preview" :class="`pos-${form.widget_position}`">
              <div class="preview-surface">
                <button type="button" class="preview-launcher"
                  :style="{ background: form.primary_color || defaultPrimaryColor }" aria-hidden="true">
                  <t-icon name="chat" />
                </button>
              </div>
            </div>
          </div>
        </section>
      </div>

      <!-- Step 5: Webhook -->
      <div v-else-if="wizardStep === 4" class="im-step-body">
        <section class="setting-drawer__section embed-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('embedPublish.sectionWebhook') }}</h4>

          <div class="settings-group">
            <div class="settings-group__field">
              <label class="form-label">{{ $t('embedPublish.webhookUrl') }}</label>
              <t-input v-model="form.webhook_url" :disabled="!isAdmin" autocomplete="off"
                :placeholder="$t('embedPublish.webhookUrlPlaceholder')" />
              <p class="form-desc">{{ $t('embedPublish.webhookUrlDesc') }}</p>
            </div>

            <div class="settings-group__field">
              <label class="form-label">{{ $t('embedPublish.webhookSecret') }}</label>
              <t-input v-model="form.webhook_secret" :disabled="!isAdmin" type="password" autocomplete="new-password"
                :placeholder="webhookSecretPlaceholder" />
              <p class="form-desc">{{ $t('embedPublish.webhookSecretDesc') }}</p>
            </div>
          </div>
        </section>

        <div v-if="!editingId" class="deploy-hint" role="note">
          <t-icon name="info-circle" class="deploy-hint__icon" />
          <p>{{ $t('embedPublish.deployAfterSaveHint') }}</p>
        </div>
      </div>

      <!-- Step 6: Deploy (edit only) -->
      <div v-else-if="editingId" class="im-step-body">
        <section class="setting-drawer__section embed-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('embedPublish.sectionDeploy') }}</h4>
          <p class="form-desc form-desc--block">{{ $t('embedPublish.deployIntro') }}</p>

          <div class="deploy-block">
            <h5 class="deploy-block__title">{{ $t('embedPublish.deployStepEmbed') }}</h5>
            <p class="deploy-block__desc">{{ $t('embedPublish.deployStepEmbedDesc') }}</p>

            <t-tabs v-model="drawerSnippetTab" class="snippet-tabs">
              <t-tab-panel value="iframe" :label="$t('embedPublish.tabIframe')" />
              <t-tab-panel value="widget" :label="$t('embedPublish.tabWidget')" />
              <t-tab-panel value="secure" :label="$t('embedPublish.tabSecure')" />
            </t-tabs>
            <p class="snippet-scenario">{{ snippetScenarioHint }}</p>
            <p v-if="drawerSnippetTab === 'widget'" class="snippet-note">
              {{ $t('embedPublish.widgetTokenNote') }}
            </p>
            <p v-else-if="drawerSnippetTab === 'secure'" class="snippet-note snippet-note--ok">
              {{ $t('embedPublish.secureTokenNote') }}
            </p>

            <div v-if="drawerSnippetTab !== 'secure'" class="embed-token-warning" role="note">
              <t-icon name="error-circle" class="embed-token-warning__icon" />
              <p>{{ $t('embedPublish.publishTokenWarning') }}</p>
            </div>

            <div class="code-panel">
              <div class="code-panel__toolbar">
                <span class="code-panel__label">
                  {{ drawerSnippetTab === 'iframe' ? $t('embedPublish.embedCode') : $t('embedPublish.widgetCode') }}
                </span>
                <div class="code-panel__actions">
                  <t-button v-if="drawerSnippetTab !== 'secure'" size="small" variant="text" :loading="previewLoading"
                    @click="openPreviewFromDrawer">
                    <template #icon><t-icon name="browse" /></template>
                    {{ $t('embedPublish.preview') }}
                  </t-button>
                  <t-button size="small" variant="outline" @click="copyDrawerSnippet">
                    <template #icon><t-icon name="file-copy" /></template>
                    {{ $t('embedPublish.copyCode') }}
                  </t-button>
                </div>
              </div>
              <pre class="code-panel__pre">{{ drawerSnippet }}</pre>
            </div>

            <template v-if="drawerSnippetTab === 'secure'">
              <p class="snippet-scenario">{{ $t('embedPublish.secureServerLabel') }}</p>
              <t-tabs v-model="secureServerLangTab" class="snippet-tabs server-lang-tabs">
                <t-tab-panel value="node" :label="$t('embedPublish.tabServerNode')" />
                <t-tab-panel value="go" :label="$t('embedPublish.tabServerGo')" />
              </t-tabs>
              <div class="code-panel">
                <div class="code-panel__toolbar">
                  <span class="code-panel__label">{{ secureServerCodeLabel }}</span>
                  <div class="code-panel__actions">
                    <t-button size="small" variant="outline" @click="copySecureServerExample">
                      <template #icon><t-icon name="file-copy" /></template>
                      {{ $t('embedPublish.copyCode') }}
                    </t-button>
                  </div>
                </div>
                <pre class="code-panel__pre">{{ secureServerExample }}</pre>
              </div>
            </template>
          </div>

          <div class="deploy-block">
            <h5 class="deploy-block__title">{{ $t('embedPublish.channelKey') }}</h5>
            <p class="deploy-block__desc">{{ $t('embedPublish.channelKeyDesc') }}</p>

            <div v-if="drawerChannel" class="channel-key-control">
              <t-input :model-value="displayChannelKey(editingId)" readonly type="text"
                class="mono-text-input channel-key-input"
                :placeholder="tokenFor(drawerChannel) ? '' : $t('embedPublish.channelKeyUnavailable')" />
              <template v-if="tokenFor(drawerChannel)">
                <t-button size="small" variant="text"
                  :title="revealedTokens[editingId] ? $t('embedPublish.hideKey') : $t('embedPublish.revealKey')"
                  @click="toggleReveal(editingId)">
                  <t-icon :name="revealedTokens[editingId] ? 'browse-off' : 'browse'" />
                </t-button>
                <t-button size="small" variant="text" :title="$t('embedPublish.copyChannelKeyTitle')"
                  @click="copyToken(drawerChannel)">
                  <t-icon name="file-copy" />
                </t-button>
              </template>
              <t-popconfirm v-if="isAdmin" theme="warning" :content="$t('embedPublish.resetKeyConfirmBody')"
                :confirm-btn="{ content: $t('embedPublish.resetKeyConfirmOk'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }" @confirm="performRotate(editingId)">
                <t-button size="small" variant="text" theme="danger" :loading="rotating"
                  :title="$t('embedPublish.resetKeyTitle')">
                  <t-icon name="refresh" />
                </t-button>
              </t-popconfirm>
            </div>
            <p v-if="drawerChannel && !tokenFor(drawerChannel)" class="form-desc">
              {{ $t('embedPublish.channelKeyHint') }}
            </p>
          </div>

        </section>
      </div>
    </SettingDrawer>

    <EmbedChannelPreview v-model:visible="previewVisible" :channel-id="previewChannel?.id || ''" :token="previewToken"
      :mode="previewMode" :title="previewChannel?.name || $t('embedPublish.preview')"
      :primary-color="previewChannel?.primary_color" :position="previewPosition" :refresh-key="previewNonce"
      :locale="previewLocale" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import EmbedChannelPreview from '@/components/EmbedChannelPreview.vue'
import {
  listAllEmbedChannels,
  createEmbedChannel,
  getEmbedChannel,
  updateEmbedChannel,
  deleteEmbedChannel,
  rotateEmbedToken,
  issueEmbedPreviewSession,
  buildEmbedSnippet,
  buildWidgetSnippet,
  buildSecureWidgetSnippet,
  buildSecureServerNodeExample,
  buildSecureServerGoExample,
  getEmbedChannelStats,
  clearEmbedStoredChatSession,
  clearEmbedStoredChatSessionIfAgentMismatch,
  type EmbedChannel,
  type EmbedLocaleTag,
  type HeaderTitleMode,
  type WidgetPosition,
} from '@/api/embed'
import {
  parseAllowedOrigins,
  validateAllowedOrigins,
  type AllowedOriginsValidationError,
} from '@/utils/embedAllowedOrigins'
import { listAgents, type CustomAgent } from '@/api/agent'
import IntegrationsAgentFilter from '@/components/IntegrationsAgentFilter.vue'

const filterAgentId = defineModel<string>('filterAgentId', { default: '' })

const { t } = useI18n()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.hasRole('admin'))

const loading = ref(false)
const saving = ref(false)
const allChannels = ref<EmbedChannel[]>([])
const channels = computed(() => {
  const filter = filterAgentId.value?.trim()
  if (!filter) return allChannels.value
  return allChannels.value.filter((ch) => ch.agent_id === filter)
})
const revealedTokens = reactive<Record<string, boolean>>({})
const previewVisible = ref(false)
const previewChannel = ref<EmbedChannel | null>(null)
const previewToken = ref('')
const previewMode = ref<'iframe' | 'widget'>('iframe')
const previewLoading = ref(false)
const previewNonce = ref(0)
const previewLocale = ref('')
const rotating = ref(false)
const showDrawer = ref(false)
const editingId = ref('')
const editingEnabled = ref(true)
const wizardStep = ref(0)
const originsText = ref('')
const originsError = ref('')
const drawerSnippetTab = ref<'iframe' | 'widget' | 'secure'>('iframe')
const secureServerLangTab = ref<'node' | 'go'>('node')
const sessionStats = ref<Record<string, number>>({})
const agents = ref<CustomAgent[]>([])
const createAgentId = ref('')
const channelNameTouched = ref(false)

const agentOptions = computed(() =>
  agents.value.map((agent) => ({ label: agent.name, value: agent.id })),
)

const drawerAgentId = computed(() => {
  if (editingId.value) {
    return drawerChannel.value?.agent_id || ''
  }
  return createAgentId.value
})

const drawerAgent = computed(() =>
  agents.value.find((agent) => agent.id === drawerAgentId.value),
)

const agentWebSearchEnabledEffective = computed(() =>
  drawerAgent.value?.config?.web_search_enabled === true,
)

const agentImageUploadEnabledEffective = computed(() =>
  drawerAgent.value?.config?.image_upload_enabled === true,
)

const WEKNORA_BRAND_COLOR = '#07C05F'

function getDefaultEmbedPrimaryColor(): string {
  if (typeof window === 'undefined') return WEKNORA_BRAND_COLOR
  const css = getComputedStyle(document.documentElement).getPropertyValue('--td-brand-color').trim()
  return css || WEKNORA_BRAND_COLOR
}

const defaultPrimaryColor = getDefaultEmbedPrimaryColor()

const defaultForm = () => ({
  name: '',
  welcome_message: '',
  rate_limit_per_minute: 30,
  rate_limit_per_day: 10000,
  primary_color: getDefaultEmbedPrimaryColor(),
  page_title: '',
  header_title_mode: 'channel' as HeaderTitleMode,
  show_suggested_questions: true,
  widget_position: 'bottom-right' as WidgetPosition,
  allow_web_search: false,
  allow_file_upload: false,
  default_locale: '' as EmbedLocaleTag,
  webhook_url: '',
  webhook_secret: '',
})
const form = ref(defaultForm())
const webhookSecretPlaceholder = computed(() =>
  drawerChannel.value?.has_webhook_secret
    ? t('embedPublish.webhookSecretKeep')
    : t('embedPublish.webhookSecretPlaceholder'))

const positionOptions = computed(() => ([
  { label: t('embedPublish.positionBottomRight'), value: 'bottom-right' },
  { label: t('embedPublish.positionBottomLeft'), value: 'bottom-left' },
  { label: t('embedPublish.positionTopRight'), value: 'top-right' },
  { label: t('embedPublish.positionTopLeft'), value: 'top-left' },
]))

const headerTitleModeOptions = computed(() => ([
  { label: t('embedPublish.headerTitleModeChannel'), value: 'channel' },
  { label: t('embedPublish.headerTitleModeSession'), value: 'session' },
]))

const defaultLocaleOptions = computed(() => ([
  { label: t('embedPublish.defaultLocaleBrowser'), value: '' },
  { label: '简体中文', value: 'zh-CN' },
  { label: 'English', value: 'en-US' },
  { label: '한국어', value: 'ko-KR' },
  { label: 'Русский', value: 'ru-RU' },
]))

const channelMenuOptions = (ch: EmbedChannel) => {
  const items: Array<{ content: string; value: string }> = []
  if (isAdmin.value) {
    items.push({ content: t('embedPublish.preview'), value: 'preview' })
  }
  items.push({
    content: ch.enabled ? t('common.off') : t('common.on'),
    value: 'toggle',
  })
  return items
}

function handleChannelMenuClick(data: { value?: string }, ch: EmbedChannel) {
  if (data.value === 'preview') {
    void openPreviewForChannel(ch)
    return
  }
  if (data.value === 'toggle') {
    void toggleEnabled(ch, !ch.enabled)
  }
}

const drawerTitle = computed(() => {
  if (!editingId.value) return t('embedPublish.createTitle')
  const ch = drawerChannel.value
  const agentId = createAgentId.value || ch?.agent_id || ''
  return channelDisplayName({
    id: editingId.value,
    name: form.value.name,
    agent_id: agentId,
  } as EmbedChannel)
})

function defaultEmbedChannelName(agentId = createAgentId.value): string {
  const agent = agentId ? agents.value.find((item) => item.id === agentId) : undefined
  if (agent?.name?.trim()) {
    return t('embedPublish.defaultChannelNameWithAgent', { agent: agent.name.trim() })
  }
  return t('embedPublish.defaultChannelName')
}

function resolvedEmbedChannelName(): string {
  return form.value.name.trim() || defaultEmbedChannelName()
}

function channelDisplayName(ch: EmbedChannel): string {
  if (ch.name?.trim()) return ch.name.trim()
  return defaultEmbedChannelName(ch.agent_id)
}

function applyDefaultChannelNameIfNeeded() {
  if (!editingId.value && !channelNameTouched.value) {
    form.value.name = defaultEmbedChannelName()
  }
}

const stepTitles = computed(() => {
  const steps = [
    t('embedPublish.stepChannel'),
    t('embedPublish.stepSecurity'),
    t('embedPublish.stepCapabilities'),
    t('embedPublish.stepAppearance'),
    t('embedPublish.stepWebhook'),
  ]
  if (editingId.value) steps.push(t('embedPublish.stepDeploy'))
  return steps
})

const drawerStepDescription = computed(() => stepTitles.value[wizardStep.value] ?? '')

const drawerConfirmText = computed(() =>
  wizardStep.value < stepTitles.value.length - 1 ? t('common.next') : t('common.save'),
)

function validateWizardStep(step: number): boolean {
  if (step === 0 && !createAgentId.value) {
    MessagePlugin.warning(t('integrations.selectAgentHint'))
    return false
  }
  if (step === 1) {
    const originsValidation = validateAllowedOrigins(parseOrigins())
    if (!originsValidation.ok) {
      originsError.value = originsValidationMessage(originsValidation.error)
      MessagePlugin.warning(originsError.value)
      return false
    }
    originsError.value = ''
  }
  return true
}

function prevWizardStep() {
  if (wizardStep.value > 0) wizardStep.value -= 1
}

function goToWizardStep(step: number) {
  if (step < 0 || step >= stepTitles.value.length) return
  wizardStep.value = step
}

function goToDeployStep() {
  if (!editingId.value) return
  wizardStep.value = stepTitles.value.length - 1
}

async function handleDrawerConfirm() {
  if (wizardStep.value < stepTitles.value.length - 1) {
    if (!validateWizardStep(wizardStep.value)) return
    wizardStep.value += 1
    return
  }
  await saveForm()
}

const drawerChannel = computed(() =>
  editingId.value ? channels.value.find((ch) => ch.id === editingId.value) : null)

const previewPosition = computed((): WidgetPosition =>
  (previewChannel.value?.widget_position as WidgetPosition) || 'bottom-right')

function agentDisplayName(ch: EmbedChannel): string {
  return agentForChannel(ch)?.name || ''
}

function agentForChannel(ch: EmbedChannel): CustomAgent | undefined {
  return agents.value.find((agent) => agent.id === ch.agent_id)
}

function mergeChannelDetail(detail: EmbedChannel) {
  const idx = allChannels.value.findIndex((ch) => ch.id === detail.id)
  if (idx >= 0) {
    allChannels.value[idx] = { ...allChannels.value[idx], ...detail }
  }
}

const load = async () => {
  loading.value = true
  try {
    const [res, agentRes] = await Promise.all([
      listAllEmbedChannels(),
      listAgents(),
    ])
    allChannels.value = res?.data || []
    agents.value = agentRes?.data || []
    await Promise.all(allChannels.value.map(async (ch) => {
      try {
        const statsRes = await getEmbedChannelStats(ch.id)
        if (statsRes?.data?.session_count != null) {
          sessionStats.value = { ...sessionStats.value, [ch.id]: statsRes.data.session_count }
        }
      } catch {
        // Stats are best-effort for the channel list subtitle.
      }
    }))
  } finally {
    loading.value = false
  }
}

watch(filterAgentId, (id) => {
  if (!showDrawer.value && !editingId.value && id) {
    createAgentId.value = id
  }
})

onMounted(() => {
  void load()
})

watch(stepTitles, (titles) => {
  if (wizardStep.value >= titles.length) {
    wizardStep.value = Math.max(0, titles.length - 1)
  }
})

watch([createAgentId, () => agents.value.length], () => {
  applyDefaultChannelNameIfNeeded()
})

watch(createAgentId, (agentId, prev) => {
  if (!editingId.value || !agentId || agentId === prev || !previewVisible.value) return
  const ch = drawerChannel.value
  if (!ch) return
  clearEmbedStoredChatSessionIfAgentMismatch(ch.id, agentId)
  previewNonce.value += 1
})

const tokenFor = (ch: EmbedChannel) => ch.publish_token || ''

const displayChannelKey = (channelId: string) => {
  const ch = channels.value.find((c) => c.id === channelId)
  const token = ch ? tokenFor(ch) : ''
  if (!token) return ''
  if (revealedTokens[channelId]) return token
  let masked = ''
  for (let i = 0; i < token.length; i++) masked += '•'
  return masked
}

const toggleReveal = (channelId: string) => {
  revealedTokens[channelId] = !revealedTokens[channelId]
}

const copyToken = async (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) {
    MessagePlugin.warning(t('embedPublish.tokenHint'))
    return
  }
  await navigator.clipboard.writeText(token)
  MessagePlugin.success(t('embedPublish.tokenCopied'))
}

const iframeSnippet = (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  // The bare iframe has no host to hand off a token, so it must embed the
  // publish token in the URL; without a token the snippet cannot work.
  if (!token) return `<!-- ${t('embedPublish.tokenHint')} -->`
  return buildEmbedSnippet(ch.id, token)
}

const widgetSnippet = (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) return `<!-- ${t('embedPublish.tokenHint')} -->`
  const position = (ch.widget_position as WidgetPosition) || 'bottom-right'
  return buildWidgetSnippet(ch.id, token, {
    primaryColor: ch.primary_color,
    title: ch.page_title || ch.name,
    position,
  })
}

const secureSnippet = (ch: EmbedChannel) => {
  const position = (ch.widget_position as WidgetPosition) || 'bottom-right'
  return buildSecureWidgetSnippet(ch.id, {
    primaryColor: ch.primary_color,
    title: ch.page_title || ch.name,
    position,
  })
}

const secureServerExample = computed(() => {
  const ch = drawerChannel.value
  if (!ch) return ''
  return secureServerLangTab.value === 'go'
    ? buildSecureServerGoExample(ch.id)
    : buildSecureServerNodeExample(ch.id)
})

const secureServerCodeLabel = computed(() =>
  secureServerLangTab.value === 'go'
    ? t('embedPublish.tabServerGo')
    : t('embedPublish.tabServerNode'),
)

const drawerSnippet = computed(() => {
  const ch = drawerChannel.value
  if (!ch) return ''
  if (drawerSnippetTab.value === 'secure') return secureSnippet(ch)
  return drawerSnippetTab.value === 'widget' ? widgetSnippet(ch) : iframeSnippet(ch)
})

const snippetScenarioHint = computed(() => {
  if (drawerSnippetTab.value === 'secure') return t('embedPublish.embedSecureDesc')
  return drawerSnippetTab.value === 'widget'
    ? t('embedPublish.embedWidgetDesc')
    : t('embedPublish.embedIframeDesc')
})

const fillFormFromChannel = (ch: EmbedChannel) => {
  editingId.value = ch.id
  editingEnabled.value = ch.enabled
  createAgentId.value = ch.agent_id
  form.value = {
    name: ch.name,
    welcome_message: ch.welcome_message,
    rate_limit_per_minute: ch.rate_limit_per_minute || 30,
    rate_limit_per_day: ch.rate_limit_per_day || 10000,
    primary_color: ch.primary_color || getDefaultEmbedPrimaryColor(),
    page_title: ch.page_title || '',
    header_title_mode: (ch.header_title_mode as HeaderTitleMode) || 'channel',
    show_suggested_questions: ch.show_suggested_questions !== false,
    widget_position: (ch.widget_position as WidgetPosition) || 'bottom-right',
    allow_web_search: ch.allow_web_search === true,
    allow_file_upload: ch.allow_file_upload === true,
    default_locale: (ch.default_locale || '') as EmbedLocaleTag,
    webhook_url: ch.webhook_url || '',
    webhook_secret: '',
  }
  originsText.value = (ch.allowed_origins || []).join('\n')
  originsError.value = ''
  drawerSnippetTab.value = 'iframe'
  secureServerLangTab.value = 'node'
}

const openCreate = () => {
  wizardStep.value = 0
  channelNameTouched.value = false
  editingId.value = ''
  editingEnabled.value = true
  createAgentId.value = filterAgentId.value || ''
  form.value = defaultForm()
  applyDefaultChannelNameIfNeeded()
  originsText.value = ''
  originsError.value = ''
  drawerSnippetTab.value = 'iframe'
  secureServerLangTab.value = 'node'
  showDrawer.value = true
}

const openDrawer = async (ch: EmbedChannel) => {
  channelNameTouched.value = true
  fillFormFromChannel(ch)
  goToDeployStep()
  showDrawer.value = true
  try {
    const res = await getEmbedChannel(ch.id)
    if (res?.data) {
      mergeChannelDetail(res.data)
    }
  } catch {
    MessagePlugin.warning(t('embedPublish.channelKeyLoadFailed'))
  }
}

const closeDrawer = () => {
  showDrawer.value = false
  wizardStep.value = 0
}

const parseOrigins = () => parseAllowedOrigins(originsText.value)

const originsValidationMessage = (error: AllowedOriginsValidationError) => {
  if (error.code === 'required') return t('embedPublish.originsRequired')
  if (error.code === 'wildcard_prod') return t('embedPublish.originsWildcardProd')
  return t('embedPublish.originsInvalid', { origin: error.origin })
}

const mapOriginsApiError = (message: string): string | null => {
  if (message === 'at least one allowed origin is required') {
    return t('embedPublish.originsRequired')
  }
  if (message === "wildcard origin '*' is not allowed in production") {
    return t('embedPublish.originsWildcardProd')
  }
  const invalidMatch = message.match(/^invalid allowed origin: "(.+)"$/)
  if (invalidMatch) {
    return t('embedPublish.originsInvalid', { origin: invalidMatch[1] })
  }
  return null
}

const saveForm = async () => {
  if (!isAdmin.value) return
  if (!createAgentId.value) {
    MessagePlugin.warning(t('integrations.selectAgentHint'))
    return
  }
  const originsValidation = validateAllowedOrigins(parseOrigins())
  if (!originsValidation.ok) {
    originsError.value = originsValidationMessage(originsValidation.error)
    MessagePlugin.warning(originsError.value)
    return
  }
  originsError.value = ''
  saving.value = true
  try {
    const payload = {
      name: resolvedEmbedChannelName(),
      welcome_message: form.value.welcome_message,
      allowed_origins: originsValidation.origins,
      rate_limit_per_minute: form.value.rate_limit_per_minute,
      rate_limit_per_day: form.value.rate_limit_per_day,
      primary_color: form.value.primary_color,
      page_title: form.value.page_title,
      header_title_mode: form.value.header_title_mode,
      show_suggested_questions: form.value.show_suggested_questions,
      widget_position: form.value.widget_position,
      allow_web_search: form.value.allow_web_search,
      allow_file_upload: form.value.allow_file_upload,
      default_locale: form.value.default_locale || '',
      webhook_url: form.value.webhook_url || '',
      webhook_secret: form.value.webhook_secret || undefined,
      enabled: editingId.value ? editingEnabled.value : true,
      agent_id: createAgentId.value,
    }
    if (editingId.value) {
      const previousAgentId = drawerChannel.value?.agent_id
      const res = await updateEmbedChannel(editingId.value, payload)
      MessagePlugin.success(t('embedPublish.updated'))
      const saved = res?.data
      await load()
      const updated = saved ?? channels.value.find((ch) => ch.id === editingId.value)
      if (updated) {
        fillFormFromChannel(updated)
        if (previousAgentId && updated.agent_id !== previousAgentId) {
          clearEmbedStoredChatSession(updated.id)
          if (previewVisible.value) {
            previewChannel.value = updated
            previewNonce.value += 1
          }
        }
      }
    } else {
      const targetAgentId = createAgentId.value
      if (!targetAgentId) {
        MessagePlugin.warning(t('integrations.selectAgentHint'))
        return
      }
      const res = await createEmbedChannel(targetAgentId, payload)
      if (res?.data?.publish_token) {
        revealedTokens[res.data.id] = true
        MessagePlugin.success(t('embedPublish.createdWithToken'))
      } else {
        MessagePlugin.success(t('embedPublish.created'))
      }
      await load()
      if (res?.data?.id) {
        const created = channels.value.find((ch) => ch.id === res.data.id) ?? res.data
        if (created) {
          mergeChannelDetail({ ...created, ...res.data })
          fillFormFromChannel(allChannels.value.find((ch) => ch.id === res.data.id) ?? created)
          wizardStep.value = stepTitles.value.length - 1
        }
      }
    }
  } catch (err: any) {
    const apiMsg = typeof err?.message === 'string' ? err.message : ''
    const originsMsg = apiMsg ? mapOriginsApiError(apiMsg) : null
    if (originsMsg) {
      originsError.value = originsMsg
      MessagePlugin.warning(originsMsg)
      return
    }
    MessagePlugin.error(apiMsg || t('embedPublish.saveFailed'))
  } finally {
    saving.value = false
  }
}

const openPreviewFromDrawer = async () => {
  const ch = drawerChannel.value
  if (!ch) return
  const mode = drawerSnippetTab.value === 'secure' || drawerSnippetTab.value === 'widget'
    ? 'widget'
    : 'iframe'
  await openPreviewForChannel(ch, { useDraft: true, mode })
}

async function openPreviewForChannel(
  ch: EmbedChannel,
  opts?: { useDraft?: boolean; mode?: 'iframe' | 'widget' },
) {
  previewLoading.value = true
  try {
    const agentId = ((opts?.useDraft ? createAgentId.value : '') || ch.agent_id)
    if (agentId) {
      clearEmbedStoredChatSessionIfAgentMismatch(ch.id, agentId)
    }
    let token = tokenFor(ch)
    if (!token) {
      const res = await issueEmbedPreviewSession(ch.id)
      token = res?.data?.session_token || ''
      if (!token) {
        MessagePlugin.warning(t('embedPublish.previewUnavailable'))
        return
      }
    }
    previewMode.value = opts?.mode ?? 'iframe'
    previewChannel.value = {
      ...ch,
      agent_id: agentId || ch.agent_id,
      primary_color: opts?.useDraft ? form.value.primary_color : ch.primary_color,
      widget_position: (opts?.useDraft ? form.value.widget_position : ch.widget_position) as WidgetPosition,
      default_locale: opts?.useDraft ? (form.value.default_locale || ch.default_locale) : ch.default_locale,
    }
    previewToken.value = token
    previewLocale.value = (opts?.useDraft ? form.value.default_locale : '') || ch.default_locale || ''
    previewNonce.value += 1
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur()
    }
    if (previewVisible.value) {
      previewVisible.value = false
      await nextTick()
    }
    previewVisible.value = true
  } catch {
    MessagePlugin.error(t('embedPublish.previewUnavailable'))
  } finally {
    previewLoading.value = false
  }
}

const copySecureServerExample = async () => {
  if (!secureServerExample.value) return
  await navigator.clipboard.writeText(secureServerExample.value)
  MessagePlugin.success(t('embedPublish.copied'))
}

const copyDrawerSnippet = async () => {
  if (!drawerSnippet.value) return
  await navigator.clipboard.writeText(drawerSnippet.value)
  MessagePlugin.success(t('embedPublish.copied'))
}

const performRotate = async (id: string) => {
  rotating.value = true
  try {
    const res = await rotateEmbedToken(id)
    if (res?.data?.publish_token) {
      mergeChannelDetail(res.data)
      revealedTokens[id] = true
      MessagePlugin.success(t('embedPublish.resetKeySuccess'))
    } else {
      MessagePlugin.error(t('embedPublish.resetKeyFailed'))
    }
    await load()
  } catch {
    MessagePlugin.error(t('embedPublish.resetKeyFailed'))
  } finally {
    rotating.value = false
  }
}

const removeChannel = async (id: string) => {
  await deleteEmbedChannel(id)
  if (editingId.value === id) closeDrawer()
  await load()
  MessagePlugin.success(t('embedPublish.deleted'))
}

const toggleEnabled = async (ch: EmbedChannel, enabled: boolean) => {
  await updateEmbedChannel(ch.id, {
    name: ch.name,
    welcome_message: ch.welcome_message,
    allowed_origins: ch.allowed_origins,
    rate_limit_per_minute: ch.rate_limit_per_minute,
    rate_limit_per_day: ch.rate_limit_per_day,
    primary_color: ch.primary_color,
    page_title: ch.page_title,
    header_title_mode: ch.header_title_mode || 'channel',
    show_suggested_questions: ch.show_suggested_questions !== false,
    widget_position: ch.widget_position,
    enabled,
  })
  await load()
}
</script>

<style scoped lang="less">
@import './css/channel-panel-list.less';

.embed-panel {
  display: flex;
  flex-direction: column;
}

.im-steps {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  padding-bottom: 12px;
}

.im-step {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  padding: 0;
  border: none;
  background: transparent;
  font-family: inherit;
  text-align: left;
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-secondary);
  }
}

.im-step-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.im-step.active {
  color: var(--td-brand-color);
  font-weight: 500;
}

.im-step.done {
  color: var(--td-text-color-secondary);
  font-weight: 500;
}

.im-step-num {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 600;
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  background: transparent;
}

.im-step.active .im-step-num {
  background: var(--td-brand-color);
  color: #fff;
  border-color: var(--td-brand-color);
}

.im-step.done .im-step-num {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
  border-color: var(--td-component-stroke);
}

.im-step-check {
  font-size: 12px;
}

.im-step-body {
  display: flex;
  flex-direction: column;
  gap: 0;
}

/* ---------- Drawer form (matches ModelEditorDialog rhythm) ---------- */
:deep(.embed-drawer__section.setting-drawer__section) {
  gap: 10px;
  padding: 10px 0 14px;
}

.form-item {
  margin-bottom: 0;
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  &--inline {
    margin-bottom: 0;
  }
}

.form-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-placeholder);

  &--block {
    margin: -2px 0 0;
    color: var(--td-text-color-secondary);
  }

  &--error {
    color: var(--td-error-color);
  }

  &--warn {
    color: var(--td-warning-color);
  }
}

.form-number {
  width: 100%;
  max-width: 200px;
}

.enable-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 32px;

  .form-desc {
    margin-top: 4px;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin-top: -4px;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
}

.setting-info {
  flex: 1;
  min-width: 0;
  max-width: 72%;
  padding-right: 8px;

  label {
    display: block;
    margin: 0 0 4px;
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    line-height: 1.4;
  }

  .desc {
    margin: 0;
    font-size: 12px;
    line-height: 1.45;
    color: var(--td-text-color-placeholder);

    &--warn {
      margin-top: 4px;
      color: var(--td-warning-color);
    }
  }
}

.setting-control {
  flex-shrink: 0;
  padding-top: 2px;
}

.deploy-hint {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px dashed var(--td-component-stroke);

  &__icon {
    flex-shrink: 0;
    margin-top: 1px;
    color: var(--td-brand-color);
    font-size: 16px;
  }

  p {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.embed-token-warning {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-top: 12px;
  padding: 12px 14px;
  border-radius: 8px;
  background: var(--td-warning-color-1, #fff7e6);
  border: 1px solid var(--td-warning-color-3, #ffd591);

  &__icon {
    flex-shrink: 0;
    margin-top: 1px;
    color: var(--td-warning-color, #e37318);
    font-size: 16px;
  }

  p {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.settings-group__field {
  padding: 12px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }

  .form-label {
    display: block;
    margin-bottom: 6px;
  }

  .form-desc {
    margin-top: 6px;
  }
}

.deploy-block {
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:first-of-type {
    padding-top: 4px;
  }

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }

  &__title {
    margin: 0 0 4px;
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__desc {
    margin: 0 0 12px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.channel-key-control {
  display: flex;
  align-items: center;
  gap: 4px;
}

.channel-key-input {
  flex: 1;
  min-width: 0;
}

.mono-text-input :deep(input) {
  font-family: var(--app-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
}

.snippet-tabs {
  margin-bottom: 4px;

  :deep(.t-tabs__nav) {
    min-height: 36px;
  }

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
    height: 36px;
    line-height: 36px;
  }
}

.server-lang-tabs {
  margin-top: 4px;
}

.snippet-scenario {
  margin: 0 0 8px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-secondary);
}

.snippet-note {
  margin: 0 0 10px;
  padding: 8px 10px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-secondary);
  background: color-mix(in srgb, var(--td-warning-color, #ed7b2f) 8%, var(--td-bg-color-container));
  border: 1px solid color-mix(in srgb, var(--td-warning-color, #ed7b2f) 20%, transparent);
}

.snippet-note--ok {
  background: color-mix(in srgb, var(--td-success-color, #2ba471) 8%, var(--td-bg-color-container));
  border-color: color-mix(in srgb, var(--td-success-color, #2ba471) 20%, transparent);
}

.code-panel {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;

  &__toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
  }

  &__label {
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-secondary);
  }

  &__actions {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
  }

  &__pre {
    margin: 0;
    padding: 10px 12px;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--td-text-color-primary);
    max-height: 180px;
    overflow: auto;
  }
}

.widget-preview {
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;
  padding: 6px;
  background: var(--td-bg-color-secondarycontainer);
  width: 100%;
}

.preview-surface {
  position: relative;
  height: 88px;
  border-radius: 6px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  overflow: hidden;
}

.preview-launcher {
  position: absolute;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  border-radius: 50%;
  color: #fff;
  font-size: 16px;
  line-height: 1;
  box-shadow: 0 3px 10px rgba(0, 0, 0, 0.12);
  cursor: default;

  :deep(.t-icon) {
    display: flex;
    align-items: center;
    justify-content: center;
  }
}

.pos-bottom-right .preview-launcher {
  right: 10px;
  bottom: 10px;
}

.pos-bottom-left .preview-launcher {
  left: 10px;
  bottom: 10px;
}

.pos-top-right .preview-launcher {
  left: auto;
  right: 10px;
  top: 10px;
}

.pos-top-left .preview-launcher {
  left: 10px;
  top: 10px;
}
</style>
