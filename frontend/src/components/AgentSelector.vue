<template>
  <Teleport to="body">
    <div v-if="visible" class="agent-selector-overlay" @click="$emit('close')">
      <div
        class="agent-selector-dropdown"
        :style="dropdownStyle"
        @click.stop
      >
        <div class="agent-selector-header">
          <span>{{ $t('agent.selectAgent') }}</span>
          <router-link to="/platform/agents" class="agent-selector-add" @click="$emit('close')">
            <span class="add-icon">+</span>
            <span class="add-text">{{ $t('agent.manageAgents') }}</span>
          </router-link>
        </div>

        <div class="agent-selector-content" @scroll="hideDetailPanel">
          <!-- 内置智能体 -->
          <div class="agent-group">
            <div class="agent-group-title">{{ $t('agent.builtinAgents') }}</div>
            <div
              v-for="agent in builtinAgents"
              :key="agent.id"
              class="agent-option"
              :class="{ selected: isMyAgentSelected(agent) }"
              @mouseenter="onOptionEnter(agent, $event)"
              @mouseleave="onOptionLeave"
              @click="selectAgent(agent)"
            >
              <div
                v-if="agent.id === BUILTIN_QUICK_ANSWER_ID || agent.id === BUILTIN_SMART_REASONING_ID"
                class="builtin-icon"
                :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'"
              >
                <TIcon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="14px" />
              </div>
              <div v-else-if="agent.avatar" class="builtin-avatar">{{ agent.avatar }}</div>
              <div v-else class="builtin-icon normal">
                <TIcon name="app" size="14px" />
              </div>
              <span class="agent-option-name">{{ agent.name }}</span>
              <div v-if="getAgentNotReadyLabels(agent).length" class="agent-option-actions">
                <t-tooltip
                  :content="$t('agent.selector.notReadyHint', { items: getAgentNotReadyLabels(agent).join('、') })"
                  placement="top"
                >
                  <TIcon name="error-circle" size="14px" class="not-ready-icon" @click.stop />
                </t-tooltip>
              </div>
            </div>
          </div>

          <!-- 自定义智能体 -->
          <div v-if="customAgents.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('agent.customAgents') }}</div>
            <div
              v-for="agent in customAgents"
              :key="agent.id"
              class="agent-option"
              :class="{ selected: isMyAgentSelected(agent) }"
              @mouseenter="onOptionEnter(agent, $event)"
              @mouseleave="onOptionLeave"
              @click="selectAgent(agent)"
            >
              <AgentAvatar :name="agent.name" size="small" />
              <span class="agent-option-name">{{ agent.name }}</span>
              <div v-if="getAgentNotReadyLabels(agent).length" class="agent-option-actions">
                <t-tooltip
                  :content="$t('agent.selector.notReadyHint', { items: getAgentNotReadyLabels(agent).join('、') })"
                  placement="top"
                >
                  <TIcon name="error-circle" size="14px" class="not-ready-icon" @click.stop />
                </t-tooltip>
              </div>
            </div>
          </div>

          <!-- 共享给我 -->
          <div v-if="sharedAgentsList.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('agent.tabs.sharedToMe') }}</div>
            <div
              v-for="shared in sharedAgentsList"
              :key="`${shared.agent.id}-${shared.source_tenant_id}`"
              class="agent-option"
              :class="{ selected: isSharedAgentSelected(shared) }"
              @mouseenter="onSharedOptionEnter(shared, $event)"
              @mouseleave="onOptionLeave"
              @click="selectSharedAgent(shared)"
            >
              <AgentAvatar :name="shared.agent.name" size="small" />
              <span class="agent-option-name">{{ shared.agent.name }}</span>
              <span class="shared-tag">{{ $t('agent.selector.sharedLabel') }}</span>
              <div
                v-if="getAgentNotReadyLabels(shared.agent, String(shared.source_tenant_id)).length"
                class="agent-option-actions"
              >
                <t-tooltip
                  :content="$t('agent.selector.notReadyHint', { items: getAgentNotReadyLabels(shared.agent, String(shared.source_tenant_id)).join('、') })"
                  placement="top"
                >
                  <TIcon name="error-circle" size="14px" class="not-ready-icon" @click.stop />
                </t-tooltip>
              </div>
            </div>
          </div>

          <div
            v-if="builtinAgents.length === 0 && customAgents.length === 0 && sharedAgentsList.length === 0"
            class="agent-option empty"
          >
            {{ $t('agent.noAgents') }}
          </div>
        </div>
      </div>
    </div>

    <!-- 详情浮层 -->
    <div
      v-if="visible && activeDetail"
      class="agent-detail-panel"
      :style="detailPanelStyle"
      @mouseenter="onDetailPanelEnter"
      @mouseleave="onDetailPanelLeave"
      @click.stop
    >
      <div class="agent-detail-panel-inner">
        <div class="agent-detail-content">
          <div class="detail-header">
            <template v-if="activeDetail.agent.id === BUILTIN_QUICK_ANSWER_ID || activeDetail.agent.id === BUILTIN_SMART_REASONING_ID">
              <div
                class="builtin-icon detail-icon"
                :class="activeDetail.agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'"
              >
                <TIcon
                  :name="activeDetail.agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                  size="14px"
                />
              </div>
            </template>
            <div v-else-if="activeDetail.agent.avatar" class="builtin-avatar detail-icon">{{ activeDetail.agent.avatar }}</div>
            <AgentAvatar v-else :name="activeDetail.agent.name" size="small" />
            <div class="detail-title-wrap">
              <div class="detail-title-row">
                <span class="detail-name">{{ activeDetail.agent.name }}</span>
                <button
                  type="button"
                  class="detail-header-action"
                  :class="{ 'detail-header-action--warn': activeDetailNotReadyLabels.length }"
                  :title="activeDetailNotReadyLabels.length
                    ? $t('agent.selector.configureAction')
                    : $t('agent.selector.goToSettings')"
                  @click="goToSettings(activeDetail.agent, activeDetail.sourceTenantId)"
                >
                  <TIcon
                    :name="activeDetailNotReadyLabels.length ? 'jump' : 'setting'"
                    size="14px"
                  />
                </button>
              </div>
              <span v-if="isDetailCurrent" class="detail-current">{{ $t('agent.selector.current') }}</span>
              <div v-else-if="activeDetailNotReadyLabels.length" class="detail-not-ready">
                <TIcon name="error-circle" size="13px" class="detail-not-ready-icon" />
                <span class="detail-not-ready-label">{{ $t('agent.selector.notReadyStatus') }}</span>
                <span
                  v-for="item in activeDetailNotReadyLabels"
                  :key="item"
                  class="detail-not-ready-item"
                >{{ item }}</span>
              </div>
            </div>
          </div>

          <p class="detail-desc">{{ activeDetail.agent.description || $t('agent.noDescription') }}</p>

          <div class="detail-tags">
            <span class="detail-tag">
              {{ activeDetail.agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.type.agent') : $t('agent.type.normal') }}
            </span>
            <span v-if="getKbCapability(activeDetail.agent)" class="detail-tag">{{ getKbCapability(activeDetail.agent) }}</span>
            <span v-if="activeDetail.agent.config?.web_search_enabled" class="detail-tag">{{ $t('agent.capabilities.webSearchOn') }}</span>
            <span v-if="getMcpCapability(activeDetail.agent)" class="detail-tag">{{ getMcpCapability(activeDetail.agent) }}</span>
            <span v-if="activeDetail.agent.config?.multi_turn_enabled" class="detail-tag">{{ $t('agent.capabilities.multiTurn') }}</span>
          </div>

          <div v-if="activeDetail.sharedMeta?.org_name || activeDetail.sharedMeta?.shared_by_username" class="detail-meta">
            <div v-if="activeDetail.sharedMeta.org_name" class="detail-meta-row">
              <img src="@/assets/img/organization-green.svg" class="detail-meta-icon" alt="" aria-hidden="true" />
              <span>{{ activeDetail.sharedMeta.org_name }}</span>
            </div>
            <div v-if="activeDetail.sharedMeta.shared_by_username" class="detail-meta-row">
              <img src="@/assets/img/user.svg" class="detail-meta-icon" alt="" aria-hidden="true" />
              <span>{{ activeDetail.sharedMeta.shared_by_username }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { Icon as TIcon, Tooltip as TTooltip } from 'tdesign-vue-next';
import { type CustomAgent, BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from '@/api/agent';
import AgentAvatar from '@/components/AgentAvatar.vue';
import { useOrganizationStore } from '@/stores/organization';
import { useSettingsStore } from '@/stores/settings';
import type { SharedAgentInfo } from '@/api/organization';
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom';
import { type ModelConfig } from '@/api/model';
import {
  getAgentNotReadyReasonKeys,
  resolveAgentNotReadySection,
  resolveAgentNotReadyHighlight,
  type AgentNotReadyReasonKey,
} from '@/utils/agent-readiness';

const { t } = useI18n();
const router = useRouter();
const orgStore = useOrganizationStore();
const settingsStore = useSettingsStore();

const props = defineProps<{
  visible: boolean;
  anchorEl?: HTMLElement;
  currentAgentId: string;
  agents?: CustomAgent[];
  allModels?: ModelConfig[];
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'select', agent: CustomAgent, sourceTenantId?: string): void;
}>();

type AgentDetailTarget = {
  agent: CustomAgent;
  sourceTenantId?: string;
  sharedMeta?: { org_name?: string; shared_by_username?: string };
};

const dropdownStyle = ref<Record<string, string>>({});
const activeDetail = ref<AgentDetailTarget | null>(null);
const detailAnchorEl = ref<HTMLElement | null>(null);
const detailPanelStyle = ref<Record<string, string>>({});
let detailHideTimer: ReturnType<typeof setTimeout> | null = null;

const agentsList = computed(() => props.agents ?? []);

const builtinAgents = computed(() => {
  const apiBuiltins = agentsList.value.filter(a => a.is_builtin);
  return apiBuiltins.map(agent => {
    if (agent.id === BUILTIN_QUICK_ANSWER_ID) {
      return { ...agent, name: t('input.normalMode'), description: t('input.normalModeDesc') };
    }
    if (agent.id === BUILTIN_SMART_REASONING_ID) {
      return { ...agent, name: t('input.agentMode'), description: t('input.agentModeDesc') };
    }
    return agent;
  });
});

const customAgents = computed(() => agentsList.value.filter(a => !a.is_builtin));

const sharedAgentsList = computed<SharedAgentInfo[]>(() =>
  (orgStore.sharedAgents || []).filter(shared => !shared.disabled_by_me),
);

const currentAgentSourceTenantId = computed(() => settingsStore.selectedAgentSourceTenantId ?? null);

const isSharedAgentSelected = (shared: SharedAgentInfo) =>
  props.currentAgentId === shared.agent.id && currentAgentSourceTenantId.value === String(shared.source_tenant_id);

const isMyAgentSelected = (agent: CustomAgent) =>
  props.currentAgentId === agent.id && !currentAgentSourceTenantId.value;

const isDetailCurrent = computed(() => {
  const detail = activeDetail.value;
  if (!detail) return false;
  if (detail.sourceTenantId) {
    return props.currentAgentId === detail.agent.id
      && currentAgentSourceTenantId.value === detail.sourceTenantId;
  }
  return isMyAgentSelected(detail.agent);
});

const activeDetailNotReadyLabels = computed(() => {
  const detail = activeDetail.value;
  if (!detail) return [];
  return getAgentNotReadyLabels(detail.agent, detail.sourceTenantId);
});

const getKbCapability = (agent: CustomAgent): string => {
  const config = agent.config || {};
  if (config.kb_selection_mode === 'none') return '';
  if (config.knowledge_bases && config.knowledge_bases.length > 0) {
    return t('agent.capabilities.kbCount', { count: config.knowledge_bases.length });
  }
  if (config.kb_selection_mode === 'all') return t('agent.capabilities.kbAll');
  return '';
};

const getMcpCapability = (agent: CustomAgent): string => {
  const config = agent.config || {};
  if (config.mcp_selection_mode === 'none' || (!config.mcp_services?.length && config.mcp_selection_mode !== 'all')) {
    return '';
  }
  if (config.mcp_selection_mode === 'all') return t('agent.shareScope.mcpAll');
  if (config.mcp_services?.length) {
    return t('agent.shareScope.mcpSelected', { count: config.mcp_services.length });
  }
  return t('agent.capabilities.mcpEnabled');
};

const modelsList = computed(() => props.allModels ?? []);

const formatAgentNotReadyReasons = (
  reasonKeys: AgentNotReadyReasonKey[],
  isBuiltin: boolean,
): string[] => {
  return reasonKeys.map((key) => {
    if (key === 'summary_model') {
      return isBuiltin ? t('input.agentMissingSummaryModel') : t('input.customAgentMissingSummaryModel');
    }
    if (key === 'rerank_model') {
      return isBuiltin ? t('input.agentMissingRerankModel') : t('input.customAgentMissingRerankModel');
    }
    return t('input.agentMissingAllowedTools');
  });
};

const getAgentNotReadyReasonKeysFor = (agent: CustomAgent, sourceTenantId?: string) => {
  const isAgentMode = agent.config?.agent_mode === 'smart-reasoning';
  const isSharedAgent = !!sourceTenantId;
  return getAgentNotReadyReasonKeys(agent.config, modelsList.value, {
    isAgentMode,
    isSharedAgent,
  });
};

const getAgentNotReadyLabels = (agent: CustomAgent, sourceTenantId?: string): string[] => {
  return formatAgentNotReadyReasons(
    getAgentNotReadyReasonKeysFor(agent, sourceTenantId),
    agent.is_builtin,
  );
};

const clearDetailHideTimer = () => {
  if (detailHideTimer) {
    clearTimeout(detailHideTimer);
    detailHideTimer = null;
  }
};

const updateDetailPanelPosition = () => {
  const el = detailAnchorEl.value;
  if (!el || !activeDetail.value) return;

  const zoom = getRootZoom();
  const rect = rectToCssPx(el.getBoundingClientRect(), zoom);
  const { width: vw, height: vh } = cssViewportSize(zoom);
  const panelWidth = 200;
  const gap = 6;
  let left = rect.right + gap;
  let top = rect.top;

  if (left + panelWidth > vw - 8) {
    left = Math.max(8, vw - panelWidth - 8);
  }
  if (top + 240 > vh - 8) {
    top = Math.max(8, vh - 248);
  }

  detailPanelStyle.value = {
    position: 'fixed',
    left: `${Math.round(left)}px`,
    top: `${Math.round(top)}px`,
    width: `${panelWidth}px`,
    zIndex: '10002',
  };
};

const onOptionEnter = (agent: CustomAgent, event: MouseEvent, sourceTenantId?: string, sharedMeta?: AgentDetailTarget['sharedMeta']) => {
  clearDetailHideTimer();
  detailAnchorEl.value = event.currentTarget as HTMLElement;
  activeDetail.value = { agent, sourceTenantId, sharedMeta };
  nextTick(() => updateDetailPanelPosition());
};

const onSharedOptionEnter = (shared: SharedAgentInfo, event: MouseEvent) => {
  onOptionEnter(shared.agent as CustomAgent, event, String(shared.source_tenant_id), {
    org_name: shared.org_name,
    shared_by_username: shared.shared_by_username,
  });
};

const onOptionLeave = () => {
  detailHideTimer = setTimeout(() => {
    activeDetail.value = null;
    detailAnchorEl.value = null;
  }, 300);
};

const onDetailPanelEnter = () => {
  clearDetailHideTimer();
};

const onDetailPanelLeave = () => {
  onOptionLeave();
};

const hideDetailPanel = () => {
  clearDetailHideTimer();
  activeDetail.value = null;
  detailAnchorEl.value = null;
};

const selectAgent = (agent: CustomAgent) => {
  if (getAgentNotReadyLabels(agent).length > 0) return;
  emit('select', agent);
};

const selectSharedAgent = (shared: SharedAgentInfo) => {
  const sourceTenantId = String(shared.source_tenant_id);
  if (getAgentNotReadyLabels(shared.agent, sourceTenantId).length > 0) return;
  emit('select', shared.agent as CustomAgent, sourceTenantId);
};

const goToSettings = (agent: CustomAgent, sourceTenantId?: string) => {
  const reasonKeys = getAgentNotReadyReasonKeysFor(agent, sourceTenantId);
  const section = reasonKeys.length > 0 ? resolveAgentNotReadySection(reasonKeys) : 'basic';
  const highlight = resolveAgentNotReadyHighlight(reasonKeys);
  hideDetailPanel();
  emit('close');
  router.push({
    path: '/platform/agents',
    query: {
      edit: agent.id,
      section,
      ...(highlight ? { highlight } : {}),
    },
  });
};

const updateDropdownPosition = () => {
  if (!props.anchorEl) return;

  const zoom = getRootZoom();
  const rect = rectToCssPx(props.anchorEl.getBoundingClientRect(), zoom);
  const { width: vw, height: vh } = cssViewportSize(zoom);

  const dropdownWidth = 240;
  const offsetY = 8;

  let left = Math.floor(rect.left);
  const minLeft = 16;
  const maxLeft = Math.max(16, vw - dropdownWidth - 16);
  left = Math.max(minLeft, Math.min(maxLeft, left));

  const preferredDropdownHeight = 320;
  const minDropdownHeight = 100;
  const topMargin = 20;
  const spaceBelow = vh - rect.bottom;
  const spaceAbove = rect.top;

  let actualHeight: number;

  if (spaceBelow >= minDropdownHeight + offsetY) {
    actualHeight = Math.min(preferredDropdownHeight, spaceBelow - offsetY - 16);
    dropdownStyle.value = {
      position: 'fixed',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      top: `${Math.floor(rect.bottom + offsetY)}px`,
      maxHeight: `${actualHeight}px`,
      zIndex: '10001',
    };
  } else {
    const availableHeight = spaceAbove - offsetY - topMargin;
    actualHeight = availableHeight >= preferredDropdownHeight
      ? preferredDropdownHeight
      : Math.max(minDropdownHeight, availableHeight);

    dropdownStyle.value = {
      position: 'fixed',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      bottom: `${vh - rect.top + offsetY}px`,
      maxHeight: `${actualHeight}px`,
      zIndex: '10001',
    };
  }
};

watch(() => props.visible, (newVal) => {
  if (newVal) {
    nextTick(() => updateDropdownPosition());
  } else {
    hideDetailPanel();
  }
});
</script>

<style scoped lang="less">
.agent-selector-overlay,
.agent-selector-overlay *,
.agent-selector-overlay *::before,
.agent-selector-overlay *::after {
  box-sizing: border-box;
}

.agent-selector-overlay {
  position: fixed;
  inset: 0;
  z-index: 10000;
  background: transparent;
  touch-action: none;
}

.agent-selector-dropdown {
  position: fixed !important;
  background: var(--td-bg-color-container);
  border: .5px solid var(--td-component-border);
  border-radius: 10px;
  box-shadow: var(--td-shadow-2);
  display: flex;
  flex-direction: column;
  z-index: 10001;
  margin: 0;
  transform-origin: top left;
  animation: agentSelectorFadeIn 0.15s ease-out;
}

@keyframes agentSelectorFadeIn {
  from { opacity: 0; transform: scale(0.98); }
  to { opacity: 1; transform: scale(1); }
}

.agent-selector-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-bottom: .5px solid var(--td-component-stroke);
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.agent-selector-add {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 6px;
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  text-decoration: none;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.agent-selector-content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
  padding: 6px 8px;
}

.agent-group {
  &:not(:last-child) {
    margin-bottom: 6px;
    padding-bottom: 6px;
    border-bottom: .5px solid var(--td-component-stroke);
  }
}

.agent-group-title {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  padding: 4px 8px 6px;
  font-weight: 600;
}

.agent-option {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  cursor: pointer;
  transition: background 0.12s;
  border-radius: 6px;
  margin-bottom: 4px;

  &:last-child {
    margin-bottom: 0;
  }

  &:hover,
  &.selected {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.empty {
    color: var(--td-text-color-placeholder);
    cursor: default;
    text-align: center;
    padding: 20px 8px;

    &:hover {
      background: transparent;
    }
  }
}

.agent-option-name {
  font-size: 12px;
  color: var(--td-text-color-primary);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.4;
}

.shared-tag {
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.agent-option-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.not-ready-icon {
  flex-shrink: 0;
  color: var(--td-warning-color, #ed7b2f);
  cursor: default;
}

.builtin-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  flex-shrink: 0;

  &.normal {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color-active);
  }

  &.agent {
    background: rgba(124, 77, 255, 0.1);
    color: var(--td-brand-color);
  }

  &.detail-icon {
    width: 28px;
    height: 28px;
  }
}

.builtin-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  flex-shrink: 0;
  font-size: 16px;
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);

  &.detail-icon {
    width: 28px;
    height: 28px;
    font-size: 18px;
  }
}

/* 详情浮层 */
.agent-detail-panel {
  box-sizing: border-box;
}

.agent-detail-panel-inner {
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-large, 9px);
  background: var(--td-bg-color-container);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.1), 0 2px 8px rgba(0, 0, 0, 0.04);
}

.agent-detail-content {
  font-size: 12px;
  color: var(--td-text-color-primary);
  line-height: 1.5;
}

.detail-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.detail-title-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.detail-title-row {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.detail-name {
  font-weight: 600;
  font-size: 13px;
  line-height: 18px;
  word-break: break-word;
  min-width: 0;
  flex: 1;
}

.detail-settings-icon,
.detail-header-action {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-component-hover, #e8e8e8);
    color: var(--td-text-color-secondary);
  }

  &--warn {
    color: var(--td-warning-color, #ed7b2f);

    &:hover {
      background: rgba(237, 123, 47, 0.1);
      color: var(--td-warning-color, #ed7b2f);
    }
  }
}

.detail-current {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.detail-not-ready {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 1px;
}

.detail-not-ready-icon {
  flex-shrink: 0;
  color: var(--td-warning-color, #ed7b2f);
  opacity: 0.85;
}

.detail-not-ready-label {
  font-size: 11px;
  line-height: 16px;
  color: var(--td-text-color-secondary);
}

.detail-not-ready-item {
  display: inline-flex;
  align-items: center;
  padding: 0 5px;
  border: 1px solid rgba(237, 123, 47, 0.28);
  border-radius: 4px;
  font-size: 10px;
  line-height: 16px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-container);
}

.detail-desc {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-word;
}

.detail-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 8px;
  border-top: .5px solid var(--td-component-stroke);
}

.detail-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium, 6px);
  font-size: 11px;
  line-height: 18px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
}

.detail-meta {
  margin-top: 8px;
  padding-top: 8px;
  border-top: .5px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.detail-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.detail-meta-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}
</style>
