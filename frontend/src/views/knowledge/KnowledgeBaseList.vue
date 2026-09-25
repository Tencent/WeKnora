<template>
  <div class="kb-list-container">
    <div class="kb-list-content">
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <div class="title-row" style="--wails-draggable: drag">
            <h2 style="--wails-draggable: drag">
              <ResourceIcon type="knowledge" :size="24" />
              {{ $t('knowledgeBase.title') }}
            </h2>
            <div class="header-actions" style="--wails-draggable: no-drag">
              <ResourceSortControl v-model="selectedResourceSort" />
              <t-tooltip v-if="authStore.hasRole('contributor')" :content="$t('knowledgeList.create')" placement="bottom">
                 <t-button variant="text" theme="default" size="small" class="header-action-btn"
                   data-guide="kb-list-create" style="--wails-draggable: no-drag" @click="handleCreateKnowledgeBase">
                   <template #icon><t-icon name="folder-add" size="16px" /></template>
                  {{ $t('knowledgeList.create') }}
                 </t-button>
               </t-tooltip>
             </div>
          </div>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('knowledgeList.subtitle') }}</p>
        </div>
      </div>
      <ResourceListToolbar :hide-scopes="authStore.isLiteMode" v-model="spaceSelection" v-model:query="keyword" :count-all="allKnowledgeBases"
      :count-mine="kbs.length" :count-by-org="effectiveSharedCountByOrg" :count-favorites="kbFavoritesCount"
      :count-recents="kbRecentsCount" />
      <div class="kb-list-main">
        <EmptyState v-if="keyword.trim() && !(loading || spaceKbsLoading) && visibleResultCount === 0" icon="search"
          :title="$t('common.noResult')">
          <t-button variant="outline" @click="keyword = ''">{{ $t('common.clear') }}</t-button>
        </EmptyState>
        <!-- creator filter intentionally removed from chrome: every card
             already shows its creator via ResourceOriginBadge / avatar, so
             a dedicated horizontal switch added more noise than signal.
             The backend `?creator=mine|others` param and the URL-state
             field are kept so a future "filter by member" entry point
             (e.g. clicking an avatar) can deep-link without re-plumbing. -->

        <!-- 未初始化知识库提示 -->
        <div v-if="hasUninitializedKbs" class="warning-banner">
          <t-icon name="info-circle" size="16px" />
          <span>{{ $t('knowledgeList.uninitializedBanner') }}</span>
        </div>

        <!-- 骨架屏占位 -->
        <div v-if="loading && kbs.length === 0" class="kb-card-wrap">
          <div v-for="n in 6" :key="'skel-' + n" class="kb-card kb-card-skeleton is-skeleton">
            <div class="card-header">
              <t-skeleton animation="gradient" :row-col="[{ width: '60%', height: '20px' }]" />
            </div>
            <div class="card-content">
              <t-skeleton animation="gradient"
                :row-col="[{ width: '100%', height: '14px' }, { width: '80%', height: '14px' }]" />
            </div>
            <div class="card-bottom">
              <t-skeleton animation="gradient"
                :row-col="[[{ width: '28px', height: '28px', type: 'rect' }, { width: '28px', height: '28px', type: 'rect' }]]" />
            </div>
          </div>
        </div>

        <!-- 卡片网格：全部 / 收藏 / 最近 — 共用同一份卡片模板，
             仅依赖 filteredKnowledgeBases 切片即可切换视图 -->
        <div
          v-if="(spaceSelection === 'all' || spaceSelection === 'favorites' || spaceSelection === 'recents') && filteredKnowledgeBases.length > 0"
          class="kb-card-wrap">
          <!-- 置顶分组标题 -->
          <div
            v-if="filteredKnowledgeBases[0] && filteredKnowledgeBases[0].isMine && filteredKnowledgeBases[0].is_pinned"
            class="kb-section-header kb-section-header-pinned" role="button" tabindex="0"
            :aria-expanded="!isKbSectionCollapsed('pinned')" @click="toggleKbSection('pinned')"
            @keydown.enter.prevent="toggleKbSection('pinned')"
            @keydown.space.prevent="toggleKbSection('pinned')">
            <t-icon name="pin-filled" size="14px" />
            <span>{{ $t('knowledgeList.sections.pinned') }}</span>
            <span class="kb-section-count">{{ filteredKbSectionCounts.pinned }}</span>
            <t-icon class="kb-section-toggle" :name="isKbSectionCollapsed('pinned') ? 'chevron-right' : 'chevron-down'"
              size="14px" />
          </div>
          <!-- 全部：我的知识库 + 共享给我的知识库。
               「已置顶」分组由顶部 header 接管。其余分段（我创建 / 本空间 ·
               仅查看 / 共享给我）各自打自己的标题；原本的「其他」过渡标题
               在 per-user 置顶模型下已无意义，删除以免和具体子段标题叠加。 -->
          <template v-for="(kb, index) in filteredKnowledgeBases" :key="kb.id">
            <!-- 我创建的：第一张「我创建」非置顶卡片前打标题，
                 对所有角色显示，不受上方是否存在「已置顶」分组影响。 -->
            <div v-if="kb.isMine
              && isMyKb(kb as KB)
              && !kb.is_pinned
              && (index === 0
                || (filteredKnowledgeBases[index - 1] as any).is_pinned)" class="kb-section-header" role="button"
              tabindex="0" :aria-expanded="!isKbSectionCollapsed('mine')" @click="toggleKbSection('mine')"
              @keydown.enter.prevent="toggleKbSection('mine')"
              @keydown.space.prevent="toggleKbSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('knowledgeList.sections.mine') }}</span>
              <span class="kb-section-count">{{ filteredKbSectionCounts.mine }}</span>
              <t-icon class="kb-section-toggle" :name="isKbSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'"
                size="14px" />
            </div>
            <!-- 本空间 · 仅查看：本空间里同事创建、对当前 contributor 不可编辑。
                 当前卡片必须是非置顶（否则归在「已置顶」），且前一张要么
                 不存在、要么是「共享给我」、要么是我创建、要么是置顶卡片
                 （置顶→非置顶的过渡同样要打这个标题）。 -->
            <div v-if="kb.isMine
              && !isMyKb(kb as KB)
              && !kb.is_pinned
              && (index === 0
                || !filteredKnowledgeBases[index - 1].isMine
                || isMyKb(filteredKnowledgeBases[index - 1] as KB)
                || (filteredKnowledgeBases[index - 1] as any).is_pinned)" class="kb-section-header" role="button"
              tabindex="0" :aria-expanded="!isKbSectionCollapsed('tenantOthers')" @click="toggleKbSection('tenantOthers')"
              @keydown.enter.prevent="toggleKbSection('tenantOthers')"
              @keydown.space.prevent="toggleKbSection('tenantOthers')">
              <t-icon :name="tenantSectionIconName" size="14px" />
              <span>{{ $t(tenantSectionLabelKey) }}</span>
              <span class="kb-section-count">{{ filteredKbSectionCounts.tenantOthers }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('tenantOthers') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：从「我的（含同事）」首次过渡到共享 + 可编辑 -->
            <div v-if="!kb.isMine
              && isSharedKbEditable((kb as any).permission)
              && (index === 0 || filteredKnowledgeBases[index - 1].isMine)" class="kb-section-header" role="button"
              tabindex="0" :aria-expanded="!isKbSectionCollapsed('sharedEditable')" @click="toggleKbSection('sharedEditable')"
              @keydown.enter.prevent="toggleKbSection('sharedEditable')"
              @keydown.space.prevent="toggleKbSection('sharedEditable')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="edit-1" size="12px" class="kb-section-subicon" />
              <span>{{ $t('knowledgeList.sections.sharedEditable') }}</span>
              <span class="kb-section-count">{{ filteredKbSectionCounts.sharedEditable }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('sharedEditable') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 仅查看：从「可编辑共享 / 我的」过渡到 viewer 共享 -->
            <div v-if="!kb.isMine
              && !isSharedKbEditable((kb as any).permission)
              && (index === 0
                || filteredKnowledgeBases[index - 1].isMine
                || isSharedKbEditable((filteredKnowledgeBases[index - 1] as any).permission))"
              class="kb-section-header" role="button" tabindex="0" :aria-expanded="!isKbSectionCollapsed('sharedReadonly')" @click="toggleKbSection('sharedReadonly')"
              @keydown.enter.prevent="toggleKbSection('sharedReadonly')"
              @keydown.space.prevent="toggleKbSection('sharedReadonly')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="browse" size="12px" class="kb-section-subicon" />
              <span>{{ $t('knowledgeList.sections.sharedReadonly') }}</span>
              <span class="kb-section-count">{{ filteredKbSectionCounts.sharedReadonly }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('sharedReadonly') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 我的知识库卡片 -->
            <div v-if="kb.isMine" v-show="!isKbSectionCollapsed(kbSectionOf(kb))" class="kb-card" :class="{
              'uninitialized': !isInitialized(kb),
              'kb-type-document': (kb.type || 'document') === 'document',
              'kb-type-faq': kb.type === 'faq',
              'highlight-flash': highlightedKbId !== null && highlightedKbId === kb.id
            }"
              :ref="el => { if (highlightedKbId !== null && highlightedKbId === kb.id && el) highlightedCardRef = el as HTMLElement }"
              role="link" tabindex="0" @keydown.enter.self.prevent="handleCardClick(kb)" @keydown.space.self.prevent="handleCardClick(kb)" @click="handleCardClick(kb)">
              <!-- 行尾收藏操作，与更多菜单分开。 -->
              <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isKbFavorited(kb.id) }"
                :aria-label="$t('listSpaceSidebar.favorites')" :aria-pressed="isKbFavorited(kb.id)" @click.stop="toggleFavoriteKb(kb.id, $event)">
                <t-icon :name="isKbFavorited(kb.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="kb.name">
                  <KbWikiBadge v-if="isWikiKb(kb)" />
                  <span class="card-title-text">{{ kb.name }}</span>
                </span>
                <!-- The card menu always exists when the card is visible: pin
                     is now per-user and available to anyone who can see the KB
                     (backend route only requires KB read access). Settings /
                     Delete are mutations, so they stay behind canManageKBCard. -->
                <t-popup overlayClassName="card-more-popup" trigger="click" destroy-on-close
                  placement="bottom-right">
                  <button type="button" :aria-label="$t('common.expand')" class="more-wrap" @click.stop>
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </button>
                  <template #content>
                    <div class="popup-menu" @click.stop>
                      <div class="popup-menu-item" @click.stop="handleTogglePinById(kb.id)">
                        <t-icon class="menu-icon" :name="kb.is_pinned ? 'pin-filled' : 'pin'" />
                        <span>{{ kb.is_pinned ? $t('knowledgeList.pin.unpin') : $t('knowledgeList.pin.pin') }}</span>
                      </div>
                      <div v-if="canDuplicateKBCard(kb)" class="popup-menu-item"
                        @click.stop="handleDuplicateById(kb.id)">
                        <t-icon class="menu-icon" name="file-copy" />
                        <span>{{ $t('knowledgeList.menu.duplicate') }}</span>
                      </div>
                      <template v-if="canManageKBCard(kb)">
                        <div class="popup-menu-item" @click.stop="handleSettingsById(kb.id)">
                          <t-icon class="menu-icon" name="setting" />
                          <span>{{ $t('knowledgeBase.settings') }}</span>
                        </div>
                        <div class="popup-menu-item delete" @click.stop="handleDeleteById(kb.id)">
                          <t-icon class="menu-icon" name="delete" />
                          <span>{{ $t('common.delete') }}</span>
                        </div>
                      </template>
                    </div>
                  </template>
                </t-popup>
              </div>

              <!-- 卡片内容 -->
              <div class="card-content">
                <div class="card-description" :title="kb.description || $t('knowledgeBase.noDescription')">
                  {{ kb.description || $t('knowledgeBase.noDescription') }}
                </div>
              </div>

              <!-- 卡片底部 -->
              <div class="card-bottom">
                <div class="bottom-left">
                  <div class="feature-badges">
                    <t-tooltip
                      :content="kb.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'type-document': (kb.type || 'document') === 'document', 'type-faq': kb.type === 'faq' }">
                        <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'file'" size="14px" />
                        <span class="badge-count">{{ kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count ||
                          0) }}</span>
                        <t-icon v-if="kb.isProcessing" name="loading" size="12px" class="processing-icon" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.extract_config?.enabled" :content="$t('knowledgeList.features.knowledgeGraph')"
                      placement="top">
                      <div class="feature-badge kg">
                        <t-icon name="relation" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.vlm_config?.enabled" :content="$t('knowledgeList.features.multimodal')"
                      placement="top">
                      <div class="feature-badge multimodal">
                        <t-icon name="image" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.question_generation_config?.enabled"
                      :content="$t('knowledgeList.features.questionGeneration')" placement="top">
                      <div class="feature-badge question">
                        <t-icon name="help-circle" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.share_count && kb.share_count > 0"
                      :content="$t('knowledgeList.sharedToOrgs', { count: kb.share_count })" placement="top">
                      <div class="feature-badge shared">
                        <t-icon name="share" size="14px" />
                      </div>
                    </t-tooltip>
                  </div>
                </div>
                <div v-if="!authStore.isLiteMode && showKbOriginBadge(kb)" class="bottom-right">
                  <ResourceOriginBadge :variant="kbOriginVariant(kb)" :creator-name="kb.creator_name" />
                </div>
              </div>
            </div>

            <!-- 共享知识库卡片 -->
            <div v-else v-show="!isKbSectionCollapsed(kbSectionOf(kb))" class="kb-card shared-kb-card" :class="{
              'kb-type-document': (kb.type || 'document') === 'document',
              'kb-type-faq': kb.type === 'faq'
            }" role="link" tabindex="0" @keydown.enter.self.prevent="handleSharedKbClickFromAll(kb)" @keydown.space.self.prevent="handleSharedKbClickFromAll(kb)" @click="handleSharedKbClickFromAll(kb)">
              <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isKbFavorited(kb.id) }"
                :aria-label="$t('listSpaceSidebar.favorites')" :aria-pressed="isKbFavorited(kb.id)" @click.stop="toggleFavoriteKb(kb.id, $event)">
                <t-icon :name="isKbFavorited(kb.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="kb.name">
                  <KbWikiBadge v-if="isWikiKb(kb)" />
                  <span class="card-title-text">{{ kb.name }}</span>
                </span>
                <t-tooltip :content="$t('knowledgeList.menu.viewDetails')" placement="top">
                  <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetailFromAll(kb)"
                    :aria-label="$t('knowledgeList.menu.viewDetails')">
                    <t-icon name="info-circle" size="16px" />
                  </button>
                </t-tooltip>
              </div>

              <!-- 卡片内容 -->
              <div class="card-content">
                <div class="card-description" :title="kb.description || $t('knowledgeBase.noDescription')">
                  {{ kb.description || $t('knowledgeBase.noDescription') }}
                </div>
              </div>

              <!-- 卡片底部 -->
              <div class="card-bottom">
                <div class="bottom-left">
                  <div class="feature-badges">
                    <t-tooltip
                      :content="kb.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'type-document': (kb.type || 'document') === 'document', 'type-faq': kb.type === 'faq' }">
                        <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'file'" size="14px" />
                        <span class="badge-count">{{ kb.type === 'faq' ? (kb.chunk_count || '-') : (kb.knowledge_count
                          || '-')
                        }}</span>
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.extract_config?.enabled" :content="$t('knowledgeList.features.knowledgeGraph')"
                      placement="top">
                      <div class="feature-badge kg">
                        <t-icon name="relation" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip
                      v-if="kb.vlm_config?.enabled || (kb.storage_provider_config?.provider && kb.storage_provider_config.provider !== 'local')"
                      :content="$t('knowledgeList.features.multimodal')" placement="top">
                      <div class="feature-badge multimodal">
                        <t-icon name="image" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.question_generation_config?.enabled"
                      :content="$t('knowledgeList.features.questionGeneration')" placement="top">
                      <div class="feature-badge question">
                        <t-icon name="help-circle" size="14px" />
                      </div>
                    </t-tooltip>
                  </div>
                </div>
                <div class="bottom-right">
                  <t-tooltip :content="kb.org_name" placement="top">
                    <div class="org-source">
                      <img src="@/assets/img/organization-green.svg" class="org-source-icon" alt=""
                        aria-hidden="true" />
                      <span>{{ kb.org_name }}</span>
                    </div>
                  </t-tooltip>
                </div>
              </div>
            </div>
          </template>
        </div>

        <div v-if="spaceSelection === 'mine' && sortedMineKbs.length > 0" class="kb-card-wrap">
          <!-- 置顶分组标题 -->
          <div v-if="sortedMineKbs[0] && sortedMineKbs[0].is_pinned" class="kb-section-header kb-section-header-pinned"
            role="button" tabindex="0" :aria-expanded="!isKbSectionCollapsed('pinned')" @click="toggleKbSection('pinned')"
            @keydown.enter.prevent="toggleKbSection('pinned')"
            @keydown.space.prevent="toggleKbSection('pinned')">
            <t-icon name="pin-filled" size="14px" />
            <span>{{ $t('knowledgeList.sections.pinned') }}</span>
            <span class="kb-section-count">{{ mineKbSectionCounts.pinned }}</span>
            <t-icon class="kb-section-toggle" :name="isKbSectionCollapsed('pinned') ? 'chevron-right' : 'chevron-down'"
              size="14px" />
          </div>
          <!-- 我的知识库。「已置顶」由顶部 header 接管；其余各分段各打各的
               标题——见「全部」tab 同处注释。 -->
          <template v-for="(kb, index) in sortedMineKbs" :key="kb.id">
            <!-- 我创建的：第一张非置顶的我创建卡片前打标题，无论上方是否
                 有「已置顶」段都要显示，和「本空间 · 仅查看」对齐——见
                 「全部」tab 同处注释。 -->
            <div v-if="isMyKb(kb)
              && !kb.is_pinned
              && (index === 0 || sortedMineKbs[index - 1].is_pinned)" class="kb-section-header" role="button"
              tabindex="0" :aria-expanded="!isKbSectionCollapsed('mine')" @click="toggleKbSection('mine')"
              @keydown.enter.prevent="toggleKbSection('mine')"
              @keydown.space.prevent="toggleKbSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('knowledgeList.sections.mine') }}</span>
              <span class="kb-section-count">{{ mineKbSectionCounts.mine }}</span>
              <t-icon class="kb-section-toggle" :name="isKbSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'"
                size="14px" />
            </div>
            <!-- 本空间 · 仅查看：当前非置顶的同事 KB，且前一张要么不存在、
                 要么是我创建、要么是置顶卡片（置顶→非置顶过渡）。 -->
            <div v-if="!isMyKb(kb)
              && !kb.is_pinned
              && (index === 0
                || isMyKb(sortedMineKbs[index - 1])
                || sortedMineKbs[index - 1].is_pinned)" class="kb-section-header" role="button" tabindex="0"
              :aria-expanded="!isKbSectionCollapsed('tenantOthers')" @click="toggleKbSection('tenantOthers')"
              @keydown.enter.prevent="toggleKbSection('tenantOthers')"
              @keydown.space.prevent="toggleKbSection('tenantOthers')">
              <t-icon :name="tenantSectionIconName" size="14px" />
              <span>{{ $t(tenantSectionLabelKey) }}</span>
              <span class="kb-section-count">{{ mineKbSectionCounts.tenantOthers }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('tenantOthers') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <div v-show="!isKbSectionCollapsed(kbSectionOf(kb))" class="kb-card" :class="{
              'uninitialized': !isInitialized(kb),
              'kb-type-document': (kb.type || 'document') === 'document',
              'kb-type-faq': kb.type === 'faq',
              'highlight-flash': highlightedKbId !== null && highlightedKbId === kb.id
            }"
              :ref="el => { if (highlightedKbId !== null && highlightedKbId === kb.id && el) highlightedCardRef = el as HTMLElement }"
              role="link" tabindex="0" @keydown.enter.self.prevent="handleCardClick(kb)" @keydown.space.self.prevent="handleCardClick(kb)" @click="handleCardClick(kb)">
              <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isKbFavorited(kb.id) }"
                :aria-label="$t('listSpaceSidebar.favorites')" :aria-pressed="isKbFavorited(kb.id)" @click.stop="toggleFavoriteKb(kb.id, $event)">
                <t-icon :name="isKbFavorited(kb.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="kb.name">
                  <KbWikiBadge v-if="isWikiKb(kb)" />
                  <span class="card-title-text">{{ kb.name }}</span>
                </span>
                <!-- See the matching block in the "all" tab template for why
                     this is no longer gated by canManageKBCard. -->
                <t-popup v-model="kb.showMore" overlayClassName="card-more-popup"
                  :on-visible-change="onVisibleChange" trigger="click" destroy-on-close placement="bottom-right">
                  <button type="button" :aria-label="$t('common.expand')" class="more-wrap" @click.stop="openMore(index)"
                    :class="{ 'active-more': currentMoreIndex === index }">
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </button>
                  <template #content>
                    <div class="popup-menu" @click.stop>
                      <div class="popup-menu-item" @click.stop="handleTogglePin(kb)">
                        <t-icon class="menu-icon" :name="kb.is_pinned ? 'pin-filled' : 'pin'" />
                        <span>{{ kb.is_pinned ? $t('knowledgeList.pin.unpin') : $t('knowledgeList.pin.pin') }}</span>
                      </div>
                      <div v-if="canDuplicateKBCard(kb)" class="popup-menu-item" @click.stop="handleDuplicate(kb)">
                        <t-icon class="menu-icon" name="file-copy" />
                        <span>{{ $t('knowledgeList.menu.duplicate') }}</span>
                      </div>
                      <template v-if="canManageKBCard(kb)">
                        <div class="popup-menu-item" @click.stop="handleSettings(kb)">
                          <t-icon class="menu-icon" name="setting" />
                          <span>{{ $t('knowledgeBase.settings') }}</span>
                        </div>
                        <div class="popup-menu-item delete" @click.stop="handleDelete(kb)">
                          <t-icon class="menu-icon" name="delete" />
                          <span>{{ $t('common.delete') }}</span>
                        </div>
                      </template>
                    </div>
                  </template>
                </t-popup>
              </div>

              <!-- 卡片内容 -->
              <div class="card-content">
                <div class="card-description" :title="kb.description || $t('knowledgeBase.noDescription')">
                  {{ kb.description || $t('knowledgeBase.noDescription') }}
                </div>
              </div>

              <!-- 卡片底部 -->
              <div class="card-bottom">
                <div class="bottom-left">
                  <div class="feature-badges">
                    <t-tooltip
                      :content="kb.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'type-document': (kb.type || 'document') === 'document', 'type-faq': kb.type === 'faq' }">
                        <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'file'" size="14px" />
                        <span class="badge-count">{{ kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count ||
                          0) }}</span>
                        <t-icon v-if="kb.isProcessing" name="loading" size="12px" class="processing-icon" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.extract_config?.enabled" :content="$t('knowledgeList.features.knowledgeGraph')"
                      placement="top">
                      <div class="feature-badge kg">
                        <t-icon name="relation" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip
                      v-if="kb.vlm_config?.enabled || (kb.storage_provider_config?.provider && kb.storage_provider_config.provider !== 'local')"
                      :content="$t('knowledgeList.features.multimodal')" placement="top">
                      <div class="feature-badge multimodal">
                        <t-icon name="image" size="14px" />
                      </div>
                    </t-tooltip>
                    <t-tooltip v-if="kb.question_generation_config?.enabled"
                      :content="$t('knowledgeList.features.questionGeneration')" placement="top">
                      <div class="feature-badge question">
                        <t-icon name="help-circle" size="14px" />
                      </div>
                    </t-tooltip>
                    <!-- 共享状态图标 -->
                    <t-tooltip v-if="(kb.share_count ?? 0) > 0"
                      :content="$t('knowledgeList.sharedToOrgs', { count: kb.share_count ?? 0 })" placement="top">
                      <div class="feature-badge shared">
                        <t-icon name="share" size="14px" />
                      </div>
                    </t-tooltip>
                  </div>
                </div>
                <div v-if="!authStore.isLiteMode && showKbOriginBadge(kb)" class="bottom-right">
                  <ResourceOriginBadge :variant="kbOriginVariant(kb)" :creator-name="kb.creator_name" />
                </div>
              </div>
            </div>
          </template>
        </div>

        <!-- 协作 / 共享给我 聚合视图已移除：共享 KB 走「全部」或具体空间下展示 -->

        <!-- 按空间筛选：该空间内全部知识库（含我共享的） -->
        <div v-if="spaceSelectionOrgId && spaceKbsLoading" class="kb-list-main-loading">
          <t-loading size="medium" text="" />
        </div>
        <div v-else-if="spaceSelectionOrgId && sortedSpaceKbsList.length > 0" class="kb-card-wrap">
          <template v-for="(shared, index) in sortedSpaceKbsList"
            :key="'shared-' + (shared.share_id || `agent-${shared.knowledge_base?.id}-${shared.source_from_agent?.agent_id || ''}`)">
            <!-- 我共享的：本空间下我自己创建并共享进来的条目，只在第一条 is_mine 上挂标题 -->
            <div v-if="shared.is_mine && index === 0" class="kb-section-header"
              role="button" tabindex="0" :aria-expanded="!isKbSectionCollapsed('sharedByMe')" @click="toggleKbSection('sharedByMe')"
              @keydown.enter.prevent="toggleKbSection('sharedByMe')"
              @keydown.space.prevent="toggleKbSection('sharedByMe')">
              <t-icon name="share" size="14px" />
              <span>{{ $t('knowledgeList.sections.sharedByMe') }}</span>
              <span class="kb-section-count">{{ spaceKbSectionCounts.sharedByMe }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('sharedByMe') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：从「我的」首次进入「共享 + 可编辑」 -->
            <div v-if="!shared.is_mine
              && isSharedKbEditable(shared.permission)
              && (index === 0 || sortedSpaceKbsList[index - 1].is_mine)" class="kb-section-header"
              role="button" tabindex="0" :aria-expanded="!isKbSectionCollapsed('sharedEditable')" @click="toggleKbSection('sharedEditable')"
              @keydown.enter.prevent="toggleKbSection('sharedEditable')"
              @keydown.space.prevent="toggleKbSection('sharedEditable')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="edit-1" size="12px" class="kb-section-subicon" />
              <span>{{ $t('knowledgeList.sections.sharedEditable') }}</span>
              <span class="kb-section-count">{{ spaceKbSectionCounts.sharedEditable }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('sharedEditable') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 仅查看：从「可编辑共享 / 我的」首次进入「viewer」 -->
            <div v-if="!shared.is_mine
              && !isSharedKbEditable(shared.permission)
              && (index === 0
                || sortedSpaceKbsList[index - 1].is_mine
                || isSharedKbEditable(sortedSpaceKbsList[index - 1].permission))" class="kb-section-header"
              role="button" tabindex="0" :aria-expanded="!isKbSectionCollapsed('sharedReadonly')" @click="toggleKbSection('sharedReadonly')"
              @keydown.enter.prevent="toggleKbSection('sharedReadonly')"
              @keydown.space.prevent="toggleKbSection('sharedReadonly')">
              <t-icon name="usergroup-add" size="14px" />
              <t-icon name="browse" size="12px" class="kb-section-subicon" />
              <span>{{ $t('knowledgeList.sections.sharedReadonly') }}</span>
              <span class="kb-section-count">{{ spaceKbSectionCounts.sharedReadonly }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('sharedReadonly') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <div v-show="!isSpaceKbCollapsed(shared)" class="kb-card shared-kb-card" :class="{
              'kb-type-document': (shared.knowledge_base.type || 'document') === 'document',
              'kb-type-faq': shared.knowledge_base.type === 'faq'
            }" role="link" tabindex="0" @keydown.enter.self.prevent="handleSharedKbClick(shared)" @keydown.space.self.prevent="handleSharedKbClick(shared)" @click="handleSharedKbClick(shared)">
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="shared.knowledge_base.name">
                  <KbWikiBadge v-if="isWikiKb(shared.knowledge_base)" />
                  <span class="card-title-text">{{ shared.knowledge_base.name }}</span>
                </span>
                <t-tooltip v-if="!shared.is_mine" :content="$t('knowledgeList.menu.viewDetails')" placement="top">
                  <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetail(shared)"
                    :aria-label="$t('knowledgeList.menu.viewDetails')">
                    <t-icon name="info-circle" size="16px" />
                  </button>
                </t-tooltip>
              </div>

              <!-- 卡片内容 -->
              <div class="card-content">
                <div class="card-description" :title="shared.knowledge_base.description || $t('knowledgeBase.noDescription')">
                  {{ shared.knowledge_base.description || $t('knowledgeBase.noDescription') }}
                </div>
              </div>

              <!-- 卡片底部 -->
              <div class="card-bottom">
                <div class="bottom-left">
                  <div class="feature-badges">
                    <t-tooltip
                      :content="shared.knowledge_base.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')"
                      placement="top">
                      <div class="feature-badge"
                        :class="{ 'type-document': (shared.knowledge_base.type || 'document') === 'document', 'type-faq': shared.knowledge_base.type === 'faq' }">
                        <t-icon :name="shared.knowledge_base.type === 'faq' ? 'chat-bubble-help' : 'file'"
                          size="14px" />
                        <span class="badge-count">{{ shared.knowledge_base.type === 'faq' ?
                          (shared.knowledge_base.chunk_count ??
                            '-') : (shared.knowledge_base.knowledge_count ?? '-') }}</span>
                      </div>
                    </t-tooltip>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </div>

        <!-- 全部空状态：保留「新建知识库」CTA，因为是空间没有任何 KB 的真空场景 -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'all' && filteredKnowledgeBases.length === 0 && !loading" :title="$t('knowledgeList.empty.title')"
          :description="$t('knowledgeList.empty.description')">
          <template #icon><ResourceIcon type="knowledge" :size="32" /></template>
          <t-button v-if="authStore.hasRole('contributor')" theme="primary" class="kb-create-btn"
            data-guide="kb-list-create" @click="handleCreateKnowledgeBase">
            <template #icon><t-icon name="folder-add" /></template>
            {{ $t('knowledgeList.create') }}
          </t-button>
        </EmptyState>

        <!-- 收藏空状态：不放创建按钮——「没有收藏」 ≠ 「没有知识库」，
             正确引导是「去星标一下」，不是「再建一个」。 -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'favorites' && filteredKnowledgeBases.length === 0 && !loading" icon="star" :title="$t('knowledgeList.empty.favoritesTitle')"
          :description="$t('knowledgeList.empty.favoritesDescription')" />

        <!-- 最近空状态：同理，引导是「去打开一个」。 -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'recents' && filteredKnowledgeBases.length === 0 && !loading" icon="history" :title="$t('knowledgeList.empty.recentsTitle')"
          :description="$t('knowledgeList.empty.recentsDescription')" />

        <!-- 我的知识库空状态 -->
        <EmptyState v-if="!keyword.trim() && spaceSelection === 'mine' && kbs.length === 0 && !loading" :title="$t('knowledgeList.empty.title')"
          :description="$t('knowledgeList.empty.description')">
          <template #icon><ResourceIcon type="knowledge" :size="32" /></template>
          <t-button v-if="authStore.hasRole('contributor')" theme="primary" class="kb-create-btn"
            data-guide="kb-list-create" @click="handleCreateKnowledgeBase">
            <template #icon><t-icon name="folder-add" /></template>
            {{ $t('knowledgeList.create') }}
          </t-button>
        </EmptyState>

        <!-- 空间下知识库空状态 -->
        <EmptyState v-if="!keyword.trim() && spaceSelectionOrgId && !spaceKbsLoading && spaceKbsList.length === 0" :title="$t('knowledgeList.empty.sharedTitle')"
          :description="$t('knowledgeList.empty.sharedDescription')">
          <template #icon><ResourceIcon type="knowledge" :size="32" /></template>
        </EmptyState>
      </div>
    </div>

    <!-- 知识库编辑器（创建/编辑统一组件） -->
    <KnowledgeBaseEditorModal :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
      :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
      @update:visible="(val) => val ? null : uiStore.closeKBEditor()" @success="handleKBEditorSuccess" />

    <!-- 右侧：共享知识库详情面板 -->
    <Teleport to="body">
      <Transition name="shared-detail-drawer">
        <div v-if="sharedDetailPanelVisible && currentSharedKbForDetail" class="shared-detail-drawer-overlay"
          @click.self="closeSharedDetailPanel">
          <div class="shared-detail-drawer">
            <div class="shared-detail-drawer-header">
              <h3 class="shared-detail-drawer-title">{{ $t('knowledgeList.detail.title') }}</h3>
              <button type="button" class="shared-detail-drawer-close" @click="closeSharedDetailPanel"
                :aria-label="$t('general.close')">
                <t-icon name="close" size="20px" />
              </button>
            </div>
            <div class="shared-detail-drawer-body">
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('knowledgeBase.name') }}</span>
                <span class="shared-detail-value">{{ currentSharedKbForDetail.knowledge_base.name }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('knowledgeList.detail.sourceType') }}</span>
                <span class="shared-detail-value shared-detail-source-type">
                  {{ currentSharedKbForDetail.source_from_agent ? $t('knowledgeList.detail.sourceTypeAgent') :
                    $t('knowledgeList.detail.sourceTypeKbShare') }}
                </span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ currentSharedKbForDetail.source_from_agent ?
                  $t('knowledgeList.detail.sourceFromAgent') : $t('knowledgeList.detail.sourceOrg') }}</span>
                <span class="shared-detail-value shared-detail-org">
                  <img src="@/assets/img/organization-green.svg" class="shared-detail-org-icon" alt=""
                    aria-hidden="true" />
                  {{ currentSharedKbForDetail.source_from_agent ? currentSharedKbForDetail.source_from_agent.agent_name
                    :
                    currentSharedKbForDetail.org_name }}
                </span>
              </div>
              <div v-if="currentSharedKbForDetail.source_from_agent" class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('knowledgeList.detail.agentKbStrategy') }}</span>
                <span class="shared-detail-value">
                  {{ agentKbStrategyText(currentSharedKbForDetail.source_from_agent?.kb_selection_mode ?? '') }}
                </span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('knowledgeList.detail.sharedAt') }}</span>
                <span class="shared-detail-value">{{ formatStringDate(new Date(currentSharedKbForDetail.shared_at))
                }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('knowledgeList.detail.myPermission') }}</span>
                <t-tag size="small"
                  :theme="currentSharedKbForDetail.permission === 'admin' ? 'primary' : currentSharedKbForDetail.permission === 'editor' ? 'warning' : 'default'">
                  {{ $t(`organization.role.${currentSharedKbForDetail.permission}`) }}
                </t-tag>
              </div>
            </div>
            <div class="shared-detail-drawer-footer">
              <t-button theme="default" variant="outline" @click="closeSharedDetailPanel">{{ $t('common.close')
              }}</t-button>
              <t-button theme="primary" class="go-to-kb-btn" @click="goToSharedKbFromPanel">
                <t-icon name="browse" />
                {{ $t('knowledgeList.detail.goToKb') }}
              </t-button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

    <ContextualGuide tour="kbList" :when="showKbListContextualGuide" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed, watch, nextTick } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { MessagePlugin, Icon as TIcon } from 'tdesign-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { deleteKnowledgeBase, duplicateKnowledgeBase, togglePinKnowledgeBase } from '@/api/knowledge-base'
import { useChatResourcesStore } from '@/stores/chatResources'
import { formatStringDate } from '@/utils/index'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { useOrganizationStore } from '@/stores/organization'
import { listOrganizationSharedKnowledgeBases, type SharedKnowledgeBase, type OrganizationSharedKnowledgeBaseItem, type SourceFromAgentInfo } from '@/api/organization'
import { mergeAllScopeKnowledgeBases, type OwnedKnowledgeBase, type SharedKnowledgeBaseLike } from './kbListMerge'
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue'
import KbWikiBadge from './components/KbWikiBadge.vue'
import ResourceListToolbar from '@/components/ResourceListToolbar.vue'
import { matchesResourceQuery } from '@/utils/resourceListSearch'
import ResourceOriginBadge from '@/components/ResourceOriginBadge.vue'
import { shouldShowResourceOriginBadge } from '@/utils/card-list-badge'
import { permissionCanManageKB } from '@/utils/kbPermission'
import ContextualGuide from '@/components/ContextualGuide.vue'
import ResourceSortControl from '@/components/ResourceSortControl.vue'
import { isContextualGuideDone, markContextualGuideDone } from '@/config/contextualGuides'
import { useTenantModelReadiness } from '@/composables/useTenantModelReadiness'
import { useI18n } from 'vue-i18n'
import { useListUrlState } from '@/composables/useListUrlState'
import { useResourcePins } from '@/composables/useResourcePins'
import {
  DEFAULT_RESOURCE_SORT,
  sortResourcesWithinGroups,
  type ResourceSortAccessors,
  type ResourceSortValue,
} from '@/utils/resourceSorting'

const router = useRouter()
const route = useRoute()
const uiStore = useUIStore()
const authStore = useAuthStore()
const { loaded: modelsReadyLoaded, isReadyForDocumentKb } = useTenantModelReadiness()
const orgStore = useOrganizationStore()
const chatResources = useChatResourcesStore()
const { t } = useI18n()
const selectedResourceSort = ref<ResourceSortValue>(DEFAULT_RESOURCE_SORT)

// 左侧空间选择：默认根据当前角色决定。
// Viewer 在该空间里通常 0 KB owned，"我的"会显示空状态、又把共享 KB 藏起来，
// 体验非常误导；所以 Viewer 默认落到 "all"（我的 + 共享给我都显示）。
// Contributor 及以上一进来主要管理自己创建的 KB，仍默认 "mine"。
//
// State lives in `?scope=` so links are shareable/bookmarkable; the
// composable handles two-way sync with the URL. We keep "mine" as the
// stored value (not "workspace") for back-compat with any external link
// that might point at the old query; ResourceListToolbar labels it as the current workspace.
const defaultScope: 'all' | 'mine' = authStore.hasRole('contributor') ? 'mine' : 'all'
const { scope: spaceSelection, creator: creatorFilter, query: keyword } = useListUrlState({
  defaultScope,
  defaultCreator: 'all',
})

// Per-user favorites + recents (localStorage-backed). isFavorite & touchRecent
// are wired into card render and click handlers below.
const pins = useResourcePins()
const kbFavoritesCount = computed(
  () => pins.favorites.value.filter((e) => e.type === 'kb').length
)
const kbRecentsCount = computed(
  () => pins.recents.value.filter((e) => e.type === 'kb').length
)

interface KB {
  id: string;
  name: string;
  description?: string;
  updated_at?: string;
  created_at?: string;
  pinned_at?: string;
  embedding_model_id?: string;
  summary_model_id?: string;
  type?: 'document' | 'faq';
  showMore?: boolean;
  vlm_config?: { enabled?: boolean; model_id?: string };
  extract_config?: { enabled?: boolean };
  storage_provider_config?: { provider?: string };
  storage_config?: { provider?: string; bucket_name?: string }; // legacy
  question_generation_config?: { enabled?: boolean; question_count?: number };
  knowledge_count?: number;
  chunk_count?: number;
  isProcessing?: boolean;
  processing_count?: number;
  share_count?: number;
  is_pinned?: boolean;
  // creator_id is the owner-id matched against authStore.user.id when
  // gating the per-card more-menu (Settings / Delete). Empty for legacy
  // KBs created before PR 5; those fall back to the role gate.
  creator_id?: string;
  // creator_name 由后端 list 接口回填，仅用于卡片右下角来源徽章的 tooltip。
  creator_name?: string;
}

const flatKnowledgeBaseSortAccessors: ResourceSortAccessors<any> = {
  getName: item => item?.name,
  getUpdatedAt: item => item?.updated_at ?? item?.shared_at,
  getCreatedAt: item => item?.created_at ?? item?.shared_at,
}

const sharedKnowledgeBaseSortAccessors: ResourceSortAccessors<OrganizationSharedKnowledgeBaseItem> = {
  getName: item => item.knowledge_base?.name,
  getUpdatedAt: item => item.knowledge_base?.updated_at ?? item.shared_at,
  getCreatedAt: item => item.knowledge_base?.created_at ?? item.shared_at,
}

const kbs = ref<KB[]>([])
const loading = ref(false)
const confirmDelete = useConfirmDelete()
const currentMoreIndex = ref<number>(-1)
const highlightedKbId = ref<string | null>(null)
const highlightedCardRef = ref<HTMLElement | null>(null)
let uploadRefreshTimer: ReturnType<typeof setTimeout> | null = null

// Shared knowledge bases (everything cross-tenant shared to me, including
// viewer-only). Used by the per-space views and the "all" aggregate so
// readers still see read-only shares — those are valid resources, just
// not editable.
const sharedKbs = computed<SharedKnowledgeBase[]>(() => orgStore.sharedKnowledgeBases || [])

const allKnowledgeBases = computed(() => kbs.value.length + sharedKbs.value.length)

// 当前选中的是空间 ID（非全部、非我的、非收藏/最近这类伪 scope）
// NB: keep the reserved-scope list in sync with ResourceListToolbar's
// non-org buckets — otherwise a new pseudo-scope (e.g. "favorites")
// falls through here and triggers the per-space code paths, which
// renders an extra "no shared KB" empty state on top of the real view.
const RESERVED_SCOPES = new Set(['all', 'mine', 'favorites', 'recents'])
const spaceSelectionOrgId = computed(() => {
  const s = spaceSelection.value
  return !!s && !RESERVED_SCOPES.has(s)
})

// 空间视角：该空间内全部知识库（含我共享的），选中空间时请求新接口
const spaceKbsList = ref<OrganizationSharedKnowledgeBaseItem[]>([])
const spaceKbsLoading = ref(false)

// 「工作空间」固定保持“置顶 → 我创建 → 同事创建”，用户选择只影响组内顺序。
const unsearchedSortedMineKbs = computed<KB[]>(() => {
  return sortResourcesWithinGroups(
    kbs.value,
    selectedResourceSort.value,
    kb => kb.is_pinned ? 'pinned' : isMyKb(kb) ? 'mine' : 'tenantOthers',
    ['pinned', 'mine', 'tenantOthers'],
    flatKnowledgeBaseSortAccessors,
  )
})
const sortedMineKbs = computed(() => unsearchedSortedMineKbs.value.filter(item => matchesResourceQuery(item, keyword.value)))

// 空间视角固定保持“我共享的 → 可编辑 → 仅查看”，用户选择只影响组内顺序。
const unsearchedSortedSpaceKbsList = computed(() => {
  return sortResourcesWithinGroups(
    spaceKbsList.value,
    selectedResourceSort.value,
    item => item.is_mine
      ? 'sharedByMe'
      : isSharedKbEditable(item.permission) ? 'sharedEditable' : 'sharedReadonly',
    ['sharedByMe', 'sharedEditable', 'sharedReadonly'],
    sharedKnowledgeBaseSortAccessors,
  )
})
const sortedSpaceKbsList = computed(() => unsearchedSortedSpaceKbsList.value.filter(item => matchesResourceQuery(item.knowledge_base, keyword.value)))
const spaceCountByOrg = ref<Record<string, number>>({})

// 各空间下的共享知识库数量（用于侧栏展示）：优先用接口返回的该空间总数，否则用「共享给我」数量
const sharedCountByOrg = computed<Record<string, number>>(() => {
  const map: Record<string, number> = {}
  sharedKbs.value.forEach(s => {
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
  Object.keys(spaceCountByOrg.value).forEach(orgId => {
    merged[orgId] = spaceCountByOrg.value[orgId]
  })
  return merged
})

// Favorites / Recents views: hydrate pin entries by id against every KB
// the user can already see in this page (own + cross-tenant shared). KBs
// the user no longer has access to (deleted / share revoked) are dropped
// silently — the pin survives until the next mutation, which keeps the
// composable simple at the cost of harmless ghost entries.
//
// Order:
//   - favorites: most recently starred first (PinEntry.ts desc)
//   - recents: most recently opened first (also ts desc, already sorted)
const kbResourceIndex = computed(() => {
  const map = new Map<string, { kb: any; isMine: boolean; shared?: SharedKnowledgeBase }>()
  for (const kb of kbs.value) {
    map.set(kb.id, { kb, isMine: true })
  }
  for (const shared of sharedKbs.value) {
    if (!shared.knowledge_base) continue
    if (!map.has(shared.knowledge_base.id)) {
      map.set(shared.knowledge_base.id, { kb: shared.knowledge_base, isMine: false, shared })
    }
  }
  return map
})

const favoritesList = computed(() => {
  return pins.favorites.value
    .filter((e) => e.type === 'kb')
    .map((e) => {
      const entry = kbResourceIndex.value.get(e.id)
      if (!entry) return null
      if (entry.isMine) {
        return { ...entry.kb, isMine: true as const, _pinTs: e.ts }
      }
      const s = entry.shared!
      return {
        ...entry.kb,
        isMine: false as const,
        permission: s.permission,
        shared_at: s.shared_at,
        share_id: s.share_id,
        org_name: s.org_name,
        _pinTs: e.ts,
      } as any
    })
    .filter((x): x is NonNullable<typeof x> => x !== null)
})

const recentsList = computed(() => {
  return pins.recents.value
    .filter((e) => e.type === 'kb')
    .map((e) => {
      const entry = kbResourceIndex.value.get(e.id)
      if (!entry) return null
      if (entry.isMine) {
        return { ...entry.kb, isMine: true as const, _pinTs: e.ts }
      }
      const s = entry.shared!
      return {
        ...entry.kb,
        isMine: false as const,
        permission: s.permission,
        shared_at: s.shared_at,
        share_id: s.share_id,
        org_name: s.org_name,
        _pinTs: e.ts,
      } as any
    })
    .filter((x): x is NonNullable<typeof x> => x !== null)
})

// 可编辑权限：editor / admin。viewer 进入「仅查看」组。
// 用 share-level permission（不是空间角色）做判断——跨空间拿到 viewer 的，
// 即便我在本空间是 owner 也确实改不动那个 KB；反过来跨空间拿到 editor 的，
// 哪怕我在本空间是 contributor 也确实能改。
const EDITABLE_PERMS = new Set(['admin', 'editor'])
function isSharedKbEditable(perm: string | undefined): boolean {
  return !!perm && EDITABLE_PERMS.has(perm)
}

// 同空间、非当前用户创建的 KB 分组标题。
// contributor / viewer 在本空间里对这些 KB 没有写权限，所以打"仅查看"；
// admin / owner 反而对整个空间都有编辑权限，"仅查看"会反复误导他们以为
// 自己改不了——这一段实际上是"工作空间里其他成员创建的 KB"，按所有权
// 而非权限来标注更准确。
const tenantSectionLabelKey = computed(() =>
  authStore.hasRole('admin')
    ? 'knowledgeList.sections.tenantOthers'
    : 'knowledgeList.sections.tenantReadonly'
)

// 图标和上面的文案对齐：admin/owner 看到的是"本空间 · 其他成员"，按所有权
// 划分，配 usergroup（多人）更贴；contributor/viewer 看到的是"仅查看"，
// 维持 browse（眼睛）传达"只能看不能改"的语义。
const tenantSectionIconName = computed(() =>
  authStore.hasRole('admin') ? 'usergroup' : 'browse'
)

// 分组折叠：ephemeral，只在当前会话里生效，不落 localStorage/服务器。
// 之所以走"折叠集合"而不是"展开集合"，是因为默认全展开——空 Set
// 即表示初始的全展开状态，避免每次新加分段还得回头维护默认值。
type KbSectionKey = 'pinned' | 'mine' | 'tenantOthers' | 'sharedByMe' | 'sharedEditable' | 'sharedReadonly'
const collapsedKbSections = ref<Set<KbSectionKey>>(new Set())
const isKbSectionCollapsed = (key: KbSectionKey) => collapsedKbSections.value.has(key)
const toggleKbSection = (key: KbSectionKey) => {
  // 重新赋一个新的 Set 是为了让 ref 的 .value 身份变化触发模板重渲染；
  // 直接 .add/.delete 在 Vue 3 的 reactive Set 里也能 work，但 ref(Set) 的
  // 内层代理行为在不同版本上略有差异，整体替换最稳。
  const next = new Set(collapsedKbSections.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  collapsedKbSections.value = next
}
// 判断一条 KB 应该归在哪个分组——和模板里几处 v-if 用的是同一套判定，
// 抽出来是为了 v-show 卡片时复用，避免把 5 个分组的 v-if 重新拼一遍。
//
// 输入有两种形态：
//   1. filteredKnowledgeBases 的元素，会显式带 `isMine` 标志（见
//      filteredKnowledgeBases 里的 spread；跨空间 shared 拆给 isMine=false）。
//   2. sortedMineKbs 的元素就是原始 KB，无 isMine、也无 permission 字段。
// 跨空间共享条目一定带 `permission`，本空间条目永远没有，所以"无 permission"
// 是本空间的安全标识。综合：先看 isMine，再回退到 permission 是否存在。
const kbSectionOf = (kb: any): KbSectionKey => {
  if (kb?.is_pinned) return 'pinned'
  const isOwnTenant = kb?.isMine === true || (kb?.isMine !== false && kb?.permission == null)
  if (isOwnTenant) return isMyKb(kb) ? 'mine' : 'tenantOthers'
  return isSharedKbEditable(kb?.permission) ? 'sharedEditable' : 'sharedReadonly'
}

// 空间筛选视图（sortedSpaceKbsList）的条目结构与上面不同：is_mine 直接标识
// 「我共享出来的」，其余按 permission 走 sharedEditable / sharedReadonly。
const spaceKbSectionOf = (shared: any): KbSectionKey => {
  if (shared?.is_mine) return 'sharedByMe'
  return isSharedKbEditable(shared?.permission) ? 'sharedEditable' : 'sharedReadonly'
}
const isSpaceKbCollapsed = (shared: any): boolean => isKbSectionCollapsed(spaceKbSectionOf(shared))

// 每个分组里实际有多少张卡片——直接把分组判定函数复用一遍。组标题上展示
// "(N)" 让用户一眼知道折叠后会藏掉多少，也方便核对筛选结果。
const emptyKbCounts = (): Record<KbSectionKey, number> => ({
  pinned: 0, mine: 0, tenantOthers: 0, sharedByMe: 0, sharedEditable: 0, sharedReadonly: 0,
})
const filteredKbSectionCounts = computed<Record<KbSectionKey, number>>(() => {
  const c = emptyKbCounts()
  filteredKnowledgeBases.value.forEach(kb => { c[kbSectionOf(kb)]++ })
  return c
})
const mineKbSectionCounts = computed<Record<KbSectionKey, number>>(() => {
  const c = emptyKbCounts()
  sortedMineKbs.value.forEach(kb => { c[kbSectionOf(kb)]++ })
  return c
})
const spaceKbSectionCounts = computed<Record<KbSectionKey, number>>(() => {
  const c = emptyKbCounts()
  sortedSpaceKbsList.value.forEach(shared => { c[spaceKbSectionOf(shared)]++ })
  return c
})

// 收藏、最近、全部和工作空间都使用同一排序选项，同时保持原有分组顺序。
const unsearchedFilteredKnowledgeBases = computed(() => {
  if (spaceSelection.value === 'favorites') {
    return sortResourcesWithinGroups(
      favoritesList.value,
      selectedResourceSort.value,
      kbSectionOf,
      ['pinned', 'mine', 'tenantOthers', 'sharedEditable', 'sharedReadonly'],
      flatKnowledgeBaseSortAccessors,
    )
  }
  if (spaceSelection.value === 'recents') {
    return sortResourcesWithinGroups(
      recentsList.value,
      selectedResourceSort.value,
      kbSectionOf,
      ['pinned', 'mine', 'tenantOthers', 'sharedEditable', 'sharedReadonly'],
      flatKnowledgeBaseSortAccessors,
    )
  }
  if (spaceSelection.value === 'mine') {
    return sortedMineKbs.value.map(kb => ({ ...kb, isMine: true as const }))
  }
  if (spaceSelection.value !== 'all') {
    return []
  }
  // The "All" scope merges own + shared KBs. The card template keys each
  // row by `kb.id`, so the same KB surfacing twice — owned *and* shared
  // back, or shared into the caller's view through two different orgs —
  // produced duplicate `v-for` keys and blanked the list once there were
  // ≥2 entries (#795). mergeAllScopeKnowledgeBases de-duplicates by KB id
  // (owned wins; most-privileged share kept) while preserving the existing
  // pinned → mine → teammate → shared(editable-first) ordering.
  const merged = mergeAllScopeKnowledgeBases(
    kbs.value as unknown as OwnedKnowledgeBase[],
    sharedKbs.value as unknown as SharedKnowledgeBaseLike[],
    authStore.user?.id,
  ) as unknown as Array<(KB & { isMine: true }) | (SharedKnowledgeBase['knowledge_base'] & { isMine: false; permission: string; shared_at: string; share_id: string } & any)>
  return sortResourcesWithinGroups(
    merged,
    selectedResourceSort.value,
    kbSectionOf,
    ['pinned', 'mine', 'tenantOthers', 'sharedEditable', 'sharedReadonly'],
    flatKnowledgeBaseSortAccessors,
  )
})
const filteredKnowledgeBases = computed(() => unsearchedFilteredKnowledgeBases.value.filter(item => matchesResourceQuery(item, keyword.value)))

const showKbListEmpty = computed(() => {
  if (loading.value || keyword.value.trim()) return false
  if (!authStore.hasRole('contributor')) return false
  if (spaceSelection.value === 'all' && filteredKnowledgeBases.value.length === 0) return true
  if (spaceSelection.value === 'mine' && kbs.value.length === 0) return true
  return false
})

const showKbListContextualGuide = computed(
  () => showKbListEmpty.value && !uiStore.showKBEditorModal,
)

const applyKbListData = (data: any[]) => {
  kbs.value = data.map((kb: any) => ({
    ...kb,
    updated_at: kb.updated_at ? formatStringDate(new Date(kb.updated_at)) : '',
    showMore: false,
    isProcessing: kb.is_processing || false,
    processing_count: kb.processing_count || 0
  }))
}

const fetchList = (force = false) => {
  loading.value = true
  // The creator filter only applies to the caller's own tenant KBs (the
  // first call). Shared KBs are inherently "not mine" so we don't filter
  // them server-side; the segmented control is also hidden whenever the
  // user is browsing the shared / per-space scopes.
  return Promise.all([
    chatResources.fetchKnowledgeBasesForList({ creator: creatorFilter.value }, force).then(applyKbListData),
    orgStore.fetchSharedKnowledgeBases({ force }),
    orgStore.fetchOrganizations({ force }),
  ]).finally(() => { loading.value = false }).then(() => {
    // 各空间知识库数量已由 GET /organizations 的 resource_counts 带回，存于 orgStore.resourceCounts
    const counts = orgStore.resourceCounts?.knowledge_bases?.by_organization
    if (counts) spaceCountByOrg.value = { ...counts }
  })
}

// 选中空间时请求该空间内全部知识库（含我共享的）
watch(spaceSelection, (val) => {
  // Stale URL guard: an older "协作" view used scope=shared; that view
  // was removed, so normalize back to "all" instead of letting the
  // value fall through to the per-space fetch branch (which would 404
  // on the string "shared").
  if (val === 'shared') {
    spaceSelection.value = 'all'
    return
  }
  if (val === 'all' || val === 'mine' || val === 'favorites' || val === 'recents' || !val) {
    spaceKbsList.value = []
    return
  }
  spaceKbsLoading.value = true
  listOrganizationSharedKnowledgeBases(val).then((res) => {
    if (res.success && res.data) {
      spaceKbsList.value = res.data
      spaceCountByOrg.value = { ...spaceCountByOrg.value, [val]: res.data.length }
    } else {
      spaceKbsList.value = []
    }
  }).finally(() => {
    spaceKbsLoading.value = false
  })
}, { immediate: true })

// Refetch when the creator filter flips. We re-pull the whole list rather
// than filtering in-memory so the server stays the single source of truth
// (and we don't need to worry about stale share_count or pagination later).
watch(creatorFilter, () => {
  fetchList(true)
})

onMounted(() => {
  fetchList().then(() => {
    // 检查路由参数中是否有需要高亮的知识库ID
    const highlightKbId = route.query.highlightKbId as string
    if (highlightKbId) {
      triggerHighlightFlash(highlightKbId)
      // Drop the transient highlight param but preserve other state
      // (scope / creator / q) so refreshing doesn't reset the user's view.
      const { highlightKbId: _drop, ...rest } = route.query
      router.replace({ query: rest })
    }
  })

  window.addEventListener('knowledgeFileUploaded', handleUploadFinishedEvent as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploaded', handleUploadFinishedEvent as EventListener)

  if (uploadRefreshTimer) {
    clearTimeout(uploadRefreshTimer)
    uploadRefreshTimer = null
  }
})

// 监听路由变化，处理从其他页面跳转过来的高亮需求
watch(() => route.query.highlightKbId, (newKbId) => {
  if (newKbId && typeof newKbId === 'string' && kbs.value.length > 0) {
    triggerHighlightFlash(newKbId)
    const { highlightKbId: _drop, ...rest } = route.query
    router.replace({ query: rest })
  }
})

const openMore = (index: number) => {
  // 只记录当前打开的索引，用于显示激活样式
  // 弹窗的开关由 v-model 自动管理
  currentMoreIndex.value = index
}

const onVisibleChange = (visible: boolean) => {
  // 弹窗关闭时重置索引
  if (!visible) {
    currentMoreIndex.value = -1
  }
}

const handleSettings = (kb: KB) => {
  // 手动关闭弹窗
  kb.showMore = false
  goSettings(kb.id)
}

// canManageKBCard mirrors KnowledgeBase.vue's `canManage`, gating the
// destructive items of the per-card menu — Settings, Delete — so a
// Viewer cannot click into them for a KB they don't own. The server
// still rejects the call (PR 5 guards every such mutation with
// OwnedKBOrAdmin) but the UI shouldn't surface buttons the user has
// no authority to use.
//
// The pin item is intentionally NOT gated by this predicate any more:
// pin state is per (user, kb) as of migration 000050 and the backend
// route only requires KB read access, so anyone who can see the card
// should be able to pin it for themselves.
//
// Legacy KBs created before PR 5 have an empty creator_id; treat
// those as tenant-owned (Admin+ may manage) so existing KBs aren't
// suddenly unmanageable for everyone.
function canManageKBCard(kb: KB): boolean {
  // Shared-space cards carry the org-share permission; when it exists it is
  // the only signal that counts. A read-only (viewer) or editor share must
  // not surface Settings/Delete even when the browsing user is an admin of
  // their own personal workspace — the backend 3-D permission cap would 403
  // the call anyway (#3098).
  const sharePermission = (kb as any).permission as string | undefined
  if (sharePermission) return permissionCanManageKB(sharePermission)
  // Shared-card shapes that lost their permission field (pin/recents merges)
  // are marked isMine === false; they must not fall through to the local
  // admin/creator fallbacks either.
  if ((kb as any).isMine === false) return false
  const userId = authStore.user?.id || ''
  if (kb.creator_id && userId && kb.creator_id === userId) return true
  return authStore.hasRole('admin')
}

function canDuplicateKBCard(kb: any): boolean {
  return authStore.hasRole('contributor') && kb.isMine !== false
}

// isMyKb 仅用于卡片右下角徽章在「我创建」与「同空间其他成员创建」之间切换。
// 与 canManageKBCard 不同：管理权限有 admin 兜底，徽章纯粹按创建者匹配。
// creator_id 为空（PR 5 RBAC 迁移之前的老 KB）一律按 tenant 处理——避免把
// 全空间共有的旧 KB 错误地都标成「我创建」。
function isMyKb(kb: { creator_id?: string }): boolean {
  const userId = authStore.user?.id || ''
  return !!(kb.creator_id && userId && kb.creator_id === userId)
}

// kbOriginVariant 决定卡片右下角徽章的展示形态：
//   - 我自己创建的：mine（绿色 "我创建"）
//   - 同空间他人创建的：creator 变体——只显示创建者名字。用户始终在
//     某个工作空间内浏览（顶部 TenantSelector 已经标了空间身份），右下
//     角再贴一遍空间名属于重复信息；contributor / admin / owner / viewer
//     看到的徽章一致。创建者无法解析时，creator 变体自动回退到
//     resourceOrigin.tenant 文案（"本空间"），不会出现空标签。
function kbOriginVariant(kb: { creator_id?: string }): 'mine' | 'creator' {
  return isMyKb(kb) ? 'mine' : 'creator'
}

function showKbOriginBadge(kb: { creator_id?: string; creator_name?: string }): boolean {
  return shouldShowResourceOriginBadge({
    section: kbSectionOf(kb),
    variant: kbOriginVariant(kb),
    creatorName: kb.creator_name,
    showSectionHeaders: true,
  })
}

// 通过 ID 处理设置（用于全部 Tab 下的知识库）
const handleSettingsById = (id: string) => {
  goSettings(id)
}

// 通过 ID 处理删除（用于全部 Tab 下的知识库）
const handleDeleteById = (id: string) => {
  const kb = kbs.value.find(k => k.id === id)
  if (kb) requestDelete(kb)
}

const handleTogglePin = async (kb: KB) => {
  kb.showMore = false
  try {
    const res: any = await togglePinKnowledgeBase(kb.id)
    if (res.success) {
      MessagePlugin.success(
        res.data.is_pinned ? t('knowledgeList.pin.pinSuccess') : t('knowledgeList.pin.unpinSuccess')
      )
      fetchList(true)
    }
  } catch {
    MessagePlugin.error(t('knowledgeList.pin.failed'))
  }
}

const handleTogglePinById = async (id: string) => {
  try {
    const res: any = await togglePinKnowledgeBase(id)
    if (res.success) {
      MessagePlugin.success(
        res.data.is_pinned ? t('knowledgeList.pin.pinSuccess') : t('knowledgeList.pin.unpinSuccess')
      )
      fetchList(true)
    }
  } catch {
    MessagePlugin.error(t('knowledgeList.pin.failed'))
  }
}

const handleDuplicate = async (kb: KB) => {
  kb.showMore = false
  await duplicateKB(kb.id)
}

const handleDuplicateById = async (id: string) => {
  await duplicateKB(id)
}

const duplicateKB = async (id: string) => {
  try {
    const res: any = await duplicateKnowledgeBase(id)
    if (res?.success) {
      const newKbId = res.data?.target_id || res.data?.knowledge_base?.id
      MessagePlugin.success(t('knowledgeList.messages.duplicateSuccess'))
      await fetchList(true)
      if (newKbId) {
        triggerHighlightFlash(newKbId)
      }
    } else {
      MessagePlugin.error(res?.message || t('knowledgeList.messages.duplicateFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeList.messages.duplicateFailed'))
  }
}

const handleSharedKbClick = (sharedKb: SharedKnowledgeBase) => {
  pins.touchRecent('kb', sharedKb.knowledge_base.id)
  // 跳转到共享知识库详情页
  router.push(`/platform/knowledge-bases/${sharedKb.knowledge_base.id}`)
}

// 处理"全部"Tab 中的共享知识库卡片点击（直接进入知识库）
const handleSharedKbClickFromAll = (kb: any) => {
  pins.touchRecent('kb', kb.id)
  router.push(`/platform/knowledge-bases/${kb.id}`)
}

// 右侧详情面板：共享知识库详情（含直接共享与来自智能体的）
type SharedKbDetailItem = SharedKnowledgeBase & { is_mine?: boolean; source_from_agent?: SourceFromAgentInfo }
const sharedDetailPanelVisible = ref(false)
const currentSharedKbForDetail = ref<SharedKbDetailItem | null>(null)

const closeSharedDetailPanel = () => {
  sharedDetailPanelVisible.value = false
  currentSharedKbForDetail.value = null
}

// 打开右侧详情面板（全部 Tab 共享卡片）
const openSharedDetailFromAll = (kb: any) => {
  const sharedKb = sharedKbs.value.find(s => s.knowledge_base.id === kb.id)
  if (sharedKb) {
    currentSharedKbForDetail.value = sharedKb
    sharedDetailPanelVisible.value = true
  }
}

// 打开右侧详情面板（空间 Tab：直接共享或来自智能体）
const openSharedDetail = (sharedKb: SharedKbDetailItem) => {
  currentSharedKbForDetail.value = sharedKb
  sharedDetailPanelVisible.value = true
}

// 智能体对知识库的策略文案（用于抽屉「来源方式」为智能体时）
const agentKbStrategyText = (mode: string) => {
  if (mode === 'all') return t('knowledgeList.detail.agentKbStrategyAll')
  if (mode === 'selected') return t('knowledgeList.detail.agentKbStrategySelected')
  return t('knowledgeList.detail.agentKbStrategyNone')
}

// 从右侧面板进入知识库
const goToSharedKbFromPanel = () => {
  if (currentSharedKbForDetail.value) {
    router.push(`/platform/knowledge-bases/${currentSharedKbForDetail.value.knowledge_base.id}`)
    closeSharedDetailPanel()
  }
}

const handleDelete = (kb: KB) => {
  // 手动关闭弹窗
  kb.showMore = false
  requestDelete(kb)
}

const requestDelete = (kb: KB) => {
  confirmDelete({
    title: t('knowledgeList.delete.confirmTitle'),
    body: t('knowledgeList.delete.confirmMessage', { name: kb.name }),
    onConfirm: async () => {
      try {
        const res: any = await deleteKnowledgeBase(kb.id)
        if (res.success) {
          MessagePlugin.success(t('knowledgeList.messages.deleted'))
          fetchList(true)
        } else {
          MessagePlugin.error(res.message || t('knowledgeList.messages.deleteFailed'))
        }
      } catch (e: any) {
        MessagePlugin.error(e?.message || t('knowledgeList.messages.deleteFailed'))
      }
    },
  })
}

const isInitialized = (kb: KB) => {
  // LLM (summary) model is always required
  if (!kb.summary_model_id || kb.summary_model_id === '') return false
  // Embedding model only required when RAG indexing is enabled (vector or keyword)
  const strategy = (kb as any).indexing_strategy
  const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled
  if (needsEmbedding && (!kb.embedding_model_id || kb.embedding_model_id === '')) return false
  return true
}

const isWikiKb = (kb: unknown) =>
  !!(kb as { indexing_strategy?: { wiki_enabled?: boolean } } | null | undefined)?.indexing_strategy?.wiki_enabled

// 计算是否有未初始化的知识库
const hasUninitializedKbs = computed(() => {
  return kbs.value.some(kb => !isInitialized(kb))
})

const handleCardClick = (kb: KB) => {
  // Track this open in the per-user "recent" list before navigating —
  // matches the user mental model "this is what I last worked on".
  pins.touchRecent('kb', kb.id)
  if (isInitialized(kb)) {
    goDetail(kb.id)
  } else {
    goSettings(kb.id)
  }
}

// toggleFavoriteKb is the click handler for the star icon rendered on
// each card. Stops propagation so it doesn't bubble into the card's
// own @click which would open the KB.
const toggleFavoriteKb = (kbId: string, evt?: Event) => {
  evt?.stopPropagation()
  pins.toggleFavorite('kb', kbId)
}
const isKbFavorited = (kbId: string) => pins.isFavorite('kb', kbId)

const goDetail = (id: string) => {
  router.push(`/platform/knowledge-bases/${id}`)
}

const goSettings = (id: string) => {
  // 使用模态框打开设置
  uiStore.openKBSettings(id)
}

// 创建知识库
const handleCreateKnowledgeBase = () => {
  markContextualGuideDone('kbList')
  // 无模型时仍打开创建向导，并定位到模型配置页；用户可在向导内添加模型，无需先跳转系统设置
  const initialSection =
    modelsReadyLoaded.value && !isReadyForDocumentKb.value ? 'models' : undefined
  uiStore.openCreateKB('document', initialSection)
}

// 知识库编辑器成功回调（创建或编辑成功）
const handleKBEditorSuccess = (kbId: string) => {
  console.log('[KnowledgeBaseList] knowledge operation success:', kbId)
  const shouldOpenDetailForUploadGuide = !isContextualGuideDone('kbDetail')
  // 列表页编辑同样要让单 KB 详情缓存失效，否则侧栏 / 详情页 60s 内仍显示旧信息
  chatResources.invalidateKnowledgeBaseDetail(kbId)
  fetchList(true).then(() => {
    if (shouldOpenDetailForUploadGuide && kbId && !uiStore.showKBEditorModal) {
      goDetail(kbId)
    }
    // 如果是从路由参数中获取的高亮ID，触发闪烁效果
    if (route.query.highlightKbId === kbId) {
      triggerHighlightFlash(kbId)
      const { highlightKbId: _drop, ...rest } = route.query
      router.replace({ query: rest })
    }
  })
}

// 触发高亮闪烁效果
const triggerHighlightFlash = (kbId: string) => {
  highlightedKbId.value = kbId
  nextTick(() => {
    if (highlightedCardRef.value) {
      // 滚动到高亮的卡片
      highlightedCardRef.value.scrollIntoView({
        behavior: 'smooth',
        block: 'center'
      })
    }
    // 3秒后清除高亮
    setTimeout(() => {
      highlightedKbId.value = null
    }, 3000)
  })
}

const handleUploadFinishedEvent = (event: Event) => {
  const detail = (event as CustomEvent<{ kbId?: string | number; settled?: boolean }>).detail
  if (!detail?.kbId) return
  // Counts only need the batch's final refresh; a forced reload every couple
  // of seconds mid-batch would also close any open card menu.
  if (detail.settled === false) return
  if (uploadRefreshTimer) {
    clearTimeout(uploadRefreshTimer)
  }
  uploadRefreshTimer = setTimeout(() => {
    fetchList(true)
    uploadRefreshTimer = null
  }, 800)
}
const visibleResultCount = computed(() => spaceSelection.value === 'mine' ? sortedMineKbs.value.length : spaceSelectionOrgId.value ? sortedSpaceKbsList.value.length : filteredKnowledgeBases.value.length)
// A new search reveals matching rows even if their group was previously collapsed.
watch(keyword, () => { collapsedKbSections.value = new Set() })
</script>

<style scoped lang="less">
@import (reference) '@/components/css/resource-card.less';

.kb-list-container {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: 100%;
  display: flex;
}

.kb-list-content { .resource-list-content(); }

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

.kb-list-main { .resource-list-main(); }

.kb-list-main-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  padding: 12px;
  background: var(--td-bg-color-container);
}

.header-subtitle {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: var(--app-text-base);
  font-weight: 400;
  line-height: 20px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
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

  :deep(.t-icon),
  :deep(.btn-icon-wrapper) {
    color: var(--td-brand-color);
  }
}

// 来源组织（空间图标 + 空间名）
.org-source {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
  border-radius: var(--app-radius-sm);
  font-size: var(--app-text-sm);
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  max-width: 140px;
  transition: background-color var(--app-motion-fast) ease;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 500;
  }

  .org-source-icon {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
    vertical-align: middle;
  }

  .t-icon {
    color: var(--td-brand-color);
    flex-shrink: 0;
  }
}

.shared-kb-card {
  position: relative;

  .org-tag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: var(--app-text-sm);
    border-color: color-mix(in srgb, var(--td-brand-color) 15%, transparent);
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 4%, transparent);
    font-weight: 500;
    padding: 2px 8px;
    border-radius: var(--app-radius-xs);
    max-width: fit-content;
  }
}

.warning-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  margin-bottom: 20px;
  background: var(--td-warning-color-light);
  border: 1px solid var(--td-warning-color-focus);
  border-radius: var(--app-radius-sm);
  color: var(--td-warning-color);
  font-family: var(--app-font-family);
  font-size: var(--app-text-base);

  .t-icon {
    color: var(--td-warning-color);
    flex-shrink: 0;
  }
}

.kb-card-wrap {
  .resource-card-grid();
}

.kb-section-header {
  .resource-section-header();
}

.kb-card {
  .resource-card();

  &.uninitialized {
    opacity: 0.9;
  }

  .kb-favorite-star { .resource-favorite-button(); }

}

/* 三个列表卡片统一：描述字体 */
.bottom-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.bottom-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;

  .card-time {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
  }
}

.feature-badge {
  .resource-feature-badge();

  &.type-document {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    width: auto;
    padding: 0 6px;
    gap: 3px;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    .badge-count {
      font-size: var(--app-text-xs);
      font-weight: 500;
    }

    .processing-icon {
      animation: wk-spin 1s linear infinite;
    }
  }

  &.type-faq {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    width: auto;
    padding: 0 6px;
    gap: 3px;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    .badge-count {
      font-size: var(--app-text-xs);
      font-weight: 500;
    }

    .processing-icon {
      animation: wk-spin 1s linear infinite;
    }
  }

  &.kg {
    background: color-mix(in srgb, var(--app-accent-purple) 8%, transparent);
    color: var(--td-brand-color);

    &:hover {
      background: color-mix(in srgb, var(--app-accent-purple) 12%, transparent);
    }
  }

  &.multimodal {
    background: color-mix(in srgb, var(--td-warning-color) 8%, transparent);
    color: var(--td-warning-color);

    &:hover {
      background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    }
  }

  &.question {
    background: color-mix(in srgb, var(--td-success-color) 8%, transparent);
    color: var(--td-success-color);

    &:hover {
      background: color-mix(in srgb, var(--td-success-color) 12%, transparent);
    }
  }

  &.shared {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);

    &:hover {
      background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    }
  }

  &.role-admin {
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color-active);

    &:hover {
      background: color-mix(in srgb, var(--td-brand-color) 15%, transparent);
    }
  }

  &.role-editor {
    background: color-mix(in srgb, var(--td-warning-color) 10%, transparent);
    color: var(--td-warning-color);

    &:hover {
      background: color-mix(in srgb, var(--td-warning-color) 15%, transparent);
    }
  }

  &.role-viewer {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-secondary);

    &:hover {
      background: var(--td-bg-color-component);
    }
  }
}

@keyframes highlightFlash {
  0% {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 0 color-mix(in srgb, var(--td-brand-color) 40%, transparent);
    transform: scale(1);
  }

  50% {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 8px color-mix(in srgb, var(--td-brand-color) 0%, transparent);
    transform: scale(1.02);
  }

  100% {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 0 color-mix(in srgb, var(--td-brand-color) 0%, transparent);
    transform: scale(1);
  }
}

.kb-card.highlight-flash {
  animation: highlightFlash 0.6s ease-in-out 3;
  border-color: var(--td-brand-color) !important;
  box-shadow: 0 0 12px color-mix(in srgb, var(--td-brand-color) 30%, transparent) !important;
}

// 删除确认对话框样式
:deep(.t-dialog__position.t-dialog--top) {
  padding-top: 40vh !important;
}

.resource-list-header();

</style>

<style lang="less">
/* 下拉菜单样式已统一至 @/assets/dropdown-menu.less */

// 共享知识库卡片：详情触发（替代三点，用「查看详情」链接样式）
.shared-detail-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-brand-color);
  font-size: var(--app-text-md);
  font-family: var(--app-font-family);
  cursor: pointer;
  transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;

  .t-icon {
    flex-shrink: 0;
  }

  &:hover {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);
  }
}

// 右侧滑出：共享知识库详情面板
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

  &.shared-detail-source-type {
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

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
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  background: var(--td-bg-color-container);

  .go-to-kb-btn .t-button__text {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
}

// 右侧滑入动画
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
