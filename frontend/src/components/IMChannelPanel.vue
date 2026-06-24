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
            <div class="channel-card__badge">
              <t-icon name="chat-message" size="18px" />
            </div>
            <div class="channel-card__body">
              <div class="channel-card__header">
                <h3 class="channel-card__title">{{ channel.name || $t('agentEditor.im.unnamed') }}</h3>
                <t-tag v-if="!channel.enabled" size="small" variant="light" theme="warning">
                  {{ $t('agentEditor.im.disabled') }}
                </t-tag>
              </div>
              <p class="channel-card__subtitle">{{ channelSummary(channel) }}</p>
            </div>
            <div v-if="authStore.hasRole('admin')" class="channel-card__actions" @click.stop>
              <t-switch
                :value="channel.enabled"
                size="small"
                @change="() => handleToggle(channel)"
              />
              <t-dropdown
                trigger="click"
                placement="bottom-right"
                :options="channelMenuOptions"
                @click="(data) => data.value === 'delete' && confirmDelete(channel.id)"
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
            <span class="channel-card--add__icon" aria-hidden="true">
              <t-icon name="add" />
            </span>
            <span class="channel-card--add__label">{{ $t('agentEditor.im.addChannel') }}</span>
          </button>
        </div>
      </t-loading>
    </div>

    <SettingDrawer
      v-model:visible="showCreateDialog"
      :title="drawerTitle"
      icon="chat-message"
      storage-key="setting-drawer:im-channel"
      width="560px"
      :confirm-loading="saving"
      :hide-footer="!authStore.hasRole('admin')"
      @confirm="handleSave"
      @cancel="resetForm"
    >
      <div class="drawer-form">
        <!-- Platform -->
        <div class="form-item">
          <label class="form-label">{{ $t('agentEditor.im.platform') }}</label>
          <t-select v-model="formData.platform" :disabled="!!editingChannel" @change="onPlatformChange">
            <t-option value="wecom" :label="$t('agentEditor.im.wecom')">
              <span class="platform-badge wecom" style="margin-right: 8px;">{{ $t('agentEditor.im.wecom') }}</span>
            </t-option>
            <t-option value="feishu" :label="$t('agentEditor.im.feishu')">
              <span class="platform-badge feishu" style="margin-right: 8px;">{{ $t('agentEditor.im.feishu') }}</span>
            </t-option>
            <t-option value="slack" :label="$t('agentEditor.im.slack')">
              <span class="platform-badge slack" style="margin-right: 8px;">{{ $t('agentEditor.im.slack') }}</span>
            </t-option>
            <t-option value="telegram" :label="$t('agentEditor.im.telegram')">
              <span class="platform-badge telegram" style="margin-right: 8px;">{{ $t('agentEditor.im.telegram') }}</span>
            </t-option>
            <t-option value="dingtalk" :label="$t('agentEditor.im.dingtalk')">
              <span class="platform-badge dingtalk" style="margin-right: 8px;">{{ $t('agentEditor.im.dingtalk') }}</span>
            </t-option>
            <t-option value="mattermost" :label="$t('agentEditor.im.mattermost')">
              <span class="platform-badge mattermost" style="margin-right: 8px;">{{ $t('agentEditor.im.mattermost') }}</span>
            </t-option>
            <t-option value="wechat" :label="$t('agentEditor.im.wechat')">
              <span class="platform-badge wechat" style="margin-right: 8px;">{{ $t('agentEditor.im.wechat') }}</span>
            </t-option>
          </t-select>
        </div>

        <!-- Name -->
        <div class="form-item">
          <label class="form-label">{{ $t('agentEditor.im.channelName') }}</label>
          <t-input v-model="formData.name" :placeholder="$t('agentEditor.im.channelNamePlaceholder')" />
        </div>

        <!-- Mode (hidden for WeChat) -->
        <div v-if="formData.platform !== 'wechat'" class="form-item">
          <label class="form-label">{{ $t('agentEditor.im.mode') }}</label>
          <t-radio-group v-model="formData.mode">
            <t-radio-button value="websocket" :disabled="formData.platform === 'mattermost'">WebSocket</t-radio-button>
            <t-radio-button value="webhook">Webhook</t-radio-button>
          </t-radio-group>
          <p v-if="formData.platform === 'mattermost'" class="form-hint">{{ $t('agentEditor.im.mattermostModeHint') }}</p>
          <p v-else class="form-hint">{{ $t('agentEditor.im.modeHint') }}</p>
        </div>

        <!-- Callback URL (webhook mode, edit only) -->
        <div v-if="editingChannel && formData.mode === 'webhook'" class="form-item">
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

        <!-- Output mode (hidden for WeChat) -->
        <div v-if="formData.platform !== 'wechat'" class="form-item">
          <label class="form-label">{{ $t('agentEditor.im.outputMode') }}</label>
          <t-radio-group v-model="formData.output_mode">
            <t-radio-button value="stream">{{ $t('agentEditor.im.outputStream') }}</t-radio-button>
            <t-radio-button value="full">{{ $t('agentEditor.im.outputFull') }}</t-radio-button>
          </t-radio-group>
        </div>

        <!-- Session Mode -->
        <div class="form-item">
          <label class="form-label">{{ $t('agentEditor.im.sessionMode') }}</label>
          <t-radio-group v-model="formData.session_mode">
            <t-radio-button value="user">{{ $t('agentEditor.im.sessionModeUser') }}</t-radio-button>
            <t-radio-button value="thread"
              :disabled="!platformSupportsThread(formData.platform)">
              {{ $t('agentEditor.im.sessionModeThread') }}
            </t-radio-button>
          </t-radio-group>
          <p class="form-hint">{{ $t('agentEditor.im.sessionModeHint') }}</p>
        </div>

        <!-- Knowledge base for file messages -->
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
          <p class="form-hint">{{ $t('agentEditor.im.fileKnowledgeBaseHint') }}</p>
        </div>

        <!-- Credentials divider -->
        <div class="form-divider"></div>

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
              <p class="form-hint">{{ $t('agentEditor.im.wecomWSEndpointHint') }}</p>
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
              <p class="form-hint">{{ $t('agentEditor.im.wecomAPIBaseURLHint') }}</p>
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
            <p class="form-hint">{{ $t('agentEditor.im.dingtalkCardTemplateIdHint') }}</p>
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
          <div class="form-item mattermost-post-main-row">
            <div class="mattermost-post-main-label">
              <label class="form-label">{{ $t('agentEditor.im.mattermostPostToMain') }}</label>
              <t-switch
                :value="!!formData.credentials.post_to_main"
                @change="(v: boolean) => { formData.credentials.post_to_main = v }"
              />
            </div>
            <p class="form-hint">{{ $t('agentEditor.im.mattermostPostToMainHint') }}</p>
          </div>
        </template>
        <!-- WeChat credentials (QR code binding) -->
        <template v-if="formData.platform === 'wechat'">
          <p class="form-hint">{{ $t('agentEditor.im.wechatHint') }}</p>

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
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, onUnmounted, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  listIMChannels, createIMChannel, updateIMChannel, deleteIMChannel, toggleIMChannel,
  getWeChatQRCode, pollWeChatQRCodeStatus,
} from '@/api/agent';
import { useChatResourcesStore } from '@/stores/chatResources';
import type { IMChannel } from '@/api/agent';
import { useAuthStore } from '@/stores/auth';
import SettingDrawer from '@/components/settings/SettingDrawer.vue';

const { t } = useI18n();
const authStore = useAuthStore();

const props = defineProps<{
  agentId: string;
}>();

const channels = ref<IMChannel[]>([]);
const loading = ref(false);
const saving = ref(false);
const showCreateDialog = ref(false);
const editingChannel = ref<IMChannel | null>(null);

const drawerTitle = computed(() =>
  editingChannel.value ? t('agentEditor.im.editChannel') : t('agentEditor.im.addChannel'),
);

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
  platform: 'wecom' as 'wecom' | 'feishu' | 'slack' | 'telegram' | 'dingtalk' | 'mattermost' | 'wechat',
  name: '',
  mode: 'websocket' as 'webhook' | 'websocket' | 'longpoll',
  output_mode: 'stream' as 'stream' | 'full',
  session_mode: 'user' as 'user' | 'thread',
  knowledge_base_id: '',
  credentials: defaultCredentials(),
});

const channelMenuOptions = computed(() => ([
  { content: t('common.delete'), value: 'delete', theme: 'error' },
]));

const channelSummary = (channel: IMChannel) => {
  const parts = [
    platformLabel(channel.platform),
    channel.mode,
    channel.output_mode === 'stream' ? t('agentEditor.im.outputStream') : t('agentEditor.im.outputFull'),
  ];
  if (channel.session_mode === 'thread') {
    parts.push(t('agentEditor.im.sessionModeThread'));
  }
  return parts.join(' · ');
};

function platformLabel(platform: string): string {
  const key = `agentEditor.im.${platform}`;
  return t(key);
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
    const [channelRes] = await Promise.all([
      listIMChannels(props.agentId),
      chatResources.ensureKnowledgeBases(),
    ]);
    channels.value = channelRes.data || [];
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
  showCreateDialog.value = true;
}

function openDrawer(channel: IMChannel) {
  editChannel(channel);
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

function editChannel(channel: IMChannel) {
  editingChannel.value = channel;
  formData.value = {
    platform: channel.platform,
    name: channel.name,
    mode: channel.mode,
    output_mode: channel.output_mode,
    session_mode: channel.session_mode || 'user',
    knowledge_base_id: channel.knowledge_base_id || '',
    credentials: { ...channel.credentials },
  };
  showCreateDialog.value = true;
}

function resetForm() {
  editingChannel.value = null;
  stopWeChatPolling();
  wechatQRContent.value = '';
  wechatQRImgUrl.value = '';
  wechatQRCode.value = '';
  wechatQRStatus.value = '';
  formData.value = {
    platform: 'wecom',
    name: '',
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
        name: formData.value.name,
        mode: formData.value.mode,
        output_mode: formData.value.output_mode,
        session_mode: formData.value.session_mode,
        knowledge_base_id: formData.value.knowledge_base_id,
        credentials: formData.value.credentials,
      });
      MessagePlugin.success(t('common.updateSuccess'));
    } else {
      await createIMChannel(props.agentId, {
        platform: formData.value.platform,
        name: formData.value.name,
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

async function handleToggle(channel: IMChannel) {
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
  loadChannels();
});

onUnmounted(() => {
  stopWeChatPolling();
});
</script>

<style scoped lang="less">
.im-panel {
  display: flex;
  flex-direction: column;
}

.channels-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;

  .channels-title {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .channels-count {
    padding: 2px 8px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 10px;
    font-size: 12px;
    color: var(--td-text-color-disabled);
  }
}

.channels-loading-wrap {
  min-height: 80px;
}

.channels-empty {
  padding: 32px 0;
}

.channel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 12px;
}

.channel-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  text-align: left;
  font: inherit;
  color: inherit;
  transition: border-color 0.18s ease, box-shadow 0.18s ease;

  &--clickable {
    cursor: pointer;
    width: 100%;

    &:hover,
    &:focus-visible {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
      outline: none;
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 68px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    width: 100%;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
      box-shadow: none;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      color: var(--td-brand-color);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }

  &__badge {
    flex-shrink: 0;
    width: 36px;
    height: 36px;
    border-radius: 9px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color);
  }

  &__body {
    flex: 1;
    min-width: 0;
  }

  &__header {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  &__title {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__subtitle {
    margin: 2px 0 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__actions {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 2px;
    padding-top: 2px;
  }

  &__more {
    flex-shrink: 0;
    padding: 2px;
    opacity: 0;
    color: var(--td-text-color-placeholder);
    transition: opacity 0.15s ease;

    &:hover,
    &:focus-visible {
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-primary);
    }
  }

  &:hover .channel-card__more,
  &:focus-within .channel-card__more,
  &__actions:focus-within .channel-card__more {
    opacity: 1;
  }
}

.platform-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;

  &.wecom {
    background: rgba(0, 130, 239, 0.08);
    color: #0082ef;
  }

  &.feishu {
    background: rgba(51, 112, 255, 0.08);
    color: #3370ff;
  }

  &.slack {
    background: rgba(224, 30, 90, 0.08);
    color: #e01e5a;
  }

  &.telegram {
    background: rgba(38, 166, 219, 0.08);
    color: #26a6db;
  }

  &.dingtalk {
    background: rgba(23, 126, 251, 0.08);
    color: #177efb;
  }

  &.mattermost {
    background: rgba(25, 42, 77, 0.08);
    color: #192a4d;
  }

  &.wechat {
    background: rgba(7, 193, 96, 0.08);
    color: #07c160;
  }
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
  gap: 16px;
}

.form-item {
  .form-label {
    display: block;
    margin-bottom: 8px;
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }
}

.mattermost-post-main-row {
  .mattermost-post-main-label {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;

    .form-label {
      margin-bottom: 0;
      flex: 1;
    }
  }
}

.form-divider {
  height: 1px;
  background: var(--td-component-stroke);
  margin: 4px 0;
}

.form-hint {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.4;
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
