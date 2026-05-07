<template>
  <div class="section-content">
    <!-- Channel list header -->
    <div class="channels-section">
      <div class="channels-header">
        <span class="channels-title">{{ $t('agentEditor.im.addChannel') }}</span>
        <span class="channels-count">{{ channels.length }}</span>
      </div>

      <div v-if="loading" class="channels-loading">
        <t-loading size="small" />
        <span>{{ $t('common.loading') }}</span>
      </div>

      <div v-else-if="channels.length === 0" class="channels-empty">
        <t-icon name="chat-message" class="empty-icon" />
        <span>{{ $t('agentEditor.im.empty') }}</span>
      </div>

      <div v-else class="channels-list">
        <div v-for="channel in channels" :key="channel.id" class="channel-item">
          <div class="channel-item-header">
            <div class="channel-info-top">
              <div class="channel-main">
                <span class="platform-badge" :class="channel.platform">
                  {{ platformLabel(channel.platform) }}
                </span>
                <span class="channel-name">{{ channel.name || $t('agentEditor.im.unnamed') }}</span>
              </div>
            </div>
            <div class="channel-actions">
              <t-switch
                :value="channel.enabled"
                size="small"
                @change="handleToggle(channel)"
              />
              <t-dropdown
                trigger="click"
                placement="bottom-right"
                :options="[
                  { content: $t('common.edit'), value: 'edit', onClick: () => editChannel(channel) },
                  { content: $t('common.delete'), value: 'delete', theme: 'error' }
                ]"
                @click="(data) => data.value === 'delete' && handleDelete(channel.id)"
              >
                <t-button variant="text" theme="default" size="small">
                  <t-icon name="more" />
                </t-button>
              </t-dropdown>
            </div>
          </div>
          <div class="channel-info">
            <div class="channel-meta">
              <span class="meta-tag">
                <t-icon name="link" class="meta-icon" />
                {{ channel.mode }}
              </span>
              <span class="meta-tag">
                <t-icon name="play-circle" class="meta-icon" />
                {{ channel.output_mode === 'stream' ? $t('agentEditor.im.outputStream') : $t('agentEditor.im.outputFull') }}
              </span>
              <span v-if="channel.session_mode === 'thread'" class="meta-tag">
                <t-icon name="chat" class="meta-icon" />
                {{ $t('agentEditor.im.sessionModeThread') }}
              </span>
            </div>
            <div v-if="channel.mode === 'webhook'" class="callback-url-row">
              <span class="url-label">{{ $t('agentEditor.im.callbackUrl') }}:</span>
              <code class="url-value">{{ getCallbackUrl(channel) }}</code>
              <t-button theme="default" size="small" variant="text" @click="copyUrl(channel)">
                <t-icon name="file-copy" />
              </t-button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Add button -->
    <t-button theme="default" variant="dashed" block @click="showCreateDialog = true" class="add-btn">
      <t-icon name="add" />
      {{ $t('agentEditor.im.addChannel') }}
    </t-button>

    <!-- Create/Edit drawer -->
    <t-drawer
      v-model:visible="showCreateDialog"
      :header="editingChannel ? $t('agentEditor.im.editChannel') : $t('agentEditor.im.addChannel')"
      :confirm-btn="$t('common.save')"
      :cancel-btn="$t('common.cancel')"
      @confirm="handleSave"
      @close="resetForm"
      size="560px"
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
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, onUnmounted, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listIMChannels, createIMChannel, updateIMChannel, deleteIMChannel, toggleIMChannel,
  getWeChatQRCode, pollWeChatQRCodeStatus,
} from '@/api/agent';
import { listKnowledgeBases } from '@/api/knowledge-base';
import type { IMChannel } from '@/api/agent';

const { t } = useI18n();

const props = defineProps<{
  agentId: string;
}>();

const channels = ref<IMChannel[]>([]);
const loading = ref(false);
const showCreateDialog = ref(false);
const editingChannel = ref<IMChannel | null>(null);

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
    const [channelRes, kbRes] = await Promise.all([
      listIMChannels(props.agentId),
      listKnowledgeBases(),
    ]);
    channels.value = channelRes.data || [];
    knowledgeBases.value = (kbRes.data || []).map((kb: any) => ({ id: kb.id, name: kb.name }));
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
.section-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

// --- Channel list section (matches AgentShareSettings pattern) ---
.channels-section {
  margin-bottom: 8px;
}

.channels-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;

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

.channels-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 32px;
  color: var(--td-text-color-disabled);
  font-size: 14px;
}

.channels-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 40px 20px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  color: var(--td-text-color-disabled);

  .empty-icon {
    font-size: 32px;
    opacity: 0.5;
  }
}

.channels-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
  max-height: 400px;
  overflow-y: auto;
}

.channel-item {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  transition: all 0.2s cubic-bezier(0.38, 0, 0.24, 1);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
  }
}

.channel-item-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}

.channel-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.channel-info-top {
  display: flex;
  align-items: center;
  gap: 12px;
}

.channel-main {
  display: flex;
  align-items: center;
  gap: 8px;
}

.platform-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;

  &.wecom {
    background: rgba(7, 193, 96, 0.08);
    color: #07c160;
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

.channel-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.channel-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);

  .meta-tag {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 2px 6px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 4px;
  }

  .meta-icon {
    font-size: 12px;
    flex-shrink: 0;
  }
}

.callback-url-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  padding-top: 4px;
  border-top: 1px dashed var(--td-component-stroke);

  .url-label {
    color: var(--td-text-color-secondary);
    white-space: nowrap;
  }

  .url-value {
    background: var(--td-bg-color-container);
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    min-width: 0;
  }
}

.channel-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.add-btn {
  margin-top: 4px;

  :deep(.t-button__text) {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
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
