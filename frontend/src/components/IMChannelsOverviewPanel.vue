<template>
  <div class="im-submenu" @click.stop>
    <div class="submenu-header">
      <div class="titles">
        <h4 class="title">{{ $t('imOverview.pageTitle') }}</h4>
        <p class="subtitle" :title="$t('imOverview.subtitle')">
          {{ $t('imOverview.subtitle') }}
        </p>
      </div>
      <t-button
        variant="text"
        theme="default"
        size="small"
        :loading="loading"
        @click="loadAll"
      >
        <template #icon><t-icon name="refresh" /></template>
      </t-button>
    </div>

    <div class="submenu-body">
      <div v-if="loading && channels.length === 0" class="state-placeholder">
        <t-loading size="small" />
      </div>

      <div v-else-if="channels.length === 0" class="state-placeholder">
        <span class="empty-text">{{ $t('imOverview.empty') }}</span>
      </div>

      <div v-else class="channels-list">
        <div
          v-for="ch in channels"
          :key="ch.id"
          class="channel-item"
          :class="{ 'is-disabled': !ch.enabled }"
          role="button"
          :tabindex="0"
          :title="$t('imOverview.gotoAgentEditor')"
          @click="gotoAgentEditor(ch)"
          @keydown.enter="gotoAgentEditor(ch)"
          @keydown.space.prevent="gotoAgentEditor(ch)"
        >
          <div class="item-main">
            <div class="channel-primary">
              <span class="platform-avatar">
                <img
                  v-if="hasPlatformLogo(ch.platform)"
                  :src="platformLogo(ch.platform)"
                  :alt="platformLabel(ch.platform)"
                />
                <t-icon v-else name="link" size="14px" />
              </span>
              <span class="channel-name" :title="channelDisplayName(ch)">
                {{ channelDisplayName(ch) }}
              </span>
            </div>
            <div class="channel-secondary">
              <span class="agent-avatar-sm">
                <span
                  v-if="agentMeta(ch.agent_id)?.is_builtin"
                  class="builtin-avatar"
                  :class="agentMeta(ch.agent_id)?.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'"
                >
                  <t-icon
                    :name="agentMeta(ch.agent_id)?.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                    size="10px"
                  />
                </span>
                <span
                  v-else-if="agentMeta(ch.agent_id)?.avatar"
                  class="builtin-avatar agent-emoji"
                >{{ agentMeta(ch.agent_id)!.avatar }}</span>
                <AgentAvatar
                  v-else
                  :name="agentDisplayName(ch)"
                  size="small"
                />
              </span>
              <span class="agent-name" :title="agentDisplayName(ch)">
                {{ agentDisplayName(ch) }}
              </span>
            </div>
          </div>

          <div class="item-actions" @click.stop>
            <t-switch
              :value="ch.enabled"
              size="small"
              :loading="togglingId === ch.id"
              :disabled="!authStore.hasRole('admin')"
              @change="handleToggle(ch)"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { useAuthStore } from '@/stores/auth';
import {
  listAllIMChannels,
  listAgents,
  toggleIMChannel,
  type IMChannelOverview,
  type CustomAgent,
} from '@/api/agent';
import AgentAvatar from '@/components/AgentAvatar.vue';

import wecomLogo from '@/assets/img/im/wecom.svg';
import feishuLogo from '@/assets/img/im/feishu.svg';
import slackLogo from '@/assets/img/im/slack.svg';
import telegramLogo from '@/assets/img/im/telegram.svg';
import dingtalkLogo from '@/assets/img/im/dingtalk.svg';
import mattermostLogo from '@/assets/img/im/mattermost.svg';
import wechatLogo from '@/assets/img/im/wechat.svg';

const PLATFORM_LOGO: Record<string, string> = {
  wecom: wecomLogo,
  feishu: feishuLogo,
  slack: slackLogo,
  telegram: telegramLogo,
  dingtalk: dingtalkLogo,
  mattermost: mattermostLogo,
  wechat: wechatLogo,
};

const props = defineProps<{
  active: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'channels-changed', channels: IMChannelOverview[]): void;
}>();

const { t } = useI18n();
const router = useRouter();
const authStore = useAuthStore();

const channels = ref<IMChannelOverview[]>([]);
const agentMap = ref<Map<string, CustomAgent>>(new Map());
const loading = ref(false);
const togglingId = ref<string>('');

watch(
  () => props.active,
  (v, old) => {
    if (v && !old) loadAll();
  },
  { immediate: true },
);

function hasPlatformLogo(p: string): boolean {
  return Boolean(PLATFORM_LOGO[p]);
}

function platformLogo(p: string): string {
  return PLATFORM_LOGO[p] || '';
}

function platformLabel(p: string): string {
  return t(`agentEditor.im.${p}`);
}

function channelDisplayName(ch: IMChannelOverview): string {
  return ch.name || platformLabel(ch.platform);
}

function agentMeta(agentId: string): CustomAgent | undefined {
  return agentMap.value.get(agentId);
}

function agentDisplayName(ch: IMChannelOverview): string {
  return ch.agent_name || agentMeta(ch.agent_id)?.name || t('imOverview.builtinAgent');
}

async function loadAll() {
  loading.value = true;
  try {
    const [chanResp, agentResp] = await Promise.all([
      listAllIMChannels(),
      listAgents(),
    ]);
    channels.value = chanResp?.data || [];
    const m = new Map<string, CustomAgent>();
    (agentResp?.data || []).forEach((a) => m.set(a.id, a));
    agentMap.value = m;
    emit('channels-changed', channels.value);
  } catch (err) {
    console.error('Failed to load IM overview:', err);
    MessagePlugin.error(t('imOverview.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function handleToggle(ch: IMChannelOverview) {
  if (togglingId.value === ch.id) return;
  togglingId.value = ch.id;
  const prev = ch.enabled;
  ch.enabled = !prev;
  try {
    await toggleIMChannel(ch.id);
    const resp = await listAllIMChannels();
    channels.value = resp?.data || [];
    emit('channels-changed', channels.value);
  } catch (err: any) {
    ch.enabled = prev;
    console.error('Failed to toggle IM channel:', err);
    MessagePlugin.error(err?.message || t('common.operationFailed'));
  } finally {
    if (togglingId.value === ch.id) togglingId.value = '';
  }
}

function gotoAgentEditor(ch: IMChannelOverview) {
  emit('close');
  router.push({
    path: '/platform/agents',
    query: { edit: ch.agent_id, section: 'im' },
  });
}
</script>

<style lang="less" scoped>
.im-submenu {
  display: flex;
  flex-direction: column;
  width: 300px;
  max-height: 520px;
  background: var(--td-bg-color-container);
  border-radius: 10px;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.12);
  border: 0.5px solid var(--td-component-stroke);
  overflow: hidden;
}

.submenu-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px 8px;
  border-bottom: 0.5px solid var(--td-component-stroke);

  .titles {
    min-width: 0;
  }

  .title {
    margin: 0 0 4px;
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    line-height: 1.3;
  }

  .subtitle {
    margin: 0;
    font-size: 11px;
    color: var(--td-text-color-placeholder);
    line-height: 1.45;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
  }
}

.submenu-body {
  flex: 1;
  overflow-y: auto;
  padding: 4px;
}

.state-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 40px 0;
  color: var(--td-text-color-placeholder);

  .empty-text {
    font-size: 12px;
  }
}

.channels-list {
  display: flex;
  flex-direction: column;
}

.channel-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px;
  border-radius: 6px;
  cursor: pointer;
  outline: none;
  transition: background 0.15s;

  & + & {
    border-top: 0.5px solid var(--td-component-stroke);
  }

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.is-disabled .item-main {
    opacity: 0.6;
  }
}

.item-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.channel-primary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.platform-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  flex-shrink: 0;
  background: var(--td-bg-color-secondarycontainer);

  img {
    width: 18px;
    height: 18px;
    object-fit: contain;
  }

  .t-icon {
    color: var(--td-text-color-placeholder);
  }
}

.channel-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  line-height: 1.35;
}

.channel-secondary {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  padding-left: 32px;
}

.agent-avatar-sm {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex-shrink: 0;

  :deep(.agent-avatar),
  :deep(.agent-avatar-small) {
    width: 16px;
    height: 16px;
  }

  :deep(.agent-avatar-letter) {
    font-size: 9px !important;
  }
}

.agent-name {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  line-height: 1.35;
}

.item-actions {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  padding-top: 2px;
}

.builtin-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 4px;
  flex-shrink: 0;

  &.agent-emoji {
    font-size: 11px;
    line-height: 1;
    background: var(--td-bg-color-container-hover);
  }

  &.normal {
    background: linear-gradient(135deg, rgba(7, 192, 95, 0.15) 0%, rgba(7, 192, 95, 0.08) 100%);
    color: var(--td-brand-color-active);
  }

  &.agent {
    background: linear-gradient(135deg, rgba(124, 77, 255, 0.15) 0%, rgba(124, 77, 255, 0.08) 100%);
    color: var(--td-brand-color);
  }
}
</style>
