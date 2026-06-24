<template>
  <div class="im-panel">
    <div class="channels-section">
      <div class="channels-header">
        <span class="channels-title">{{ $t('agentEditor.im.channelsTitle') }}</span>
        <span class="channels-count">{{ channels.length }}</span>
      </div>

      <t-loading :loading="loading" size="small" class="channels-loading-wrap">
        <div v-if="!loading && channels.length === 0 && !authStore.hasRole('admin')" class="channels-empty">
          <t-empty :description="$t('agentEditor.im.empty')" />
        </div>

        <div v-else-if="!loading" class="channel-grid">
          <button
            v-for="channel in channels"
            :key="channel.id"
            type="button"
            class="channel-card channel-card--clickable"
            @click="openDrawer(channel)"
          >
            <div class="channel-card__badge" :class="`channel-card__badge--${channel.platform}`">
              <img
                v-if="platformLogo(channel.platform)"
                :src="platformLogo(channel.platform)"
                :alt="platformLabel(channel.platform)"
                class="channel-card__logo"
              />
              <t-icon v-else name="chat-message" size="18px" />
            </div>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <h3 class="channel-card__title">{{ channel.name || $t('agentEditor.im.unnamed') }}</h3>
                <t-tag v-if="!channel.enabled" size="small" variant="light" theme="warning">
                  {{ $t('agentEditor.im.disabled') }}
                </t-tag>
              </div>
              <span v-if="tenantMode && agentDisplayName(channel)" class="channel-card__agent-name">
                {{ agentDisplayName(channel) }}
              </span>
            </div>
            <div v-if="authStore.hasRole('admin')" class="channel-card__actions" @click.stop>
              <t-dropdown
                trigger="click"
                placement="bottom-right"
                attach="body"
                :options="channelMenuOptions(channel)"
                @click="(data) => handleChannelMenuClick(data, channel)"
              >
                <t-button
                  variant="text"
                  shape="square"
                  size="small"
                  class="channel-card__more"
                  @click.stop
                >
                  <template #icon><t-icon name="ellipsis" /></template>
                </t-button>
              </t-dropdown>
            </div>
          </button>

          <button
            v-if="authStore.hasRole('admin')"
            type="button"
            class="channel-card channel-card--add"
            @click="openCreate"
          >
            <span class="channel-card__badge" aria-hidden="true">
              <t-icon name="add" />
            </span>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <span class="channel-card__title">{{ $t('agentEditor.im.addChannel') }}</span>
              </div>
            </div>
            <span class="channel-card__actions channel-card__actions--spacer" aria-hidden="true" />
          </button>
        </div>
      </t-loading>
    </div>

    <SettingDrawer
      v-model:visible="showCreateDialog"
      class="im-channel-drawer"
      :title="drawerTitle"
      :description="drawerStepDescription"
      storage-key="setting-drawer:im-channel"
      width="560px"
      :confirm-loading="saving"
      :confirm-text="drawerConfirmText"
      :hide-footer="!authStore.hasRole('admin')"
      @confirm="handleDrawerConfirm"
      @cancel="resetForm"
    >
      <template #headerIcon>
        <img
          v-if="platformLogo(formData.platform)"
          :src="platformLogo(formData.platform)"
          :alt="platformLabel(formData.platform)"
          class="drawer-platform-icon"
        />
        <t-icon v-else name="chat-message" />
      </template>

      <template v-if="wizardStep > 0" #footer-left>
        <t-button variant="outline" @click="prevWizardStep">
          {{ $t('datasource.back') }}
        </t-button>
      </template>

      <div class="im-steps">
        <div
          v-for="(title, i) in stepTitles"
          :key="i"
          :class="['im-step', { active: wizardStep === i, done: wizardStep > i }]"
        >
          <span class="im-step-num">
            <t-icon v-if="wizardStep > i" name="check" class="im-step-check" />
            <template v-else>{{ i + 1 }}</template>
          </span>
          <span class="im-step-title">{{ title }}</span>
        </div>
      </div>

      <!-- Step 1: Basic -->
      <div v-if="wizardStep === 0" class="im-step-body">
        <section class="setting-drawer__section im-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('agentEditor.im.sectionChannel') }}</h4>

          <div v-if="tenantMode" class="form-item">
            <label class="form-label required">{{ $t('integrations.boundAgent') }}</label>
            <div class="agent-field-row">
              <t-select
                v-model="formData.target_agent_id"
                :options="agentOptions"
                filterable
                :placeholder="$t('integrations.selectAgentPlaceholder')"
              />
            </div>
          </div>

          <div class="form-item">
            <label class="form-label required">{{ $t('agentEditor.im.platform') }}</label>
            <t-select
              v-model="formData.platform"
              :disabled="!!editingChannel"
              class="im-platform-select"
              @change="onPlatformChange"
            >
              <template v-if="platformLogo(formData.platform)" #prefixIcon>
                <img
                  :src="platformLogo(formData.platform)"
                  :alt="platformLabel(formData.platform)"
                  class="im-platform-select-prefix"
                />
              </template>
              <t-option
                v-for="item in platformOptions"
                :key="item.value"
                :value="item.value"
                :label="item.label"
              >
                <div class="im-platform-select-option">
                  <img :src="item.logo" :alt="item.label" class="im-platform-select-option__icon" />
                  <span>{{ item.label }}</span>
                </div>
              </t-option>
            </t-select>
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('agentEditor.im.channelName') }}</label>
            <t-input
              v-model="formData.name"
              :placeholder="$t('agentEditor.im.channelNamePlaceholder')"
              @focus="channelNameTouched = true"
            />
            <p v-if="!editingChannel" class="form-desc">{{ $t('agentEditor.im.channelNameDefaultHint') }}</p>
          </div>

          <div v-if="editingChannel && authStore.hasRole('admin')" class="setting-row setting-row--last">
            <div class="setting-info">
              <label>{{ $t('agentEditor.im.enabled') }}</label>
            </div>
            <div class="setting-control">
              <t-switch v-model="editingEnabled" size="small" />
            </div>
          </div>
        </section>
      </div>

      <!-- Step 2: Connection -->
      <div v-else-if="wizardStep === 1" class="im-step-body">
        <section v-if="formData.platform !== 'wechat'" class="setting-drawer__section im-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('agentEditor.im.sectionAccess') }}</h4>

          <div class="form-item">
            <label class="form-label required">{{ $t('agentEditor.im.mode') }}</label>
            <div class="option-chips">
              <button
                type="button"
                class="option-chip"
                :class="{ 'option-chip--active': formData.mode === 'websocket' }"
                :disabled="formData.platform === 'mattermost'"
                @click="formData.mode = 'websocket'"
              >
                WebSocket
              </button>
              <button
                type="button"
                class="option-chip"
                :class="{ 'option-chip--active': formData.mode === 'webhook' }"
                @click="formData.mode = 'webhook'"
              >
                Webhook
              </button>
            </div>
            <p class="form-desc">
              {{ formData.platform === 'mattermost' ? $t('agentEditor.im.mattermostModeHint') : $t('agentEditor.im.modeHint') }}
            </p>
          </div>

          <div class="form-item">
            <label class="form-label required">{{ $t('agentEditor.im.outputMode') }}</label>
            <div class="option-chips">
              <button
                type="button"
                class="option-chip"
                :class="{ 'option-chip--active': formData.output_mode === 'stream' }"
                @click="formData.output_mode = 'stream'"
              >
                {{ $t('agentEditor.im.outputStream') }}
              </button>
              <button
                type="button"
                class="option-chip"
                :class="{ 'option-chip--active': formData.output_mode === 'full' }"
                @click="formData.output_mode = 'full'"
              >
                {{ $t('agentEditor.im.outputFull') }}
              </button>
            </div>
          </div>
        </section>

        <section class="setting-drawer__section im-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('agentEditor.im.sectionSession') }}</h4>
          <div class="form-item">
            <label class="form-label required">{{ $t('agentEditor.im.sessionMode') }}</label>
            <div class="option-chips">
              <button
                type="button"
                class="option-chip"
                :class="{ 'option-chip--active': formData.session_mode === 'user' }"
                @click="formData.session_mode = 'user'"
              >
                {{ $t('agentEditor.im.sessionModeUser') }}
              </button>
              <button
                type="button"
                class="option-chip"
                :class="{ 'option-chip--active': formData.session_mode === 'thread' }"
                :disabled="!platformSupportsThread(formData.platform)"
                @click="formData.session_mode = 'thread'"
              >
                {{ $t('agentEditor.im.sessionModeThread') }}
              </button>
            </div>
            <p class="form-desc">{{ $t('agentEditor.im.sessionModeHint') }}</p>
          </div>
        </section>

        <section v-if="editingChannel && formData.mode === 'webhook'" class="setting-drawer__section im-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('agentEditor.im.sectionCallback') }}</h4>
          <div class="form-item">
            <label class="form-label">{{ $t('agentEditor.im.callbackUrl') }}</label>
            <div class="callback-url-control">
              <t-input
                :model-value="getCallbackUrl(editingChannel)"
                readonly
                class="mono-text-input callback-url-input"
              />
              <t-button size="small" variant="text" :title="$t('common.copy')" @click="copyUrl(editingChannel)">
                <t-icon name="file-copy" />
              </t-button>
            </div>
          </div>
        </section>
      </div>

      <!-- Step 3: File knowledge base -->
      <div v-else-if="wizardStep === 2" class="im-step-body">
        <section class="setting-drawer__section im-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('agentEditor.im.sectionKnowledge') }}</h4>
          <div class="form-item">
            <label class="form-label">{{ $t('agentEditor.im.fileKnowledgeBase') }}</label>
            <t-select
              v-model="formData.knowledge_base_id"
              :placeholder="$t('agentEditor.im.fileKnowledgeBasePlaceholder')"
              clearable
              filterable
            >
              <t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" />
            </t-select>
            <p class="form-desc">{{ $t('agentEditor.im.fileKnowledgeBaseHint') }}</p>
          </div>
        </section>
      </div>

      <!-- Step 4: Credentials -->
      <div v-else class="im-step-body">
        <section class="setting-drawer__section im-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('agentEditor.im.sectionCredentials') }}</h4>
          <div class="drawer-form">
        <!-- WeCom credentials -->
        <template v-if="formData.platform === 'wecom'">
          <div class="platform-link-hint">
            <a href="https://work.weixin.qq.com/" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ $t('agentEditor.im.wecomConsole') }}
              <t-icon name="link" class="link-icon" />
            </a>
            <span class="hint-text">{{ $t('agentEditor.im.consoleTip') }}</span>
          </div>
          <template v-if="formData.mode === 'websocket'">
            <div class="form-item">
              <label class="form-label">Bot ID</label>
              <t-input v-model="formData.credentials.bot_id" placeholder="Bot ID" />
            </div>
            <div class="form-item">
              <label class="form-label">Bot Secret</label>
              <t-input v-model="formData.credentials.bot_secret" type="password" placeholder="Bot Secret" />
            </div>
            <div class="form-item">
              <label class="form-label">WebSocket Endpoint</label>
              <t-input v-model="formData.credentials.ws_endpoint" placeholder="wss://openws.work.weixin.qq.com" />
              <p class="form-desc">{{ $t('agentEditor.im.wecomWSEndpointHint') }}</p>
            </div>
          </template>
          <template v-else>
            <div class="form-item">
              <label class="form-label">Corp ID</label>
              <t-input v-model="formData.credentials.corp_id" placeholder="Corp ID" />
            </div>
            <div class="form-item">
              <label class="form-label">Agent Secret</label>
              <t-input v-model="formData.credentials.agent_secret" type="password" placeholder="Agent Secret" />
            </div>
            <div class="form-item">
              <label class="form-label">Token</label>
              <t-input v-model="formData.credentials.token" placeholder="Token" />
            </div>
            <div class="form-item">
              <label class="form-label">EncodingAESKey</label>
              <t-input v-model="formData.credentials.encoding_aes_key" placeholder="EncodingAESKey" />
            </div>
            <div class="form-item">
              <label class="form-label">Corp Agent ID</label>
              <t-input-number v-model="formData.credentials.corp_agent_id" placeholder="Corp Agent ID" style="width: 100%;" />
            </div>
            <div class="form-item">
              <label class="form-label">API Base URL</label>
              <t-input v-model="formData.credentials.api_base_url" placeholder="https://qyapi.weixin.qq.com" />
              <p class="form-desc">{{ $t('agentEditor.im.wecomAPIBaseURLHint') }}</p>
            </div>
          </template>
        </template>

        <!-- Feishu credentials -->
        <template v-if="formData.platform === 'feishu'">
          <div class="platform-link-hint">
            <a href="https://open.feishu.cn/" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ $t('agentEditor.im.feishuConsole') }}
              <t-icon name="link" class="link-icon" />
            </a>
            <span class="hint-text">{{ $t('agentEditor.im.consoleTip') }}</span>
          </div>
          <div class="form-item">
            <label class="form-label">App ID</label>
            <t-input v-model="formData.credentials.app_id" placeholder="App ID" />
          </div>
          <div class="form-item">
            <label class="form-label">App Secret</label>
            <t-input v-model="formData.credentials.app_secret" type="password" placeholder="App Secret" />
          </div>
          <template v-if="formData.mode === 'webhook'">
            <div class="form-item">
              <label class="form-label">Verification Token</label>
              <t-input v-model="formData.credentials.verification_token" placeholder="Verification Token" />
            </div>
            <div class="form-item">
              <label class="form-label">Encrypt Key</label>
              <t-input v-model="formData.credentials.encrypt_key" type="password" placeholder="Encrypt Key" />
            </div>
          </template>
        </template>

        <!-- Slack credentials -->
        <template v-if="formData.platform === 'slack'">
          <div class="platform-link-hint">
            <a href="https://api.slack.com/apps" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ $t('agentEditor.im.slackConsole') }}
              <t-icon name="link" class="link-icon" />
            </a>
            <span class="hint-text">{{ $t('agentEditor.im.consoleTip') }}</span>
          </div>
          <template v-if="formData.mode === 'websocket'">
            <div class="form-item">
              <label class="form-label">App Token</label>
              <t-input v-model="formData.credentials.app_token" type="password" placeholder="xapp-..." />
            </div>
            <div class="form-item">
              <label class="form-label">Bot Token</label>
              <t-input v-model="formData.credentials.bot_token" type="password" placeholder="xoxb-..." />
            </div>
          </template>
          <template v-else>
            <div class="form-item">
              <label class="form-label">Bot Token</label>
              <t-input v-model="formData.credentials.bot_token" type="password" placeholder="xoxb-..." />
            </div>
            <div class="form-item">
              <label class="form-label">Signing Secret</label>
              <t-input v-model="formData.credentials.signing_secret" type="password" placeholder="Signing Secret" />
            </div>
          </template>
        </template>

        <!-- Telegram credentials -->
        <template v-if="formData.platform === 'telegram'">
          <div class="platform-link-hint">
            <a href="https://t.me/BotFather" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ $t('agentEditor.im.telegramConsole') }}
              <t-icon name="link" class="link-icon" />
            </a>
            <span class="hint-text">{{ $t('agentEditor.im.consoleTip') }}</span>
          </div>
          <div class="form-item">
            <label class="form-label">Bot Token</label>
            <t-input v-model="formData.credentials.bot_token" type="password" placeholder="123456789:AABBccdd..." />
          </div>
          <template v-if="formData.mode === 'webhook'">
            <div class="form-item">
              <label class="form-label">Secret Token</label>
              <t-input v-model="formData.credentials.secret_token" type="password" placeholder="Secret Token (optional)" />
            </div>
          </template>
        </template>

        <!-- DingTalk credentials -->
        <template v-if="formData.platform === 'dingtalk'">
          <div class="platform-link-hint">
            <a href="https://open.dingtalk.com/" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ $t('agentEditor.im.dingtalkConsole') }}
              <t-icon name="link" class="link-icon" />
            </a>
            <span class="hint-text">{{ $t('agentEditor.im.consoleTip') }}</span>
          </div>
          <div class="form-item">
            <label class="form-label">Client ID (AppKey)</label>
            <t-input v-model="formData.credentials.client_id" placeholder="Client ID / AppKey" />
          </div>
          <div class="form-item">
            <label class="form-label">Client Secret (AppSecret)</label>
            <t-input v-model="formData.credentials.client_secret" type="password" placeholder="Client Secret / AppSecret" />
          </div>
          <div class="form-item">
            <label class="form-label">{{ $t('agentEditor.im.dingtalkCardTemplateId') }}</label>
            <t-input v-model="formData.credentials.card_template_id" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.schema" />
            <p class="form-desc">{{ $t('agentEditor.im.dingtalkCardTemplateIdHint') }}</p>
          </div>
        </template>

        <!-- Mattermost credentials -->
        <template v-if="formData.platform === 'mattermost'">
          <div class="platform-link-hint">
            <a href="https://developers.mattermost.com/integrate/webhooks/outgoing/" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ $t('agentEditor.im.mattermostConsole') }}
              <t-icon name="link" class="link-icon" />
            </a>
            <span class="hint-text">{{ $t('agentEditor.im.consoleTip') }}</span>
          </div>
          <div class="form-item">
            <label class="form-label">Site URL</label>
            <t-input v-model="formData.credentials.site_url" placeholder="https://mattermost.example.com" />
          </div>
          <div class="form-item">
            <label class="form-label">Bot Token</label>
            <t-input v-model="formData.credentials.bot_token" type="password" placeholder="Bot Token" />
          </div>
          <div class="form-item">
            <label class="form-label">Outgoing Webhook Token</label>
            <t-input v-model="formData.credentials.outgoing_token" type="password" placeholder="Token from Outgoing Webhook" />
          </div>
          <div class="form-item">
            <label class="form-label">Bot User ID</label>
            <t-input v-model="formData.credentials.bot_user_id" placeholder="Optional — filter bot self-messages" />
          </div>
          <div class="settings-group">
            <div class="setting-row setting-row--last">
              <div class="setting-info">
                <label>{{ $t('agentEditor.im.mattermostPostToMain') }}</label>
                <p class="desc">{{ $t('agentEditor.im.mattermostPostToMainHint') }}</p>
              </div>
              <div class="setting-control">
                <t-switch
                  :value="!!formData.credentials.post_to_main"
                  size="small"
                  @change="(v: boolean) => { formData.credentials.post_to_main = v }"
                />
              </div>
            </div>
          </div>
        </template>
        <!-- WeChat credentials (QR code binding) -->
        <template v-if="formData.platform === 'wechat'">
          <p class="form-desc">{{ $t('agentEditor.im.wechatHint') }}</p>

          <!-- Already bound state -->
          <div v-if="wechatBound" class="wechat-bound-status">
            <t-icon name="check-circle-filled" class="bound-icon" />
            <span>{{ $t('agentEditor.im.wechatBindSuccess') }}</span>
            <t-button size="small" variant="outline" theme="default" @click="startWeChatBinding">
              {{ $t('agentEditor.im.wechatRebind') }}
            </t-button>
          </div>

          <!-- QR code binding flow -->
          <div v-else class="wechat-qr-section">
            <!-- Initial state: show bind button -->
            <div v-if="!wechatQRImgUrl" class="wechat-bind-action">
              <t-button theme="default" variant="outline" :loading="wechatLoading" @click="startWeChatBinding">
                <template #icon><t-icon name="scan" /></template>
                {{ $t('agentEditor.im.wechatScanBind') }}
              </t-button>
            </div>

            <!-- QR code displayed -->
            <div v-else class="wechat-qr-display">
              <div class="qr-container">
                <img :src="wechatQRImgUrl" alt="WeChat QR Code" class="qr-image" />
                <div v-if="wechatQRStatus === 'expired'" class="qr-expired-overlay" @click="startWeChatBinding">
                  <t-icon name="refresh" class="refresh-icon" />
                  <span>{{ $t('agentEditor.im.wechatQRExpired') }}</span>
                </div>
              </div>
              <p class="qr-hint">
                <template v-if="wechatQRStatus === 'scaned'">
                  {{ $t('agentEditor.im.wechatBinding') }}
                </template>
                <template v-else>
                  {{ $t('agentEditor.im.wechatScanning') }}
                </template>
              </p>
            </div>
          </div>
        </template>
          </div>
        </section>
      </div>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, onUnmounted, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  listIMChannels, createIMChannel, updateIMChannel, deleteIMChannel, toggleIMChannel,
  getWeChatQRCode, pollWeChatQRCodeStatus, listAllIMChannels, listAgents,
  type IMChannelOverview, type CustomAgent,
} from '@/api/agent';
import { useChatResourcesStore } from '@/stores/chatResources';
import type { IMChannel } from '@/api/agent';
import { useAuthStore } from '@/stores/auth';
import SettingDrawer from '@/components/settings/SettingDrawer.vue';
import wecomLogo from '@/assets/img/im/wecom.svg';
import feishuLogo from '@/assets/img/im/feishu.svg';
import slackLogo from '@/assets/img/im/slack.svg';
import telegramLogo from '@/assets/img/im/telegram.svg';
import dingtalkLogo from '@/assets/img/im/dingtalk.svg';
import mattermostLogo from '@/assets/img/im/mattermost.svg';
import wechatLogo from '@/assets/img/im/wechat.svg';

type IMPlatform = IMChannel['platform'];

const PLATFORM_LOGO: Record<string, string> = {
  wecom: wecomLogo,
  feishu: feishuLogo,
  slack: slackLogo,
  telegram: telegramLogo,
  dingtalk: dingtalkLogo,
  mattermost: mattermostLogo,
  wechat: wechatLogo,
};

const platformLogo = (platform: string): string => (platform ? PLATFORM_LOGO[platform] || '' : '');

const { t } = useI18n();
const authStore = useAuthStore();

const props = withDefaults(defineProps<{
  agentId?: string;
  tenantMode?: boolean;
  initialAgentId?: string;
}>(), {
  tenantMode: false,
});

const agents = ref<CustomAgent[]>([]);
const agentOptions = computed(() =>
  agents.value.map((agent) => ({ label: agent.name, value: agent.id })),
);

const channels = ref<Array<IMChannel | IMChannelOverview>>([]);
const loading = ref(false);
const saving = ref(false);
const showCreateDialog = ref(false);
const editingChannel = ref<IMChannel | null>(null);
const editingEnabled = ref(true);
const wizardStep = ref(0);
const channelNameTouched = ref(false);

const stepTitles = computed(() => [
  t('agentEditor.im.stepBasic'),
  t('agentEditor.im.stepConnection'),
  t('agentEditor.im.stepKnowledge'),
  t('agentEditor.im.stepCredentials'),
]);

const drawerStepDescription = computed(() => stepTitles.value[wizardStep.value] ?? '');

const drawerConfirmText = computed(() =>
  wizardStep.value < stepTitles.value.length - 1 ? t('common.next') : t('common.save'),
);

const platformOptions = computed(() => ([
  { value: 'wecom' as IMPlatform, label: t('agentEditor.im.wecom'), logo: wecomLogo },
  { value: 'feishu' as IMPlatform, label: t('agentEditor.im.feishu'), logo: feishuLogo },
  { value: 'slack' as IMPlatform, label: t('agentEditor.im.slack'), logo: slackLogo },
  { value: 'telegram' as IMPlatform, label: t('agentEditor.im.telegram'), logo: telegramLogo },
  { value: 'dingtalk' as IMPlatform, label: t('agentEditor.im.dingtalk'), logo: dingtalkLogo },
  { value: 'mattermost' as IMPlatform, label: t('agentEditor.im.mattermost'), logo: mattermostLogo },
  { value: 'wechat' as IMPlatform, label: t('agentEditor.im.wechat'), logo: wechatLogo },
]));

const drawerTitle = computed(() => {
  if (editingChannel.value) {
    return formData.value.name?.trim() || t('agentEditor.im.unnamed');
  }
  return t('agentEditor.im.addChannel');
});

function validateWizardStep(step: number): boolean {
  if (step === 0 && props.tenantMode && !formData.value.target_agent_id) {
    MessagePlugin.warning(t('integrations.selectAgentHint'));
    return false;
  }
  return true;
}

function prevWizardStep() {
  if (wizardStep.value > 0) wizardStep.value -= 1;
}

async function handleDrawerConfirm() {
  if (wizardStep.value < stepTitles.value.length - 1) {
    if (!validateWizardStep(wizardStep.value)) return;
    wizardStep.value += 1;
    return;
  }
  await handleSave();
}

// Knowledge base options for file-to-KB feature
const knowledgeBases = ref<{ id: string; name: string }[]>([]);

// WeChat QR code binding state
const wechatQRContent = ref('');  // raw text to encode as QR code
const wechatQRImgUrl = ref('');   // generated QR image URL
const wechatQRCode = ref('');     // opaque token for polling status
const wechatQRStatus = ref<string>('');
const wechatLoading = ref(false);
let wechatPollActive = false;
let wechatPollTimer: ReturnType<typeof setTimeout> | null = null;

const defaultCredentials = (): Record<string, any> => ({});

const formData = ref({
  target_agent_id: '',
  platform: 'wecom' as 'wecom' | 'feishu' | 'slack' | 'telegram' | 'dingtalk' | 'mattermost' | 'wechat',
  name: '',
  mode: 'websocket' as 'webhook' | 'websocket' | 'longpoll',
  output_mode: 'stream' as 'stream' | 'full',
  session_mode: 'user' as 'user' | 'thread',
  knowledge_base_id: '',
  credentials: defaultCredentials(),
});

const channelMenuOptions = (channel: IMChannel | IMChannelOverview) => ([
  {
    content: channel.enabled ? t('common.off') : t('common.on'),
    value: 'toggle',
  },
  { content: t('common.delete'), value: 'delete', theme: 'error' as const },
]);

function handleChannelMenuClick(
  data: { value?: string },
  channel: IMChannel | IMChannelOverview,
) {
  if (data.value === 'toggle') {
    void handleToggle(channel);
    return;
  }
  if (data.value === 'delete') {
    confirmDelete(channel.id);
  }
}

function agentDisplayName(channel: IMChannel | IMChannelOverview): string {
  return agentForChannel(channel)?.name || '';
}

function agentForChannel(channel: IMChannel | IMChannelOverview): CustomAgent | undefined {
  const found = agents.value.find((agent) => agent.id === channel.agent_id);
  if (found) return found;
  const overviewName = (channel as IMChannelOverview).agent_name;
  if (!overviewName) return undefined;
  return {
    id: channel.agent_id,
    name: overviewName,
    is_builtin: false,
    config: {},
  };
}

function platformLabel(platform: string): string {
  const key = `agentEditor.im.${platform}`;
  return t(key);
}

function defaultChannelName(platform: string = formData.value.platform): string {
  return platformLabel(platform);
}

function resolvedChannelName(): string {
  return formData.value.name.trim() || defaultChannelName();
}

function platformSupportsThread(platform: string): boolean {
  return ['slack', 'mattermost', 'feishu', 'telegram'].includes(platform);
}

watch(
  () => formData.value.platform,
  (p) => {
    if (p === 'mattermost') {
      formData.value.mode = 'webhook';
      if (typeof formData.value.credentials.post_to_main !== 'boolean') {
        formData.value.credentials.post_to_main = false;
      }
    }
    if (!platformSupportsThread(p)) {
      formData.value.session_mode = 'user';
    }
  },
);
// Whether WeChat credentials are already bound
const wechatBound = computed(() => {
  return formData.value.platform === 'wechat' &&
    formData.value.credentials.bot_token &&
    formData.value.credentials.ilink_bot_id;
});


function onPlatformChange(val: string | number | boolean) {
  if (editingChannel.value) return;
  formData.value.credentials = defaultCredentials();
  stopWeChatPolling();
  wechatQRContent.value = '';
  wechatQRImgUrl.value = '';
  wechatQRCode.value = '';
  wechatQRStatus.value = '';
  // WeChat uses fixed mode/output
  if (val === 'wechat') {
    formData.value.mode = 'longpoll';
    formData.value.output_mode = 'full';
  } else {
    formData.value.mode = 'websocket';
    formData.value.output_mode = 'stream';
  }
  if (!channelNameTouched.value) {
    formData.value.name = defaultChannelName(String(val));
  }
}

async function startWeChatBinding() {
  stopWeChatPolling();
  wechatLoading.value = true;
  wechatQRContent.value = '';
  wechatQRImgUrl.value = '';
  wechatQRStatus.value = '';

  try {
    const res = await getWeChatQRCode();
    // qrcode_url is the text content to encode as QR code (e.g. a weixin:// URL)
    wechatQRContent.value = res.data.qrcode_url;
    wechatQRCode.value = res.data.qrcode;
    wechatQRStatus.value = 'wait';

    // Generate QR code image via public API (no extra npm dependency needed)
    wechatQRImgUrl.value = `https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(res.data.qrcode_url)}`;

    // Start long-polling for scan status
    startStatusPolling();
  } catch (e: any) {
    MessagePlugin.error(e?.message || 'Failed to generate QR code');
  } finally {
    wechatLoading.value = false;
  }
}

function startStatusPolling() {
  wechatPollActive = true;
  pollOnce();
}

async function pollOnce() {
  if (!wechatPollActive) return;
  try {
    const statusRes = await pollWeChatQRCodeStatus(wechatQRCode.value);
    if (!wechatPollActive) return;
    wechatQRStatus.value = statusRes.data.status;

    if (statusRes.data.status === 'confirmed' && statusRes.data.credentials) {
      formData.value.credentials = {
        bot_token: statusRes.data.credentials.bot_token,
        ilink_bot_id: statusRes.data.credentials.ilink_bot_id,
        ilink_user_id: statusRes.data.credentials.ilink_user_id,
      };
      stopWeChatPolling();
      wechatQRContent.value = '';
      wechatQRImgUrl.value = '';
      MessagePlugin.success(t('agentEditor.im.wechatBindSuccess'));
      return;
    }
    if (statusRes.data.status === 'expired') {
      stopWeChatPolling();
      return;
    }
  } catch {
    // transient error
  }
  // Schedule next poll with a short delay (the backend already long-polled ~35s)
  if (wechatPollActive) {
    wechatPollTimer = setTimeout(pollOnce, 500);
  }
}

function stopWeChatPolling() {
  wechatPollActive = false;
  if (wechatPollTimer) {
    clearTimeout(wechatPollTimer);
    wechatPollTimer = null;
  }
}

async function loadChannels() {
  loading.value = true;
  try {
    const chatResources = useChatResourcesStore();
    if (props.tenantMode) {
      const [channelRes, agentRes] = await Promise.all([
        listAllIMChannels(),
        listAgents(),
        chatResources.ensureKnowledgeBases(),
      ]);
      channels.value = channelRes.data || [];
      agents.value = agentRes?.data || [];
    } else if (props.agentId) {
      const [channelRes] = await Promise.all([
        listIMChannels(props.agentId),
        chatResources.ensureKnowledgeBases(),
      ]);
      channels.value = channelRes.data || [];
    } else {
      channels.value = [];
    }
    knowledgeBases.value = chatResources.rawKnowledgeBases.map((kb: any) => ({ id: kb.id, name: kb.name }));
  } catch {
    channels.value = [];
  } finally {
    loading.value = false;
  }
}

function getCallbackUrl(channel: IMChannel): string {
  const base = window.location.origin;
  return `${base}/api/v1/im/callback/${channel.id}`;
}

async function copyUrl(channel: IMChannel) {
  const text = getCallbackUrl(channel);
  try {
    await navigator.clipboard.writeText(text);
    MessagePlugin.success(t('common.copySuccess'));
  } catch {
    const el = document.createElement('textarea');
    el.value = text;
    el.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
    document.body.appendChild(el);
    el.focus();
    el.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(el);
    if (ok) {
      MessagePlugin.success(t('common.copySuccess'));
    } else {
      MessagePlugin.error(t('common.copyFailed'));
    }
  }
}

function openCreate() {
  resetForm();
  if (props.tenantMode && props.initialAgentId) {
    formData.value.target_agent_id = props.initialAgentId;
  }
  showCreateDialog.value = true;
}

function openDrawer(channel: IMChannel | IMChannelOverview) {
  void editChannel(channel);
}

function confirmDelete(id: string) {
  const dialog = DialogPlugin.confirm({
    header: t('common.delete'),
    body: t('agentEditor.im.deleteConfirm'),
    confirmBtn: { content: t('common.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.destroy();
      await handleDelete(id);
    },
    onClose: () => dialog.destroy(),
  });
}

async function editChannel(channel: IMChannel | IMChannelOverview) {
  wizardStep.value = 0;
  let fullChannel: IMChannel | null = null;
  if (props.tenantMode && !('credentials' in channel)) {
    try {
      const res = await listIMChannels(channel.agent_id);
      fullChannel = (res.data || []).find((item) => item.id === channel.id) || null;
    } catch {
      fullChannel = null;
    }
  } else {
    fullChannel = channel as IMChannel;
  }
  if (!fullChannel) {
    MessagePlugin.error(t('common.operationFailed'));
    return;
  }
  editingChannel.value = fullChannel;
  editingEnabled.value = fullChannel.enabled;
  channelNameTouched.value = true;
  formData.value = {
    target_agent_id: fullChannel.agent_id,
    platform: fullChannel.platform,
    name: fullChannel.name,
    mode: fullChannel.mode,
    output_mode: fullChannel.output_mode,
    session_mode: fullChannel.session_mode || 'user',
    knowledge_base_id: fullChannel.knowledge_base_id || '',
    credentials: { ...fullChannel.credentials },
  };
  showCreateDialog.value = true;
}

function resetForm() {
  editingChannel.value = null;
  editingEnabled.value = true;
  wizardStep.value = 0;
  channelNameTouched.value = false;
  stopWeChatPolling();
  wechatQRContent.value = '';
  wechatQRImgUrl.value = '';
  wechatQRCode.value = '';
  wechatQRStatus.value = '';
  formData.value = {
    target_agent_id: props.tenantMode ? (props.initialAgentId || '') : '',
    platform: 'wecom',
    name: defaultChannelName('wecom'),
    mode: 'websocket',
    output_mode: 'stream',
    session_mode: 'user',
    knowledge_base_id: '',
    credentials: defaultCredentials(),
  };
}

async function handleSave() {
  saving.value = true;
  try {
    // For WeChat, validate that credentials are bound
    if (formData.value.platform === 'wechat' && !formData.value.credentials.bot_token) {
      MessagePlugin.warning(t('agentEditor.im.wechatScanBind'));
      return;
    }

    if (editingChannel.value) {
      await updateIMChannel(editingChannel.value.id, {
        name: resolvedChannelName(),
        mode: formData.value.mode,
        output_mode: formData.value.output_mode,
        session_mode: formData.value.session_mode,
        knowledge_base_id: formData.value.knowledge_base_id,
        credentials: formData.value.credentials,
        enabled: editingEnabled.value,
        ...(props.tenantMode && formData.value.target_agent_id
          ? { agent_id: formData.value.target_agent_id }
          : {}),
      });
      MessagePlugin.success(t('common.updateSuccess'));
    } else {
      const targetAgentId = props.tenantMode ? formData.value.target_agent_id : props.agentId;
      if (!targetAgentId) {
        MessagePlugin.warning(t('integrations.selectAgentHint'));
        return;
      }
      await createIMChannel(targetAgentId, {
        platform: formData.value.platform,
        name: resolvedChannelName(),
        mode: formData.value.mode,
        output_mode: formData.value.output_mode,
        session_mode: formData.value.session_mode,
        knowledge_base_id: formData.value.knowledge_base_id,
        credentials: formData.value.credentials,
      });
      MessagePlugin.success(t('common.createSuccess'));
    }
    showCreateDialog.value = false;
    resetForm();
    await loadChannels();
  } catch (e: any) {
    const msg = e?.message || (typeof e?.error === 'string' ? e.error : null) || t('common.operationFailed');
    MessagePlugin.error(msg);
  } finally {
    saving.value = false;
  }
}

async function handleToggle(channel: IMChannel | IMChannelOverview) {
  try {
    await toggleIMChannel(channel.id);
    await loadChannels();
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'));
  }
}

async function handleDelete(id: string) {
  try {
    await deleteIMChannel(id);
    MessagePlugin.success(t('common.deleteSuccess'));
    await loadChannels();
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'));
  }
}

onMounted(() => {
  if (props.tenantMode || props.agentId) {
    loadChannels();
  }
});

watch(
  () => [props.tenantMode, props.agentId],
  () => {
    if (props.tenantMode || props.agentId) {
      loadChannels();
    }
  },
);

onUnmounted(() => {
  stopWeChatPolling();
});
</script>

<style scoped lang="less">
@import './css/channel-panel-list.less';

.im-panel {
  display: flex;
  flex-direction: column;
}

.drawer-platform-icon {
  width: 16px;
  height: 16px;
  object-fit: contain;
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

:deep(.im-drawer__section.setting-drawer__section) {
  gap: 10px;
  padding: 10px 0 14px;
}

.callback-url-control {
  display: flex;
  align-items: center;
  gap: 4px;
}

.callback-url-input {
  flex: 1;
  min-width: 0;
}

.mono-text-input :deep(input) {
  font-family: var(--app-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
}

.drawer-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
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

  &.required::after {
    content: '*';
    color: var(--td-error-color);
    margin-left: 4px;
  }
}

.im-platform-select-prefix {
  width: 16px;
  height: 16px;
  object-fit: contain;
}

.im-platform-select-option {
  display: flex;
  align-items: center;
  gap: 8px;

  &__icon {
    width: 16px;
    height: 16px;
    object-fit: contain;
    flex-shrink: 0;
  }
}

.form-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-placeholder);
}

.option-chips {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 3px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.option-chip {
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: 12px;
  line-height: 1.3;
  padding: 5px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
  white-space: nowrap;

  &:hover:not(:disabled) {
    color: var(--td-text-color-primary);
  }

  &:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  &--active {
    background: var(--td-bg-color-container);
    color: var(--td-brand-color);
    font-weight: 500;
    box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
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
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &--last {
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
  }
}

.setting-control {
  flex-shrink: 0;
  padding-top: 2px;
}

.platform-link-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);

  .doc-link {
    white-space: nowrap;
  }

  .hint-text {
    color: var(--td-text-color-placeholder);
  }
}

// --- WeChat QR code binding ---
.wechat-bound-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: rgba(7, 193, 96, 0.06);
  border: 1px solid rgba(7, 193, 96, 0.2);
  border-radius: 8px;
  font-size: 14px;
  color: var(--td-text-color-primary);

  .bound-icon {
    font-size: 18px;
    color: #07c160;
  }
}

.wechat-qr-section {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.wechat-bind-action {
  padding: 24px 0;
}

.wechat-qr-display {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 16px 0;
}

.qr-container {
  position: relative;
  width: 200px;
  height: 200px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
  // QR code images are always black-on-white; force white background
  // so the code remains scannable in dark mode.
  background: #fff;

  .qr-image {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
}

.qr-expired-overlay {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  background: rgba(0, 0, 0, 0.6);
  color: #fff;
  cursor: pointer;
  font-size: 12px;

  .refresh-icon {
    font-size: 24px;
  }
}

.qr-hint {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  text-align: center;
}
</style>

<style lang="less">
.im-channel-drawer .setting-drawer__header-icon:has(.drawer-platform-icon) {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.im-channel-drawer .drawer-platform-icon {
  display: block;
  width: 20px;
  height: 20px;
  object-fit: contain;
}
</style>
