<template>
  <div class="faq-manager">
    <div class="faq-content">
      <!-- Header -->
      <div class="faq-header">
        <div class="faq-header-title">
          <div class="faq-title-row">
            <h2 class="faq-breadcrumb">
              <button type="button" class="breadcrumb-link" @click="handleNavigateToKbList">
                {{ $t('menu.knowledgeBase') }}
              </button>
              <t-icon name="chevron-right" class="breadcrumb-separator" />
              <t-dropdown
                v-if="knowledgeDropdownOptions.length"
                :options="knowledgeDropdownOptions"
                trigger="click"
                placement="bottom-left"
                @click="handleKnowledgeDropdownSelect"
              >
                <button
                  type="button"
                  class="breadcrumb-link dropdown"
                  :disabled="!props.kbId"
                  @click.stop="handleNavigateToCurrentKB"
                >
                  <span>{{ kbInfo?.name || '--' }}</span>
                  <t-icon name="chevron-down" />
                </button>
              </t-dropdown>
              <button
                v-else
                type="button"
                class="breadcrumb-link"
                :disabled="!props.kbId"
                @click="handleNavigateToCurrentKB"
              >
                {{ kbInfo?.name || '--' }}
              </button>
              <t-icon name="chevron-right" class="breadcrumb-separator" />
              <span class="breadcrumb-current">{{ $t('knowledgeEditor.faq.title') }}</span>
            </h2>
            <t-tooltip :content="$t('knowledgeBase.settings')" placement="top">
              <button
                type="button"
                class="kb-settings-button"
                :disabled="!props.kbId"
                @click="handleOpenKBSettings"
              >
                <t-icon name="setting" size="16px" />
              </button>
            </t-tooltip>
          </div>
          <p class="faq-subtitle">{{ $t('knowledgeEditor.faq.subtitle') }}</p>
        </div>
      </div>

      <!-- 导入结果统计（持久化显示） -->
      <div v-if="importResult && importResult.display_status === 'open' && !importState.taskId" class="faq-import-result-card">
        <div class="import-result-content">
          <div class="import-result-header">
            <div class="header-left">
              <t-icon name="check-circle-filled" size="20px" class="result-icon" />
              <span class="result-title">最近导入结果</span>
            </div>
            <div class="header-right">
              <span class="result-time">{{ formatImportTime(importResult.imported_at) }}</span>
              <t-button
                variant="text"
                theme="default"
                size="small"
                class="result-close-btn"
                @click="closeImportResult"
              >
                <t-icon name="close" size="16px" />
              </t-button>
            </div>
          </div>
          <div class="import-result-body">
            <div class="import-result-stats">
              <div class="stat-item">
                <span class="stat-label">导入数据</span>
                <span class="stat-value">{{ importResult.total_entries }}条</span>
              </div>
              <div class="stat-item success">
                <span class="stat-label">成功</span>
                <span class="stat-value">{{ importResult.success_count }}条</span>
              </div>
              <div v-if="importResult.failed_count > 0" class="stat-item failed">
                <span class="stat-label">失败</span>
                <span class="stat-value">{{ importResult.failed_count }}条</span>
                <t-button
                  v-if="importResult.failed_entries_url"
                  variant="outline"
                  theme="danger"
                  size="small"
                  class="download-failed-btn"
                  @click="downloadFailedEntries"
                >
                  <t-icon name="download" size="14px" />
                  下载原因
                </t-button>
              </div>
              <div v-if="importResult.skipped_count > 0" class="stat-item skipped">
                <span class="stat-label">跳过</span>
                <span class="stat-value">{{ importResult.skipped_count }}条</span>
              </div>
            </div>
            <div class="import-mode-tag">
              <t-tag size="small" variant="light" theme="success">
                {{ importResult.import_mode === 'append' ? '追加模式' : '替换模式' }}
              </t-tag>
            </div>
          </div>
        </div>
      </div>

      <!-- 导入进度条（显示在列表页面顶部） -->
      <div v-if="importState.taskId && importState.taskStatus" class="faq-import-progress-bar">
        <div class="progress-bar-content">
          <div class="progress-bar-header">
            <div class="progress-left">
              <t-icon 
                :name="importState.taskStatus.status === 'running' ? 'loading' : 
                       importState.taskStatus.status === 'success' ? 'check-circle' : 
                       importState.taskStatus.status === 'failed' ? 'error-circle' : 'time'"
                size="18px" 
                class="progress-icon"
                :class="{
                  'icon-loading': importState.taskStatus.status === 'running',
                  'icon-success': importState.taskStatus.status === 'success',
                  'icon-error': importState.taskStatus.status === 'failed'
                }"
              />
              <span class="progress-title">
                {{ importState.taskStatus.status === 'running' ? '导入中...' : 
                   importState.taskStatus.status === 'success' ? '导入完成' : 
                   importState.taskStatus.status === 'failed' ? '导入失败' : '等待中...' }}
              </span>
            </div>
            <div class="progress-right">
              <span class="progress-count">
                {{ importState.taskStatus.processed }}/{{ importState.taskStatus.total }} 条
              </span>
              <t-button
                v-if="importState.taskStatus.status === 'success' || importState.taskStatus.status === 'failed'"
                variant="text"
                theme="default"
                size="small"
                class="progress-close-btn"
                @click="handleCloseProgress"
              >
                <t-icon name="close" size="14px" />
              </t-button>
            </div>
          </div>
          <t-progress
            :percentage="importState.taskStatus.progress"
            :status="importState.taskStatus.status === 'failed' ? 'error' : 
                     importState.taskStatus.status === 'success' ? 'success' : 'active'"
            :label="false"
            class="progress-bar"
          />
          <p v-if="importState.taskStatus.error" class="progress-error">
            {{ importState.taskStatus.error }}
          </p>
        </div>
      </div>

      <div class="faq-main">
        <aside class="faq-tag-panel">
          <div class="sidebar-header">
            <div class="sidebar-title">
              <span>{{ $t('knowledgeBase.faqCategoryTitle') }}</span>
              <span class="sidebar-count">({{ sidebarCategoryCount }})</span>
            </div>
            <div class="sidebar-actions">
              <t-button
                size="small"
                variant="text"
                class="create-tag-btn"
                :aria-label="$t('knowledgeBase.tagCreateAction')"
                :title="$t('knowledgeBase.tagCreateAction')"
                @click="startCreateTag"
              >
                <span class="create-tag-plus" aria-hidden="true">+</span>
              </t-button>
            </div>
          </div>
          <div class="tag-search-bar">
            <t-input
              v-model.trim="tagSearchQuery"
              size="small"
              :placeholder="$t('knowledgeBase.tagSearchPlaceholder')"
              clearable
            >
              <template #prefix-icon>
                <t-icon name="search" size="14px" />
              </template>
            </t-input>
          </div>
          <t-loading :loading="tagLoading" size="small">
            <div ref="tagListRef" class="faq-tag-list" @scroll="handleTagListScroll">
              <div v-if="creatingTag" class="faq-tag-item tag-editing" @click.stop>
                <div class="faq-tag-left">
                  <t-icon name="folder" size="18px" />
                  <div class="tag-edit-input">
                    <t-input
                      ref="newTagInputRef"
                      v-model="newTagName"
                      size="small"
                      :maxlength="40"
                      :placeholder="$t('knowledgeBase.tagNamePlaceholder')"
                      @keydown.enter.stop.prevent="submitCreateTag"
                      @keydown.esc.stop.prevent="cancelCreateTag"
                    />
                  </div>
                </div>
                <div class="tag-inline-actions">
                  <t-button
                    variant="text"
                  theme="default"
                    size="small"
                  class="tag-action-btn confirm"
                    :loading="creatingTagLoading"
                    @click.stop="submitCreateTag"
                  >
                    <t-icon name="check" size="16px" />
                  </t-button>
                <t-button
                  variant="text"
                  theme="default"
                  size="small"
                  class="tag-action-btn cancel"
                  @click.stop="cancelCreateTag"
                >
                    <t-icon name="close" size="16px" />
                  </t-button>
                </div>
              </div>

              <template v-if="filteredTags.length">
                <div
                  v-for="tag in filteredTags"
                  :key="tag.id"
                  class="faq-tag-item"
                  :class="{ active: selectedTagId === tag.seq_id, editing: editingTagId === tag.id }"
                  @click="handleTagRowClick(tag.seq_id)"
                >
                  <div class="faq-tag-left">
                    <t-icon name="folder" size="18px" />
                    <template v-if="editingTagId === tag.id">
                      <div class="tag-edit-input" @click.stop>
                        <t-input
                          :ref="setEditingTagInputRefByTag(tag.id)"
                          v-model="editingTagName"
                          size="small"
                          :maxlength="40"
                          @keydown.enter.stop.prevent="submitEditTag"
                          @keydown.esc.stop.prevent="cancelEditTag"
                        />
                      </div>
                    </template>
                    <template v-else>
                      <span class="tag-name" :title="tag.name">{{ tag.name }}</span>
                    </template>
                  </div>
                  <div class="faq-tag-right">
                    <span class="faq-tag-count">{{ tag.chunk_count || 0 }}</span>
                    <template v-if="editingTagId === tag.id">
                      <div class="tag-inline-actions" @click.stop>
                        <t-button
                          variant="text"
                          theme="default"
                          size="small"
                          class="tag-action-btn confirm"
                          :loading="editingTagSubmitting"
                          @click.stop="submitEditTag"
                        >
                          <t-icon name="check" size="16px" />
                        </t-button>
                        <t-button
                          variant="text"
                          theme="default"
                          size="small"
                          class="tag-action-btn cancel"
                          @click.stop="cancelEditTag"
                        >
                          <t-icon name="close" size="16px" />
                        </t-button>
                      </div>
                    </template>
                    <template v-else>
                      <div class="tag-more" @click.stop>
                        <t-popup trigger="click" placement="top-right" overlayClassName="tag-more-popup">
                          <div class="tag-more-btn">
                            <t-icon name="more" size="14px" />
                          </div>
                          <template #content>
                            <div class="tag-menu">
                              <div class="tag-menu-item" @click="startEditTag(tag)">
                                <t-icon class="menu-icon" name="edit" />
                                <span>{{ $t('knowledgeBase.tagEditAction') }}</span>
                              </div>
                              <div class="tag-menu-item danger" @click="confirmDeleteTag(tag)">
                                <t-icon class="menu-icon" name="delete" />
                                <span>{{ $t('knowledgeBase.tagDeleteAction') }}</span>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </template>
                  </div>
                </div>
              </template>
              <div v-else class="tag-empty-state">
                {{ $t('knowledgeBase.tagEmptyResult') }}
              </div>
              <div v-if="tagLoadingMore" class="tag-loading-more">
                <t-loading size="small" />
              </div>
            </div>
          </t-loading>
        </aside>

        <div class="faq-card-area">
          <!-- 搜索栏 -->
          <div class="faq-search-bar">
            <t-input
              v-model.trim="entrySearchKeyword"
              :placeholder="$t('knowledgeEditor.faq.searchPlaceholder')"
              clearable
              @clear="loadEntries()"
              @keydown.enter="loadEntries()"
            >
              <template #prefix-icon>
                <t-icon name="search" size="16px" />
              </template>
            </t-input>
          </div>
          <!-- Card List Container with Scroll -->
          <div ref="scrollContainer" class="faq-scroll-container" @scroll="handleScroll">
          <t-loading :loading="loading && entries.length === 0" size="medium">
            <!-- Card List -->
            <template v-if="entries.length > 0">
              <div ref="cardListRef" class="faq-card-list">
                <div
                  v-for="entry in entries"
                  :key="entry.id"
                  class="faq-card"
                  :class="{ 'selected': selectedRowKeys.includes(entry.id) }"
                  @click="handleCardSelect(entry.id, !selectedRowKeys.includes(entry.id))"
                >
                  <!-- Card Header -->
                  <div class="faq-card-header">
                    <div class="faq-header-top">
                      <div class="faq-question" :title="entry.standard_question">
                        {{ entry.standard_question }}
                      </div>
                      <div class="faq-card-actions">
                        <t-popup
                          v-model="entry.showMore"
                          overlayClassName="faq-card-popup"
                          trigger="click"
                          destroy-on-close
                          placement="bottom-right"
                          @visible-change="(visible: boolean) => (entry.showMore = visible)"
                        >
                          <div class="card-more-btn" @click.stop>
                            <img class="more-icon" src="@/assets/img/more.png" alt="" />
                          </div>
                          <template #content>
                            <div class="card-menu" @click.stop>
                              <div class="card-menu-item" @click.stop="handleMenuEdit(entry)">
                                <t-icon class="menu-icon" name="edit" />
                                <span>{{ $t('common.edit') }}</span>
                              </div>
                              <div class="card-menu-item danger" @click.stop="handleMenuDelete(entry)">
                                <t-icon class="menu-icon" name="delete" />
                                <span>{{ $t('common.delete') }}</span>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </div>
                  </div>

                  <!-- Card Body -->
                  <div class="faq-card-body">
                    <!-- Similar Questions Section -->
                    <div v-if="entry.similar_questions?.length" class="faq-section similar">
                      <div
                        class="faq-section-label clickable"
                        @click.stop="entry.similarCollapsed = !entry.similarCollapsed"
                      >
                        <span>{{ $t('knowledgeEditor.faq.similarQuestions') }}</span>
                        <span class="section-count">
                          ({{ entry.similar_questions.length }})
                        </span>
                        <t-icon
                          :name="entry.similarCollapsed ? 'chevron-right' : 'chevron-down'"
                          class="collapse-icon"
                        />
                      </div>
                      <Transition name="slide-down">
                        <div v-if="!entry.similarCollapsed" class="faq-tags">
                          <FAQTagTooltip
                            v-for="question in entry.similar_questions"
                            :key="question"
                            :content="question"
                            type="similar"
                            placement="top"
                          >
                            <t-tag
                              size="small"
                              variant="light-outline"
                              class="question-tag"
                            >
                              {{ question }}
                            </t-tag>
                          </FAQTagTooltip>
                        </div>
                      </Transition>
                    </div>

                    <!-- Negative Questions Section -->
                    <div v-if="entry.negative_questions?.length" class="faq-section negative">
                      <div
                        class="faq-section-label clickable"
                        @click.stop="entry.negativeCollapsed = !entry.negativeCollapsed"
                      >
                        <span>{{ $t('knowledgeEditor.faq.negativeQuestions') }}</span>
                        <span class="section-count">
                          ({{ entry.negative_questions.length }})
                        </span>
                        <t-icon
                          :name="entry.negativeCollapsed ? 'chevron-right' : 'chevron-down'"
                          class="collapse-icon"
                        />
                      </div>
                      <Transition name="slide-down">
                        <div v-if="!entry.negativeCollapsed" class="faq-tags">
                          <FAQTagTooltip
                            v-for="question in entry.negative_questions"
                            :key="question"
                            :content="question"
                            type="negative"
                            placement="top"
                          >
                            <t-tag
                              size="small"
                              theme="warning"
                              variant="light-outline"
                              class="question-tag"
                            >
                              {{ question }}
                            </t-tag>
                          </FAQTagTooltip>
                        </div>
                      </Transition>
                    </div>

                    <!-- Answers Section -->
                    <div class="faq-section answers">
                      <div
                        class="faq-section-label clickable"
                        @click.stop="entry.answersCollapsed = !entry.answersCollapsed"
                      >
                        <span>{{ $t('knowledgeEditor.faq.answers') }}</span>
                        <span v-if="entry.answers?.length" class="section-count">
                          ({{ entry.answers.length }})
                        </span>
                        <t-icon
                          :name="entry.answersCollapsed ? 'chevron-right' : 'chevron-down'"
                          class="collapse-icon"
                        />
                      </div>
                      <Transition name="slide-down">
                        <div v-if="!entry.answersCollapsed" class="faq-tags">
                          <FAQTagTooltip
                            v-for="answer in entry.answers"
                            :key="answer"
                            :content="answer"
                            type="answer"
                            placement="top"
                          >
                            <t-tag
                              size="small"
                              theme="success"
                              variant="light-outline"
                              class="question-tag"
                            >
                              {{ answer }}
                            </t-tag>
                          </FAQTagTooltip>
                        </div>
                      </Transition>
                    </div>
                  </div>

                  <!-- Card Footer -->
                  <div class="faq-card-footer">
                    <div class="faq-card-tag" @click.stop>
                      <template v-if="tagList.length">
                        <t-dropdown
                          :options="tagDropdownOptions"
                          trigger="click"
                          @click="(data: any) => handleEntryTagChange(entry.id, data.value as string)"
                        >
                          <t-tag size="small" variant="light-outline" class="faq-tag-chip">
                            <span class="tag-text">{{ getTagName(entry.tag_id) || $t('knowledgeBase.untagged') }}</span>
                          </t-tag>
                        </t-dropdown>
                      </template>
                      <template v-else>
                        <t-tag size="small" variant="light-outline" class="faq-tag-chip">
                          <span class="tag-text">{{ getTagName(entry.tag_id) || $t('knowledgeBase.untagged') }}</span>
                        </t-tag>
                      </template>
                    </div>
                    <div class="faq-card-status" @click.stop>
                      <!-- 暂时隐藏推荐开关
                      <t-tooltip
                        :content="entry.is_recommended ? $t('knowledgeEditor.faq.recommendedEnabled') : $t('knowledgeEditor.faq.recommendedDisabled')"
                        placement="top"
                      >
                        <div class="status-item-compact">
                          <t-switch
                            :key="`${entry.id}-recommended-${entry.is_recommended}`"
                            size="small"
                            :value="entry.is_recommended"
                            :loading="!!entryRecommendedLoading[entry.id]"
                            :disabled="!!entryRecommendedLoading[entry.id]"
                            @click.stop
                            @change="(value: boolean) => handleEntryRecommendedChange(entry, value)"
                          />
                          <span class="status-label">{{ $t('knowledgeEditor.faq.recommended') }}</span>
                        </div>
                      </t-tooltip>
                      -->
                      <t-tooltip
                        :content="entry.is_enabled ? $t('knowledgeEditor.faq.statusEnabled') : $t('knowledgeEditor.faq.statusDisabled')"
                        placement="top"
                      >
                        <div class="status-item-compact">
                          <t-switch
                            :key="`${entry.id}-${entry.is_enabled}`"
                            size="small"
                            :value="entry.is_enabled"
                            :loading="!!entryStatusLoading[entry.id]"
                            :disabled="!!entryStatusLoading[entry.id]"
                            @click.stop
                            @change="(value: boolean) => handleEntryStatusChange(entry, value)"
                          />
                        </div>
                      </t-tooltip>
                    </div>
                  </div>
                </div>
              </div>
            </template>
            <template v-else>
              <div v-if="!loading" class="faq-empty-state">
                <div class="empty-content">
                  <t-icon name="file-add" size="48px" class="empty-icon" />
                  <div class="empty-text">{{ $t('knowledgeEditor.faq.emptyTitle') }}</div>
                  <div class="empty-desc">{{ $t('knowledgeEditor.faq.emptyDesc') }}</div>
                </div>
              </div>
            </template>
          </t-loading>
          <div v-if="loadingMore" class="faq-load-more">
            <t-loading size="small" :text="$t('common.loading')" />
          </div>
          <div v-if="hasMore === false && entries.length > 0" class="faq-no-more">
            {{ $t('common.noMoreData') }}
          </div>
          </div>
        </div>
      </div>
    </div>
    <!-- Editor Drawer -->
    <t-drawer
      v-model:visible="editorVisible"
      :header="editorMode === 'create' ? $t('knowledgeEditor.faq.editorCreate') : $t('knowledgeEditor.faq.editorEdit')"
      :close-btn="true"
      size="520px"
      placement="right"
      class="faq-editor-drawer"
      @close="handleEditorClose"
    >
      <div class="faq-editor-drawer-content">
        <t-form
          ref="editorFormRef"
          :data="editorForm"
          :rules="editorRules"
          layout="vertical"
          :label-width="0"
          class="faq-editor-form"
        >
          <div class="settings-group">
            <!-- 标准问 -->
            <div class="setting-row vertical setting-row-primary">
              <div class="setting-info">
                <label class="required-label">
                  {{ $t('knowledgeEditor.faq.standardQuestion') }}
                  <span class="required-mark">*</span>
                </label>
                <p class="desc">{{ $t('knowledgeEditor.faq.standardQuestionDesc') }}</p>
              </div>
              <div class="setting-control">
                <t-input 
                  v-model="editorForm.standard_question" 
                  :maxlength="200"
                  class="full-width-input"
                />
              </div>
            </div>

            <!-- 相似问 -->
            <div class="setting-row vertical setting-row-optional setting-row-similar">
              <div class="setting-info">
                <label class="optional-label">{{ $t('knowledgeEditor.faq.similarQuestions') }}</label>
                <p class="desc optional-desc">{{ $t('knowledgeEditor.faq.similarQuestionsDesc') }}</p>
              </div>
              <div class="setting-control">
                <div class="full-width-input-wrapper">
                  <t-input
                    v-model="similarInput"
                    :placeholder="$t('knowledgeEditor.faq.similarPlaceholder')"
                    @keydown.enter.prevent="addSimilar"
                    class="full-width-input"
                  />
                  <t-button
                    theme="primary"
                    variant="outline"
                    :disabled="!similarInput.trim() || editorForm.similar_questions.length >= 10"
                    @click="addSimilar"
                    class="add-item-btn"
                    size="small"
                  >
                    <t-icon name="add" size="16px" />
                  </t-button>
                </div>
                <div v-if="editorForm.similar_questions.length > 0" class="item-list">
                  <div
                    v-for="(question, index) in editorForm.similar_questions"
                    :key="index"
                    class="item-row"
                  >
                    <div class="item-content">{{ question }}</div>
                    <t-button
                      theme="default"
                      variant="text"
                      size="small"
                      @click="removeSimilar(index)"
                      class="remove-item-btn"
                    >
                      <t-icon name="close" size="16px" />
                    </t-button>
                  </div>
                </div>
              </div>
            </div>

            <!-- 反例 -->
            <div class="setting-row vertical setting-row-optional setting-row-negative">
              <div class="setting-info">
                <label class="optional-label">{{ $t('knowledgeEditor.faq.negativeQuestions') }}</label>
                <p class="desc optional-desc">{{ $t('knowledgeEditor.faq.negativeQuestionsDesc') }}</p>
              </div>
              <div class="setting-control">
                <div class="full-width-input-wrapper">
                  <t-input
                    v-model="negativeInput"
                    :placeholder="$t('knowledgeEditor.faq.negativePlaceholder')"
                    @keydown.enter.prevent="addNegative"
                    class="full-width-input"
                  />
                  <t-button
                    theme="primary"
                    variant="outline"
                    :disabled="!negativeInput.trim() || editorForm.negative_questions.length >= 10"
                    @click="addNegative"
                    class="add-item-btn"
                    size="small"
                  >
                    <t-icon name="add" size="16px" />
                  </t-button>
                </div>
                <div v-if="editorForm.negative_questions.length > 0" class="item-list">
                  <div
                    v-for="(question, index) in editorForm.negative_questions"
                    :key="index"
                    class="item-row negative"
                  >
                    <div class="item-content">{{ question }}</div>
                    <t-button
                      theme="default"
                      variant="text"
                      size="small"
                      @click="removeNegative(index)"
                      class="remove-item-btn"
                    >
                      <t-icon name="close" size="16px" />
                    </t-button>
                  </div>
                </div>
              </div>
            </div>

            <!-- 答案 -->
            <div class="setting-row vertical setting-row-primary setting-row-answer">
              <div class="setting-info">
                <label class="required-label">
                  {{ $t('knowledgeEditor.faq.answers') }}
                  <span class="required-mark">*</span>
                </label>
                <p class="desc">{{ $t('knowledgeEditor.faq.answersDesc') }}</p>
              </div>
              <div class="setting-control">
                <div class="textarea-container">
                  <div class="full-width-input-wrapper textarea-wrapper">
                    <t-textarea
                      v-model="answerInput"
                      :placeholder="$t('knowledgeEditor.faq.answerPlaceholder')"
                      :autosize="{ minRows: 3, maxRows: 6 }"
                      class="full-width-textarea"
                      @keydown.ctrl.enter="addAnswer"
                      @keydown.meta.enter="addAnswer"
                    />
                    <t-button
                      theme="primary"
                      variant="outline"
                      :disabled="!answerInput.trim() || editorForm.answers.length >= 5"
                      @click="addAnswer"
                      class="add-item-btn"
                      size="small"
                    >
                      <t-icon name="add" size="16px" />
                    </t-button>
                  </div>
                  <div class="item-count">{{ editorForm.answers.length }}/5</div>
                </div>
                <div v-if="editorForm.answers.length > 0" class="item-list">
                  <div
                    v-for="(answer, index) in editorForm.answers"
                    :key="index"
                    class="item-row answer-row"
                  >
                    <div class="item-content">{{ answer }}</div>
                    <t-button
                      theme="default"
                      variant="text"
                      size="small"
                      @click="removeAnswer(index)"
                      class="remove-item-btn"
                    >
                      <t-icon name="close" size="16px" />
                    </t-button>
                  </div>
                </div>
              </div>
            </div>

            <div class="setting-row vertical">
              <div class="setting-info">
                <label>{{ $t('knowledgeBase.category') || '分类' }}</label>
                <p class="desc">{{ $t('knowledgeEditor.faq.tagDesc') || '为 FAQ 条目选择分类' }}</p>
              </div>
              <div class="setting-control">
                <t-select
                  v-model="editorForm.tag_id"
                  class="full-width-input"
                  :options="tagSelectOptions"
                  clearable
                  :placeholder="$t('knowledgeEditor.faq.tagPlaceholder') || '选择分类'"
                />
              </div>
            </div>
          </div>
        </t-form>
      </div>

      <template #footer>
        <div class="faq-editor-drawer-footer">
          <t-button theme="default" variant="outline" @click="editorVisible = false">
            {{ $t('common.cancel') }}
          </t-button>
          <t-button theme="primary" @click="handleSubmitEntry" :loading="savingEntry">
            {{ editorMode === 'create' ? $t('knowledgeEditor.faq.editorCreate') : $t('common.save') }}
          </t-button>
        </div>
      </template>
    </t-drawer>

    <!-- Import Dialog -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="importVisible" class="faq-import-overlay" @click.self="importVisible = false">
          <div class="faq-import-modal">
            <!-- 关闭按钮 -->
            <button class="close-btn" @click="importVisible = false" :aria-label="$t('general.close')">
              <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
              </svg>
            </button>

            <div class="faq-import-container">
              <div class="faq-import-header">
                <h2 class="import-title">{{ $t('knowledgeEditor.faqImport.title') }}</h2>
              </div>

              <div class="faq-import-content">
                <!-- 导入模式选择 -->
                <div class="import-form-item">
                  <label class="import-form-label required">{{ $t('knowledgeEditor.faqImport.modeLabel') }}</label>
                  <t-radio-group v-model="importState.mode" class="import-radio-group">
                    <t-radio-button value="append">{{ $t('knowledgeEditor.faqImport.appendMode') }}</t-radio-button>
                    <t-radio-button value="replace">{{ $t('knowledgeEditor.faqImport.replaceMode') }}</t-radio-button>
                  </t-radio-group>
                </div>

                <!-- 文件上传区域 -->
                <div class="import-form-item">
                  <div class="file-label-row">
                    <label class="import-form-label required">{{ $t('knowledgeEditor.faqImport.fileLabel') }}</label>
                    <t-dropdown
                      :options="downloadExampleOptions"
                      placement="bottom-right"
                      trigger="click"
                      @click="handleDownloadExample"
                      class="download-example-dropdown"
                    >
                      <t-button theme="default" variant="outline" size="small" class="download-example-btn">
                        <t-icon name="download" size="16px" />
                        <span>{{ $t('knowledgeEditor.faqImport.downloadExample') }}</span>
                      </t-button>
                    </t-dropdown>
                  </div>
                  <div class="file-upload-wrapper">
                    <input
                      ref="fileInputRef"
                      type="file"
                      accept=".json,.csv,.xlsx,.xls"
                      @change="handleFileChange"
                      class="file-input-hidden"
                    />
                    <div
                      class="file-upload-area"
                      :class="{ 'has-file': importState.file }"
                      @click="fileInputRef?.click()"
                      @dragover.prevent
                      @dragenter.prevent
                      @drop.prevent="handleFileDrop"
                    >
                      <div class="file-upload-content">
                        <t-icon name="upload" size="32px" class="upload-icon" />
                        <div class="upload-text">
                          <span v-if="!importState.file" class="upload-primary-text">
                            {{ $t('knowledgeEditor.faqImport.clickToUpload') }}
                          </span>
                          <span v-else class="upload-file-name">
                            {{ importState.file.name }}
                          </span>
                          <span v-if="!importState.file" class="upload-secondary-text">
                            {{ $t('knowledgeEditor.faqImport.dragDropTip') }}
                          </span>
                        </div>
                      </div>
                    </div>
                    <p class="import-form-tip">{{ $t('knowledgeEditor.faqImport.fileTip') }}</p>
                  </div>
                </div>

                <!-- 预览区域 -->
                <div v-if="importState.preview.length" class="import-preview">
                  <div class="preview-header">
                    <t-icon name="file-view" size="16px" class="preview-icon" />
                    <span class="preview-title">
                      {{ $t('knowledgeEditor.faqImport.previewCount', { count: importState.preview.length }) }}
                    </span>
                  </div>
                  <div class="preview-list">
                    <div
                      v-for="(item, index) in importState.preview.slice(0, 5)"
                      :key="index"
                      class="preview-item"
                    >
                      <span class="preview-index">{{ index + 1 }}</span>
                      <span class="preview-question">{{ item.standard_question }}</span>
                    </div>
                  </div>
                  <p v-if="importState.preview.length > 5" class="preview-more">
                    {{ $t('knowledgeEditor.faqImport.previewMore', { count: importState.preview.length - 5 }) }}
                  </p>
                </div>

              </div>

              <div class="faq-import-footer">
                <t-button 
                  theme="default" 
                  variant="outline" 
                  @click="handleCancelImport"
                  :disabled="importState.importing && importState.taskStatus?.status === 'running'"
                >
                  {{ $t('common.cancel') }}
                </t-button>
                <t-button 
                  theme="primary" 
                  @click="handleImport" 
                  :loading="importState.importing && !importState.taskId"
                  :disabled="importState.taskStatus?.status === 'running'"
                >
                  {{ importState.taskStatus?.status === 'success' ? $t('common.close') : 
                     importState.taskStatus?.status === 'failed' ? '重试' :
                     $t('knowledgeEditor.faqImport.importButton') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

    <!-- Batch Tag Dialog -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="batchTagDialogVisible" class="batch-tag-overlay" @click.self="batchTagDialogVisible = false">
          <div class="batch-tag-modal">
            <!-- 关闭按钮 -->
            <button class="batch-tag-close-btn" @click="batchTagDialogVisible = false" :aria-label="$t('general.close')">
              <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
              </svg>
            </button>

            <div class="batch-tag-container">
              <div class="batch-tag-header">
                <h2 class="batch-tag-title">{{ $t('knowledgeEditor.faq.batchUpdateTag') }}</h2>
              </div>

              <div class="batch-tag-content">
                <div class="batch-tag-tip">
                  <t-icon name="info-circle" size="16px" class="tip-icon" />
                  <span>{{ $t('knowledgeEditor.faq.batchUpdateTagTip', { count: selectedRowKeys.length }) }}</span>
                </div>
                <t-form layout="vertical" class="batch-tag-form">
                  <t-form-item :label="$t('knowledgeBase.tagLabel')">
                    <t-select
                      v-model="batchTagValue"
                      :options="tagSelectOptions"
                      :placeholder="$t('knowledgeBase.tagPlaceholder')"
                      clearable
                      filterable
                      class="batch-tag-select"
                    >
                      <template #empty>
                        <div class="tag-select-empty">
                          {{ $t('knowledgeBase.noTags') }}
                        </div>
                      </template>
                    </t-select>
                  </t-form-item>
                </t-form>
              </div>

              <div class="batch-tag-footer">
                <t-button 
                  theme="default" 
                  variant="outline" 
                  @click="batchTagDialogVisible = false"
                >
                  {{ $t('common.cancel') }}
                </t-button>
                <t-button 
                  theme="primary" 
                  @click="handleBatchTag"
                >
                  {{ $t('common.confirm') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

    <!-- Search Test Drawer -->
    <t-drawer
      v-model:visible="searchDrawerVisible"
      :header="$t('knowledgeEditor.faq.searchTestTitle')"
      :close-btn="true"
      size="420px"
      placement="right"
      class="faq-search-drawer"
    >
      <div class="search-test-content">
        <t-form layout="vertical" class="search-form" :label-width="0">
          <div class="settings-group">
            <!-- 查询文本 -->
            <div class="setting-row vertical search-first-row">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.faq.queryLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.faq.queryPlaceholder') }}</p>
              </div>
              <div class="setting-control">
                <t-input
                  v-model="searchForm.query"
                  :placeholder="$t('knowledgeEditor.faq.queryPlaceholder')"
                  @keydown.enter.prevent="handleSearch"
                  class="full-width-input"
                />
              </div>
            </div>

            <!-- 相似度阈值 -->
            <div class="setting-row vertical">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.faq.similarityThresholdLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.faq.vectorThresholdDesc') }}</p>
              </div>
              <div class="setting-control">
                <div class="slider-wrapper">
                  <t-slider
                    v-model="searchForm.vectorThreshold"
                    :min="0"
                    :max="1"
                    :step="0.1"
                    :show-tooltip="true"
                    :format-tooltip="(val: number) => val.toFixed(2)"
                  />
                  <div class="slider-value">{{ searchForm.vectorThreshold.toFixed(2) }}</div>
                </div>
              </div>
            </div>

            <!-- 匹配数量 -->
            <div class="setting-row vertical">
              <div class="setting-info">
                <label>{{ $t('knowledgeEditor.faq.matchCountLabel') }}</label>
                <p class="desc">{{ $t('knowledgeEditor.faq.matchCountDesc') }}</p>
              </div>
              <div class="setting-control">
                <div class="slider-wrapper">
                  <t-slider
                    v-model="searchForm.matchCount"
                    :min="1"
                    :max="50"
                    :step="1"
                    :show-tooltip="true"
                  />
                  <div class="slider-value">{{ searchForm.matchCount }}</div>
                </div>
              </div>
            </div>

            <!-- 搜索按钮 -->
            <div class="setting-row vertical">
              <div class="setting-control">
                <t-button
                  theme="primary"
                  block
                  :loading="searching"
                  @click="handleSearch"
                  class="search-button"
                >
                  {{ searching ? $t('knowledgeEditor.faq.searching') : $t('knowledgeEditor.faq.searchButton') }}
                </t-button>
              </div>
            </div>
          </div>
        </t-form>

        <!-- Search Results -->
        <div v-if="searchResults.length > 0 || hasSearched" class="search-results">
          <div class="results-header">
            <span>{{ $t('knowledgeEditor.faq.searchResults') }} ({{ searchResults.length }})</span>
          </div>
          <div v-if="searchResults.length === 0" class="no-results">
            {{ $t('knowledgeEditor.faq.noResults') }}
          </div>
          <div v-else class="results-list">
            <div
              v-for="(result, index) in searchResults"
              :key="result.id"
              class="result-card"
              :class="{ 'expanded': result.expanded }"
            >
              <div class="result-header" @click="toggleResult(result)">
                <div class="result-question-wrapper">
                  <div class="result-main">
                    <div class="result-question">
                      <span class="result-index">{{ index + 1 }}.</span>
                      {{ result.standard_question }}
                    </div>
                    <div v-if="result.matched_question && result.matched_question !== result.standard_question" class="matched-question">
                      <span class="matched-label">{{ $t('knowledgeEditor.faq.matchedQuestion') }}:</span>
                      <span class="matched-text">{{ result.matched_question }}</span>
                    </div>
                  </div>
                  <div class="result-meta">
                    <t-tag size="small" variant="light-outline" class="score-tag">
                      {{ (result.score || 0).toFixed(3) }}
                    </t-tag>
                  </div>
                  <t-icon 
                    :name="result.expanded ? 'chevron-up' : 'chevron-down'" 
                    class="expand-icon"
                  />
                </div>
              </div>
              <Transition name="slide-down">
                <div v-if="result.expanded" class="result-body">
                  <div v-if="result.answers?.length" class="result-section">
                    <div class="section-label">{{ $t('knowledgeEditor.faq.answers') }}</div>
                    <div class="result-tags">
                      <t-tooltip
                        v-for="answer in result.answers"
                        :key="answer"
                        :content="answer"
                        placement="top"
                      >
                        <t-tag size="small" theme="success" variant="light" class="answer-tag">
                          {{ answer }}
                        </t-tag>
                      </t-tooltip>
                    </div>
                  </div>
                  <div v-if="result.similar_questions?.length" class="result-section">
                    <div class="section-label">{{ $t('knowledgeEditor.faq.similarQuestions') }}</div>
                    <div class="result-tags">
                      <t-tooltip
                        v-for="question in result.similar_questions"
                        :key="question"
                        :content="question"
                        placement="top"
                      >
                        <t-tag size="small" variant="light-outline" class="question-tag">
                          {{ question }}
                        </t-tag>
                      </t-tooltip>
                    </div>
                  </div>
                </div>
              </Transition>
            </div>
          </div>
        </div>
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, onMounted, computed, nextTick, onUnmounted, h } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { MessagePlugin, DialogPlugin, Icon as TIcon } from 'tdesign-vue-next'
import type { FormRules, FormInstanceFunctions } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  listFAQEntries,
  upsertFAQEntries,
  createFAQEntry,
  updateFAQEntry,
  updateFAQEntryFieldsBatch,
  deleteFAQEntries,
  searchFAQEntries,
  exportFAQEntries,
  listKnowledgeTags,
  updateFAQEntryTagBatch,
  createKnowledgeBaseTag,
  updateKnowledgeBaseTag,
  deleteKnowledgeBaseTag,
  getKnowledgeBaseById,
  listKnowledgeBases,
  getFAQImportProgress,
  updateFAQImportResultDisplayStatus,
} from '@/api/knowledge-base'
import * as XLSX from 'xlsx'
import Papa from 'papaparse'
import FAQTagTooltip from '@/components/FAQTagTooltip.vue'
import { useUIStore } from '@/stores/ui'

interface FAQEntry {
  id: number
  chunk_id: string
  knowledge_id: string
  knowledge_base_id: string
  tag_id?: number
  is_enabled: boolean
  is_recommended: boolean
  standard_question: string
  similar_questions: string[]
  negative_questions: string[]
  answers: string[]
  updated_at: string
  showMore?: boolean
  score?: number
  match_type?: string
  matched_question?: string
  expanded?: boolean
  similarCollapsed?: boolean
  negativeCollapsed?: boolean
  answersCollapsed?: boolean
}

interface FAQEntryPayload {
  standard_question: string
  similar_questions: string[]
  negative_questions: string[]
  answers: string[]
  tag_id?: number
  tag_name?: string
  is_enabled?: boolean
  is_recommended?: boolean
}

const props = defineProps<{
  kbId: string
}>()

const { t } = useI18n()
const router = useRouter()
const uiStore = useUIStore()

const loading = ref(false)
const loadingMore = ref(false)
const entries = ref<FAQEntry[]>([])
const entryStatusLoading = reactive<Record<number, boolean>>({})
const entryRecommendedLoading = reactive<Record<number, boolean>>({})
const selectedRowKeys = ref<number[]>([])
const scrollContainer = ref<HTMLElement | null>(null)
const cardListRef = ref<HTMLElement | null>(null)
const hasMore = ref(true)
const pageSize = 20
let currentPage = 1
const entrySearchKeyword = ref('')
let entrySearchDebounce: ReturnType<typeof setTimeout> | null = null
type TagInputInstance = ComponentPublicInstance<{ focus: () => void; select: () => void }>

const tagList = ref<any[]>([])
const tagLoading = ref(false)
const tagListRef = ref<HTMLElement | null>(null)
// Selected tag seq_id for filtering (0 means show all)
const selectedTagId = ref<number>(0)
const overallFAQTotal = ref(0)
const tagSearchQuery = ref('')
const TAG_PAGE_SIZE = 20
const tagPage = ref(1)
const tagHasMore = ref(false)
const tagLoadingMore = ref(false)
const tagTotal = ref(0)
let tagSearchDebounce: ReturnType<typeof setTimeout> | null = null
const editingTagInputRefs = new Map<string, TagInputInstance | null>()
const setEditingTagInputRef = (el: TagInputInstance | null, tagId: string) => {
  if (el) {
    editingTagInputRefs.set(tagId, el)
  } else {
    editingTagInputRefs.delete(tagId)
  }
}
const setEditingTagInputRefByTag = (tagId: string) => (el: TagInputInstance | null) => {
  setEditingTagInputRef(el, tagId)
}
const newTagInputRef = ref<TagInputInstance | null>(null)
const creatingTag = ref(false)
const creatingTagLoading = ref(false)
const newTagName = ref('')
const editingTagId = ref<string | null>(null)
const editingTagName = ref('')
const editingTagSubmitting = ref(false)
// tagMap uses seq_id as key for looking up by entry.tag_id
const tagMap = computed<Record<number, any>>(() => {
  const map: Record<number, any> = {}
  tagList.value.forEach((tag) => {
    map[tag.seq_id] = tag
  })
  return map
})
// tagMapById uses UUID as key for editing operations
const tagMapById = computed<Record<string, any>>(() => {
  const map: Record<string, any> = {}
  tagList.value.forEach((tag) => {
    map[tag.id] = tag
  })
  return map
})
// All tags are now regular tags (no pseudo-tag)
const regularTags = computed(() => tagList.value)
const tagDropdownOptions = computed(() =>
  regularTags.value.map((tag: any) => ({ content: tag.name, value: String(tag.seq_id) })),
)
const tagSelectOptions = computed(() =>
  regularTags.value.map((tag: any) => ({ label: tag.name, value: tag.seq_id })),
)
const sidebarCategoryCount = computed(() => tagList.value.length)
const filteredTags = computed(() => {
  const query = tagSearchQuery.value.trim().toLowerCase()
  if (!query) {
    return tagList.value
  }
  return tagList.value.filter((tag) => (tag.name || '').toLowerCase().includes(query))
})

const kbInfo = ref<any>(null)
const knowledgeList = ref<Array<{ id: string; name: string; type?: string }>>([])
const knowledgeDropdownOptions = computed(() =>
  knowledgeList.value
    .map((item) => ({
      content: item.name,
      value: item.id,
      prefixIcon: () => h(TIcon, { name: item.type === 'document' ? 'folder' : 'chat-bubble-help', size: '16px' }),
    })),
)

const loadKnowledgeInfo = async (kbId: string) => {
  if (!kbId) {
    kbInfo.value = null
    return
  }
  try {
    const res: any = await getKnowledgeBaseById(kbId)
    kbInfo.value = res?.data || null
    return kbInfo.value
  } catch (error) {
    console.error('Failed to load knowledge base info:', error)
    kbInfo.value = null
    return null
  }
}

const loadKnowledgeList = async () => {
  try {
    const res: any = await listKnowledgeBases()
    knowledgeList.value = (res?.data || []).map((item: any) => ({
      id: String(item.id),
      name: item.name,
      type: item.type,
    }))
  } catch (error) {
    console.error('Failed to load knowledge bases:', error)
  }
}

const editorVisible = ref(false)
const editorMode = ref<'create' | 'edit'>('create')
const currentEntryId = ref<number | null>(null)
const editorForm = reactive<FAQEntryPayload>({
  standard_question: '',
  similar_questions: [],
  negative_questions: [],
  answers: [],
  tag_id: undefined,
})
const editorFormRef = ref<FormInstanceFunctions>()
const savingEntry = ref(false)

// 输入框状态
const answerInput = ref('')
const similarInput = ref('')
const negativeInput = ref('')

const importVisible = ref(false)
const fileInputRef = ref<HTMLInputElement | null>(null)
const importState = reactive({
  mode: 'append' as 'append' | 'replace',
  file: null as File | null,
  preview: [] as FAQEntryPayload[],
  importing: false,
  taskId: null as string | null,
  taskStatus: null as {
    status: string
    progress: number
    total: number
    processed: number
    error?: string
  } | null,
  pollingInterval: null as ReturnType<typeof setInterval> | null,
})

// FAQ导入结果状态（持久化的）
const importResult = ref<{
  total_entries: number
  success_count: number
  failed_count: number
  skipped_count: number
  import_mode: string
  imported_at: string
  task_id: string
  processing_time: number
  failed_entries_url?: string
  success_entries?: Array<{
    index: number
    seq_id: number
    tag_id?: number
    tag_name?: string
    standard_question: string
  }>
  display_status: string
} | null>(null)

// Search test state
const searchDrawerVisible = ref(false)
const searching = ref(false)
const hasSearched = ref(false)
const searchResults = ref<FAQEntry[]>([])
const searchForm = reactive({
  query: '',
  vectorThreshold: 0.7,
  matchCount: 10,
})


// 标签列表滚动加载更多
const handleTagListScroll = () => {
  const container = tagListRef.value
  if (!container) return
  if (tagLoadingMore.value || !tagHasMore.value) return
  
  const { scrollTop, scrollHeight, clientHeight } = container
  // 距离底部 50px 时触发加载
  if (scrollTop + clientHeight >= scrollHeight - 50) {
    loadTags()
  }
}

const loadTags = async (reset = false) => {
  if (!props.kbId) {
    tagList.value = []
    tagTotal.value = 0
    tagHasMore.value = false
    tagPage.value = 1
    return
  }

  if (reset) {
    tagPage.value = 1
    tagList.value = []
    tagTotal.value = 0
    tagHasMore.value = false
  }

  const currentPage = tagPage.value || 1
  tagLoading.value = currentPage === 1
  tagLoadingMore.value = currentPage > 1

  try {
    const res: any = await listKnowledgeTags(props.kbId, {
      page: currentPage,
      page_size: TAG_PAGE_SIZE,
      keyword: tagSearchQuery.value || undefined,
    })
    const pageData = (res?.data || {}) as {
      data?: any[]
      total?: number
    }
    const pageTags = (pageData.data || []).map((tag: any) => ({
      ...tag,
      id: String(tag.id),
    }))

    if (currentPage === 1) {
      tagList.value = pageTags
    } else {
      tagList.value = [...tagList.value, ...pageTags]
    }

    tagTotal.value = pageData.total || tagList.value.length
    tagHasMore.value = tagList.value.length < tagTotal.value
    if (tagHasMore.value) {
      tagPage.value = currentPage + 1
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  } finally {
    tagLoading.value = false
    tagLoadingMore.value = false
  }
}

const getTagName = (tagId?: number) => {
  if (!tagId) return t('knowledgeBase.untagged') || '未分类'
  return tagMap.value[tagId]?.name || (t('knowledgeBase.untagged') || '未分类')
}

const handleTagFilterChange = (value: number) => {
  selectedTagId.value = value
}

const handleTagRowClick = (tagSeqId: number) => {
  if (editingTagId.value) {
    cancelEditTag()
  }
  if (creatingTag.value) {
    cancelCreateTag()
  }
  if (selectedTagId.value === tagSeqId) {
    return
  }
  handleTagFilterChange(tagSeqId)
}

const startCreateTag = () => {
  if (!props.kbId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  if (creatingTag.value) {
    return
  }
  cancelEditTag()
  creatingTag.value = true
  nextTick(() => {
    newTagInputRef.value?.focus?.()
    newTagInputRef.value?.select?.()
  })
}

const cancelCreateTag = () => {
  creatingTag.value = false
  newTagName.value = ''
}

const submitCreateTag = async () => {
  if (!props.kbId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  const name = newTagName.value.trim()
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'))
    return
  }
  creatingTagLoading.value = true
  try {
    await createKnowledgeBaseTag(props.kbId, { name })
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'))
    cancelCreateTag()
    await loadTags()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  } finally {
    creatingTagLoading.value = false
  }
}

const startEditTag = (tag: any) => {
  cancelCreateTag()
  editingTagId.value = tag.id
  editingTagName.value = tag.name
  nextTick(() => {
    const inputRef = editingTagInputRefs.get(tag.id)
    inputRef?.focus?.()
    inputRef?.select?.()
  })
}

const cancelEditTag = () => {
  editingTagId.value = null
  editingTagName.value = ''
}

const submitEditTag = async () => {
  if (!props.kbId || !editingTagId.value) {
    return
  }
  const name = editingTagName.value.trim()
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'))
    return
  }
  if (name === tagMapById.value[editingTagId.value]?.name) {
    cancelEditTag()
    return
  }
  editingTagSubmitting.value = true
  try {
    await updateKnowledgeBaseTag(props.kbId, editingTagId.value, { name })
    MessagePlugin.success(t('knowledgeBase.tagEditSuccess'))
    cancelEditTag()
    await loadTags()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  } finally {
    editingTagSubmitting.value = false
  }
}

const confirmDeleteTag = (tag: any) => {
  if (!props.kbId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  if (creatingTag.value) {
    cancelCreateTag()
  }
  if (editingTagId.value) {
    cancelEditTag()
  }
  const confirmDialog = DialogPlugin.confirm({
    header: t('knowledgeBase.tagDeleteTitle'),
    body: t('knowledgeBase.tagDeleteDesc', { name: tag.name }),
    confirmBtn: { content: t('common.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        await deleteKnowledgeBaseTag(props.kbId, tag.seq_id, { force: true })
        MessagePlugin.success(t('knowledgeBase.tagDeleteSuccess'))
        if (selectedTagId.value === tag.seq_id) {
          // Reset to show all entries when current tag is deleted
          selectedTagId.value = 0
          handleTagFilterChange(0)
        }
        await loadTags()
        await loadEntries()
        confirmDialog.hide()
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('common.operationFailed'))
      }
    },
  })
}

const handleEntryTagChange = async (entryId: number, value?: string) => {
  if (!props.kbId) return
  const targetEntry = entries.value.find((item) => item.id === entryId)
  const previousTagId = targetEntry ? targetEntry.tag_id : undefined
  const normalizedValue = value ? Number(value) : null
  if (normalizedValue === previousTagId) {
    return
  }
  try {
    await updateFAQEntryTagBatch(props.kbId, { updates: { [entryId]: normalizedValue } })
    MessagePlugin.success(t('knowledgeEditor.messages.updateSuccess'))
    await loadEntries()
    await loadTags()
  } catch (error: any) {
    if (targetEntry) {
      targetEntry.tag_id = previousTagId
    }
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  }
}

const handleNavigateToKbList = () => {
  router.push('/platform/knowledge-bases')
}

const handleNavigateToCurrentKB = () => {
  if (!props.kbId) return
  router.push(`/platform/knowledge-bases/${props.kbId}`)
}

const handleOpenKBSettings = () => {
  if (!props.kbId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  uiStore.openKBSettings(props.kbId)
}

const handleKnowledgeDropdownSelect = (data: { value: string }) => {
  if (!data?.value || data.value === props.kbId) return
  router.push(`/platform/knowledge-bases/${data.value}`)
}

const handleFaqMenuAction = (event: Event) => {
  const detail = (event as CustomEvent<{ action: string; kbId: string }>).detail
  if (!detail || detail.kbId !== props.kbId) return
  if (detail.action === 'create') {
    openEditor()
  } else if (detail.action === 'import') {
    openImportDialog()
  } else if (detail.action === 'search') {
    searchDrawerVisible.value = true
  } else if (detail.action === 'export') {
    handleExportCSV()
  } else if (detail.action === 'batch') {
    // 批量操作通过左侧菜单的下拉菜单处理
    if (selectedRowKeys.value.length === 0) {
      MessagePlugin.warning(t('knowledgeEditor.faq.selectEntriesFirst') || '请先选中要操作的FAQ条目')
    }
  } else if (detail.action === 'batchTag') {
    if (selectedRowKeys.value.length > 0) {
      openBatchTagDialog()
    }
  } else if (detail.action === 'batchEnable') {
    if (selectedRowKeys.value.length > 0) {
      handleBatchStatusChange(true)
    }
  } else if (detail.action === 'batchDisable') {
    if (selectedRowKeys.value.length > 0) {
      handleBatchStatusChange(false)
    }
  } else if (detail.action === 'batchDelete') {
    if (selectedRowKeys.value.length > 0) {
      handleBatchDelete()
    }
  }
}

const handleEntryStatusChange = async (entry: FAQEntry, value: boolean) => {
  if (!props.kbId) {
    return
  }
  const entryIndex = entries.value.findIndex(e => e.id === entry.id)
  if (entryIndex === -1) {
    return
  }
  // 从数组中获取实际的对象引用，确保使用最新的数据
  const actualEntry = entries.value[entryIndex]
  const previous = actualEntry.is_enabled
  if (previous === value) {
    return
  }
  // 直接更新属性，Vue 3 的响应式系统应该能够检测到
  actualEntry.is_enabled = value
  entryStatusLoading[entry.id] = true
  try {
    await updateFAQEntryFieldsBatch(props.kbId, { by_id: { [entry.id]: { is_enabled: value } } })
    MessagePlugin.success(t(value ? 'knowledgeEditor.faq.statusEnableSuccess' : 'knowledgeEditor.faq.statusDisableSuccess'))
  } catch (error: any) {
    // 失败时回滚
    actualEntry.is_enabled = previous
    MessagePlugin.error(error?.message || t('knowledgeEditor.faq.statusUpdateFailed'))
  } finally {
    entryStatusLoading[entry.id] = false
  }
}

const handleEntryRecommendedChange = async (entry: FAQEntry, value: boolean) => {
  if (entryRecommendedLoading[entry.id]) {
    return
  }
  const entryIndex = entries.value.findIndex(e => e.id === entry.id)
  if (entryIndex === -1) {
    return
  }
  const actualEntry = entries.value[entryIndex]
  const previous = actualEntry.is_recommended
  if (previous === value) {
    return
  }
  actualEntry.is_recommended = value
  entryRecommendedLoading[entry.id] = true
  try {
    await updateFAQEntryFieldsBatch(props.kbId, { by_id: { [entry.id]: { is_recommended: value } } })
    MessagePlugin.success(t(value ? 'knowledgeEditor.faq.recommendedEnableSuccess' : 'knowledgeEditor.faq.recommendedDisableSuccess'))
  } catch (error: any) {
    actualEntry.is_recommended = previous
    MessagePlugin.error(error?.message || t('knowledgeEditor.faq.recommendedUpdateFailed'))
  } finally {
    entryRecommendedLoading[entry.id] = false
  }
}

const editorRules: FormRules<FAQEntryPayload> = {
  standard_question: [
    { required: true, message: t('knowledgeEditor.messages.nameRequired') },
  ],
  answers: [
    {
      validator: (val: string[]) => Array.isArray(val) && val.length > 0,
      message: t('knowledgeEditor.faq.answerRequired'),
    },
  ],
}

const loadEntries = async (append = false) => {
  if (!props.kbId) return
  if (append) {
    loadingMore.value = true
  } else {
    loading.value = true
    currentPage = 1
    entries.value = []
    selectedRowKeys.value = []
    Object.keys(entryStatusLoading).forEach((key) => {
      delete entryStatusLoading[Number(key)]
    })
  }

  try {
    // If overallFAQTotal is not initialized, fetch it first (without tag_id filter)
    if (overallFAQTotal.value === 0 && !append) {
      const totalRes = await listFAQEntries(props.kbId, {
        page: 1,
        page_size: 1,
      })
      const totalData = (totalRes.data || {}) as { total: number }
      overallFAQTotal.value = totalData.total || 0
    }

    const res = await listFAQEntries(props.kbId, {
      page: currentPage,
      page_size: pageSize,
      tag_id: selectedTagId.value || undefined,
      keyword: entrySearchKeyword.value ? entrySearchKeyword.value.trim() : undefined,
    })
    const pageData = (res.data || {}) as {
      data: FAQEntry[]
      total: number
    }
    const newEntries = (pageData.data || []).map(entry => ({
      ...entry,
      showMore: false,
      similarCollapsed: true,  // 相似问默认折叠
      negativeCollapsed: true,  // 反例默认折叠
      answersCollapsed: true,   // 答案默认折叠
      is_enabled: entry.is_enabled !== false,
    }))
    
    if (append) {
      entries.value = [...entries.value, ...newEntries]
    } else {
      entries.value = newEntries
    }
    // 判断是否还有更多数据
    hasMore.value = entries.value.length < (pageData.total || 0)
    currentPage++
    
    // 等待 DOM 更新后重新布局
    await nextTick()
    arrangeCards()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  } finally {
    loading.value = false
    loadingMore.value = false
    
    // 检查是否需要继续加载以填满可视区域
    // 延迟执行以确保 arrangeCards 的 requestAnimationFrame 完成
    setTimeout(() => {
      checkAndLoadMore()
    }, 350)
  }
}

const handleScroll = () => {
  if (!scrollContainer.value || loadingMore.value || !hasMore.value) return

  const container = scrollContainer.value
  const scrollTop = container.scrollTop
  const scrollHeight = container.scrollHeight
  const clientHeight = container.clientHeight

  // 当滚动到距离底部 200px 时加载更多
  if (scrollTop + clientHeight >= scrollHeight - 200) {
    loadEntries(true)
  }
}

// 检查内容是否填满可视区域，如果没有且还有更多数据，继续加载
const checkAndLoadMore = () => {
  if (!scrollContainer.value) return
  if (loadingMore.value || loading.value) return
  if (!hasMore.value) return
  
  const container = scrollContainer.value
  const scrollHeight = container.scrollHeight
  const clientHeight = container.clientHeight
  
  // 如果内容高度小于容器高度 + 50px 的缓冲，说明可能没有滚动条或接近底部，需要继续加载
  if (scrollHeight <= clientHeight + 50) {
    loadEntries(true)
  }
}

const handleCardSelect = (entryId: number, checked: boolean) => {
  if (checked) {
    if (!selectedRowKeys.value.includes(entryId)) {
      selectedRowKeys.value.push(entryId)
    }
  } else {
    const index = selectedRowKeys.value.indexOf(entryId)
    if (index > -1) {
      selectedRowKeys.value.splice(index, 1)
    }
  }
}

const resetEditorForm = () => {
  editorForm.standard_question = ''
  editorForm.similar_questions = []
  editorForm.negative_questions = []
  editorForm.answers = []
  editorForm.tag_id = undefined
  answerInput.value = ''
  similarInput.value = ''
  negativeInput.value = ''
}

const openEditor = (entry?: FAQEntry) => {
  if (entry) {
    editorMode.value = 'edit'
    currentEntryId.value = entry.id
    editorForm.standard_question = entry.standard_question
    editorForm.similar_questions = [...(entry.similar_questions || [])]
    editorForm.negative_questions = [...(entry.negative_questions || [])]
    editorForm.answers = [...(entry.answers || [])]
    editorForm.tag_id = entry.tag_id || undefined
  } else {
    editorMode.value = 'create'
    currentEntryId.value = null
    resetEditorForm()
  }
  answerInput.value = ''
  similarInput.value = ''
  negativeInput.value = ''
  editorVisible.value = true
}

const handleEditorClose = () => {
  // 关闭时重置表单
  resetEditorForm()
  answerInput.value = ''
  similarInput.value = ''
  negativeInput.value = ''
  editorFormRef.value?.clearValidate?.()
}

// 添加答案
const addAnswer = () => {
  const trimmed = answerInput.value.trim()
  if (trimmed && editorForm.answers.length < 5 && !editorForm.answers.includes(trimmed)) {
    editorForm.answers.push(trimmed)
    answerInput.value = ''
  }
}

// 删除答案
const removeAnswer = (index: number) => {
  editorForm.answers.splice(index, 1)
}

// 添加相似问
const addSimilar = () => {
  const trimmed = similarInput.value.trim()
  if (trimmed && editorForm.similar_questions.length < 10 && !editorForm.similar_questions.includes(trimmed)) {
    editorForm.similar_questions.push(trimmed)
    similarInput.value = ''
  }
}

// 删除相似问
const removeSimilar = (index: number) => {
  editorForm.similar_questions.splice(index, 1)
}

// 添加反例
const addNegative = () => {
  const trimmed = negativeInput.value.trim()
  if (trimmed && editorForm.negative_questions.length < 10 && !editorForm.negative_questions.includes(trimmed)) {
    editorForm.negative_questions.push(trimmed)
    negativeInput.value = ''
  }
}

// 删除反例
const removeNegative = (index: number) => {
  editorForm.negative_questions.splice(index, 1)
}

const handleSubmitEntry = async () => {
  if (!editorFormRef.value) return
  const result = await editorFormRef.value.validate?.()
  if (result !== true) return

  savingEntry.value = true
  try {
    const payload: FAQEntryPayload = {
      standard_question: editorForm.standard_question,
      similar_questions: [...editorForm.similar_questions],
      negative_questions: [...editorForm.negative_questions],
      answers: [...editorForm.answers],
      tag_id: editorForm.tag_id || undefined,
    }
    if (editorMode.value === 'create') {
      await createFAQEntry(props.kbId, payload)
      MessagePlugin.success(t('knowledgeEditor.messages.createSuccess'))
    } else if (currentEntryId.value) {
      await updateFAQEntry(props.kbId, currentEntryId.value, payload)
      MessagePlugin.success(t('knowledgeEditor.messages.updateSuccess'))
    }
    editorVisible.value = false
    await loadEntries()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  } finally {
    savingEntry.value = false
  }
}

const handleBatchDelete = async () => {
  if (!selectedRowKeys.value.length) return
  try {
    await deleteFAQEntries(props.kbId, selectedRowKeys.value)
    MessagePlugin.success(t('knowledgeEditor.faqImport.deleteSuccess'))
    selectedRowKeys.value = []
    await loadEntries()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  }
}

// 批量状态更新对话框
const batchTagDialogVisible = ref(false)
const batchTagValue = ref<string>('')

const openBatchTagDialog = () => {
  if (!selectedRowKeys.value.length) return
  batchTagValue.value = ''
  batchTagDialogVisible.value = true
}

const handleBatchTag = async () => {
  if (!selectedRowKeys.value.length || !props.kbId) return
  try {
    const updates: Record<number, number | null> = {}
    selectedRowKeys.value.forEach(id => {
      updates[id] = batchTagValue.value ? Number(batchTagValue.value) : null
    })
    await updateFAQEntryTagBatch(props.kbId, { updates })
    MessagePlugin.success(t('knowledgeEditor.messages.updateSuccess'))
    batchTagDialogVisible.value = false
    selectedRowKeys.value = []
    await loadEntries()
    await loadTags()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  }
}

const handleBatchStatusChange = async (isEnabled: boolean) => {
  if (!selectedRowKeys.value.length || !props.kbId) return
  try {
    const by_id: Record<number, { is_enabled: boolean }> = {}
    selectedRowKeys.value.forEach(id => {
      by_id[id] = { is_enabled: isEnabled }
    })
    await updateFAQEntryFieldsBatch(props.kbId, { by_id })
    MessagePlugin.success(t(isEnabled ? 'knowledgeEditor.faq.statusEnableSuccess' : 'knowledgeEditor.faq.statusDisableSuccess'))
    selectedRowKeys.value = []
    await loadEntries()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  }
}

const handleBatchRecommendedChange = async (isRecommended: boolean) => {
  if (!selectedRowKeys.value.length || !props.kbId) return
  try {
    const by_id: Record<number, { is_recommended: boolean }> = {}
    selectedRowKeys.value.forEach(id => {
      by_id[id] = { is_recommended: isRecommended }
    })
    await updateFAQEntryFieldsBatch(props.kbId, { by_id })
    MessagePlugin.success(t(isRecommended ? 'knowledgeEditor.faq.recommendedEnableSuccess' : 'knowledgeEditor.faq.recommendedDisableSuccess'))
    selectedRowKeys.value = []
    await loadEntries()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  }
}

const handleMenuEdit = (entry: FAQEntry) => {
  entry.showMore = false
  openEditor(entry)
}

const handleMenuDelete = async (entry: FAQEntry) => {
  entry.showMore = false
  try {
    await deleteFAQEntries(props.kbId, [entry.id])
    MessagePlugin.success(t('knowledgeEditor.faqImport.deleteSuccess'))
    await loadEntries()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  }
}

const openImportDialog = () => {
  // 如果正在导入，不允许打开导入对话框
  if (importState.taskStatus?.status === 'running') {
    MessagePlugin.warning('导入正在进行中，请等待完成后再试')
    return
  }
  stopPolling()
  importVisible.value = true
  importState.file = null
  importState.preview = []
  importState.mode = 'append'
  // 注意：不清除taskId和taskStatus，以便在关闭对话框后仍能看到进度
  importState.importing = false
}

const processFile = async (file: File) => {
  importState.file = file

  try {
    let parsed: FAQEntryPayload[] = []
    if (file.name.endsWith('.json')) {
      parsed = await parseJSONFile(file)
    } else if (file.name.endsWith('.csv')) {
      parsed = await parseCSVFile(file)
    } else if (file.name.endsWith('.xlsx') || file.name.endsWith('.xls')) {
      parsed = await parseExcelFile(file)
    } else {
      MessagePlugin.warning(t('knowledgeEditor.faqImport.unsupportedFormat'))
      importState.preview = []
      return
    }
    importState.preview = parsed
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeEditor.faqImport.parseFailed'))
    importState.preview = []
  }
}

const handleFileChange = async (event: Event) => {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file) return
  await processFile(file)
}

const handleFileDrop = async (event: DragEvent) => {
  const file = event.dataTransfer?.files[0]
  if (!file) return
  await processFile(file)
}

const parseJSONFile = async (file: File): Promise<FAQEntryPayload[]> => {
  const text = await file.text()
  const data = JSON.parse(text)
  if (!Array.isArray(data)) {
    throw new Error(t('knowledgeEditor.faqImport.invalidJSON'))
  }
  return data.map(normalizePayload)
}

const parseCSVFile = async (file: File): Promise<FAQEntryPayload[]> => {
  const text = await file.text()
  
  // 使用 papaparse 解析 CSV，自动处理引号、转义、分隔符等
  return new Promise((resolve, reject) => {
    Papa.parse(text, {
      header: true,
      skipEmptyLines: true,
      delimiter: '', // 自动检测分隔符（逗号或制表符）
      quoteChar: '"',
      escapeChar: '"',
      transformHeader: (header: string) => {
        // 移除字段名中的括号和说明，只保留核心字段名
        const cleaned = header.trim()
          .replace(/\([^)]*\)/g, '') // 移除括号及内容
          .trim()
        // 对于中文字段名，不转换为小写；对于英文字段名，转换为小写
        return /[\u4e00-\u9fa5]/.test(cleaned) ? cleaned : cleaned.toLowerCase()
      },
      complete: (results) => {
        try {
          const payloads: FAQEntryPayload[] = []
          results.data.forEach((row: any) => {
            const record: Record<string, string> = {}
            // 将行数据转换为记录对象
            Object.keys(row).forEach((key) => {
              record[key] = String(row[key] || '').trim()
            })
            
            const isDisabled = parseBooleanField(record['是否停用'], false)
            payloads.push(
              normalizePayload({
                standard_question: record['问题'] || record['standard_question'] || record['question'] || '',
                answers: splitByDelimiter(record['机器人回答'] || record['answers']),
                similar_questions: splitByDelimiter(record['相似问题'] || record['similar_questions']),
                negative_questions: splitByDelimiter(record['反例问题'] || record['negative_questions']),
                tag_id: record['tag_id'] ? Number(record['tag_id']) : undefined,
                tag_name: record['分类'] || record['tag_name'] || '',
                is_enabled: isDisabled !== undefined ? !isDisabled : undefined, // 是否停用：FALSE表示启用，TRUE表示停用，所以取反
              }),
            )
          })
          resolve(payloads)
        } catch (error) {
          reject(error)
        }
      },
      error: (error: Error) => {
        reject(new Error(`CSV解析失败: ${error.message}`))
      },
    })
  })
}

const parseExcelFile = async (file: File): Promise<FAQEntryPayload[]> => {
  const data = await file.arrayBuffer()
  const workbook = XLSX.read(data, { type: 'array' })
  const sheetName = workbook.SheetNames[0]
  const worksheet = workbook.Sheets[sheetName]
  // 使用 raw: false 确保正确处理引号和转义
  const json = XLSX.utils.sheet_to_json<Record<string, string>>(worksheet, { 
    defval: '',
    raw: false // 确保字符串值被正确解析
  })
  return json.map((row) => {
    // 获取原始表头（去除括号说明）
    const normalizedRow: Record<string, string> = {}
    Object.keys(row).forEach((key) => {
      const normalizedKey = key.trim()
        .replace(/\([^)]*\)/g, '') // 移除括号及内容
        .trim()
      // 对于中文字段名，不转换为小写；对于英文字段名，转换为小写
      const finalKey = /[\u4e00-\u9fa5]/.test(normalizedKey) ? normalizedKey : normalizedKey.toLowerCase()
      // 确保值是字符串类型
      normalizedRow[finalKey] = String(row[key] || '').trim()
    })
    
    const isDisabled = parseBooleanField(normalizedRow['是否停用'], false)
    return normalizePayload({
      standard_question: normalizedRow['问题'] || normalizedRow['standard_question'] || normalizedRow['question'] || '',
      answers: splitByDelimiter(normalizedRow['机器人回答'] || normalizedRow['answers']),
      similar_questions: splitByDelimiter(normalizedRow['相似问题'] || normalizedRow['similar_questions']),
      negative_questions: splitByDelimiter(normalizedRow['反例问题'] || normalizedRow['negative_questions']),
      tag_id: normalizedRow['tag_id'] ? Number(normalizedRow['tag_id']) : undefined,
      tag_name: normalizedRow['分类'] || normalizedRow['tag_name'] || '',
      is_enabled: isDisabled !== undefined ? !isDisabled : undefined, // 是否停用：FALSE表示启用，TRUE表示停用，所以取反
    })
  })
}

const splitByDelimiter = (value?: string) => {
  if (!value) return []
  // 只使用 ## 作为分隔符，避免错误分割包含逗号、分号等内容
  const trimmedValue = value.trim()
  if (!trimmedValue) return []
  
  // 如果包含 ## 分隔符，按 ## 分割
  if (trimmedValue.includes('##')) {
    return trimmedValue
      .split('##')
      .map(item => item.trim())
      .filter(Boolean)
  }
  
  // 如果没有 ## 分隔符，整个值作为一个答案
  return [trimmedValue]
}

// 解析布尔字段（支持多种格式：TRUE/FALSE, true/false, 是/否, 1/0等）
const parseBooleanField = (value?: string, defaultValue: boolean = true): boolean | undefined => {
  if (!value) return undefined
  const normalized = value.trim().toUpperCase()
  if (normalized === 'TRUE' || normalized === '1' || normalized === '是' || normalized === 'YES') {
    return true
  }
  if (normalized === 'FALSE' || normalized === '0' || normalized === '否' || normalized === 'NO') {
    return false
  }
  return defaultValue
}

const normalizePayload = (payload: Partial<FAQEntryPayload>): FAQEntryPayload => ({
  standard_question: payload.standard_question || '',
  answers: payload.answers?.filter(Boolean) || [],
  similar_questions: payload.similar_questions?.filter(Boolean) || [],
  negative_questions: payload.negative_questions?.filter(Boolean) || [],
  tag_id: payload.tag_id || undefined,
  tag_name: payload.tag_name || '',
  is_enabled: payload.is_enabled !== undefined ? payload.is_enabled : undefined,
})

const stopPolling = () => {
  if (importState.pollingInterval) {
    clearInterval(importState.pollingInterval)
    importState.pollingInterval = null
  }
}

const startPolling = (taskId: string) => {
  stopPolling()
  // 保存taskId到localStorage，以便刷新后恢复
  saveTaskIdToStorage(taskId)
  
  // 记录上次已处理数量，用于判断是否需要刷新列表
  let lastProcessed = 0
  
  importState.pollingInterval = setInterval(async () => {
    try {
      const res: any = await getFAQImportProgress(taskId)
      const progressData = res?.data
      if (progressData) {
        // 从Redis进度数据中提取状态
        // status: "pending" -> "pending", "processing" -> "running", "completed" -> "success", "failed" -> "failed"
        let status = progressData.status
        if (status === 'processing') {
          status = 'running'
        } else if (status === 'completed') {
          status = 'success'
        }
        
        const progress = progressData.progress || 0
        const total = progressData.total || 0
        const processed = progressData.processed || 0
        const error = progressData.error || ''
        
        importState.taskStatus = {
          status: status,
          progress: progress,
          total: total,
          processed: processed,
          error: error,
        }

        // 进度更新时刷新FAQ列表（每增加一些条目就刷新一次）
        if (processed > lastProcessed) {
          lastProcessed = processed
          await loadEntries()
          await loadTags()
        }

        // 任务完成或失败，停止轮询（但不自动关闭进度条，让用户手动关闭）
        if (status === 'success' || status === 'failed') {
          stopPolling()
          if (status === 'success') {
            // 保存已完成的 taskId 用于后续加载结果
            if (importState.taskId) {
              saveLastCompletedTaskId(importState.taskId)
            }
            MessagePlugin.success(t('knowledgeEditor.faqImport.importSuccess'))
            // 清除筛选条件，确保用户能看到所有新导入的数据
            selectedTagId.value = 0
            entrySearchKeyword.value = ''
            overallFAQTotal.value = 0  // Reset to trigger re-fetch
            await loadEntries()
            await loadTags()
            await loadImportResult() // 加载最新的导入结果统计
            // 任务完成后，3秒后自动关闭进度条
            setTimeout(() => {
              if (importState.taskStatus?.status === 'success') {
                handleCloseProgress()
              }
            }, 3000)
          } else {
            MessagePlugin.error(error || t('common.operationFailed'))
            // 失败时不自动关闭，让用户看到错误信息
          }
        }
      }
    } catch (error: any) {
      console.error('Failed to poll task status:', error)
      // 如果任务不存在或已过期，清除存储
      if (error?.response?.status === 404 || error?.message?.includes('not found')) {
        clearTaskIdFromStorage()
        stopPolling()
        importState.taskId = null
        importState.taskStatus = null
      }
    }
  }, 3000) // 每3秒轮询一次
}

const handleCancelImport = () => {
  stopPolling()
  importState.importing = false
  importState.taskId = null
  importState.taskStatus = null
  importVisible.value = false
  // 注意：不清除localStorage，因为任务可能还在进行中
}

const handleCloseProgress = () => {
  stopPolling()
  importState.taskId = null
  importState.taskStatus = null
  clearTaskIdFromStorage()
}

// localStorage相关函数
const getStorageKey = () => {
  return `faq_import_task_${props.kbId}`
}

const saveTaskIdToStorage = (taskId: string) => {
  if (!props.kbId) return
  try {
    localStorage.setItem(getStorageKey(), taskId)
  } catch (error) {
    console.error('Failed to save taskId to localStorage:', error)
  }
}

const getTaskIdFromStorage = (): string | null => {
  if (!props.kbId) return null
  try {
    return localStorage.getItem(getStorageKey())
  } catch (error) {
    console.error('Failed to get taskId from localStorage:', error)
    return null
  }
}

const clearTaskIdFromStorage = () => {
  if (!props.kbId) return
  try {
    localStorage.removeItem(getStorageKey())
  } catch (error) {
    console.error('Failed to clear taskId from localStorage:', error)
  }
}

// 恢复导入任务状态（用于刷新后恢复）
const restoreImportTask = async () => {
  if (!props.kbId) return
  
  const savedTaskId = getTaskIdFromStorage()
  if (!savedTaskId) return

  try {
    // 查询Redis中的进度状态
    const res: any = await getFAQImportProgress(savedTaskId)
    const progressData = res?.data
    
    if (progressData) {
      // 从Redis进度数据中提取状态
      let status = progressData.status
      if (status === 'processing') {
        status = 'running'
      } else if (status === 'completed') {
        status = 'success'
      }
      
      const progress = progressData.progress || 0
      const total = progressData.total || 0
      const processed = progressData.processed || 0
      const error = progressData.error || ''
      
      importState.taskId = savedTaskId
      importState.taskStatus = {
        status: status,
        progress: progress,
        total: total,
        processed: processed,
        error: error,
      }
      
      // 如果任务还在进行中，恢复轮询
      if (status === 'pending' || status === 'running') {
        startPolling(savedTaskId)
      } else {
        // 任务已完成或失败，清除存储
        clearTaskIdFromStorage()
      }
    } else {
      // 任务不存在，清除存储
      clearTaskIdFromStorage()
    }
  } catch (error: any) {
    console.error('Failed to restore import task:', error)
    // 如果任务不存在或已过期，清除存储
    if (error?.response?.status === 404 || error?.message?.includes('not found')) {
      clearTaskIdFromStorage()
    }
  }
}

// localStorage key for last completed task
const getLastCompletedTaskKey = () => {
  return `faq_import_last_completed_${props.kbId}`
}

const saveLastCompletedTaskId = (taskId: string) => {
  if (!props.kbId) return
  try {
    localStorage.setItem(getLastCompletedTaskKey(), taskId)
  } catch (error) {
    console.error('Failed to save last completed taskId:', error)
  }
}

const getLastCompletedTaskId = (): string | null => {
  if (!props.kbId) return null
  try {
    return localStorage.getItem(getLastCompletedTaskKey())
  } catch (error) {
    return null
  }
}

// 加载持久化的导入结果统计
const loadImportResult = async () => {
  if (!props.kbId) return
  
  const lastTaskId = getLastCompletedTaskId()
  if (!lastTaskId) {
    importResult.value = null
    return
  }
  
  try {
    const res: any = await getFAQImportProgress(lastTaskId)
    const data = res?.data
    if (data && data.status === 'completed') {
      // 检查后端返回的 display_status，如果是 close 则不显示
      if (data.display_status === 'close') {
        importResult.value = null
        return
      }
      // Map progress fields to importResult format
      importResult.value = {
        total_entries: data.total,
        success_count: data.success_count,
        failed_count: data.failed_count,
        skipped_count: data.skipped_count || 0,
        import_mode: data.import_mode || 'append',
        imported_at: data.imported_at,
        task_id: data.task_id,
        failed_entries_url: data.failed_entries_url,
        success_entries: data.success_entries,
        display_status: data.display_status || 'open',
        processing_time: data.processing_time || 0,
      }
    } else {
      importResult.value = null
    }
  } catch (error) {
    console.error('Failed to load FAQ import result:', error)
    importResult.value = null
  }
}

// 关闭导入结果统计卡片
const closeImportResult = async () => {
  if (!props.kbId) return
  try {
    await updateFAQImportResultDisplayStatus(props.kbId, 'close')
    if (importResult.value) {
      importResult.value.display_status = 'close'
    }
  } catch (error) {
    console.error('Failed to close import result:', error)
  }
}

// 下载失败条目原因
const downloadFailedEntries = () => {
  if (!importResult.value?.failed_entries_url) {
    MessagePlugin.warning('暂无失败记录可下载')
    return
  }
  // 直接打开下载链接
  window.open(importResult.value.failed_entries_url, '_blank')
}

// 格式化导入时间
const formatImportTime = (timeStr?: string) => {
  if (!timeStr) return ''
  try {
    const date = new Date(timeStr)
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
  } catch (e) {
    return timeStr
  }
}

const handleImport = async () => {
  if (!importState.file || !importState.preview.length) {
    MessagePlugin.warning(t('knowledgeEditor.faqImport.selectFile'))
    return
  }

  // 如果任务已完成或失败，关闭对话框
  if (importState.taskStatus?.status === 'success' || importState.taskStatus?.status === 'failed') {
    if (importState.taskStatus.status === 'success') {
      handleCancelImport()
    } else {
      // 失败时重试
      importState.taskId = null
      importState.taskStatus = null
      importState.importing = false
    }
    return
  }

  importState.importing = true
  try {
    const res: any = await upsertFAQEntries(props.kbId, {
      entries: importState.preview,
      mode: importState.mode,
    })
    
    const taskId = res?.data?.task_id
    if (taskId) {
      importState.taskId = taskId
      importState.taskStatus = {
        status: 'pending',
        progress: 0,
        total: importState.preview.length,
        processed: 0,
      }
      // 开始轮询任务状态
      startPolling(taskId)
      // 立即关闭导入对话框，进度将在列表页面顶部显示
      importVisible.value = false
      // 重置导入对话框状态（但保留taskId和taskStatus用于进度显示）
      importState.file = null
      importState.preview = []
      importState.importing = false
    } else {
      // 如果没有返回任务ID，可能是旧版本API，使用同步方式
      MessagePlugin.success(t('knowledgeEditor.faqImport.importSuccess'))
      importVisible.value = false
      await loadEntries()
      importState.importing = false
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
    importState.importing = false
    stopPolling()
  }
}

// 监听选中数量变化，通知左侧菜单
watch(selectedRowKeys, (newKeys, oldKeys) => {
  const count = newKeys.length
  // 获取选中条目的状态信息
  const selectedEntries = entries.value.filter(entry => newKeys.includes(entry.id))
  const enabledCount = selectedEntries.filter(entry => entry.is_enabled !== false).length
  const disabledCount = count - enabledCount
  
  const event = new CustomEvent('faqSelectionChanged', {
    detail: { 
      count,
      enabledCount,
      disabledCount
    }
  })
  window.dispatchEvent(event)
}, { immediate: true, deep: true })

// 组件卸载时清理轮询
onUnmounted(() => {
  stopPolling()
})

// 下载示例文件选项
const downloadExampleOptions = computed(() => [
  { content: t('knowledgeEditor.faqImport.downloadExampleJSON'), value: 'json' },
  { content: t('knowledgeEditor.faqImport.downloadExampleCSV'), value: 'csv' },
  { content: t('knowledgeEditor.faqImport.downloadExampleExcel'), value: 'excel' },
])

// 示例数据
const exampleData: FAQEntryPayload[] = [
  {
    standard_question: '什么是 WeKnora？',
    answers: ['WeKnora 是一个智能知识库管理系统', '它支持多种知识库类型和导入方式'],
    similar_questions: ['WeKnora 是什么？', '介绍一下 WeKnora'],
    negative_questions: ['这不是 WeKnora', '与 WeKnora 无关'],
    tag_name: '产品介绍',
  },
  {
    standard_question: '如何创建知识库？',
    answers: ['点击"新建知识库"按钮', '选择知识库类型并填写相关信息', '完成创建后即可开始使用'],
    similar_questions: ['怎么创建知识库？', '如何新建知识库？'],
    negative_questions: [],
    tag_name: '使用指南',
  },
]

// 下载示例文件
const handleDownloadExample = (data: { value: string }) => {
  const { value } = data
  switch (value) {
    case 'json':
      downloadJSONExample()
      break
    case 'csv':
      downloadCSVExample()
      break
    case 'excel':
      downloadExcelExample()
      break
  }
}

// 下载 JSON 示例
const downloadJSONExample = () => {
  const jsonStr = JSON.stringify(exampleData, null, 2)
  const blob = new Blob([jsonStr], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'faq_example.json'
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

// 下载 CSV 示例
const downloadCSVExample = () => {
  const headers = ['分类(必填)', '问题(必填)', '相似问题(选填-多个用##分隔)', '反例问题(选填-多个用##分隔)', '机器人回答(必填-多个用##分隔)', '是否全部回复(选填-默认FALSE)', '是否停用(选填-默认FALSE)', '是否禁止被推荐(选填-默认False 可被推荐)']
  const rows = exampleData.map((item) => {
    return [
      item.tag_name || '', // 分类
      item.standard_question,
      item.similar_questions.join('##'),
      item.negative_questions.join('##'),
      item.answers.join('##'),
      'FALSE', // 是否全部回复
      'FALSE', // 是否停用
      'FALSE', // 是否禁止被推荐
    ]
  })
  const csvContent = [
    headers.join('\t'), // 使用制表符分隔
    ...rows.map((row) => row.map((cell) => {
      // 如果包含制表符、换行符或引号，需要用引号包裹
      if (cell.includes('\t') || cell.includes('\n') || cell.includes('"')) {
        return `"${cell.replace(/"/g, '""')}"`
      }
      return cell
    }).join('\t')),
  ].join('\n')
  const blob = new Blob(['\ufeff' + csvContent], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'faq_example.csv'
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

// 下载 Excel 示例
const downloadExcelExample = () => {
  const worksheet = XLSX.utils.json_to_sheet(
    exampleData.map((item) => ({
      '分类(必填)': item.tag_name || '',
      '问题(必填)': item.standard_question,
      '相似问题(选填-多个用##分隔)': item.similar_questions.join('##'),
      '反例问题(选填-多个用##分隔)': item.negative_questions.join('##'),
      '机器人回答(必填-多个用##分隔)': item.answers.join('##'),
      '是否全部回复(选填-默认FALSE)': 'FALSE',
      '是否停用(选填-默认FALSE)': 'FALSE',
      '是否禁止被推荐(选填-默认False 可被推荐)': 'FALSE',
    })),
  )
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, worksheet, 'FAQ')
  XLSX.writeFile(workbook, 'faq_example.xlsx')
}

// 导出 FAQ 数据为 CSV
const exportLoading = ref(false)
const handleExportCSV = async () => {
  if (!props.kbId) {
    MessagePlugin.warning(t('knowledgeBase.selectKnowledgeBase') || '请先选择知识库')
    return
  }
  
  exportLoading.value = true
  try {
    const blob = await exportFAQEntries(props.kbId)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `faq_export_${new Date().toISOString().slice(0, 10)}.csv`
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
    MessagePlugin.success(t('knowledgeEditor.faqExport.exportSuccess') || '导出成功')
  } catch (error: any) {
    console.error('Export failed:', error)
    MessagePlugin.error(t('knowledgeEditor.faqExport.exportFailed') || '导出失败')
  } finally {
    exportLoading.value = false
  }
}

watch(
  () => props.kbId,
  async (newKbId) => {
    currentPage = 1
    hasMore.value = true
    selectedTagId.value = 0
    overallFAQTotal.value = 0  // Reset to trigger re-fetch
    cancelCreateTag()
    cancelEditTag()
    tagSearchQuery.value = ''

    if (!newKbId) {
      kbInfo.value = null
      // kbId变化时，清除之前的任务状态
      stopPolling()
      importState.taskId = null
      importState.taskStatus = null
      clearTaskIdFromStorage()
      return
    }

    const info = await loadKnowledgeInfo(newKbId)
    if (!info || info.type !== 'faq') {
      return
    }

    loadEntries()
    loadTags(true)
    // 恢复导入任务状态（如果存在）
    await restoreImportTask()
  },
  { immediate: true },
)

watch(selectedTagId, (newVal, oldVal) => {
  if (oldVal === undefined) return
  if (newVal !== oldVal) {
    currentPage = 1
    entries.value = []
    selectedRowKeys.value = []
    loadEntries()
  }
})

watch(tagSearchQuery, (newVal, oldVal) => {
  if (newVal === oldVal) return
  if (tagSearchDebounce) {
    clearTimeout(tagSearchDebounce)
  }
  tagSearchDebounce = window.setTimeout(() => {
    loadTags(true)
  }, 300)
})

// 监听FAQ搜索关键词变化
watch(entrySearchKeyword, (newVal, oldVal) => {
  if (newVal === oldVal) return
  if (entrySearchDebounce) {
    clearTimeout(entrySearchDebounce)
  }
  entrySearchDebounce = window.setTimeout(() => {
    loadEntries()
  }, 300)
})

const handleSearch = async () => {
  if (!searchForm.query.trim()) {
    MessagePlugin.warning(t('knowledgeEditor.faq.queryPlaceholder'))
    return
  }

  searching.value = true
  hasSearched.value = true
  try {
    const res = await searchFAQEntries(props.kbId, {
      query_text: searchForm.query.trim(),
      vector_threshold: searchForm.vectorThreshold,
      match_count: searchForm.matchCount,
    })
    const results = (res.data || []).map((entry: FAQEntry) => ({
      ...entry,
      similarCollapsed: true,  // 相似问默认折叠
      negativeCollapsed: true,  // 反例默认折叠
      answersCollapsed: true,   // 答案默认折叠
      expanded: false,
    })) as FAQEntry[]
    
    // 按score从大到小排序
    searchResults.value = results.sort((a, b) => (b.score || 0) - (a.score || 0))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'))
    searchResults.value = []
  } finally {
    searching.value = false
  }
}

const getMatchTypeLabel = (matchType?: string) => {
  if (!matchType) return ''
  if (matchType === 'embedding') {
    return t('knowledgeEditor.faq.matchTypeEmbedding')
  }
  if (matchType === 'keywords') {
    return t('knowledgeEditor.faq.matchTypeKeywords')
  }
  return matchType
}

const toggleResult = (result: FAQEntry) => {
  result.expanded = !result.expanded
}

// 防抖函数
let arrangeCardsTimer: ReturnType<typeof setTimeout> | null = null
const debounceArrangeCards = (delay = 100) => {
  if (arrangeCardsTimer) {
    clearTimeout(arrangeCardsTimer)
  }
  arrangeCardsTimer = setTimeout(() => {
    arrangeCards()
    arrangeCardsTimer = null
  }, delay)
}

// 瀑布流布局函数 - 优化版本，避免闪烁
const arrangeCards = () => {
  if (!cardListRef.value) return
  
  const cards = cardListRef.value.querySelectorAll('.faq-card') as NodeListOf<HTMLElement>
  if (cards.length === 0) return
  
  // 获取容器宽度和列数
  const containerWidth = cardListRef.value.offsetWidth
  const gap = 12 // 与 CSS gap 保持一致
  let columnCount = 1
  
  // 根据容器宽度计算列数（增加每行的卡片数量）
  if (containerWidth >= 2560) columnCount = 12
  else if (containerWidth >= 1920) columnCount = 10
  else if (containerWidth >= 1536) columnCount = 8
  else if (containerWidth >= 1280) columnCount = 6
  else if (containerWidth >= 1024) columnCount = 5
  else if (containerWidth >= 768) columnCount = 4
  else if (containerWidth >= 640) columnCount = 3
  
  const columnWidth = (containerWidth - (gap * (columnCount - 1))) / columnCount
  
  // 初始化每列的高度数组
  const columnHeights = new Array(columnCount).fill(0)
  
  // 使用 requestAnimationFrame 优化性能
  requestAnimationFrame(() => {
    // 先设置宽度，保持当前位置不变
    cards.forEach((card) => {
      // 确保卡片是绝对定位
      if (card.style.position !== 'absolute') {
        card.style.position = 'absolute'
      }
      // 设置宽度以便正确计算高度
      card.style.width = `${columnWidth}px`
    })
    
    // 等待浏览器重新计算布局
    requestAnimationFrame(() => {
      // 计算所有卡片的高度（不改变位置）
      const cardHeights: number[] = []
      cards.forEach((card) => {
        const height = card.offsetHeight || card.getBoundingClientRect().height
        cardHeights.push(height)
      })
      
      // 计算新位置
      const newPositions: Array<{ top: number; left: number }> = []
      cardHeights.forEach((height) => {
        const shortestColumnIndex = columnHeights.indexOf(Math.min(...columnHeights))
        const top = columnHeights[shortestColumnIndex]
        const left = shortestColumnIndex * (columnWidth + gap)
        
        newPositions.push({ top, left })
        columnHeights[shortestColumnIndex] += height + gap
      })
      
      // 批量更新所有卡片位置，使用CSS过渡实现平滑移动
      cards.forEach((card, index) => {
        const { top, left } = newPositions[index]
        const currentTop = parseFloat(card.style.top) || 0
        const currentLeft = parseFloat(card.style.left) || 0
        
        // 如果位置发生变化，添加过渡效果
        if (Math.abs(currentTop - top) > 1 || Math.abs(currentLeft - left) > 1) {
          // 使用 will-change 提示浏览器优化
          card.style.willChange = 'top, left'
          card.style.transition = 'top 0.3s cubic-bezier(0.4, 0, 0.2, 1), left 0.3s cubic-bezier(0.4, 0, 0.2, 1)'
        }
        
        card.style.position = 'absolute'
        card.style.top = `${top}px`
        card.style.left = `${left}px`
        card.style.width = `${columnWidth}px`
      })
      
      // 设置容器高度
      const maxHeight = Math.max(...columnHeights)
      if (cardListRef.value) {
        cardListRef.value.style.height = `${maxHeight}px`
        cardListRef.value.style.position = 'relative'
      }
      
      // 动画完成后移除过渡和 will-change，避免影响后续交互
      setTimeout(() => {
        cards.forEach((card) => {
          card.style.transition = ''
          card.style.willChange = ''
        })
      }, 300)
    })
  })
}

// 监听窗口大小变化（使用防抖）
let resizeTimer: ReturnType<typeof setTimeout> | null = null
const handleResize = () => {
  if (resizeTimer) {
    clearTimeout(resizeTimer)
  }
  resizeTimer = setTimeout(() => {
    arrangeCards()
    // 窗口变大时可能需要加载更多，延迟执行确保布局完成
    setTimeout(() => {
      checkAndLoadMore()
    }, 350)
    resizeTimer = null
  }, 150)
}

onMounted(async () => {
  loadKnowledgeList()
  window.addEventListener('resize', handleResize)
  window.addEventListener('faqMenuAction', handleFaqMenuAction as EventListener)
  // 如果已有kbId，恢复导入任务状态
  if (props.kbId) {
    await restoreImportTask()
    await loadImportResult() // 加载导入结果
  }
  // 主动触发一次选中数量事件，确保左侧菜单能接收到初始状态
  nextTick(() => {
    const count = selectedRowKeys.value.length
    const selectedEntries = entries.value.filter(entry => selectedRowKeys.value.includes(entry.id))
    const enabledCount = selectedEntries.filter(entry => entry.is_enabled !== false).length
    const disabledCount = count - enabledCount
    window.dispatchEvent(new CustomEvent('faqSelectionChanged', {
      detail: { 
        count,
        enabledCount,
        disabledCount
      }
    }))
  })
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  window.removeEventListener('faqMenuAction', handleFaqMenuAction as EventListener)
  if (arrangeCardsTimer) {
    clearTimeout(arrangeCardsTimer)
  }
  if (resizeTimer) {
    clearTimeout(resizeTimer)
  }
})

// 监听 entries 变化，重新布局
watch(() => entries.value.length, () => {
  nextTick(() => {
    arrangeCards()
  })
})

// 监听折叠状态变化，重新布局（使用防抖和动画完成后的回调）
watch(() => entries.value.map(e => ({
  id: e.id,
  similarCollapsed: e.similarCollapsed,
  negativeCollapsed: e.negativeCollapsed,
  answersCollapsed: e.answersCollapsed
})), () => {
  // 使用 nextTick 确保 DOM 更新
  nextTick(() => {
    // 等待一个渲染帧，让高度变化生效
    requestAnimationFrame(() => {
      // 再等待一个渲染帧，确保高度计算准确
      requestAnimationFrame(() => {
        // 等待 Transition 动画完成后再布局（slide-down 动画时长约 200ms）
        // 使用防抖避免频繁调用
        debounceArrangeCards(250)
      })
    })
  })
}, { deep: true })
</script>

<style lang="less">
.tag-more-popup {
  z-index: 99 !important;

  .t-popup__content {
    padding: 4px 0 !important;
    margin-top: 4px !important;
    min-width: 120px;
  }
}

/* 面包屑下拉菜单优化 */
.t-popup__content {
  .t-dropdown__menu {
    background: #ffffff;
    border: 1px solid #e7e9eb;
    border-radius: 10px;
    box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
    padding: 6px;
    min-width: 150px;
    max-width: 200px;
  }

  .t-dropdown__item {
    padding: 8px 12px;
    border-radius: 6px;
    margin: 2px 0;
    transition: all 0.12s ease;
    font-size: 13px;
    color: #0f172a;
    cursor: pointer;
    min-width: auto !important;
    max-width: 100% !important;
    display: flex !important;
    align-items: center;
    width: 100%;

    &:hover {
      background: #f6f8f7;
      color: #10b981;
    }

    .t-dropdown__item-icon {
      flex-shrink: 0;
      margin-right: 8px;
      color: inherit;
      display: flex;
      align-items: center;
      
      .t-icon {
        font-size: 16px;
      }
    }

    .t-dropdown__item-text {
      color: inherit !important;
      font-size: 13px !important;
      line-height: 1.5 !important;
      white-space: nowrap !important;
      overflow: hidden !important;
      text-overflow: ellipsis !important;
      flex: 1;
      min-width: 0;
      display: block;
    }
  }
}
</style>
<style scoped lang="less">
.faq-manager {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

.faq-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.faq-main {
  display: flex;
  gap: 16px;
  flex: 1;
  min-height: 0;
}

.faq-tag-panel {
  width: 230px;
  background: #fff;
  border: 1px solid #e7ebf0;
  border-radius: 12px;
  padding: 16px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  max-height: 100%;
  min-height: 0;
  overflow: hidden;

  // t-loading 包裹容器需要撑满剩余空间
  > .t-loading__parent,
  > .t-loading {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
    color: #1d2129;

    .sidebar-title {
      display: flex;
      align-items: baseline;
      gap: 4px;
      font-weight: 600;

      .sidebar-count {
        font-size: 12px;
        color: #86909c;
      }
    }

    .sidebar-actions {
      display: flex;
      gap: 8px;
      color: #c9ced6;

      .create-tag-btn {
        width: 28px;
        height: 28px;
        padding: 0;
        border-radius: 6px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 18px;
        font-weight: 600;
        color: #00a870;
        line-height: 1;
        transition: background 0.2s ease, color 0.2s ease;

        &:hover {
          background: #f3f5f7;
          color: #05a04f;
        }
      }

      .create-tag-plus {
        line-height: 1;
      }

      .sidebar-action-icon {
        width: 28px;
        height: 28px;
        border-radius: 6px;
        display: flex;
        align-items: center;
        justify-content: center;
        cursor: pointer;
        transition: background 0.2s ease, color 0.2s ease;

        &:hover {
          background: #f3f5f7;
          color: #00a870;
        }
      }
    }
  }

  .tag-search-bar {
    margin-bottom: 12px;

    :deep(.t-input) {
      font-size: 12px;
      background-color: #f7f9fc;
      border-color: #e5e9f2;
    }
  }

  .faq-tag-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;

    .tag-loading-more {
      padding: 12px 0;
      display: flex;
      justify-content: center;
      flex-shrink: 0;
    }

    .faq-tag-item {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 8px 10px;
      border-radius: 6px;
      color: #4e5969;
      cursor: pointer;
      transition: all 0.2s ease;
      font-size: 13px;

      .faq-tag-left {
        display: flex;
        align-items: center;
        gap: 8px;
        min-width: 0;
        flex: 1;

        .t-icon {
          flex-shrink: 0;
          color: #86909c;
          font-size: 16px;
          transition: color 0.2s ease;
        }
      }

      .tag-name {
        flex: 1;
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        font-weight: 400;
      }

      .faq-tag-right {
        display: flex;
        align-items: center;
        gap: 6px;
        margin-left: 8px;
        min-width: 0;
        flex-shrink: 0;
      }

      .faq-tag-count {
        font-size: 12px;
        color: #86909c;
        font-weight: 500;
        padding: 2px 6px;
        border-radius: 10px;
        background: #f7f9fc;
        transition: all 0.2s ease;
      }

      &:hover {
        background: #f7f9fc;
        color: #1d2129;

        .faq-tag-left .t-icon {
          color: #4e5969;
        }

        .faq-tag-count {
          background: #e5e9f2;
          color: #4e5969;
        }
      }

      &.active {
        background: #e6f7ec;
        color: #00a870;
        font-weight: 500;

        .faq-tag-left .t-icon {
          color: #00a870;
        }

        .faq-tag-count {
          background: #b8f0d3;
          color: #00a870;
          font-weight: 600;
        }

        &:hover {
          background: #d4f4e3;
        }
      }

      &.editing {
        background: transparent;
        border: none;
      }

      &.tag-editing {
        cursor: default;
        padding-right: 8px;
        background: transparent;
        border: none;

        .tag-edit-input {
          flex: 1;
        }
      }

      &.tag-editing .tag-edit-input {
        width: 100%;
      }

      .tag-inline-actions {
        display: flex;
        gap: 4px;
        margin-left: auto;

        :deep(.t-button) {
          padding: 0 4px;
          height: 24px;
        }

        :deep(.tag-action-btn) {
          border-radius: 4px;
          transition: all 0.2s ease;

          .t-icon {
            font-size: 14px;
          }
        }

        :deep(.tag-action-btn.confirm) {
          background: #eefcf5;
          color: #059669;

          &:hover {
            background: #d9f7e9;
            color: #047857;
          }
        }

        :deep(.tag-action-btn.cancel) {
          background: #f9fafb;
          color: #6b7280;

          &:hover {
            background: #f3f4f6;
            color: #4b5563;
          }
        }
      }

      .tag-edit-input {
        flex: 1;
        min-width: 0;
        max-width: 100%;

        :deep(.t-input) {
          font-size: 12px;
          background-color: transparent;
          border: none;
          border-bottom: 1px solid #d0d5dd;
          border-radius: 0;
          box-shadow: none;
          padding-left: 0;
          padding-right: 0;
        }

        :deep(.t-input__wrap) {
          background-color: transparent;
          border: none;
          border-bottom: 1px solid #d0d5dd;
          border-radius: 0;
          box-shadow: none;
        }

        :deep(.t-input__inner) {
          padding-left: 0;
          padding-right: 0;
          color: #1d2129;
          caret-color: #1d2129;
        }

        :deep(.t-input:hover),
        :deep(.t-input.t-is-focused),
        :deep(.t-input__wrap:hover),
        :deep(.t-input__wrap.t-is-focused) {
          border-bottom-color: #00a870;
        }
      }

      .tag-more-btn {
        width: 24px;
        height: 24px;
        display: flex;
        align-items: center;
        justify-content: center;
        border-radius: 4px;
        color: #86909c;
        transition: all 0.2s ease;
        opacity: 0.6;

        &:hover {
          background: #f3f5f7;
          color: #4e5969;
          opacity: 1;
        }
      }


      .tag-more {
        display: flex;
        align-items: center;
      }

      .tag-more-placeholder {
        width: 24px;  // Same width as tag-more-btn
        height: 24px;
        flex-shrink: 0;
      }
    }

    .tag-empty-state {
      text-align: center;
      padding: 12px 8px;
      color: #a1a7b3;
      font-size: 12px;
    }
  }
}

.faq-card-area {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.faq-search-bar {
  padding: 0px 0px 10px 0px;
  flex-shrink: 0;

  :deep(.t-input) {
    font-size: 13px;
    background-color: #f7f9fc;
    border-color: #e5e9f2;
    border-radius: 6px;

    &:hover,
    &:focus,
    &.t-is-focused {
      background-color: #fff;
      border-color: #00a870;
    }
  }
}

:deep(.tag-menu) {
  display: flex;
  flex-direction: column;
}

:deep(.tag-menu-item) {
  display: flex;
  align-items: center;
  padding: 8px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  color: #000000e6;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;

  .menu-icon {
    margin-right: 8px;
    font-size: 16px;
  }

  &:hover {
    background: #f5f5f5;
    color: #000000e6;
  }

  &.danger {
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

.faq-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 16px;
  padding: 0 0 16px;
  margin-bottom: 20px;
  border-bottom: 1px solid #e7ebf0;
  flex-shrink: 0;
}

// 导入进度条样式（显示在列表页面顶部）
.faq-import-progress-bar {
  margin-bottom: 16px;
  background: linear-gradient(135deg, #f8fffe 0%, #f5fff9 100%);
  border: 1px solid #d4f0e0;
  border-radius: 10px;
  padding: 14px 18px;
  box-shadow: 0 2px 12px rgba(0, 168, 112, 0.08);

  .progress-bar-content {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .progress-bar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 14px;
    color: #000000e6;

    .progress-left {
      display: flex;
      align-items: center;
      gap: 10px;
    }

    .progress-right {
      display: flex;
      align-items: center;
      gap: 16px;
    }

    .progress-icon {
      flex-shrink: 0;

      &.icon-loading {
        animation: rotate 1s linear infinite;
        color: #00a870;
      }

      &.icon-success {
        color: #00a870;
      }

      &.icon-error {
        color: #fa5151;
      }
    }

    .progress-title {
      font-weight: 600;
      font-size: 14px;
      color: #1d2129;
    }

    .progress-count {
      color: #4e5969;
      font-size: 13px;
      font-weight: 500;
      background: rgba(0, 168, 112, 0.1);
      padding: 2px 10px;
      border-radius: 12px;
    }

    .progress-close-btn {
      flex-shrink: 0;
      padding: 4px;
      margin-left: 4px;
      border-radius: 4px;
      
      &:hover {
        background: rgba(0, 0, 0, 0.06);
      }
    }
  }

  .progress-bar {
    margin: 0;
    width: 100%;
    
    :deep(.t-progress) {
      width: 100%;
    }
    
    :deep(.t-progress__bar) {
      width: 100%;
      height: 8px;
      border-radius: 4px;
      background: rgba(0, 168, 112, 0.15);
    }
    
    :deep(.t-progress__inner) {
      border-radius: 4px;
    }
  }

  .progress-error {
    margin: 0;
    font-size: 13px;
    color: #fa5151;
    line-height: 1.5;
    background: rgba(250, 81, 81, 0.08);
    padding: 8px 12px;
    border-radius: 6px;
  }
}

@keyframes rotate {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

// 导入结果统计卡片样式
.faq-import-result-card {
  margin-bottom: 16px;
  background: #ffffff;
  border: 1px solid #e7ebf0;
  border-radius: 8px;
  padding: 16px 20px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.04);

  .import-result-content {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .import-result-header {
    display: flex;
    align-items: center;
    justify-content: space-between;

    .header-left {
      display: flex;
      align-items: center;
      gap: 8px;

      .result-icon {
        color: #07C05F;
        flex-shrink: 0;
      }

      .result-title {
        font-family: "PingFang SC";
        font-weight: 600;
        font-size: 14px;
        color: #1d2129;
      }
    }

    .header-right {
      display: flex;
      align-items: center;
      gap: 12px;

      .result-time {
        font-family: "PingFang SC";
        font-size: 13px;
        color: #86909c;
      }

      .result-close-btn {
        padding: 4px;
        border-radius: 4px;
        color: #86909c;
        transition: all 0.2s ease;

        &:hover {
          background: #f2f3f5;
          color: #4e5969;
        }
      }
    }
  }

  .import-result-body {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px;
  }

  .import-result-stats {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 24px;

    .stat-item {
      display: flex;
      align-items: center;
      gap: 6px;
      font-family: "PingFang SC";
      font-size: 13px;

      .stat-label {
        color: #86909c;
      }

      .stat-value {
        font-weight: 600;
        color: #1d2129;
      }

      &.success .stat-value {
        color: #07C05F;
      }

      &.failed .stat-value {
        color: #E34D59;
      }

      &.skipped .stat-value {
        color: #ED7B2F;
      }

      .download-failed-btn {
        margin-left: 4px;
        padding: 0 8px;
        height: 24px;
        font-size: 12px;
        border-radius: 4px;
        display: inline-flex;
        align-items: center;
        gap: 4px;

        .t-icon {
          font-size: 12px;
        }
      }
    }
  }

  .import-mode-tag {
    flex-shrink: 0;
  }
}

  .faq-header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .faq-title-row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .faq-breadcrumb {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    color: #1d2129;
  }

  .breadcrumb-link {
    border: none;
    background: transparent;
    padding: 4px 8px;
    margin: -4px -8px;
    font: inherit;
    color: #4e5969;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border-radius: 6px;
    transition: all 0.12s ease;

    &:hover:not(:disabled) {
      color: #10b981;
      background: #f6f8f7;
    }

    &:disabled {
      cursor: not-allowed;
      color: #c9ced6;
    }

    &.dropdown {
      padding-right: 6px;
      
      :deep(.t-icon) {
        font-size: 14px;
        transition: transform 0.12s ease;
      }

      &:hover:not(:disabled) {
        :deep(.t-icon) {
          transform: translateY(1px);
        }
      }
    }
  }

  .breadcrumb-separator {
    font-size: 14px;
    color: #c9ced6;
  }

  .breadcrumb-current {
    color: #1d2129;
    font-weight: 600;
  }

  h2 {
    margin: 0;
    color: #1d2129;
    font-family: "PingFang SC";
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }

  .faq-subtitle {
    margin: 0;
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
  }


.tag-filter-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;

  .tag-filter-label {
    color: #6B7280;
    font-size: 14px;
  }
}


.kb-settings-button {
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 50%;
  background: #f5f6f8;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #6b7280;
  cursor: pointer;
  transition: all 0.2s ease;
  padding: 0;

  &:hover:not(:disabled) {
    background: #e6f7ec;
    color: #07c05f;
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }

  :deep(.t-icon) {
    font-size: 18px;
  }
}

// 滚动容器
.faq-scroll-container {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding-right: 4px;
}

// 卡片列表样式 - 使用绝对定位实现瀑布流，下一行补齐上一行空缺
.faq-card-list {
  position: relative;
  width: 100%;
  min-width: 0;
}

.faq-card {
  border: 1px solid #E7E7E7;
  border-radius: 12px;
  background: #fff;
  padding: 12px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;
  box-sizing: border-box;
  height: fit-content; // 高度根据内容自适应
  // 注意：top 和 left 的 transition 由 JS 动态控制，避免与布局动画冲突

  &:hover {
    border-color: #07C05F;
    box-shadow: 0 2px 8px rgba(7, 192, 95, 0.1);
  }

  &.selected {
    border-color: #07C05F;
    background: #F0FDF4;
    box-shadow: 0 2px 8px rgba(7, 192, 95, 0.15);
  }
}

.faq-card-header {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-bottom: 12px;
  border-bottom: 1px solid #F3F4F6;
  position: relative;
}

.faq-header-top {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.faq-card-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  flex-shrink: 0;
}

.faq-header-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding-top: 6px;
  border-top: 1px dashed #F3F4F6;
}

.faq-meta-item {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  padding: 4px 10px;
  border-radius: 999px;
  background: #F9FAFB;
  border: 1px solid #EEF2F7;

  .meta-label {
    font-size: 12px;
    color: #6B7280;
    font-weight: 500;
  }

  .meta-value {
    font-size: 13px;
    color: #111827;
    font-weight: 600;
  }
}

.faq-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 16px;
  margin: 0 -16px -12px;
  background: rgba(48, 50, 54, 0.02);
  border-top: 1px solid #f5f5f5;
  flex-wrap: nowrap;
}

.faq-card-status {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  margin-left: auto;
}

.status-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-radius: 999px;
  background: #F9FAFB;
  border: 1px solid #EEF2F7;
  font-size: 12px;
  color: #4B5563;
  font-family: "PingFang SC";

  .status-icon {
    font-size: 14px;
    color: #9CA3AF;

    &.warning {
      color: #F97316;
    }

    &.success {
      color: #10B981;
    }
  }
}

.status-item-compact {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 4px;
  border-radius: 4px;
  background: transparent;
  border: none;
  cursor: pointer;
  transition: all 0.2s ease;

  &:hover {
    background: rgba(0, 0, 0, 0.04);
  }

  .status-icon {
    font-size: 16px;
    flex-shrink: 0;

    &.warning {
      color: #F97316;
    }

    &.success {
      color: #10B981;
    }
  }

  :deep(.t-switch) {
    flex-shrink: 0;
  }
}

.faq-card-tag {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  flex: 1;
  min-width: 0;

  :deep(.t-tag) {
    display: inline-flex;
    align-items: center;
    cursor: pointer;
    max-width: 140px;
    height: 22px;
    border-radius: 4px;
    border-color: #e5e7eb;
    color: #00000066;
    padding: 0 8px;
    background: #3032360f;
    font-size: 12px;
    font-weight: 400;
    font-family: "PingFang SC";
    transition: all 0.2s ease;

    &:hover {
      border-color: #07c05f;
      color: #059669;
      background: #ecfdf5;
    }
  }
}

.faq-tag-chip {
  display: inline-flex;
  align-items: center;
  cursor: pointer;

  .tag-text {
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    font-weight: 400;
    color: #00000066;
  }
}

.card-more-btn {
  display: flex;
  width: 28px;
  height: 28px;
  justify-content: center;
  align-items: center;
  border-radius: 6px;
  cursor: pointer;
  flex-shrink: 0;
  opacity: 0.6;

  &:hover {
    background: #F3F4F6;
    opacity: 1;
  }

  &.mobile {
    display: none;
  }

  .more-icon {
    width: 16px;
    height: 16px;
  }
}

.card-menu {
  display: flex;
  flex-direction: column;
}

.card-menu-item {
  display: flex;
  align-items: center;
  padding: 8px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  color: #000000e6;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;

  .menu-icon {
    margin-right: 8px;
    font-size: 16px;
    flex-shrink: 0;
  }

  &:hover {
    background: #f5f5f5;
    color: #000000e6;
  }

  &.danger {
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

.faq-question {
  flex: 1;
  color: #111827;
  font-family: "PingFang SC";
  font-size: 15px;
  font-weight: 600;
  line-height: 1.5;
  word-break: break-word;
  min-width: 0;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.faq-card-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  contain: layout; // 优化渲染性能
}

.faq-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  overflow: hidden;

  .faq-section-label {
    color: #6B7280;
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 2px;

    &::before {
      content: '';
      width: 3px;
      height: 12px;
      background: #07C05F;
      border-radius: 2px;
      flex-shrink: 0;
    }

    &.clickable {
      cursor: pointer;
      user-select: none;
      padding: 2px 0;
      border-radius: 4px;

      &:hover {
        color: #111827;
        background: #F9FAFB;
        padding-left: 4px;
        padding-right: 4px;
        margin-left: -4px;
        margin-right: -4px;
      }
    }

    .collapse-icon {
      font-size: 14px;
      color: #9CA3AF;
      flex-shrink: 0;
      margin-left: auto; // 让箭头靠右对齐
    }

    .section-count {
      color: #9CA3AF;
      font-weight: 400;
      margin-left: 4px;
    }
  }

  &.answers .faq-section-label::before {
    background: #07C05F;
  }

  &.similar .faq-section-label::before {
    background: #3B82F6;
  }

  &.negative .faq-section-label::before {
    background: #F59E0B;
  }
}

.faq-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 20px;
  min-width: 0;
  width: 100%;
  overflow: hidden;
  contain: layout style paint; // 优化渲染性能
  
  // 确保每个标签都有最大宽度限制
  > * {
    max-width: 100%;
    min-width: 0;
    flex: 0 1 auto;
  }
  
  // 当标签单独一行时，限制最大宽度
  > *:first-child:last-child {
    max-width: 100%;
  }
}

.question-tag {
  font-size: 12px;
  padding: 4px 10px;
  max-width: 100%;
  min-width: 0;
  border-radius: 6px;
  font-family: "PingFang SC";
  flex: 0 1 auto;
  
  :deep(.t-tag) {
    max-width: 100% !important;
    min-width: 0 !important;
    width: auto !important;
    display: inline-flex !important;
    align-items: center;
    vertical-align: middle;
    overflow: hidden !important;
    box-sizing: border-box;
    background: #F9FAFB;
    border-color: #E5E7EB;
    color: #374151;
  }
  
  // 针对TDesign tag内部的span元素
  :deep(.t-tag span),
  :deep(.t-tag > span) {
    display: block !important;
    overflow: hidden !important;
    text-overflow: ellipsis !important;
    white-space: nowrap !important;
    max-width: 100% !important;
    width: auto !important;
    line-height: 1.4;
    min-width: 0 !important;
  }
}

// 确保 tag 本身不会超出容器
.faq-tags :deep(.t-tag) {
  max-width: 100%;
  min-width: 0;
  flex-shrink: 1;
}

.faq-tags :deep(.faq-tag-wrapper) {
  max-width: 100%;
  min-width: 0;
  flex-shrink: 1;
}

.empty-tip {
  color: #9CA3AF;
  font-size: 12px;
  font-style: italic;
  padding: 8px 0;
  font-family: "PingFang SC";
}


.faq-load-more,
.faq-no-more {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 24px 16px;
  color: #6B7280;
  font-size: 13px;
  font-family: "PingFang SC";
}

.faq-no-more {
  color: #9CA3AF;
  font-style: italic;
}

// 空状态样式
.faq-empty-state {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 400px;
  padding: 60px 20px;

  .empty-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 16px;
    text-align: center;
    max-width: 400px;
  }

  .empty-icon {
    color: #D1D5DB;
    opacity: 0.6;
  }

  .empty-text {
    color: #111827;
    font-family: "PingFang SC";
    font-size: 18px;
    font-weight: 600;
    line-height: 28px;
  }

  .empty-desc {
    color: #6B7280;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
  }
}

// 导入对话框样式 - 与创建知识库弹窗风格一致
.faq-import-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
}

.faq-import-modal {
  position: relative;
  width: 100%;
  max-width: 600px;
  max-height: 90vh;
  background: #ffffff;
  border-radius: 12px;
  box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
  overflow: hidden;
  display: flex;
  flex-direction: column;

  .close-btn {
    position: absolute;
    top: 20px;
    right: 20px;
    width: 32px;
    height: 32px;
    border: none;
    background: #f5f5f5;
    border-radius: 6px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #666;
    transition: all 0.2s ease;
    z-index: 10;

    &:hover {
      background: #e5e5e5;
      color: #000;
    }
  }
}

.faq-import-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.faq-import-header {
  padding: 24px 24px 16px;
  border-bottom: 1px solid #e5e5e5;
  flex-shrink: 0;

  .import-title {
    margin: 0;
    font-family: "PingFang SC";
    font-size: 18px;
    font-weight: 600;
    color: #000000e6;
  }
}

.faq-import-content {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 24px;
  min-height: 0;
  max-height: calc(90vh - 140px); // 减去 header 和 footer 的高度
  
  // 自定义滚动条
  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: #f5f5f5;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #d0d0d0;
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: #07C05F;
    }
  }
}

.faq-import-footer {
  padding: 16px 24px;
  border-top: 1px solid #e5e5e5;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
}

// 导入表单项
.import-form-item {
  margin-bottom: 24px;

  &:last-child {
    margin-bottom: 0;
  }
}

// 文件标签行
.file-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
  gap: 12px;
}

// 下载示例按钮
.download-example-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  font-family: "PingFang SC";
  font-size: 13px;
  font-weight: 500;
  padding: 6px 14px;
  border-radius: 6px;
  border: 1px solid #E7E7E7;
  background: #ffffff;
  color: #333333;
  transition: all 0.2s ease;
  cursor: pointer;
  white-space: nowrap;

  &:hover {
    border-color: #07C05F;
    color: #07C05F;
    background: #f0fdf4;
  }

  &:active {
    background: #e0f9ea;
  }

  :deep(.t-icon) {
    font-size: 16px;
  }
}

// 导入表单标签
.import-form-label {
  display: block;
  margin-bottom: 0;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #333333;
  letter-spacing: -0.2px;
  flex: 1;

  &.required::after {
    content: '*';
    color: #FA5151;
    margin-left: 4px;
    font-weight: 600;
  }
}

// 单选按钮组样式 - 符合项目主题风格
:deep(.import-radio-group) {
  .t-radio-group--filled {
    background: #f5f5f5;
    border-radius: 6px;
    padding: 2px;
  }
  
  .t-radio-button {
    font-family: "PingFang SC";
    font-size: 14px;
    border-color: #d9d9d9;
    transition: all 0.2s ease;

    &:hover:not(.t-is-disabled) {
      border-color: #07c05f;
      color: #07c05f;
    }

    &.t-is-checked {
      background: #07c05f;
      border-color: #07c05f;
      color: #fff;
      font-weight: 500;

      &:hover:not(.t-is-disabled) {
        background: #05a04f;
        border-color: #05a04f;
        color: #fff;
      }
    }
  }
}

// 文件上传包装器
.file-upload-wrapper {
  width: 100%;
}

// 隐藏的文件输入
.file-input-hidden {
  position: absolute;
  width: 0;
  height: 0;
  opacity: 0;
  overflow: hidden;
  pointer-events: none;
}

// 文件上传区域
.file-upload-area {
  position: relative;
  width: 100%;
  min-height: 120px;
  border: 2px dashed #d9d9d9;
  border-radius: 8px;
  background: #fafafa;
  cursor: pointer;
  transition: all 0.3s ease;
  display: flex;
  align-items: center;
  justify-content: center;

  &:hover {
    border-color: #07C05F;
    background: #f0fdf4;
  }

  &.has-file {
    border-color: #07C05F;
    background: #f0fdf4;
    border-style: solid;
  }
}

// 文件上传内容
.file-upload-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
}

.upload-icon {
  color: #07C05F;
  transition: transform 0.2s ease;
}

.file-upload-area:hover .upload-icon {
  transform: translateY(-2px);
}

.upload-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.upload-primary-text {
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #333333;
}

.upload-secondary-text {
  font-family: "PingFang SC";
  font-size: 12px;
  color: #666666;
}

.upload-file-name {
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #07C05F;
  word-break: break-all;
}

// 导入表单提示
.import-form-tip {
  margin-top: 8px;
  font-family: "PingFang SC";
  font-size: 12px;
  color: #00000066;
  line-height: 18px;
}

// 预览区域
.import-preview {
  margin-top: 20px;
  padding: 16px;
  background: #fafafa;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
}

.preview-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid #e5e7eb;
}

.preview-icon {
  color: #07C05F;
  flex-shrink: 0;
}

.preview-title {
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #333333;
}

.preview-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 8px;
}

.preview-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 10px 12px;
  background: #ffffff;
  border: 1px solid #e5e7eb;
  border-radius: 6px;
  transition: all 0.2s ease;

  &:hover {
    border-color: #07C05F;
    box-shadow: 0 2px 4px rgba(7, 192, 95, 0.08);
  }
}

.preview-index {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #07C05F 0%, #05a04f 100%);
  color: #ffffff;
  border-radius: 4px;
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 600;
}

.preview-question {
  flex: 1;
  font-family: "PingFang SC";
  font-size: 13px;
  color: #333333;
  line-height: 1.5;
  word-break: break-word;
}

.preview-more {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #e5e7eb;
  font-family: "PingFang SC";
  font-size: 12px;
  color: #666666;
  text-align: center;
}

// 响应式布局由 JavaScript 动态计算，这里不需要媒体查询

// 卡片菜单弹窗样式
:deep(.faq-card-popup) {
  z-index: 99 !important;

  .t-popup__content {
    padding: 4px 0 !important;
    margin-top: 4px !important;
    min-width: 120px;
  }
}

// FAQ 编辑器抽屉样式
:deep(.faq-editor-drawer) {
  .t-drawer__body {
    padding: 20px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .t-drawer__header {
    padding: 20px 24px;
    border-bottom: 1px solid #e5e5e5;
    font-family: "PingFang SC";
    font-size: 18px;
    font-weight: 600;
    color: #000000e6;
  }

  .t-drawer__footer {
    padding: 16px 24px;
    border-top: 1px solid #e5e5e5;
  }
}

.faq-editor-drawer-content {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  min-height: 0;
  
  // 自定义滚动条
  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: #f5f5f5;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #d0d0d0;
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: #07C05F;
    }
  }

  .editor-form {
    width: 100%;
  }
}

.faq-editor-drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

// 全宽输入框包装器 - 统一样式
.full-width-input-wrapper {
  display: flex;
  gap: 8px;
  align-items: center;
  width: 100%;

  .full-width-input {
    flex: 1;
    min-width: 0;
  }

  .full-width-textarea {
    flex: 1;
    min-width: 0;
    
    :deep(.t-textarea__inner) {
      min-height: 80px;
    }
  }

  // textarea需要顶部对齐
  &.textarea-wrapper {
    align-items: flex-start;
  }

  .add-item-btn {
    flex-shrink: 0;
    width: 32px;
    height: 32px;
    min-width: 32px;
    padding: 0;
    font-family: "PingFang SC";
    transition: all 0.2s ease;
    border-radius: 8px;
  }

  :deep(.add-item-btn) {
    background: #07C05F !important;
    border: 1px solid #07C05F !important;
    border-radius: 8px !important;
    color: #ffffff !important;
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover:not(:disabled) {
      background: #05a04f !important;
      border-color: #05a04f !important;
      transform: scale(1.05);
      box-shadow: 0 2px 8px rgba(7, 192, 95, 0.3);
    }

    &:active:not(:disabled) {
      background: #048a42 !important;
      border-color: #048a42 !important;
      transform: scale(0.98);
    }

    &:disabled {
      background: #E5E7EB !important;
      border-color: #E5E7EB !important;
      color: #9CA3AF !important;
      cursor: not-allowed;
      opacity: 0.6;
    }

    .t-icon {
      font-size: 16px;
    }
  }
}

.textarea-container {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.item-count {
  font-size: 13px;
  color: #6B7280;
  font-family: "PingFang SC";
  font-weight: 500;
  text-align: right;
  padding-right: 40px;
  line-height: 1;
}

.item-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  margin-top: 8px;
}


.item-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  background: #ffffff;
  border: 1px solid #E7E7E7;
  border-radius: 8px;
  transition: all 0.2s ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
  position: relative;

  &.answer-row {
    align-items: flex-start;
    padding: 12px 14px;
  }

  &:hover {
    background: #fafafa;
    border-color: #07C05F;
    box-shadow: 0 2px 8px rgba(7, 192, 95, 0.12);
    transform: translateY(-1px);
  }

  &.negative {
    background: #FFFBEB;
    border-color: #FDE68A;

    &:hover {
      background: #FEF3C7;
      border-color: #FCD34D;
      box-shadow: 0 2px 8px rgba(251, 191, 36, 0.15);
    }
  }

  .item-content {
    flex: 1;
    font-size: 14px;
    line-height: 1.6;
    color: #111827;
    font-family: "PingFang SC";
    white-space: pre-wrap;
    word-break: break-word;
    padding: 0;
    font-weight: 400;
  }

  .remove-item-btn {
    flex-shrink: 0;
    color: #9CA3AF;
    padding: 0;
    width: 24px;
    height: 24px;
    min-width: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 6px;
    transition: all 0.2s ease;
    background: transparent;
    border: none;
    cursor: pointer;

    &:hover {
      color: #EF4444;
      background: #FEE2E2;
    }

    &:active {
      background: #FECACA;
    }

    :deep(.t-icon) {
      font-size: 14px;
    }
  }

  &.answer-row .remove-item-btn {
    margin-top: 0;
  }
}

.form-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #00000066;
  font-family: "PingFang SC";
}

// FAQ编辑器表单样式 - 完全参考设置页面
.faq-editor-form {
  width: 100%;

  // 隐藏Form的默认结构
  :deep(.t-form__label) {
    display: none !important;
    width: 0 !important;
    padding: 0 !important;
    margin: 0 !important;
  }

  :deep(.t-form__controls) {
    margin-left: 0 !important;
    width: 100% !important;
  }

  :deep(.t-form__controls-content) {
    margin: 0 !important;
    padding: 0 !important;
    width: 100% !important;
    display: block !important;
  }

  :deep(.t-form-item) {
    margin-bottom: 0 !important;
    padding: 0 !important;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid #e5e7eb;

  &:last-child {
    border-bottom: none;
  }

  &.vertical {
    flex-direction: column;
    gap: 12px;

    .setting-control {
      width: 100%;
      max-width: 100%;
    }
  }

  // 主要字段（标准问、答案）的强调样式
  &.setting-row-primary {
    padding: 20px 0;
    padding-left: 12px;
    position: relative;

    // 第一个（标准问）去掉顶部间距
    &:first-child {
      padding-top: 0;
    }

    // 左侧颜色标记（标准问和答案都用绿色）
    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 20px;
      width: 3px;
      height: calc(100% - 40px);
      background: #07C05F;
      border-radius: 0 2px 2px 0;
    }

    &:first-child::before {
      top: 0;
      height: calc(100% - 20px);
    }
  }

  // 可选字段（相似问、反例）的次要样式
  &.setting-row-optional {
    padding-left: 12px;
    position: relative;

    // 左侧颜色标记
    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 20px;
      width: 3px;
      height: calc(100% - 40px);
      border-radius: 0 2px 2px 0;
    }

    .setting-info {
      .optional-label {
        color: #333333;
        font-weight: 500;
      }

      .optional-desc {
        color: #666666;
      }
    }
  }

  // 相似问的蓝色标记
  &.setting-row-similar::before {
    background: #3B82F6;
  }

  // 反例的橙色标记
  &.setting-row-negative::before {
    background: #F59E0B;
  }

  // 答案去掉底部边框
  &.setting-row-answer {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: #333333;
    display: block;
    margin-bottom: 4px;
  }

  .required-label {
    font-size: 15px;
    font-weight: 600;
    color: #333333;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 4px;
  }

  .required-mark {
    color: #FA5151;
    font-weight: 600;
    font-size: 14px;
  }

  .optional-label {
    font-size: 15px;
    font-weight: 600;
    color: #333333;
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }

  .optional-desc {
    font-size: 13px;
    color: #666666;
  }
}

.setting-row.vertical .setting-info {
  max-width: 100%;
  padding-right: 0;
  width: 100%;
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.setting-row.vertical .setting-control {
  width: 100%;
  max-width: 100%;
  min-width: unset;
  justify-content: flex-start;
  align-items: flex-start;
  flex-direction: column;
}

// 垂直布局中的输入框确保全宽
.setting-row.vertical .full-width-input {
  width: 100%;

  :deep(.t-input__wrap) {
    width: 100%;
  }
}

.setting-row.vertical .full-width-textarea {
  width: 100%;

  :deep(.t-textarea) {
    width: 100%;
  }
}

// Input 组件样式 - 与登录页面一致
:deep(.t-input) {
  font-family: "PingFang SC";
  font-size: 14px;
  border: 1px solid #E7E7E7;
  border-radius: 8px;
  background: #fff;
  transition: all 0.2s ease;

  &:hover {
    border-color: #07C05F;
  }

  &:focus-within {
    border-color: #07C05F;
    box-shadow: 0 0 0 3px rgba(7, 192, 95, 0.1);
  }

  .t-input__inner {
    border: none !important;
    box-shadow: none !important;
    outline: none !important;
    background: transparent;
    font-size: 14px;
    font-family: "PingFang SC";
    padding: 6px 12px;
    color: #111827;

    &:focus {
      border: none !important;
      box-shadow: none !important;
      outline: none !important;
    }

    &::placeholder {
      color: #9CA3AF;
    }
  }

  .t-input__wrap {
    border: none !important;
    box-shadow: none !important;
  }
}

// Textarea 组件样式
:deep(.t-textarea) {
  font-family: "PingFang SC";
  font-size: 14px;
  border: 1px solid #E7E7E7;
  border-radius: 8px;
  background: #fff;
  transition: all 0.2s ease;

  &:hover {
    border-color: #07C05F;
  }

  &:focus-within {
    border-color: #07C05F;
    box-shadow: 0 0 0 3px rgba(7, 192, 95, 0.1);
  }

  .t-textarea__inner {
    border: none !important;
    box-shadow: none !important;
    outline: none !important;
    background: transparent;
    font-size: 14px;
    font-family: "PingFang SC";
    line-height: 1.6;
    resize: vertical;
    padding: 6px 12px;
    color: #111827;

    &:focus {
      border: none !important;
      box-shadow: none !important;
      outline: none !important;
    }

    &::placeholder {
      color: #9CA3AF;
    }
  }
}

:deep(.t-button--theme-primary) {
  background-color: #07c05f;
  border-color: #07c05f;
  
  &:hover {
    background-color: #05a04f;
    border-color: #05a04f;
  }
}

// 导入弹窗动画
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}

.modal-enter-active .faq-import-modal,
.modal-leave-active .faq-import-modal,
.modal-enter-active .batch-tag-modal,
.modal-leave-active .batch-tag-modal {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .faq-import-modal,
.modal-leave-to .faq-import-modal,
.modal-enter-from .batch-tag-modal,
.modal-leave-to .batch-tag-modal {
  transform: scale(0.95);
  opacity: 0;
}

// Tag 样式优化
.answer-tag {
  background: #07c05f1a;
  color: #07c05f;
  border-color: #07c05f33;
}

.question-tag {
  background: #fff;
  border-color: #d9d9d9;
  color: #00000099;
}

// Search test drawer styles - 与编辑器抽屉风格一致
:deep(.faq-search-drawer) {
  .t-drawer__body {
    padding: 20px;
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .t-drawer__header {
    padding: 20px 24px;
    border-bottom: 1px solid #e5e5e5;
    font-family: "PingFang SC";
    font-size: 18px;
    font-weight: 600;
    color: #000000e6;
  }
}

.search-test-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-right: 0;

  // 隐藏滚动条但保持滚动功能
  scrollbar-width: none; // Firefox
  -ms-overflow-style: none; // IE and Edge

  &::-webkit-scrollbar {
    display: none; // Chrome, Safari, Opera
  }
}

.search-form {
  flex-shrink: 0;

  :deep(.t-form__label) {
    display: none !important;
    width: 0 !important;
    padding: 0 !important;
    margin: 0 !important;
  }

  :deep(.t-form__controls) {
    margin-left: 0 !important;
    width: 100% !important;
  }

  :deep(.t-form__controls-content) {
    margin: 0 !important;
    padding: 0 !important;
    width: 100% !important;
    display: block !important;
  }

  :deep(.t-form-item) {
    margin-bottom: 0 !important;
    padding: 0 !important;
  }
}

.slider-wrapper {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 2px 0;
}

.search-form .setting-row {
  padding: 16px 0;
  border-bottom: 1px solid #e5e7eb;

  &.search-first-row {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }

  .setting-info {
    max-width: 100%;
    padding-right: 0;
    margin-bottom: 8px;

    label {
      font-size: 14px;
      font-weight: 500;
      color: #333333;
      display: block;
      margin-bottom: 4px;
    }

    .desc {
      font-size: 12px;
      color: #666666;
      margin: 0;
      line-height: 1.4;
    }
  }

  .setting-control {
    width: 100%;
    max-width: 100%;
    min-width: unset;
    justify-content: flex-start;
    align-items: flex-start;
    flex-direction: column;
  }
}

:deep(.slider-wrapper .t-slider) {
  flex: 1;
  min-width: 0;

  .t-slider__rail {
    background: #E7E7E7;
    height: 4px;
    border-radius: 2px;
  }

  .t-slider__track {
    background: #07C05F;
    height: 4px;
    border-radius: 2px;
  }

  .t-slider__button {
    width: 16px;
    height: 16px;
    border: 2px solid #07C05F;
    background: #ffffff;
    box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);

    &:hover {
      border-color: #05a04f;
      box-shadow: 0 2px 8px rgba(7, 192, 95, 0.2);
    }
  }
}

.slider-value {
  flex-shrink: 0;
  min-width: 50px;
  text-align: right;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #111827;
  padding: 4px 8px;
  background: #F9FAFB;
  border-radius: 6px;
}

.search-button {
  height: 36px;
  border-radius: 8px;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  transition: all 0.2s ease;

  &:hover:not(:disabled) {
    transform: translateY(-1px);
    box-shadow: 0 4px 12px rgba(7, 192, 95, 0.3);
  }

  &:active:not(:disabled) {
    transform: translateY(0);
  }
}

.search-results {
  display: flex;
  flex-direction: column;
  padding-top: 20px;
  padding-left: 0;
  width: 100%;
  box-sizing: border-box;
}

.results-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
  margin-left: 0;
  margin-right: 0;
  padding-left: 0;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 600;
  color: #111827;
  flex-shrink: 0;
  justify-content: flex-start;

  .t-icon {
    color: #07C05F;
  }
}

.no-results {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 16px;
  color: #6B7280;
  font-family: "PingFang SC";
  font-size: 14px;
  text-align: center;
  background: #F9FAFB;
  border-radius: 8px;
  border: 1px dashed #E7E7E7;
}

.results-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.result-card {
  border: 1px solid #E7E7E7;
  border-radius: 8px;
  background: #fff;
  padding: 14px;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
  width: 100%;
  box-sizing: border-box;
  min-width: 0;
  overflow: visible;
  position: relative;

  &:hover {
    border-color: #07C05F;
    box-shadow: 0 2px 8px rgba(7, 192, 95, 0.12);
  }
}

.result-header {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 0;
  border-bottom: none;
  cursor: pointer;
  user-select: none;
  padding: 4px;
  margin: -4px;
  border-radius: 6px;
  position: relative;

  &:hover {
    background-color: #F9FAFB;
  }
}

.result-card.expanded .result-header {
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid #E7E7E7;
  margin-left: -4px;
  margin-right: -4px;
  padding-left: 4px;
  padding-right: 4px;
}

.result-question-wrapper {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  width: 100%;
}

.result-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.result-question {
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 600;
  color: #111827;
  line-height: 1.6;
  word-break: break-word;
  display: flex;
  align-items: flex-start;
  gap: 6px;

  .result-index {
    flex-shrink: 0;
    color: #07C05F;
    font-weight: 600;
  }
}

.matched-question {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  padding-left: 20px;
  font-size: 12px;
  line-height: 1.5;

  .matched-label {
    flex-shrink: 0;
    color: #E37318;
    font-weight: 500;
  }

  .matched-text {
    color: #92400E;
    background: linear-gradient(90deg, rgba(251, 191, 36, 0.15) 0%, rgba(251, 191, 36, 0.05) 100%);
    padding: 1px 6px;
    border-radius: 4px;
    word-break: break-word;
  }
}

.result-meta {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  flex-shrink: 0;
  margin-left: auto;
}

.expand-icon {
  flex-shrink: 0;
  font-size: 18px;
  color: #6B7280;
  transition: transform 0.2s ease;
  cursor: pointer;

  &:hover {
    color: #07C05F;
  }
}

.score-tag,
.match-type-tag {
  font-size: 12px;
  padding: 4px 8px;
  border-radius: 6px;
  font-family: "PingFang SC";
}

.result-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 12px;
  margin-top: 0;
  border-top: 1px solid #F3F4F6;
  position: relative;
  width: 100%;
}

// Slide down animation - 优化性能
.slide-down-enter-active {
  transition: opacity 0.2s cubic-bezier(0.4, 0, 0.2, 1), 
              transform 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  overflow: hidden;
  will-change: opacity, transform;
}

.slide-down-leave-active {
  transition: opacity 0.2s cubic-bezier(0.4, 0, 0.2, 1), 
              transform 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  overflow: hidden;
  will-change: opacity, transform;
}

.slide-down-enter-from {
  opacity: 0;
  transform: translateY(-8px);
}

.slide-down-enter-to {
  opacity: 1;
  transform: translateY(0);
}

.slide-down-leave-from {
  opacity: 1;
  transform: translateY(0);
}

.slide-down-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}

.result-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

// 批量分类弹窗样式 - 与导入对话框风格一致
.batch-tag-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
}

.batch-tag-modal {
  position: relative;
  width: 100%;
  max-width: 480px;
  background: #ffffff;
  border-radius: 12px;
  box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
  overflow: hidden;
  display: flex;
  flex-direction: column;

  .batch-tag-close-btn {
    position: absolute;
    top: 20px;
    right: 20px;
    width: 32px;
    height: 32px;
    border: none;
    background: #f5f5f5;
    border-radius: 6px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #666;
    transition: all 0.2s ease;
    z-index: 10;

    &:hover {
      background: #e5e5e5;
      color: #000;
    }
  }
}

.batch-tag-container {
  display: flex;
  flex-direction: column;
  padding: 24px;
}

.batch-tag-header {
  margin-bottom: 24px;
  padding-right: 40px;

  .batch-tag-title {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    color: #0f172a;
    line-height: 1.4;
  }
}

.batch-tag-content {
  flex: 1;
  min-height: 0;
}

.batch-tag-tip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px 16px;
  margin-bottom: 20px;
  background: #f0f9ff;
  border: 1px solid #bae6fd;
  border-radius: 8px;
  font-size: 14px;
  color: #0369a1;
  line-height: 1.5;

  .tip-icon {
    flex-shrink: 0;
    margin-top: 2px;
    color: #0284c7;
  }
}

.batch-tag-form {
  margin-top: 0;

  :deep(.t-form-item) {
    margin-bottom: 0;
  }

  :deep(.t-form-item__label) {
    font-size: 14px;
    font-weight: 500;
    color: #1d2129;
    margin-bottom: 8px;
  }
}

.batch-tag-select {
  width: 100%;
}

.batch-tag-footer {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid #e5e7eb;
}

.tag-select-empty {
  padding: 8px 12px;
  text-align: center;
  color: #86909c;
  font-size: 14px;
}

.section-label {
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 600;
  color: #6B7280;
  margin-bottom: 4px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.result-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  width: 100%;
  min-width: 0;
}

:deep(.result-tags .t-tag) {
  max-width: 100%;
  min-width: 0;
  word-break: break-word;
  overflow-wrap: break-word;
}

:deep(.result-tags .t-tag__text) {
  display: inline-block;
  max-width: 100%;
  word-break: break-word;
  overflow-wrap: break-word;
  white-space: normal;
  line-height: 1.4;
}
</style>



