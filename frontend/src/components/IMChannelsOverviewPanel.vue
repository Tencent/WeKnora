<template>
  <div class="im-submenu" @click.stop>
    <div class="submenu-header">
      <div class="titles">
        <h4 class="title">{{ $t('imOverview.pageTitle') }}</h4>
        <p class="subtitle">{{ $t('imOverview.subtitle') }}</p>
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
          <!-- Stacked rows: agent on top, IM channel below. Same avatar size
               and typography → both read as co-equal identities of the row. -->
          <div class="stack">
            <div class="stack-row">
              <span class="row-avatar">
                <span
                  v-if="agentMeta(ch.agent_id)?.is_builtin"
                  class="builtin-avatar"
                  :class="agentMeta(ch.agent_id)?.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'"
                >
                  <t-icon
                    :name="agentMeta(ch.agent_id)?.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                    size="12px"
                  />
                </span>
                <span
                  v-else-if="agentMeta(ch.agent_id)?.avatar"
                  class="builtin-avatar agent-emoji"
                >{{ agentMeta(ch.agent_id)!.avatar }}</span>
                <AgentAvatar
                  v-else
                  :name="ch.agent_name || ch.agent_id"
                  size="small"
                />
              </span>
              <span class="row-label" :title="ch.agent_name || agentMeta(ch.agent_id)?.name || ''">
                {{ ch.agent_name || agentMeta(ch.agent_id)?.name || $t('imOverview.builtinAgent') }}
              </span>
            </div>
            <div class="stack-row">
              <img
                class="row-avatar platform-logo"
                :src="platformLogo(ch.platform)"
                :alt="platformLabel(ch.platform)"
              />
              <span class="row-label" :title="ch.name || platformLabel(ch.platform)">
                {{ ch.name || platformLabel(ch.platform) }}
              </span>
            </div>
          </div>

          <div class="item-actions" @click.stop>
            <t-switch
              :value="ch.enabled"
              size="small"
              :loading="togglingId === ch.id"
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
import {
  listAllIMChannels,
  listAgents,
  toggleIMChannel,
  type IMChannelOverview,
  type CustomAgent,
} from '@/api/agent';
import AgentAvatar from '@/components/AgentAvatar.vue';

// Platform logos sourced from iconify (simple-icons / logos / tdesign / remix icon).
// Bundled as static assets so they share the app's caching story.
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

const channels = ref<IMChannelOverview[]>([]);
const agentMap = ref<Map<string, CustomAgent>>(new Map());
const loading = ref(false);
const togglingId = ref<string>('');

// Parent tells us when the submenu is active (hover/click-open). Reload only
// when transitioning into active so we don't refetch on every re-render.
watch(
  () => props.active,
  (v, old) => {
    if (v && !old) loadAll();
  },
  { immediate: true },
);

function platformLogo(p: string): string {
  return PLATFORM_LOGO[p] || '';
}

function platformLabel(p: string): string {
  return t(`agentEditor.im.${p}`);
}

function agentMeta(agentId: string): CustomAgent | undefined {
  return agentMap.value.get(agentId);
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
  if (togglingId.value) return;
  togglingId.value = ch.id;
  try {
    await toggleIMChannel(ch.id);
    const resp = await listAllIMChannels();
    channels.value = resp?.data || [];
    emit('channels-changed', channels.value);
  } catch (err: any) {
    console.error('Failed to toggle IM channel:', err);
    MessagePlugin.error(err?.message || t('common.operationFailed'));
  } finally {
    togglingId.value = '';
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
  border-radius: 8px;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.14);
  border: 1px solid var(--td-component-stroke);
  overflow: hidden;
}

.submenu-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--td-component-stroke);

  .titles {
    min-width: 0;
  }

  .title {
    margin: 0 0 2px;
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    line-height: 1.3;
  }

  .subtitle {
    margin: 0;
    font-size: 11px;
    color: var(--td-text-color-secondary);
    line-height: 1.4;
  }
}

.submenu-body {
  flex: 1;
  overflow-y: auto;
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 4px;
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
  gap: 2px;
}

.channel-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
  outline: none;
  transition: background 0.15s;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-container-hover);
  }

  &.is-disabled {
    opacity: 0.55;
  }
}

// Stacked rows: agent on top, channel below. Both lines share identical
// typography and avatar size so neither dominates — they read as two facets
// of the same row rather than a headline + caption.
.stack {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.stack-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.row-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 4px;
  flex-shrink: 0;

  &.platform-logo {
    object-fit: contain;
    background: transparent;
    padding: 1px;
  }
}

.row-label {
  font-size: 12px;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  line-height: 1.3;
}

// Switch stays right-anchored, separated from the stack by the natural row gap.
.item-actions {
  margin-left: 8px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
}

.builtin-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 4px;
  flex-shrink: 0;

  &.agent-emoji {
    font-size: 13px;
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

// Size the generated AgentAvatar (hash-based gradient + letter) to match the
// builtin-avatar and platform-logo so all three rows align perfectly.
.stack-row {
  :deep(.agent-avatar),
  :deep(.agent-avatar-small) {
    width: 20px;
    height: 20px;
  }
  :deep(.agent-avatar-letter) {
    font-size: 10px !important;
  }
}
</style>
