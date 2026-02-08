<template>
  <div class="agent-list-container">
    <!-- 头部：仅标题与副标题 -->
    <div class="header">
      <div class="header-title">
        <h2>{{ $t('agent.title') }}</h2>
        <p class="header-subtitle">{{ $t('agent.subtitle') }}</p>
      </div>
    </div>

    <!-- 左侧菜单 + 主内容 -->
    <div class="agent-list-body">
      <ListSpaceSidebar
        v-model="spaceSelection"
        :count-all="allAgentsCount"
        :count-mine="agents.length"
        :count-by-org="effectiveSharedCountByOrg"
      >
        <template #actions>
          <t-tooltip :content="$t('agent.createAgent')" placement="top">
            <t-button
              variant="text"
              theme="default"
              class="sidebar-action-btn"
              size="small"
              :aria-label="$t('agent.createAgent')"
              @click="handleCreateAgent"
            >
              <template #icon>
                <span class="btn-icon-wrapper">
                  <svg class="sparkles-icon" width="16" height="16" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" fill="currentColor" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"/>
                    <path d="M15.5 4L15.8 5.2C15.85 5.45 16.05 5.65 16.3 5.7L17.5 6L16.3 6.3C16.05 6.35 15.85 6.55 15.8 6.8L15.5 8L15.2 6.8C15.15 6.55 14.95 6.35 14.7 6.3L13.5 6L14.7 5.7C14.95 5.65 15.15 5.45 15.2 5.2L15.5 4Z" fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"/>
                    <path d="M4.5 13L4.8 14.2C4.85 14.45 5.05 14.65 5.3 14.7L6.5 15L5.3 15.3C5.05 15.35 4.85 15.55 4.8 15.8L4.5 17L4.2 15.8C4.15 15.55 3.95 15.35 3.7 15.3L2.5 15L3.7 14.7C3.95 14.65 4.15 14.45 4.2 14.2L4.5 13Z" fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"/>
                  </svg>
                </span>
              </template>
            </t-button>
          </t-tooltip>
        </template>
      </ListSpaceSidebar>
      <div class="agent-list-main">
    <!-- 全部：我的 + 共享 -->
    <div v-if="spaceSelection === 'all' && filteredAgents.length > 0" class="agent-card-wrap">
      <div
        v-for="agent in filteredAgents"
        :key="agent.isMine ? agent.id : `shared-${agent.share_id}`"
        class="agent-card"
        :class="{
          'is-builtin': agent.is_builtin,
          'agent-mode-normal': agent.config?.agent_mode === 'quick-answer',
          'agent-mode-agent': agent.config?.agent_mode === 'smart-reasoning',
          'shared-agent-card': !agent.isMine
        }"
        @click="handleCardClick(agent)"
      >
        <!-- 装饰星星 -->
        <div class="card-decoration">
          <svg class="star-icon" width="24" height="24" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round" fill="currentColor" fill-opacity="0.15"/>
          </svg>
          <svg class="star-icon small" width="14" height="14" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round" fill="currentColor" fill-opacity="0.15"/>
          </svg>
        </div>
        <div class="card-header">
          <div class="card-header-left">
            <div v-if="agent.is_builtin" class="builtin-avatar" :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
              <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="18px" />
            </div>
            <div v-else-if="agent.avatar" class="builtin-avatar agent-emoji">{{ agent.avatar }}</div>
            <AgentAvatar v-else :name="agent.name" size="small" />
            <span class="card-title" :title="agent.name">{{ agent.name }}</span>
          </div>
          <t-popup
            v-if="agent.isMine"
            :visible="openMoreAgentId === agent.id"
            trigger="hover"
            overlayClassName="card-more-popup"
            destroy-on-close
            placement="bottom-right"
            @visible-change="onVisibleChange"
            @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }"
          >
            <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === agent.id }" @click="toggleMore($event, agent.id)">
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu">
                <div class="popup-menu-item" @click="handleEdit(agent)"><t-icon class="menu-icon" name="edit" /><span>{{ $t('common.edit') }}</span></div>
                <div class="popup-menu-item" @click="handleCopy(agent)"><t-icon class="menu-icon" name="file-copy" /><span>{{ $t('common.copy') }}</span></div>
                <div v-if="!agent.is_builtin" class="popup-menu-item" @click="handleToggleDisabled(agent)">
                  <t-icon class="menu-icon" name="poweroff" />
                  <span>{{ agent.disabled_by_me ? $t('agent.enable') : $t('agent.disable') }}</span>
                </div>
                <div v-if="!agent.is_builtin" class="popup-menu-item delete" @click="handleDelete(agent)"><t-icon class="menu-icon" name="delete" /><span>{{ $t('common.delete') }}</span></div>
              </div>
            </template>
          </t-popup>
          <t-popup
            v-else
            :visible="openMoreAgentId === 'shared-' + agent.share_id"
            trigger="hover"
            overlayClassName="card-more-popup"
            destroy-on-close
            placement="bottom-right"
            @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }"
          >
            <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === 'shared-' + agent.share_id }" @click.stop="toggleMore($event, 'shared-' + agent.share_id)">
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu">
                <div class="popup-menu-item" @click="handleToggleSharedDisabled(agent)">
                  <t-icon class="menu-icon" name="poweroff" />
                  <span>{{ agent.disabled_by_me ? $t('agent.enable') : $t('agent.disable') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
        <div class="card-content">
          <div class="card-description">{{ agent.description || $t('agent.noDescription') }}</div>
        </div>
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tag v-if="agent.isMine && !agent.is_builtin && agent.disabled_by_me" theme="default" size="small" class="disabled-badge">{{ $t('agent.disabled') }}</t-tag>
              <t-tag v-if="!agent.isMine && agent.disabled_by_me" theme="default" size="small" class="disabled-badge">{{ $t('agent.disabled') }}</t-tag>
              <t-tooltip :content="agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.mode.agent') : $t('agent.mode.normal')" placement="top">
                <div class="feature-badge" :class="{ 'mode-normal': agent.config?.agent_mode === 'quick-answer', 'mode-agent': agent.config?.agent_mode === 'smart-reasoning' }">
                  <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="14px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.web_search_enabled" :content="$t('agent.features.webSearch')" placement="top">
                <div class="feature-badge web-search">
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none"/>
                    <ellipse cx="8" cy="8" rx="2.5" ry="6" stroke="currentColor" stroke-width="1.2" fill="none"/>
                    <line x1="2" y1="6" x2="14" y2="6" stroke="currentColor" stroke-width="1.2"/>
                    <line x1="2" y1="10" x2="14" y2="10" stroke="currentColor" stroke-width="1.2"/>
                  </svg>
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.knowledge_bases?.length || agent.config?.kb_selection_mode === 'all'" :content="$t('agent.features.knowledgeBase')" placement="top">
                <div class="feature-badge knowledge">
                  <t-icon name="folder" size="16px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.mcp_services?.length || agent.config?.mcp_selection_mode === 'all'" :content="$t('agent.features.mcp')" placement="top">
                <div class="feature-badge mcp">
                  <t-icon name="extension" size="16px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.multi_turn_enabled" :content="$t('agent.features.multiTurn')" placement="top">
                <div class="feature-badge multi-turn">
                  <t-icon name="chat-bubble" size="16px" />
                </div>
              </t-tooltip>
            </div>
          </div>
          <!-- 右下角：内置 / 自定义 / 空间图标+名称 -->
          <div v-if="!agent.isMine" class="card-bottom-source">
            <img src="@/assets/img/organization-green.svg" class="org-icon" alt="" aria-hidden="true" />
            <span class="org-source-text">{{ agent.org_name }}</span>
          </div>
          <div v-else-if="agent.is_builtin" class="builtin-badge">
            <t-icon name="lock-on" size="12px" />
            <span>{{ $t('agent.builtin') }}</span>
          </div>
          <div v-else class="custom-badge">
            <span>{{ $t('agent.type.custom') }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 我的智能体 -->
    <div v-if="spaceSelection === 'mine' && agents.length > 0" class="agent-card-wrap">
      <div 
        v-for="agent in agents" 
        :key="agent.id" 
        class="agent-card"
        :class="{ 
          'is-builtin': agent.is_builtin,
          'agent-mode-normal': agent.config?.agent_mode === 'quick-answer',
          'agent-mode-agent': agent.config?.agent_mode === 'smart-reasoning'
        }"
        @click="handleCardClick(agent)"
      >
        <!-- 装饰星星 -->
        <div class="card-decoration">
          <svg class="star-icon" width="24" height="24" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round" fill="currentColor" fill-opacity="0.15"/>
          </svg>
          <svg class="star-icon small" width="14" height="14" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round" fill="currentColor" fill-opacity="0.15"/>
          </svg>
        </div>
        
        <!-- 卡片头部 -->
        <div class="card-header">
          <div class="card-header-left">
            <!-- 内置智能体使用简洁图标 -->
            <div v-if="agent.is_builtin" class="builtin-avatar" :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
              <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="18px" />
            </div>
            <div v-else-if="agent.avatar" class="builtin-avatar agent-emoji">{{ agent.avatar }}</div>
            <AgentAvatar v-else :name="agent.name" size="small" />
            <span class="card-title" :title="agent.name">{{ agent.name }}</span>
          </div>
          <t-popup
            :visible="openMoreAgentId === agent.id"
            trigger="hover"
            overlayClassName="card-more-popup"
            destroy-on-close
            placement="bottom-right"
            @visible-change="onVisibleChange"
            @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }"
          >
            <div
              class="more-wrap"
              :class="{ 'active-more': openMoreAgentId === agent.id }"
              @click="toggleMore($event, agent.id)"
            >
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu">
                <div class="popup-menu-item" @click="handleEdit(agent)">
                  <t-icon class="menu-icon" name="edit" />
                  <span>{{ $t('common.edit') }}</span>
                </div>
                <div class="popup-menu-item" @click="handleCopy(agent)">
                  <t-icon class="menu-icon" name="file-copy" />
                  <span>{{ $t('common.copy') }}</span>
                </div>
                <div v-if="!agent.is_builtin" class="popup-menu-item" @click="handleToggleDisabled(agent)">
                  <t-icon class="menu-icon" name="poweroff" />
                  <span>{{ agent.disabled_by_me ? $t('agent.enable') : $t('agent.disable') }}</span>
                </div>
                <div v-if="!agent.is_builtin" class="popup-menu-item delete" @click="handleDelete(agent)">
                  <t-icon class="menu-icon" name="delete" />
                  <span>{{ $t('common.delete') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>

        <!-- 卡片内容 -->
        <div class="card-content">
          <div class="card-description">
            {{ agent.description || $t('agent.noDescription') }}
          </div>
        </div>

        <!-- 卡片底部 -->
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tag v-if="!agent.is_builtin && agent.disabled_by_me" theme="default" size="small" class="disabled-badge">{{ $t('agent.disabled') }}</t-tag>
              <t-tooltip :content="agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.mode.agent') : $t('agent.mode.normal')" placement="top">
                <div class="feature-badge" :class="{ 'mode-normal': agent.config?.agent_mode === 'quick-answer', 'mode-agent': agent.config?.agent_mode === 'smart-reasoning' }">
                  <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="14px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.web_search_enabled" :content="$t('agent.features.webSearch')" placement="top">
                <div class="feature-badge web-search">
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none"/>
                    <ellipse cx="8" cy="8" rx="2.5" ry="6" stroke="currentColor" stroke-width="1.2" fill="none"/>
                    <line x1="2" y1="6" x2="14" y2="6" stroke="currentColor" stroke-width="1.2"/>
                    <line x1="2" y1="10" x2="14" y2="10" stroke="currentColor" stroke-width="1.2"/>
                  </svg>
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.knowledge_bases?.length || agent.config?.kb_selection_mode === 'all'" :content="$t('agent.features.knowledgeBase')" placement="top">
                <div class="feature-badge knowledge">
                  <t-icon name="folder" size="16px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.mcp_services?.length || agent.config?.mcp_selection_mode === 'all'" :content="$t('agent.features.mcp')" placement="top">
                <div class="feature-badge mcp">
                  <t-icon name="extension" size="16px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="agent.config?.multi_turn_enabled" :content="$t('agent.features.multiTurn')" placement="top">
                <div class="feature-badge multi-turn">
                  <t-icon name="chat-bubble" size="16px" />
                </div>
              </t-tooltip>
            </div>
          </div>
          <!-- 右下角：内置 / 自定义 -->
          <div v-if="agent.is_builtin" class="builtin-badge">
            <t-icon name="lock-on" size="12px" />
            <span>{{ $t('agent.builtin') }}</span>
          </div>
          <div v-else class="custom-badge">
            <span>{{ $t('agent.type.custom') }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 按空间筛选：该空间内全部智能体（含我共享的） -->
    <div v-if="spaceSelectionOrgId && spaceAgentsLoading" class="agent-list-main-loading">
      <t-loading size="large" text="" />
    </div>
    <div v-else-if="spaceSelectionOrgId && spaceAgentsList.length > 0" class="agent-card-wrap">
      <div
        v-for="shared in spaceAgentsList"
        :key="'shared-' + shared.share_id"
        class="agent-card shared-agent-card"
        :class="{
          'agent-mode-normal': shared.agent?.config?.agent_mode === 'quick-answer',
          'agent-mode-agent': shared.agent?.config?.agent_mode === 'smart-reasoning'
        }"
        @click="handleSpaceAgentCardClick(shared)"
      >
        <div class="card-decoration">
          <svg class="star-icon" width="24" height="24" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round" fill="currentColor" fill-opacity="0.15"/>
          </svg>
          <svg class="star-icon small" width="14" height="14" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round" fill="currentColor" fill-opacity="0.15"/>
          </svg>
        </div>
        <div class="card-header">
          <div class="card-header-left">
            <div v-if="shared.agent?.avatar" class="builtin-avatar agent-emoji">{{ shared.agent.avatar }}</div>
            <AgentAvatar v-else :name="shared.agent?.name" size="small" />
            <span class="card-title" :title="shared.agent?.name">{{ shared.agent?.name }}</span>
            <span v-if="shared.is_mine" class="shared-by-me-badge">{{ $t('listSpaceSidebar.mine') }}</span>
          </div>
          <t-popup
            v-if="!shared.is_mine"
            :visible="openMoreAgentId === 'shared-tab-' + shared.share_id"
            trigger="hover"
            overlayClassName="card-more-popup"
            destroy-on-close
            placement="bottom-right"
            @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }"
          >
            <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === 'shared-tab-' + shared.share_id }" @click.stop="toggleMore($event, 'shared-tab-' + shared.share_id)">
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu">
                <div class="popup-menu-item" @click="handleToggleSharedDisabledFromShared(shared)">
                  <t-icon class="menu-icon" name="poweroff" />
                  <span>{{ shared.disabled_by_me ? $t('agent.enable') : $t('agent.disable') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
        <div class="card-content">
          <div class="card-description">{{ shared.agent?.description || $t('agent.noDescription') }}</div>
        </div>
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tag v-if="shared.disabled_by_me" theme="default" size="small" class="disabled-badge">{{ $t('agent.disabled') }}</t-tag>
              <t-tooltip :content="shared.agent?.config?.agent_mode === 'smart-reasoning' ? $t('agent.mode.agent') : $t('agent.mode.normal')" placement="top">
                <div class="feature-badge" :class="{ 'mode-normal': shared.agent?.config?.agent_mode === 'quick-answer', 'mode-agent': shared.agent?.config?.agent_mode === 'smart-reasoning' }">
                  <t-icon :name="shared.agent?.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'" size="14px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="shared.agent?.config?.web_search_enabled" :content="$t('agent.features.webSearch')" placement="top">
                <div class="feature-badge web-search"><svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none"/><ellipse cx="8" cy="8" rx="2.5" ry="6" stroke="currentColor" stroke-width="1.2" fill="none"/><line x1="2" y1="6" x2="14" y2="6" stroke="currentColor" stroke-width="1.2"/><line x1="2" y1="10" x2="14" y2="10" stroke="currentColor" stroke-width="1.2"/></svg></div>
              </t-tooltip>
              <t-tooltip v-if="shared.agent?.config?.knowledge_bases?.length || shared.agent?.config?.kb_selection_mode === 'all'" :content="$t('agent.features.knowledgeBase')" placement="top">
                <div class="feature-badge knowledge"><t-icon name="folder" size="16px" /></div>
              </t-tooltip>
              <t-tooltip v-if="shared.agent?.config?.mcp_services?.length || shared.agent?.config?.mcp_selection_mode === 'all'" :content="$t('agent.features.mcp')" placement="top">
                <div class="feature-badge mcp"><t-icon name="extension" size="16px" /></div>
              </t-tooltip>
              <t-tooltip v-if="shared.agent?.config?.multi_turn_enabled" :content="$t('agent.features.multiTurn')" placement="top">
                <div class="feature-badge multi-turn"><t-icon name="chat-bubble" size="16px" /></div>
              </t-tooltip>
            </div>
          </div>
          <!-- 右下角：空间图标+名称 -->
          <div class="card-bottom-source">
            <img src="@/assets/img/organization-green.svg" class="org-icon" alt="" aria-hidden="true" />
            <span class="org-source-text">{{ shared.org_name }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态：全部 -->
    <div v-if="spaceSelection === 'all' && filteredAgents.length === 0 && !loading" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ $t('agent.empty.title') }}</span>
      <span class="empty-desc">{{ $t('agent.empty.description') }}</span>
      <t-button class="agent-create-btn empty-state-btn" @click="handleCreateAgent">
        <template #icon>
          <span class="btn-icon-wrapper">
            <svg class="sparkles-icon" width="18" height="18" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" fill="currentColor" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M15.5 4L15.8 5.2C15.85 5.45 16.05 5.65 16.3 5.7L17.5 6L16.3 6.3C16.05 6.35 15.85 6.55 15.8 6.8L15.5 8L15.2 6.8C15.15 6.55 14.95 6.35 14.7 6.3L13.5 6L14.7 5.7C14.95 5.65 15.15 5.45 15.2 5.2L15.5 4Z" fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M4.5 13L4.8 14.2C4.85 14.45 5.05 14.65 5.3 14.7L6.5 15L5.3 15.3C5.05 15.35 4.85 15.55 4.8 15.8L4.5 17L4.2 15.8C4.15 15.55 3.95 15.35 3.7 15.3L2.5 15L3.7 14.7C3.95 14.65 4.15 14.45 4.2 14.2L4.5 13Z" fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </span>
        </template>
        <span>{{ $t('agent.createAgent') }}</span>
      </t-button>
    </div>
    <!-- 空状态：我的 -->
    <div v-if="spaceSelection === 'mine' && agents.length === 0 && !loading" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ $t('agent.empty.title') }}</span>
      <span class="empty-desc">{{ $t('agent.empty.description') }}</span>
      <t-button class="agent-create-btn empty-state-btn" @click="handleCreateAgent">
        <template #icon>
          <span class="btn-icon-wrapper">
            <svg class="sparkles-icon" width="18" height="18" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z" fill="currentColor" stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M15.5 4L15.8 5.2C15.85 5.45 16.05 5.65 16.3 5.7L17.5 6L16.3 6.3C16.05 6.35 15.85 6.55 15.8 6.8L15.5 8L15.2 6.8C15.15 6.55 14.95 6.35 14.7 6.3L13.5 6L14.7 5.7C14.95 5.65 15.15 5.45 15.2 5.2L15.5 4Z" fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M4.5 13L4.8 14.2C4.85 14.45 5.05 14.65 5.3 14.7L6.5 15L5.3 15.3C5.05 15.35 4.85 15.55 4.8 15.8L4.5 17L4.2 15.8C4.15 15.55 3.95 15.35 3.7 15.3L2.5 15L3.7 14.7C3.95 14.65 4.15 14.45 4.2 14.2L4.5 13Z" fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </span>
        </template>
        <span>{{ $t('agent.createAgent') }}</span>
      </t-button>
    </div>
    <!-- 空状态：空间下 -->
    <div v-if="spaceSelectionOrgId && !spaceAgentsLoading && spaceAgentsList.length === 0" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ $t('agent.empty.sharedTitle') }}</span>
      <span class="empty-desc">{{ $t('agent.empty.sharedDescription') }}</span>
    </div>
      </div>
    </div>

    <!-- 删除确认对话框 -->
    <t-dialog 
      v-model:visible="deleteVisible" 
      dialogClassName="del-agent-dialog" 
      :closeBtn="false" 
      :cancelBtn="null"
      :confirmBtn="null"
    >
      <div class="circle-wrap">
        <div class="dialog-header">
          <img class="circle-img" src="@/assets/img/circle.png" alt="">
          <span class="circle-title">{{ $t('agent.delete.confirmTitle') }}</span>
        </div>
        <span class="del-circle-txt">
          {{ $t('agent.delete.confirmMessage', { name: deletingAgent?.name ?? '' }) }}
        </span>
        <div class="circle-btn">
          <span class="circle-btn-txt" @click="deleteVisible = false">{{ $t('common.cancel') }}</span>
          <span class="circle-btn-txt confirm" @click="confirmDelete">{{ $t('agent.delete.confirmButton') }}</span>
        </div>
      </div>
    </t-dialog>

    <!-- 共享智能体详情侧边栏 -->
    <Transition name="shared-detail-drawer">
      <div v-if="sharedDetailVisible && currentSharedAgent" class="shared-detail-drawer-overlay" @click.self="closeSharedAgentDetail">
        <div class="shared-detail-drawer">
          <div class="shared-detail-drawer-header">
            <h3 class="shared-detail-drawer-title">{{ $t('agent.detail.title') || '智能体详情' }}</h3>
            <button type="button" class="shared-detail-drawer-close" @click="closeSharedAgentDetail" :aria-label="$t('general.close')">
              <t-icon name="close" />
            </button>
          </div>
          <div class="shared-detail-drawer-body">
            <div class="shared-detail-row">
              <span class="shared-detail-label">{{ $t('agent.editor.name') || '名称' }}</span>
              <span class="shared-detail-value">{{ currentSharedAgent.agent?.name }}</span>
            </div>
            <div class="shared-detail-row">
              <span class="shared-detail-label">{{ $t('knowledgeList.detail.sourceOrg') || '来源空间' }}</span>
              <span class="shared-detail-value shared-detail-org">
                <img src="@/assets/img/organization-green.svg" class="shared-detail-org-icon" alt="" aria-hidden="true" />
                <span>{{ currentSharedAgent.org_name }}</span>
              </span>
            </div>
            <div class="shared-detail-row">
              <span class="shared-detail-label">{{ $t('knowledgeList.detail.myPermission') || '我的权限' }}</span>
              <span class="shared-detail-value">{{ $t('organization.share.permissionReadonly') }}</span>
            </div>
            <!-- 能力范围（与共享范围说明一致） -->
            <template v-if="currentSharedAgent.agent?.config">
              <div class="shared-detail-section-title">{{ $t('agent.shareScope.title') }}</div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.knowledgeBase') }}</span>
                <span class="shared-detail-value">{{ sharedAgentKbScopeText }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.chatModel') }}</span>
                <span class="shared-detail-value">{{ currentSharedAgent.agent.config.model_id ? $t('agent.shareScope.modelConfigured') : $t('agent.shareScope.modelNotSet') }}</span>
              </div>
              <div v-if="sharedAgentUsesKb" class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.rerankModel') }}</span>
                <span class="shared-detail-value">{{ currentSharedAgent.agent.config.rerank_model_id ? $t('agent.shareScope.modelConfigured') : $t('agent.shareScope.modelNotSet') }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.webSearch') }}</span>
                <span class="shared-detail-value">{{ currentSharedAgent.agent.config.web_search_enabled ? $t('agent.shareScope.enabled') : $t('agent.shareScope.disabled') }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.mcp') }}</span>
                <span class="shared-detail-value">{{ sharedAgentMcpScopeText }}</span>
              </div>
            </template>
          </div>
          <div class="shared-detail-drawer-footer">
            <t-button theme="primary" block @click="handleUseSharedAgentInChat(currentSharedAgent)">
              {{ $t('agent.detail.useInChat') || '在对话中使用' }}
            </t-button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 智能体编辑器弹窗 -->
    <AgentEditorModal 
      :visible="editorVisible"
      :mode="editorMode"
      :agent="editingAgent"
      :initialSection="editorInitialSection"
      @update:visible="editorVisible = $event"
      @success="handleEditorSuccess"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin, Icon as TIcon } from 'tdesign-vue-next'
import { listAgents, deleteAgent, copyAgent, type CustomAgent } from '@/api/agent'
import { formatStringDate } from '@/utils/index'
import { useI18n } from 'vue-i18n'
import { createSessions } from '@/api/chat/index'
import { useOrganizationStore } from '@/stores/organization'
import { setSharedAgentDisabledByMe, listOrganizationSharedAgents } from '@/api/organization'
import { useSettingsStore } from '@/stores/settings'
import { useMenuStore } from '@/stores/menu'
import type { SharedAgentInfo, OrganizationSharedAgentItem } from '@/api/organization'
import AgentEditorModal from './AgentEditorModal.vue'
import AgentAvatar from '@/components/AgentAvatar.vue'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const orgStore = useOrganizationStore()

interface AgentWithUI extends CustomAgent {
  showMore?: boolean
  /** 当前租户在对话下拉中停用（仅影响本租户） */
  disabled_by_me?: boolean
}

/** Merged agent for "all" tab: my agents (isMine: true) or shared (isMine: false, org_name, source_tenant_id, share_id, disabled_by_me?) */
type DisplayAgent = (AgentWithUI & { isMine: true }) | (CustomAgent & { isMine: false; org_name: string; source_tenant_id: number; share_id: string; showMore?: boolean; disabled_by_me?: boolean })

// 左侧空间选择：我的 / 空间 ID（已去掉「全部」）
const spaceSelection = ref<'all' | 'mine' | string>('mine')
const agents = ref<AgentWithUI[]>([])
const sharedAgents = computed<SharedAgentInfo[]>(() => orgStore.sharedAgents || [])
const allAgentsCount = computed(() => agents.value.length + sharedAgents.value.length)

const spaceSelectionOrgId = computed(() => {
  const s = spaceSelection.value
  return s !== 'all' && s !== 'mine' && !!s
})

const sharedAgentsByOrg = computed(() => {
  const orgId = spaceSelection.value
  if (orgId === 'all' || orgId === 'mine') return []
  return sharedAgents.value.filter(s => s.organization_id === orgId)
})

// 空间视角：该空间内全部智能体（含我共享的），选中空间时请求新接口
const spaceAgentsList = ref<OrganizationSharedAgentItem[]>([])
const spaceAgentsLoading = ref(false)
const spaceAgentCountByOrg = ref<Record<string, number>>({})

// 各空间下的共享智能体数量（用于侧栏展示）：优先用接口返回的该空间总数
const sharedCountByOrg = computed<Record<string, number>>(() => {
  const map: Record<string, number> = {}
  sharedAgents.value.forEach(s => {
    const id = s.organization_id
    if (!id) return
    map[id] = (map[id] || 0) + 1
  })
  ;(orgStore.organizations || []).forEach(org => {
    if (map[org.id] === undefined) map[org.id] = 0
  })
  return map
})
const effectiveSharedCountByOrg = computed<Record<string, number>>(() => {
  const base = sharedCountByOrg.value
  const merged = { ...base }
  Object.keys(spaceAgentCountByOrg.value).forEach(orgId => {
    merged[orgId] = spaceAgentCountByOrg.value[orgId]
  })
  return merged
})

const filteredAgents = computed<DisplayAgent[]>(() => {
  if (spaceSelection.value === 'mine') {
    return agents.value.map(a => ({ ...a, isMine: true as const }))
  }
  if (spaceSelection.value !== 'all') return []
  const list: DisplayAgent[] = []
  agents.value.forEach(a => list.push({ ...a, isMine: true as const }))
  sharedAgents.value.forEach(shared => {
    if (!shared.agent) return
    list.push({
      ...shared.agent,
      isMine: false as const,
      org_name: shared.org_name,
      source_tenant_id: shared.source_tenant_id,
      share_id: shared.share_id,
      disabled_by_me: shared.disabled_by_me,
      showMore: false
    } as DisplayAgent)
  })
  return list
})
const loading = ref(false)
const deleteVisible = ref(false)
const deletingAgent = ref<AgentWithUI | null>(null)
const sharedDetailVisible = ref(false)
const currentSharedAgent = ref<SharedAgentInfo | null>(null)
const sharedAgentUsesKb = computed(() => {
  const c = currentSharedAgent.value?.agent?.config
  if (!c) return false
  return c.kb_selection_mode !== 'none' && c.kb_selection_mode !== undefined
})
const sharedAgentKbScopeText = computed(() => {
  const c = currentSharedAgent.value?.agent?.config
  if (!c) return t('agent.shareScope.kbNone')
  if (c.kb_selection_mode === 'all') return t('agent.shareScope.kbAll')
  if (c.kb_selection_mode === 'selected' && c.knowledge_bases?.length) return t('agent.shareScope.kbSelected', { count: c.knowledge_bases.length })
  return t('agent.shareScope.kbNone')
})
const sharedAgentMcpScopeText = computed(() => {
  const c = currentSharedAgent.value?.agent?.config
  if (!c) return t('agent.shareScope.mcpNone')
  if (c.mcp_selection_mode === 'all') return t('agent.shareScope.mcpAll')
  if (c.mcp_selection_mode === 'selected' && c.mcp_services?.length) return t('agent.shareScope.mcpSelected', { count: c.mcp_services.length })
  return t('agent.shareScope.mcpNone')
})
const editorVisible = ref(false)
const editorMode = ref<'create' | 'edit'>('create')
const editingAgent = ref<CustomAgent | null>(null)
const editorInitialSection = ref<string>('basic')
/** 当前打开三点菜单的卡片 agent.id（用于受控弹出层，避免 computed 项无持久引用导致菜单不响应） */
const openMoreAgentId = ref<string | null>(null)

const fetchList = () => {
  loading.value = true
  return Promise.all([
    listAgents().then((res: any) => {
      const data = res.data || []
      const disabledOwnIds = res.disabled_own_agent_ids || []
      agents.value = data.map((agent: CustomAgent) => ({
        ...agent,
        showMore: false,
        disabled_by_me: disabledOwnIds.includes(agent.id)
      }))
      checkAndOpenEditModal()
    }),
    orgStore.fetchSharedAgents(),
    orgStore.fetchOrganizations()
  ]).finally(() => { loading.value = false }).then(() => {
    // 各空间智能体数量已由 GET /organizations 的 resource_counts 带回，存于 orgStore.resourceCounts
    const counts = orgStore.resourceCounts?.agents?.by_organization
    if (counts) spaceAgentCountByOrg.value = { ...counts }
  })
}

// 检查 URL 参数并打开编辑模态框
const checkAndOpenEditModal = () => {
  const editId = route.query.edit as string
  const section = route.query.section as string
  if (editId) {
    const agent = agents.value.find(a => a.id === editId)
    if (agent) {
      editingAgent.value = agent
      editorMode.value = 'edit'
      editorInitialSection.value = section || 'basic'
      editorVisible.value = true
    }
    // 清除 URL 中的参数
    router.replace({ path: route.path, query: {} })
  }
}

// 监听菜单创建智能体事件
const handleOpenAgentEditor = (event: CustomEvent) => {
  if (event.detail?.mode === 'create') {
    openCreateModal()
  }
}

// 选中空间时请求该空间内全部智能体（含我共享的）
watch(spaceSelection, (val) => {
  if (val === 'all' || val === 'mine' || !val) {
    spaceAgentsList.value = []
    return
  }
  spaceAgentsLoading.value = true
  listOrganizationSharedAgents(val).then((res) => {
    if (res.success && res.data) {
      spaceAgentsList.value = res.data
      spaceAgentCountByOrg.value = { ...spaceAgentCountByOrg.value, [val]: res.data.length }
    } else {
      spaceAgentsList.value = []
    }
  }).finally(() => {
    spaceAgentsLoading.value = false
  })
}, { immediate: true })

onMounted(() => {
  fetchList()
  window.addEventListener('openAgentEditor', handleOpenAgentEditor as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('openAgentEditor', handleOpenAgentEditor as EventListener)
})

const onVisibleChange = (visible: boolean) => {
  if (!visible) {
    openMoreAgentId.value = null
  }
}

const toggleMore = (e: Event, agentId: string) => {
  e.stopPropagation()
  openMoreAgentId.value = openMoreAgentId.value === agentId ? null : agentId
}

const handleCardClick = (agent: DisplayAgent | AgentWithUI) => {
  if (openMoreAgentId.value === agent.id) return
  if ('isMine' in agent && !agent.isMine) {
    const shared = sharedAgents.value.find(s => s.agent?.id === agent.id && s.source_tenant_id === agent.source_tenant_id)
    if (shared) openSharedAgentDetail(shared)
    return
  }
  handleEdit(agent as AgentWithUI)
}

function openSharedAgentDetail(shared: SharedAgentInfo) {
  currentSharedAgent.value = shared
  sharedDetailVisible.value = true
}

/** 空间视角下点击卡片：我共享的进编辑，他人共享的打开详情抽屉 */
function handleSpaceAgentCardClick(shared: OrganizationSharedAgentItem) {
  if (shared.is_mine && shared.agent) {
    handleEdit({ ...shared.agent, showMore: false, disabled_by_me: shared.disabled_by_me } as AgentWithUI)
  } else {
    openSharedAgentDetail(shared)
  }
}

function closeSharedAgentDetail() {
  sharedDetailVisible.value = false
  currentSharedAgent.value = null
}

/** 在对话中使用共享智能体：创建新会话并跳转 */
async function handleUseSharedAgentInChat(shared: SharedAgentInfo) {
  if (!shared.agent?.id) return
  closeSharedAgentDetail()
  const settingsStore = useSettingsStore()
  const menuStore = useMenuStore()
  settingsStore.selectAgent(shared.agent.id, String(shared.source_tenant_id))
  try {
    const res = await createSessions({})
    if (res?.data?.id) {
      const sessionId = res.data.id
      const now = new Date().toISOString()
      menuStore.updataMenuChildren({
        title: t('createChat.newSessionTitle'),
        path: `chat/${sessionId}`,
        id: sessionId,
        isMore: false,
        isNoTitle: true,
        created_at: now,
        updated_at: now
      })
      menuStore.changeIsFirstSession(false)
      router.push({
        path: `/platform/chat/${sessionId}`,
        query: { agent_id: shared.agent.id, source_tenant_id: String(shared.source_tenant_id) }
      })
    } else {
      MessagePlugin.error(t('createChat.messages.createFailed') || '创建会话失败')
    }
  } catch (e) {
    console.error('Create session for shared agent failed', e)
    MessagePlugin.error(t('createChat.messages.createError') || '创建会话出错')
  }
}

const handleEdit = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  editingAgent.value = agent
  editorMode.value = 'edit'
  editorVisible.value = true
}

const handleDelete = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  deletingAgent.value = agent
  deleteVisible.value = true
}

const handleCopy = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  copyAgent(agent.id).then((res: any) => {
    if (res.data) {
      MessagePlugin.success(t('agent.messages.copied'))
      fetchList()
    } else {
      MessagePlugin.error(res.message || t('agent.messages.copyFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.copyFailed'))
  })
}

/** 切换「我的」智能体停用状态（仅影响当前租户对话下拉显示） */
const handleToggleDisabled = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  const nextDisabled = !agent.disabled_by_me
  setSharedAgentDisabledByMe(agent.id, nextDisabled).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(nextDisabled ? t('agent.messages.disabled') : t('agent.messages.enabled'))
      fetchList()
    } else {
      MessagePlugin.error(res.message || t('agent.messages.saveFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.saveFailed'))
  })
}

/** 切换共享智能体“停用”状态（仅影响当前用户对话下拉显示） */
const handleToggleSharedDisabled = (agent: DisplayAgent) => {
  if (agent.isMine) return
  openMoreAgentId.value = null
  const nextDisabled = !agent.disabled_by_me
  setSharedAgentDisabledByMe(agent.id, nextDisabled).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(nextDisabled ? t('agent.messages.disabled') : t('agent.messages.enabled'))
      orgStore.fetchSharedAgents()
    } else {
      MessagePlugin.error(res.message || t('agent.messages.saveFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.saveFailed'))
  })
}

const handleToggleSharedDisabledFromShared = (shared: SharedAgentInfo) => {
  if (!shared.agent) return
  openMoreAgentId.value = null
  const nextDisabled = !shared.disabled_by_me
  setSharedAgentDisabledByMe(shared.agent.id, nextDisabled).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(nextDisabled ? t('agent.messages.disabled') : t('agent.messages.enabled'))
      orgStore.fetchSharedAgents()
    } else {
      MessagePlugin.error(res.message || t('agent.messages.saveFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.saveFailed'))
  })
}

const confirmDelete = () => {
  if (!deletingAgent.value) return
  
  deleteAgent(deletingAgent.value.id).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(t('agent.messages.deleted'))
      deleteVisible.value = false
      deletingAgent.value = null
      fetchList()
    } else {
      MessagePlugin.error(res.message || t('agent.messages.deleteFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.deleteFailed'))
  })
}

const handleEditorSuccess = () => {
  editorVisible.value = false
  editingAgent.value = null
  fetchList()
}

const formatDate = (dateStr: string) => {
  if (!dateStr) return ''
  return formatStringDate(new Date(dateStr))
}

// 暴露创建方法供外部调用
const openCreateModal = () => {
  editingAgent.value = null
  editorMode.value = 'create'
  editorVisible.value = true
}

// 创建智能体
const handleCreateAgent = () => {
  openCreateModal()
}

defineExpose({
  openCreateModal
})
</script>

<style scoped lang="less">
.agent-list-container {
  padding: 24px 32px;
  margin: 0 16px 0 4px;
  height: calc(100vh);
  box-sizing: border-box;
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.agent-list-body {
  display: flex;
  flex: 1;
  min-height: 0;
  background: #fafbfc;
  border: 1px solid #e7ebf0;
  border-radius: 10px;
  overflow: hidden;
}

.agent-list-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 12px;
  background: #fafbfc;
}

.agent-list-main-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  padding: 12px;
  background: #fafbfc;
}

.shared-by-me-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 6px;
  background: rgba(7, 192, 95, 0.1);
  border-radius: 4px;
  font-size: 12px;
  color: #07c05f;
  margin-left: 6px;
}

.header {
  display: flex;
  align-items: center;
  margin-bottom: 20px;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  h2 {
    margin: 0;
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }
}

:deep(.agent-create-btn) {
  --ripple-color: rgba(118, 75, 162, 0.3) !important;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%) !important;
  border: none !important;
  color: #fff !important;
  position: relative;
  overflow: hidden;

  &:hover,
  &:active,
  &:focus,
  &.t-is-active,
  &[data-state="active"] {
    background: linear-gradient(135deg, #5a6fd6 0%, #6a4190 100%) !important;
    border: none !important;
    color: #fff !important;
  }

  --td-button-primary-bg-color: #667eea !important;
  --td-button-primary-border-color: #667eea !important;
  --td-button-primary-active-bg-color: #5a6fd6 !important;
  --td-button-primary-active-border-color: #5a6fd6 !important;

  .btn-icon-wrapper {
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }

  .sparkles-icon {
    animation: twinkle 2s ease-in-out infinite;
  }

  &::before {
    content: '';
    position: absolute;
    top: -50%;
    left: -50%;
    width: 200%;
    height: 200%;
    background: linear-gradient(
      45deg,
      transparent 30%,
      rgba(255, 255, 255, 0.1) 50%,
      transparent 70%
    );
    transform: translateX(-100%);
    transition: transform 0.6s ease;
    z-index: 0;
  }

  &:hover::before {
    transform: translateX(100%);
  }
}

@keyframes twinkle {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.8; transform: scale(0.95); }
}

.header-subtitle {
  margin: 0;
  color: #00000099;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

.agent-tabs {
  display: flex;
  align-items: center;
  gap: 24px;
  border-bottom: 1px solid #e7ebf0;
  margin-bottom: 20px;

  .tab-item {
    padding: 12px 0;
    cursor: pointer;
    color: #666;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    transition: color 0.2s;

    &:hover {
      color: #1a1a1a;
    }

    &.active {
      color: #07c05f;
      font-weight: 600;
      border-bottom: 2px solid #07c05f;
      margin-bottom: -1px;
    }
  }
}

.shared-badge {
  flex-shrink: 0;
}

.card-bottom-source {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.04);
  flex-shrink: 0;
}

.card-bottom-source .org-icon {
  width: 12px;
  height: 12px;
  flex-shrink: 0;
}

.org-source-text {
  color: #666;
  font-family: "PingFang SC";
  font-size: 11px;
  font-weight: 500;
  flex-shrink: 0;
}

.custom-badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.04);
  color: #666;
  font-family: "PingFang SC";
  font-size: 11px;
  font-weight: 500;
  flex-shrink: 0;
}


.agent-card-wrap {
  display: grid;
  gap: 20px;
  grid-template-columns: 1fr;
}

/* 与知识库列表卡片统一尺寸：160px 高、18px 20px 内边距、12px 圆角 */
.agent-card {
  border: 1px solid #f0f0f0;
  border-radius: 12px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: #fff;
  position: relative;
  cursor: pointer;
  transition: all 0.25s ease;
  padding: 18px 20px;
  display: flex;
  flex-direction: column;
  height: 160px;
  min-height: 160px;

  &:hover {
    border-color: #07c05f;
    box-shadow: 0 4px 12px rgba(7, 192, 95, 0.12);
  }

  // 普通模式样式
  &.agent-mode-normal {
    background: linear-gradient(135deg, #ffffff 0%, #f8fcfa 100%);
    border-color: #e8f5ed;

    &:hover {
      border-color: #07c05f;
      background: linear-gradient(135deg, #ffffff 0%, #f0fdf4 100%);
    }

    .card-decoration {
      color: rgba(7, 192, 95, 0.35);
    }

    &:hover .card-decoration {
      color: rgba(7, 192, 95, 0.5);
    }
  }

  // Agent 模式样式
  &.agent-mode-agent {
    background: linear-gradient(135deg, #ffffff 0%, #f8f5ff 100%);
    border-color: #ede8ff;

    &:hover {
      border-color: #7c4dff;
      box-shadow: 0 4px 12px rgba(124, 77, 255, 0.12);
      background: linear-gradient(135deg, #ffffff 0%, #f3efff 100%);
    }

    .card-decoration {
      color: rgba(124, 77, 255, 0.35);
    }

    &:hover .card-decoration {
      color: rgba(124, 77, 255, 0.5);
    }
  }

  // 确保内容在装饰之上
  .card-header,
  .card-content,
  .card-bottom {
    position: relative;
    z-index: 1;
  }

  .card-header {
    margin-bottom: 10px;
  }

  .card-title {
    font-size: 16px;
    line-height: 24px;
  }

  .card-content {
    margin-bottom: 10px;
  }

  .card-description {
    font-size: 12px;
    line-height: 18px;
  }

  .card-bottom {
    padding-top: 8px;
  }

  .more-wrap {
    width: 28px;
    height: 28px;

    .more-icon {
      width: 16px;
      height: 16px;
    }
  }

  .builtin-avatar {
    width: 32px;
    height: 32px;
    border-radius: 8px;
  }

  .edit-btn {
    width: 32px;
    height: 32px;
    border-radius: 8px;
  }
}

.card-decoration {
  position: absolute;
  top: 12px;
  right: 44px;
  display: flex;
  align-items: flex-start;
  gap: 4px;
  pointer-events: none;
  z-index: 0;
  transition: color 0.25s ease;
  
  .star-icon {
    opacity: 0.9;
    
    &.small {
      margin-top: 10px;
      opacity: 0.7;
    }
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.card-title {
  color: #1d2129;
  font-family: "PingFang SC", -apple-system, sans-serif;
  font-size: 15px;
  font-weight: 600;
  line-height: 22px;
  letter-spacing: 0.01em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.builtin-badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.04);
  color: #666;
  font-family: "PingFang SC";
  font-size: 11px;
  font-weight: 500;
  flex-shrink: 0;
}

.builtin-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  flex-shrink: 0;

  &.agent-emoji {
    font-size: 18px;
    line-height: 1;
    background: rgba(0, 0, 0, 0.04);
  }
  
  &.normal {
    background: linear-gradient(135deg, rgba(7, 192, 95, 0.15) 0%, rgba(7, 192, 95, 0.08) 100%);
    color: #059669;
  }
  
  &.agent {
    background: linear-gradient(135deg, rgba(124, 77, 255, 0.15) 0%, rgba(124, 77, 255, 0.08) 100%);
    color: #7c4dff;
  }
}

.edit-btn {
  display: flex;
  width: 32px;
  height: 32px;
  justify-content: center;
  align-items: center;
  border-radius: 8px;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.2s ease;
  color: #00000066;

  &:hover {
    background: rgba(0, 0, 0, 0.06);
    color: #07c05f;
  }
}

.more-wrap {
  display: flex;
  width: 28px;
  height: 28px;
  justify-content: center;
  align-items: center;
  border-radius: 8px;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.2s ease;
  opacity: 0;

  .agent-card:hover & {
    opacity: 0.6;
  }

  &:hover {
    background: rgba(0, 0, 0, 0.05);
    opacity: 1 !important;
  }

  &.active-more {
    background: rgba(0, 0, 0, 0.06);
    opacity: 1 !important;
  }

  .more-icon {
    width: 16px;
    height: 16px;
  }
}

/* 与知识库卡片内容区一致 */
.card-content {
  flex: 1;
  min-height: 0;
  margin-bottom: 8px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* 三个列表卡片统一：描述字体 */
.card-description {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  color: #666;
  font-family: "PingFang SC", -apple-system, sans-serif;
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.card-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid #f0f0f0;
}

.bottom-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.feature-badges {
  display: flex;
  align-items: center;
  gap: 4px;
}

.feature-badge {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 5px;
  cursor: default;
  transition: background 0.2s ease;

  &.mode-normal {
    background: rgba(7, 192, 95, 0.08);
    color: #059669;

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }
  }

  &.mode-agent {
    background: rgba(124, 77, 255, 0.08);
    color: #7c4dff;

    &:hover {
      background: rgba(124, 77, 255, 0.12);
    }
  }

  &.web-search {
    background: rgba(255, 152, 0, 0.08);
    color: #f59e0b;

    &:hover {
      background: rgba(255, 152, 0, 0.12);
    }
  }

  &.knowledge {
    background: rgba(7, 192, 95, 0.08);
    color: #059669;

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }
  }

  &.mcp {
    background: rgba(236, 72, 153, 0.08);
    color: #ec4899;

    &:hover {
      background: rgba(236, 72, 153, 0.12);
    }
  }

  &.multi-turn {
    background: rgba(59, 130, 246, 0.08);
    color: #3b82f6;

    &:hover {
      background: rgba(59, 130, 246, 0.12);
    }
  }
}

.card-time {
  color: #999;
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 400;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  padding: 60px 20px;

  .empty-img {
    width: 162px;
    height: 162px;
    margin-bottom: 20px;
  }

  .empty-txt {
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 26px;
    margin-bottom: 8px;
  }

  .empty-desc {
    color: #00000066;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    margin-bottom: 0;
  }

  .empty-state-btn {
    margin-top: 20px;
  }
}

// 响应式布局
@media (min-width: 900px) {
  .agent-card-wrap {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (min-width: 1250px) {
  .agent-card-wrap {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (min-width: 1600px) {
  .agent-card-wrap {
    grid-template-columns: repeat(4, 1fr);
  }
}

// 删除确认对话框样式
:deep(.del-agent-dialog) {
  padding: 0px !important;
  border-radius: 6px !important;

  .t-dialog__header {
    display: none;
  }

  .t-dialog__body {
    padding: 16px;
  }

  .t-dialog__footer {
    padding: 0;
  }
}

:deep(.t-dialog__position.t-dialog--top) {
  padding-top: 40vh !important;
}

.circle-wrap {
  .dialog-header {
    display: flex;
    align-items: center;
    margin-bottom: 8px;
  }

  .circle-img {
    width: 20px;
    height: 20px;
    margin-right: 8px;
  }

  .circle-title {
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
  }

  .del-circle-txt {
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    display: inline-block;
    margin-left: 29px;
    margin-bottom: 21px;
  }

  .circle-btn {
    height: 22px;
    width: 100%;
    display: flex;
    justify-content: flex-end;
  }

  .circle-btn-txt {
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    cursor: pointer;

    &:hover {
      opacity: 0.8;
    }
  }

  .confirm {
    color: #FA5151;
    margin-left: 40px;

    &:hover {
      opacity: 0.8;
    }
  }
}
</style>

<style lang="less">
// 更多操作弹窗样式
.card-more-popup {
  z-index: 99 !important;

  .t-popup__content {
    padding: 6px 0 !important;
    margin-top: 6px !important;
    min-width: 140px;
    border-radius: 6px !important;
    box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1) !important;
    border: 1px solid #e7ebf0 !important;
  }
}

.popup-menu {
  display: flex;
  flex-direction: column;
}

.popup-menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  color: #000000e6;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;

  .menu-icon {
    font-size: 16px;
    flex-shrink: 0;
    color: #00000099;
    transition: color 0.2s ease;
  }

  &:hover {
    background: #f7f9fc;
    
    .menu-icon {
      color: #000000e6;
    }
  }

  &.delete {
    color: #000000e6;
    
    &:hover {
      background: #fff1f0;
      color: #fa5151;

      .menu-icon {
        color: #fa5151;
      }
    }
  }
}

// 共享智能体详情侧边栏
.shared-detail-drawer-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 1000;
  display: flex;
  justify-content: flex-end;
}

.shared-detail-drawer {
  width: 360px;
  max-width: 90vw;
  height: 100%;
  background: #fff;
  box-shadow: -4px 0 24px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  font-family: "PingFang SC", sans-serif;
}

.shared-detail-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 24px;
  border-bottom: 1px solid #e7ebf0;
  flex-shrink: 0;
}

.shared-detail-drawer-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #1d2129;
}

.shared-detail-drawer-close {
  width: 32px;
  height: 32px;
  border: none;
  border-radius: 6px;
  background: #f5f6f8;
  color: #86909c;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.2s ease, color 0.2s ease;

  &:hover {
    background: #e7ebf0;
    color: #1d2129;
  }
}

.shared-detail-drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.shared-detail-drawer-body .shared-detail-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.shared-detail-drawer-body .shared-detail-section-title {
  font-size: 13px;
  font-weight: 600;
  color: #1d2129;
  margin: 20px 0 12px 0;
  padding-top: 16px;
  border-top: 1px solid #e7ebf0;
}

.shared-detail-drawer-body .shared-detail-label {
  font-size: 12px;
  color: #86909c;
  line-height: 1.4;
}

.shared-detail-drawer-body .shared-detail-value {
  font-size: 14px;
  color: #1d2129;
  line-height: 1.5;
  word-break: break-word;

  &.shared-detail-org {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
}

.shared-detail-drawer-body .shared-detail-org-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

.shared-detail-drawer-footer {
  padding: 16px 24px;
  border-top: 1px solid #e7ebf0;
  flex-shrink: 0;
  background: #fff;
}

.shared-detail-drawer-enter-active,
.shared-detail-drawer-leave-active {
  transition: opacity 0.25s ease;

  .shared-detail-drawer {
    transition: transform 0.25s ease;
  }
}

.shared-detail-drawer-enter-from,
.shared-detail-drawer-leave-to {
  opacity: 0;

  .shared-detail-drawer {
    transform: translateX(100%);
  }
}
</style>
