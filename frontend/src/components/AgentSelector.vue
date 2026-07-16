<template>
  <Teleport to="body">
    <div v-if="visible" class="agent-selector-overlay" @click="$emit('close')">
      <div class="agent-selector-dropdown" :style="dropdownStyle" @click.stop>
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
            <div v-for="agent in builtinAgents" :key="agent.id" class="agent-option"
              :class="{ selected: isMyAgentSelected(agent) }" @mouseenter="onOptionEnter(agent, $event)"
              @mouseleave="onOptionLeave" @click="selectAgent(agent)">
              <div v-if="agent.id === BUILTIN_QUICK_ANSWER_ID || agent.id === BUILTIN_SMART_REASONING_ID"
                class="builtin-icon" :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
                <TIcon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                  size="13px" />
              </div>
              <div v-else-if="agent.avatar" class="builtin-avatar">{{ agent.avatar }}</div>
              <div v-else class="builtin-icon normal">
                <TIcon name="app" size="13px" />
              </div>
              <span class="agent-option-name">{{ agent.name }}</span>
              <div v-if="getAgentNotReadyLabels(agent).length" class="agent-option-actions">
                <t-tooltip :content="$t('agent.selector.notReadyHint', { items: formatNotReadyHint(agent) })"
                  placement="top">
                  <TIcon name="error-circle" size="14px" class="not-ready-icon" @click.stop />
                </t-tooltip>
              </div>
            </div>
          </div>

          <!-- 自定义智能体 -->
          <div v-if="customAgents.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('agent.customAgents') }}</div>
            <div v-for="agent in customAgents" :key="agent.id" class="agent-option"
              :class="{ selected: isMyAgentSelected(agent) }" @mouseenter="onOptionEnter(agent, $event)"
              @mouseleave="onOptionLeave" @click="selectAgent(agent)">
              <AgentAvatar :name="agent.name" size="small" />
              <span class="agent-option-name">{{ agent.name }}</span>
              <div v-if="getAgentNotReadyLabels(agent).length" class="agent-option-actions">
                <t-tooltip :content="$t('agent.selector.notReadyHint', { items: formatNotReadyHint(agent) })"
                  placement="top">
                  <TIcon name="error-circle" size="14px" class="not-ready-icon" @click.stop />
                </t-tooltip>
              </div>
            </div>
          </div>

          <!-- 共享给我 -->
          <div v-if="sharedAgentsList.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('agent.tabs.sharedToMe') }}</div>
            <div v-for="shared in sharedAgentsList" :key="`${shared.agent.id}-${shared.source_tenant_id}`"
              class="agent-option" :class="{ selected: isSharedAgentSelected(shared) }"
              @mouseenter="onSharedOptionEnter(shared, $event)" @mouseleave="onOptionLeave"
              @click="selectSharedAgent(shared)">
              <AgentAvatar :name="shared.agent.name" size="small" />
              <span class="agent-option-name">{{ shared.agent.name }}</span>
              <span class="shared-tag">{{ $t('agent.selector.sharedLabel') }}</span>
              <div v-if="getAgentNotReadyLabels(shared.agent, String(shared.source_tenant_id)).length"
                class="agent-option-actions">
                <t-tooltip
                  :content="$t('agent.selector.notReadyHint', { items: formatNotReadyHint(shared.agent, String(shared.source_tenant_id)) })"
                  placement="top">
                  <TIcon name="error-circle" size="14px" class="not-ready-icon" @click.stop />
                </t-tooltip>
              </div>
            </div>
          </div>

          <div v-if="builtinAgents.length === 0 && customAgents.length === 0 && sharedAgentsList.length === 0"
            class="agent-option empty">
            {{ $t('agent.noAgents') }}
          </div>
        </div>
      </div>
    </div>

    <!-- 详情浮层 -->
    <div v-if="visible && activeDetail" ref="detailPanelRef" class="agent-detail-panel" :style="detailPanelStyle"
      @mouseenter="onDetailPanelEnter" @mouseleave="onDetailPanelLeave" @click.stop>
      <div class="agent-detail-panel-inner">
        <div class="agent-detail-content">
          <div class="detail-header">
            <template
              v-if="activeDetail.agent.id === BUILTIN_QUICK_ANSWER_ID || activeDetail.agent.id === BUILTIN_SMART_REASONING_ID">
              <div class="builtin-icon detail-icon"
                :class="activeDetail.agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
                <TIcon :name="activeDetail.agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                  size="14px" />
              </div>
            </template>
            <div v-else-if="activeDetail.agent.avatar" class="builtin-avatar detail-icon">{{ activeDetail.agent.avatar
              }}</div>
            <AgentAvatar v-else :name="activeDetail.agent.name" size="small" />
            <div class="detail-title-wrap">
              <div class="detail-title-row">
                <span class="detail-name">{{ activeDetail.agent.name }}</span>
                <button v-if="canShowDetailHeaderAction" type="button" class="detail-header-action"
                  :class="{ 'detail-header-action--warn': activeDetailNotReadyLabels.length }" :title="activeDetailNotReadyLabels.length
                    ? $t('agent.selector.configureAction')
                    : $t('agent.selector.goToSettings')"
                  @click="goToSettings(activeDetail.agent, activeDetail.sourceTenantId)">
                  <TIcon :name="activeDetailNotReadyLabels.length ? 'jump' : 'setting'" size="14px" />
                </button>
              </div>
              <span v-if="isDetailCurrent" class="detail-current">{{ $t('agent.selector.current') }}</span>
              <div v-else-if="activeDetailNotReadyLabels.length" class="detail-not-ready">
                <TIcon name="error-circle" size="13px" class="detail-not-ready-icon" />
                <span class="detail-not-ready-label">{{ $t('agent.selector.notReadyStatus') }}</span>
                <span v-for="item in activeDetailNotReadyLabels" :key="item" class="detail-not-ready-item">{{ item
                  }}</span>
                <span v-if="activeDetail.sourceTenantId && activeDetailNotReadyLabels.length"
                  class="detail-not-ready-shared-hint">{{ $t('agent.selector.sharedNotReadyContact') }}</span>
              </div>
            </div>
          </div>

          <p class="detail-desc">{{ activeDetail.agent.description || $t('agent.noDescription') }}</p>

          <div class="detail-tags">
            <span class="detail-tag">
              {{ activeDetail.agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.type.agent') :
                $t('agent.type.normal') }}
            </span>
            <span v-if="getKbCapability(activeDetail.agent)" class="detail-tag">{{ getKbCapability(activeDetail.agent)
              }}</span>
            <span v-if="getMcpCapability(activeDetail.agent)" class="detail-tag">{{ getMcpCapability(activeDetail.agent)
              }}</span>
            <span v-if="activeDetail.agent.config?.multi_turn_enabled" class="detail-tag">{{
              $t('agent.capabilities.multiTurn')
              }}</span>
          </div>

          <div class="detail-tag-group">
            <div class="detail-tag-group-title">{{ $t('agent.selector.capabilitiesSection') }}</div>
            <div class="detail-tags detail-tags--capabilities">
              <span class="detail-tag detail-capability-tag"
                :class="getWebSearchCapabilityClass(activeDetail.agent)">
                <span class="detail-capability-icon-wrap">
                  <TIcon :name="getWebSearchCapabilityIcon(activeDetail.agent)" size="10px"
                    class="detail-capability-icon" />
                </span>
                <span class="detail-capability-name">{{ $t('agent.selector.webSearchCapability') }}</span>
                <span class="detail-capability-state">{{ getWebSearchCapabilityState(activeDetail.agent) }}</span>
              </span>
              <span class="detail-tag detail-capability-tag"
                :class="isImageUploadEnabledForAgent(activeDetail.agent) ? 'detail-capability-tag--on' : 'detail-capability-tag--off'">
                <span class="detail-capability-icon-wrap">
                  <TIcon :name="isImageUploadEnabledForAgent(activeDetail.agent) ? 'check' : 'close'" size="10px"
                    class="detail-capability-icon" />
                </span>
                <span class="detail-capability-name">{{ $t('agent.selector.imageUploadCapability') }}</span>
                <span class="detail-capability-state">{{ getImageUploadCapabilityState(activeDetail.agent) }}</span>
              </span>
            </div>
          </div>

          <div v-if="activeDetail.sharedMeta?.org_name || activeDetail.sharedMeta?.shared_by_username"
            class="detail-meta">
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
  canLocallyConfigureAgent,
  type AgentNotReadyReasonKey,
} from '@/utils/agent-readiness';
import { formatLocalizedList } from '@/utils/format-list';
import { useChatResourcesStore } from '@/stores/chatResources';
import {
  isAgentWebSearchEnabled,
  isAgentWebSearchReady,
} from '@/utils/agentWebSearch';

const { t, locale } = useI18n();
const router = useRouter();
const orgStore = useOrganizationStore();
const settingsStore = useSettingsStore();
const chatResources = useChatResourcesStore();

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
  (e: 'not-ready', agent: CustomAgent, labels: string[], keys: AgentNotReadyReasonKey[], sourceTenantId?: string): void;
}>();

type AgentDetailTarget = {
  agent: CustomAgent;
  sourceTenantId?: string;
  sharedMeta?: { org_name?: string; shared_by_username?: string };
};

type SharedAgentSelection = Omit<SharedAgentInfo, 'agent'> & {
  agent: CustomAgent;
};

const dropdownStyle = ref<Record<string, string>>({});
const activeDetail = ref<AgentDetailTarget | null>(null);
const detailAnchorEl = ref<HTMLElement | null>(null);
const detailPanelRef = ref<HTMLElement | null>(null);
const detailPanelStyle = ref<Record<string, string>>({});
let detailHideTimer: ReturnType<typeof setTimeout> | null = null;

const DETAIL_PANEL_WIDTH = 200;
const DETAIL_PANEL_GAP = 8;
const DETAIL_HIDE_DELAY_MS = 400;

const agentsList = computed(() => props.agents ?? []);
const webSearchProviders = computed(() => chatResources.webSearchProviders);

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

const toCustomAgent = (agent: SharedAgentInfo['agent']): CustomAgent => ({
  is_builtin: false,
  config: {},
  ...agent,
});

const sharedAgentsList = computed<SharedAgentSelection[]>(() =>
  (orgStore.sharedAgents || [])
    .filter(shared => !shared.disabled_by_me)
    .map(shared => ({ ...shared, agent: toCustomAgent(shared.agent) })),
);

const currentAgentSourceTenantId = computed(() => settingsStore.selectedAgentSourceTenantId ?? null);

const isSharedAgentSelected = (shared: SharedAgentSelection) =>
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

const canShowDetailHeaderAction = computed(() => {
  const detail = activeDetail.value;
  if (!detail) return false;
  if (canLocallyConfigureAgent(detail.sourceTenantId)) return true;
  return activeDetailNotReadyLabels.value.length === 0;
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

const isWebSearchEnabledForAgent = (agent: CustomAgent): boolean => {
  return isAgentWebSearchEnabled(agent.config);
};

const isWebSearchReadyForAgent = (agent: CustomAgent): boolean => {
  return isAgentWebSearchReady(agent.config, webSearchProviders.value);
};

const isImageUploadEnabledForAgent = (agent: CustomAgent): boolean => {
  const config = agent.config || {};
  return config.image_upload_enabled === true;
};

const getWebSearchCapabilityClass = (agent: CustomAgent): string => {
  if (isWebSearchReadyForAgent(agent)) return 'detail-capability-tag--on';
  if (isWebSearchEnabledForAgent(agent)) return 'detail-capability-tag--warning';
  return 'detail-capability-tag--off';
};

const getWebSearchCapabilityIcon = (agent: CustomAgent): string => {
  if (isWebSearchReadyForAgent(agent)) return 'check';
  if (isWebSearchEnabledForAgent(agent)) return 'error-circle';
  return 'close';
};

const getWebSearchCapabilityState = (agent: CustomAgent): string => {
  if (isWebSearchReadyForAgent(agent)) return t('agent.selector.capabilityEnabled');
  if (isWebSearchEnabledForAgent(agent)) return t('agent.selector.capabilityUnconfigured');
  return t('agent.selector.capabilityDisabled');
};

const getImageUploadCapabilityState = (agent: CustomAgent): string => {
  return isImageUploadEnabledForAgent(agent)
    ? t('agent.selector.capabilitySupported')
    : t('agent.selector.capabilityUnsupported');
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

const formatNotReadyHint = (agent: CustomAgent, sourceTenantId?: string): string => {
  return formatLocalizedList(getAgentNotReadyLabels(agent, sourceTenantId), locale.value);
};

const emitAgentNotReady = (agent: CustomAgent, sourceTenantId?: string) => {
  const keys = getAgentNotReadyReasonKeysFor(agent, sourceTenantId);
  const labels = formatAgentNotReadyReasons(keys, agent.is_builtin);
  emit('not-ready', agent, labels, keys, sourceTenantId);
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
  const rowRect = rectToCssPx(el.getBoundingClientRect(), zoom);
  const { width: vw, height: vh } = cssViewportSize(zoom);

  // 浮层显示在列表右侧，保留小间隙；透明桥接区覆盖间隙，避免鼠标移入时浮层消失
  let left = rowRect.right + DETAIL_PANEL_GAP;
  if (left + DETAIL_PANEL_WIDTH > vw - 8) {
    left = Math.max(8, vw - DETAIL_PANEL_WIDTH - 8);
  }

  const panelHeight = detailPanelRef.value?.offsetHeight || 180;
  const rowCenter = rowRect.top + rowRect.height / 2;
  let top = rowCenter - panelHeight / 2;

  const minTop = 8;
  const maxTop = vh - panelHeight - 8;
  if (top < minTop) {
    top = minTop;
  } else if (top > maxTop) {
    // 贴近当前行：优先让浮层与 hover 行在垂直方向仍有交集
    top = Math.min(rowRect.top, maxTop);
    top = Math.max(minTop, Math.min(top, rowRect.bottom - panelHeight));
    if (top + panelHeight < rowRect.top) {
      top = rowRect.top;
    }
    top = Math.max(minTop, Math.min(top, maxTop));
  }

  detailPanelStyle.value = {
    position: 'fixed',
    left: `${Math.round(left)}px`,
    top: `${Math.round(top)}px`,
    width: `${DETAIL_PANEL_WIDTH}px`,
    zIndex: '10002',
  };
};

const scheduleDetailPanelPosition = () => {
  nextTick(() => {
    updateDetailPanelPosition();
    requestAnimationFrame(() => updateDetailPanelPosition());
  });
};

const onOptionEnter = (agent: CustomAgent, event: MouseEvent, sourceTenantId?: string, sharedMeta?: AgentDetailTarget['sharedMeta']) => {
  clearDetailHideTimer();
  detailAnchorEl.value = event.currentTarget as HTMLElement;
  activeDetail.value = { agent, sourceTenantId, sharedMeta };
  scheduleDetailPanelPosition();
};

const onSharedOptionEnter = (shared: SharedAgentSelection, event: MouseEvent) => {
  onOptionEnter(shared.agent, event, String(shared.source_tenant_id), {
    org_name: shared.org_name,
    shared_by_username: shared.shared_by_username,
  });
};

const onOptionLeave = () => {
  detailHideTimer = setTimeout(() => {
    activeDetail.value = null;
    detailAnchorEl.value = null;
  }, DETAIL_HIDE_DELAY_MS);
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
  if (getAgentNotReadyLabels(agent).length > 0) {
    emitAgentNotReady(agent);
    return;
  }
  emit('select', agent);
};

const selectSharedAgent = (shared: SharedAgentSelection) => {
  const sourceTenantId = String(shared.source_tenant_id);
  if (getAgentNotReadyLabels(shared.agent, sourceTenantId).length > 0) {
    emitAgentNotReady(shared.agent, sourceTenantId);
    return;
  }
  emit('select', shared.agent, sourceTenantId);
};

const goToSettings = (agent: CustomAgent, sourceTenantId?: string) => {
  if (!canLocallyConfigureAgent(sourceTenantId) && getAgentNotReadyLabels(agent, sourceTenantId).length > 0) {
    return;
  }
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
      ...(sourceTenantId ? { sourceTenantId } : {}),
    },
  });
};

const updateDropdownPosition = () => {
  if (!props.anchorEl) return;

  const zoom = getRootZoom();
  const rect = rectToCssPx(props.anchorEl.getBoundingClientRect(), zoom);
  const { width: vw, height: vh } = cssViewportSize(zoom);

  const dropdownWidth = 220;
  const offsetY = 6;

  let left = Math.floor(rect.left);
  const minLeft = 16;
  const maxLeft = Math.max(16, vw - dropdownWidth - 16);
  left = Math.max(minLeft, Math.min(maxLeft, left));

  const preferredDropdownHeight = 280;
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
    chatResources.ensureWebSearchProviders();
  } else {
    hideDetailPanel();
  }
});

watch(activeDetail, (detail) => {
  if (detail) {
    scheduleDetailPanelPosition();
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
  border-radius: 8px;
  box-shadow: var(--td-shadow-2);
  display: flex;
  flex-direction: column;
  z-index: 10001;
  margin: 0;
  transform-origin: top left;
  animation: agentSelectorFadeIn 0.15s ease-out;
}

@keyframes agentSelectorFadeIn {
  from {
    opacity: 0;
    transform: scale(0.98);
  }

  to {
    opacity: 1;
    transform: scale(1);
  }
}

.agent-selector-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 36px;
  padding: 8px 12px;
  border-bottom: .5px solid var(--td-component-stroke);
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  color: var(--td-text-color-secondary);
}

.agent-selector-add {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  height: 22px;
  padding: 0 6px;
  border-radius: 5px;
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  cursor: pointer;
  text-decoration: none;
  flex-shrink: 0;

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
  padding: 6px 4px 8px;
}

.agent-group {
  &:not(:last-child) {
    margin-bottom: 4px;
    padding-bottom: 4px;
    border-bottom: .5px solid var(--td-component-stroke);
  }
}

.agent-group-title {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  padding: 8px 12px 4px;
  font-weight: 600;
  line-height: 16px;
}

.agent-option {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  margin: 0 4px;
  padding: 6px 8px;
  cursor: pointer;
  transition: background 0.12s;
  border-radius: 5px;

  &:hover,
  &.selected {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.empty {
    color: var(--td-text-color-placeholder);
    cursor: default;
    text-align: center;
    padding: 16px 8px;
    min-height: auto;

    &:hover {
      background: transparent;
    }
  }

  :deep(.agent-avatar-small) {
    width: 22px;
    height: 22px;
    border-radius: 5px;
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
  line-height: 22px;
}

.shared-tag {
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
  line-height: 22px;
}

.agent-option-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
}

.not-ready-icon {
  flex-shrink: 0;
  color: var(--td-warning-color, #ed7b2f);
  cursor: default;
  display: block;
  line-height: 1;
}

.builtin-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 5px;
  flex-shrink: 0;
  line-height: 1;

  :deep(.t-icon) {
    display: block;
    line-height: 1;
  }

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
  width: 22px;
  height: 22px;
  border-radius: 5px;
  flex-shrink: 0;
  font-size: 14px;
  line-height: 1;
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);
  overflow: hidden;

  &.detail-icon {
    width: 28px;
    height: 28px;
    font-size: 18px;
  }
}

/* 详情浮层 */
.agent-detail-panel {
  box-sizing: border-box;
  position: relative;
  --detail-panel-gap: 8px;
  --detail-bridge-width: 8px;

  // 左侧透明桥接区：承接从选项移入的鼠标，避免经过间隙时浮层消失
  &::before {
    content: '';
    position: absolute;
    left: calc(-1 * (var(--detail-bridge-width) + var(--detail-panel-gap)));
    top: 0;
    width: calc(var(--detail-bridge-width) + var(--detail-panel-gap));
    height: 100%;
  }
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

.detail-not-ready-shared-hint {
  display: block;
  width: 100%;
  margin-top: 2px;
  font-size: 10px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
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

  &--capabilities {
    padding-top: 0;
    border-top: none;
  }
}

.detail-tag-group {
  margin-top: 8px;
  padding-top: 8px;
  border-top: .5px solid var(--td-component-stroke);
}

.detail-tag-group-title {
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
  color: var(--td-text-color-placeholder);
  margin-bottom: 6px;
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

.detail-capability-tag {
  gap: 5px;
  max-width: 100%;
  padding: 3px 7px 3px 5px;

  .detail-capability-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--td-text-color-secondary);
    font-weight: 500;
  }

  .detail-capability-state {
    flex-shrink: 0;
    font-size: 10px;
    line-height: 14px;
    color: var(--td-text-color-placeholder);
  }
}

.detail-capability-tag--on {
  border-color: rgba(0, 168, 112, 0.2);
  background: rgba(0, 168, 112, 0.06);

  .detail-capability-icon-wrap {
    color: var(--td-success-color, #00a870);
    background: rgba(0, 168, 112, 0.12);
  }

  .detail-capability-icon {
    color: var(--td-success-color, #00a870);
  }

  .detail-capability-state {
    color: var(--td-success-color, #00a870);
  }
}

.detail-capability-tag--warning {
  border-color: rgba(237, 123, 47, 0.22);
  background: rgba(237, 123, 47, 0.06);

  .detail-capability-icon-wrap {
    color: var(--td-warning-color, #ed7b2f);
    background: rgba(237, 123, 47, 0.12);
  }

  .detail-capability-icon,
  .detail-capability-state {
    color: var(--td-warning-color, #ed7b2f);
  }
}

.detail-capability-tag--off {
  border-color: var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);

  .detail-capability-icon-wrap {
    color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-component, rgba(0, 0, 0, 0.04));
  }

  .detail-capability-icon {
    color: var(--td-text-color-placeholder);
  }
}

.detail-capability-icon-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  flex-shrink: 0;
}

.detail-capability-icon {
  display: inline-flex;
  flex-shrink: 0;
  line-height: 1;
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
