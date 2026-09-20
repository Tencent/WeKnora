<template>
  <div class="agent-list-container">
    <div class="agent-list-content">
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <div class="title-row" style="--wails-draggable: drag">
            <h2 style="--wails-draggable: drag">
              <ResourceIcon type="agent" :size="24" />
              {{ $t('agent.title') }}
            </h2>
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
                {{ $t('agent.createAgent') }}
              </t-button>
            </t-tooltip>
          </div>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('agent.subtitle') }}</p>
        </div>
      </div>
      <ResourceListToolbar :hide-scopes="authStore.isLiteMode" v-model="spaceSelection" v-model:query="keyword" :count-all="allAgentsCount"
      :count-mine="agents.length" :count-by-org="effectiveSharedCountByOrg" :count-favorites="agentFavoritesCount"
      :count-recents="agentRecentsCount" />
      <div class="agent-list-main">
        <EmptyState v-if="keyword.trim() && !(loading || spaceAgentsLoading) && visibleResultCount === 0" icon="search"
          :title="$t('common.noResult')">
          <t-button variant="outline" @click="keyword = ''">{{ $t('common.clear') }}</t-button>
        </EmptyState>
        <!-- creator filter removed; see KnowledgeBaseList for rationale.
             Card-level creator display + URL-state field are retained. -->

        <!-- 骨架屏占位 -->
        <div v-if="loading && agents.length === 0" class="agent-card-wrap">
          <div v-for="n in 6" :key="'skel-' + n" class="agent-card agent-card-skeleton is-skeleton">
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
            <div v-if="agent.isMine
              && agent.is_builtin
              && (index === 0
                || !filteredAgents[index - 1].isMine
                || !(filteredAgents[index - 1] as AgentWithUI).is_builtin)" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('builtin')" @click="toggleAgentSection('builtin')"
              @keydown.enter.prevent="toggleAgentSection('builtin')"
              @keydown.space.prevent="toggleAgentSection('builtin')">
              <t-icon name="app" size="14px" />
              <span>{{ $t('agent.sections.builtin') }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.builtin }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('builtin') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 我创建的：当前 agent 是本空间 + 非内置 + 我亲手创建，且前一张
                 要么不存在、要么不是本空间、要么是内置（builtin → mine 过渡）、
                 要么是同事创建。与 KB 列表对齐。 -->
            <div v-if="agent.isMine
              && !agent.is_builtin
              && isMyAgent(agent)
              && (index === 0
                || !filteredAgents[index - 1].isMine
                || (filteredAgents[index - 1] as AgentWithUI).is_builtin
                || !isMyAgent(filteredAgents[index - 1] as AgentWithUI))" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('mine')" @click="toggleAgentSection('mine')"
              @keydown.enter.prevent="toggleAgentSection('mine')"
              @keydown.space.prevent="toggleAgentSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('agent.sections.mine') }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.mine }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 本空间 · 仅查看 / 其他成员：本空间里非内置且非我创建的同事 agent。 -->
            <div v-if="agent.isMine
              && !agent.is_builtin
              && !isMyAgent(agent)
              && (index === 0
                || !filteredAgents[index - 1].isMine
                || (filteredAgents[index - 1] as AgentWithUI).is_builtin
                || isMyAgent(filteredAgents[index - 1] as AgentWithUI))" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('tenantOthers')" @click="toggleAgentSection('tenantOthers')"
              @keydown.enter.prevent="toggleAgentSection('tenantOthers')"
              @keydown.space.prevent="toggleAgentSection('tenantOthers')">
              <t-icon :name="tenantSectionIconName" size="14px" />
              <span>{{ $t(tenantSectionLabelKey) }}</span>
              <span class="agent-section-count">{{ filteredAgentSectionCounts.tenantOthers }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('tenantOthers') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：仅在「全部」视图过渡处显示分组标题 -->
            <div v-if="!agent.isMine
              && isSharedAgentEditable((agent as any).permission)
              && (index === 0 || filteredAgents[index - 1].isMine)" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('sharedEditable')" @click="toggleAgentSection('sharedEditable')"
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
            <div v-if="!agent.isMine
              && !isSharedAgentEditable((agent as any).permission)
              && (index === 0
                || filteredAgents[index - 1].isMine
                || isSharedAgentEditable((filteredAgents[index - 1] as any).permission))" class="agent-section-header"
              role="button" tabindex="0" :aria-expanded="!isAgentSectionCollapsed('sharedReadonly')" @click="toggleAgentSection('sharedReadonly')"
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
            }" role="link" tabindex="0" @keydown.enter.self.prevent="handleCardClick(agent)" @keydown.space.self.prevent="handleCardClick(agent)" @click="handleCardClick(agent)">
              <!-- 行尾收藏操作，与更多菜单分开。 -->
              <button type="button" class="agent-favorite-star"
                :class="{ 'is-favorited': isAgentFavorited(agent.id) }"
                :aria-label="$t('listSpaceSidebar.favorites')" :aria-pressed="isAgentFavorited(agent.id)" @click.stop="toggleFavoriteAgent(agent.id, $event)">
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
                  :visible="openMoreAgentId === agent.id" trigger="click" overlayClassName="card-more-popup"
                  destroy-on-close placement="bottom-right"
                  @update:visible="(v: boolean) => { openMoreAgentId = v ? agent.id : null }">
                  <button type="button" :aria-label="$t('common.expand')" class="more-wrap" :class="{ 'active-more': openMoreAgentId === agent.id }"
                    @click.stop>
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </button>
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
                  :visible="openMoreAgentId === 'shared-' + agent.share_id" trigger="click"
                  overlayClassName="card-more-popup" destroy-on-close placement="bottom-right"
                  @update:visible="(v: boolean) => { openMoreAgentId = v ? 'shared-' + agent.share_id : null }">
                  <button type="button" :aria-label="$t('common.expand')" class="more-wrap" :class="{ 'active-more': openMoreAgentId === 'shared-' + agent.share_id }"
                    @click.stop>
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </button>
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
                <div class="card-description" :title="agent.description || $t('agent.noDescription')">
                  {{ agent.description || $t('agent.noDescription') }}</div>
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
                <div v-else-if="showAgentBuiltinBadge(agent)" class="builtin-badge">
                  <t-icon name="lock-on" size="12px" />
                  <span>{{ $t('agent.builtin') }}</span>
                </div>
                <ResourceOriginBadge v-else-if="showAgentOriginBadge(agent)" :variant="agentOriginVariant(agent)"
                  :creator-name="(agent as any).creator_name" />
              </div>
            </div>
          </template>
        </div>

        <!-- 我的智能体 -->
        <div v-if="spaceSelection === 'mine' && sortedMineAgents.length > 0" class="agent-card-wrap">
          <template v-for="(agent, index) in sortedMineAgents" :key="agent.id">
            <!-- 内置：始终置顶。sortedMineAgents 已按 内置→我→同事 排序。 -->
            <div v-if="agent.is_builtin
              && (index === 0 || !sortedMineAgents[index - 1].is_builtin)" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('builtin')" @click="toggleAgentSection('builtin')"
              @keydown.enter.prevent="toggleAgentSection('builtin')"
              @keydown.space.prevent="toggleAgentSection('builtin')">
              <t-icon name="app" size="14px" />
              <span>{{ $t('agent.sections.builtin') }}</span>
              <span class="agent-section-count">{{ mineAgentSectionCounts.builtin }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('builtin') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 我创建的：第一张非内置且我亲手创建的卡片前打标题 -->
            <div v-if="!agent.is_builtin
              && isMyAgent(agent)
              && (index === 0
                || sortedMineAgents[index - 1].is_builtin
                || !isMyAgent(sortedMineAgents[index - 1]))" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('mine')" @click="toggleAgentSection('mine')"
              @keydown.enter.prevent="toggleAgentSection('mine')"
              @keydown.space.prevent="toggleAgentSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('agent.sections.mine') }}</span>
              <span class="agent-section-count">{{ mineAgentSectionCounts.mine }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 本空间 · 仅查看 / 其他成员：非内置且非我创建的同事 agent -->
            <div v-if="!agent.is_builtin
              && !isMyAgent(agent)
              && (index === 0
                || sortedMineAgents[index - 1].is_builtin
                || isMyAgent(sortedMineAgents[index - 1]))" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('tenantOthers')" @click="toggleAgentSection('tenantOthers')"
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
            }" role="link" tabindex="0" @keydown.enter.self.prevent="handleCardClick(agent)" @keydown.space.self.prevent="handleCardClick(agent)" @click="handleCardClick(agent)">

              <button type="button" class="agent-favorite-star"
                :class="{ 'is-favorited': isAgentFavorited(agent.id) }"
                :aria-label="$t('listSpaceSidebar.favorites')" :aria-pressed="isAgentFavorited(agent.id)" @click.stop="toggleFavoriteAgent(agent.id, $event)">
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
                  :visible="openMoreAgentId === agent.id" trigger="click" overlayClassName="card-more-popup"
                  destroy-on-close placement="bottom-right"
                  @update:visible="(v: boolean) => { openMoreAgentId = v ? agent.id : null }">
                  <button type="button" :aria-label="$t('common.expand')" class="more-wrap" :class="{ 'active-more': openMoreAgentId === agent.id }"
                    @click.stop>
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </button>
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
                <div class="card-description" :title="agent.description || $t('agent.noDescription')">
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
                <!-- 右下角：内置 / 来源徽章（我创建 / 同空间其他成员） -->
                <div v-if="showAgentBuiltinBadge(agent)" class="builtin-badge">
                  <t-icon name="lock-on" size="12px" />
                  <span>{{ $t('agent.builtin') }}</span>
                </div>
                <ResourceOriginBadge v-else-if="showAgentOriginBadge(agent)" :variant="agentOriginVariant(agent)"
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
            <div v-if="shared.is_mine && index === 0" class="agent-section-header"
              role="button" tabindex="0" :aria-expanded="!isAgentSectionCollapsed('sharedByMe')" @click="toggleAgentSection('sharedByMe')"
              @keydown.enter.prevent="toggleAgentSection('sharedByMe')"
              @keydown.space.prevent="toggleAgentSection('sharedByMe')">
              <t-icon name="share" size="14px" />
              <span>{{ $t('agent.sections.sharedByMe') }}</span>
              <span class="agent-section-count">{{ spaceAgentSectionCounts.sharedByMe }}</span>
              <t-icon class="agent-section-toggle"
                :name="isAgentSectionCollapsed('sharedByMe') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：首次从 is_mine 进入共享 + editable -->
            <div v-if="!shared.is_mine
              && isSharedAgentEditable(shared.permission)
              && (index === 0 || sortedSpaceAgentsList[index - 1].is_mine)" class="agent-section-header" role="button"
              tabindex="0" :aria-expanded="!isAgentSectionCollapsed('sharedEditable')" @click="toggleAgentSection('sharedEditable')"
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
            <div v-if="!shared.is_mine
              && !isSharedAgentEditable(shared.permission)
              && (index === 0
                || sortedSpaceAgentsList[index - 1].is_mine
                || isSharedAgentEditable(sortedSpaceAgentsList[index - 1].permission))" class="agent-section-header"
              role="button" tabindex="0" :aria-expanded="!isAgentSectionCollapsed('sharedReadonly')" @click="toggleAgentSection('sharedReadonly')"
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
            }" role="link" tabindex="0" @keydown.enter.self.prevent="handleSpaceAgentCardClick(shared)" @keydown.space.self.prevent="handleSpaceAgentCardClick(shared)" @click="handleSpaceAgentCardClick(shared)">
              <div class="card-header">
                <div class="card-header-left">
                  <div v-if="shared.agent?.avatar" class="builtin-avatar agent-emoji">{{ shared.agent.avatar }}</div>
                  <AgentAvatar v-else :name="shared.agent?.name" size="small" />
                  <span class="card-title" :title="shared.agent?.name">{{ shared.agent?.name }}</span>
                </div>
                <t-popup v-if="!shared.is_mine && authStore.hasRole('admin')"
                  :visible="openMoreAgentId === 'shared-tab-' + shared.share_id" trigger="click"
                  overlayClassName="card-more-popup" destroy-on-close placement="bottom-right"
                  @update:visible="(v: boolean) => { openMoreAgentId = v ? 'shared-tab-' + shared.share_id : null }">
                  <button type="button" :aria-label="$t('common.expand')" class="more-wrap" :class="{ 'active-more': openMoreAgentId === 'shared-tab-' + shared.share_id }"
                    @click.stop>
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </button>
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
                <div class="card-description" :title="shared.agent?.description || $t('agent.noDescription')">
                  {{ shared.agent?.description || $t('agent.noDescription') }}</div>
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
              </div>
            </div>
          </template>
        </div>

        <!-- 空状态：全部（保留创建 CTA） -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'all' && filteredAgents.length === 0 && !loading" :title="$t('agent.empty.title')"
          :description="$t('agent.empty.description')">
          <template #icon><ResourceIcon type="agent" :size="32" /></template>
          <t-button v-if="authStore.hasRole('contributor')" theme="primary" class="agent-create-btn"
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
        </EmptyState>

        <!-- 空状态：收藏 / 最近 — 不放创建按钮，参见 KnowledgeBaseList 的同处理由 -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'favorites' && filteredAgents.length === 0 && !loading" icon="star" :title="$t('agent.empty.favoritesTitle')"
          :description="$t('agent.empty.favoritesDescription')" />
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'recents' && filteredAgents.length === 0 && !loading" icon="history" :title="$t('agent.empty.recentsTitle')"
          :description="$t('agent.empty.recentsDescription')" />
        <!-- 空状态：我的 -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'mine' && agents.length === 0 && !loading" :title="$t('agent.empty.title')"
          :description="$t('agent.empty.description')">
          <template #icon><ResourceIcon type="agent" :size="32" /></template>
          <t-button v-if="authStore.hasRole('contributor')" theme="primary" class="agent-create-btn"
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
        </EmptyState>
        <!-- 空状态：空间下 -->
        <EmptyState v-if="!keyword.trim() && spaceSelectionOrgId && !spaceAgentsLoading && spaceAgentsList.length === 0" :title="$t('agent.empty.sharedTitle')"
          :description="$t('agent.empty.sharedDescription')">
          <template #icon><ResourceIcon type="agent" :size="32" /></template>
        </EmptyState>
      </div>
    </div>

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
      :initialHighlightField="editorInitialHighlightField"
      :readOnly="editorMode === 'edit' && editingAgent != null && !canManageAgent(editingAgent as AgentWithUI)"
      @update:visible="editorVisible = $event" @success="handleEditorSuccess" />

    <TenantModelsGuide :when="showAgentTenantModelsGuide" variant="agent" />
    <ContextualGuide tour="agentList" :when="showAgentListContextualGuide" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin, Icon as TIcon } from 'tdesign-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { deleteAgent, copyAgent, type CustomAgent } from '@/api/agent'
import { useChatResourcesStore } from '@/stores/chatResources'
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
import { focusAgentEditorSection, markContextualGuideDone } from '@/config/contextualGuides'
import { useTenantModelReadiness } from '@/composables/useTenantModelReadiness'
import { useUIStore } from '@/stores/ui'
import AgentAvatar from '@/components/AgentAvatar.vue'
import ResourceListToolbar from '@/components/ResourceListToolbar.vue'
import { matchesResourceQuery } from '@/utils/resourceListSearch'
import ResourceOriginBadge from '@/components/ResourceOriginBadge.vue'
import { shouldShowResourceOriginBadge } from '@/utils/card-list-badge'
import { useAuthStore } from '@/stores/auth'
import { useListUrlState } from '@/composables/useListUrlState'
import { useResourcePins } from '@/composables/useResourcePins'
import { integrationSectionKey } from '@/config/settingsRoute'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const uiStore = useUIStore()
const orgStore = useOrganizationStore()
const chatResources = useChatResourcesStore()
const { loaded: modelsReadyLoaded, isReadyForAgent } = useTenantModelReadiness()

interface AgentWithUI extends CustomAgent {
  showMore?: boolean
  /** 当前空间在对话下拉中停用（仅影响本空间） */
  disabled_by_me?: boolean
}

/** Merged agent for "all" tab: my agents (isMine: true) or shared
 *  (isMine: false, org_name, source_tenant_id, share_id, permission, disabled_by_me?).
 *  `permission` drives the「可编辑 / 仅查看」分组，仅在 shared 分支携带。 */
type DisplayAgent = (AgentWithUI & { isMine: true }) | (CustomAgent & { isMine: false; org_name: string; source_tenant_id: number; share_id: string; permission?: string; showMore?: boolean; disabled_by_me?: boolean })

// 左侧空间选择：默认根据当前角色决定。
// 与 KnowledgeBaseList 同款逻辑：Viewer 在当前空间里通常没有自建智能体，
// 默认落到 "all" 才能看到内置 + 共享给我的；Contributor 以上仍默认 "mine"。
// State synced to `?scope=` so links are shareable. The "mine" value is
// retained for back-compat with existing links; its display label is
// shown as the current workspace in ResourceListToolbar.
const defaultScope: 'all' | 'mine' = authStore.hasRole('contributor') ? 'mine' : 'all'
const { scope: spaceSelection, creator: creatorFilter, query: keyword } = useListUrlState({
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
// with ResourceListToolbar's pseudo-scopes (favorites / recents / shared /
// mine / all). Anything not in here is treated as an org/space id, which
// is what triggers the per-space fetch + "no shared agents" empty state.
const RESERVED_SCOPES = new Set(['all', 'mine', 'shared', 'favorites', 'recents'])
const spaceSelectionOrgId = computed(() => {
  const s = spaceSelection.value
  return !!s && !RESERVED_SCOPES.has(s)
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

const unsearchedFilteredAgents = computed<DisplayAgent[]>(() => {
  if (spaceSelection.value === 'favorites') return favoritesAgentList.value
  if (spaceSelection.value === 'recents') return recentsAgentList.value
  if (spaceSelection.value === 'mine') {
    return agents.value.map(a => ({ ...a, isMine: true as const }))
  }
  if (spaceSelection.value !== 'all') return []
  const list: DisplayAgent[] = []
  // 本空间内的 agent 拆成 内置 → 我创建 → 同事创建 三段。
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
const filteredAgents = computed(() => unsearchedFilteredAgents.value.filter(item => matchesResourceQuery(item, keyword.value)))

// 「工作空间」视图下的稳定排序：本空间内「我创建」在前、「同事创建 / 内建」
// 在后。给 contributor 视图把「本空间 · 仅查看」分组标题正好插在过渡处。
const unsearchedSortedMineAgents = computed(() => {
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
const sortedMineAgents = computed(() => unsearchedSortedMineAgents.value.filter(item => matchesResourceQuery(item, keyword.value)))

// 空间视角下的稳定排序：我自己创建的（is_mine）放前面，其余按 permission 切分。
const unsearchedSortedSpaceAgentsList = computed(() => {
  return [...spaceAgentsList.value].sort((a, b) => {
    const aMine = a.is_mine ? 0 : 1
    const bMine = b.is_mine ? 0 : 1
    if (aMine !== bMine) return aMine - bMine
    const aE = isSharedAgentEditable(a.permission) ? 0 : 1
    const bE = isSharedAgentEditable(b.permission) ? 0 : 1
    return aE - bE
  })
})
const sortedSpaceAgentsList = computed(() => unsearchedSortedSpaceAgentsList.value.filter(item => matchesResourceQuery(item.agent, keyword.value)))
const loading = ref(false)
const confirmDelete = useConfirmDelete()
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
const editorInitialHighlightField = ref<string>('')
/** 当前打开三点菜单的卡片 agent.id（用于受控弹出层，避免 computed 项无持久引用导致菜单不响应） */
const openMoreAgentId = ref<string | null>(null)

const showAgentListEmpty = computed(() => {
  if (loading.value || keyword.value.trim()) return false
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

const applyAgentListData = (res: { data: CustomAgent[]; disabled_own_agent_ids: string[] }) => {
  const disabledOwnIds = res.disabled_own_agent_ids || []
  agents.value = (res.data || []).map((agent: CustomAgent) => ({
    ...agent,
    showMore: false,
    disabled_by_me: disabledOwnIds.includes(agent.id)
  }))
  void checkAndOpenEditModal()
}

const fetchList = (force = false) => {
  loading.value = true
  return Promise.all([
    chatResources.fetchAgentsForList({ creator: creatorFilter.value }, force).then(applyAgentListData),
    orgStore.fetchOrganizations({ force }),
    orgStore.fetchSharedAgents({ force }),
  ]).finally(() => { loading.value = false }).then(() => {
    void checkAndOpenEditModal()
    // 各空间智能体数量已由 GET /organizations 的 resource_counts 带回，存于 orgStore.resourceCounts
    const counts = orgStore.resourceCounts?.agents?.by_organization
    if (counts) spaceAgentCountByOrg.value = { ...counts }
  })
}

// 检查 URL 参数并打开编辑模态框
const resolveAgentForEdit = (editId: string, sourceTenantId?: string): CustomAgent | null => {
  const own = agents.value.find(a => a.id === editId)
  if (own) return own
  if (sourceTenantId) {
    const shared = sharedAgents.value.find(
      s => s.agent?.id === editId && String(s.source_tenant_id) === sourceTenantId,
    )
    if (shared?.agent) return shared.agent as CustomAgent
  }
  return null
}

let editOpenGeneration = 0

const checkAndOpenEditModal = async () => {
  const generation = ++editOpenGeneration
  const editId = route.query.edit as string
  const section = route.query.section as string
  const sourceTenantId = route.query.sourceTenantId as string | undefined
  if (editId && (section === 'im' || section === 'embed' || section === 'integrations' || section === 'integration-im' || section === 'integration-embed')) {
    const tab = section === 'embed' || section === 'integration-embed' ? 'embed' : 'im'
    router.replace({
      path: '/platform/settings',
      query: { section: integrationSectionKey(tab), agentId: editId },
    })
    return
  }
  if (editId) {
    const agent = resolveAgentForEdit(editId, sourceTenantId)
    // A route change can remove a creator filter before the corresponding
    // all-agent fetch completes. Keep the deep-link query intact so the list
    // refresh callback can resolve and open the target instead of losing it.
    if (!agent) return

    const requestedSection = section || 'basic'
    const requestedHighlight = (route.query.highlight as string) || ''
    if (
      editorVisible.value
      && editingAgent.value?.id === agent.id
      && !requestedHighlight
    ) {
      // Global Settings may be covering this exact editor. Preserve any
      // unsaved draft and focus the requested configuration section in place.
      editorInitialSection.value = requestedSection
      focusAgentEditorSection(requestedSection)
    } else {
      // A global Settings dialog can be opened on top of an existing agent
      // editor. Flush visible=false before loading the deep-linked target so
      // AgentEditorModal's visibility watcher rebuilds its form data.
      editorVisible.value = false
      await nextTick()
      if (generation !== editOpenGeneration) return
      editingAgent.value = agent
      editorMode.value = 'edit'
      editorInitialSection.value = requestedSection
      editorInitialHighlightField.value = requestedHighlight
      editorVisible.value = true
    }
    if (generation !== editOpenGeneration) return
    // Drop the transient edit/section params but preserve other filter
    // state (scope / creator / q) so refreshing doesn't reset the view.
    const { edit: _e, section: _s, highlight: _h, sourceTenantId: _st, ...rest } = route.query
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
    if (v && (agents.value.length > 0 || sharedAgents.value.length > 0)) {
      void checkAndOpenEditModal()
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
  fetchList(true)
})

onMounted(() => {
  fetchList()
  window.addEventListener('openAgentEditor', handleOpenAgentEditor as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('openAgentEditor', handleOpenAgentEditor as EventListener)
})

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
  editorInitialSection.value = 'basic'
  editorInitialHighlightField.value = ''
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

// isMyAgent 仅用于卡片来源徽章在「我创建」与「同空间其他成员创建」之间切换。
// 跟 canManageAgent 区别：管理权限有 admin 兜底；徽章纯粹按 created_by 匹配。
// 内建 agent（created_by=""）也归到非 mine 一档，由模板上的 builtin 分支
// 提前拦截，不会落到 ResourceOriginBadge。
function isMyAgent(agent: { created_by?: string }): boolean {
  const userId = authStore.user?.id || ''
  return !!(agent.created_by && userId && agent.created_by === userId)
}

// agentOriginVariant 跟 kbOriginVariant 对齐：右下角徽章不再重复空间名
// （顶部 TenantSelector 已经标了空间身份），所有角色都用 creator 变体。
// 内建 agent 走 v-else 前的 builtin 分支，到不了这里。
function agentOriginVariant(agent: { created_by?: string }): 'mine' | 'creator' {
  return isMyAgent(agent) ? 'mine' : 'creator'
}

function showAgentOriginBadge(agent: { created_by?: string; creator_name?: string }): boolean {
  return shouldShowResourceOriginBadge({
    section: agentSectionOf(agent),
    variant: agentOriginVariant(agent),
    creatorName: (agent as any).creator_name,
    showSectionHeaders: true,
  })
}

function showAgentBuiltinBadge(agent: { is_builtin?: boolean }): boolean {
  if (!agent.is_builtin) return false
  return shouldShowResourceOriginBadge({
    section: agentSectionOf(agent),
    variant: 'mine',
    showSectionHeaders: true,
  })
}

// 按共享资源的实际权限划分可编辑和只读分组。
const AGENT_EDITABLE_PERMS = new Set(['admin', 'editor'])
function isSharedAgentEditable(perm: string | undefined): boolean {
  return !!perm && AGENT_EDITABLE_PERMS.has(perm)
}
// 同空间、非当前用户创建的 Agent 分组标题。
// contributor / viewer 在本空间里对这些 Agent 没有写权限，所以打"仅查看"；
// admin / owner 对整个空间都有编辑权限，"仅查看"反而误导，统一改成
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
// 是原始 agent（永远当作本空间）；sortedSpaceAgentsList 用 is_mine。
//
// 当前用户自己创建的 agent 在模板里**没有**独立分组标题（不像 KB 那边有
// "我创建的"段），所以这里返回 null——折叠任何分组都不会影响到它们。
const agentSectionOf = (item: any): AgentSectionKey | null => {
  // 内置 agent（is_builtin=true）单独成段，置顶展示——它们是空间共有的
  // 系统资源，跟"我 / 同事 / 共享"几个所有权分类不在同一维度。判定要早于
  // shared 那一档，因为 filteredAgents 里的 shared 条目也可能携带 is_builtin
  // （理论上不会，但保守一些）。
  if (item?.is_builtin === true) return 'builtin'
  // 跨空间 shared 条目（filteredAgents 拆出来的 isMine=false / 空间视图的
  // sortedSpaceAgentsList 用 is_mine=false）一律按 permission 分到
  // sharedEditable / sharedReadonly。
  if (item?.isMine === false || item?.is_mine === false) {
    return isSharedAgentEditable(item?.permission) ? 'sharedEditable' : 'sharedReadonly'
  }
  // 本空间内：我亲手创建 → 'mine'；同事 / 非内置但无 created_by → 'tenantOthers'。
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
  confirmDelete({
    title: t('agent.delete.confirmTitle'),
    body: t('agent.delete.confirmMessage', { name: agent.name }),
    onConfirm: async () => {
      try {
        const res: any = await deleteAgent(agent.id)
        if (res.success) {
          MessagePlugin.success(t('agent.messages.deleted'))
          fetchList(true)
        } else {
          MessagePlugin.error(res.message || t('agent.messages.deleteFailed'))
        }
      } catch (e: any) {
        MessagePlugin.error(e?.message || t('agent.messages.deleteFailed'))
      }
    },
  })
}

const handleCopy = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  copyAgent(agent.id).then((res: any) => {
    if (res.data) {
      MessagePlugin.success(t('agent.messages.copied'))
      fetchList(true)
    } else {
      MessagePlugin.error(res.message || t('agent.messages.copyFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.copyFailed'))
  })
}

/** 切换「我的」智能体停用状态（仅影响当前空间对话下拉显示） */
const handleToggleDisabled = (agent: AgentWithUI) => {
  openMoreAgentId.value = null
  const nextDisabled = !agent.disabled_by_me
  setSharedAgentDisabledByMe(agent.id, nextDisabled).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(nextDisabled ? t('agent.messages.disabled') : t('agent.messages.enabled'))
      fetchList(true)
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
      orgStore.fetchSharedAgents({ force: true })
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
      orgStore.fetchSharedAgents({ force: true })
    } else {
      MessagePlugin.error(res.message || t('agent.messages.saveFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('agent.messages.saveFailed'))
  })
}

const handleEditorSuccess = (agent?: CustomAgent) => {
  if (agent) {
    editingAgent.value = agent
    editorMode.value = 'edit'
  }
  fetchList(true)
}

// 暴露创建方法供外部调用
const openCreateModal = () => {
  editingAgent.value = null
  editorMode.value = 'create'
  editorInitialSection.value = 'basic'
  editorInitialHighlightField.value = ''
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
const visibleResultCount = computed(() => spaceSelection.value === 'mine' ? sortedMineAgents.value.length : spaceSelectionOrgId.value ? sortedSpaceAgentsList.value.length : filteredAgents.value.length)
// A new search reveals matching rows even if their group was previously collapsed.
watch(keyword, () => { collapsedAgentSections.value = new Set() })
</script>

<style scoped lang="less">
@import (reference) '@/components/css/resource-card.less';

.agent-list-container {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: 100%;
  display: flex;
}

.agent-list-content { .resource-list-content(); }

.agent-list-main { .resource-list-main(); }

.agent-list-main-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  padding: 12px;
  background: var(--td-bg-color-container);
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  padding-right: 28px;

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
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: var(--app-text-4xl);
    font-weight: 600;
    line-height: 32px;
  }
}

.header-subtitle {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: var(--app-text-base);
  font-weight: 400;
  line-height: 20px;
}

.header-action-btn {
  padding: 0;
  min-width: 28px;
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--td-bg-color-container) 72%, transparent);
  transition: background var(--app-motion-base), border-color var(--app-motion-base), color var(--app-motion-base);

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    border-color: var(--td-component-stroke);
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

.card-bottom-source {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--app-radius-lg);
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
  font-size: var(--app-text-xs);
  font-weight: 500;
  flex-shrink: 0;
}

// 共享给我 · 可编辑 / 仅查看 分组标题，与 KB 列表 .kb-section-header 对齐。
.agent-section-header {
  .resource-section-header();
}

.agent-card-wrap {
  .resource-card-grid();
}

// 共享卡片尺寸与交互。
.agent-card {
  .resource-card();

  .agent-favorite-star { .resource-favorite-button(); }
}

.builtin-badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 8px;
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container-hover);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-xs);
  font-weight: 500;
  flex-shrink: 0;
}

.builtin-avatar {
  border-radius: var(--app-radius-md);

  &.agent-emoji {
    font-size: var(--app-text-2xl);
    line-height: 1;
    background: var(--td-bg-color-container-hover);
  }

  &.normal {
    background: linear-gradient(135deg, color-mix(in srgb, var(--td-brand-color) 15%, transparent) 0%, color-mix(in srgb, var(--td-brand-color) 8%, transparent) 100%);
    color: var(--td-brand-color-active);
  }

  &.agent {
    background: linear-gradient(135deg, color-mix(in srgb, var(--app-accent-purple) 15%, transparent) 0%, color-mix(in srgb, var(--app-accent-purple) 8%, transparent) 100%);
    color: var(--td-brand-color);
  }
}

/* 与知识库卡片内容区一致 */
/* 三个列表卡片统一：描述字体 */
.bottom-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.feature-badge {
  .resource-feature-badge();

  &.mode-normal {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color-active);

    &:hover {
      background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    }
  }

  &.mode-agent {
    background: color-mix(in srgb, var(--app-accent-purple) 8%, transparent);
    color: var(--td-brand-color);

    &:hover {
      background: color-mix(in srgb, var(--app-accent-purple) 12%, transparent);
    }
  }

  &.web-search {
    background: color-mix(in srgb, var(--td-warning-color) 8%, transparent);
    color: var(--td-warning-color);

    &:hover {
      background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    }
  }

  &.knowledge {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);

    &:hover {
      background: var(--td-bg-color-container-hover);
    }
  }

  &.mcp {
    background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
    color: var(--td-error-color);

    &:hover {
      background: color-mix(in srgb, var(--td-error-color) 12%, transparent);
    }
  }

  &.multi-turn {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);

    &:hover {
      background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    }
  }
}

// 删除确认对话框样式
:deep(.t-dialog__position.t-dialog--top) {
  padding-top: 40vh !important;
}

.resource-list-header();

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
  font-size: var(--app-text-2xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.shared-detail-drawer-close {
  width: 32px;
  height: 32px;
  border: none;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;

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
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 20px 0 12px 0;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.shared-detail-drawer-body .shared-detail-label {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  line-height: 1.4;
}

.shared-detail-drawer-body .shared-detail-value {
  font-size: var(--app-text-base);
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
