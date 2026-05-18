<template>
  <div class="kb-list-container">
    <ListSpaceSidebar v-if="!authStore.isLiteMode" v-model="spaceSelection" :count-all="allKnowledgeBases"
      :count-mine="kbs.length" :count-by-org="effectiveSharedCountByOrg" :count-favorites="kbFavoritesCount"
      :count-recents="kbRecentsCount" />
    <div class="kb-list-content">
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <div class="title-row" style="--wails-draggable: drag">
            <h2 style="--wails-draggable: drag">{{ $t('knowledgeBase.title') }}</h2>
            <t-tooltip v-if="authStore.hasRole('contributor')" :content="$t('knowledgeList.create')" placement="bottom">
              <t-button variant="text" theme="default" size="small" class="header-action-btn"
                style="--wails-draggable: no-drag" @click="handleCreateKnowledgeBase">
                <template #icon><t-icon name="folder-add" size="16px" /></template>
              </t-button>
            </t-tooltip>
          </div>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('knowledgeList.subtitle') }}</p>
        </div>
      </div>
      <div class="kb-list-main">
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

        <!-- 上传进度提示 -->
        <div v-if="uploadSummaries.length" class="upload-progress-panel">
          <div v-for="summary in uploadSummaries" :key="summary.kbId" class="upload-progress-item">
            <div class="upload-progress-icon">
              <t-icon :name="summary.completed === summary.total ? 'check-circle-filled' : 'upload'" size="20px" />
            </div>
            <div class="upload-progress-content">
              <div class="progress-title">
                {{
                  summary.completed === summary.total
                    ? $t('knowledgeList.uploadProgress.completedTitle', { name: summary.kbName })
                    : $t('knowledgeList.uploadProgress.uploadingTitle', { name: summary.kbName })
                }}
              </div>
              <div class="progress-subtitle">
                {{
                  summary.completed === summary.total
                    ? $t('knowledgeList.uploadProgress.completedDetail', { total: summary.total })
                    : $t('knowledgeList.uploadProgress.detail', { completed: summary.completed, total: summary.total })
                }}
              </div>
              <div class="progress-subtitle secondary">
                {{
                  summary.completed === summary.total
                    ? $t('knowledgeList.uploadProgress.refreshing')
                    : $t('knowledgeList.uploadProgress.keepPageOpen')
                }}
              </div>
              <div v-if="summary.hasError" class="progress-subtitle error">
                {{ $t('knowledgeList.uploadProgress.errorTip') }}
              </div>
              <div class="progress-bar">
                <div class="progress-bar-inner" :style="{ width: summary.progress + '%' }"></div>
              </div>
            </div>
          </div>
        </div>

        <!-- 骨架屏占位 -->
        <div v-if="loading && kbs.length === 0" class="kb-card-wrap">
          <div v-for="n in 6" :key="'skel-' + n" class="kb-card kb-card-skeleton">
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
            @click="toggleKbSection('pinned')"
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
            <!-- 我创建的：第一张「我创建」非置顶卡片前打标题，统一展示
                 不管上方是否存在「已置顶」段。与「本空间 · 仅查看」同样
                 仅在 contributor 视图下出现——admin/owner 视图原本就没有
                 任何分段标题，单独冒一个反而失衡。 -->
            <div v-if="showShareGroupHeaders
              && kb.isMine
              && isMyKb(kb as KB)
              && !kb.is_pinned
              && (index === 0
                || (filteredKnowledgeBases[index - 1] as any).is_pinned)" class="kb-section-header" role="button"
              tabindex="0" @click="toggleKbSection('mine')"
              @keydown.enter.prevent="toggleKbSection('mine')"
              @keydown.space.prevent="toggleKbSection('mine')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('knowledgeList.sections.mine') }}</span>
              <span class="kb-section-count">{{ filteredKbSectionCounts.mine }}</span>
              <t-icon class="kb-section-toggle" :name="isKbSectionCollapsed('mine') ? 'chevron-right' : 'chevron-down'"
                size="14px" />
            </div>
            <!-- 本空间 · 仅查看：本租户里同事创建、对当前 contributor 不可编辑。
                 当前卡片必须是非置顶（否则归在「已置顶」），且前一张要么
                 不存在、要么是「共享给我」、要么是我创建、要么是置顶卡片
                 （置顶→非置顶的过渡同样要打这个标题）。 -->
            <div v-if="showShareGroupHeaders
              && kb.isMine
              && !isMyKb(kb as KB)
              && !kb.is_pinned
              && (index === 0
                || !filteredKnowledgeBases[index - 1].isMine
                || isMyKb(filteredKnowledgeBases[index - 1] as KB)
                || (filteredKnowledgeBases[index - 1] as any).is_pinned)" class="kb-section-header" role="button"
              tabindex="0" @click="toggleKbSection('tenantOthers')"
              @keydown.enter.prevent="toggleKbSection('tenantOthers')"
              @keydown.space.prevent="toggleKbSection('tenantOthers')">
              <t-icon :name="tenantSectionIconName" size="14px" />
              <span>{{ $t(tenantSectionLabelKey) }}</span>
              <span class="kb-section-count">{{ filteredKbSectionCounts.tenantOthers }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('tenantOthers') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：从「我的（含同事）」首次过渡到共享 + 可编辑 -->
            <div v-if="showShareGroupHeaders
              && !kb.isMine
              && isSharedKbEditable((kb as any).permission)
              && (index === 0 || filteredKnowledgeBases[index - 1].isMine)" class="kb-section-header" role="button"
              tabindex="0" @click="toggleKbSection('sharedEditable')"
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
            <div v-if="showShareGroupHeaders
              && !kb.isMine
              && !isSharedKbEditable((kb as any).permission)
              && (index === 0
                || filteredKnowledgeBases[index - 1].isMine
                || isSharedKbEditable((filteredKnowledgeBases[index - 1] as any).permission))"
              class="kb-section-header" role="button" tabindex="0" @click="toggleKbSection('sharedReadonly')"
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
              @click="handleCardClick(kb)">
              <!-- 收藏按钮：右上角浮动；通过 .card-header 的 padding-right
                   给「更多」按钮腾出空间，避免两个按钮叠在一起。 -->
              <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isKbFavorited(kb.id) }"
                @click.stop="toggleFavoriteKb(kb.id, $event)">
                <t-icon :name="isKbFavorited(kb.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="kb.name">{{ kb.name }}</span>
                <!-- The card menu always exists when the card is visible: pin
                     is now per-user and available to anyone who can see the KB
                     (backend route only requires KB read access). Settings /
                     Delete are mutations, so they stay behind canManageKBCard. -->
                <t-popup overlayClassName="card-more-popup" trigger="click" destroy-on-close
                  placement="bottom-right">
                  <div class="more-wrap" @click.stop>
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </div>
                  <template #content>
                    <div class="popup-menu" @click.stop>
                      <div class="popup-menu-item" @click.stop="handleTogglePinById(kb.id)">
                        <t-icon class="menu-icon" :name="kb.is_pinned ? 'pin-filled' : 'pin'" />
                        <span>{{ kb.is_pinned ? $t('knowledgeList.pin.unpin') : $t('knowledgeList.pin.pin') }}</span>
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
                <div class="card-description">
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
                        <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
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
                <div v-if="!authStore.isLiteMode" class="bottom-right">
                  <ResourceOriginBadge :variant="kbOriginVariant(kb)" :creator-name="kb.creator_name" />
                </div>
              </div>
            </div>

            <!-- 共享知识库卡片 -->
            <div v-else v-show="!isKbSectionCollapsed(kbSectionOf(kb))" class="kb-card shared-kb-card" :class="{
              'kb-type-document': (kb.type || 'document') === 'document',
              'kb-type-faq': kb.type === 'faq'
            }" @click="handleSharedKbClickFromAll(kb)">
              <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isKbFavorited(kb.id) }"
                @click.stop="toggleFavoriteKb(kb.id, $event)">
                <t-icon :name="isKbFavorited(kb.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="kb.name">{{ kb.name }}</span>
                <t-tooltip :content="$t('knowledgeList.menu.viewDetails')" placement="top">
                  <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetailFromAll(kb)"
                    :aria-label="$t('knowledgeList.menu.viewDetails')">
                    <t-icon name="info-circle" size="16px" />
                  </button>
                </t-tooltip>
              </div>

              <!-- 卡片内容 -->
              <div class="card-content">
                <div class="card-description">
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
                        <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
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
            role="button" tabindex="0" @click="toggleKbSection('pinned')"
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
            <div v-if="showShareGroupHeaders
              && isMyKb(kb)
              && !kb.is_pinned
              && (index === 0 || sortedMineKbs[index - 1].is_pinned)" class="kb-section-header" role="button"
              tabindex="0" @click="toggleKbSection('mine')"
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
            <div v-if="showShareGroupHeaders
              && !isMyKb(kb)
              && !kb.is_pinned
              && (index === 0
                || isMyKb(sortedMineKbs[index - 1])
                || sortedMineKbs[index - 1].is_pinned)" class="kb-section-header" role="button" tabindex="0"
              @click="toggleKbSection('tenantOthers')"
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
              @click="handleCardClick(kb)">
              <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isKbFavorited(kb.id) }"
                @click.stop="toggleFavoriteKb(kb.id, $event)">
                <t-icon :name="isKbFavorited(kb.id) ? 'star-filled' : 'star'" size="14px" />
              </button>
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="kb.name">{{ kb.name }}</span>
                <!-- See the matching block in the "all" tab template for why
                     this is no longer gated by canManageKBCard. -->
                <t-popup v-model="kb.showMore" overlayClassName="card-more-popup"
                  :on-visible-change="onVisibleChange" trigger="click" destroy-on-close placement="bottom-right">
                  <div variant="outline" class="more-wrap" @click.stop="openMore(index)"
                    :class="{ 'active-more': currentMoreIndex === index }">
                    <img class="more-icon" src="@/assets/img/more.png" alt="" />
                  </div>
                  <template #content>
                    <div class="popup-menu" @click.stop>
                      <div class="popup-menu-item" @click.stop="handleTogglePin(kb)">
                        <t-icon class="menu-icon" :name="kb.is_pinned ? 'pin-filled' : 'pin'" />
                        <span>{{ kb.is_pinned ? $t('knowledgeList.pin.unpin') : $t('knowledgeList.pin.pin') }}</span>
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
                <div class="card-description">
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
                        <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
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
                <div v-if="!authStore.isLiteMode" class="bottom-right">
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
            <div v-if="showShareGroupHeaders && shared.is_mine && index === 0" class="kb-section-header"
              role="button" tabindex="0" @click="toggleKbSection('sharedByMe')"
              @keydown.enter.prevent="toggleKbSection('sharedByMe')"
              @keydown.space.prevent="toggleKbSection('sharedByMe')">
              <t-icon name="share" size="14px" />
              <span>{{ $t('knowledgeList.sections.sharedByMe') }}</span>
              <span class="kb-section-count">{{ spaceKbSectionCounts.sharedByMe }}</span>
              <t-icon class="kb-section-toggle"
                :name="isKbSectionCollapsed('sharedByMe') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 共享给我 · 可编辑：从「我的」首次进入「共享 + 可编辑」 -->
            <div v-if="showShareGroupHeaders
              && !shared.is_mine
              && isSharedKbEditable(shared.permission)
              && (index === 0 || sortedSpaceKbsList[index - 1].is_mine)" class="kb-section-header"
              role="button" tabindex="0" @click="toggleKbSection('sharedEditable')"
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
            <div v-if="showShareGroupHeaders
              && !shared.is_mine
              && !isSharedKbEditable(shared.permission)
              && (index === 0
                || sortedSpaceKbsList[index - 1].is_mine
                || isSharedKbEditable(sortedSpaceKbsList[index - 1].permission))" class="kb-section-header"
              role="button" tabindex="0" @click="toggleKbSection('sharedReadonly')"
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
            }" @click="handleSharedKbClick(shared)">
              <!-- 卡片头部 -->
              <div class="card-header">
                <span class="card-title" :title="shared.knowledge_base.name">{{ shared.knowledge_base.name }}</span>
                <t-tooltip v-if="!shared.is_mine" :content="$t('knowledgeList.menu.viewDetails')" placement="top">
                  <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetail(shared)"
                    :aria-label="$t('knowledgeList.menu.viewDetails')">
                    <t-icon name="info-circle" size="16px" />
                  </button>
                </t-tooltip>
              </div>

              <!-- 卡片内容 -->
              <div class="card-content">
                <div class="card-description">
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
                        <t-icon :name="shared.knowledge_base.type === 'faq' ? 'chat-bubble-help' : 'folder'"
                          size="14px" />
                        <span class="badge-count">{{ shared.knowledge_base.type === 'faq' ?
                          (shared.knowledge_base.chunk_count ??
                            '-') : (shared.knowledge_base.knowledge_count ?? '-') }}</span>
                      </div>
                    </t-tooltip>
                  </div>
                </div>
                <div class="bottom-right">
                  <t-tooltip :content="shared.org_name" placement="top">
                    <div class="org-source">
                      <img src="@/assets/img/organization-green.svg" class="org-source-icon" alt=""
                        aria-hidden="true" />
                      <span>{{ shared.org_name }}</span>
                    </div>
                  </t-tooltip>
                </div>
              </div>
            </div>
          </template>
        </div>

        <!-- 全部空状态：保留「新建知识库」CTA，因为是租户没有任何 KB 的真空场景 -->
        <div v-if="spaceSelection === 'all' && filteredKnowledgeBases.length === 0 && !loading" class="empty-state">
          <img class="empty-img" src="@/assets/img/upload.svg" alt="">
          <span class="empty-txt">{{ $t('knowledgeList.empty.title') }}</span>
          <span class="empty-desc">{{ $t('knowledgeList.empty.description') }}</span>
          <t-button v-if="authStore.hasRole('contributor')" class="kb-create-btn empty-state-btn"
            @click="handleCreateKnowledgeBase">
            <template #icon><t-icon name="folder-add" /></template>
            {{ $t('knowledgeList.create') }}
          </t-button>
        </div>

        <!-- 收藏空状态：不放创建按钮——「没有收藏」 ≠ 「没有知识库」，
             正确引导是「去星标一下」，不是「再建一个」。 -->
        <div v-if="spaceSelection === 'favorites' && filteredKnowledgeBases.length === 0 && !loading"
          class="empty-state">
          <t-icon name="star" size="48px" class="empty-icon" />
          <span class="empty-txt">{{ $t('knowledgeList.empty.favoritesTitle') }}</span>
          <span class="empty-desc">{{ $t('knowledgeList.empty.favoritesDescription') }}</span>
        </div>

        <!-- 最近空状态：同理，引导是「去打开一个」。 -->
        <div v-if="spaceSelection === 'recents' && filteredKnowledgeBases.length === 0 && !loading" class="empty-state">
          <t-icon name="history" size="48px" class="empty-icon" />
          <span class="empty-txt">{{ $t('knowledgeList.empty.recentsTitle') }}</span>
          <span class="empty-desc">{{ $t('knowledgeList.empty.recentsDescription') }}</span>
        </div>

        <!-- 我的知识库空状态 -->
        <div v-if="spaceSelection === 'mine' && kbs.length === 0 && !loading" class="empty-state">
          <img class="empty-img" src="@/assets/img/upload.svg" alt="">
          <span class="empty-txt">{{ $t('knowledgeList.empty.title') }}</span>
          <span class="empty-desc">{{ $t('knowledgeList.empty.description') }}</span>
          <t-button v-if="authStore.hasRole('contributor')" class="kb-create-btn empty-state-btn"
            @click="handleCreateKnowledgeBase">
            <template #icon><t-icon name="folder-add" /></template>
            {{ $t('knowledgeList.create') }}
          </t-button>
        </div>

        <!-- 空间下知识库空状态 -->
        <div v-if="spaceSelectionOrgId && !spaceKbsLoading && spaceKbsList.length === 0" class="empty-state">
          <img class="empty-img" src="@/assets/img/upload.svg" alt="">
          <span class="empty-txt">{{ $t('knowledgeList.empty.sharedTitle') }}</span>
          <span class="empty-desc">{{ $t('knowledgeList.empty.sharedDescription') }}</span>
        </div>
      </div>
    </div>

    <!-- 删除确认对话框 -->
    <t-dialog v-model:visible="deleteVisible" dialogClassName="del-knowledge-dialog" :closeBtn="false" :cancelBtn="null"
      :confirmBtn="null">
      <div class="circle-wrap">
        <div class="dialog-header">
          <img class="circle-img" src="@/assets/img/circle.png" alt="">
          <span class="circle-title">{{ $t('knowledgeList.delete.confirmTitle') }}</span>
        </div>
        <span class="del-circle-txt">
          {{ $t('knowledgeList.delete.confirmMessage', { name: deletingKb?.name ?? '' }) }}
        </span>
        <div class="circle-btn">
          <span class="circle-btn-txt" @click="deleteVisible = false">{{ $t('common.cancel') }}</span>
          <span class="circle-btn-txt confirm" @click="confirmDelete">{{ $t('knowledgeList.delete.confirmButton')
          }}</span>
        </div>
      </div>
    </t-dialog>

    <!-- 知识库编辑器（创建/编辑统一组件） -->
    <KnowledgeBaseEditorModal :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
      :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
      @update:visible="(val) => val ? null : uiStore.closeKBEditor()" @success="handleKBEditorSuccess" />

    <!-- 共享知识库对话框 -->
    <ShareKnowledgeBaseDialog v-model:visible="shareDialogVisible" :knowledge-base-id="sharingKbId"
      :knowledge-base-name="sharingKbName" @shared="handleShareSuccess" />

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

  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed, watch, nextTick } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { MessagePlugin, Icon as TIcon } from 'tdesign-vue-next'
import { listKnowledgeBases, deleteKnowledgeBase, togglePinKnowledgeBase } from '@/api/knowledge-base'
import { formatStringDate } from '@/utils/index'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { useOrganizationStore } from '@/stores/organization'
import { listOrganizationSharedKnowledgeBases, type SharedKnowledgeBase, type OrganizationSharedKnowledgeBaseItem, type SourceFromAgentInfo } from '@/api/organization'
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue'
import ShareKnowledgeBaseDialog from '@/components/ShareKnowledgeBaseDialog.vue'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'
import ResourceOriginBadge from '@/components/ResourceOriginBadge.vue'
import { useI18n } from 'vue-i18n'
import { useListUrlState } from '@/composables/useListUrlState'
import { useResourcePins } from '@/composables/useResourcePins'

const router = useRouter()
const route = useRoute()
const uiStore = useUIStore()
const authStore = useAuthStore()
const orgStore = useOrganizationStore()
const { t } = useI18n()

// 左侧空间选择：默认根据当前角色决定。
// Viewer 在该租户里通常 0 KB owned，"我的"会显示空状态、又把共享 KB 藏起来，
// 体验非常误导；所以 Viewer 默认落到 "all"（我的 + 共享给我都显示）。
// Contributor 及以上一进来主要管理自己创建的 KB，仍默认 "mine"。
//
// State lives in `?scope=` so links are shareable/bookmarkable; the
// composable handles two-way sync with the URL. We keep "mine" as the
// stored value (not "workspace") for back-compat with any external link
// that might point at the old query — its display label is rebranded
// via ListSpaceSidebar's workspaceLabel computed.
const defaultScope: 'all' | 'mine' = authStore.hasRole('contributor') ? 'mine' : 'all'
const { scope: spaceSelection, creator: creatorFilter } = useListUrlState({
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

const kbs = ref<KB[]>([])
const loading = ref(false)
const deleteVisible = ref(false)
const deletingKb = ref<KB | null>(null)
const currentMoreIndex = ref<number>(-1)
const highlightedKbId = ref<string | null>(null)
const highlightedCardRef = ref<HTMLElement | null>(null)
const uploadTasks = ref<UploadTaskState[]>([])
const uploadCleanupTimers = new Map<string, ReturnType<typeof setTimeout>>()
let uploadRefreshTimer: ReturnType<typeof setTimeout> | null = null
const UPLOAD_CLEANUP_DELAY = 10000

// Share dialog state
const shareDialogVisible = ref(false)
const sharingKbId = ref('')
const sharingKbName = ref('')

// Shared knowledge bases (everything cross-tenant shared to me, including
// viewer-only). Used by the per-space views and the "all" aggregate so
// readers still see read-only shares — those are valid resources, just
// not editable.
const sharedKbs = computed<SharedKnowledgeBase[]>(() => orgStore.sharedKnowledgeBases || [])

const allKnowledgeBases = computed(() => kbs.value.length + sharedKbs.value.length)

// 当前选中的是空间 ID（非全部、非我的、非收藏/最近这类伪 scope）
// NB: keep the reserved-scope list in sync with ListSpaceSidebar's
// non-org buckets — otherwise a new pseudo-scope (e.g. "favorites")
// falls through here and triggers the per-space code paths, which
// renders an extra "no shared KB" empty state on top of the real view.
const RESERVED_SCOPES = new Set(['all', 'mine', 'favorites', 'recents'])
const spaceSelectionOrgId = computed(() => {
  const s = spaceSelection.value
  return !!s && !RESERVED_SCOPES.has(s)
})

// 当前空间下共享给我的知识库（旧：仅他人共享；保留用于兼容）
const sharedKbsByOrg = computed(() => {
  const orgId = spaceSelection.value
  if (orgId === 'all' || orgId === 'mine') return []
  return sharedKbs.value.filter(s => s.organization_id === orgId)
})

// 空间视角：该空间内全部知识库（含我共享的），选中空间时请求新接口
const spaceKbsList = ref<OrganizationSharedKnowledgeBaseItem[]>([])
const spaceKbsLoading = ref(false)

// 「工作空间」视图下的稳定排序：本租户内「我创建」在前、「同事创建」在后；
// 子段内保留服务端的置顶优先顺序。给 contributor 视图把「本空间 · 仅查看」
// 分组标题正好插在过渡处；其他角色看不到标题，纯排序变化也无害。
// Ordering for the 「本空间」 tab:
//   1. pinned KBs (mine or teammate), newest pin first
//   2. my non-pinned KBs
//   3. teammate non-pinned KBs (rendered under the「本空间 · 仅查看」header)
//
// Pin is per-user as of migration 000050, so a teammate-created KB that
// the caller has personally pinned must float into the pinned section
// even though it would otherwise live in the teammate sub-group. The
// previous version only bucketed by isMyKb and silently demoted these
// pinned-but-teammate KBs.
const sortedMineKbs = computed<KB[]>(() => {
  return [...kbs.value].sort((a, b) => {
    const ap = a.is_pinned ? 0 : 1
    const bp = b.is_pinned ? 0 : 1
    if (ap !== bp) return ap - bp
    if (a.is_pinned && b.is_pinned) {
      const at = a.pinned_at ? Date.parse(a.pinned_at as string) : 0
      const bt = b.pinned_at ? Date.parse(b.pinned_at as string) : 0
      if (at !== bt) return bt - at
    }
    const am = isMyKb(a) ? 0 : 1
    const bm = isMyKb(b) ? 0 : 1
    if (am !== bm) return am - bm
    const ac = a.created_at ? Date.parse(a.created_at as string) : 0
    const bc = b.created_at ? Date.parse(b.created_at as string) : 0
    return bc - ac
  })
})

// 空间视角下的稳定排序：我创建的（is_mine）放在前面，剩下的共享部分再按
// 可编辑 / 仅查看 排序——这样空间列表跟「全部」视图的视觉顺序一致。
const sortedSpaceKbsList = computed(() => {
  return [...spaceKbsList.value].sort((a, b) => {
    const aMine = a.is_mine ? 0 : 1
    const bMine = b.is_mine ? 0 : 1
    if (aMine !== bMine) return aMine - bMine
    const aE = isSharedKbEditable(a.permission) ? 0 : 1
    const bE = isSharedKbEditable(b.permission) ? 0 : 1
    return aE - bE
  })
})
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
// 用 share-level permission（不是租户角色）做判断——跨租户拿到 viewer 的，
// 即便我在本租户是 owner 也确实改不动那个 KB；反过来跨租户拿到 editor 的，
// 哪怕我在本租户是 contributor 也确实能改。
const EDITABLE_PERMS = new Set(['admin', 'editor'])
function isSharedKbEditable(perm: string | undefined): boolean {
  return !!perm && EDITABLE_PERMS.has(perm)
}

// 是否在共享区展示「可编辑 / 仅查看」二级分组：仅对中间档（contributor / editor）
// 有意义。viewer 反正都是只读，admin / owner 视角统一管理，分组反而碎。
// 这里只是 UI 呈现，权限由后端兜底，不要把它当成安全边界。
// 分组标题对所有角色生效——置顶 / 我创建的 / 本空间 · 仅查看 / 共享给我
// 都是基于"创建者 + 来源"的客观信息，不依赖当前用户的可写权限。
// 原本只对 contributor 显示是为了在 admin/owner 那里隐藏"仅查看"这个权限
// 暗示——但实际上 admin/owner 也会想区分自己创建 vs 同事创建的卡片，所以
// 现在统一打开。如果哪天需要把权限色彩从标题里拿掉，就改 i18n 文案即可，
// 不需要再回头碰这个 computed。
const showShareGroupHeaders = computed(() => true)

// 同租户、非当前用户创建的 KB 分组标题。
// contributor / viewer 在本租户里对这些 KB 没有写权限，所以打"仅查看"；
// admin / owner 反而对整个租户都有编辑权限，"仅查看"会反复误导他们以为
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
//      filteredKnowledgeBases 里的 spread；跨租户 shared 拆给 isMine=false）。
//   2. sortedMineKbs 的元素就是原始 KB，无 isMine、也无 permission 字段。
// 跨租户共享条目一定带 `permission`，本租户条目永远没有，所以"无 permission"
// 是本租户的安全标识。综合：先看 isMine，再回退到 permission 是否存在。
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

// Filtered knowledge bases: 全部 = 我的 + 全部共享；我的 = 仅我的
//
// Favorites / Recents reuse the same render path as `all` — they're just
// pre-filtered, pre-ordered slices, so the existing kb-card / shared
// kb-card templates render them with zero extra markup. Order is
// preserved via the upstream array (pins order is ts-desc).
const filteredKnowledgeBases = computed(() => {
  if (spaceSelection.value === 'favorites') {
    return favoritesList.value
  }
  if (spaceSelection.value === 'recents') {
    return recentsList.value
  }
  if (spaceSelection.value === 'mine') {
    return kbs.value.map(kb => ({ ...kb, isMine: true as const }))
  }
  if (spaceSelection.value !== 'all') {
    return []
  }
  const result: Array<(KB & { isMine: true }) | (SharedKnowledgeBase['knowledge_base'] & { isMine: false; permission: string; shared_at: string; share_id: string } & any)> = []
  // 本租户的 KB 分三段渲染：①任何人创建但被当前用户置顶的→「已置顶」组；
  // ②我创建的非置顶；③同事创建的非置顶（contributor 视图下挂在「本空间 ·
  // 仅查看」标题下）。置顶现在是 per-user 维度，必须跨创建者优先级地上浮，
  // 否则同事创建但我置顶的 KB 会被错误地沉到底部。
  const pinned: KB[] = []
  const ownMine: KB[] = []
  const teammateMine: KB[] = []
  kbs.value.forEach(kb => {
    if (kb.is_pinned) pinned.push(kb)
    else if (isMyKb(kb)) ownMine.push(kb)
    else teammateMine.push(kb)
  })
  pinned.sort((a, b) => {
    const at = a.pinned_at ? Date.parse(a.pinned_at as string) : 0
    const bt = b.pinned_at ? Date.parse(b.pinned_at as string) : 0
    return bt - at
  })
  pinned.forEach(kb => result.push({ ...kb, isMine: true as const }))
  ownMine.forEach(kb => result.push({ ...kb, isMine: true as const }))
  teammateMine.forEach(kb => result.push({ ...kb, isMine: true as const }))
  // 共享区按 permission 排序：可编辑（admin/editor）在前，仅查看（viewer）在后。
  // 即便当前角色不显示分组标题，排序也保留——展示更可预测，并且让分组开关切换
  // 时不会引起卡片顺序跳变。
  const sortedShared = [...sharedKbs.value].sort((a, b) => {
    const aE = isSharedKbEditable(a.permission) ? 0 : 1
    const bE = isSharedKbEditable(b.permission) ? 0 : 1
    return aE - bE
  })
  sortedShared.forEach(shared => {
    const kb = shared.knowledge_base
    if (!kb) return
    result.push({
      ...kb,
      isMine: false as const,
      permission: shared.permission,
      shared_at: shared.shared_at,
      share_id: shared.share_id,
      org_name: shared.org_name,
      knowledge_count: kb.knowledge_count,
      chunk_count: kb.chunk_count,
    } as any)
  })
  return result
})

interface UploadTaskState {
  uploadId: string
  kbId: string
  fileName?: string
  progress: number
  status: 'uploading' | 'success' | 'error'
  error?: string
}

interface UploadSummary {
  kbId: string
  kbName: string
  total: number
  completed: number
  progress: number
  hasError: boolean
}

const fetchList = () => {
  loading.value = true
  // The creator filter only applies to the caller's own tenant KBs (the
  // first call). Shared KBs are inherently "not mine" so we don't filter
  // them server-side; the segmented control is also hidden whenever the
  // user is browsing the shared / per-space scopes.
  return Promise.all([
    listKnowledgeBases({ creator: creatorFilter.value }).then((res: any) => {
      const data = res.data || []
      // 格式化时间，并初始化 showMore 状态
      // is_processing 字段由后端返回
      kbs.value = data.map((kb: any) => ({
        ...kb,
        updated_at: kb.updated_at ? formatStringDate(new Date(kb.updated_at)) : '',
        showMore: false,
        isProcessing: kb.is_processing || false,
        processing_count: kb.processing_count || 0
      }))
    }),
    orgStore.fetchSharedKnowledgeBases(),
    orgStore.fetchOrganizations()
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
  fetchList()
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

  window.addEventListener('knowledgeFileUploadStart', handleUploadStartEvent as EventListener)
  window.addEventListener('knowledgeFileUploadProgress', handleUploadProgressEvent as EventListener)
  window.addEventListener('knowledgeFileUploadComplete', handleUploadCompleteEvent as EventListener)
  window.addEventListener('knowledgeFileUploaded', handleUploadFinishedEvent as EventListener)
})

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploadStart', handleUploadStartEvent as EventListener)
  window.removeEventListener('knowledgeFileUploadProgress', handleUploadProgressEvent as EventListener)
  window.removeEventListener('knowledgeFileUploadComplete', handleUploadCompleteEvent as EventListener)
  window.removeEventListener('knowledgeFileUploaded', handleUploadFinishedEvent as EventListener)

  uploadCleanupTimers.forEach(timer => clearTimeout(timer))
  uploadCleanupTimers.clear()
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
  const userId = authStore.user?.id || ''
  if (kb.creator_id && userId && kb.creator_id === userId) return true
  return authStore.hasRole('admin')
}

// isMyKb 仅用于卡片右下角徽章在「我创建」与「同租户其他成员创建」之间切换。
// 与 canManageKBCard 不同：管理权限有 admin 兜底，徽章纯粹按创建者匹配。
// creator_id 为空（PR 5 RBAC 迁移之前的老 KB）一律按 tenant 处理——避免把
// 全租户共有的旧 KB 错误地都标成「我创建」。
function isMyKb(kb: { creator_id?: string }): boolean {
  const userId = authStore.user?.id || ''
  return !!(kb.creator_id && userId && kb.creator_id === userId)
}

// kbOriginVariant 决定卡片右下角徽章的展示形态：
//   - 我自己创建的：mine（绿色 "我创建"）
//   - 同租户他人创建的：creator 变体——只显示创建者名字。用户始终在
//     某个工作空间内浏览（顶部 TenantSelector 已经标了租户身份），右下
//     角再贴一遍租户名属于重复信息；contributor / admin / owner / viewer
//     看到的徽章一致。创建者无法解析时，creator 变体自动回退到
//     resourceOrigin.tenant 文案（"本空间"），不会出现空标签。
function kbOriginVariant(kb: { creator_id?: string }): 'mine' | 'creator' {
  return isMyKb(kb) ? 'mine' : 'creator'
}

// 通过 ID 处理设置（用于全部 Tab 下的知识库）
const handleSettingsById = (id: string) => {
  goSettings(id)
}

// 通过 ID 处理删除（用于全部 Tab 下的知识库）
const handleDeleteById = (id: string) => {
  const kb = kbs.value.find(k => k.id === id)
  if (kb) {
    deletingKb.value = kb
    deleteVisible.value = true
  }
}

const handleTogglePin = async (kb: KB) => {
  kb.showMore = false
  try {
    const res: any = await togglePinKnowledgeBase(kb.id)
    if (res.success) {
      MessagePlugin.success(
        res.data.is_pinned ? t('knowledgeList.pin.pinSuccess') : t('knowledgeList.pin.unpinSuccess')
      )
      fetchList()
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
      fetchList()
    }
  } catch {
    MessagePlugin.error(t('knowledgeList.pin.failed'))
  }
}

const handleShare = (kb: KB) => {
  // 手动关闭弹窗
  kb.showMore = false
  sharingKbId.value = kb.id
  sharingKbName.value = kb.name
  shareDialogVisible.value = true
}

const handleShareSuccess = () => {
  // 共享成功后可刷新列表
  fetchList()
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
  deletingKb.value = kb
  deleteVisible.value = true
}

const confirmDelete = () => {
  if (!deletingKb.value) return

  deleteKnowledgeBase(deletingKb.value.id).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(t('knowledgeList.messages.deleted'))
      deleteVisible.value = false
      deletingKb.value = null
      fetchList()
    } else {
      MessagePlugin.error(res.message || t('knowledgeList.messages.deleteFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('knowledgeList.messages.deleteFailed'))
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

// 计算是否有未初始化的知识库
const hasUninitializedKbs = computed(() => {
  return kbs.value.some(kb => !isInitialized(kb))
})

const getKbDisplayName = (kbId: string) => {
  const target = kbs.value.find(kb => kb.id === kbId)
  if (target?.name) return target.name
  return t('knowledgeList.uploadProgress.unknownKb', { id: kbId }) as string
}

const uploadSummaries = computed<UploadSummary[]>(() => {
  if (!uploadTasks.value.length) return []
  const grouped: Record<string, UploadTaskState[]> = {}
  uploadTasks.value.forEach(task => {
    const kbKey = String(task.kbId)
    if (!grouped[kbKey]) grouped[kbKey] = []
    grouped[kbKey].push(task)
  })
  return Object.entries(grouped).map(([kbId, tasks]) => {
    const total = tasks.length
    const completed = tasks.filter(task => task.status !== 'uploading').length
    const progressSum = tasks.reduce((sum, task) => sum + (task.progress ?? 0), 0)
    const avgProgress = total === 0 ? 0 : Math.min(100, Math.max(0, Math.round(progressSum / total)))
    const hasError = tasks.some(task => task.status === 'error')
    return {
      kbId,
      kbName: getKbDisplayName(kbId),
      total,
      completed,
      progress: avgProgress,
      hasError
    }
  }).sort((a, b) => a.kbName.localeCompare(b.kbName))
})

const clampProgress = (value: number) => Math.min(100, Math.max(0, Math.round(value)))

const addUploadTask = (task: UploadTaskState) => {
  uploadTasks.value = [
    ...uploadTasks.value.filter(item => item.uploadId !== task.uploadId),
    task,
  ]
}

const patchUploadTask = (uploadId: string, patch: Partial<UploadTaskState>) => {
  const index = uploadTasks.value.findIndex(task => task.uploadId === uploadId)
  if (index === -1) return
  const nextTasks = [...uploadTasks.value]
  nextTasks[index] = { ...nextTasks[index], ...patch }
  uploadTasks.value = nextTasks
}

const removeUploadTask = (uploadId: string) => {
  uploadTasks.value = uploadTasks.value.filter(task => task.uploadId !== uploadId)
  const timer = uploadCleanupTimers.get(uploadId)
  if (timer) {
    clearTimeout(timer)
    uploadCleanupTimers.delete(uploadId)
  }
}

const scheduleUploadTaskCleanup = (uploadId: string) => {
  const existing = uploadCleanupTimers.get(uploadId)
  if (existing) {
    clearTimeout(existing)
  }
  const timer = setTimeout(() => {
    removeUploadTask(uploadId)
  }, UPLOAD_CLEANUP_DELAY)
  uploadCleanupTimers.set(uploadId, timer)
}

type UploadEventDetail = {
  uploadId: string
  kbId?: string | number
  fileName?: string
  progress?: number
  status?: UploadTaskState['status']
  error?: string
}

const ensureUploadTaskEntry = (detail?: UploadEventDetail) => {
  if (!detail?.uploadId) return null
  const existing = uploadTasks.value.find(task => task.uploadId === detail.uploadId)
  if (existing) return existing
  if (!detail.kbId) return null
  const initialProgress = typeof detail.progress === 'number' ? clampProgress(detail.progress) : 0
  const newTask: UploadTaskState = {
    uploadId: detail.uploadId,
    kbId: String(detail.kbId),
    fileName: detail.fileName,
    progress: initialProgress,
    status: detail.status || 'uploading',
    error: detail.error
  }
  addUploadTask(newTask)
  return newTask
}

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
  uiStore.openCreateKB()
}

// 知识库编辑器成功回调（创建或编辑成功）
const handleKBEditorSuccess = (kbId: string) => {
  console.log('[KnowledgeBaseList] knowledge operation success:', kbId)
  fetchList().then(() => {
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

const handleUploadStartEvent = (event: Event) => {
  const detail = (event as CustomEvent<UploadEventDetail>).detail
  if (!detail?.uploadId || !detail?.kbId) return
  addUploadTask({
    uploadId: detail.uploadId,
    kbId: String(detail.kbId),
    fileName: detail.fileName,
    progress: typeof detail.progress === 'number' ? clampProgress(detail.progress) : 0,
    status: 'uploading'
  })
}

const handleUploadProgressEvent = (event: Event) => {
  const detail = (event as CustomEvent<UploadEventDetail>).detail
  if (!detail?.uploadId || typeof detail.progress !== 'number') return
  if (!ensureUploadTaskEntry(detail)) return
  patchUploadTask(detail.uploadId, {
    progress: clampProgress(detail.progress)
  })
}

const handleUploadCompleteEvent = (event: Event) => {
  const detail = (event as CustomEvent<UploadEventDetail>).detail
  if (!detail?.uploadId) return
  const progress = typeof detail.progress === 'number'
    ? clampProgress(detail.progress)
    : 100
  if (!ensureUploadTaskEntry({ ...detail, progress })) return
  patchUploadTask(detail.uploadId, {
    status: detail.status || 'success',
    progress,
    error: detail.error
  })
  scheduleUploadTaskCleanup(detail.uploadId)
}

const handleUploadFinishedEvent = (event: Event) => {
  const detail = (event as CustomEvent<{ kbId?: string | number }>).detail
  if (!detail?.kbId) return
  if (uploadRefreshTimer) {
    clearTimeout(uploadRefreshTimer)
  }
  uploadRefreshTimer = setTimeout(() => {
    fetchList()
    uploadRefreshTimer = null
  }, 800)
}
</script>

<style scoped lang="less">
.kb-list-container {
  margin: 0 16px 0 0;
  height: 100%;
  box-sizing: border-box;
  flex: 1;
  display: flex;
  position: relative;
  min-height: 0;
}

.kb-list-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 20px 28px 0 28px;
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

.kb-create-btn {
  background: linear-gradient(135deg, var(--td-brand-color) 0%, #00a67e 100%);
  border: none;
  color: var(--td-text-color-anti);

  &:hover {
    background: linear-gradient(135deg, var(--td-brand-color) 0%, var(--td-brand-color-active) 100%);
  }
}

.kb-list-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  // 顶部不留 padding，sticky 的分组标题 (top: 0) 才能贴到容器最顶；
  // 底部 padding 保留，避免最后一行卡片紧贴边。
  padding: 0 0 8px;
}

.kb-list-main-loading {
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

  :deep(.t-icon),
  :deep(.btn-icon-wrapper) {
    color: var(--td-brand-color);
  }
}

// Tab 切换样式（已由左侧菜单替代，保留以备兼容）
.kb-tabs {
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
    user-select: none;
    position: relative;
    transition: color 0.2s ease;

    &:hover {
      color: var(--td-text-color-primary);
    }

    &.active {
      color: var(--td-brand-color);
      font-weight: 500;

      &::after {
        content: '';
        position: absolute;
        bottom: -1px;
        left: 0;
        right: 0;
        height: 2px;
        background: var(--td-brand-color);
        border-radius: 1px;
      }
    }
  }
}


// 共享知识库卡片样式
// 共享标识（文档类型默认绿色，位置贴右上角）
.shared-badge {
  position: absolute;
  top: 8px;
  right: 14px;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  background: rgba(7, 192, 95, 0.1);
  border-radius: 4px;
  font-size: 12px;
  color: var(--td-brand-color);
  font-weight: 500;

  .t-icon {
    color: var(--td-brand-color);
  }
}

// 来源组织（空间图标 + 空间名）
.org-source {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  background: rgba(7, 192, 95, 0.06);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  max-width: 140px;
  transition: background-color 0.15s ease;

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

// 「我的」知识库标签（与 .org-source 同套样式：灰字 + 绿标 + 浅绿底）
.personal-source {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  background: rgba(7, 192, 95, 0.06);
  border-radius: 6px;
  font-size: 11px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  font-weight: 500;
  transition: background-color 0.15s ease;

  span {
    font-weight: 500;
  }

  .t-icon {
    color: var(--td-brand-color);
    flex-shrink: 0;
  }
}

.shared-kb-card {
  position: relative;

  // 共享知识库根据类型显示不同样式
  &.kb-type-document {
    background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(7, 192, 95, 0.04) 100%) !important;

    &:hover {
      border-color: var(--td-brand-color) !important;
      box-shadow: 0 4px 12px rgba(7, 192, 95, 0.12) !important;
      background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(7, 192, 95, 0.08) 100%) !important;
    }

    &::after {
      background: linear-gradient(135deg, rgba(7, 192, 95, 0.08) 0%, transparent 100%) !important;
    }
  }

  &.kb-type-faq {
    background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(0, 82, 217, 0.04) 100%) !important;

    &:hover {
      border-color: var(--td-brand-color) !important;
      box-shadow: 0 4px 12px rgba(0, 82, 217, 0.12) !important;
      background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(0, 82, 217, 0.08) 100%) !important;
    }

    &::after {
      background: linear-gradient(135deg, rgba(0, 82, 217, 0.08) 0%, transparent 100%) !important;
    }

    // FAQ 类型共享标识使用蓝色
    .shared-badge {
      background: rgba(0, 82, 217, 0.1);
      color: var(--td-brand-color);

      .t-icon {
        color: var(--td-brand-color);
      }
    }
  }

  .org-tag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    border-color: rgba(0, 82, 217, 0.15);
    color: var(--td-brand-color);
    background: rgba(0, 82, 217, 0.04);
    font-weight: 500;
    padding: 2px 8px;
    border-radius: 4px;
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
  border-radius: 6px;
  color: var(--td-warning-color);
  font-family: var(--app-font-family);
  font-size: 14px;

  .t-icon {
    color: var(--td-warning-color);
    flex-shrink: 0;
  }
}

.upload-progress-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 20px;
}

.upload-progress-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
}

.upload-progress-icon {
  color: var(--td-brand-color);
  display: flex;
  align-items: center;
  justify-content: center;
}

.upload-progress-content {
  flex: 1;
}

.progress-title {
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 600;
  line-height: 22px;
  margin-bottom: 2px;
}

.progress-subtitle {
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 12px;
  line-height: 18px;
}

.progress-subtitle.secondary {
  color: var(--td-text-color-placeholder);
  margin-top: 2px;
}

.progress-subtitle.error {
  color: var(--td-error-color);
  margin-top: 4px;
}

.progress-bar {
  width: 100%;
  height: 6px;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
  margin-top: 10px;
  overflow: hidden;
}

.progress-bar-inner {
  height: 100%;
  background: linear-gradient(90deg, var(--td-brand-color-active) 0%, var(--td-brand-color) 100%);
  transition: width 0.2s ease;
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

.kb-card-wrap {
  display: grid;
  gap: 12px;
  grid-template-columns: 1fr;
  animation: contentFadeIn 0.32s ease-out;
}

.kb-section-header {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  gap: 6px;
  // 整行只用来铺背景实现 sticky；点击事件靠子元素冒泡触发，避免点到
  // 标题右侧大片空白时误折叠。键盘 tab/enter 不受 pointer-events 影响。
  pointer-events: none;

  & > * {
    pointer-events: auto;
  }
  // 下滑时吸顶到滚动容器（.kb-list-main）顶部。z-index 要高于卡片自身的
  // hover 阴影 / 装饰层；背景必须不透明，否则卡片会从下方透出来。
  position: sticky;
  top: 0;
  z-index: 5;
  background: var(--td-bg-color-container);
  // 用 box-shadow 把背景再往上"延伸"8px，封掉 sticky 与容器顶之间任何
  // subpixel 残缝（border-radius 的圆角三角、滚动时浏览器子像素渲染等
  // 都会让卡片从这里漏出 1-2px）。第二条 shadow 在下方也再补一点，避免
  // grid-gap 区域里卡片穿插过来。
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

  // Icons inherit the section header's text color so the whole row
  // (icon + label) reads as one muted secondary tone. The pinned
  // modifier no longer overrides this either — uniform appearance
  // is intentional; the icon shape alone is enough to flag which
  // section the user is looking at.
  .t-icon {
    color: inherit;
  }

  .kb-section-toggle {
    margin-left: 4px;
    opacity: 0.7;
    transition: opacity 0.15s ease;
  }

  // 共享给我的两个子分组共用一个主图标 usergroup-add，再用子图标
  // (edit / browse) 区分权限。子图标向左挤靠主图标，整体读起来还是一个"组"。
  .kb-section-subicon {
    margin-left: -4px;
    opacity: 0.75;
  }

  // 组里实际有多少张卡片。用 13px 主字号同色降透明度，避免抢标题视觉，
  // 同时给个轻底色保证在浅色容器上仍可读。
  .kb-section-count {
    margin-left: 2px;
    padding: 0 6px;
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: 11px;
    line-height: 16px;
    font-weight: 500;
  }

  &:hover .kb-section-toggle {
    opacity: 1;
  }
}

.kb-card {
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

  &.kb-card-skeleton {
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

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 4px 12px rgba(7, 192, 95, 0.12);
  }

  &.uninitialized {
    opacity: 0.9;
  }

  // 文档类型样式
  &.kb-type-document {
    background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(7, 192, 95, 0.04) 100%);

    &:hover {
      border-color: var(--td-brand-color);
      background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(7, 192, 95, 0.08) 100%);
    }

    // 右上角装饰
    &::after {
      content: '';
      position: absolute;
      top: 0;
      right: 0;
      width: 60px;
      height: 60px;
      background: linear-gradient(135deg, rgba(7, 192, 95, 0.08) 0%, transparent 100%);
      border-radius: 0 12px 0 100%;
      pointer-events: none;
      z-index: 0;
    }
  }

  // 问答类型样式
  &.kb-type-faq {
    background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(0, 82, 217, 0.04) 100%);

    &:hover {
      border-color: var(--td-brand-color);
      box-shadow: 0 4px 12px rgba(0, 82, 217, 0.12);
      background: linear-gradient(135deg, var(--td-bg-color-container) 0%, rgba(0, 82, 217, 0.08) 100%);
    }

    // 右上角装饰
    &::after {
      content: '';
      position: absolute;
      top: 0;
      right: 0;
      width: 60px;
      height: 60px;
      background: linear-gradient(135deg, rgba(0, 82, 217, 0.08) 0%, transparent 100%);
      border-radius: 0 12px 0 100%;
      pointer-events: none;
      z-index: 0;
    }
  }

  .kb-favorite-star {
    // 浮在卡片右上角顶角。卡片自身有 padding，"更多"按钮在 header flex 末端
    // 自然落在 padding 内部，与零位的 star 错开一段距离。
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

  // Reveal the star on card hover; favorited state forces it visible.
  &:hover .kb-favorite-star {
    opacity: 1;
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

  .card-more-btn {
    width: 28px;
    height: 28px;
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 4px;
  margin-bottom: 6px;

  .card-title {
    flex: 1;
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    letter-spacing: 0.01em;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    display: flex;
    align-items: center;
    gap: 5px;
  }

  .card-more-btn {
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 6px;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    transition: all 0.2s;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-secondary);
    }
  }

  .permission-tag {
    flex-shrink: 0;
  }
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

.more-wrap {
  display: flex;
  width: 24px;
  height: 24px;
  justify-content: center;
  align-items: center;
  border-radius: 6px;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.2s ease;
  opacity: 0;

  .kb-card:hover & {
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
    width: 14px;
    height: 14px;
  }
}

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
  flex: 1;
  min-width: 0;
}

.bottom-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;

  .card-time {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
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

  &.type-document {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-brand-color-active);
    width: auto;
    padding: 0 6px;
    gap: 3px;

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }

    .badge-count {
      font-size: 11px;
      font-weight: 500;
    }

    .processing-icon {
      animation: spin 1s linear infinite;
    }
  }

  &.type-faq {
    background: rgba(0, 82, 217, 0.08);
    color: var(--td-brand-color);
    width: auto;
    padding: 0 6px;
    gap: 3px;

    &:hover {
      background: rgba(0, 82, 217, 0.12);
    }

    .badge-count {
      font-size: 11px;
      font-weight: 500;
    }

    .processing-icon {
      animation: spin 1s linear infinite;
    }
  }

  &.kg {
    background: rgba(124, 77, 255, 0.08);
    color: var(--td-brand-color);

    &:hover {
      background: rgba(124, 77, 255, 0.12);
    }
  }

  &.multimodal {
    background: rgba(255, 152, 0, 0.08);
    color: var(--td-warning-color);

    &:hover {
      background: rgba(255, 152, 0, 0.12);
    }
  }

  &.question {
    background: rgba(0, 150, 136, 0.08);
    color: var(--td-success-color);

    &:hover {
      background: rgba(0, 150, 136, 0.12);
    }
  }

  &.shared {
    background: rgba(0, 82, 217, 0.08);
    color: var(--td-brand-color);

    &:hover {
      background: rgba(0, 82, 217, 0.12);
    }
  }

  &.role-admin {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color-active);

    &:hover {
      background: rgba(7, 192, 95, 0.15);
    }
  }

  &.role-editor {
    background: rgba(255, 152, 0, 0.1);
    color: var(--td-warning-color);

    &:hover {
      background: rgba(255, 152, 0, 0.15);
    }
  }

  &.role-viewer {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-secondary);

    &:hover {
      background: rgba(0, 0, 0, 0.08);
    }
  }
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }

  to {
    transform: rotate(360deg);
  }
}

@keyframes highlightFlash {
  0% {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 0 rgba(7, 192, 95, 0.4);
    transform: scale(1);
  }

  50% {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 8px rgba(7, 192, 95, 0);
    transform: scale(1.02);
  }

  100% {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 0 rgba(7, 192, 95, 0);
    transform: scale(1);
  }
}

.kb-card.highlight-flash {
  animation: highlightFlash 0.6s ease-in-out 3;
  border-color: var(--td-brand-color) !important;
  box-shadow: 0 0 12px rgba(7, 192, 95, 0.3) !important;
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
  .kb-card-wrap {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (min-width: 1250px) {
  .kb-card-wrap {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (min-width: 1600px) {
  .kb-card-wrap {
    grid-template-columns: repeat(4, 1fr);
  }
}

@media (min-width: 1900px) {
  .kb-card-wrap {
    grid-template-columns: repeat(5, 1fr);
  }
}

@media (min-width: 2200px) {
  .kb-card-wrap {
    grid-template-columns: repeat(6, 1fr);
  }
}

// 删除确认对话框样式
:deep(.del-knowledge-dialog) {
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

// 共享知识库卡片：详情触发（替代三点，用「查看详情」链接样式）
.shared-detail-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-brand-color);
  font-size: 13px;
  font-family: var(--app-font-family);
  cursor: pointer;
  transition: background 0.2s ease, color 0.2s ease;

  .t-icon {
    flex-shrink: 0;
  }

  &:hover {
    background: rgba(7, 192, 95, 0.08);
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

// 创建对话框样式优化
.create-kb-dialog {
  .t-form-item__label {
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .t-input,
  .t-textarea {
    font-family: var(--app-font-family);
  }

}
</style>
