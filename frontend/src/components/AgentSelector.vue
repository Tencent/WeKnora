<template>
  <Teleport to="body">
    <div v-if="visible" class="agent-selector-overlay" @click="$emit('close')">
      <div 
        class="agent-selector-dropdown"
        :style="dropdownStyle"
        @click.stop
      >
        <!-- 头部 -->
        <div class="agent-selector-header">
          <span>{{ $t('agent.selectAgent') }}</span>
          <router-link to="/platform/agents" class="agent-selector-add" @click="$emit('close')">
            <span class="add-icon">+</span>
            <span class="add-text">{{ $t('agent.manageAgents') }}</span>
          </router-link>
        </div>
        
        <!-- 内容区域 -->
        <div class="agent-selector-content">
          <!-- 内置智能体分组 -->
          <div class="agent-group">
            <div class="agent-group-title">{{ $t('agent.builtinAgents') }}</div>
            <t-popup 
              v-for="agent in builtinAgents" 
              :key="agent.id"
              placement="right"
              trigger="hover"
              :show-arrow="true"
              :overlay-inner-class-name="'agent-tooltip-popup'"
            >
              <div 
                class="agent-option"
                :class="{ 'selected': isMyAgentSelected(agent) }"
                @click="selectAgent(agent)"
              >
                <!-- 快速回答和智能推理使用图标，其他内置智能体使用 avatar -->
                <div v-if="agent.id === BUILTIN_QUICK_ANSWER_ID || agent.id === BUILTIN_SMART_REASONING_ID" 
                     class="builtin-icon" 
                     :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
                  <TIcon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="14px" />
                </div>
                <div v-else-if="agent.avatar" class="builtin-avatar">{{ agent.avatar }}</div>
                <div v-else class="builtin-icon normal">
                  <TIcon name="app" size="14px" />
                </div>
                <span class="agent-option-name">{{ agent.name }}</span>
                <div class="agent-option-actions">
                  <t-tooltip :content="$t('agent.selector.goToSettings')" placement="top">
                    <div class="settings-btn" @click.stop="goToSettings(agent)">
                      <TIcon name="setting" size="14px" />
                    </div>
                  </t-tooltip>
                  <svg 
                    v-if="isMyAgentSelected(agent)"
                    width="14" 
                    height="14" 
                    viewBox="0 0 16 16" 
                    fill="currentColor"
                    class="check-icon"
                  >
                    <path d="M13.5 4.5L6 12L2.5 8.5L3.5 7.5L6 10L12.5 3.5L13.5 4.5Z"/>
                  </svg>
                </div>
              </div>
              <template #content>
                <div class="agent-tooltip-content">
                  <div class="agent-tooltip-header">
                    <!-- 快速回答和智能推理使用图标，其他内置智能体使用 avatar -->
                    <div v-if="agent.id === BUILTIN_QUICK_ANSWER_ID || agent.id === BUILTIN_SMART_REASONING_ID" 
                         class="builtin-icon" 
                         :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
                      <TIcon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="14px" />
                    </div>
                    <div v-else-if="agent.avatar" class="builtin-avatar">{{ agent.avatar }}</div>
                    <div v-else class="builtin-icon normal">
                      <TIcon name="app" size="14px" />
                    </div>
                    <div class="agent-tooltip-title">
                      <span class="agent-tooltip-name">{{ agent.name }}</span>
                      <span v-if="isMyAgentSelected(agent)" class="agent-tooltip-selected">{{ $t('agent.selector.current') }}</span>
                    </div>
                  </div>
                  <p class="agent-tooltip-desc">{{ agent.description || $t('agent.noDescription') }}</p>
                  <div class="agent-tooltip-capabilities">
                    <div class="capability-item">
                      <TIcon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="12px" />
                      <span>{{ agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.type.agent') : $t('agent.type.normal') }}</span>
                    </div>
                    <div v-if="getKbCapability(agent)" class="capability-item">
                      <TIcon name="folder" size="12px" />
                      <span>{{ getKbCapability(agent) }}</span>
                    </div>
                    <div v-if="agent.config?.web_search_enabled" class="capability-item">
                      <TIcon name="internet" size="12px" />
                      <span>{{ $t('agent.capabilities.webSearchOn') }}</span>
                    </div>
                    <div v-if="getMcpCapability(agent)" class="capability-item">
                      <TIcon name="extension" size="12px" />
                      <span>{{ getMcpCapability(agent) }}</span>
                    </div>
                    <div v-if="agent.config?.multi_turn_enabled" class="capability-item">
                      <TIcon name="chat-bubble" size="12px" />
                      <span>{{ $t('agent.capabilities.multiTurn') }}</span>
                    </div>
                  </div>
                </div>
              </template>
            </t-popup>
          </div>

          <!-- 自定义智能体分组 -->
          <div v-if="customAgents.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('agent.customAgents') }}</div>
            <t-popup 
              v-for="agent in customAgents" 
              :key="agent.id"
              placement="right"
              trigger="hover"
              :show-arrow="true"
              :overlay-inner-class-name="'agent-tooltip-popup'"
            >
              <div 
                class="agent-option"
                :class="{ 'selected': isMyAgentSelected(agent) }"
                @click="selectAgent(agent)"
              >
                <AgentAvatar :name="agent.name" size="small" />
                <span class="agent-option-name">{{ agent.name }}</span>
                <div class="agent-option-actions">
                  <t-tooltip :content="$t('agent.selector.goToSettings')" placement="top">
                    <div class="settings-btn" @click.stop="goToSettings(agent)">
                      <TIcon name="setting" size="14px" />
                    </div>
                  </t-tooltip>
                  <svg 
                    v-if="isMyAgentSelected(agent)"
                    width="14" 
                    height="14" 
                    viewBox="0 0 16 16" 
                    fill="currentColor"
                    class="check-icon"
                  >
                    <path d="M13.5 4.5L6 12L2.5 8.5L3.5 7.5L6 10L12.5 3.5L13.5 4.5Z"/>
                  </svg>
                </div>
              </div>
              <template #content>
                <div class="agent-tooltip-content">
                  <div class="agent-tooltip-header">
                    <AgentAvatar :name="agent.name" size="small" />
                    <div class="agent-tooltip-title">
                      <span class="agent-tooltip-name">{{ agent.name }}</span>
                      <span v-if="isMyAgentSelected(agent)" class="agent-tooltip-selected">{{ $t('agent.selector.current') }}</span>
                    </div>
                  </div>
                  <p class="agent-tooltip-desc">{{ agent.description || $t('agent.noDescription') }}</p>
                  <div class="agent-tooltip-capabilities">
                    <div class="capability-item">
                      <TIcon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="12px" />
                      <span>{{ agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.type.agent') : $t('agent.type.normal') }}</span>
                    </div>
                    <div v-if="getKbCapability(agent)" class="capability-item">
                      <TIcon name="folder" size="12px" />
                      <span>{{ getKbCapability(agent) }}</span>
                    </div>
                    <div v-if="agent.config?.web_search_enabled" class="capability-item">
                      <TIcon name="internet" size="12px" />
                      <span>{{ $t('agent.capabilities.webSearchOn') }}</span>
                    </div>
                    <div v-if="getMcpCapability(agent)" class="capability-item">
                      <TIcon name="extension" size="12px" />
                      <span>{{ getMcpCapability(agent) }}</span>
                    </div>
                    <div v-if="agent.config?.multi_turn_enabled" class="capability-item">
                      <TIcon name="chat-bubble" size="12px" />
                      <span>{{ $t('agent.capabilities.multiTurn') }}</span>
                    </div>
                  </div>
                </div>
              </template>
            </t-popup>
          </div>

          <!-- 共享给我分组 -->
          <div v-if="sharedAgentsList.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('agent.tabs.sharedToMe') }}</div>
            <t-popup
              v-for="shared in sharedAgentsList"
              :key="`${shared.agent.id}-${shared.source_tenant_id}`"
              placement="right"
              trigger="hover"
              :show-arrow="true"
              :overlay-inner-class-name="'agent-tooltip-popup'"
            >
              <div
                class="agent-option"
                :class="{ 'selected': isSharedAgentSelected(shared) }"
                @click="selectSharedAgent(shared)"
              >
                <AgentAvatar :name="shared.agent.name" size="small" />
                <span class="agent-option-name">{{ shared.agent.name }}</span>
                <span class="shared-tag">{{ $t('agent.selector.sharedLabel') }}</span>
                <div class="agent-option-actions">
                  <svg
                    v-if="isSharedAgentSelected(shared)"
                    width="14"
                    height="14"
                    viewBox="0 0 16 16"
                    fill="currentColor"
                    class="check-icon"
                  >
                    <path d="M13.5 4.5L6 12L2.5 8.5L3.5 7.5L6 10L12.5 3.5L13.5 4.5Z"/>
                  </svg>
                </div>
              </div>
              <template #content>
                <div class="agent-tooltip-content">
                  <div class="agent-tooltip-header">
                    <AgentAvatar :name="shared.agent.name" size="small" />
                    <div class="agent-tooltip-title">
                      <span class="agent-tooltip-name">{{ shared.agent.name }}</span>
                      <span v-if="isSharedAgentSelected(shared)" class="agent-tooltip-selected">{{ $t('agent.selector.current') }}</span>
                    </div>
                  </div>
                  <p class="agent-tooltip-desc">{{ shared.agent.description || $t('agent.noDescription') }}</p>
                  <div class="agent-tooltip-capabilities">
                    <div class="capability-item">
                      <TIcon :name="shared.agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="12px" />
                      <span>{{ shared.agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.type.agent') : $t('agent.type.normal') }}</span>
                    </div>
                    <div v-if="getKbCapability(shared.agent)" class="capability-item">
                      <TIcon name="folder" size="12px" />
                      <span>{{ getKbCapability(shared.agent) }}</span>
                    </div>
                    <div v-if="shared.agent.config?.web_search_enabled" class="capability-item">
                      <TIcon name="internet" size="12px" />
                      <span>{{ $t('agent.capabilities.webSearchOn') }}</span>
                    </div>
                    <div v-if="getMcpCapability(shared.agent)" class="capability-item">
                      <TIcon name="extension" size="12px" />
                      <span>{{ getMcpCapability(shared.agent) }}</span>
                    </div>
                    <div v-if="shared.agent.config?.multi_turn_enabled" class="capability-item">
                      <TIcon name="chat-bubble" size="12px" />
                      <span>{{ $t('agent.capabilities.multiTurn') }}</span>
                    </div>
                  </div>
                  <div v-if="shared.org_name || shared.shared_by_username" class="agent-tooltip-meta-list">
                    <div v-if="shared.org_name" class="agent-tooltip-meta-row">
                      <img src="@/assets/img/organization-green.svg" class="agent-tooltip-meta-icon" alt="" aria-hidden="true" />
                      <span class="agent-tooltip-meta-text">{{ shared.org_name }}</span>
                    </div>
                    <div v-if="shared.shared_by_username" class="agent-tooltip-meta-row">
                      <img src="@/assets/img/user.svg" class="agent-tooltip-meta-icon" alt="" aria-hidden="true" />
                      <span class="agent-tooltip-meta-text">{{ shared.shared_by_username }}</span>
                    </div>
                  </div>
                </div>
              </template>
            </t-popup>
          </div>

          <!-- 空状态 -->
          <div v-if="builtinAgents.length === 0 && customAgents.length === 0 && sharedAgentsList.length === 0" class="agent-option empty">
            {{ $t('agent.noAgents') }}
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
import { Icon as TIcon, Popup as TPopup, Tooltip as TTooltip } from 'tdesign-vue-next';
import { type CustomAgent, BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from '@/api/agent';
import AgentAvatar from '@/components/AgentAvatar.vue';
import { useOrganizationStore } from '@/stores/organization';
import { useSettingsStore } from '@/stores/settings';
import type { SharedAgentInfo } from '@/api/organization';

const { t } = useI18n();
const router = useRouter();
const orgStore = useOrganizationStore();
const settingsStore = useSettingsStore();

const props = defineProps<{
  visible: boolean;
  anchorEl?: HTMLElement;
  currentAgentId: string;
  /** 由父组件加载的智能体列表，避免下拉打开时重复请求 agents / shared-agents */
  agents?: CustomAgent[];
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'select', agent: CustomAgent, sourceTenantId?: string): void;
}>();

const dropdownStyle = ref<Record<string, string>>({});

// 父组件已按「当前租户停用」过滤，此处直接使用
const agentsList = computed(() => props.agents ?? []);

// 内置智能体（从 API 获取，对特定 ID 使用本地化名称）
const builtinAgents = computed(() => {
  // 从 API 获取的内置智能体（内置无 disabled 概念，全部展示）
  const apiBuiltins = agentsList.value.filter(a => a.is_builtin);
  
  // 对特定内置智能体使用本地化名称和描述
  return apiBuiltins.map(agent => {
    if (agent.id === BUILTIN_QUICK_ANSWER_ID) {
      return {
        ...agent,
        name: t('input.normalMode'),
        description: t('input.normalModeDesc'),
      };
    } else if (agent.id === BUILTIN_SMART_REASONING_ID) {
      return {
        ...agent,
        name: t('input.agentMode'),
        description: t('input.agentModeDesc'),
      };
    }
    // 其他内置智能体使用 API 返回的名称和描述
    return agent;
  });
});

// 自定义智能体（我的）
const customAgents = computed(() => {
  return agentsList.value.filter(a => !a.is_builtin);
});

// 共享给我的智能体（仅展示当前用户未停用的）
const sharedAgentsList = computed<SharedAgentInfo[]>(() =>
  (orgStore.sharedAgents || []).filter(shared => !shared.disabled_by_me)
);

// 当前选中的来源租户（共享智能体时）
const currentAgentSourceTenantId = computed(() => settingsStore.selectedAgentSourceTenantId ?? null);

const isSharedAgentSelected = (shared: SharedAgentInfo) =>
  props.currentAgentId === shared.agent.id && currentAgentSourceTenantId.value === String(shared.source_tenant_id);

// 我的智能体（内置或自定义）选中态：仅当未选共享来源时
const isMyAgentSelected = (agent: CustomAgent) =>
  props.currentAgentId === agent.id && !currentAgentSourceTenantId.value;

// 获取知识库能力描述
const getKbCapability = (agent: CustomAgent): string => {
  const config = agent.config || {};
  if (config.kb_selection_mode === 'none') {
    return '';
  } else if (config.knowledge_bases && config.knowledge_bases.length > 0) {
    return t('agent.capabilities.kbCount', { count: config.knowledge_bases.length });
  } else if (config.kb_selection_mode === 'all') {
    return t('agent.capabilities.kbAll');
  }
  return '';
};

// 获取 MCP 能力描述（更详细：全部 / 指定 N 个）
const getMcpCapability = (agent: CustomAgent): string => {
  const config = agent.config || {};
  if (config.mcp_selection_mode === 'none' || (!config.mcp_services?.length && config.mcp_selection_mode !== 'all')) {
    return '';
  }
  if (config.mcp_selection_mode === 'all') {
    return t('agent.detail.shareScope.mcpAll');
  }
  if (config.mcp_services?.length) {
    return t('agent.detail.shareScope.mcpSelected', { count: config.mcp_services.length });
  }
  return t('agent.capabilities.mcpEnabled');
};

// 选择智能体（我的或内置）
const selectAgent = (agent: CustomAgent) => {
  emit('select', agent);
};

// 选择共享智能体
const selectSharedAgent = (shared: SharedAgentInfo) => {
  emit('select', shared.agent as CustomAgent, String(shared.source_tenant_id));
};

// 跳转到智能体设置页面
const goToSettings = (agent: CustomAgent) => {
  emit('close');
  router.push({
    path: '/platform/agents',
    query: { edit: agent.id }
  });
};

// 更新下拉框位置（与模型选择器一致）
const updateDropdownPosition = () => {
  if (!props.anchorEl) return;
  
  const rect = props.anchorEl.getBoundingClientRect();
  const dropdownWidth = 200;
  const offsetY = 8;
  const vh = window.innerHeight;
  const vw = window.innerWidth;
  
  // 水平位置：左对齐
  let left = Math.floor(rect.left);
  const minLeft = 16;
  const maxLeft = Math.max(16, vw - dropdownWidth - 16);
  left = Math.max(minLeft, Math.min(maxLeft, left));
  
  // 垂直位置
  const preferredDropdownHeight = 320;
  const minDropdownHeight = 100;
  const topMargin = 20;
  const spaceBelow = vh - rect.bottom;
  const spaceAbove = rect.top;
  
  let actualHeight: number;
  
  if (spaceBelow >= minDropdownHeight + offsetY) {
    // 向下弹出
    actualHeight = Math.min(preferredDropdownHeight, spaceBelow - offsetY - 16);
    const top = Math.floor(rect.bottom + offsetY);
    
    dropdownStyle.value = {
      position: 'fixed',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      top: `${top}px`,
      maxHeight: `${actualHeight}px`,
      zIndex: '9999'
    };
  } else {
    // 向上弹出
    const availableHeight = spaceAbove - offsetY - topMargin;
    actualHeight = availableHeight >= preferredDropdownHeight 
      ? preferredDropdownHeight 
      : Math.max(minDropdownHeight, availableHeight);
    
    const bottom = vh - rect.top + offsetY;
    
    dropdownStyle.value = {
      position: 'fixed',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      bottom: `${bottom}px`,
      maxHeight: `${actualHeight}px`,
      zIndex: '9999'
    };
  }
};

// 监听显示状态（仅更新位置，数据由父组件加载后通过 props.agents 传入）
watch(() => props.visible, (newVal) => {
  if (newVal) {
    nextTick(() => {
      updateDropdownPosition();
    });
  }
});
</script>

<style scoped lang="less">
.agent-selector-overlay {
  position: fixed;
  inset: 0;
  z-index: 9998;
  background: transparent;
  touch-action: none;
}

.agent-selector-dropdown {
  position: fixed;
  background: var(--td-bg-color-container, #fff);
  border-radius: 10px;
  box-shadow: var(--td-shadow-2, 0 6px 28px rgba(15, 23, 42, 0.08));
  border: 1px solid var(--td-component-border, #e7e9eb);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.agent-selector-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
  background: var(--td-bg-color-container, #fff);
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-secondary, #666);
}

.agent-selector-add {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--td-brand-color, #07c05f);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
  text-decoration: none;
  
  .add-icon {
    font-size: 14px;
    line-height: 1;
    font-weight: 400;
  }
  
  &:hover {
    color: var(--td-brand-color-hover, #05a04f);
    background: var(--td-bg-color-secondarycontainer, #f3f3f3);
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
    margin-bottom: 8px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
  }
}

.agent-group-title {
  font-size: 11px;
  color: var(--td-text-color-placeholder, #999);
  padding: 4px 8px 6px;
  font-weight: 500;
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
  
  &:hover {
    background: var(--td-bg-color-container-hover, #f6f8f7);
  }
  
  &.selected {
    background: var(--td-brand-color-light, #eefdf5);
    
    .agent-option-name {
      color: #10b981;
      font-weight: 600;
    }
  }
  
  &.empty {
    color: var(--td-text-color-disabled, #9aa0a6);
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
  color: var(--td-text-color-primary, #222);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.4;
}

.shared-tag {
  font-size: 10px;
  color: var(--td-text-color-placeholder, #999);
  flex-shrink: 0;
}

.agent-tooltip-meta-list {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--td-component-stroke, #f0f0f0);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.agent-tooltip-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--td-text-color-placeholder, #999);
}

.agent-tooltip-meta-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

.agent-tooltip-meta-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
    background: rgba(7, 192, 95, 0.1);
    color: #059669;
  }
  
  &.agent {
    background: rgba(124, 77, 255, 0.1);
    color: #7c4dff;
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
}

.agent-option-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.settings-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 4px;
  color: var(--td-text-color-placeholder, #999);
  cursor: pointer;
  opacity: 0;
  transition: all 0.15s ease;
  
  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover, #e8e8e8);
    color: var(--td-brand-color, #07c05f);
  }
}

.agent-option:hover .settings-btn {
  opacity: 1;
}

.check-icon {
  width: 14px;
  height: 14px;
  color: #10b981;
  flex-shrink: 0;
}

// Tooltip 内容样式
.agent-tooltip-content {
  padding: 4px 0;
  min-width: 200px;
  max-width: 280px;
}

.agent-tooltip-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  
  .builtin-icon {
    width: 28px;
    height: 28px;
  }
  
  .builtin-avatar {
    width: 28px;
    height: 28px;
    font-size: 18px;
  }
}

.agent-tooltip-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.agent-tooltip-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary, #222);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-tooltip-selected {
  font-size: 10px;
  color: #10b981;
  font-weight: 500;
}

.agent-tooltip-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary, #666);
  line-height: 1.5;
  margin: 0 0 10px 0;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.agent-tooltip-capabilities {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 8px;
  border-top: 1px solid var(--td-component-stroke, #f0f0f0);
}

.capability-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);
  border-radius: 4px;
  font-size: 11px;
  color: var(--td-text-color-secondary, #666);
  
  :deep(.t-icon) {
    color: var(--td-text-color-placeholder, #999);
  }
}
</style>

<!-- 全局样式覆盖 TDesign Popup -->
<style lang="less">
.agent-tooltip-popup {
  &.t-popup__content {
    background: var(--td-bg-color-container, #fff) !important;
    border: 1px solid var(--td-component-border, #e7e9eb) !important;
    border-radius: 8px !important;
    box-shadow: var(--td-shadow-2, 0 6px 28px rgba(15, 23, 42, 0.08)) !important;
    padding: 10px 12px !important;
  }
  
  .t-popup__arrow {
    &::before {
      background: var(--td-bg-color-container, #fff) !important;
      border-color: var(--td-component-border, #e7e9eb) !important;
    }
  }
}
</style>
