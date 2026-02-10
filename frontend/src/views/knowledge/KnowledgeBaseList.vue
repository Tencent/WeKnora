<template>
  <div class="kb-list-container">
    <!-- 头部：仅标题与副标题 -->
    <div class="header">
      <div class="header-title">
        <h2>{{ $t('knowledgeBase.title') }}</h2>
        <p class="header-subtitle">{{ $t('knowledgeList.subtitle') }}</p>
      </div>
    </div>

    <!-- 左侧菜单 + 主内容 -->
    <div class="kb-list-body">
      <ListSpaceSidebar
        v-model="spaceSelection"
        :count-all="allKnowledgeBases"
        :count-mine="kbs.length"
        :count-shared="sharedKbs.length"
        :count-by-org="effectiveSharedCountByOrg"
      >
        <template #actions>
          <t-tooltip :content="$t('knowledgeList.create')" placement="top">
            <t-button
              variant="text"
              theme="default"
              class="sidebar-action-btn"
              size="small"
              :aria-label="$t('knowledgeList.create')"
              @click="handleCreateKnowledgeBase"
            >
              <template #icon><t-icon name="folder-add" size="16px" /></template>
            </t-button>
          </t-tooltip>
        </template>
      </ListSpaceSidebar>
      <div class="kb-list-main">
    <!-- 未初始化知识库提示 -->
    <div v-if="hasUninitializedKbs" class="warning-banner">
      <t-icon name="info-circle" size="16px" />
      <span>{{ $t('knowledgeList.uninitializedBanner') }}</span>
    </div>

    <!-- 上传进度提示 -->
    <div v-if="uploadSummaries.length" class="upload-progress-panel">
      <div 
        v-for="summary in uploadSummaries" 
        :key="summary.kbId" 
        class="upload-progress-item"
      >
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

    <!-- 卡片网格：全部 -->
    <div v-if="spaceSelection === 'all' && filteredKnowledgeBases.length > 0" class="kb-card-wrap">
      <!-- 全部：我的知识库 + 共享给我的知识库 -->
      <template v-for="kb in filteredKnowledgeBases" :key="kb.id">
        <!-- 我的知识库卡片 -->
        <div
          v-if="kb.isMine"
          class="kb-card"
          :class="{
            'uninitialized': !isInitialized(kb),
            'kb-type-document': (kb.type || 'document') === 'document',
            'kb-type-faq': kb.type === 'faq',
            'highlight-flash': highlightedKbId !== null && highlightedKbId === kb.id
          }"
          :ref="el => { if (highlightedKbId !== null && highlightedKbId === kb.id && el) highlightedCardRef = el as HTMLElement }"
          @click="handleCardClick(kb)"
        >
          <!-- 卡片头部 -->
          <div class="card-header">
            <span class="card-title" :title="kb.name">{{ kb.name }}</span>
            <t-popup
              overlayClassName="card-more-popup"
              trigger="click"
              destroy-on-close
              placement="bottom-right"
            >
              <div class="more-wrap" @click.stop>
                <img class="more-icon" src="@/assets/img/more.png" alt="" />
              </div>
              <template #content>
                <div class="popup-menu" @click.stop>
                  <div class="popup-menu-item" @click.stop="handleSettingsById(kb.id)">
                    <t-icon class="menu-icon" name="setting" />
                    <span>{{ $t('knowledgeBase.settings') }}</span>
                  </div>
                  <div class="popup-menu-item delete" @click.stop="handleDeleteById(kb.id)">
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
              {{ kb.description || $t('knowledgeBase.noDescription') }}
            </div>
          </div>

          <!-- 卡片底部 -->
          <div class="card-bottom">
            <div class="bottom-left">
              <div class="feature-badges">
                <t-tooltip :content="kb.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')" placement="top">
                  <div class="feature-badge" :class="{ 'type-document': (kb.type || 'document') === 'document', 'type-faq': kb.type === 'faq' }">
                    <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
                    <span class="badge-count">{{ kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0) }}</span>
                    <t-icon v-if="kb.isProcessing" name="loading" size="12px" class="processing-icon" />
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.extract_config?.enabled" :content="$t('knowledgeList.features.knowledgeGraph')" placement="top">
                  <div class="feature-badge kg">
                    <t-icon name="relation" size="14px" />
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.vlm_config?.enabled || (kb.cos_config?.provider && kb.cos_config?.bucket_name)" :content="$t('knowledgeList.features.multimodal')" placement="top">
                  <div class="feature-badge multimodal">
                    <t-icon name="image" size="14px" />
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.question_generation_config?.enabled" :content="$t('knowledgeList.features.questionGeneration')" placement="top">
                  <div class="feature-badge question">
                    <t-icon name="help-circle" size="14px" />
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.share_count && kb.share_count > 0" :content="$t('knowledgeList.sharedToOrgs', { count: kb.share_count })" placement="top">
                  <div class="feature-badge shared">
                    <t-icon name="share" size="14px" />
                  </div>
                </t-tooltip>
              </div>
            </div>
            <div class="bottom-right">
              <div class="personal-source">
                <t-icon name="user" size="14px" />
                <span>{{ $t('knowledgeList.myLabel') }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- 共享知识库卡片 -->
        <div
          v-else
          class="kb-card shared-kb-card"
          :class="{
            'kb-type-document': (kb.type || 'document') === 'document',
            'kb-type-faq': kb.type === 'faq'
          }"
          @click="handleSharedKbClickFromAll(kb)"
        >
          <!-- 卡片头部 -->
          <div class="card-header">
            <span class="card-title" :title="kb.name">{{ kb.name }}</span>
            <t-tooltip :content="$t('knowledgeList.menu.viewDetails')" placement="top">
              <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetailFromAll(kb)" :aria-label="$t('knowledgeList.menu.viewDetails')">
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
                <t-tooltip :content="kb.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')" placement="top">
                  <div class="feature-badge" :class="{ 'type-document': (kb.type || 'document') === 'document', 'type-faq': kb.type === 'faq' }">
                    <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
                    <span class="badge-count">{{ kb.type === 'faq' ? (kb.chunk_count || '-') : (kb.knowledge_count || '-') }}</span>
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.extract_config?.enabled" :content="$t('knowledgeList.features.knowledgeGraph')" placement="top">
                  <div class="feature-badge kg">
                    <t-icon name="relation" size="14px" />
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.vlm_config?.enabled || (kb.cos_config?.provider && kb.cos_config?.bucket_name)" :content="$t('knowledgeList.features.multimodal')" placement="top">
                  <div class="feature-badge multimodal">
                    <t-icon name="image" size="14px" />
                  </div>
                </t-tooltip>
                <t-tooltip v-if="kb.question_generation_config?.enabled" :content="$t('knowledgeList.features.questionGeneration')" placement="top">
                  <div class="feature-badge question">
                    <t-icon name="help-circle" size="14px" />
                  </div>
                </t-tooltip>
              </div>
            </div>
            <div class="bottom-right">
              <t-tooltip :content="kb.org_name" placement="top">
                  <div class="org-source">
                    <img src="@/assets/img/organization-green.svg" class="org-source-icon" alt="" aria-hidden="true" />
                    <span>{{ kb.org_name }}</span>
                  </div>
                </t-tooltip>
            </div>
          </div>
        </div>
      </template>
    </div>

    <div v-if="spaceSelection === 'mine' && kbs.length > 0" class="kb-card-wrap">
      <!-- 我的知识库 -->
      <div
        v-for="(kb, index) in kbs"
        :key="kb.id"
        class="kb-card"
        :class="{
          'uninitialized': !isInitialized(kb),
          'kb-type-document': (kb.type || 'document') === 'document',
          'kb-type-faq': kb.type === 'faq',
          'highlight-flash': highlightedKbId !== null && highlightedKbId === kb.id
        }"
        :ref="el => { if (highlightedKbId !== null && highlightedKbId === kb.id && el) highlightedCardRef = el as HTMLElement }"
        @click="handleCardClick(kb)"
      >
        <!-- 卡片头部 -->
        <div class="card-header">
          <span class="card-title" :title="kb.name">{{ kb.name }}</span>
          <t-popup
            v-model="kb.showMore"
            overlayClassName="card-more-popup"
            :on-visible-change="onVisibleChange"
            trigger="click"
            destroy-on-close
            placement="bottom-right"
          >
            <div
              variant="outline"
              class="more-wrap"
              @click.stop="openMore(index)"
              :class="{ 'active-more': currentMoreIndex === index }"
            >
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu" @click.stop>
                <div class="popup-menu-item" @click.stop="handleSettings(kb)">
                  <t-icon class="menu-icon" name="setting" />
                  <span>{{ $t('knowledgeBase.settings') }}</span>
                </div>
                <div class="popup-menu-item delete" @click.stop="handleDelete(kb)">
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
            {{ kb.description || $t('knowledgeBase.noDescription') }}
          </div>
        </div>

        <!-- 卡片底部 -->
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tooltip :content="kb.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')" placement="top">
                <div class="feature-badge" :class="{ 'type-document': (kb.type || 'document') === 'document', 'type-faq': kb.type === 'faq' }">
                  <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
                  <span class="badge-count">{{ kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0) }}</span>
                  <t-icon v-if="kb.isProcessing" name="loading" size="12px" class="processing-icon" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="kb.extract_config?.enabled" :content="$t('knowledgeList.features.knowledgeGraph')" placement="top">
                <div class="feature-badge kg">
                  <t-icon name="relation" size="14px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="kb.vlm_config?.enabled || (kb.cos_config?.provider && kb.cos_config?.bucket_name)" :content="$t('knowledgeList.features.multimodal')" placement="top">
                <div class="feature-badge multimodal">
                  <t-icon name="image" size="14px" />
                </div>
              </t-tooltip>
              <t-tooltip v-if="kb.question_generation_config?.enabled" :content="$t('knowledgeList.features.questionGeneration')" placement="top">
                <div class="feature-badge question">
                  <t-icon name="help-circle" size="14px" />
                </div>
              </t-tooltip>
              <!-- 共享状态图标 -->
              <t-tooltip v-if="(kb.share_count ?? 0) > 0" :content="$t('knowledgeList.sharedToOrgs', { count: kb.share_count ?? 0 })" placement="top">
                <div class="feature-badge shared">
                  <t-icon name="share" size="14px" />
                </div>
              </t-tooltip>
            </div>
          </div>
          <div class="bottom-right">
            <div class="personal-source">
              <t-icon name="user" size="14px" />
              <span>{{ $t('knowledgeList.myLabel') }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 卡片网格：共享给我 -->
    <div v-if="spaceSelection === 'shared' && sharedKbs.length > 0" class="kb-card-wrap">
      <div
        v-for="shared in sharedKbs"
        :key="'shared-' + shared.share_id"
        class="kb-card shared-kb-card"
        :class="{
          'kb-type-document': (shared.knowledge_base.type || 'document') === 'document',
          'kb-type-faq': shared.knowledge_base.type === 'faq'
        }"
        @click="handleSharedKbClickFromAll(shared.knowledge_base)"
      >
        <div class="card-header">
          <span class="card-title" :title="shared.knowledge_base.name">{{ shared.knowledge_base.name }}</span>
          <t-tooltip :content="$t('knowledgeList.menu.viewDetails')" placement="top">
            <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetail(shared)" :aria-label="$t('knowledgeList.menu.viewDetails')">
              <t-icon name="info-circle" size="16px" />
            </button>
          </t-tooltip>
        </div>
        <div class="card-content">
          <div class="card-description">
            {{ shared.knowledge_base.description || $t('knowledgeBase.noDescription') }}
          </div>
        </div>
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tooltip :content="shared.knowledge_base.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')" placement="top">
                <div class="feature-badge" :class="{ 'type-document': (shared.knowledge_base.type || 'document') === 'document', 'type-faq': shared.knowledge_base.type === 'faq' }">
                  <t-icon :name="shared.knowledge_base.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
                  <span class="badge-count">{{ shared.knowledge_base.type === 'faq' ? (shared.knowledge_base.chunk_count || '-') : (shared.knowledge_base.knowledge_count || '-') }}</span>
                </div>
              </t-tooltip>
            </div>
          </div>
          <div class="bottom-right">
            <t-tooltip :content="shared.org_name" placement="top">
              <div class="org-source">
                <img src="@/assets/img/organization-green.svg" class="org-source-icon" alt="" aria-hidden="true" />
                <span>{{ shared.org_name }}</span>
              </div>
            </t-tooltip>
          </div>
        </div>
      </div>
    </div>

    <!-- 共享给我空状态 -->
    <div v-if="spaceSelection === 'shared' && sharedKbs.length === 0 && !loading" class="empty-state">
      <t-icon name="share" size="48px" class="empty-icon" />
      <p>{{ $t('knowledgeList.emptyShared') }}</p>
    </div>

    <!-- 按空间筛选：该空间内全部知识库（含我共享的） -->
    <div v-if="spaceSelectionOrgId && spaceKbsLoading" class="kb-list-main-loading">
      <t-loading size="large" text="" />
    </div>
    <div v-else-if="spaceSelectionOrgId && spaceKbsList.length > 0" class="kb-card-wrap">
      <div
        v-for="shared in spaceKbsList"
        :key="'shared-' + (shared.share_id || `agent-${shared.knowledge_base?.id}-${shared.source_from_agent?.agent_id || ''}`)"
        class="kb-card shared-kb-card"
        :class="{
          'kb-type-document': (shared.knowledge_base.type || 'document') === 'document',
          'kb-type-faq': shared.knowledge_base.type === 'faq'
        }"
        @click="handleSharedKbClick(shared)"
      >
        <!-- 卡片头部 -->
        <div class="card-header">
          <span class="card-title" :title="shared.knowledge_base.name">{{ shared.knowledge_base.name }}</span>
          <t-tooltip v-if="shared.is_mine" :content="$t('knowledgeList.myLabel')" placement="top">
            <span class="shared-by-me-badge">{{ $t('knowledgeList.myLabel') }}</span>
          </t-tooltip>
          <t-tooltip v-if="!shared.is_mine" :content="$t('knowledgeList.menu.viewDetails')" placement="top">
            <button type="button" class="shared-detail-trigger" @click.stop="openSharedDetail(shared)" :aria-label="$t('knowledgeList.menu.viewDetails')">
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
              <t-tooltip :content="shared.knowledge_base.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument')" placement="top">
                <div class="feature-badge" :class="{ 'type-document': (shared.knowledge_base.type || 'document') === 'document', 'type-faq': shared.knowledge_base.type === 'faq' }">
                  <t-icon :name="shared.knowledge_base.type === 'faq' ? 'chat-bubble-help' : 'folder'" size="14px" />
                  <span class="badge-count">{{ shared.knowledge_base.type === 'faq' ? (shared.knowledge_base.chunk_count ?? '-') : (shared.knowledge_base.knowledge_count ?? '-') }}</span>
                </div>
              </t-tooltip>
            </div>
          </div>
          <div class="bottom-right">
            <t-tooltip :content="shared.org_name" placement="top">
              <div class="org-source">
                <img src="@/assets/img/organization-green.svg" class="org-source-icon" alt="" aria-hidden="true" />
                <span>{{ shared.org_name }}</span>
              </div>
            </t-tooltip>
          </div>
        </div>
      </div>
    </div>

    <!-- 全部空状态 -->
    <div v-if="spaceSelection === 'all' && filteredKnowledgeBases.length === 0 && !loading" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ $t('knowledgeList.empty.title') }}</span>
      <span class="empty-desc">{{ $t('knowledgeList.empty.description') }}</span>
      <t-button class="kb-create-btn empty-state-btn" @click="handleCreateKnowledgeBase">
        <template #icon><t-icon name="folder-add" /></template>
        {{ $t('knowledgeList.create') }}
      </t-button>
    </div>

    <!-- 我的知识库空状态 -->
    <div v-if="spaceSelection === 'mine' && kbs.length === 0 && !loading" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ $t('knowledgeList.empty.title') }}</span>
      <span class="empty-desc">{{ $t('knowledgeList.empty.description') }}</span>
      <t-button class="kb-create-btn empty-state-btn" @click="handleCreateKnowledgeBase">
        <template #icon><t-icon name="folder-add" /></template>
        {{ $t('knowledgeList.create') }}
      </t-button>
    </div>

    <!-- 空间下知识库空状态 -->
    <div v-if="spaceSelectionOrgId && !spaceKbsLoading && spaceKbsList.length === 0" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ $t('knowledgeList.empty.sharedTitle') || '暂无共享知识库' }}</span>
      <span class="empty-desc">{{ $t('knowledgeList.empty.sharedDescription') || '您可以加入组织或请求他人共享知识库给您' }}</span>
    </div>
      </div>
    </div>

    <!-- 删除确认对话框 -->
    <t-dialog 
      v-model:visible="deleteVisible" 
      dialogClassName="del-knowledge-dialog" 
      :closeBtn="false" 
      :cancelBtn="null"
      :confirmBtn="null"
    >
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
          <span class="circle-btn-txt confirm" @click="confirmDelete">{{ $t('knowledgeList.delete.confirmButton') }}</span>
        </div>
      </div>
    </t-dialog>

    <!-- 知识库编辑器（创建/编辑统一组件） -->
    <KnowledgeBaseEditorModal 
      :visible="uiStore.showKBEditorModal"
      :mode="uiStore.kbEditorMode"
      :kb-id="uiStore.currentKBId || undefined"
      :initial-type="uiStore.kbEditorType"
      @update:visible="(val) => val ? null : uiStore.closeKBEditor()"
      @success="handleKBEditorSuccess"
    />

    <!-- 共享知识库对话框 -->
    <ShareKnowledgeBaseDialog
      v-model:visible="shareDialogVisible"
      :knowledge-base-id="sharingKbId"
      :knowledge-base-name="sharingKbName"
      @shared="handleShareSuccess"
    />

    <!-- 右侧：共享知识库详情面板 -->
    <Teleport to="body">
      <Transition name="shared-detail-drawer">
        <div v-if="sharedDetailPanelVisible && currentSharedKbForDetail" class="shared-detail-drawer-overlay" @click.self="closeSharedDetailPanel">
          <div class="shared-detail-drawer">
            <div class="shared-detail-drawer-header">
              <h3 class="shared-detail-drawer-title">{{ $t('knowledgeList.detail.title') }}</h3>
              <button type="button" class="shared-detail-drawer-close" @click="closeSharedDetailPanel" :aria-label="$t('general.close')">
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
                  {{ currentSharedKbForDetail.source_from_agent ? $t('knowledgeList.detail.sourceTypeAgent') : $t('knowledgeList.detail.sourceTypeKbShare') }}
                </span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ currentSharedKbForDetail.source_from_agent ? $t('knowledgeList.detail.sourceFromAgent') : $t('knowledgeList.detail.sourceOrg') }}</span>
                <span class="shared-detail-value shared-detail-org">
                  <img src="@/assets/img/organization-green.svg" class="shared-detail-org-icon" alt="" aria-hidden="true" />
                  {{ currentSharedKbForDetail.source_from_agent ? currentSharedKbForDetail.source_from_agent.agent_name : currentSharedKbForDetail.org_name }}
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
                <span class="shared-detail-value">{{ formatStringDate(new Date(currentSharedKbForDetail.shared_at)) }}</span>
              </div>
              <div class="shared-detail-row">
                <span class="shared-detail-label">{{ $t('knowledgeList.detail.myPermission') }}</span>
                <t-tag size="small" :theme="currentSharedKbForDetail.permission === 'admin' ? 'primary' : currentSharedKbForDetail.permission === 'editor' ? 'warning' : 'default'">
                  {{ $t(`organization.role.${currentSharedKbForDetail.permission}`) }}
                </t-tag>
              </div>
            </div>
            <div class="shared-detail-drawer-footer">
              <t-button theme="default" variant="outline" @click="closeSharedDetailPanel">{{ $t('common.close') }}</t-button>
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
import { listKnowledgeBases, deleteKnowledgeBase } from '@/api/knowledge-base'
import { formatStringDate } from '@/utils/index'
import { useUIStore } from '@/stores/ui'
import { useOrganizationStore } from '@/stores/organization'
import { listOrganizationSharedKnowledgeBases, type SharedKnowledgeBase, type OrganizationSharedKnowledgeBaseItem, type SourceFromAgentInfo } from '@/api/organization'
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue'
import ShareKnowledgeBaseDialog from '@/components/ShareKnowledgeBaseDialog.vue'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'
import { useI18n } from 'vue-i18n'

const router = useRouter()
const route = useRoute()
const uiStore = useUIStore()
const orgStore = useOrganizationStore()
const { t } = useI18n()

// 左侧空间选择：我的 / 空间 ID（已去掉「全部」）
const spaceSelection = ref<'all' | 'mine' | 'shared' | string>('mine')

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
  cos_config?: { provider?: string; bucket_name?: string };
  question_generation_config?: { enabled?: boolean; question_count?: number };
  knowledge_count?: number;
  chunk_count?: number;
  isProcessing?: boolean; // 是否有正在处理的导入任务
  processing_count?: number; // 正在处理的文档数量（仅文档类型）
  share_count?: number; // 共享给组织的数量
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

// Shared knowledge bases
const sharedKbs = computed<SharedKnowledgeBase[]>(() => orgStore.sharedKnowledgeBases || [])

// All knowledge bases (mine + shared to me)
const allKnowledgeBases = computed(() => kbs.value.length + sharedKbs.value.length)

// 当前选中的是空间 ID（非全部、非我的）
const spaceSelectionOrgId = computed(() => {
  const s = spaceSelection.value
  return s !== 'all' && s !== 'mine' && s !== 'shared' && !!s
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
const spaceCountByOrg = ref<Record<string, number>>({})

// 各空间下的共享知识库数量（用于侧栏展示）：优先用接口返回的该空间总数，否则用「共享给我」数量
const sharedCountByOrg = computed<Record<string, number>>(() => {
  const map: Record<string, number> = {}
  sharedKbs.value.forEach(s => {
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
  Object.keys(spaceCountByOrg.value).forEach(orgId => {
    merged[orgId] = spaceCountByOrg.value[orgId]
  })
  return merged
})

// Filtered knowledge bases: 全部 = 我的 + 全部共享；我的 = 仅我的
const filteredKnowledgeBases = computed(() => {
  if (spaceSelection.value === 'mine') {
    return kbs.value.map(kb => ({ ...kb, isMine: true as const }))
  }
  if (spaceSelection.value !== 'all') {
    return []
  }
  const result: Array<(KB & { isMine: true }) | (SharedKnowledgeBase['knowledge_base'] & { isMine: false; permission: string; shared_at: string; share_id: string } & any)> = []
  kbs.value.forEach(kb => {
    result.push({ ...kb, isMine: true as const })
  })
  sharedKbs.value.forEach(shared => {
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
  return Promise.all([
    listKnowledgeBases().then((res: any) => {
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
  if (val === 'all' || val === 'mine' || val === 'shared' || !val) {
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

onMounted(() => {
  fetchList().then(() => {
    // 检查路由参数中是否有需要高亮的知识库ID
    const highlightKbId = route.query.highlightKbId as string
    if (highlightKbId) {
      triggerHighlightFlash(highlightKbId)
      // 清除 URL 中的查询参数
      router.replace({ query: {} })
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
    router.replace({ query: {} })
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
  // 跳转到共享知识库详情页
  router.push(`/platform/knowledge-bases/${sharedKb.knowledge_base.id}`)
}

// 处理"全部"Tab 中的共享知识库卡片点击（直接进入知识库）
const handleSharedKbClickFromAll = (kb: any) => {
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
  return !!(kb.embedding_model_id && kb.embedding_model_id !== '' && 
            kb.summary_model_id && kb.summary_model_id !== '')
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
  if (isInitialized(kb)) {
    goDetail(kb.id)
  } else {
    goSettings(kb.id)
  }
}

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
      // 清除 URL 中的查询参数
      router.replace({ query: {} })
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
  padding: 24px 32px;
  margin: 0 16px 0 4px;
  height: calc(100vh);
  box-sizing: border-box;
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
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

.kb-create-btn {
  background: linear-gradient(135deg, #07c05f 0%, #00a67e 100%);
  border: none;
  color: #fff;

  &:hover {
    background: linear-gradient(135deg, #05a04f 0%, #008a6a 100%);
  }
}

.kb-list-body {
  display: flex;
  flex: 1;
  min-height: 0;
  background: #fafbfc;
  border: 1px solid #e7ebf0;
  border-radius: 10px;
  overflow: hidden;
}

.kb-list-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 12px;
  background: #fafbfc;
}

.kb-list-main-loading {
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

.header-subtitle {
  margin: 0;
  color: #00000099;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

// Tab 切换样式（已由左侧菜单替代，保留以备兼容）
.kb-tabs {
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
    user-select: none;
    position: relative;
    transition: color 0.2s ease;

    &:hover {
      color: #333;
    }

    &.active {
      color: #07c05f;
      font-weight: 500;

      &::after {
        content: '';
        position: absolute;
        bottom: -1px;
        left: 0;
        right: 0;
        height: 2px;
        background: #07c05f;
        border-radius: 1px;
      }
    }
  }
}


// 共享知识库卡片样式
// 共享标识（文档类型默认绿色，位置贴右上角）
.shared-badge {
  position: absolute;
  top: 10px;
  right: 18px;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  background: rgba(7, 192, 95, 0.1);
  border-radius: 4px;
  font-size: 12px;
  color: #07c05f;
  font-weight: 500;

  .t-icon {
    color: #07c05f;
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
  color: #4e5969;
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
    color: #07c05f;
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
  color: #4e5969;
  font-weight: 500;
  transition: background-color 0.15s ease;

  span {
    font-weight: 500;
  }

  .t-icon {
    color: #07c05f;
    flex-shrink: 0;
  }
}

.shared-kb-card {
  position: relative;

  // 共享知识库根据类型显示不同样式
  &.kb-type-document {
    background: linear-gradient(135deg, #ffffff 0%, #f8fcfa 100%) !important;
    border-color: #e8f5ed !important;

    &:hover {
      border-color: #07c05f !important;
      box-shadow: 0 4px 12px rgba(7, 192, 95, 0.12) !important;
      background: linear-gradient(135deg, #ffffff 0%, #f0fdf4 100%) !important;
    }

    &::after {
      background: linear-gradient(135deg, rgba(7, 192, 95, 0.08) 0%, transparent 100%) !important;
    }
  }

  &.kb-type-faq {
    background: linear-gradient(135deg, #ffffff 0%, #f8fbff 100%) !important;
    border-color: #e6f0ff !important;

    &:hover {
      border-color: #0052d9 !important;
      box-shadow: 0 4px 12px rgba(0, 82, 217, 0.12) !important;
      background: linear-gradient(135deg, #ffffff 0%, #eff6ff 100%) !important;
    }

    &::after {
      background: linear-gradient(135deg, rgba(0, 82, 217, 0.08) 0%, transparent 100%) !important;
    }

    // FAQ 类型共享标识使用蓝色
    .shared-badge {
      background: rgba(0, 82, 217, 0.1);
      color: #0052d9;

      .t-icon {
        color: #0052d9;
      }
    }
  }

  .org-tag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    border-color: rgba(0, 82, 217, 0.15);
    color: #0052d9;
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
  background: #fff7e6;
  border: 1px solid #ffd591;
  border-radius: 6px;
  color: #d46b08;
  font-family: "PingFang SC";
  font-size: 14px;
  
  .t-icon {
    color: #d46b08;
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
  border: 1px solid #e7ebf0;
  border-radius: 8px;
  background: #f7fbff;
}

.upload-progress-icon {
  color: #07c05f;
  display: flex;
  align-items: center;
  justify-content: center;
}

.upload-progress-content {
  flex: 1;
}

.progress-title {
  color: #0f172a;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 600;
  line-height: 22px;
  margin-bottom: 2px;
}

.progress-subtitle {
  color: #475467;
  font-family: "PingFang SC";
  font-size: 12px;
  line-height: 18px;
}

.progress-subtitle.secondary {
  color: #94a3b8;
  margin-top: 2px;
}

.progress-subtitle.error {
  color: #d92d20;
  margin-top: 4px;
}

.progress-bar {
  width: 100%;
  height: 6px;
  border-radius: 999px;
  background: #e7eef5;
  margin-top: 10px;
  overflow: hidden;
}

.progress-bar-inner {
  height: 100%;
  background: linear-gradient(90deg, #05a04f 0%, #07c05f 100%);
  transition: width 0.2s ease;
}

.kb-card-wrap {
  display: grid;
  gap: 20px;
  grid-template-columns: 1fr;
}

.kb-card {
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

  &.uninitialized {
    opacity: 0.9;
  }

  // 文档类型样式
  &.kb-type-document {
    background: linear-gradient(135deg, #ffffff 0%, #f8fcfa 100%);
    border-color: #e8f5ed;

    &:hover {
      border-color: #07c05f;
      background: linear-gradient(135deg, #ffffff 0%, #f0fdf4 100%);
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
    background: linear-gradient(135deg, #ffffff 0%, #f8fbff 100%);
    border-color: #e6f0ff;

    &:hover {
      border-color: #0052d9;
      box-shadow: 0 4px 12px rgba(0, 82, 217, 0.12);
      background: linear-gradient(135deg, #ffffff 0%, #eff6ff 100%);
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

  .card-more-btn {
    width: 28px;
    height: 28px;
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;

  .card-title {
    flex: 1;
    font-size: 15px;
    font-weight: 600;
    color: #1d2129;
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
    color: #999;
    cursor: pointer;
    transition: all 0.2s;

    &:hover {
      background: rgba(0, 0, 0, 0.05);
      color: #666;
    }
  }

  .permission-tag {
    flex-shrink: 0;
  }
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
    background: rgba(0, 0, 0, 0.05);
    opacity: 1 !important;
  }

  &.active-more {
    background: rgba(0, 0, 0, 0.06);
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
    color: #999;
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
    color: #059669;
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
    color: #0052d9;
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
    color: #7c4dff;

    &:hover {
      background: rgba(124, 77, 255, 0.12);
    }
  }

  &.multimodal {
    background: rgba(255, 152, 0, 0.08);
    color: #f59e0b;

    &:hover {
      background: rgba(255, 152, 0, 0.12);
    }
  }

  &.question {
    background: rgba(0, 150, 136, 0.08);
    color: #009688;

    &:hover {
      background: rgba(0, 150, 136, 0.12);
    }
  }

  &.shared {
    background: rgba(0, 82, 217, 0.08);
    color: #0052d9;

    &:hover {
      background: rgba(0, 82, 217, 0.12);
    }
  }

  &.role-admin {
    background: rgba(7, 192, 95, 0.1);
    color: #059669;

    &:hover {
      background: rgba(7, 192, 95, 0.15);
    }
  }

  &.role-editor {
    background: rgba(255, 152, 0, 0.1);
    color: #f59e0b;

    &:hover {
      background: rgba(255, 152, 0, 0.15);
    }
  }

  &.role-viewer {
    background: rgba(0, 0, 0, 0.05);
    color: #666;

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
    border-color: #07c05f;
    box-shadow: 0 0 0 0 rgba(7, 192, 95, 0.4);
    transform: scale(1);
  }
  50% {
    border-color: #07c05f;
    box-shadow: 0 0 0 8px rgba(7, 192, 95, 0);
    transform: scale(1.02);
  }
  100% {
    border-color: #07c05f;
    box-shadow: 0 0 0 0 rgba(7, 192, 95, 0);
    transform: scale(1);
  }
}

.kb-card.highlight-flash {
  animation: highlightFlash 0.6s ease-in-out 3;
  border-color: #07c05f !important;
  box-shadow: 0 0 12px rgba(7, 192, 95, 0.3) !important;
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

// 共享知识库卡片：详情触发（替代三点，用「查看详情」链接样式）
.shared-detail-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #07c05f;
  font-size: 13px;
  font-family: "PingFang SC", sans-serif;
  cursor: pointer;
  transition: background 0.2s ease, color 0.2s ease;

  .t-icon {
    flex-shrink: 0;
  }

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    color: #059655;
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

  &.shared-detail-source-type {
    font-weight: 500;
    color: #0d0d0d;
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
  border-top: 1px solid #e7ebf0;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  background: #fff;

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
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }

  .t-input,
  .t-textarea {
    font-family: "PingFang SC";
  }

  .t-button--theme-primary {
    background-color: #07c05f;
    border-color: #07c05f;

    &:hover {
      background-color: #05a04f;
      border-color: #05a04f;
    }
  }
}
</style>
