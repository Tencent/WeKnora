<template>
  <div class="agent-list-container">
    <ListSpaceSidebar v-if="!authStore.isLiteMode" v-model="spaceSelection" :count-all="allAgentsCount"
      :count-mine="agents.length" :count-by-org="effectiveSharedCountByOrg" :count-favorites="agentFavoritesCount"
      :count-recents="agentRecentsCount" />
    <div class="agent-list-content">
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <div class="title-row" style="--wails-draggable: drag">
            <h2 style="--wails-draggable: drag">{{ $t('agent.title') }}</h2>
            <t-tooltip v-if="authStore.hasRole('contributor')" :content="$t('agent.createAgent')" placement="bottom">
              <t-button variant="text" theme="default" size="small" class="header-action-btn"
                data-guide="agent-list-create" style="--wails-draggable: no-drag" @click="handleCreateAgent">
                <template #icon>
                  <span class="btn-icon-wrapper">
                    <svg class="sparkles-icon" width="19" height="19" viewBox="0 0 20 20" fill="none"
                      xmlns="http://www.w3.org/2000/svg">
                      <path
                        d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                        fill="currentColor" stroke="currentColor" stroke-width="0.8" stroke-linecap="round"
                        stroke-linejoin="round" />
                      <path
                        d="M15.5 4L15.8 5.2C15.85 5.45 16.05 5.65 16.3 5.7L17.5 6L16.3 6.3C16.05 6.35 15.85 6.55 15.8 6.8L15.5 8L15.2 6.8C15.15 6.55 14.95 6.35 14.7 6.3L13.5 6L14.7 5.7C14.95 5.65 15.15 5.45 15.2 5.2L15.5 4Z"
                        fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round"
                        stroke-linejoin="round" />
                      <path
                        d="M4.5 13L4.8 14.2C4.85 14.45 5.05 14.65 5.3 14.7L6.5 15L5.3 15.3C5.05 15.35 4.85 15.55 4.8 15.8L4.5 17L4.2 15.8C4.15 15.55 3.95 15.35 3.7 15.3L2.5 15L3.7 14.7C3.95 14.65 4.15 14.45 4.2 14.2L4.5 13Z"
                        fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round"
                        stroke-linejoin="round" />
                    </svg>
                  </span>
                </template>
              </t-button>
            </t-tooltip>
          </div>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('agent.subtitle') }}</p>
        </div>
      </div>
      <div class="agent-list-main">
        <!-- creator filter removed; see KnowledgeBaseList for rationale.
             Card-level creator display + URL-state field are retained. -->

        <!-- 骨架屏占位 -->
        <div v-if="loading && agents.length === 0" class="agent-card-wrap">
          <div v-for="n in 6" :key="'skel-' + n" class="agent-card agent-card-skeleton">
            <div class="card-header">
              <div class="card-header-left">
                <t-skeleton animation="gradient"
                  :row-col="[[{ width: '32px', height: '32px', type: 'circle' }, { width: '40%', height: '18px' }]]" />
              </div>
            </div>
            <div class="card-content">
              <t-skeleton animation="gradient"
                :row-col="[{ width: '100%', height: '14px' }, { width: '70%', height: '14px' }]" />
            </div>
            <div class="card-bottom">
              <t-skeleton animation="gradient"
                :row-col="[[{ width: '60px', height: '22px', type: 'rect' }, { width: '60px', height: '22px', type: 'rect' }]]" />
            </div>
          </div>
        </div>

        <!-- 全部 / 收藏 / 最近：共用同一份卡片模板 -->
        <div
          v-if="(spaceSelection === 'all' || spaceSelection === 'favorites' || spaceSelection === 'recents') && filteredAgents.length > 0"
          class="agent-card-wrap">
          <template v-for="(agent, index) in filteredAgents"
            :key="agent.isMine ? agent.id : `shared-${agent.share_id}`">
            <!-- 内置：始终置顶。filteredAgents 在 all 视图里已经把
                 builtin 排到最前；这里只在第一张 builtin 之前打一次标题。 -->
            <div v-if="showShareGroupHeaders
              && agent.isMine
              && agent.is_builtin
              && (index === 0
                || !filteredAgents[index - 1].isMine
                || !(filteredAgents[index - 1] as AgentWithUI).is_builtin)" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('builtin')"
              @keydown.enter.prevent="toggleAgentSection('builtin')"
              @keydown.space.prevent="toggleAgentSection('builtin')">
              <t-icon name="app" size="14px" />
              <span>{{ $t('agent.sections.builtin') }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.builtin }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('builtin') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 我创建的：当前 agent 是本租户 + 非内置 + 我亲手创建，且前一张
                 要么不存在、要么不是本租户、要么是内置（builtin → mine 过渡）、
                 要么是同事创建。与 KB 列表对齐。 -->
            <div v-if="showShareGroupHeaders
              && agent.isMine
              && !agent.is_builtin
              && isMyAgent(agent)
              && (index === 0
                || !filteredAgents[index - 1].isMine
                || (filteredAgents[index - 1] as AgentWithUI).is_builtin
                || !isMyAgent(filteredAgents[index - 1] as AgentWithUI))" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('mine')"
              @keydown.enter.prevent="toggleAgentSection('mine')"
              @keydown.space.prevent="toggleAgentSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('agent.sections.mine') }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.mine }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 本空间 · 仅查看 / 其他成员：本租户里非内置且非我创建的同事 agent。 -->
            <div v-if="showShareGroupHeaders
              && agent.isMine
              && !agent.is_builtin
              && !isMyAgent(agent)
              && (index === 0
                || !filteredAgents[index - 1].isMine
                || (filteredAgents[index - 1] as AgentWithUI).is_builtin
                || isMyAgent(filteredAgents[index - 1] as AgentWithUI))" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('tenantOthers')"
              @keydown.enter.prevent="toggleAgentSection('tenantOthers')"
              @keydown.space.prevent="toggleAgentSection('tenantOthers')">
              <t-icon :name="tenantSectionIconName" size="14px" />
              <span>{{ $t(tenantSectionLabelKey) }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.tenantOthers }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('tenantOthers') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：仅在「全部」视图过渡处显示分组标题 -->
            <div v-if="showShareGroupHeaders
              && !agent.isMine
              && isSharedAgentEditable((agent as any).permission)
              && (index === 0 || filteredAgents[index - 1].isMine)" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('sharedEditable')"
              @keydown.enter.prevent="toggleAgentSection('sharedEditable')"
              @keydown.space.prevent="toggleAgentSection('sharedEditable')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="edit-1" size="12px" class="agent-section-subicon" />
              <span>{{ $t('agent.sections.sharedEditable') }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.sharedEditable }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('sharedEditable') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 仅查看 -->
            <div v-if="showShareGroupHeaders
              && !agent.isMine
              && !isSharedAgentEditable((agent as any).permission)
              && (index === 0
                || filteredAgents[index - 1].isMine
                || isSharedAgentEditable((filteredAgents[index - 1] as any).permission))" class="agent-section-header"
              role="button" tabindex="0" @click="toggleAgentSection('sharedReadonly')"
              @keydown.enter.prevent="toggleAgentSection('sharedReadonly')"
              @keydown.space.prevent="toggleAgentSection('sharedReadonly')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="browse" size="12px" class="agent-section-subicon" />
              <span>{{ $t('agent.sections.sharedReadonly') }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.sharedReadonly }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('sharedReadonly') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <div v-show="!isAgentRowHidden(agent)" class="agent-card" :class="{
              'is-builtin': agent.is_builtin,
              'agent-mode-normal': agent.config?.agent_mode === 'quick-answer',
              'agent-mode-agent': agent.config?.agent_mode === 'smart-reasoning',
              'shared-agent-card': !agent.isMine
            }" @click="handleCardClick(agent)">
              <!-- 装饰星星 -->
              <div class="card-decoration">
                <svg class="star-icon" width="24" height="24" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"
                    fill="currentColor" fill-opacity="0.15" />
                </svg>
                <svg class="star-icon small" width="14" height="14" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"
                    fill="currentColor" fill-opacity="0.15" />
                </svg>
              </div>
              <!-- 收藏按钮：浮在卡片右上角；.card-header padding-right 已为
                   "更多"按钮腾出空间，避免重叠。 -->
              <button type="button" class="agent-favorite-star"
                :class="{ 'is-favorited': isAgentFavorited(agent.id) }"
                @click.stop="toggleFavoriteAgent(agent.id, $event)">
                <t-icon :name="isAgentFavorited(agent.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <div class="card-header">
                <div class="card-header-left">
                  <div v-if="agent.is_builtin" class="builtin-avatar"
                    :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
                    <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                      size="18px" />
                  </div>
                  <div v-else-if="agent.avatar" class="builtin-avatar agent-emoji">{{ agent.avatar }}</div>
                  <AgentAvatar v-else :name="agent.name" size="small" />
                  <span class="card-title" :title="agent.name">{{ agent.name }}</span>
                </div>
                <t-popup
                  v-if="agent.isMine && (canManageAgent(agent) || authStore.hasRole('contributor') || authStore.hasRole('admin'))"
                  :visible="openMoreAgentId === agent.id" trigger="hover" overlayClassName="card-more-popup"
                  destroy-on-close placement="bottom-right" @visible-change="onVisibleChange"
                  @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }">
                  <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === agent.id }"
                    @click="toggleMore($event, agent.id)">
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </div>
                  <template #content>
                    <div class="popup-menu">
                      <div v-if="canManageAgent(agent)" class="popup-menu-item" @click="handleEdit(agent)"><t-icon
                          class="menu-icon" name="edit" /><span>{{ $t('common.edit') }}</span></div>
                      <div v-if="authStore.hasRole('contributor')" class="popup-menu-item" @click="handleCopy(agent)">
                        <t-icon class="menu-icon" name="file-copy" /><span>{{ $t('common.copy') }}</span>
                      </div>
                      <div v-if="authStore.hasRole('admin')" class="popup-menu-item"
                        @click="handleToggleDisabled(agent)">
                        <t-icon class="menu-icon" name="poweroff" />
                        <span>{{ agent.disabled_by_me ? $t('agent.enable') : $t('agent.disable') }}</span>
                      </div>
                      <div v-if="!agent.is_builtin && canManageAgent(agent)" class="popup-menu-item delete"
                        @click="handleDelete(agent)"><t-icon class="menu-icon" name="delete" /><span>{{
                          $t('common.delete') }}</span></div>
                    </div>
                  </template>
                </t-popup>
                <t-popup v-else-if="!agent.isMine && authStore.hasRole('admin')"
                  :visible="openMoreAgentId === 'shared-' + agent.share_id" trigger="hover"
                  overlayClassName="card-more-popup" destroy-on-close placement="bottom-right"
                  @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }">
                  <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === 'shared-' + agent.share_id }"
                    @click.stop="toggleMore($event, 'shared-' + agent.share_id)">
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
                    <t-tag v-if="agent.isMine && agent.disabled_by_me" theme="default" size="small"
                      class="disabled-badge">{{
                        $t('agent.disabled') }}</t-tag>
                    <t-tag v-if="!agent.isMine && agent.disabled_by_me" theme="default" size="small"
                      class="disabled-badge">{{
                        $t('agent.disabled') }}</t-tag>
                    <t-tooltip
                      :content="agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.mode.agent') : $t('agent.mode.normal')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'mode-normal': agent.config?.agent_mode === 'quick-answer', 'mode-agent': agent.config?.agent_mode === 'smart-reasoning' }">
                        <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                          size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.web_search_enabled" :content="$t('agent.features.webSearch')"
                      placement="top">
                      <div class="feature-badge web-search">
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
                          <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none" />
                          <ellipse cx="8" cy="8" rx="2.5" ry="6" stroke="currentColor" stroke-width="1.2" fill="none" />
                          <line x1="2" y1="6" x2="14" y2="6" stroke="currentColor" stroke-width="1.2" />
                          <line x1="2" y1="10" x2="14" y2="10" stroke="currentColor" stroke-width="1.2" />
                        </svg>
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.knowledge_bases?.length || agent.config?.kb_selection_mode === 'all'"
                      :content="$t('agent.features.knowledgeBase')" placement="top">
                      <div class="feature-badge knowledge">
                        <t-icon name="folder" size="16px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.mcp_services?.length || agent.config?.mcp_selection_mode === 'all'"
                      :content="$t('agent.features.mcp')" placement="top">
                      <div class="feature-badge mcp">
                        <t-icon name="extension" size="16px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.multi_turn_enabled" :content="$t('agent.features.multiTurn')"
                      placement="top">
                      <div class="feature-badge multi-turn">
                        <t-icon name="chat-bubble" size="16px" />
                      </div>
                    </t-tooltip>
                  </div>
                </div>
                <!-- 右下角：内置 / 来源徽章 / 空间图标+名称 -->
                <div v-if="!agent.isMine" class="card-bottom-source">
                  <img src="@/assets/img/organization-green.svg" class="org-icon" alt="" aria-hidden="true" />
                  <span class="org-source-text">{{ agent.org_name }}</span>
                </div>
                <div v-else-if="agent.is_builtin" class="builtin-badge">
                  <t-icon name="lock-on" size="12px" />
                  <span>{{ $t('agent.builtin') }}</span>
                </div>
                <ResourceOriginBadge v-else :variant="agentOriginVariant(agent)"
                  :creator-name="(agent as any).creator_name" />
              </div>
            </div>
          </template>
        </div>

        <!-- 我的智能体 -->
        <div v-if="spaceSelection === 'mine' && sortedMineAgents.length > 0" class="agent-card-wrap">
          <template v-for="(agent, index) in sortedMineAgents" :key="agent.id">
            <!-- 内置：始终置顶。sortedMineAgents 已按 内置→我→同事 排序。 -->
            <div v-if="showShareGroupHeaders
              && agent.is_builtin
              && (index === 0 || !sortedMineAgents[index - 1].is_builtin)" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('builtin')"
              @keydown.enter.prevent="toggleAgentSection('builtin')"
              @keydown.space.prevent="toggleAgentSection('builtin')">
              <t-icon name="app" size="14px" />
              <span>{{ $t('agent.sections.builtin') }}</span>
              <span class="agent-section-count">{{ mineAgentSectionCounts.builtin }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('builtin') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 我创建的：第一张非内置且我亲手创建的卡片前打标题 -->
            <div v-if="showShareGroupHeaders
              && !agent.is_builtin
              && isMyAgent(agent)
              && (index === 0
                || sortedMineAgents[index - 1].is_builtin
                || !isMyAgent(sortedMineAgents[index - 1]))" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('mine')"
              @keydown.enter.prevent="toggleAgentSection('mine')"
              @keydown.space.prevent="toggleAgentSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('agent.sections.mine') }}</span>
              <span class="agent-section-count">{{ mineAgentSectionCounts.mine }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 本空间 · 仅查看 / 其他成员：非内置且非我创建的同事 agent -->
            <div v-if="showShareGroupHeaders
              && !agent.is_builtin
              && !isMyAgent(agent)
              && (index === 0
                || sortedMineAgents[index - 1].is_builtin
                || isMyAgent(sortedMineAgents[index - 1]))" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('tenantOthers')"
              @keydown.enter.prevent="toggleAgentSection('tenantOthers')"
              @keydown.space.prevent="toggleAgentSection('tenantOthers')">
              <t-icon :name="tenantSectionIconName" size="14px" />
              <span>{{ $t(tenantSectionLabelKey) }}</span>
              <span class="agent-section-count">{{ mineAgentSectionCounts.tenantOthers }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('tenantOthers') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <div v-show="!isAgentRowHidden(agent)" class="agent-card" :class="{
              'is-builtin': agent.is_builtin,
              'agent-mode-normal': agent.config?.agent_mode === 'quick-answer',
              'agent-mode-agent': agent.config?.agent_mode === 'smart-reasoning'
            }" @click="handleCardClick(agent)">
              <!-- 装饰星星 -->
              <div class="card-decoration">
                <svg class="star-icon" width="24" height="24" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"
                    fill="currentColor" fill-opacity="0.15" />
                </svg>
                <svg class="star-icon small" width="14" height="14" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"
                    fill="currentColor" fill-opacity="0.15" />
                </svg>
              </div>

              <button type="button" class="agent-favorite-star"
                :class="{ 'is-favorited': isAgentFavorited(agent.id) }"
                @click.stop="toggleFavoriteAgent(agent.id, $event)">
                <t-icon :name="isAgentFavorited(agent.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <div class="card-header-left">
                  <!-- 内置智能体使用简洁图标 -->
                  <div v-if="agent.is_builtin" class="builtin-avatar"
                    :class="agent.config?.agent_mode === 'smart-reasoning' ? 'agent' : 'normal'">
                    <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                      size="18px" />
                  </div>
                  <div v-else-if="agent.avatar" class="builtin-avatar agent-emoji">{{ agent.avatar }}</div>
                  <AgentAvatar v-else :name="agent.name" size="small" />
                  <span class="card-title" :title="agent.name">{{ agent.name }}</span>
                </div>
                <t-popup v-if="canManageAgent(agent) || authStore.hasRole('contributor') || authStore.hasRole('admin')"
                  :visible="openMoreAgentId === agent.id" trigger="hover" overlayClassName="card-more-popup"
                  destroy-on-close placement="bottom-right" @visible-change="onVisibleChange"
                  @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }">
                  <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === agent.id }"
                    @click="toggleMore($event, agent.id)">
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </div>
                  <template #content>
                    <div class="popup-menu">
                      <div v-if="canManageAgent(agent)" class="popup-menu-item" @click="handleEdit(agent)">
                        <t-icon class="menu-icon" name="edit" />
                        <span>{{ $t('common.edit') }}</span>
                      </div>
                      <div v-if="authStore.hasRole('contributor')" class="popup-menu-item" @click="handleCopy(agent)">
                        <t-icon class="menu-icon" name="file-copy" />
                        <span>{{ $t('common.copy') }}</span>
                      </div>
                      <div v-if="authStore.hasRole('admin')" class="popup-menu-item"
                        @click="handleToggleDisabled(agent)">
                        <t-icon class="menu-icon" name="poweroff" />
                        <span>{{ agent.disabled_by_me ? $t('agent.enable') : $t('agent.disable') }}</span>
                      </div>
                      <div v-if="!agent.is_builtin && canManageAgent(agent)" class="popup-menu-item delete"
                        @click="handleDelete(agent)">
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
                    <t-tag v-if="agent.disabled_by_me" theme="default" size="small" class="disabled-badge">{{
                      $t('agent.disabled') }}</t-tag>
                    <t-tooltip
                      :content="agent.config?.agent_mode === 'smart-reasoning' ? $t('agent.mode.agent') : $t('agent.mode.normal')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'mode-normal': agent.config?.agent_mode === 'quick-answer', 'mode-agent': agent.config?.agent_mode === 'smart-reasoning' }">
                        <t-icon :name="agent.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                          size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.web_search_enabled" :content="$t('agent.features.webSearch')"
                      placement="top">
                      <div class="feature-badge web-search">
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
                          <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none" />
                          <ellipse cx="8" cy="8" rx="2.5" ry="6" stroke="currentColor" stroke-width="1.2" fill="none" />
                          <line x1="2" y1="6" x2="14" y2="6" stroke="currentColor" stroke-width="1.2" />
                          <line x1="2" y1="10" x2="14" y2="10" stroke="currentColor" stroke-width="1.2" />
                        </svg>
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.knowledge_bases?.length || agent.config?.kb_selection_mode === 'all'"
                      :content="$t('agent.features.knowledgeBase')" placement="top">
                      <div class="feature-badge knowledge">
                        <t-icon name="folder" size="16px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.mcp_services?.length || agent.config?.mcp_selection_mode === 'all'"
                      :content="$t('agent.features.mcp')" placement="top">
                      <div class="feature-badge mcp">
                        <t-icon name="extension" size="16px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="agent.config?.multi_turn_enabled" :content="$t('agent.features.multiTurn')"
                      placement="top">
                      <div class="feature-badge multi-turn">
                        <t-icon name="chat-bubble" size="16px" />
                      </div>
                    </t-tooltip>
                  </div>
                </div>
                <!-- 右下角：内置 / 来源徽章（我创建 / 同租户其他成员） -->
                <div v-if="agent.is_builtin" class="builtin-badge">
                  <t-icon name="lock-on" size="12px" />
                  <span>{{ $t('agent.builtin') }}</span>
                </div>
                <ResourceOriginBadge v-else :variant="agentOriginVariant(agent)"
                  :creator-name="(agent as any).creator_name" />
              </div>
            </div>
          </template>
        </div>

        <!-- 按空间筛选：该空间内全部智能体（含我共享的） -->
        <div v-if="spaceSelectionOrgId && spaceAgentsLoading" class="agent-list-main-loading">
          <t-loading size="medium" text="" />
        </div>
        <div v-else-if="spaceSelectionOrgId && sortedSpaceAgentsList.length > 0" class="agent-card-wrap">
          <template v-for="(shared, index) in sortedSpaceAgentsList" :key="'shared-' + shared.share_id">
            <!-- 我共享的：当前用户共享进本空间的智能体，只在首条 is_mine 上挂标题 -->
            <div v-if="showShareGroupHeaders && shared.is_mine && index === 0" class="agent-section-header"
              role="button" tabindex="0" @click="toggleAgentSection('sharedByMe')"
              @keydown.enter.prevent="toggleAgentSection('sharedByMe')"
              @keydown.space.prevent="toggleAgentSection('sharedByMe')">
              <t-icon name="share" size="14px" />
              <span>{{ $t('agent.sections.sharedByMe') }}</span>
              <span class="agent-section-count">{{ spaceAgentSectionCounts.sharedByMe }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('sharedByMe') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：首次从 is_mine 进入共享 + editable -->
            <div v-if="showShareGroupHeaders
              && !shared.is_mine
              && isSharedAgentEditable(shared.permission)
              && (index === 0 || sortedSpaceAgentsList[index - 1].is_mine)" class="agent-section-header" role="button"
              tabindex="0" @click="toggleAgentSection('sharedEditable')"
              @keydown.enter.prevent="toggleAgentSection('sharedEditable')"
              @keydown.space.prevent="toggleAgentSection('sharedEditable')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="edit-1" size="12px" class="agent-section-subicon" />
              <span>{{ $t('agent.sections.sharedEditable') }}</span>
              <span class="agent-section-count">{{ spaceAgentSectionCounts.sharedEditable }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('sharedEditable') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 仅查看：首次从可编辑 / is_mine 进入 viewer -->
            <div v-if="showShareGroupHeaders
              && !shared.is_mine
              && !isSharedAgentEditable(shared.permission)
              && (index === 0
                || sortedSpaceAgentsList[index - 1].is_mine
                || isSharedAgentEditable(sortedSpaceAgentsList[index - 1].permission))" class="agent-section-header"
              role="button" tabindex="0" @click="toggleAgentSection('sharedReadonly')"
              @keydown.enter.prevent="toggleAgentSection('sharedReadonly')"
              @keydown.space.prevent="toggleAgentSection('sharedReadonly')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="browse" size="12px" class="agent-section-subicon" />
              <span>{{ $t('agent.sections.sharedReadonly') }}</span>
              <span class="agent-section-count">{{ spaceAgentSectionCounts.sharedReadonly }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('sharedReadonly') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <div v-show="!isSpaceAgentCollapsed(shared)" class="agent-card shared-agent-card" :class="{
              'agent-mode-normal': shared.agent?.config?.agent_mode === 'quick-answer',
              'agent-mode-agent': shared.agent?.config?.agent_mode === 'smart-reasoning'
            }" @click="handleSpaceAgentCardClick(shared)">
              <div class="card-decoration">
                <svg class="star-icon" width="24" height="24" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"
                    fill="currentColor" fill-opacity="0.15" />
                </svg>
                <svg class="star-icon small" width="14" height="14" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    stroke="currentColor" stroke-width="0.8" stroke-linecap="round" stroke-linejoin="round"
                    fill="currentColor" fill-opacity="0.15" />
                </svg>
              </div>
              <div class="card-header">
                <div class="card-header-left">
                  <div v-if="shared.agent?.avatar" class="builtin-avatar agent-emoji">{{ shared.agent.avatar }}</div>
                  <AgentAvatar v-else :name="shared.agent?.name" size="small" />
                  <span class="card-title" :title="shared.agent?.name">{{ shared.agent?.name }}</span>
                </div>
                <t-popup v-if="!shared.is_mine && authStore.hasRole('admin')"
                  :visible="openMoreAgentId === 'shared-tab-' + shared.share_id" trigger="hover"
                  overlayClassName="card-more-popup" destroy-on-close placement="bottom-right"
                  @update:visible="(v: boolean) => { if (!v) openMoreAgentId = null }">
                  <div class="more-wrap" :class="{ 'active-more': openMoreAgentId === 'shared-tab-' + shared.share_id }"
                    @click.stop="toggleMore($event, 'shared-tab-' + shared.share_id)">
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
                    <t-tag v-if="shared.disabled_by_me" theme="default" size="small" class="disabled-badge">{{
                      $t('agent.disabled') }}</t-tag>
                    <t-tooltip
                      :content="shared.agent?.config?.agent_mode === 'smart-reasoning' ? $t('agent.mode.agent') : $t('agent.mode.normal')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'mode-normal': shared.agent?.config?.agent_mode === 'quick-answer', 'mode-agent': shared.agent?.config?.agent_mode === 'smart-reasoning' }">
                        <t-icon
                          :name="shared.agent?.config?.agent_mode === 'smart-reasoning' ? 'control-platform' : 'chat'"
                          size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="shared.agent?.config?.web_search_enabled" :content="$t('agent.features.webSearch')"
                      placement="top">
                      <div class="feature-badge web-search"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"
                          xmlns="http://www.w3.org/2000/svg">
                          <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none" />
                          <ellipse cx="8" cy="8" rx="2.5" ry="6" stroke="currentColor" stroke-width="1.2" fill="none" />
                          <line x1="2" y1="6" x2="14" y2="6" stroke="currentColor" stroke-width="1.2" />
                          <line x1="2" y1="10" x2="14" y2="10" stroke="currentColor" stroke-width="1.2" />
                        </svg></div>
                    </t-tooltip>
                    <t-tooltip
                      v-if="shared.agent?.config?.knowledge_bases?.length || shared.agent?.config?.kb_selection_mode === 'all'"
                      :content="$t('agent.features.knowledgeBase')" placement="top">
                      <div class="feature-badge knowledge"><t-icon name="folder" size="16px" /></div>
                    </t-tooltip>
                    <t-tooltip
                      v-if="shared.agent?.config?.mcp_services?.length || shared.agent?.config?.mcp_selection_mode === 'all'"
                      :content="$t('agent.features.mcp')" placement="top">
                      <div class="feature-badge mcp"><t-icon name="extension" size="16px" /></div>
                    </t-tooltip>
                    <t-tooltip v-if="shared.agent?.config?.multi_turn_enabled" :content="$t('agent.features.multiTurn')"
                      placement="top">
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
          </template>
        </div>

        <!-- 空状态：全部（保留创建 CTA） -->
        <div v-if="spaceSelection === 'all' && filteredAgents.length === 0 && !loading" class="empty-state">
          <img class="empty-img" src="@/assets/img/upload.svg" alt="">
          <span class="empty-txt">{{ $t('agent.empty.title') }}</span>
          <span class="empty-desc">{{ $t('agent.empty.description') }}</span>
          <t-button v-if="authStore.hasRole('contributor')" class="agent-create-btn empty-state-btn"
            data-guide="agent-list-create" @click="handleCreateAgent">
            <template #icon>
              <span class="btn-icon-wrapper">
                <svg class="sparkles-icon" width="18" height="18" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    fill="currentColor" stroke="currentColor" stroke-width="0.8" stroke-linecap="round"
                    stroke-linejoin="round" />
                  <path
                    d="M15.5 4L15.8 5.2C15.85 5.45 16.05 5.65 16.3 5.7L17.5 6L16.3 6.3C16.05 6.35 15.85 6.55 15.8 6.8L15.5 8L15.2 6.8C15.15 6.55 14.95 6.35 14.7 6.3L13.5 6L14.7 5.7C14.95 5.65 15.15 5.45 15.2 5.2L15.5 4Z"
                    fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round"
                    stroke-linejoin="round" />
                  <path
                    d="M4.5 13L4.8 14.2C4.85 14.45 5.05 14.65 5.3 14.7L6.5 15L5.3 15.3C5.05 15.35 4.85 15.55 4.8 15.8L4.5 17L4.2 15.8C4.15 15.55 3.95 15.35 3.7 15.3L2.5 15L3.7 14.7C3.95 14.65 4.15 14.45 4.2 14.2L4.5 13Z"
                    fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round"
                    stroke-linejoin="round" />
                </svg>
              </span>
            </template>
            <span>{{ $t('agent.createAgent') }}</span>
          </t-button>
        </div>

        <!-- 空状态：收藏 / 最近 — 不放创建按钮，参见 KnowledgeBaseList 的同处理由 -->
        <div v-if="spaceSelection === 'favorites' && filteredAgents.length === 0 && !loading" class="empty-state">
          <t-icon name="star" size="48px" class="empty-icon" />
          <span class="empty-txt">{{ $t('agent.empty.favoritesTitle') }}</span>
          <span class="empty-desc">{{ $t('agent.empty.favoritesDescription') }}</span>
        </div>
        <div v-if="spaceSelection === 'recents' && filteredAgents.length === 0 && !loading" class="empty-state">
          <t-icon name="history" size="48px" class="empty-icon" />
          <span class="empty-txt">{{ $t('agent.empty.recentsTitle') }}</span>
          <span class="empty-desc">{{ $t('agent.empty.recentsDescription') }}</span>
        </div>
        <!-- 空状态：我的 -->
        <div v-if="spaceSelection === 'mine' && agents.length === 0 && !loading" class="empty-state">
          <img class="empty-img" src="@/assets/img/upload.svg" alt="">
          <span class="empty-txt">{{ $t('agent.empty.title') }}</span>
          <span class="empty-desc">{{ $t('agent.empty.description') }}</span>
          <t-button v-if="authStore.hasRole('contributor')" class="agent-create-btn empty-state-btn"
            @click="handleCreateAgent">
            <template #icon>
              <span class="btn-icon-wrapper">
                <svg class="sparkles-icon" width="18" height="18" viewBox="0 0 20 20" fill="none"
                  xmlns="http://www.w3.org/2000/svg">
                  <path
                    d="M10 3L10.8 6.2C10.9 6.7 11.3 7.1 11.8 7.2L15 8L11.8 8.8C11.3 8.9 10.9 9.3 10.8 9.8L10 13L9.2 9.8C9.1 9.3 8.7 8.9 8.2 8.8L5 8L8.2 7.2C8.7 7.1 9.1 6.7 9.2 6.2L10 3Z"
                    fill="currentColor" stroke="currentColor" stroke-width="0.8" stroke-linecap="round"
                    stroke-linejoin="round" />
                  <path
                    d="M15.5 4L15.8 5.2C15.85 5.45 16.05 5.65 16.3 5.7L17.5 6L16.3 6.3C16.05 6.35 15.85 6.55 15.8 6.8L15.5 8L15.2 6.8C15.15 6.55 14.95 6.35 14.7 6.3L13.5 6L14.7 5.7C14.95 5.65 15.15 5.45 15.2 5.2L15.5 4Z"
                    fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round"
                    stroke-linejoin="round" />
                  <path
                    d="M4.5 13L4.8 14.2C4.85 14.45 5.05 14.65 5.3 14.7L6.5 15L5.3 15.3C5.05 15.35 4.85 15.55 4.8 15.8L4.5 17L4.2 15.8C4.15 15.55 3.95 15.35 3.7 15.3L2.5 15L3.7 14.7C3.95 14.65 4.15 14.45 4.2 14.2L4.5 13Z"
                    fill="currentColor" stroke="currentColor" stroke-width="0.6" stroke-linecap="round"
                    stroke-linejoin="round" />
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
    <t-dialog v-model:visible="deleteVisible" dialogClassName="del-agent-dialog" :closeBtn="false" :cancelBtn="null"
      :confirmBtn="null">
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
      <div v-if="sharedDetailVisible && currentSharedAgent" class="shared-detail-drawer-overlay"
        @click.self="closeSharedAgentDetail">
        <div class="shared-detail-drawer">
          <div class="shared-detail-drawer-header">
            <h3 class="shared-detail-drawer-title">{{ $t('agent.detail.title') }}</h3>
            <button type="button" class="shared-detail-drawer-close" @click="closeSharedAgentDetail"
              :aria-label="$t('general.close')">
              <t-icon name="close" />
            </button>
          </div>
          <div class="shared-detail-drawer-body">
            <div class="shared-detail-row">
              <span class="shared-detail-label">{{ $t('agent.editor.name') }}</span>
              <span class="shared-detail-value">{{ currentSharedAgent.agent?.name }}</span>
            </div>
            <div class="shared-detail-row">
              <span class="shared-detail-label">{{ $t('knowledgeList.detail.sourceOrg') }}</span>
              <span class="shared-detail-value shared-detail-org">
                <img src="@/assets/img/organization-green.svg" class="shared-detail-org-icon" alt=""
                  aria-hidden="true" />
                <span>{{ currentSharedAgent.org_name }}</span>
              </span>
            </div>
            <div class="shared-detail-row">
              <span class="shared-detail-label">{{ $t('knowledgeList.detail.myPermission') }}</span>
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
                <span class="shared-detail-value">{{ currentSharedAgent.agent.config.model_id ?
                  $t('agent.shareScope.modelConfigured') : $t('agent.shareScope.modelNotSet') }}</span>
              </div>
              <div v-if="sharedAgentUsesKb" class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.rerankModel') }}</span>
                <span class="shared-detail-value">{{ currentSharedAgent.agent.config.rerank_model_id ?
                  $t('agent.shareScope.modelConfigured') : $t('agent.shareScope.modelNotSet') }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.webSearch') }}</span>
                <span class="shared-detail-value">{{ currentSharedAgent.agent.config.web_search_enabled ?
                  $t('agent.shareScope.enabled') : $t('agent.shareScope.disabled') }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('agent.shareScope.mcp') }}</span>
                <span class="shared-detail-value">{{ sharedAgentMcpScopeText }}</span>
              </div>
            </template>
          </div>
          <div class="shared-detail-drawer-footer">
            <t-button theme="primary" block @click="handleUseSharedAgentInChat(currentSharedAgent)">
              {{ $t('agent.detail.useInChat') }}
            </t-button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 智能体编辑器弹窗 -->
    <AgentEditorModal :visible="editorVisible" :mode="editorMode" :agent="editingAgent"
      :initialSection="editorInitialSection"
      :readOnly="editorMode === 'edit' && editingAgent != null && !canManageAgent(editingAgent as AgentWithUI)"
      @update:visible="editorVisible = $event" @success="handleEditorSuccess" />

    <TenantModelsGuide :when="showAgentTenantModelsGuide" variant="agent" />
    <ContextualGuide tour="agentList" :when="showAgentListContextualGuide" />
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
import ContextualGuide from '@/components/ContextualGuide.vue'
import TenantModelsGuide from '@/components/TenantModelsGuide.vue'
import { markContextualGuideDone } from '@/config/contextualGuides'
import { useTenantModelReadiness } from '@/composables/useTenantModelReadiness'
import { useUIStore } from '@/stores/ui'
import AgentAvatar from '@/components/AgentAvatar.vue'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'
import ResourceOriginBadge from '@/components/ResourceOriginBadge.vue'
import { useAuthStore } from '@/stores/auth'
import { useListUrlState } from '@/composables/useListUrlState'
import { useResourcePins } from '@/composables/useResourcePins'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const uiStore = useUIStore()
const orgStore = useOrganizationStore()
const { loaded: modelsReadyLoaded, isReadyForAgent } = useTenantModelReadiness()

interface AgentWithUI extends CustomAgent {
  showMore?: boolean
  /** 当前租户在对话下拉中停用（仅影响本租户） */
  disabled_by_me?: boolean
}

/** Merged agent for "all" tab: my agents (isMine: true) or shared
 *  (isMine: false, org_name, source_tenant_id, share_id, permission, disabled_by_me?).
 *  `permission` drives the「可编辑 / 仅查看」分组，仅在 shared 分支携带。 */
type DisplayAgent = (AgentWithUI & { isMine: true }) | (CustomAgent & { isMine: false; org_name: string; source_tenant_id: number; share_id: string; permission?: string; showMore?: boolean; disabled_by_me?: boolean })

// 左侧空间选择：默认根据当前角色决定。
// 与 KnowledgeBaseList 同款逻辑：Viewer 在当前租户里通常没有自建智能体，
// 默认落到 "all" 才能看到内置 + 共享给我的；Contributor 以上仍默认 "mine"。
// State synced to `?scope=` so links are shareable. The "mine" value is
// retained for back-compat with existing links; its display label is
// rebranded to the active tenant name inside ListSpaceSidebar.
const defaultScope: 'all' | 'mine' = authStore.hasRole('contributor') ? 'mine' : 'all'
const { scope: spaceSelection, creator: creatorFilter } = useListUrlState({
  defaultScope,
  defaultCreator: 'all',
})

// Per-user favorites + recents (localStorage-backed). See useResourcePins.
const pins = useResourcePins()
const agentFavoritesCount = computed(
  () => pins.favorites.value.filter((e) => e.type === 'agent').length
)
const agentRecentsCount = computed(
  () => pins.recents.value.filter((e) => e.type === 'agent').length
)
const agents = ref<AgentWithUI[]>([])
const sharedAgents = computed<SharedAgentInfo[]>(() => orgStore.sharedAgents || [])
const allAgentsCount = computed(() => agents.value.length + sharedAgents.value.length)

// Same gotcha as KnowledgeBaseList: keep the reserved-scope set in sync
// with ListSpaceSidebar's pseudo-scopes (favorites / recents / shared /
// mine / all). Anything not in here is treated as an org/space id, which
// is what triggers the per-space fetch + "no shared agents" empty state.
const RESERVED_SCOPES = new Set(['all', 'mine', 'shared', 'favorites', 'recents'])
const spaceSelectionOrgId = computed(() => {
  const s = spaceSelection.value
  return !!s && !RESERVED_SCOPES.has(s)
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
    ; (orgStore.organizations || []).forEach(org => {
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

// Favorites / Recents view: hydrate pinned ids against own + shared agents.
const agentResourceIndex = computed(() => {
  const map = new Map<string, { agent: any; isMine: boolean; shared?: SharedAgentInfo }>()
  for (const a of agents.value) map.set(a.id, { agent: a, isMine: true })
  for (const s of sharedAgents.value) {
    if (s.agent && !map.has(s.agent.id)) map.set(s.agent.id, { agent: s.agent, isMine: false, shared: s })
  }
  return map
})

const favoritesAgentList = computed<DisplayAgent[]>(() => {
  return pins.favorites.value
    .filter((e) => e.type === 'agent')
    .map((e) => {
      const entry = agentResourceIndex.value.get(e.id)
      if (!entry) return null
      if (entry.isMine) return { ...entry.agent, isMine: true as const, showMore: false }
      const s = entry.shared!
      return {
        ...entry.agent,
        isMine: false as const,
        org_name: s.org_name,
        source_tenant_id: s.source_tenant_id,
        share_id: s.share_id,
        disabled_by_me: s.disabled_by_me,
        showMore: false,
      } as DisplayAgent
    })
    .filter((x): x is DisplayAgent => x !== null)
})

const recentsAgentList = computed<DisplayAgent[]>(() => {
  return pins.recents.value
    .filter((e) => e.type === 'agent')
    .map((e) => {
      const entry = agentResourceIndex.value.get(e.id)
      if (!entry) return null
      if (entry.isMine) return { ...entry.agent, isMine: true as const, showMore: false }
      const s = entry.shared!
      return {
        ...entry.agent,
        isMine: false as const,
        org_name: s.org_name,
        source_tenant_id: s.source_tenant_id,
        share_id: s.share_id,
        disabled_by_me: s.disabled_by_me,
        showMore: false,
      } as DisplayAgent
    })
    .filter((x): x is DisplayAgent => x !== null)
})

const filteredAgents = computed<DisplayAgent[]>(() => {
  if (spaceSelection.value === 'favorites') return favoritesAgentList.value
  if (spaceSelection.value === 'recents') return recentsAgentList.value
  if (spaceSelection.value === 'mine') {
    return agents.value.map(a => ({ ...a, isMine: true as const }))
  }
  if (spaceSelection.value !== 'all') return []
  const list: DisplayAgent[] = []
  // 本租户内的 agent 拆成 内置 → 我创建 → 同事创建 三段。
  // 内置（is_builtin=true）和"个人所有权"是两个维度的概念，置顶为单独
  // 一段；它们的 created_by 始终为空，跟在「同事/无创建者」桶里反而让
  // tenantOthers 段同时混入"系统内置 + 历史无 owner 的自定义"两类，
  // 语义不清。
  const builtin: AgentWithUI[] = []
  const ownMine: AgentWithUI[] = []
  const teammateMine: AgentWithUI[] = []
  agents.value.forEach(a => {
    if (a.is_builtin) builtin.push(a)
    else if (isMyAgent(a)) ownMine.push(a)
    else teammateMine.push(a)
  })
  builtin.forEach(a => list.push({ ...a, isMine: true as const }))
  ownMine.forEach(a => list.push({ ...a, isMine: true as const }))
  teammateMine.forEach(a => list.push({ ...a, isMine: true as const }))
  // 共享区按 share permission 排序：editor/admin 在前，viewer 在后，
  // 让「共享给我 · 可编辑 / 仅查看」分组标题正好落在过渡处。即便当前角色
  // 不显示分组标题，排序也保留——展示更可预测。
  const sortedShared = [...sharedAgents.value].sort((a, b) => {
    const aE = isSharedAgentEditable(a.permission) ? 0 : 1
    const bE = isSharedAgentEditable(b.permission) ? 0 : 1
    return aE - bE
  })
  sortedShared.forEach(shared => {
    if (!shared.agent) return
    list.push({
      ...shared.agent,
      isMine: false as const,
      org_name: shared.org_name,
      source_tenant_id: shared.source_tenant_id,
      share_id: shared.share_id,
      permission: shared.permission,
      disabled_by_me: shared.disabled_by_me,
      showMore: false
    } as DisplayAgent)
  })
  return list
})

// 「工作空间」视图下的稳定排序：本租户内「我创建」在前、「同事创建 / 内建」
// 在后。给 contributor 视图把「本空间 · 仅查看」分组标题正好插在过渡处。
const sortedMineAgents = computed(() => {
  // 内置 → 我创建 → 同事创建。与 filteredAgents 的"全部"视图保持同序。
  const builtin: AgentWithUI[] = []
  const own: AgentWithUI[] = []
  const teammate: AgentWithUI[] = []
  agents.value.forEach(a => {
    if (a.is_builtin) builtin.push(a)
    else if (isMyAgent(a)) own.push(a)
    else teammate.push(a)
  })
  return [...builtin, ...own, ...teammate]
})

// 空间视角下的稳定排序：我自己创建的（is_mine）放前面，其余按 permission 切分。
const sortedSpaceAgentsList = computed(() => {
  return [...spaceAgentsList.value].sort((a, b) => {
    const aMine = a.is_mine ? 0 : 1
    const bMine = b.is_mine ? 0 : 1
    if (aMine !== bMine) return aMine - bMine
    const aE = isSharedAgentEditable(a.permission) ? 0 : 1
    const bE = isSharedAgentEditable(b.permission) ? 0 : 1
    return aE - bE
  })
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

const showAgentListEmpty = computed(() => {
  if (loading.value) return false
  if (!authStore.hasRole('contributor')) return false
  if (spaceSelection.value === 'all' && filteredAgents.value.length === 0) return true
  if (spaceSelection.value === 'mine' && agents.value.length === 0) return true
  return false
})

const showAgentTenantModelsGuide = computed(
  () => modelsReadyLoaded.value && showAgentListEmpty.value && !isReadyForAgent.value,
)

const showAgentListContextualGuide = computed(
  () => showAgentListEmpty.value && isReadyForAgent.value && !editorVisible.value,
)

const fetchList = () => {
  loading.value = true
  return Promise.all([
    listAgents({ creator: creatorFilter.value }).then((res: any) => {
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
    // Drop the transient edit/section params but preserve other filter
    // state (scope / creator / q) so refreshing doesn't reset the view.
    const { edit: _e, section: _s, ...rest } = route.query
    router.replace({ path: route.path, query: rest })
  }
}

// Also re-run when the query mutates while this view is already mounted —
// e.g. the IM overview dialog navigating here via router.push lands on the
// same route, so onMounted alone never fires and the editor would only open
// after a manual refresh.
watch(
  () => route.query.edit,
  (v) => {
    if (v && agents.value.length > 0) {
      checkAndOpenEditModal()
    }
  },
)

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

// Refetch when the creator filter flips so the server applies the
// predicate uniformly (also keeps built-in agents always present, see
// the matching block in custom_agent.go).
watch(creatorFilter, () => {
  fetchList()
})

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
  // Track recency before any branch — Recents should reflect what the
  // user *looked at*, not only what they edited.
  pins.touchRecent('agent', agent.id)
  if ('isMine' in agent && !agent.isMine) {
    const shared = sharedAgents.value.find(s => s.agent?.id === agent.id && s.source_tenant_id === agent.source_tenant_id)
    if (shared) openSharedAgentDetail(shared)
    return
  }
  handleEdit(agent as AgentWithUI)
}

const toggleFavoriteAgent = (agentId: string, evt?: Event) => {
  evt?.stopPropagation()
  pins.toggleFavorite('agent', agentId)
}
const isAgentFavorited = (agentId: string) => pins.isFavorite('agent', agentId)

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
      MessagePlugin.error(t('createChat.messages.createFailed'))
    }
  } catch (e) {
    console.error('Create session for shared agent failed', e)
    MessagePlugin.error(t('createChat.messages.createError'))
  }
}

const handleEdit = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  editingAgent.value = agent
  editorMode.value = 'edit'
  editorVisible.value = true
}

// canManageAgent mirrors the server-side OwnedAgentOrAdmin guard
// (PR 5 #1303): the agent's creator may always edit / delete; otherwise
// Admin+ is required. Built-in agents have created_by="" → only Admin+
// matches, which lines up with the "Admin can mutate tenant-owned
// agents" rule. The server still enforces the same matrix on every
// mutation; this gate just hides buttons the user has no authority
// to use.
function canManageAgent(agent: AgentWithUI): boolean {
  const userId = authStore.user?.id || ''
  const creatorId = (agent as any).created_by || ''
  if (creatorId && userId && creatorId === userId) return true
  return authStore.hasRole('admin')
}

// isMyAgent 仅用于卡片来源徽章在「我创建」与「同租户其他成员创建」之间切换。
// 跟 canManageAgent 区别：管理权限有 admin 兜底；徽章纯粹按 created_by 匹配。
// 内建 agent（created_by=""）也归到非 mine 一档，由模板上的 builtin 分支
// 提前拦截，不会落到 ResourceOriginBadge。
function isMyAgent(agent: { created_by?: string }): boolean {
  const userId = authStore.user?.id || ''
  return !!(agent.created_by && userId && agent.created_by === userId)
}

// agentOriginVariant 跟 kbOriginVariant 对齐：右下角徽章不再重复租户名
// （顶部 TenantSelector 已经标了租户身份），所有角色都用 creator 变体。
// 内建 agent 走 v-else 前的 builtin 分支，到不了这里。
function agentOriginVariant(agent: { created_by?: string }): 'mine' | 'creator' {
  return isMyAgent(agent) ? 'mine' : 'creator'
}

// 共享 agent 的可编辑/只读分组开关，与 KB 列表逻辑保持一致：仅对
// contributor / editor 中间档展示分组标题。Viewer / Admin+ 不分组。
const AGENT_EDITABLE_PERMS = new Set(['admin', 'editor'])
function isSharedAgentEditable(perm: string | undefined): boolean {
  return !!perm && AGENT_EDITABLE_PERMS.has(perm)
}
// 与 KnowledgeBaseList 同理：分组标题对所有角色生效，依据"创建者 + 来源"
// 这种客观信息分段，不再按当前用户的可写权限筛掉。
const showShareGroupHeaders = computed(() => true)

// 同租户、非当前用户创建的 Agent 分组标题。
// contributor / viewer 在本租户里对这些 Agent 没有写权限，所以打"仅查看"；
// admin / owner 对整个租户都有编辑权限，"仅查看"反而误导，统一改成
// "本空间 · 其他成员"——按所有权而非权限来标注。
const tenantSectionLabelKey = computed(() =>
  authStore.hasRole('admin')
    ? 'agent.sections.tenantOthers'
    : 'agent.sections.tenantReadonly'
)

// 与 KB 列表 .tenantSectionIconName 同理：admin/owner 看到"其他成员"配
// usergroup（多人）；contributor/viewer 看到"仅查看"配 browse（眼睛）。
const tenantSectionIconName = computed(() =>
  authStore.hasRole('admin') ? 'usergroup' : 'browse'
)

// 分组折叠：ephemeral，只在当前会话生效。和 KnowledgeBaseList 共用同一套
// 思路——空 Set = 全展开，避免新增分段还得维护默认值。
type AgentSectionKey = 'builtin' | 'mine' | 'tenantOthers' | 'sharedByMe' | 'sharedEditable' | 'sharedReadonly'
const collapsedAgentSections = ref<Set<AgentSectionKey>>(new Set())
const isAgentSectionCollapsed = (key: AgentSectionKey) => collapsedAgentSections.value.has(key)
const toggleAgentSection = (key: AgentSectionKey) => {
  const next = new Set(collapsedAgentSections.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  collapsedAgentSections.value = next
}
// 根据 agent 数据形态判分组：filteredAgents 元素带 isMine；sortedMineAgents
// 是原始 agent（永远当作本租户）；sortedSpaceAgentsList 用 is_mine。
//
// 当前用户自己创建的 agent 在模板里**没有**独立分组标题（不像 KB 那边有
// "我创建的"段），所以这里返回 null——折叠任何分组都不会影响到它们。
const agentSectionOf = (item: any): AgentSectionKey | null => {
  // 内置 agent（is_builtin=true）单独成段，置顶展示——它们是租户共有的
  // 系统资源，跟"我 / 同事 / 共享"几个所有权分类不在同一维度。判定要早于
  // shared 那一档，因为 filteredAgents 里的 shared 条目也可能携带 is_builtin
  // （理论上不会，但保守一些）。
  if (item?.is_builtin === true) return 'builtin'
  // 跨租户 shared 条目（filteredAgents 拆出来的 isMine=false / 空间视图的
  // sortedSpaceAgentsList 用 is_mine=false）一律按 permission 分到
  // sharedEditable / sharedReadonly。
  if (item?.isMine === false || item?.is_mine === false) {
    return isSharedAgentEditable(item?.permission) ? 'sharedEditable' : 'sharedReadonly'
  }
  // 本租户内：我亲手创建 → 'mine'；同事 / 非内置但无 created_by → 'tenantOthers'。
  return isMyAgent(item as AgentWithUI) ? 'mine' : 'tenantOthers'
}
const isAgentRowHidden = (item: any): boolean => {
  const key = agentSectionOf(item)
  return key !== null && isAgentSectionCollapsed(key)
}

// 空间筛选视图（sortedSpaceAgentsList）的条目结构和 filteredAgents 不同：
// is_mine=true 表示「我共享给这个空间」，需要独立成段（避免和首页的"我创建的"
// 共用一个折叠状态）。is_mine=false 仍按 permission 走 sharedEditable / Readonly。
const spaceAgentSectionOf = (shared: any): AgentSectionKey => {
  if (shared?.is_mine) return 'sharedByMe'
  return isSharedAgentEditable(shared?.permission) ? 'sharedEditable' : 'sharedReadonly'
}
const isSpaceAgentCollapsed = (shared: any): boolean => isAgentSectionCollapsed(spaceAgentSectionOf(shared))

// 各分组卡片数量——和 KB 列表同思路，组标题上展示"(N)"，方便折叠后核对。
const emptyAgentCounts = (): Record<AgentSectionKey, number> => ({
  builtin: 0, mine: 0, tenantOthers: 0, sharedByMe: 0, sharedEditable: 0, sharedReadonly: 0,
})
const filteredAgentSectionCounts = computed<Record<AgentSectionKey, number>>(() => {
  const c = emptyAgentCounts()
  filteredAgents.value.forEach(a => {
    const key = agentSectionOf(a)
    if (key) c[key]++
  })
  return c
})
const mineAgentSectionCounts = computed<Record<AgentSectionKey, number>>(() => {
  const c = emptyAgentCounts()
  sortedMineAgents.value.forEach(a => {
    const key = agentSectionOf(a)
    if (key) c[key]++
  })
  return c
})
const spaceAgentSectionCounts = computed<Record<AgentSectionKey, number>>(() => {
  const c = emptyAgentCounts()
  sortedSpaceAgentsList.value.forEach(shared => { c[spaceAgentSectionOf(shared)]++ })
  return c
})

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
  if (!isReadyForAgent.value) {
    MessagePlugin.warning(t('contextualGuide.tenantModels.needChatModelFirst'))
    uiStore.openSettings('models')
    return
  }
  markContextualGuideDone('agentList')
  openCreateModal()
}

defineExpose({
  openCreateModal
})
</script>

<style scoped lang="less">
.agent-list-container {
  margin: 0 16px 0 0;
  height: 100%;
  box-sizing: border-box;
  flex: 1;
  display: flex;
  position: relative;
  min-height: 0;
}

.agent-list-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 20px 28px 0 28px;
}

.agent-list-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  // 同 KB 列表：顶部去掉 padding，让 sticky 分组标题贴到容器最顶。
  padding: 0 0 8px;
}

.agent-list-main-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  padding: 12px;
  background: var(--td-bg-color-container);
}

.shared-by-me-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 6px;
  background: rgba(7, 192, 95, 0.1);
  border-radius: 4px;
  font-size: 12px;
  color: var(--td-brand-color);
  margin-left: 6px;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  h2 {
    margin: 0;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }
}

:deep(.agent-create-btn) {
  --ripple-color: rgba(118, 75, 162, 0.3) !important;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%) !important;
  border: none !important;
  color: var(--td-text-color-anti) !important;
  position: relative;
  overflow: hidden;

  &:hover,
  &:active,
  &:focus,
  &.t-is-active,
  &[data-state="active"] {
    background: linear-gradient(135deg, #5a6fd6 0%, #6a4190 100%) !important;
    border: none !important;
    color: var(--td-text-color-anti) !important;
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
    background: linear-gradient(45deg,
        transparent 30%,
        rgba(255, 255, 255, 0.1) 50%,
        transparent 70%);
    transform: translateX(-100%);
    transition: transform 0.6s ease;
    z-index: 0;
  }

  &:hover::before {
    transform: translateX(100%);
  }
}

@keyframes twinkle {

  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }

  50% {
    opacity: 0.8;
    transform: scale(0.95);
  }
}

.header-subtitle {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

.header-action-btn {
  padding: 0 !important;
  min-width: 28px !important;
  width: 28px !important;
  height: 28px !important;
  display: inline-flex !important;
  align-items: center !important;
  justify-content: center !important;
  background: var(--td-bg-color-secondarycontainer) !important;
  border: 1px solid var(--td-component-stroke) !important;
  border-radius: 6px !important;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background 0.2s, border-color 0.2s, color 0.2s;

  &:hover {
    background: var(--td-bg-color-secondarycontainer) !important;
    border-color: var(--td-component-stroke) !important;
    color: var(--td-text-color-primary);
  }

  :deep(.t-button__icon) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }

  :deep(.t-icon),
  :deep(.btn-icon-wrapper) {
    color: var(--td-brand-color);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }
}

.agent-tabs {
  display: flex;
  align-items: center;
  gap: 24px;
  border-bottom: 1px solid var(--td-component-stroke);
  margin-bottom: 20px;

  .tab-item {
    padding: 12px 0;
    cursor: pointer;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 400;
    transition: color 0.2s;

    &:hover {
      color: var(--td-text-color-primary);
    }

    &.active {
      color: var(--td-brand-color);
      font-weight: 600;
      border-bottom: 2px solid var(--td-brand-color);
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
  background: var(--td-bg-color-container-hover);
  flex-shrink: 0;
}

.card-bottom-source .org-icon {
  width: 12px;
  height: 12px;
  flex-shrink: 0;
}

.org-source-text {
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
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
  background: var(--td-bg-color-container-hover);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 11px;
  font-weight: 500;
  flex-shrink: 0;
}

// 共享给我 · 可编辑 / 仅查看 分组标题，与 KB 列表 .kb-section-header 对齐。
.agent-section-header {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  gap: 6px;
  // 整行只用来铺背景；点击靠子元素冒泡，避免点到标题右侧空白误折叠。
  pointer-events: none;

  & > * {
    pointer-events: auto;
  }
  // 同 KB 列表：下滑到当前分组时标题吸顶到滚动容器顶部，box-shadow 向上/
  // 向下延伸背景以封掉 sticky 边缘的 subpixel 残缝。
  position: sticky;
  top: 0;
  z-index: 5;
  background: var(--td-bg-color-container);
  box-shadow: 0 -8px 0 0 var(--td-bg-color-container),
    0 4px 0 0 var(--td-bg-color-container);
  padding: 6px 4px 6px 0;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 13px;
  font-weight: 600;
  line-height: 20px;
  cursor: pointer;
  user-select: none;
  outline: none;

  &:hover {
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    box-shadow: 0 0 0 2px var(--td-brand-color-focus, rgba(0, 82, 217, 0.2));
  }

  .t-icon {
    color: inherit;
  }

  .agent-section-toggle {
    margin-left: 4px;
    opacity: 0.7;
    transition: opacity 0.15s ease;
  }

  // 共享给我的两个子分组：主图标 usergroup-add 表达"共享"语义，
  // 子图标 (edit / browse) 紧挨主图标用来区分权限。
  .agent-section-subicon {
    margin-left: -4px;
    opacity: 0.75;
  }

  // 与 KB 列表口径一致：组里的卡片数量徽标。
  .agent-section-count {
    margin-left: 2px;
    padding: 0 6px;
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: 11px;
    line-height: 16px;
    font-weight: 500;
  }

  &:hover .agent-section-toggle {
    opacity: 1;
  }
}


@keyframes contentFadeIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.agent-card-wrap {
  display: grid;
  gap: 12px;
  grid-template-columns: 1fr;
  animation: contentFadeIn 0.32s ease-out;
}

.agent-card-skeleton {
  cursor: default;

  .card-header {
    margin-bottom: 12px;
  }

  .card-content {
    flex: 1;
  }

  .card-bottom {
    margin-top: auto;
  }
}

/* 与知识库列表卡片统一尺寸：紧凑行高、148px 卡片高 */
.agent-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: all 0.25s ease;
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  height: 136px;
  min-height: 136px;

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 4px 12px rgba(7, 192, 95, 0.12);
  }

  .agent-favorite-star {
    // 浮在卡片右上角顶角。卡片自身有 padding，"更多"按钮在 header flex
    // 末端自然落在 padding 内部，与零位的 star 错开。
    position: absolute;
    top: 0;
    right: 0;
    z-index: 3;
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--td-text-color-secondary);
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s ease, background 0.15s ease, color 0.15s ease;

    &:hover {
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-warning-color, #e37318);
    }

    &.is-favorited {
      opacity: 1;
      color: var(--td-warning-color, #e37318);
    }
  }

  &:hover .agent-favorite-star {
    opacity: 1;
  }

  // 普通模式样式
  &.agent-mode-normal {
    background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(7, 192, 95, 0.04) 100%);

    &:hover {
      border-color: var(--td-brand-color);
      background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(7, 192, 95, 0.08) 100%);
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
    background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(124, 77, 255, 0.04) 100%);

    &:hover {
      border-color: var(--td-brand-color);
      box-shadow: 0 4px 12px rgba(124, 77, 255, 0.12);
      background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(124, 77, 255, 0.08) 100%);
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
    margin-bottom: 6px;
  }

  .card-title {
    font-size: 15px;
    line-height: 22px;
  }

  .card-content {
    margin-bottom: 6px;
  }

  .card-description {
    font-size: 12px;
    line-height: 17px;
  }

  .card-bottom {
    padding-top: 6px;
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
  gap: 4px;
  margin-bottom: 6px;
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.card-title {
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
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
  background: var(--td-bg-color-container-hover);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
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
  color: var(--td-text-color-disabled);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-brand-color);
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
    background: var(--td-bg-color-container-hover);
    opacity: 1 !important;
  }

  &.active-more {
    background: var(--td-bg-color-container-hover);
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
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
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
  border-top: .5px solid var(--td-component-stroke);
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
    color: var(--td-brand-color-active);

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }
  }

  &.mode-agent {
    background: rgba(124, 77, 255, 0.08);
    color: var(--td-brand-color);

    &:hover {
      background: rgba(124, 77, 255, 0.12);
    }
  }

  &.web-search {
    background: rgba(255, 152, 0, 0.08);
    color: var(--td-warning-color);

    &:hover {
      background: rgba(255, 152, 0, 0.12);
    }
  }

  &.knowledge {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-brand-color-active);

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }
  }

  &.mcp {
    background: rgba(236, 72, 153, 0.08);
    color: var(--td-error-color);

    &:hover {
      background: rgba(236, 72, 153, 0.12);
    }
  }

  &.multi-turn {
    background: rgba(59, 130, 246, 0.08);
    color: var(--td-brand-color);

    &:hover {
      background: rgba(59, 130, 246, 0.12);
    }
  }
}

.card-time {
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
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
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 16px;
    font-weight: 600;
    line-height: 26px;
    margin-bottom: 8px;
  }

  .empty-desc {
    color: var(--td-text-color-disabled);
    font-family: var(--app-font-family);
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

@media (min-width: 1900px) {
  .agent-card-wrap {
    grid-template-columns: repeat(5, 1fr);
  }
}

@media (min-width: 2200px) {
  .agent-card-wrap {
    grid-template-columns: repeat(6, 1fr);
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
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
  }

  .del-circle-txt {
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
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
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    cursor: pointer;

    &:hover {
      opacity: 0.8;
    }
  }

  .confirm {
    color: var(--td-error-color);
    margin-left: 40px;

    &:hover {
      opacity: 0.8;
    }
  }
}
</style>

<style lang="less">
/* 下拉菜单样式已统一至 @/assets/dropdown-menu.less */

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
  background: var(--td-bg-color-container);
  box-shadow: -4px 0 24px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  font-family: var(--app-font-family);
}

.shared-detail-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 24px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.shared-detail-drawer-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.shared-detail-drawer-close {
  width: 32px;
  height: 32px;
  border: none;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.2s ease, color 0.2s ease;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
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
  color: var(--td-text-color-primary);
  margin: 20px 0 12px 0;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.shared-detail-drawer-body .shared-detail-label {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.4;
}

.shared-detail-drawer-body .shared-detail-value {
  font-size: 14px;
  color: var(--td-text-color-primary);
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
  border-top: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
  background: var(--td-bg-color-container);
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
