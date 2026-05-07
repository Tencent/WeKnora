<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="handleClose">
        <div class="settings-modal">
          <!-- 关闭按钮 -->
          <button class="close-btn" @click="handleClose" :aria-label="$t('common.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </button>

          <div class="settings-container">
            <!-- 左侧导航 -->
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ mode === 'create' ? $t('agent.editor.createTitle') : $t('agent.editor.editTitle') }}</h2>
              </div>
              <div class="settings-nav">
                <div 
                  v-for="(item, index) in navItems" 
                  :key="index"
                  :class="['nav-item', { 'active': currentSection === item.key }]"
                  @click="currentSection = item.key"
                >
                  <t-icon :name="item.icon" class="nav-icon" />
                  <span class="nav-label">{{ item.label }}</span>
                </div>
              </div>
            </div>

            <!-- 右侧内容区域 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <!-- 基础设置 -->
                <div v-show="currentSection === 'basic'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.basicInfo') }}</h2>
                    <p class="section-description">{{ $t('agent.editor.basicInfoDesc') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 内置智能体提示 -->
                    <div v-if="isBuiltinAgent" class="builtin-agent-notice">
                      <t-icon name="info-circle" />
                      <span>{{ $t('agentEditor.builtinHint') }}</span>
                    </div>

                    <!-- 运行模式（首先选择） -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.mode') }} <span class="required">*</span></label>
                        <p class="desc">{{ agentMode === 'smart-reasoning' ? $t('agent.editor.agentDesc') : $t('agent.editor.normalDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-radio-group v-model="agentMode" :disabled="isBuiltinAgent">
                          <t-radio-button value="quick-answer">
                            {{ $t('agent.type.normal') }}
                          </t-radio-button>
                          <t-radio-button value="smart-reasoning">
                            {{ $t('agent.type.agent') }}
                          </t-radio-button>
                        </t-radio-group>
                      </div>
                    </div>

                    <!-- 智能体类型（仅智能推理模式下显示） -->
                    <div v-if="isAgentMode && agentTypePresets.length > 0" class="setting-row setting-row--emphasize">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.agentType.label') }}</label>
                        <p class="desc">{{ $t('agentEditor.agentType.desc') }}</p>
                        <p
                          v-if="activeAgentTypePreset"
                          class="desc agent-type-preset-desc"
                        >{{ agentTypePresetDescription(activeAgentTypePreset) }}</p>
                      </div>
                      <div class="setting-control">
                        <t-select
                          :value="agentType"
                          @change="onAgentTypeChange"
                          :disabled="isBuiltinAgent"
                          :placeholder="$t('agentEditor.agentType.label')"
                          :options="agentTypeSelectOptions"
                          :popup-props="{ overlayClassName: 'agent-type-popup' }"
                          class="agent-type-select"
                        >
                          <template #option="{ option }">
                            <div class="agent-type-option">
                              <span class="agent-type-option-label">{{ option.label }}</span>
                              <span v-if="option.desc" class="agent-type-option-desc">{{ option.desc }}</span>
                            </div>
                          </template>
                        </t-select>
                      </div>
                    </div>

                    <!-- 名称 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.name') }} <span v-if="!isBuiltinAgent" class="required">*</span></label>
                        <p class="desc">{{ $t('agentEditor.desc.name') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="name-input-wrapper">
                          <!-- 内置智能体使用简洁图标 -->
                          <div v-if="isBuiltinAgent" class="builtin-avatar" :class="isAgentMode ? 'agent' : 'normal'">
                            <t-icon :name="isAgentMode ? 'control-platform' : 'chat'" size="24px" />
                          </div>
                          <!-- 自定义智能体使用 AgentAvatar -->
                          <AgentAvatar v-else :name="formData.name || '?'" size="medium" />
                          <t-input 
                            v-model="formData.name" 
                            :placeholder="$t('agent.editor.namePlaceholder')" 
                            class="name-input"
                            :disabled="isBuiltinAgent"
                          />
                        </div>
                      </div>
                    </div>

                    <!-- 描述 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.description') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.description') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-textarea 
                          v-model="formData.description" 
                          :placeholder="$t('agent.editor.descriptionPlaceholder')"
                          :autosize="{ minRows: 2, maxRows: 4 }"
                          :disabled="isBuiltinAgent"
                        />
                      </div>
                    </div>

                    <!-- 系统提示词 -->
                    <div class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.systemPrompt') }} <span v-if="!isBuiltinAgent" class="required">*</span></label>
                        <p class="desc">{{ $t('agentEditor.desc.systemPrompt') }}{{ isBuiltinAgent ? $t('agentEditor.desc.leaveEmptyDefault') : '' }}</p>
                        <div class="placeholder-tags">
                          <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                          <t-tooltip 
                            v-for="placeholder in availablePlaceholders" 
                            :key="placeholder.name"
                            :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                            placement="top"
                          >
                            <span 
                              class="placeholder-tag"
                              @click="handlePlaceholderClick('system', placeholder.name)"
                              v-text="'{{' + placeholder.name + '}}'"
                            ></span>
                          </t-tooltip>
                          <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                        </div>
                      </div>
                      <div class="setting-control setting-control-full" style="position: relative;">
                        <!-- Agent模式：统一提示词（使用 {{web_search_status}} 占位符动态控制行为） -->
                        <div v-if="isAgentMode" class="textarea-with-template">
                          <t-textarea 
                            ref="promptTextareaRef"
                            v-model="formData.config.system_prompt" 
                            :placeholder="systemPromptPlaceholder"
                            :autosize="{ minRows: 10, maxRows: 25 }"
                            @input="handlePromptInput"
                            class="system-prompt-textarea"
                          />
                          <PromptTemplateSelector 
                            type="agentSystemPrompt" 
                            position="corner"
                            :hasKnowledgeBase="hasKnowledgeBase"
                            @select="handleSystemPromptTemplateSelect"
                            @reset-default="handleAgentSystemPromptResetDefault"
                          />
                        </div>
                        <!-- 普通模式：单个提示词 -->
                        <div v-else class="textarea-with-template">
                          <t-textarea 
                            ref="promptTextareaRef"
                            v-model="formData.config.system_prompt" 
                            :placeholder="systemPromptPlaceholder"
                            :autosize="{ minRows: 10, maxRows: 25 }"
                            @input="handlePromptInput"
                            class="system-prompt-textarea"
                          />
                          <PromptTemplateSelector 
                            type="systemPrompt" 
                            position="corner"
                            :hasKnowledgeBase="hasKnowledgeBase"
                            @select="handleSystemPromptTemplateSelect"
                            @reset-default="handleSystemPromptTemplateSelect"
                          />
                        </div>
                        <!-- 占位符提示下拉框 -->
                        <Teleport to="body">
                          <div
                            v-if="showPlaceholderPopup && filteredPlaceholders.length > 0"
                            class="placeholder-popup-wrapper"
                            :style="popupStyle"
                          >
                            <div class="placeholder-popup">
                              <div
                                v-for="(placeholder, index) in filteredPlaceholders"
                                :key="placeholder.name"
                                class="placeholder-item"
                                :class="{ active: selectedPlaceholderIndex === index }"
                                @mousedown.prevent="insertPlaceholder(placeholder.name, true)"
                                @mouseenter="selectedPlaceholderIndex = index"
                              >
                                <div class="placeholder-name">
                                  <code v-html="`{{${placeholder.name}}}`"></code>
                                </div>
                                <div class="placeholder-desc">{{ placeholder.description }}</div>
                              </div>
                            </div>
                          </div>
                        </Teleport>
                      </div>
                    </div>

                    <!-- 上下文模板（仅普通模式） -->
                    <div v-if="!isAgentMode" class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.contextTemplate') }} <span v-if="!isBuiltinAgent" class="required">*</span></label>
                        <p class="desc">{{ $t('agentEditor.desc.contextTemplate') }}{{ isBuiltinAgent ? $t('agentEditor.desc.leaveEmptyDefault') : '' }}</p>
                        <div class="placeholder-tags">
                          <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                          <t-tooltip 
                            v-for="placeholder in contextTemplatePlaceholders" 
                            :key="placeholder.name"
                            :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                            placement="top"
                          >
                            <span 
                              class="placeholder-tag"
                              @click="handlePlaceholderClick('context', placeholder.name)"
                              v-text="'{{' + placeholder.name + '}}'"
                            ></span>
                          </t-tooltip>
                          <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                        </div>
                      </div>
                      <div class="setting-control setting-control-full" style="position: relative;">
                        <div class="textarea-with-template">
                          <t-textarea 
                            ref="contextTemplateTextareaRef"
                            v-model="formData.config.context_template" 
                            :placeholder="contextTemplatePlaceholder"
                            :autosize="{ minRows: 8, maxRows: 20 }"
                            @input="handleContextTemplateInput"
                            class="system-prompt-textarea"
                          />
                          <PromptTemplateSelector 
                            type="contextTemplate" 
                            position="corner"
                            :hasKnowledgeBase="hasKnowledgeBase"
                            @select="handleContextTemplateSelect"
                            @reset-default="handleContextTemplateSelect"
                          />
                        </div>
                        <!-- 上下文模板占位符提示下拉框 -->
                        <Teleport to="body">
                          <div
                            v-if="showContextPlaceholderPopup && filteredContextPlaceholders.length > 0"
                            class="placeholder-popup-wrapper"
                            :style="contextPopupStyle"
                          >
                            <div class="placeholder-popup">
                              <div
                                v-for="(placeholder, index) in filteredContextPlaceholders"
                                :key="placeholder.name"
                                class="placeholder-item"
                                :class="{ active: selectedContextPlaceholderIndex === index }"
                                @mousedown.prevent="insertContextPlaceholder(placeholder.name, true)"
                                @mouseenter="selectedContextPlaceholderIndex = index"
                              >
                                <div class="placeholder-name">
                                  <code v-html="`{{${placeholder.name}}}`"></code>
                                </div>
                                <div class="placeholder-desc">{{ placeholder.description }}</div>
                              </div>
                            </div>
                          </div>
                        </Teleport>
                      </div>
                    </div>

                  </div>
                </div>

                <!-- 模型配置 -->
                <div v-show="currentSection === 'model'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.modelConfig') }}</h2>
                    <p class="section-description">{{ $t('agent.editor.modelConfigDesc') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 模型选择 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.model') }} <span class="required">*</span></label>
                        <p class="desc">{{ $t('agentEditor.desc.model') }}</p>
                      </div>
                      <div class="setting-control">
                        <ModelSelector
                          model-type="KnowledgeQA"
                          :selected-model-id="formData.config.model_id"
                          :all-models="allModels"
                          @update:selected-model-id="(val: string) => formData.config.model_id = val"
                          @add-model="handleAddModel('llm')"
                          :placeholder="$t('agent.editor.modelPlaceholder')"
                        />
                      </div>
                    </div>

                    <!-- 温度 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.temperature') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.temperature') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="slider-wrapper">
                          <t-slider v-model="formData.config.temperature" :min="0" :max="1" :step="0.1" />
                          <span class="slider-value">{{ formData.config.temperature }}</span>
                        </div>
                      </div>
                    </div>

                    <!-- 最大生成Token数（仅普通模式） -->
                    <div v-if="!isAgentMode" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.maxCompletionTokens') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.maxTokens') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-input-number v-model="formData.config.max_completion_tokens" :min="100" :max="100000" :step="100" theme="column" />
                      </div>
                    </div>

                    <!-- 思考模式 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.thinking') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.thinking') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="thinkingEnabled" />
                      </div>
                    </div>

                  </div>
                </div>

                <!-- 多模态配置 -->
                <div v-show="currentSection === 'multimodal'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agentEditor.imageUpload.sectionTitle') }}</h2>
                    <p class="section-description">{{ $t('agentEditor.imageUpload.sectionDesc') }}</p>
                  </div>

                  <div class="settings-group">
                    <!-- 图片上传（多模态） -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.imageUpload.label') }}</label>
                        <p class="desc">{{ $t('agentEditor.imageUpload.desc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.image_upload_enabled" />
                      </div>
                    </div>

                    <!-- VLM模型（图片上传启用时） -->
                    <div v-if="formData.config.image_upload_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.imageUpload.vlmModel') }} <span class="required">*</span></label>
                        <p class="desc">{{ $t('agentEditor.imageUpload.vlmModelDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <ModelSelector
                          model-type="VLLM"
                          :selected-model-id="formData.config.vlm_model_id"
                          :all-models="allModels"
                          @update:selected-model-id="(val: string) => formData.config.vlm_model_id = val"
                          @add-model="handleAddModel('vllm')"
                          :placeholder="$t('agentEditor.imageUpload.vlmModelPlaceholder')"
                        />
                      </div>
                    </div>

                    <!-- 图片存储 Provider（图片上传启用时） -->
                    <div v-if="formData.config.image_upload_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.imageUpload.storageProvider') }}</label>
                        <p class="desc">{{ $t('agentEditor.imageUpload.storageProviderDesc') }}</p>
                      </div>
                      <div class="setting-control" style="flex-direction: column; align-items: flex-end;">
                        <t-select
                          v-model="formData.config.image_storage_provider"
                          style="width: 280px;"
                          :placeholder="$t('agentEditor.imageUpload.storageProviderPlaceholder')"
                          clearable
                        >
                          <t-option value="" :label="$t('agentEditor.imageUpload.storageDefault')" />
                          <t-option
                            v-for="opt in imageStorageOptions"
                            :key="opt.value"
                            :value="opt.value"
                            :label="opt.label"
                            :disabled="opt.disabled"
                          >
                            <span class="select-option-with-tag">
                              <span>{{ opt.label }}</span>
                              <t-tag v-if="opt.disabled" theme="warning" variant="light" size="small">{{ $t('agentEditor.imageUpload.notConfigured') }}</t-tag>
                            </span>
                          </t-option>
                        </t-select>
                        <a href="javascript:void(0)" class="go-settings-link" @click.prevent="uiStore.openSettings('storage')">
                          {{ $t('agentEditor.imageUpload.goStorageSettings') }}
                        </a>
                      </div>
                    </div>

                    <!-- 音频上传开关 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.audioUpload.label') }}</label>
                        <p class="desc">{{ $t('agentEditor.audioUpload.desc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.audio_upload_enabled" />
                      </div>
                    </div>

                    <!-- ASR模型（音频上传启用时） -->
                    <div v-if="formData.config.audio_upload_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.audioUpload.asrModel') }}</label>
                        <p class="desc">{{ $t('agentEditor.audioUpload.asrModelDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <ModelSelector
                          model-type="ASR"
                          :selected-model-id="formData.config.asr_model_id"
                          :all-models="allModels"
                          @update:selected-model-id="(val: string) => formData.config.asr_model_id = val"
                          @add-model="handleAddModel('asr')"
                          :placeholder="$t('agentEditor.audioUpload.asrModelPlaceholder')"
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 多轮对话（仅普通模式显示，Agent模式内部自动控制） -->
                <div v-show="currentSection === 'conversation' && !isAgentMode" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.conversationSettings') }}</h2>
                    <p class="section-description">{{ $t('agentEditor.desc.conversationSection') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 多轮对话 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.multiTurn') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.multiTurn') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.multi_turn_enabled" />
                      </div>
                    </div>

                    <!-- 保留轮数 -->
                    <div v-if="formData.config.multi_turn_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.historyTurns') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.historyRounds') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-input-number v-model="formData.config.history_turns" :min="1" :max="20" theme="column" />
                      </div>
                    </div>

                    <!-- 问题改写（仅多轮对话开启且普通模式时显示） -->
                    <div v-if="formData.config.multi_turn_enabled && !isAgentMode" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.enableRewrite') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.rewrite') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.enable_rewrite" />
                      </div>
                    </div>

                    <!-- 改写系统提示词 -->
                    <div v-if="formData.config.multi_turn_enabled && !isAgentMode && formData.config.enable_rewrite" class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.rewritePromptSystem') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.rewriteSystemPrompt') }}</p>
                        <div class="placeholder-tags" v-if="rewriteSystemPlaceholders.length > 0">
                          <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                          <t-tooltip 
                            v-for="placeholder in rewriteSystemPlaceholders" 
                            :key="placeholder.name"
                            :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                            placement="top"
                          >
                            <span 
                              class="placeholder-tag"
                              @click="handlePlaceholderClick('rewriteSystem', placeholder.name)"
                              v-text="'{{' + placeholder.name + '}}'"
                            ></span>
                          </t-tooltip>
                          <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                        </div>
                      </div>
                      <div class="setting-control setting-control-full" style="position: relative;">
                        <div class="textarea-with-template">
                          <t-textarea 
                            ref="rewriteSystemTextareaRef"
                            v-model="formData.config.rewrite_prompt_system" 
                            :placeholder="defaultRewritePromptSystem || $t('agent.editor.rewritePromptSystemPlaceholder')"
                            :autosize="{ minRows: 4, maxRows: 10 }"
                            @input="handleRewriteSystemInput"
                          />
                          <PromptTemplateSelector 
                            type="rewrite" 
                            position="corner"
                            @select="handleRewriteTemplateSelect"
                            @reset-default="handleRewriteTemplateSelect"
                          />
                        </div>
                        <Teleport to="body">
                          <div
                            v-if="rewriteSystemPopup.show && filteredRewriteSystemPlaceholders.length > 0"
                            class="placeholder-popup-wrapper"
                            :style="rewriteSystemPopup.style"
                          >
                            <div class="placeholder-popup">
                              <div
                                v-for="(placeholder, index) in filteredRewriteSystemPlaceholders"
                                :key="placeholder.name"
                                class="placeholder-item"
                                :class="{ active: rewriteSystemPopup.selectedIndex === index }"
                                @mousedown.prevent="insertGenericPlaceholder('rewriteSystem', placeholder.name, true)"
                                @mouseenter="rewriteSystemPopup.selectedIndex = index"
                              >
                                <div class="placeholder-name">
                                  <code v-html="`{{${placeholder.name}}}`"></code>
                                </div>
                                <div class="placeholder-desc">{{ placeholder.description }}</div>
                              </div>
                            </div>
                          </div>
                        </Teleport>
                      </div>
                    </div>

                    <!-- 改写用户提示词 -->
                    <div v-if="formData.config.multi_turn_enabled && !isAgentMode && formData.config.enable_rewrite" class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.rewritePromptUser') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.rewriteUserPrompt') }}</p>
                        <div class="placeholder-tags" v-if="rewritePlaceholders.length > 0">
                          <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                          <t-tooltip 
                            v-for="placeholder in rewritePlaceholders" 
                            :key="placeholder.name"
                            :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                            placement="top"
                          >
                            <span 
                              class="placeholder-tag"
                              @click="handlePlaceholderClick('rewriteUser', placeholder.name)"
                              v-text="'{{' + placeholder.name + '}}'"
                            ></span>
                          </t-tooltip>
                          <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                        </div>
                      </div>
                      <div class="setting-control setting-control-full" style="position: relative;">
                        <div class="textarea-with-template">
                          <t-textarea 
                            ref="rewriteUserTextareaRef"
                            v-model="formData.config.rewrite_prompt_user" 
                            :placeholder="defaultRewritePromptUser || $t('agent.editor.rewritePromptUserPlaceholder')"
                            :autosize="{ minRows: 4, maxRows: 10 }"
                            @input="handleRewriteUserInput"
                          />
                          <PromptTemplateSelector 
                            type="rewrite" 
                            position="corner"
                            @select="handleRewriteTemplateSelect"
                            @reset-default="handleRewriteTemplateSelect"
                          />
                        </div>
                        <Teleport to="body">
                          <div
                            v-if="rewriteUserPopup.show && filteredRewriteUserPlaceholders.length > 0"
                            class="placeholder-popup-wrapper"
                            :style="rewriteUserPopup.style"
                          >
                            <div class="placeholder-popup">
                              <div
                                v-for="(placeholder, index) in filteredRewriteUserPlaceholders"
                                :key="placeholder.name"
                                class="placeholder-item"
                                :class="{ active: rewriteUserPopup.selectedIndex === index }"
                                @mousedown.prevent="insertGenericPlaceholder('rewriteUser', placeholder.name, true)"
                                @mouseenter="rewriteUserPopup.selectedIndex = index"
                              >
                                <div class="placeholder-name">
                                  <code v-html="`{{${placeholder.name}}}`"></code>
                                </div>
                                <div class="placeholder-desc">{{ placeholder.description }}</div>
                              </div>
                            </div>
                          </div>
                        </Teleport>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 工具配置（仅 Agent 模式） -->
                <div v-show="currentSection === 'tools' && isAgentMode" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.toolsConfig') }}</h2>
                    <p class="section-description">{{ $t('agent.editor.toolsConfigDesc') }}</p>
                  </div>

                  <!-- 合并面板：能力状态 + 预设切换 -->
                  <div class="tools-overview">
                    <div class="tools-overview-row">
                      <div class="tools-status-chip">
                        <t-icon name="folder" />
                        <template v-if="kbSelectionMode === 'none'">
                          <span>{{ $t('agentEditor.tools.statusNoKb') }}</span>
                        </template>
                        <template v-else>
                          <span class="tools-status-metric">
                            <strong>{{ ragKbCount }}</strong> {{ $t('agentEditor.tools.kbMetricRag') }}
                          </span>
                          <span class="tools-status-sep">·</span>
                          <span class="tools-status-metric">
                            <strong>{{ wikiKbCount }}</strong> {{ $t('agentEditor.tools.kbMetricWiki') }}
                          </span>
                        </template>
                      </div>
                      <div v-if="inactiveToolCount > 0" class="tools-status-chip tools-status-chip--warn">
                        <t-icon name="error-circle" />
                        <span>{{ $t('agentEditor.tools.statusInactive', { count: inactiveToolCount }) }}</span>
                      </div>
                    </div>
                  </div>

                  <div class="settings-group">
                    <!-- 允许的工具（按组渲染，统一网格） -->
                    <div class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.allowedTools') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.selectTools') }}</p>
                      </div>
                      <div class="setting-control setting-control-full">
                        <t-checkbox-group v-model="formData.config.allowed_tools" class="tool-groups">
                          <section
                            v-for="group in groupedAvailableTools"
                            :key="group.key"
                            :class="['tool-group', `tool-group--${group.key}`]"
                          >
                            <header class="tool-group-header">
                              <span class="tool-group-bar" />
                              <span class="tool-group-title">{{ group.label }}</span>
                              <span class="tool-group-count">{{ group.tools.length }}</span>
                              <span v-if="group.key === 'wiki_edit'" class="tool-group-warning">
                                <t-icon name="error-circle" />
                                {{ $t('agentEditor.tools.writeWarning') }}
                              </span>
                            </header>
                            <div class="tool-grid">
                              <t-checkbox
                                v-for="tool in group.tools"
                                :key="tool.value"
                                :value="tool.value"
                                :disabled="tool.disabled"
                                :class="['tool-card', { 'tool-card--disabled': tool.disabled, 'tool-card--danger': tool.danger }]"
                              >
                                <div class="tool-card-body">
                                  <div class="tool-card-head">
                                    <span class="tool-card-name">{{ tool.label }}</span>
                                    <span v-if="tool.danger" class="tool-card-badge">
                                      {{ $t('agentEditor.tools.dangerTag') }}
                                    </span>
                                  </div>
                                  <span v-if="tool.description" class="tool-card-desc">{{ tool.description }}</span>
                                  <span v-if="tool.disabled && tool.disabledReason" class="tool-card-hint">
                                    {{ tool.disabledReason }}
                                  </span>
                                </div>
                              </t-checkbox>
                            </div>
                          </section>
                        </t-checkbox-group>
                      </div>
                    </div>

                    <!-- 有效工具预览：所见即所得 -->
                    <div class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.tools.effectiveLabel') }}</label>
                        <p class="desc">{{ $t('agentEditor.tools.effectiveDesc') }}</p>
                      </div>
                      <div class="setting-control setting-control-full">
                        <div class="effective-tools">
                          <template v-if="effectiveTools.length === 0">
                            <div class="effective-tools-empty">
                              {{ $t('agentEditor.tools.effectiveEmpty') }}
                            </div>
                          </template>
                          <template v-else>
                            <span
                              v-for="item in effectiveTools"
                              :key="item.value"
                              :class="['effective-chip', { 'effective-chip--inactive': !item.active }]"
                              :title="item.reason || ''"
                            >
                              <span class="effective-chip-label">{{ item.label }}</span>
                              <span v-if="!item.active" class="effective-chip-reason">{{ item.reason }}</span>
                            </span>
                          </template>
                        </div>
                      </div>
                    </div>

                    <!-- 最大迭代次数 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.maxIterations') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.maxIterations') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-input-number v-model="formData.config.max_iterations" :min="1" :max="50" theme="column" />
                      </div>
                    </div>

                    <!-- LLM 调用超时时间 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.llmCallTimeout.label') }}</label>
                        <p class="desc">{{ $t('agentEditor.llmCallTimeout.desc') }}</p>
                        <p class="desc-hint">{{ $t('agentEditor.llmCallTimeout.hint') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-input-number 
                          v-model="formData.config.llm_call_timeout" 
                          :min="0" 
                          :max="600" 
                          theme="column"
                          :placeholder="$t('agentEditor.llmCallTimeout.placeholder')"
                          clearable
                        />
                      </div>
                    </div>

                    <!-- MCP 服务选择 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.mcp.label') }}</label>
                        <p class="desc">{{ $t('agentEditor.mcp.desc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-radio-group v-model="mcpSelectionMode">
                          <t-radio-button value="all">{{ $t('agentEditor.selection.all') }}</t-radio-button>
                          <t-radio-button value="selected">{{ $t('agentEditor.selection.selected') }}</t-radio-button>
                          <t-radio-button value="none">{{ $t('agentEditor.selection.disabled') }}</t-radio-button>
                        </t-radio-group>
                      </div>
                    </div>

                    <!-- 选择指定 MCP 服务 -->
                    <div v-if="mcpSelectionMode === 'selected' && mcpOptions.length > 0" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.mcp.selectLabel') }}</label>
                        <p class="desc">{{ $t('agentEditor.mcp.selectDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-select
                          v-model="formData.config.mcp_services"
                          multiple
                          :placeholder="$t('agentEditor.mcp.selectPlaceholder')"
                          filterable
                        >
                          <t-option 
                            v-for="mcp in mcpOptions" 
                            :key="mcp.value" 
                            :value="mcp.value" 
                            :label="mcp.label" 
                          />
                        </t-select>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- Skills 配置（仅 Agent 模式） -->
                <div v-show="currentSection === 'skills' && isAgentMode" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.skillsConfig') }}</h2>
                    <p class="section-description">{{ $t('agent.editor.skillsConfigDesc') }}</p>
                  </div>

                  <div class="settings-group">
                    <!-- Skills 选择模式 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.skillsSelection') }}</label>
                        <p class="desc">{{ $t('agent.editor.skillsSelectionDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-radio-group v-model="skillsSelectionMode">
                          <t-radio-button value="all">{{ $t('agent.editor.skillsAll') }}</t-radio-button>
                          <t-radio-button value="selected">{{ $t('agent.editor.skillsSelected') }}</t-radio-button>
                          <t-radio-button value="none">{{ $t('agent.editor.skillsNone') }}</t-radio-button>
                        </t-radio-group>
                      </div>
                    </div>

                    <!-- 选择指定 Skills -->
                    <div v-if="skillsSelectionMode === 'selected' && skillOptions.length > 0" class="setting-row setting-row-vertical">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.selectSkills') }}</label>
                        <p class="desc">{{ $t('agent.editor.selectSkillsDesc') }}</p>
                      </div>
                      <div class="setting-control setting-control-full">
                        <t-checkbox-group v-model="formData.config.selected_skills" class="skills-checkbox-group">
                          <t-checkbox
                            v-for="skill in skillOptions"
                            :key="skill.name"
                            :value="skill.name"
                            class="skill-checkbox-item"
                          >
                            <div class="skill-item-content">
                              <span class="skill-name">{{ skill.name }}</span>
                              <span class="skill-desc">{{ skill.description }}</span>
                            </div>
                          </t-checkbox>
                        </t-checkbox-group>
                      </div>
                    </div>

                    <!-- 无可用 Skills 提示 -->
                    <div v-if="skillOptions.length === 0" class="setting-row">
                      <div class="setting-info">
                        <p class="desc empty-hint">{{ $t('agent.editor.noSkillsAvailable') }}</p>
                      </div>
                    </div>

                    <!-- Skills 说明 -->
                    <div class="skill-info-box">
                      <t-icon name="lightbulb" class="info-icon" />
                      <div class="info-content">
                        <p><strong>{{ $t('agent.editor.skillsInfoTitle') }}</strong></p>
                        <p>{{ $t('agent.editor.skillsInfoContent') }}</p>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 知识库配置 -->
                <div v-show="currentSection === 'knowledge'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.knowledgeConfig') }}</h2>
                    <p class="section-description">{{ $t('agent.editor.knowledgeConfigDesc') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 关联知识库 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.knowledgeBases') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.kbScope') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-radio-group v-model="kbSelectionMode">
                          <t-radio-button value="all">{{ $t('agent.editor.allKnowledgeBases') }}</t-radio-button>
                          <t-radio-button value="selected">{{ $t('agent.editor.selectedKnowledgeBases') }}</t-radio-button>
                          <t-radio-button value="none">{{ $t('agent.editor.noKnowledgeBase') }}</t-radio-button>
                        </t-radio-group>
                      </div>
                    </div>

                    <!-- 选择指定知识库（仅在选择"指定知识库"时显示） -->
                    <div v-if="kbSelectionMode === 'selected'" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.selectKnowledgeBases') }}</label>
                        <p class="desc">{{ $t('agent.editor.selectKnowledgeBasesDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-select 
                          v-model="formData.config.knowledge_bases" 
                          multiple 
                          :placeholder="$t('agent.editor.selectKnowledgeBases')"
                          filterable
                          :min-collapsed-num="3"
                        >
                          <t-option-group v-if="filteredMyKbOptions.length" :label="$t('agent.editor.myKnowledgeBases')">
                            <t-option
                              v-for="kb in filteredMyKbOptions"
                              :key="kb.value"
                              :value="kb.value"
                              :label="kb.label"
                              :disabled="kb.disabled"
                            >
                              <div class="kb-option-item" :title="kb.disabled ? kb.disabledReason : ''">
                                <span class="kb-option-icon" :class="kb.type === 'faq' ? 'faq-icon' : 'doc-icon'">
                                  <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                                </span>
                                <span class="kb-option-label">{{ kb.label }}</span>
                                <span v-if="kb.ragEnabled" class="kb-option-tag tag-rag">RAG</span>
                                <span v-if="kb.wikiEnabled" class="kb-option-tag tag-wiki">Wiki</span>
                                <span class="kb-option-count">{{ kb.count || 0 }}</span>
                                <span v-if="kb.disabled" class="kb-option-disabled-hint">{{ kb.disabledReason }}</span>
                              </div>
                            </t-option>
                          </t-option-group>
                          <t-option-group v-if="filteredSharedKbOptions.length" :label="$t('agent.editor.sharedKnowledgeBases')">
                            <t-option
                              v-for="kb in filteredSharedKbOptions"
                              :key="kb.value"
                              :value="kb.value"
                              :label="kb.label"
                              :disabled="kb.disabled"
                            >
                              <div class="kb-option-item" :title="kb.disabled ? kb.disabledReason : ''">
                                <span class="kb-option-icon" :class="kb.type === 'faq' ? 'faq-icon' : 'doc-icon'">
                                  <t-icon :name="kb.type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                                </span>
                                <span class="kb-option-label">{{ kb.label }}</span>
                                <span v-if="kb.ragEnabled" class="kb-option-tag tag-rag">RAG</span>
                                <span v-if="kb.wikiEnabled" class="kb-option-tag tag-wiki">Wiki</span>
                                <span v-if="kb.orgName" class="kb-option-org">{{ kb.orgName }}</span>
                                <span class="kb-option-count">{{ kb.count || 0 }}</span>
                                <span v-if="kb.disabled" class="kb-option-disabled-hint">{{ kb.disabledReason }}</span>
                              </div>
                            </t-option>
                          </t-option-group>
                        </t-select>
                      </div>
                    </div>

                    <!-- 支持的文件类型（限制用户可选择的文件类型） -->
                    <div v-if="hasKnowledgeBase" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agentEditor.fileTypes.label') }}</label>
                        <p class="desc">{{ $t('agentEditor.fileTypes.desc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-select 
                          v-model="formData.config.supported_file_types" 
                          multiple 
                          :placeholder="$t('agentEditor.fileTypes.allTypes')"
                          :min-collapsed-num="3"
                          clearable
                        >
                          <t-option 
                            v-for="ft in availableFileTypes" 
                            :key="ft.value" 
                            :value="ft.value" 
                            :label="ft.label"
                          />
                        </t-select>
                      </div>
                    </div>

                    <!-- 仅在提及时检索知识库（当配置了知识库时显示） -->
                    <div v-if="hasKnowledgeBase" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.retrieveKBOnlyWhenMentioned') }}</label>
                        <p class="desc">{{ $t('agent.editor.retrieveKBOnlyWhenMentionedDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.retrieve_kb_only_when_mentioned" />
                      </div>
                    </div>

                    <!-- ReRank 模型（当配置了知识库时显示） -->
                    <div v-if="needsRerankModel" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.rerankModel') }} <span class="required">*</span></label>
                        <p class="desc">{{ $t('agent.editor.rerankModelDesc') }}</p>
                      </div>
                      <div class="setting-control">
                        <ModelSelector
                          model-type="Rerank"
                          :selected-model-id="formData.config.rerank_model_id"
                          :all-models="allModels"
                          @update:selected-model-id="(val: string) => formData.config.rerank_model_id = val"
                          @add-model="handleAddModel('rerank')"
                          :placeholder="$t('agent.editor.rerankModelPlaceholder')"
                        />
                      </div>
                    </div>

                    <!-- FAQ 策略设置（仅当选择了 FAQ 类型知识库时显示） -->
                    <div v-if="hasFaqKnowledgeBase" class="faq-strategy-section">
                      <div class="faq-strategy-header">
                        <t-icon name="chat-bubble-help" class="faq-icon" />
                        <span>{{ $t('agentEditor.faq.title') }}</span>
                        <t-tooltip :content="$t('agentEditor.faq.tooltip')">
                          <t-icon name="help-circle" class="help-icon" />
                        </t-tooltip>
                      </div>

                      <!-- FAQ 优先开关 -->
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ $t('agentEditor.faq.enableLabel') }}</label>
                          <p class="desc">{{ $t('agentEditor.faq.enableDesc') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-switch v-model="formData.config.faq_priority_enabled" />
                        </div>
                      </div>

                      <!-- FAQ 直接回答阈值 -->
                      <div v-if="formData.config.faq_priority_enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ $t('agentEditor.faq.thresholdLabel') }}</label>
                          <p class="desc">{{ $t('agentEditor.faq.thresholdDesc') }}</p>
                        </div>
                        <div class="setting-control">
                          <div class="slider-wrapper">
                            <t-slider v-model="formData.config.faq_direct_answer_threshold" :min="0.7" :max="1" :step="0.05" />
                            <span class="slider-value">{{ formData.config.faq_direct_answer_threshold?.toFixed(2) }}</span>
                          </div>
                        </div>
                      </div>

                      <!-- FAQ 分数加权 -->
                      <div v-if="formData.config.faq_priority_enabled" class="setting-row">
                        <div class="setting-info">
                          <label>{{ $t('agentEditor.faq.boostLabel') }}</label>
                          <p class="desc">{{ $t('agentEditor.faq.boostDesc') }}</p>
                        </div>
                        <div class="setting-control">
                          <div class="slider-wrapper">
                            <t-slider v-model="formData.config.faq_score_boost" :min="1" :max="2" :step="0.1" />
                            <span class="slider-value">{{ formData.config.faq_score_boost?.toFixed(1) }}x</span>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 网络搜索配置 -->
                <div v-show="currentSection === 'websearch'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.webSearchConfig') }}</h2>
                    <p class="section-description">{{ $t('agent.editor.webSearchConfigDesc') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 网络搜索 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.webSearch') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.webSearch') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.web_search_enabled" />
                      </div>
                    </div>

                    <!-- 网络搜索最大结果数 -->
                    <div v-if="formData.config.web_search_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.webSearchProvider') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.webSearchProvider') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-select
                          v-model="formData.config.web_search_provider_id"
                          clearable
                          :placeholder="$t('agent.editor.webSearchProviderPlaceholder')"
                          style="width: 240px;"
                        >
                          <t-option
                            v-for="p in webSearchProviderList"
                            :key="p.id"
                            :value="p.id"
                            :label="p.name"
                          >
                            <span>{{ p.name }}</span>
                            <t-tag v-if="p.is_default" theme="primary" size="small" style="margin-left: 6px;">{{ $t('common.default') }}</t-tag>
                          </t-option>
                        </t-select>
                      </div>
                    </div>

                    <!-- 网络搜索最大结果数 -->
                    <div v-if="formData.config.web_search_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.webSearchMaxResults') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.webSearchMaxResults') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="slider-wrapper">
                          <t-slider v-model="formData.config.web_search_max_results" :min="1" :max="10" />
                          <span class="slider-value">{{ formData.config.web_search_max_results }}</span>
                        </div>
                      </div>
                    </div>

                    <!-- 自动抓取页面内容 -->
                    <div v-if="formData.config.web_search_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.webFetchEnabled') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.webFetchEnabled') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.web_fetch_enabled" />
                      </div>
                    </div>

                    <!-- 抓取页面数 -->
                    <div v-if="formData.config.web_search_enabled && formData.config.web_fetch_enabled" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.webFetchTopN') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.webFetchTopN') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="slider-wrapper">
                          <t-slider v-model="formData.config.web_fetch_top_n" :min="1" :max="10" />
                          <span class="slider-value">{{ formData.config.web_fetch_top_n }}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 检索策略（仅在有知识库能力时显示） -->
                <div v-show="currentSection === 'retrieval' && hasKnowledgeBase" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agent.editor.retrievalStrategy') }}</h2>
                    <p class="section-description">{{ $t('agentEditor.desc.retrievalSection') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 查询扩展（仅普通模式） -->
                    <div v-if="!isAgentMode" class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.enableQueryExpansion') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.queryExpansion') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-switch v-model="formData.config.enable_query_expansion" />
                      </div>
                    </div>

                    <!-- 向量召回TopK -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.embeddingTopK') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.embeddingTopK') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-input-number v-model="formData.config.embedding_top_k" :min="1" :max="50" theme="column" />
                      </div>
                    </div>

                    <!-- 关键词阈值 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.keywordThreshold') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.keywordThreshold') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="slider-wrapper">
                          <t-slider v-model="formData.config.keyword_threshold" :min="0" :max="1" :step="0.01" />
                          <span class="slider-value">{{ formData.config.keyword_threshold?.toFixed(2) }}</span>
                        </div>
                      </div>
                    </div>

                    <!-- 向量阈值 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.vectorThreshold') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.vectorThreshold') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="slider-wrapper">
                          <t-slider v-model="formData.config.vector_threshold" :min="0" :max="1" :step="0.01" />
                          <span class="slider-value">{{ formData.config.vector_threshold?.toFixed(2) }}</span>
                        </div>
                      </div>
                    </div>

                    <!-- 重排TopK -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.rerankTopK') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.rerankTopK') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-input-number v-model="formData.config.rerank_top_k" :min="1" :max="20" theme="column" />
                      </div>
                    </div>

                    <!-- 重排阈值 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('agent.editor.rerankThreshold') }}</label>
                        <p class="desc">{{ $t('agentEditor.desc.rerankThreshold') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="slider-wrapper">
                          <t-slider v-model="formData.config.rerank_threshold" :min="-10" :max="10" :step="0.01" />
                          <span class="slider-value">{{ formData.config.rerank_threshold?.toFixed(1) }}</span>
                        </div>
                      </div>
                    </div>

                    <!-- 兜底策略（仅普通模式） -->
                    <template v-if="!isAgentMode">
                      <div class="setting-row">
                        <div class="setting-info">
                          <label>{{ $t('agent.editor.fallbackStrategy') }}</label>
                          <p class="desc">{{ $t('agentEditor.desc.fallbackStrategy') }}</p>
                        </div>
                        <div class="setting-control">
                          <t-radio-group v-model="formData.config.fallback_strategy">
                            <t-radio-button value="fixed">{{ $t('agentEditor.fallback.fixed') }}</t-radio-button>
                            <t-radio-button value="model">{{ $t('agentEditor.fallback.model') }}</t-radio-button>
                          </t-radio-group>
                        </div>
                      </div>

                      <!-- 固定兜底回复 -->
                      <div v-if="formData.config.fallback_strategy === 'fixed'" class="setting-row setting-row-vertical">
                        <div class="setting-info">
                          <label>{{ $t('agent.editor.fallbackResponse') }}</label>
                          <p class="desc">{{ $t('agentEditor.desc.fallbackResponse') }}</p>
                        </div>
                        <div class="setting-control setting-control-full">
                          <div class="textarea-with-template">
                            <t-textarea 
                              v-model="formData.config.fallback_response" 
                              :placeholder="defaultFallbackResponse || $t('agent.editor.fallbackResponsePlaceholder')"
                              :autosize="{ minRows: 2, maxRows: 6 }"
                            />
                            <PromptTemplateSelector 
                              type="fallback" 
                              position="corner"
                              fallbackMode="fixed"
                              @select="handleFallbackResponseTemplateSelect"
                              @reset-default="handleFallbackResponseTemplateSelect"
                            />
                          </div>
                        </div>
                      </div>

                      <!-- 兜底提示词 -->
                      <div v-if="formData.config.fallback_strategy === 'model'" class="setting-row setting-row-vertical">
                        <div class="setting-info">
                          <label>{{ $t('agent.editor.fallbackPrompt') }}</label>
                          <p class="desc">{{ $t('agentEditor.desc.fallbackPrompt') }}</p>
                          <div class="placeholder-tags" v-if="fallbackPlaceholders.length > 0">
                            <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                            <t-tooltip 
                              v-for="placeholder in fallbackPlaceholders" 
                              :key="placeholder.name"
                              :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                              placement="top"
                            >
                              <span 
                                class="placeholder-tag"
                                @click="handlePlaceholderClick('fallback', placeholder.name)"
                                v-text="'{{' + placeholder.name + '}}'"
                              ></span>
                            </t-tooltip>
                            <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                          </div>
                        </div>
                        <div class="setting-control setting-control-full" style="position: relative;">
                          <div class="textarea-with-template">
                            <t-textarea 
                              ref="fallbackPromptTextareaRef"
                              v-model="formData.config.fallback_prompt" 
                              :placeholder="defaultFallbackPrompt || $t('agent.editor.fallbackPromptPlaceholder')"
                              :autosize="{ minRows: 4, maxRows: 10 }"
                              @input="handleFallbackPromptInput"
                            />
                            <PromptTemplateSelector 
                              type="fallback" 
                              position="corner"
                              fallbackMode="model"
                              @select="handleFallbackPromptTemplateSelect"
                              @reset-default="handleFallbackPromptTemplateSelect"
                            />
                          </div>
                          <Teleport to="body">
                            <div
                              v-if="fallbackPromptPopup.show && filteredFallbackPlaceholders.length > 0"
                              class="placeholder-popup-wrapper"
                              :style="fallbackPromptPopup.style"
                            >
                              <div class="placeholder-popup">
                                <div
                                  v-for="(placeholder, index) in filteredFallbackPlaceholders"
                                  :key="placeholder.name"
                                  class="placeholder-item"
                                  :class="{ active: fallbackPromptPopup.selectedIndex === index }"
                                  @mousedown.prevent="insertGenericPlaceholder('fallback', placeholder.name, true)"
                                  @mouseenter="fallbackPromptPopup.selectedIndex = index"
                                >
                                  <div class="placeholder-name">
                                    <code v-html="`{{${placeholder.name}}}`"></code>
                                  </div>
                                  <div class="placeholder-desc">{{ placeholder.description }}</div>
                                </div>
                              </div>
                            </div>
                          </Teleport>
                        </div>
                      </div>
                    </template>
                  </div>
                </div>

                <!-- 共享管理（仅编辑模式且非内置智能体） -->
                <div v-if="props.mode === 'edit' && props.agent?.id && !props.agent?.is_builtin" v-show="currentSection === 'share'" class="section">
                  <AgentShareSettings :agent-id="props.agent.id" :agent="props.agent" />
                </div>

                <!-- IM集成（仅编辑模式） -->
                <div v-if="props.mode === 'edit' && props.agent?.id" v-show="currentSection === 'im'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agentEditor.im.title') }}</h2>
                    <p class="section-description">
                      {{ $t('agentEditor.im.description') }}
                      <a href="https://github.com/Tencent/WeKnora/blob/main/docs/IM%E9%9B%86%E6%88%90%E5%BC%80%E5%8F%91%E6%96%87%E6%A1%A3.md" target="_blank" rel="noopener noreferrer" class="doc-link">
                        {{ $t('agentEditor.im.docLink') }}
                        <t-icon name="link" class="link-icon" />
                      </a>
                    </p>
                  </div>
                  <div class="settings-group">
                    <IMChannelPanel :agent-id="props.agent.id" />
                  </div>
                </div>
              </div>

              <!-- 底部操作栏 -->
              <div class="settings-footer">
                <t-button variant="outline" @click="handleClose">{{ $t('common.cancel') }}</t-button>
                <t-button theme="primary" :loading="saving" @click="handleSave">{{ $t('common.confirm') }}</t-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  createAgent,
  updateAgent,
  getPlaceholders,
  getAgentTypePresets,
  type CustomAgent,
  type PlaceholderDefinition,
  type AgentTypePreset,
  type AgentType,
  type AgentTypeKBFilter,
  type KBCapabilities,
} from '@/api/agent';
import { listModels, type ModelConfig } from '@/api/model';
import { listKnowledgeBases } from '@/api/knowledge-base';
import { listMCPServices, type MCPService } from '@/api/mcp-service';
import { listSkills, type SkillInfo } from '@/api/skill';
import { listWebSearchProviders, type WebSearchProviderEntity } from '@/api/web-search-provider';
import { getAgentConfig, getConversationConfig, getStorageEngineStatus, getPromptTemplates, type StorageEngineStatusItem, type PromptTemplate } from '@/api/system';
import { useUIStore } from '@/stores/ui';
import { useAuthStore } from '@/stores/auth';
import { useOrganizationStore } from '@/stores/organization';
import AgentAvatar from '@/components/AgentAvatar.vue';
import PromptTemplateSelector from '@/components/PromptTemplateSelector.vue';
import ModelSelector from '@/components/ModelSelector.vue';
import AgentShareSettings from '@/components/AgentShareSettings.vue';
import IMChannelPanel from '@/components/IMChannelPanel.vue';
import {
  evaluateToolRequirement,
  deriveKbFilterFromTools,
  type RequirementMissKind,
  type ScopeCapabilities,
} from '@/utils/tool-capabilities';

const uiStore = useUIStore();
const authStore = useAuthStore();
const orgStore = useOrganizationStore();

const { t, locale: i18nLocale } = useI18n();

const props = defineProps<{
  visible: boolean;
  mode: 'create' | 'edit';
  agent?: CustomAgent | null;
  initialSection?: string;
}>();

const emit = defineEmits<{
  (e: 'update:visible', visible: boolean): void;
  (e: 'success'): void;
}>();

const currentSection = ref(props.initialSection || 'basic');
const saving = ref(false);
const allModels = ref<ModelConfig[]>([]);
const kbOptions = ref<{ label: string; value: string; type?: 'document' | 'faq'; count?: number; shared?: boolean; orgName?: string; ragEnabled?: boolean; wikiEnabled?: boolean; capabilities?: KBCapabilities }[]>([]);

// 智能体类型预设（仅 smart-reasoning 模式下展示）
const agentTypePresets = ref<AgentTypePreset[]>([]);
// Agent 系统提示词模板缓存（用于切换智能体类型时根据 system_prompt_id 解析出实际文本填入）
const agentSystemPromptTemplates = ref<PromptTemplate[]>([]);
const mcpOptions = ref<{ label: string; value: string }[]>([]);
const webSearchProviderList = ref<WebSearchProviderEntity[]>([]);
const skillOptions = ref<{ name: string; description: string }[]>([]);
// 是否允许启用 Skills（取决于后端沙箱是否启用，disabled 时为 false；未请求前为 false 避免闪显）
const skillsAvailable = ref(false);
// 存储引擎可用状态（用于图片存储 provider 选择）
const storageEngineStatus = ref<StorageEngineStatusItem[]>([]);
const imageStorageOptions = computed(() => {
  const statusMap: Record<string, boolean> = {};
  for (const e of storageEngineStatus.value) {
    statusMap[e.name] = e.available;
  }
  return [
    { value: 'local', label: t('settings.storage.engineLocal'), disabled: false },
    { value: 'minio', label: 'MinIO', disabled: statusMap.minio === false },
    { value: 'cos', label: t('settings.storage.engineCos'), disabled: statusMap.cos === false },
    { value: 'tos', label: t('settings.storage.engineTos'), disabled: statusMap.tos === false },
    { value: 's3', label: 'Amazon S3', disabled: statusMap.s3 === false },
    { value: 'oss', label: t('settings.storage.engineOss'), disabled: statusMap.oss === false },
  ];
});

// 系统默认配置（用于内置智能体显示默认提示词）
const defaultAgentSystemPrompt = ref('');  // Agent 模式的默认系统提示词（来自 agent-config）
const defaultNormalSystemPrompt = ref('');  // 普通模式的默认系统提示词（来自 conversation-config）
const defaultContextTemplate = ref('');
const defaultRewritePromptSystem = ref('');
const defaultRewritePromptUser = ref('');
const defaultFallbackPrompt = ref('');
const defaultFallbackResponse = ref('');
// 默认检索参数
const defaultEmbeddingTopK = ref(10);
const defaultKeywordThreshold = ref(0.3);
const defaultVectorThreshold = ref(0.5);
const defaultRerankTopK = ref(5);
const defaultRerankThreshold = ref(0.5);
const defaultMaxCompletionTokens = ref(2048);
const defaultTemperature = ref(0.7);

// 知识库相关工具列表（用于 watch(hasKnowledgeBase) 从"无"变"有"时 seed 默认工具）
const knowledgeBaseTools = ['grep_chunks', 'knowledge_search', 'list_knowledge_chunks', 'query_knowledge_graph', 'get_document_info', 'database_query'];

// Wiki 读取类工具（用于 watch(agentMode) 切到 smart-reasoning 时 seed 默认工具）
const wikiReadTools = ['wiki_search', 'wiki_read_page', 'wiki_read_source_doc', 'wiki_flag_issue'];

// 初始化标志，防止初始化时触发 watch 自动添加工具
const isInitializing = ref(false);

// 知识库选择模式：all=全部, selected=指定, none=不使用
const kbSelectionMode = ref<'all' | 'selected' | 'none'>('none');

// MCP 服务选择模式：all=全部, selected=指定, none=不使用
const mcpSelectionMode = ref<'all' | 'selected' | 'none'>('none');

// Skills 选择模式：all=全部, selected=指定, none=不使用
const skillsSelectionMode = ref<'all' | 'selected' | 'none'>('none');

// 可用工具列表（与后台 internal/agent/tools/definitions.go 保持一致）
// group 决定 UI 分组：base / rag / wiki_read / wiki_edit / wiki_issue / data
// danger: 写类破坏性工具，UI 上给出显著提示
// 工具的 KB 能力依赖关系统一在 `@/utils/tool-capabilities` 声明，
// `availableTools` 通过 `evaluateToolRequirement` 读取，不在这里重复维护。
const allTools = computed(() => [
  // 基础思考类
  { value: 'thinking', label: t('agentEditor.tools.thinking'), description: t('agentEditor.tools.thinkingDesc'), group: 'base' },
  { value: 'todo_write', label: t('agentEditor.tools.todoWrite'), description: t('agentEditor.tools.todoWriteDesc'), group: 'base' },
  // 知识库语义/关键词检索
  { value: 'grep_chunks', label: t('agentEditor.tools.grepChunks'), description: t('agentEditor.tools.grepChunksDesc'), group: 'rag' },
  { value: 'knowledge_search', label: t('agentEditor.tools.knowledgeSearch'), description: t('agentEditor.tools.knowledgeSearchDesc'), group: 'rag' },
  { value: 'list_knowledge_chunks', label: t('agentEditor.tools.listChunks'), description: t('agentEditor.tools.listChunksDesc'), group: 'rag' },
  { value: 'query_knowledge_graph', label: t('agentEditor.tools.queryGraph'), description: t('agentEditor.tools.queryGraphDesc'), group: 'rag' },
  { value: 'get_document_info', label: t('agentEditor.tools.getDocInfo'), description: t('agentEditor.tools.getDocInfoDesc'), group: 'rag' },
  { value: 'database_query', label: t('agentEditor.tools.dbQuery'), description: t('agentEditor.tools.dbQueryDesc'), group: 'rag' },
  // Wiki 读取类（阅读、搜索、标记问题）
  { value: 'wiki_search', label: t('agentEditor.tools.wikiSearch'), description: t('agentEditor.tools.wikiSearchDesc'), group: 'wiki_read' },
  { value: 'wiki_read_page', label: t('agentEditor.tools.wikiReadPage'), description: t('agentEditor.tools.wikiReadPageDesc'), group: 'wiki_read' },
  { value: 'wiki_read_source_doc', label: t('agentEditor.tools.wikiReadSourceDoc'), description: t('agentEditor.tools.wikiReadSourceDocDesc'), group: 'wiki_read' },
  { value: 'wiki_flag_issue', label: t('agentEditor.tools.wikiFlagIssue'), description: t('agentEditor.tools.wikiFlagIssueDesc'), group: 'wiki_read' },
  // Wiki 编辑类（会直接修改 Wiki 内容）
  { value: 'wiki_write_page', label: t('agentEditor.tools.wikiWritePage'), description: t('agentEditor.tools.wikiWritePageDesc'), group: 'wiki_edit', danger: true },
  { value: 'wiki_replace_text', label: t('agentEditor.tools.wikiReplaceText'), description: t('agentEditor.tools.wikiReplaceTextDesc'), group: 'wiki_edit', danger: true },
  { value: 'wiki_rename_page', label: t('agentEditor.tools.wikiRenamePage'), description: t('agentEditor.tools.wikiRenamePageDesc'), group: 'wiki_edit', danger: true },
  { value: 'wiki_delete_page', label: t('agentEditor.tools.wikiDeletePage'), description: t('agentEditor.tools.wikiDeletePageDesc'), group: 'wiki_edit', danger: true },
  // Wiki 巡检类
  { value: 'wiki_read_issue', label: t('agentEditor.tools.wikiReadIssue'), description: t('agentEditor.tools.wikiReadIssueDesc'), group: 'wiki_issue' },
  { value: 'wiki_update_issue', label: t('agentEditor.tools.wikiUpdateIssue'), description: t('agentEditor.tools.wikiUpdateIssueDesc'), group: 'wiki_issue' },
  // 数据分析
  { value: 'data_analysis', label: t('agentEditor.tools.dataAnalysis'), description: t('agentEditor.tools.dataAnalysisDesc'), group: 'data' },
  { value: 'data_schema', label: t('agentEditor.tools.dataSchema'), description: t('agentEditor.tools.dataSchemaDesc'), group: 'data' },
]);

// 工具分组元信息
const toolGroups = computed(() => [
  { key: 'base',       label: t('agentEditor.tools.groupBase') },
  { key: 'rag',        label: t('agentEditor.tools.groupRag') },
  { key: 'wiki_read',  label: t('agentEditor.tools.groupWikiRead') },
  { key: 'wiki_edit',  label: t('agentEditor.tools.groupWikiEdit') },
  { key: 'wiki_issue', label: t('agentEditor.tools.groupWikiIssue') },
  { key: 'data',       label: t('agentEditor.tools.groupData') },
]);

// 知识库分组：我的 vs 共享的
const myKbOptions = computed(() => kbOptions.value.filter(kb => !kb.shared));
const sharedKbOptions = computed(() => kbOptions.value.filter(kb => kb.shared));

// 根据知识库配置动态计算是否有知识库能力
const hasKnowledgeBase = computed(() => {
  return kbSelectionMode.value !== 'none';
});

// 当前配置下进入到智能体作用域的知识库列表
// 注意：用户可能选了 knowledge_bases（按库级），也可能选了 knowledge_ids（按文档级）
// 这里仅用于 UI 上的工具可用性判定，按库级来计算
const kbsInScope = computed(() => {
  if (kbSelectionMode.value === 'none') return [];
  if (kbSelectionMode.value === 'all') return kbOptions.value;
  const selectedIds = formData.value.config.knowledge_bases || [];
  return kbOptions.value.filter(kb => selectedIds.includes(kb.value));
});

// 是否存在至少一个启用了 RAG 能力的知识库（向量 or 关键词）
const hasRagKnowledgeBase = computed(() => {
  return kbsInScope.value.some(kb => kb.ragEnabled);
});

// 是否存在至少一个启用了 Wiki 能力的知识库
const hasWikiKnowledgeBase = computed(() => {
  return kbsInScope.value.some(kb => kb.wikiEnabled);
});

// 作用域内 RAG/Wiki 知识库数量（用于顶部状态栏）
const ragKbCount = computed(() => kbsInScope.value.filter(kb => kb.ragEnabled).length);
const wikiKbCount = computed(() => kbsInScope.value.filter(kb => kb.wikiEnabled).length);

// 检测选择的知识库中是否包含 FAQ 类型
const hasFaqKnowledgeBase = computed(() => {
  if (kbSelectionMode.value === 'none') return false;
  if (kbSelectionMode.value === 'all') {
    // 全部知识库模式，检查是否有任何 FAQ 类型的知识库
    return kbOptions.value.some(kb => kb.type === 'faq');
  }
  // 指定知识库模式，检查选中的知识库中是否有 FAQ 类型
  const selectedKbIds = formData.value.config.knowledge_bases || [];
  return kbOptions.value.some(kb => selectedKbIds.includes(kb.value) && kb.type === 'faq');
});

// 把"作用域内 KB 能力"聚合成一个 ScopeCapabilities 对象，交给
// `evaluateToolRequirement` 统一判定；UI 上的所有可用性提示都应出自此处。
const scopeCapabilities = computed<ScopeCapabilities>(() => {
  const scope: ScopeCapabilities = { vector: false, keyword: false, wiki: false, graph: false, faq: false };
  for (const kb of kbsInScope.value) {
    const caps = kb.capabilities;
    if (caps) {
      if (caps.vector) scope.vector = true;
      if (caps.keyword) scope.keyword = true;
      if (caps.wiki) scope.wiki = true;
      if (caps.graph) scope.graph = true;
      if (caps.faq) scope.faq = true;
    } else {
      // 向后兼容：capabilities 尚未加载时，退回到 ragEnabled/wikiEnabled 推断
      if (kb.ragEnabled) { scope.vector = true; scope.keyword = true; }
      if (kb.wikiEnabled) scope.wiki = true;
      if (kb.type === 'faq') scope.faq = true;
    }
  }
  return scope;
});

// 把 evaluateToolRequirement 返回的 missKind 映射到 i18n 文案。
// 新增的 needsGraph / needsFaq 暂时复用 requiresRagKb 文案（"需要 RAG 知识库"），
// 后续可按需增加独立的 i18n 键。
const missKindToReason = (kind: RequirementMissKind): string | undefined => {
  switch (kind) {
    case 'needsKb':    return t('agentEditor.tools.requiresKb');
    case 'needsWiki':  return t('agentEditor.tools.requiresWikiKb');
    case 'needsRag':
    case 'needsGraph':
    case 'needsFaq':   return t('agentEditor.tools.requiresRagKb');
    case 'none':
    default:           return undefined;
  }
};

const availableTools = computed(() => {
  const scope = scopeCapabilities.value;
  const hasAnyKb = hasKnowledgeBase.value;
  return allTools.value.map(tool => {
    const { ok, missKind } = evaluateToolRequirement(tool.value, scope, hasAnyKb);
    return {
      ...tool,
      disabled: !ok,
      disabledReason: ok ? undefined : missKindToReason(missKind),
    };
  });
});

// 按分组切片后的工具列表，用于模板分组渲染
const groupedAvailableTools = computed(() => {
  const map: Record<string, typeof availableTools.value> = {};
  for (const tool of availableTools.value) {
    const g = tool.group || 'base';
    if (!map[g]) map[g] = [];
    map[g].push(tool);
  }
  return toolGroups.value
    .map(g => ({
      ...g,
      tools: map[g.key] || [],
    }))
    .filter(g => g.tools.length > 0);
});

// ==================== 有效工具预览 ====================
// 最终运行时智能体实际能使用的工具集合（仅做预览展示）
// 规则：基于 allowed_tools 过滤
//   1) 勾选但缺失对应能力（无 KB / 无 Wiki 能力 KB）的工具会被灰显/隐藏
//   2) 无论是否勾选，web_search / web_fetch 随 web_search_enabled 出现
//   3) final_answer 始终存在
//   4) 当 kb_selection_mode === 'none' 时，RAG/Wiki 工具都视为不可用
const effectiveTools = computed(() => {
  const chosen = new Set(formData.value.config.allowed_tools || []);
  const items: Array<{ value: string; label: string; reason?: string; active: boolean }> = [];
  for (const tool of availableTools.value) {
    const picked = chosen.has(tool.value);
    if (!picked) continue;
    if (tool.disabled) {
      items.push({ value: tool.value, label: tool.label, active: false, reason: tool.disabledReason });
    } else {
      items.push({ value: tool.value, label: tool.label, active: true });
    }
  }
  if (formData.value.config.web_search_enabled) {
    items.push({ value: 'web_search', label: t('agentEditor.tools.webSearch'), active: true });
    items.push({ value: 'web_fetch',  label: t('agentEditor.tools.webFetch'),  active: true });
  }
  items.push({ value: 'final_answer', label: t('agentEditor.tools.finalAnswer'), active: true });
  return items;
});

// 勾选了但当前配置下无法生效的工具数量（用于顶部状态提示）
const inactiveToolCount = computed(() => effectiveTools.value.filter(i => !i.active).length);

// 可用文件类型列表
const availableFileTypes = [
  { value: 'pdf', label: 'PDF', description: t('agentEditor.fileTypes.pdf') },
  { value: 'docx', label: 'Word', description: t('agentEditor.fileTypes.word') },
  { value: 'txt', label: t('agentEditor.fileTypes.textLabel'), description: t('agentEditor.fileTypes.text') },
  { value: 'md', label: 'Markdown', description: t('agentEditor.fileTypes.markdown') },
  { value: 'csv', label: 'CSV', description: t('agentEditor.fileTypes.csv') },
  { value: 'xlsx', label: 'Excel', description: t('agentEditor.fileTypes.excel') },
  { value: 'jpg', label: t('agentEditor.fileTypes.imageLabel'), description: t('agentEditor.fileTypes.image') },
];

// 占位符相关 - 从 API 获取
const placeholderData = ref<{
  system_prompt: PlaceholderDefinition[];
  agent_system_prompt: PlaceholderDefinition[];
  context_template: PlaceholderDefinition[];
  rewrite_system_prompt: PlaceholderDefinition[];
  rewrite_prompt: PlaceholderDefinition[];
  fallback_prompt: PlaceholderDefinition[];
}>({
  system_prompt: [],
  agent_system_prompt: [],
  context_template: [],
  rewrite_system_prompt: [],
  rewrite_prompt: [],
  fallback_prompt: [],
});

// 系统提示词占位符（根据模式动态选择）
const availablePlaceholders = computed(() => {
  return isAgentMode.value ? placeholderData.value.agent_system_prompt : placeholderData.value.system_prompt;
});

// 上下文模板占位符
const contextTemplatePlaceholders = computed(() => placeholderData.value.context_template);

// 改写系统提示词占位符
const rewriteSystemPlaceholders = computed(() => placeholderData.value.rewrite_system_prompt);

// 改写用户提示词占位符
const rewritePlaceholders = computed(() => placeholderData.value.rewrite_prompt);

// 兜底提示词占位符
const fallbackPlaceholders = computed(() => placeholderData.value.fallback_prompt);

const promptTextareaRef = ref<any>(null);
const showPlaceholderPopup = ref(false);
const selectedPlaceholderIndex = ref(0);
const placeholderPrefix = ref('');
const popupStyle = ref({ top: '0px', left: '0px' });
let placeholderPopupTimer: any = null;

// 上下文模板占位符相关
const contextTemplateTextareaRef = ref<any>(null);
const showContextPlaceholderPopup = ref(false);
const selectedContextPlaceholderIndex = ref(0);
const contextPlaceholderPrefix = ref('');
const contextPopupStyle = ref({ top: '0px', left: '0px' });
let contextPlaceholderPopupTimer: any = null;

// 通用占位符弹出相关（用于改写提示词和兜底提示词）
interface PlaceholderPopupState {
  show: boolean;
  selectedIndex: number;
  prefix: string;
  style: { top: string; left: string };
  timer: any;
  fieldKey: string;
  placeholders: PlaceholderDefinition[];
}

const rewriteSystemPopup = ref<PlaceholderPopupState>({
  show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' }, timer: null, fieldKey: 'rewrite_prompt_system', placeholders: []
});
const rewriteUserPopup = ref<PlaceholderPopupState>({
  show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' }, timer: null, fieldKey: 'rewrite_prompt_user', placeholders: []
});
const fallbackPromptPopup = ref<PlaceholderPopupState>({
  show: false, selectedIndex: 0, prefix: '', style: { top: '0px', left: '0px' }, timer: null, fieldKey: 'fallback_prompt', placeholders: []
});

const rewriteSystemTextareaRef = ref<any>(null);
const rewriteUserTextareaRef = ref<any>(null);
const fallbackPromptTextareaRef = ref<any>(null);

const navItems = computed(() => {
  const items: { key: string; icon: string; label: string }[] = [
    { key: 'basic', icon: 'info-circle', label: t('agent.editor.basicInfo') },
    { key: 'model', icon: 'control-platform', label: t('agent.editor.modelConfig') },
  ];
  // 知识库配置（放在工具上面）
  items.push({ key: 'knowledge', icon: 'folder', label: t('agent.editor.knowledgeConfig') });
  // Agent模式才显示工具配置
  if (isAgentMode.value) {
    items.push({ key: 'tools', icon: 'tools', label: t('agent.editor.toolsConfig') });
  }
  // Agent 模式且沙箱已启用时才显示 Skills 配置（disabled 时无法启用 Skills）
  if (isAgentMode.value && skillsAvailable.value) {
    items.push({ key: 'skills', icon: 'lightbulb', label: t('agent.editor.skillsConfig') });
  }
  // 有知识库能力时才显示检索策略
  if (hasKnowledgeBase.value) {
    items.push({ key: 'retrieval', icon: 'search', label: t('agent.editor.retrievalStrategy') });
  }
  // 网络搜索（独立菜单）
  items.push({ key: 'websearch', icon: 'internet', label: t('agent.editor.webSearchConfig') });
  // 多模态配置（图片上传）
  items.push({ key: 'multimodal', icon: 'image', label: t('agentEditor.imageUpload.navLabel') });
  // 多轮对话（仅普通模式显示，Agent模式内部自动控制）
  if (!isAgentMode.value) {
    items.push({ key: 'conversation', icon: 'chat', label: t('agent.editor.conversationSettings') });
  }
  // 共享管理（仅编辑模式且非内置智能体，Lite 模式下隐藏）
  if (props.mode === 'edit' && props.agent?.id && !props.agent?.is_builtin && !authStore.isLiteMode) {
    items.push({ key: 'share', icon: 'share', label: t('knowledgeEditor.sidebar.share') });
  }
  // IM集成（仅编辑模式，创建时Agent还没有ID）
  if (props.mode === 'edit' && props.agent?.id) {
    items.push({ key: 'im', icon: 'chat-message', label: t('agentEditor.im.title') });
  }
  return items;
});

// 初始数据
const defaultFormData = {
  name: '',
  description: '',
  is_builtin: false,
  config: {
    // 基础设置
    agent_mode: 'smart-reasoning' as 'quick-answer' | 'smart-reasoning',
    system_prompt: '',
    context_template: '{{query}}',
    // 模型设置
    model_id: '',
    rerank_model_id: '',
    temperature: 0.7,
    max_completion_tokens: 2048,
    thinking: false, // 默认禁用思考模式
    // Agent模式设置
    max_iterations: 10,
    llm_call_timeout: 120,  // 120 seconds
    allowed_tools: [] as string[],
    reflection_enabled: false,
    // MCP 服务设置
    mcp_selection_mode: 'none' as 'all' | 'selected' | 'none',
    mcp_services: [] as string[],
    // Skills 设置
    skills_selection_mode: 'none' as 'all' | 'selected' | 'none',
    selected_skills: [] as string[],
    // 知识库设置：新建智能体默认选择 "全部知识库"，
    // 让用户无需先去勾选 KB 即可上手；如有需要可改为 "selected" / "none"。
    kb_selection_mode: 'all' as 'all' | 'selected' | 'none',
    knowledge_bases: [] as string[],
    retrieve_kb_only_when_mentioned: false,
    // 智能推理下的类型预设：新建 agent 时默认给 RAG 问答（最常用场景）。
    // 编辑既有 agent 时会被 agent 自己保存的 agent_type 覆盖。
    agent_type: 'rag-qa' as AgentType,
    system_prompt_id: '' as string,
    // 图片上传/多模态设置
    image_upload_enabled: false,
    vlm_model_id: '',
    image_storage_provider: '',
    // 文件类型限制
    supported_file_types: [] as string[],
    // FAQ 策略设置
    faq_priority_enabled: true, // 是否启用 FAQ 优先策略
    faq_direct_answer_threshold: 0.9, // FAQ 直接回答阈值（相似度高于此值直接使用 FAQ 答案）
    faq_score_boost: 1.2, // FAQ 分数加权系数
    // 网络搜索设置
    web_search_enabled: false,
    web_search_max_results: 5,
    // 多轮对话设置
    multi_turn_enabled: false,
    history_turns: 5,
    // 检索策略设置
    embedding_top_k: 10,
    keyword_threshold: 0.3,
    vector_threshold: 0.5,
    rerank_top_k: 5,
    rerank_threshold: 0.5,
    // 高级设置（普通模式）
    enable_query_expansion: true,
    enable_rewrite: true,
    rewrite_prompt_system: '',
    rewrite_prompt_user: '',
    fallback_strategy: 'model' as 'fixed' | 'model',
    fallback_response: '',
    fallback_prompt: '',
    // 已废弃字段（保留兼容）
    welcome_message: '',
    suggested_prompts: [] as string[],
  }
};

const formData = ref(JSON.parse(JSON.stringify(defaultFormData)));
const agentMode = computed({
  get: () => formData.value.config.agent_mode,
  set: (val: 'quick-answer' | 'smart-reasoning') => { formData.value.config.agent_mode = val; }
});

const isAgentMode = computed(() => agentMode.value === 'smart-reasoning');

// ============================================================================
// 智能体类型预设（仅 smart-reasoning 模式下可见）
// 选择类型后自动填充 system_prompt_id / allowed_tools 等；
// 选择 "custom" 或没有匹配预设时不做任何覆盖。
// ============================================================================

const agentType = computed({
  get: () => (formData.value.config.agent_type as AgentType) || 'custom',
  set: (val: AgentType) => { formData.value.config.agent_type = val; },
});

// 当前激活的预设对象（用于 KB 过滤 / UI 徽章）
const activeAgentTypePreset = computed<AgentTypePreset | null>(() => {
  if (!isAgentMode.value) return null;
  const id = agentType.value;
  if (!id || id === 'custom') return null;
  return agentTypePresets.value.find(p => p.id === id) || null;
});

// 根据当前 locale 挑选 i18n 标签
const agentTypePresetLabel = (p: AgentTypePreset): string => {
  const locale = i18nLocale.value || 'default';
  return p.i18n?.[locale]?.label || p.i18n?.default?.label || p.id;
};
const agentTypePresetDescription = (p: AgentTypePreset): string => {
  const locale = i18nLocale.value || 'default';
  return p.i18n?.[locale]?.description || p.i18n?.default?.description || '';
};

// t-select 的 options 数据：label 给 TDesign 自己（用于选中态显示），desc 走自定义 option slot
const agentTypeSelectOptions = computed(() => {
  return agentTypePresets.value.map(p => ({
    value: p.id,
    label: agentTypePresetLabel(p),
    desc: agentTypePresetDescription(p),
  }));
});

// 为每个预设生成"我的 <label>"的默认名称，让用户可以一键保存
// custom 类型默认名为空（让用户自己想）
const getPresetDefaultName = (preset: AgentTypePreset | null): string => {
  if (!preset || preset.id === 'custom') return '';
  return t('agentEditor.agentType.defaultNamePattern', { label: agentTypePresetLabel(preset) });
};
const getPresetDefaultDescription = (preset: AgentTypePreset | null): string => {
  if (!preset) return '';
  return agentTypePresetDescription(preset);
};

// 判断当前名称/描述是否由系统自动填入（任一预设的默认值 或 空）
// 用于在切换类型时只覆盖"未被用户手动编辑"的值，避免覆盖用户输入
const isNameSystemGenerated = (name: string): boolean => {
  if (!name) return true;
  return agentTypePresets.value.some(p => getPresetDefaultName(p) === name);
};
const isDescriptionSystemGenerated = (desc: string): boolean => {
  if (!desc) return true;
  return agentTypePresets.value.some(p => getPresetDefaultDescription(p) === desc);
};

// 按预设 id 返回面向用户的不兼容原因文案。
// 不要直接把 "vector / keyword / wiki" 这些底层 capability 名回传给用户 —
// 用户不关心技术实现，只想知道"为什么我这个知识库不能用"。
const presetKbMismatchKeyMap: Record<string, string> = {
  'rag-qa':          'ragQa',
  'wiki-qa':         'wikiQa',
  'hybrid-rag-wiki': 'hybridRagWiki',
  'data-analysis':   'dataAnalysis',
};
const presetKbMismatchReason = (preset: AgentTypePreset): string => {
  const subKey = presetKbMismatchKeyMap[preset.id];
  if (subKey) return t(`agentEditor.agentType.kbMismatch.${subKey}`);
  return t('agentEditor.agentType.kbMismatch.generic');
};

// 计算预设的"有效 KB 过滤器"：工具推导 + YAML 增量叠加。
//
// 设计原则：
//   - 工具 → any_of（"KB 至少要能被其中一个工具用得上"）由
//     `deriveKbFilterFromTools` 自动算出；
//   - YAML 里的 `kb_filter` 只负责**工具推不出来**的业务规则（如
//     data-analysis 的 `none_of: ["faq"]`），作为增量合并，而不是整体覆盖；
//   - `all_of` / `none_of` 直接从 YAML 继承（工具不表达这类约束）。
//
// 这样 rag-qa / wiki-qa / hybrid 在 YAML 里彻底不写 `kb_filter`，
// data-analysis 只需声明额外的 `none_of`，"工具→能力"的映射只在
// `@/utils/tool-capabilities` 维护一份。
const effectiveKbFilter = (preset: AgentTypePreset | null): AgentTypeKBFilter | null => {
  if (!preset) return null;
  const derived = deriveKbFilterFromTools(preset.config?.allowed_tools || []);
  const yaml = preset.kb_filter;

  // YAML 提供 any_of 时整体覆盖推导（给显式控制留口子）；否则用推导的
  const anyOf = (yaml?.any_of && yaml.any_of.length > 0) ? yaml.any_of : (derived?.any_of ?? []);
  const allOf = yaml?.all_of ?? [];
  const noneOf = yaml?.none_of ?? [];
  if (anyOf.length === 0 && allOf.length === 0 && noneOf.length === 0) return null;
  return { any_of: anyOf, all_of: allOf, none_of: noneOf };
};

// 评估单个 KB 是否满足给定预设的 kb_filter
const kbSatisfiesPresetFilter = (kb: { capabilities?: KBCapabilities; ragEnabled?: boolean; wikiEnabled?: boolean; type?: string }, preset: AgentTypePreset | null): { ok: boolean; reason: string } => {
  const filter = effectiveKbFilter(preset);
  if (!preset || !filter) return { ok: true, reason: '' };
  const caps = kb.capabilities || {
    vector: !!kb.ragEnabled,
    keyword: !!kb.ragEnabled,
    wiki: !!kb.wikiEnabled,
    graph: false,
    faq: kb.type === 'faq',
  };
  const has = (name: string): boolean => {
    switch (name) {
      case 'vector': return !!caps.vector;
      case 'keyword': return !!caps.keyword;
      case 'wiki': return !!caps.wiki;
      case 'graph': return !!caps.graph;
      case 'faq': return !!caps.faq;
      default: return false;
    }
  };
  const reason = presetKbMismatchReason(preset);
  if (filter.all_of && filter.all_of.length > 0) {
    for (const n of filter.all_of) {
      if (!has(n)) return { ok: false, reason };
    }
  }
  if (filter.any_of && filter.any_of.length > 0) {
    if (!filter.any_of.some(n => has(n))) {
      return { ok: false, reason };
    }
  }
  if (filter.none_of && filter.none_of.length > 0) {
    for (const n of filter.none_of) {
      if (has(n)) return { ok: false, reason };
    }
  }
  return { ok: true, reason: '' };
};

// KB 过滤后的选项（用于"指定知识库"下拉）— 不满足的仍保留但标记 disabled + tooltip
const filteredKbOptionsForPreset = computed(() => {
  const preset = activeAgentTypePreset.value;
  return kbOptions.value.map(kb => {
    const { ok, reason } = kbSatisfiesPresetFilter(kb, preset);
    return { ...kb, disabled: !ok, disabledReason: reason };
  });
});
const filteredMyKbOptions = computed(() => filteredKbOptionsForPreset.value.filter(kb => !kb.shared));
const filteredSharedKbOptions = computed(() => filteredKbOptionsForPreset.value.filter(kb => kb.shared));

// 当前选中的 KB 中，有多少个在新预设下会被禁用（用于保存前提示）
const incompatibleSelectedKbCount = computed(() => {
  const preset = activeAgentTypePreset.value;
  if (!preset || kbSelectionMode.value !== 'selected') return 0;
  const selected = new Set(formData.value.config.knowledge_bases || []);
  return filteredKbOptionsForPreset.value.filter(kb => selected.has(kb.value) && kb.disabled).length;
});

// 应用一个预设的 config 到 formData.config（仅覆盖预设里明确设置的字段，其他不动）
const applyAgentTypePreset = (preset: AgentTypePreset | null) => {
  if (!preset || !preset.config) return;
  const c = preset.config;
  const target = formData.value.config;
  if (c.system_prompt_id !== undefined) {
    target.system_prompt_id = c.system_prompt_id;
    // 根据 system_prompt_id 从已加载的模板列表里查出正文并回填到用户可见的 textarea
    const tmpl = agentSystemPromptTemplates.value.find(t => t.id === c.system_prompt_id);
    if (tmpl && typeof tmpl.content === 'string') {
      target.system_prompt = tmpl.content;
    } else {
      // 模板列表还没加载完 / 或预设引用了不存在的 id：清空让用户感知到变化
      target.system_prompt = '';
      if (c.system_prompt_id) {
        console.warn(`[AgentType] system_prompt_id "${c.system_prompt_id}" not found in agent_system_prompt templates`);
      }
    }
  }
  if (typeof c.temperature === 'number') target.temperature = c.temperature;
  if (typeof c.max_iterations === 'number') target.max_iterations = c.max_iterations;
  if (Array.isArray(c.allowed_tools)) target.allowed_tools = [...c.allowed_tools];
  if (typeof c.retain_retrieval_history === 'boolean') target.retain_retrieval_history = c.retain_retrieval_history;
  if (typeof c.faq_priority_enabled === 'boolean') target.faq_priority_enabled = c.faq_priority_enabled;
  if (typeof c.web_search_enabled === 'boolean') target.web_search_enabled = c.web_search_enabled;
  // supported_file_types 采用"强同步"语义：只有 data-analysis 需要限定 csv/xlsx，
  // 其余类型切过来时必须清空，否则会从上一个类型带过来残留。
  if (Array.isArray(c.supported_file_types)) {
    target.supported_file_types = [...c.supported_file_types];
  } else {
    target.supported_file_types = [];
  }
  // kb_selection_mode 同步到 formData 以及 UI 状态（两处都要改，否则单选按钮不更新）
  if (c.kb_selection_mode) {
    target.kb_selection_mode = c.kb_selection_mode;
    kbSelectionMode.value = c.kb_selection_mode;
  }
};

// 用户手动切换类型 → 应用预设
const onAgentTypeChange = (val: AgentType) => {
  // 切换前捕获"名称/描述是否可安全覆盖"
  // 已编辑过的用户输入（不等于任何预设默认值）绝不覆盖
  const canOverrideName = isNameSystemGenerated(formData.value.name);
  const canOverrideDesc = isDescriptionSystemGenerated(formData.value.description);

  agentType.value = val;
  const preset = agentTypePresets.value.find(p => p.id === val) || null;
  if (val !== 'custom') {
    applyAgentTypePreset(preset);
  }

  // 用新预设的默认名/描述刷新自动填充字段
  if (canOverrideName) {
    formData.value.name = getPresetDefaultName(preset);
  }
  if (canOverrideDesc) {
    formData.value.description = getPresetDefaultDescription(preset);
  }

  // 如果新预设与当前已选 KB 冲突，软提示（不强制移除）
  if (incompatibleSelectedKbCount.value > 0) {
    MessagePlugin.warning(
      t('agentEditor.agentType.kbIncompatibleWarn', { count: incompatibleSelectedKbCount.value }),
      4000,
    );
  }
};

// 思考模式计算属性（直接绑定 boolean）
const thinkingEnabled = computed({
  get: () => formData.value.config.thinking === true,
  set: (val: boolean) => { formData.value.config.thinking = val; }
});

// 是否为内置智能体
const isBuiltinAgent = computed(() => {
  return formData.value.is_builtin === true;
});

// 系统提示词的 placeholder
const systemPromptPlaceholder = computed(() => {
  return t('agent.editor.systemPromptPlaceholder');
});

// 上下文模板的 placeholder
const contextTemplatePlaceholder = computed(() => {
  return t('agent.editor.contextTemplatePlaceholder');
});

// 是否需要配置 ReRank 模型（仅当关联的知识库中有 RAG 类型时需要）
const needsRerankModel = computed(() => {
  if (!hasKnowledgeBase.value) return false;
  const mode = kbSelectionMode.value;
  if (mode === 'all') {
    // "全部"模式下，只要存在任何一个 RAG 知识库就需要
    return kbOptions.value.some(kb => kb.ragEnabled);
  }
  if (mode === 'selected') {
    const selectedIds = formData.value.config.knowledge_bases || [];
    return kbOptions.value.some(kb => selectedIds.includes(kb.value) && kb.ragEnabled);
  }
  return false;
});

// 监听可见性变化，重置表单
watch(() => props.visible, async (val) => {
  if (val) {
    currentSection.value = props.initialSection || 'basic';
    // 先加载依赖数据（包括默认配置）
    await loadDependencies();
    
    if (props.mode === 'edit' && props.agent) {
      // 深度复制对象以避免引用问题
      const agentData = JSON.parse(JSON.stringify(props.agent));
      
      // 确保 config 对象存在
      if (!agentData.config) {
        agentData.config = JSON.parse(JSON.stringify(defaultFormData.config));
      }
      
      // 补全可能缺失的字段
      agentData.config = { ...defaultFormData.config, ...agentData.config };
      
      // 确保数组字段存在
      if (!agentData.config.suggested_prompts) agentData.config.suggested_prompts = [];
      if (!agentData.config.knowledge_bases) agentData.config.knowledge_bases = [];
      if (!agentData.config.allowed_tools) agentData.config.allowed_tools = [];
      if (!agentData.config.mcp_services) agentData.config.mcp_services = [];
      if (!agentData.config.selected_skills) agentData.config.selected_skills = [];
      if (!agentData.config.supported_file_types) agentData.config.supported_file_types = [];

      // 兼容旧数据：如果没有 agent_mode 字段，根据 allowed_tools 推断
      if (!agentData.config.agent_mode) {
        const isAgent = agentData.config.max_iterations > 1 || (agentData.config.allowed_tools && agentData.config.allowed_tools.length > 0);
        agentData.config.agent_mode = isAgent ? 'smart-reasoning' : 'quick-answer';
      }

      // 设置初始化标志，防止 watch 自动添加工具
      isInitializing.value = true;
      formData.value = agentData;
      // 初始化知识库选择模式
      initKbSelectionMode();
      initMcpSelectionMode();
      initSkillsSelectionMode();
      // 初始化完成后重置标志
      nextTick(() => {
        isInitializing.value = false;
      });
      // 内置智能体：如果提示词为空，填入系统默认值
      if (agentData.is_builtin) {
        fillBuiltinAgentDefaults();
      }
    } else {
      // 创建新智能体，使用系统默认值
      const newFormData = JSON.parse(JSON.stringify(defaultFormData));
      // 应用系统默认检索参数
      newFormData.config.embedding_top_k = defaultEmbeddingTopK.value;
      newFormData.config.keyword_threshold = defaultKeywordThreshold.value;
      newFormData.config.vector_threshold = defaultVectorThreshold.value;
      newFormData.config.rerank_top_k = defaultRerankTopK.value;
      newFormData.config.rerank_threshold = defaultRerankThreshold.value;
      newFormData.config.max_completion_tokens = defaultMaxCompletionTokens.value;
      newFormData.config.temperature = defaultTemperature.value;
      // 应用系统默认提示词（根据模式填充）
      const isAgent = newFormData.config.agent_mode === 'smart-reasoning';
      if (isAgent) {
        // Agent 模式使用 agent-config 的默认系统提示词
        if (defaultAgentSystemPrompt.value) {
          newFormData.config.system_prompt = defaultAgentSystemPrompt.value;
        }
      } else {
        // 快速问答模式使用 conversation-config 的默认提示词
        if (defaultNormalSystemPrompt.value) {
          newFormData.config.system_prompt = defaultNormalSystemPrompt.value;
        }
        if (defaultContextTemplate.value) {
          newFormData.config.context_template = defaultContextTemplate.value;
        }
        if (defaultRewritePromptSystem.value) {
          newFormData.config.rewrite_prompt_system = defaultRewritePromptSystem.value;
        }
        if (defaultRewritePromptUser.value) {
          newFormData.config.rewrite_prompt_user = defaultRewritePromptUser.value;
        }
        if (defaultFallbackPrompt.value) {
          newFormData.config.fallback_prompt = defaultFallbackPrompt.value;
        }
        if (defaultFallbackResponse.value) {
          newFormData.config.fallback_response = defaultFallbackResponse.value;
        }
      }
      formData.value = newFormData;
      // 新建智能体：知识库默认 "全部"，MCP / Skills 仍默认 "不使用"。
      kbSelectionMode.value = 'all';
      mcpSelectionMode.value = 'none';
      skillsSelectionMode.value = 'none';

      // 新建智能推理 agent 时，立即应用默认的 agent_type 预设
      // （补齐 system_prompt / allowed_tools / kb_selection_mode 等），
      // 否则用户在 modal 打开瞬间看到的"默认表单"和类型下拉显示的类型不一致。
      if (newFormData.config.agent_mode === 'smart-reasoning') {
        const defaultTypeId = newFormData.config.agent_type as AgentType;
        const preset = agentTypePresets.value.find(p => p.id === defaultTypeId) || null;
        if (defaultTypeId && defaultTypeId !== 'custom') {
          applyAgentTypePreset(preset);
        }
        // 给新建表单补上"我的 XXX"默认名 + 预设描述，让用户可直接保存；
        // 用户输入过的值不会被覆盖（此处是新建场景，字段必定为空）。
        if (!formData.value.name) {
          formData.value.name = getPresetDefaultName(preset);
        }
        if (!formData.value.description) {
          formData.value.description = getPresetDefaultDescription(preset);
        }
      }
    }
  }
});

// 初始化知识库选择模式
const initKbSelectionMode = () => {
  if (formData.value.config.kb_selection_mode) {
    // 如果有保存的模式，直接使用
    kbSelectionMode.value = formData.value.config.kb_selection_mode;
  } else if (formData.value.config.knowledge_bases?.length > 0) {
    // 有指定知识库
    kbSelectionMode.value = 'selected';
  } else {
    kbSelectionMode.value = 'none';
  }
};

// 初始化 MCP 选择模式
const initMcpSelectionMode = () => {
  if (formData.value.config.mcp_selection_mode) {
    // 如果有保存的模式，直接使用
    mcpSelectionMode.value = formData.value.config.mcp_selection_mode;
  } else if (formData.value.config.mcp_services?.length > 0) {
    // 有指定 MCP 服务
    mcpSelectionMode.value = 'selected';
  } else {
    mcpSelectionMode.value = 'none';
  }
};

// 初始化 Skills 选择模式
const initSkillsSelectionMode = () => {
  if (formData.value.config.skills_selection_mode) {
    // 如果有保存的模式，直接使用
    skillsSelectionMode.value = formData.value.config.skills_selection_mode;
  } else if (formData.value.config.selected_skills?.length > 0) {
    // 有指定 Skills
    skillsSelectionMode.value = 'selected';
  } else {
    skillsSelectionMode.value = 'none';
  }
};

// 内置智能体：填入系统默认值
const fillBuiltinAgentDefaults = () => {
  const config = formData.value.config;
  const isAgent = config.agent_mode === 'smart-reasoning';
  
  if (isAgent) {
    // Agent 模式：使用 agent-config 的默认提示词
    if (!config.system_prompt && defaultAgentSystemPrompt.value) {
      config.system_prompt = defaultAgentSystemPrompt.value;
    }
  } else {
    // 普通模式：使用 conversation-config 的默认系统提示词和上下文模板
    if (!config.system_prompt && defaultNormalSystemPrompt.value) {
      config.system_prompt = defaultNormalSystemPrompt.value;
    }
    if (!config.context_template && defaultContextTemplate.value) {
      config.context_template = defaultContextTemplate.value;
    }
  }
  
  // 通用默认值
  if (!config.rewrite_prompt_system && defaultRewritePromptSystem.value) {
    config.rewrite_prompt_system = defaultRewritePromptSystem.value;
  }
  if (!config.rewrite_prompt_user && defaultRewritePromptUser.value) {
    config.rewrite_prompt_user = defaultRewritePromptUser.value;
  }
  if (!config.fallback_prompt && defaultFallbackPrompt.value) {
    config.fallback_prompt = defaultFallbackPrompt.value;
  }
  if (!config.fallback_response && defaultFallbackResponse.value) {
    config.fallback_response = defaultFallbackResponse.value;
  }
};

// 监听知识库选择模式变化
watch(kbSelectionMode, (mode) => {
  formData.value.config.kb_selection_mode = mode;
  if (mode === 'none') {
    // 不使用知识库，清空相关配置
    formData.value.config.knowledge_bases = [];
  } else if (mode === 'all') {
    // 全部知识库，清空指定列表
    formData.value.config.knowledge_bases = [];
  }
  // selected 模式保持 knowledge_bases 不变
});

// 监听 MCP 选择模式变化
watch(mcpSelectionMode, (mode) => {
  formData.value.config.mcp_selection_mode = mode;
  if (mode === 'none') {
    // 不使用 MCP，清空相关配置
    formData.value.config.mcp_services = [];
  } else if (mode === 'all') {
    // 全部 MCP，清空指定列表
    formData.value.config.mcp_services = [];
  }
  // selected 模式保持 mcp_services 不变
});

// 监听 Skills 选择模式变化
watch(skillsSelectionMode, (mode) => {
  formData.value.config.skills_selection_mode = mode;
  if (mode === 'none') {
    // 不使用 Skills，清空相关配置
    formData.value.config.selected_skills = [];
  } else if (mode === 'all') {
    // 全部 Skills，清空指定列表
    formData.value.config.selected_skills = [];
  }
  // selected 模式保持 selected_skills 不变
});

// 监听模式变化，自动调整配置
watch(agentMode, (val, _oldVal) => {
  if (val === 'smart-reasoning') {
    // 切换到 Agent 模式，根据知识库配置启用工具。
    // 注意：默认不注入 thinking / todo_write —— 它们用于显式反思或多步计划，
    // 会显著增加 token 消耗，用户按需手动勾选。
    if (formData.value.config.allowed_tools.length === 0) {
      const tools: string[] = [];
      if (hasRagKnowledgeBase.value) {
        tools.push(
          'knowledge_search',
          'grep_chunks',
          'list_knowledge_chunks',
          'query_knowledge_graph',
          'get_document_info',
          'database_query',
        );
      }
      if (hasWikiKnowledgeBase.value) {
        tools.push(...wikiReadTools);
      }
      formData.value.config.allowed_tools = tools;
    }
    if (formData.value.config.max_iterations <= 1) {
      formData.value.config.max_iterations = 10;
    }
    // 切换到 Agent 模式时，如果系统提示词是快速问答的默认值或为空，替换为 Agent 默认提示词
    if (defaultAgentSystemPrompt.value) {
      const isDefaultNormalPrompt = formData.value.config.system_prompt === defaultNormalSystemPrompt.value;
      if (!formData.value.config.system_prompt || isDefaultNormalPrompt) {
        formData.value.config.system_prompt = defaultAgentSystemPrompt.value;
      }
    }
  } else {
    // 切换到普通模式，清空工具
    formData.value.config.allowed_tools = [];
    formData.value.config.max_iterations = 1; // 设置为1表示单轮 RAG
    // 切换到快速问答模式时，如果系统提示词是 Agent 的默认值或为空，替换为快速问答默认提示词
    if (defaultNormalSystemPrompt.value) {
      const isDefaultAgentPrompt = formData.value.config.system_prompt === defaultAgentSystemPrompt.value;
      if (!formData.value.config.system_prompt || isDefaultAgentPrompt) {
        formData.value.config.system_prompt = defaultNormalSystemPrompt.value;
      }
    }
    // 其他提示词只在为空时填充
    if (!formData.value.config.context_template && defaultContextTemplate.value) {
      formData.value.config.context_template = defaultContextTemplate.value;
    }
    if (!formData.value.config.rewrite_prompt_system && defaultRewritePromptSystem.value) {
      formData.value.config.rewrite_prompt_system = defaultRewritePromptSystem.value;
    }
    if (!formData.value.config.rewrite_prompt_user && defaultRewritePromptUser.value) {
      formData.value.config.rewrite_prompt_user = defaultRewritePromptUser.value;
    }
    if (!formData.value.config.fallback_prompt && defaultFallbackPrompt.value) {
      formData.value.config.fallback_prompt = defaultFallbackPrompt.value;
    }
    if (!formData.value.config.fallback_response && defaultFallbackResponse.value) {
      formData.value.config.fallback_response = defaultFallbackResponse.value;
    }
  }
});

// 监听知识库启用状态变化：
//   - 从"无"变"有"：自动补齐 RAG 基础工具，方便用户开箱即用（仅 seed 行为）；
//   - 从"有"变"无"：**不再**自动擦工具，依赖不满足时由 `availableTools` 灰显
//     + 运行时工具注册器过滤。`allowed_tools` 代表用户意图，只应在用户显式操作
//     （切 agent_type / 切 agent_mode / 手勾工具）时变更。
// 历史背景：旧版本在 KB 能力消失时会擦除 KB/Wiki 工具，导致用户切换
// `kb_selection_mode` 到 "selected"、但尚未勾具体 KB 的过渡期里静默丢失工具，
// 对默认工具全是 wiki_* 的内置"维基问答"智能体尤为致命。
watch(hasKnowledgeBase, (hasKB, oldHasKB) => {
  // 如果当前在检索策略页面但没有知识库能力了，切换到基础设置
  if (!hasKB && currentSection.value === 'retrieval') {
    currentSection.value = 'basic';
  }

  // 初始化期间或非 Agent 模式下不自动调整工具
  if (isInitializing.value || !isAgentMode.value) return;

  if (hasKB && !oldHasKB) {
    // 从无知识库变为有知识库，seed 默认的 RAG 工具（仅补齐未勾的）
    const currentTools = formData.value.config.allowed_tools || [];
    const toolsToAdd = knowledgeBaseTools.filter((tool: string) => !currentTools.includes(tool));
    formData.value.config.allowed_tools = [...currentTools, ...toolsToAdd];
  }
});

// 监听运行模式变化，自动切换页面
watch(isAgentMode, (isAgent) => {
  // 如果当前在高级设置页面但切换到了Agent模式，切换到基础设置
  if (isAgent && currentSection.value === 'advanced') {
    currentSection.value = 'basic';
  }
  // 如果当前在多轮对话页面但切换到了Agent模式，切换到基础设置（Agent模式下多轮对话由内部控制）
  if (isAgent && currentSection.value === 'conversation') {
    currentSection.value = 'basic';
  }
});

// 监听设置弹窗关闭，刷新模型列表
watch(() => uiStore.showSettingsModal, async (visible, prevVisible) => {
  // 从设置页面返回时（弹窗关闭），刷新模型列表
  if (prevVisible && !visible && props.visible) {
    try {
      const [models, statusRes] = await Promise.all([
        listModels(),
        getStorageEngineStatus(),
      ]);
      if (models && models.length > 0) {
        allModels.value = models;
      }
      if (statusRes?.data?.engines) {
        storageEngineStatus.value = statusRes.data.engines;
      }
    } catch (e) {
      console.warn('Failed to refresh data after settings closed', e);
    }
  }
});

// 加载依赖数据
const loadDependencies = async () => {
  try {
    // 加载所有模型列表（ModelSelector 组件会自动按类型过滤）
    const models = await listModels();
    if (models && models.length > 0) {
      allModels.value = models;
    }

    // 加载知识库列表（我的 + 共享的）
    const kbRes: any = await listKnowledgeBases();
    const myKbs: typeof kbOptions.value = [];
    if (kbRes.data) {
      kbRes.data.forEach((kb: any) => {
        const strategy = kb.indexing_strategy;
        const caps: KBCapabilities | undefined = kb.capabilities;
        myKbs.push({
          label: kb.name,
          value: kb.id,
          type: kb.type || 'document',
          count: kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0),
          shared: false,
          ragEnabled: caps ? (caps.vector || caps.keyword) : (!strategy || strategy.vector_enabled || strategy.keyword_enabled),
          wikiEnabled: caps ? caps.wiki : (strategy?.wiki_enabled || false),
          capabilities: caps,
        });
      });
    }

    // 加载共享给我的知识库
    const sharedKbs: typeof kbOptions.value = [];
    try {
      const sharedList = await orgStore.fetchSharedKnowledgeBases();
      if (sharedList && sharedList.length > 0) {
        const myKbIds = new Set(myKbs.map(kb => kb.value));
        sharedList.forEach((shared: any) => {
          const kb = shared.knowledge_base;
          if (!kb || myKbIds.has(kb.id)) return;
          const caps: KBCapabilities | undefined = kb.capabilities;
          sharedKbs.push({
            label: kb.name,
            value: kb.id,
            type: kb.type || 'document',
            count: kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0),
            shared: true,
            orgName: shared.org_name,
            ragEnabled: caps ? (caps.vector || caps.keyword) : (!kb.indexing_strategy || kb.indexing_strategy.vector_enabled || kb.indexing_strategy.keyword_enabled),
            wikiEnabled: caps ? caps.wiki : (kb.indexing_strategy?.wiki_enabled || false),
            capabilities: caps,
          });
        });
      }
    } catch (e) {
      console.warn('Failed to load shared knowledge bases', e);
    }

    kbOptions.value = [...myKbs, ...sharedKbs];

    // 加载 MCP 服务列表（只加载启用的）
    try {
      const mcpList = await listMCPServices();
      if (mcpList && mcpList.length > 0) {
        mcpOptions.value = mcpList
          .filter((mcp: MCPService) => mcp.enabled)
          .map((mcp: MCPService) => ({ label: mcp.name, value: mcp.id }));
      }
    } catch (e) {
      console.warn('Failed to load MCP services', e);
    }

    // 加载预装 Skills 列表及沙箱可用性（skills_available=false 时前端不展示 Skills 配置）
    try {
      const skillsRes = await listSkills();
      skillsAvailable.value = skillsRes.skills_available !== false;
      if (skillsRes.data && skillsRes.data.length > 0) {
        skillOptions.value = skillsRes.data;
      }
    } catch (e) {
      console.warn('Failed to load skills', e);
      skillsAvailable.value = false;
    }

    // 加载智能体类型预设（smart-reasoning 模式下的"类型"下拉）
    try {
      const presetsRes: any = await getAgentTypePresets();
      if (presetsRes?.data && Array.isArray(presetsRes.data)) {
        agentTypePresets.value = presetsRes.data as AgentTypePreset[];
      }
    } catch (e) {
      console.warn('Failed to load agent type presets', e);
    }

    // 加载 Agent 系统提示词模板（供 applyAgentTypePreset 根据 system_prompt_id 回填正文）
    try {
      const tmplRes = await getPromptTemplates();
      const cfg = tmplRes?.data;
      if (cfg?.agent_system_prompt && Array.isArray(cfg.agent_system_prompt)) {
        agentSystemPromptTemplates.value = cfg.agent_system_prompt;
      }
    } catch (e) {
      console.warn('Failed to load prompt templates for agent type presets', e);
    }

    // 加载存储引擎可用状态（用于图片存储 provider 选择）
    try {
      const statusRes = await getStorageEngineStatus();
      if (statusRes?.data?.engines) {
        storageEngineStatus.value = statusRes.data.engines;
      }
    } catch (e) {
      console.warn('Failed to load storage engine status', e);
    }

    // 加载网络搜索引擎配置列表
    try {
      const wsRes: any = await listWebSearchProviders();
      if (wsRes?.data && Array.isArray(wsRes.data)) {
        webSearchProviderList.value = wsRes.data;
      }
    } catch (e) {
      console.warn('Failed to load web search providers', e);
    }

    // 加载占位符定义（从统一 API）
    try {
      const placeholdersRes = await getPlaceholders();
      if (placeholdersRes.data) {
        placeholderData.value = placeholdersRes.data;
      }
    } catch (e) {
      console.warn('Failed to load placeholders', e);
    }

    // 加载 Agent 模式默认提示词（来自 agent-config，用于 smart-reasoning 模式）
    const agentConfig = await getAgentConfig();
    if (agentConfig.data?.system_prompt) {
      defaultAgentSystemPrompt.value = agentConfig.data.system_prompt;
    }

    // 加载系统默认配置（来自 conversation-config，用于普通模式 quick-answer）
    const conversationConfig = await getConversationConfig();
    if (conversationConfig.data?.prompt) {
      defaultNormalSystemPrompt.value = conversationConfig.data.prompt;
    }
    if (conversationConfig.data?.context_template) {
      defaultContextTemplate.value = conversationConfig.data.context_template;
    }
    if (conversationConfig.data?.rewrite_prompt_system) {
      defaultRewritePromptSystem.value = conversationConfig.data.rewrite_prompt_system;
    }
    if (conversationConfig.data?.rewrite_prompt_user) {
      defaultRewritePromptUser.value = conversationConfig.data.rewrite_prompt_user;
    }
    if (conversationConfig.data?.fallback_prompt) {
      defaultFallbackPrompt.value = conversationConfig.data.fallback_prompt;
    }
    if (conversationConfig.data?.fallback_response) {
      defaultFallbackResponse.value = conversationConfig.data.fallback_response;
    }
    // 加载默认检索参数
    if (conversationConfig.data?.embedding_top_k) {
      defaultEmbeddingTopK.value = conversationConfig.data.embedding_top_k;
    }
    if (conversationConfig.data?.keyword_threshold !== undefined) {
      defaultKeywordThreshold.value = conversationConfig.data.keyword_threshold;
    }
    if (conversationConfig.data?.vector_threshold !== undefined) {
      defaultVectorThreshold.value = conversationConfig.data.vector_threshold;
    }
    if (conversationConfig.data?.rerank_top_k) {
      defaultRerankTopK.value = conversationConfig.data.rerank_top_k;
    }
    if (conversationConfig.data?.rerank_threshold !== undefined) {
      defaultRerankThreshold.value = conversationConfig.data.rerank_threshold;
    }
    if (conversationConfig.data?.max_completion_tokens) {
      defaultMaxCompletionTokens.value = conversationConfig.data.max_completion_tokens;
    }
    if (conversationConfig.data?.temperature !== undefined) {
      defaultTemperature.value = conversationConfig.data.temperature;
    }
  } catch (e) {
    console.error('Failed to load dependencies', e);
  }
};

// 跳转到模型管理页面添加模型
const handleAddModel = (subSection: string) => {
  uiStore.openSettings('models', subSection);
};

const handleClose = () => {
  showPlaceholderPopup.value = false;
  showContextPlaceholderPopup.value = false;
  rewriteSystemPopup.value.show = false;
  rewriteUserPopup.value.show = false;
  fallbackPromptPopup.value.show = false;
  emit('update:visible', false);
};

// 过滤后的占位符列表
const filteredPlaceholders = computed(() => {
  if (!placeholderPrefix.value) {
    return availablePlaceholders.value;
  }
  const prefix = placeholderPrefix.value.toLowerCase();
  return availablePlaceholders.value.filter(p => 
    p.name.toLowerCase().startsWith(prefix)
  );
});

// 过滤后的上下文模板占位符列表
const filteredContextPlaceholders = computed(() => {
  if (!contextPlaceholderPrefix.value) {
    return contextTemplatePlaceholders.value;
  }
  const prefix = contextPlaceholderPrefix.value.toLowerCase();
  return contextTemplatePlaceholders.value.filter(p => 
    p.name.toLowerCase().startsWith(prefix)
  );
});

// 过滤后的改写系统提示词占位符列表
const filteredRewriteSystemPlaceholders = computed(() => {
  if (!rewriteSystemPopup.value.prefix) {
    return rewriteSystemPlaceholders.value;
  }
  const prefix = rewriteSystemPopup.value.prefix.toLowerCase();
  return rewriteSystemPlaceholders.value.filter(p => 
    p.name.toLowerCase().startsWith(prefix)
  );
});

// 过滤后的改写用户提示词占位符列表
const filteredRewriteUserPlaceholders = computed(() => {
  if (!rewriteUserPopup.value.prefix) {
    return rewritePlaceholders.value;
  }
  const prefix = rewriteUserPopup.value.prefix.toLowerCase();
  return rewritePlaceholders.value.filter(p => 
    p.name.toLowerCase().startsWith(prefix)
  );
});

// 过滤后的兜底提示词占位符列表
const filteredFallbackPlaceholders = computed(() => {
  if (!fallbackPromptPopup.value.prefix) {
    return fallbackPlaceholders.value;
  }
  const prefix = fallbackPromptPopup.value.prefix.toLowerCase();
  return fallbackPlaceholders.value.filter(p => 
    p.name.toLowerCase().startsWith(prefix)
  );
});

// 获取 textarea 元素
const getTextareaElement = (): HTMLTextAreaElement | null => {
  if (promptTextareaRef.value) {
    if (promptTextareaRef.value.$el) {
      return promptTextareaRef.value.$el.querySelector('textarea');
    }
    if (promptTextareaRef.value instanceof HTMLTextAreaElement) {
      return promptTextareaRef.value;
    }
  }
  return null;
};

// 计算光标位置
const calculateCursorPosition = (textarea: HTMLTextAreaElement) => {
  const cursorPos = textarea.selectionStart;
  const textBeforeCursor = formData.value.config.system_prompt.substring(0, cursorPos);
  
  const style = window.getComputedStyle(textarea);
  const textareaRect = textarea.getBoundingClientRect();
  
  const lineHeight = parseFloat(style.lineHeight) || 20;
  const paddingTop = parseFloat(style.paddingTop) || 0;
  const paddingLeft = parseFloat(style.paddingLeft) || 0;
  
  // 计算当前行号
  const lines = textBeforeCursor.split('\n');
  const currentLine = lines.length - 1;
  const currentLineText = lines[currentLine];
  
  // 创建临时 span 计算文本宽度
  const span = document.createElement('span');
  span.style.font = style.font;
  span.style.visibility = 'hidden';
  span.style.position = 'absolute';
  span.style.whiteSpace = 'pre';
  span.textContent = currentLineText;
  document.body.appendChild(span);
  const textWidth = span.offsetWidth;
  document.body.removeChild(span);
  
  const scrollTop = textarea.scrollTop;
  const top = textareaRect.top + paddingTop + (currentLine * lineHeight) - scrollTop + lineHeight + 4;
  const scrollLeft = textarea.scrollLeft;
  const left = textareaRect.left + paddingLeft + textWidth - scrollLeft;
  
  return { top, left };
};

// 检查并显示占位符提示
const checkAndShowPlaceholderPopup = () => {
  const textarea = getTextareaElement();
  if (!textarea) return;
  
  const cursorPos = textarea.selectionStart;
  const textBeforeCursor = formData.value.config.system_prompt.substring(0, cursorPos);
  
  // 查找最近的 {{ 位置
  let lastOpenPos = -1;
  for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
    if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
      const textAfterOpen = textBeforeCursor.substring(i + 1);
      if (!textAfterOpen.includes('}}')) {
        lastOpenPos = i - 1;
        break;
      }
    }
  }
  
  if (lastOpenPos === -1) {
    showPlaceholderPopup.value = false;
    placeholderPrefix.value = '';
    return;
  }
  
  const textAfterOpen = textBeforeCursor.substring(lastOpenPos + 2);
  placeholderPrefix.value = textAfterOpen;
  
  const filtered = filteredPlaceholders.value;
  if (filtered.length > 0) {
    nextTick(() => {
      const position = calculateCursorPosition(textarea);
      popupStyle.value = {
        top: `${position.top}px`,
        left: `${position.left}px`
      };
      showPlaceholderPopup.value = true;
      selectedPlaceholderIndex.value = 0;
    });
  } else {
    showPlaceholderPopup.value = false;
  }
};

// 处理输入
const handlePromptInput = () => {
  if (placeholderPopupTimer) {
    clearTimeout(placeholderPopupTimer);
  }
  placeholderPopupTimer = setTimeout(() => {
    checkAndShowPlaceholderPopup();
  }, 50);
};

// 插入占位符
const insertPlaceholder = (placeholderName: string, fromPopup: boolean = false) => {
  const textarea = getTextareaElement();
  if (!textarea) return;
  
  showPlaceholderPopup.value = false;
  placeholderPrefix.value = '';
  selectedPlaceholderIndex.value = 0;
  
  nextTick(() => {
    const cursorPos = textarea.selectionStart;
    const currentValue = formData.value.config.system_prompt || '';
    const textBeforeCursor = currentValue.substring(0, cursorPos);
    const textAfterCursor = currentValue.substring(cursorPos);
    
    // 只有从下拉列表选择时才查找 {{ 并替换
    if (fromPopup) {
      let lastOpenPos = -1;
      for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
        if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
          lastOpenPos = i - 1;
          break;
        }
      }
      
      if (lastOpenPos !== -1) {
        const textBeforeOpen = currentValue.substring(0, lastOpenPos);
        const newValue = textBeforeOpen + `{{${placeholderName}}}` + textAfterCursor;
        formData.value.config.system_prompt = newValue;
        
        nextTick(() => {
          const newCursorPos = textBeforeOpen.length + placeholderName.length + 4;
          textarea.setSelectionRange(newCursorPos, newCursorPos);
          textarea.focus();
        });
        return;
      }
    }
    
    // 直接在光标位置插入完整占位符
    const newValue = textBeforeCursor + `{{${placeholderName}}}` + textAfterCursor;
    formData.value.config.system_prompt = newValue;
    
    nextTick(() => {
      const newCursorPos = cursorPos + placeholderName.length + 4;
      textarea.setSelectionRange(newCursorPos, newCursorPos);
      textarea.focus();
    });
  });
};

// 获取上下文模板 textarea 元素
const getContextTemplateTextareaElement = (): HTMLTextAreaElement | null => {
  if (contextTemplateTextareaRef.value) {
    if (contextTemplateTextareaRef.value.$el) {
      return contextTemplateTextareaRef.value.$el.querySelector('textarea');
    }
    if (contextTemplateTextareaRef.value instanceof HTMLTextAreaElement) {
      return contextTemplateTextareaRef.value;
    }
  }
  return null;
};

// 计算上下文模板光标位置
const calculateContextCursorPosition = (textarea: HTMLTextAreaElement) => {
  const cursorPos = textarea.selectionStart;
  const textBeforeCursor = formData.value.config.context_template.substring(0, cursorPos);
  
  const style = window.getComputedStyle(textarea);
  const textareaRect = textarea.getBoundingClientRect();
  
  const lineHeight = parseFloat(style.lineHeight) || 20;
  const paddingTop = parseFloat(style.paddingTop) || 0;
  const paddingLeft = parseFloat(style.paddingLeft) || 0;
  
  const lines = textBeforeCursor.split('\n');
  const currentLine = lines.length - 1;
  const currentLineText = lines[currentLine];
  
  const span = document.createElement('span');
  span.style.font = style.font;
  span.style.visibility = 'hidden';
  span.style.position = 'absolute';
  span.style.whiteSpace = 'pre';
  span.textContent = currentLineText;
  document.body.appendChild(span);
  const textWidth = span.offsetWidth;
  document.body.removeChild(span);
  
  const scrollTop = textarea.scrollTop;
  const top = textareaRect.top + paddingTop + (currentLine * lineHeight) - scrollTop + lineHeight + 4;
  const scrollLeft = textarea.scrollLeft;
  const left = textareaRect.left + paddingLeft + textWidth - scrollLeft;
  
  return { top, left };
};

// 检查并显示上下文模板占位符提示
const checkAndShowContextPlaceholderPopup = () => {
  const textarea = getContextTemplateTextareaElement();
  if (!textarea) return;
  
  const cursorPos = textarea.selectionStart;
  const textBeforeCursor = formData.value.config.context_template.substring(0, cursorPos);
  
  let lastOpenPos = -1;
  for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
    if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
      const textAfterOpen = textBeforeCursor.substring(i + 1);
      if (!textAfterOpen.includes('}}')) {
        lastOpenPos = i - 1;
        break;
      }
    }
  }
  
  if (lastOpenPos === -1) {
    showContextPlaceholderPopup.value = false;
    contextPlaceholderPrefix.value = '';
    return;
  }
  
  const textAfterOpen = textBeforeCursor.substring(lastOpenPos + 2);
  contextPlaceholderPrefix.value = textAfterOpen;
  
  const filtered = filteredContextPlaceholders.value;
  if (filtered.length > 0) {
    nextTick(() => {
      const position = calculateContextCursorPosition(textarea);
      contextPopupStyle.value = {
        top: `${position.top}px`,
        left: `${position.left}px`
      };
      showContextPlaceholderPopup.value = true;
      selectedContextPlaceholderIndex.value = 0;
    });
  } else {
    showContextPlaceholderPopup.value = false;
  }
};

// 处理上下文模板输入
const handleContextTemplateInput = () => {
  if (contextPlaceholderPopupTimer) {
    clearTimeout(contextPlaceholderPopupTimer);
  }
  contextPlaceholderPopupTimer = setTimeout(() => {
    checkAndShowContextPlaceholderPopup();
  }, 50);
};

// 插入上下文模板占位符
const insertContextPlaceholder = (placeholderName: string, fromPopup: boolean = false) => {
  const textarea = getContextTemplateTextareaElement();
  if (!textarea) return;
  
  showContextPlaceholderPopup.value = false;
  contextPlaceholderPrefix.value = '';
  selectedContextPlaceholderIndex.value = 0;
  
  nextTick(() => {
    const cursorPos = textarea.selectionStart;
    const currentValue = formData.value.config.context_template || '';
    const textBeforeCursor = currentValue.substring(0, cursorPos);
    const textAfterCursor = currentValue.substring(cursorPos);
    
    // 只有从下拉列表选择时才查找 {{ 并替换
    if (fromPopup) {
      let lastOpenPos = -1;
      for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
        if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
          lastOpenPos = i - 1;
          break;
        }
      }
      
      if (lastOpenPos !== -1) {
        const textBeforeOpen = currentValue.substring(0, lastOpenPos);
        const newValue = textBeforeOpen + `{{${placeholderName}}}` + textAfterCursor;
        formData.value.config.context_template = newValue;
        
        nextTick(() => {
          const newCursorPos = textBeforeOpen.length + placeholderName.length + 4;
          textarea.setSelectionRange(newCursorPos, newCursorPos);
          textarea.focus();
        });
        return;
      }
    }
    
    // 直接在光标位置插入完整占位符
    const newValue = textBeforeCursor + `{{${placeholderName}}}` + textAfterCursor;
    formData.value.config.context_template = newValue;
    
    nextTick(() => {
      const newCursorPos = cursorPos + placeholderName.length + 4;
      textarea.setSelectionRange(newCursorPos, newCursorPos);
      textarea.focus();
    });
  });
};

// 通用获取 textarea 元素
const getGenericTextareaElement = (type: 'rewriteSystem' | 'rewriteUser' | 'fallback'): HTMLTextAreaElement | null => {
  const refMap = {
    rewriteSystem: rewriteSystemTextareaRef,
    rewriteUser: rewriteUserTextareaRef,
    fallback: fallbackPromptTextareaRef,
  };
  const ref = refMap[type];
  if (ref.value) {
    if (ref.value.$el) {
      return ref.value.$el.querySelector('textarea');
    }
    if (ref.value instanceof HTMLTextAreaElement) {
      return ref.value;
    }
  }
  return null;
};

// 通用计算光标位置
const calculateGenericCursorPosition = (textarea: HTMLTextAreaElement, fieldValue: string) => {
  const cursorPos = textarea.selectionStart;
  const textBeforeCursor = fieldValue.substring(0, cursorPos);
  const lines = textBeforeCursor.split('\n');
  const currentLine = lines.length - 1;
  const currentLineText = lines[currentLine];
  
  const textareaRect = textarea.getBoundingClientRect();
  const style = window.getComputedStyle(textarea);
  const lineHeight = parseFloat(style.lineHeight) || 20;
  const paddingTop = parseFloat(style.paddingTop) || 0;
  const paddingLeft = parseFloat(style.paddingLeft) || 0;
  
  const span = document.createElement('span');
  span.style.font = style.font;
  span.style.visibility = 'hidden';
  span.style.position = 'absolute';
  span.style.whiteSpace = 'pre';
  span.textContent = currentLineText;
  document.body.appendChild(span);
  const textWidth = span.offsetWidth;
  document.body.removeChild(span);
  
  const scrollTop = textarea.scrollTop;
  const top = textareaRect.top + paddingTop + (currentLine * lineHeight) - scrollTop + lineHeight + 4;
  const scrollLeft = textarea.scrollLeft;
  const left = textareaRect.left + paddingLeft + textWidth - scrollLeft;
  
  return { top, left };
};

// 通用检查并显示占位符弹出
const checkAndShowGenericPlaceholderPopup = (
  type: 'rewriteSystem' | 'rewriteUser' | 'fallback',
  popup: typeof rewriteSystemPopup,
  fieldKey: keyof typeof formData.value.config,
  filteredPlaceholders: PlaceholderDefinition[]
) => {
  const textarea = getGenericTextareaElement(type);
  if (!textarea) return;
  
  const cursorPos = textarea.selectionStart;
  const fieldValue = String(formData.value.config[fieldKey] || '');
  const textBeforeCursor = fieldValue.substring(0, cursorPos);
  
  let lastOpenPos = -1;
  for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
    if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
      const textAfterOpen = textBeforeCursor.substring(i + 1);
      if (!textAfterOpen.includes('}}')) {
        lastOpenPos = i - 1;
        break;
      }
    }
  }
  
  if (lastOpenPos === -1) {
    popup.value.show = false;
    popup.value.prefix = '';
    return;
  }
  
  const textAfterOpen = textBeforeCursor.substring(lastOpenPos + 2);
  popup.value.prefix = textAfterOpen;
  
  if (filteredPlaceholders.length > 0) {
    nextTick(() => {
      const position = calculateGenericCursorPosition(textarea, fieldValue);
      popup.value.style = {
        top: `${position.top}px`,
        left: `${position.left}px`
      };
      popup.value.show = true;
      popup.value.selectedIndex = 0;
    });
  } else {
    popup.value.show = false;
  }
};

// 处理改写系统提示词输入
const handleRewriteSystemInput = () => {
  if (rewriteSystemPopup.value.timer) {
    clearTimeout(rewriteSystemPopup.value.timer);
  }
  rewriteSystemPopup.value.timer = setTimeout(() => {
    checkAndShowGenericPlaceholderPopup('rewriteSystem', rewriteSystemPopup, 'rewrite_prompt_system', filteredRewriteSystemPlaceholders.value);
  }, 50);
};

// 处理改写用户提示词输入
const handleRewriteUserInput = () => {
  if (rewriteUserPopup.value.timer) {
    clearTimeout(rewriteUserPopup.value.timer);
  }
  rewriteUserPopup.value.timer = setTimeout(() => {
    checkAndShowGenericPlaceholderPopup('rewriteUser', rewriteUserPopup, 'rewrite_prompt_user', filteredRewriteUserPlaceholders.value);
  }, 50);
};

// 处理兜底提示词输入
const handleFallbackPromptInput = () => {
  if (fallbackPromptPopup.value.timer) {
    clearTimeout(fallbackPromptPopup.value.timer);
  }
  fallbackPromptPopup.value.timer = setTimeout(() => {
    checkAndShowGenericPlaceholderPopup('fallback', fallbackPromptPopup, 'fallback_prompt', filteredFallbackPlaceholders.value);
  }, 50);
};

// 通用插入占位符
const insertGenericPlaceholder = (type: 'rewriteSystem' | 'rewriteUser' | 'fallback', placeholderName: string, fromPopup: boolean = false) => {
  const textarea = getGenericTextareaElement(type);
  if (!textarea) return;
  
  const popupMap = {
    rewriteSystem: rewriteSystemPopup,
    rewriteUser: rewriteUserPopup,
    fallback: fallbackPromptPopup,
  };
  const fieldKeyMap: Record<string, keyof typeof formData.value.config> = {
    rewriteSystem: 'rewrite_prompt_system',
    rewriteUser: 'rewrite_prompt_user',
    fallback: 'fallback_prompt',
  };
  
  const popup = popupMap[type];
  const fieldKey = fieldKeyMap[type];
  
  popup.value.show = false;
  popup.value.prefix = '';
  popup.value.selectedIndex = 0;
  
  nextTick(() => {
    const cursorPos = textarea.selectionStart;
    const currentValue = String(formData.value.config[fieldKey] || '');
    const textBeforeCursor = currentValue.substring(0, cursorPos);
    const textAfterCursor = currentValue.substring(cursorPos);
    
    // 只有从下拉列表选择时才查找 {{ 并替换
    if (fromPopup) {
      let lastOpenPos = -1;
      for (let i = textBeforeCursor.length - 1; i >= 1; i--) {
        if (textBeforeCursor[i] === '{' && textBeforeCursor[i - 1] === '{') {
          lastOpenPos = i - 1;
          break;
        }
      }
      
      if (lastOpenPos !== -1) {
        const textBeforeOpen = currentValue.substring(0, lastOpenPos);
        const newValue = textBeforeOpen + `{{${placeholderName}}}` + textAfterCursor;
        (formData.value.config as any)[fieldKey] = newValue;
        
        nextTick(() => {
          const newCursorPos = textBeforeOpen.length + placeholderName.length + 4;
          textarea.setSelectionRange(newCursorPos, newCursorPos);
          textarea.focus();
        });
        return;
      }
    }
    
    // 直接在光标位置插入完整占位符
    const newValue = textBeforeCursor + `{{${placeholderName}}}` + textAfterCursor;
    (formData.value.config as any)[fieldKey] = newValue;
    
    nextTick(() => {
      const newCursorPos = cursorPos + placeholderName.length + 4;
      textarea.setSelectionRange(newCursorPos, newCursorPos);
      textarea.focus();
    });
  });
};

// 设置上下文模板 textarea 事件监听
const setupContextTemplateEventListeners = () => {
  nextTick(() => {
    const textarea = getContextTemplateTextareaElement();
    if (textarea) {
      textarea.addEventListener('keydown', (e: KeyboardEvent) => {
        if (showContextPlaceholderPopup.value && filteredContextPlaceholders.value.length > 0) {
          if (e.key === 'ArrowDown') {
            e.preventDefault();
            e.stopPropagation();
            if (selectedContextPlaceholderIndex.value < filteredContextPlaceholders.value.length - 1) {
              selectedContextPlaceholderIndex.value++;
            } else {
              selectedContextPlaceholderIndex.value = 0;
            }
          } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            e.stopPropagation();
            if (selectedContextPlaceholderIndex.value > 0) {
              selectedContextPlaceholderIndex.value--;
            } else {
              selectedContextPlaceholderIndex.value = filteredContextPlaceholders.value.length - 1;
            }
          } else if (e.key === 'Enter' || e.key === 'Tab') {
            e.preventDefault();
            e.stopPropagation();
            const selected = filteredContextPlaceholders.value[selectedContextPlaceholderIndex.value];
            if (selected) {
              insertContextPlaceholder(selected.name, true);
            }
          } else if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            showContextPlaceholderPopup.value = false;
            contextPlaceholderPrefix.value = '';
          }
        }
      }, true);
    }
  });
};

// 设置 textarea 事件监听
const setupTextareaEventListeners = () => {
  nextTick(() => {
    const textarea = getTextareaElement();
    if (textarea) {
      textarea.addEventListener('keydown', (e: KeyboardEvent) => {
        if (showPlaceholderPopup.value && filteredPlaceholders.value.length > 0) {
          if (e.key === 'ArrowDown') {
            e.preventDefault();
            e.stopPropagation();
            if (selectedPlaceholderIndex.value < filteredPlaceholders.value.length - 1) {
              selectedPlaceholderIndex.value++;
            } else {
              selectedPlaceholderIndex.value = 0;
            }
          } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            e.stopPropagation();
            if (selectedPlaceholderIndex.value > 0) {
              selectedPlaceholderIndex.value--;
            } else {
              selectedPlaceholderIndex.value = filteredPlaceholders.value.length - 1;
            }
          } else if (e.key === 'Enter' || e.key === 'Tab') {
            e.preventDefault();
            e.stopPropagation();
            const selected = filteredPlaceholders.value[selectedPlaceholderIndex.value];
            if (selected) {
              insertPlaceholder(selected.name, true);
            }
          } else if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            showPlaceholderPopup.value = false;
            placeholderPrefix.value = '';
          }
        }
      }, true);
    }
  });
};

// 通用设置 textarea 事件监听
const setupGenericTextareaEventListeners = (
  type: 'rewriteSystem' | 'rewriteUser' | 'fallback',
  popup: typeof rewriteSystemPopup,
  filteredPlaceholders: () => PlaceholderDefinition[]
) => {
  nextTick(() => {
    const textarea = getGenericTextareaElement(type);
    if (textarea) {
      textarea.addEventListener('keydown', (e: KeyboardEvent) => {
        const filtered = filteredPlaceholders();
        if (popup.value.show && filtered.length > 0) {
          if (e.key === 'ArrowDown') {
            e.preventDefault();
            e.stopPropagation();
            if (popup.value.selectedIndex < filtered.length - 1) {
              popup.value.selectedIndex++;
            } else {
              popup.value.selectedIndex = 0;
            }
          } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            e.stopPropagation();
            if (popup.value.selectedIndex > 0) {
              popup.value.selectedIndex--;
            } else {
              popup.value.selectedIndex = filtered.length - 1;
            }
          } else if (e.key === 'Enter' || e.key === 'Tab') {
            e.preventDefault();
            e.stopPropagation();
            const selected = filtered[popup.value.selectedIndex];
            if (selected) {
              insertGenericPlaceholder(type, selected.name, true);
            }
          } else if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            popup.value.show = false;
            popup.value.prefix = '';
          }
        }
      }, true);
    }
  });
};

// 处理点击占位符标签
const handlePlaceholderClick = (type: 'system' | 'context' | 'rewriteSystem' | 'rewriteUser' | 'fallback', placeholderName: string) => {
  if (type === 'system') {
    insertPlaceholder(placeholderName);
  } else if (type === 'context') {
    insertContextPlaceholder(placeholderName);
  } else {
    insertGenericPlaceholder(type, placeholderName);
  }
};

// 监听 visible 变化设置事件监听
watch(() => props.visible, (val) => {
  if (val) {
    nextTick(() => {
      setupTextareaEventListeners();
      setupContextTemplateEventListeners();
      setupGenericTextareaEventListeners('rewriteSystem', rewriteSystemPopup, () => filteredRewriteSystemPlaceholders.value);
      setupGenericTextareaEventListeners('rewriteUser', rewriteUserPopup, () => filteredRewriteUserPlaceholders.value);
      setupGenericTextareaEventListeners('fallback', fallbackPromptPopup, () => filteredFallbackPlaceholders.value);
    });
  }
});

// 模板选择处理函数
const handleSystemPromptTemplateSelect = (template: PromptTemplate) => {
  formData.value.config.system_prompt = template.content;
};

// Agent 系统提示词的"恢复默认"：
// 当前选中了非 custom 的智能体类型时，"默认"应当是该类型预设绑定的提示词
// （比如 Wiki 问答 → wiki_researcher），而不是 agent_system_prompt 模板表里
// 全局 default: true 的那一条。只有当类型为 custom 或找不到预设绑定的模板时，
// 才回退到 PromptTemplateSelector 传来的全局默认模板。
const handleAgentSystemPromptResetDefault = (fallback: PromptTemplate) => {
  const typeId = agentType.value;
  if (typeId && typeId !== 'custom') {
    const preset = agentTypePresets.value.find(p => p.id === typeId);
    const presetPromptId = preset?.config?.system_prompt_id;
    if (presetPromptId) {
      const tmpl = agentSystemPromptTemplates.value.find(t => t.id === presetPromptId);
      if (tmpl && typeof tmpl.content === 'string') {
        formData.value.config.system_prompt = tmpl.content;
        formData.value.config.system_prompt_id = tmpl.id;
        return;
      }
    }
  }
  // Fallback：没有合适的类型预设，用 PromptTemplateSelector 找到的全局默认
  formData.value.config.system_prompt = fallback.content;
  formData.value.config.system_prompt_id = fallback.id;
};

const handleContextTemplateSelect = (template: PromptTemplate) => {
  formData.value.config.context_template = template.content;
};

const handleRewriteTemplateSelect = (template: PromptTemplate) => {
  // Rewrite templates contain both content (system) and user fields
  formData.value.config.rewrite_prompt_system = template.content;
  if (template.user) {
    formData.value.config.rewrite_prompt_user = template.user;
  }
};

const handleFallbackResponseTemplateSelect = (template: PromptTemplate) => {
  formData.value.config.fallback_response = template.content;
};

const handleFallbackPromptTemplateSelect = (template: PromptTemplate) => {
  formData.value.config.fallback_prompt = template.content;
};

// 辅助函数：检查提示词是否包含指定占位符
const hasPlaceholder = (text: string | undefined, placeholder: string): boolean => {
  if (!text) return false;
  return text.includes(`{{${placeholder}}}`);
};

const handleSave = async () => {
  // 验证必填项（内置智能体不验证名称和系统提示词）
  if (!isBuiltinAgent.value) {
    if (!formData.value.name || !formData.value.name.trim()) {
      MessagePlugin.error(t('agent.editor.nameRequired'));
      currentSection.value = 'basic';
      return;
    }

    // 自定义智能体必须填写系统提示词
    if (!formData.value.config.system_prompt || !formData.value.config.system_prompt.trim()) {
      MessagePlugin.error(t('agent.editor.systemPromptRequired'));
      currentSection.value = 'basic';
      return;
    }

    // 自定义智能体普通模式必须填写上下文模板
    if (!isAgentMode.value && (!formData.value.config.context_template || !formData.value.config.context_template.trim())) {
      MessagePlugin.error(t('agent.editor.contextTemplateRequired'));
      currentSection.value = 'basic';
      return;
    }
  }





  // 校验占位符（普通模式 + 开启多轮对话改写）
  if (!isAgentMode.value && formData.value.config.multi_turn_enabled && formData.value.config.enable_rewrite) {
    const rewritePrompt = formData.value.config.rewrite_prompt_user || '';
    // 只有用户自定义了改写提示词时才校验
    if (rewritePrompt.trim()) {
      if (!hasPlaceholder(rewritePrompt, 'query')) {
        MessagePlugin.error(t('agent.editor.queryMissingInRewrite'));
        currentSection.value = 'conversation';
        return;
      }
    }
  }

  // 校验占位符（兜底策略为模型生成时）
  if (!isAgentMode.value && formData.value.config.fallback_strategy === 'model') {
    const fallbackPrompt = formData.value.config.fallback_prompt || '';
    // 只有用户自定义了兜底提示词时才校验
    if (fallbackPrompt.trim() && !hasPlaceholder(fallbackPrompt, 'query')) {
      MessagePlugin.error(t('agent.editor.queryMissingInFallback'));
      currentSection.value = 'retrieval';
      return;
    }
  }

  if (!formData.value.config.model_id) {
    MessagePlugin.error(t('agent.editor.modelRequired'));
    currentSection.value = 'model';
    return;
  }

  // 校验 VLM 模型（当图片上传启用时必填）
  if (formData.value.config.image_upload_enabled && !formData.value.config.vlm_model_id) {
    MessagePlugin.error(t('agentEditor.imageUpload.vlmModelRequired'));
    currentSection.value = 'multimodal';
    return;
  }

  // 校验 ReRank 模型（当需要时必填）
  if (needsRerankModel.value && !formData.value.config.rerank_model_id) {
    MessagePlugin.error(t('agent.editor.rerankModelRequired'));
    currentSection.value = 'knowledge';
    return;
  }

  // 过滤空推荐问题
  if (formData.value.config.suggested_prompts) {
    formData.value.config.suggested_prompts = formData.value.config.suggested_prompts.filter((p: string) => p.trim() !== '');
  }

  saving.value = true;
  try {
    if (props.mode === 'create') {
      await createAgent(formData.value);
      MessagePlugin.success(t('agent.messages.created'));
    } else {
      await updateAgent(formData.value.id, formData.value);
      MessagePlugin.success(t('agent.messages.updated'));
    }
    emit('success');
    handleClose();
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('agent.messages.saveFailed'));
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped lang="less">
// 复用创建知识库的样式
.settings-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.settings-modal {
  position: relative;
  width: 90vw;
  max-width: 1100px;
  height: 85vh;
  max-height: 750px;
  background: var(--td-bg-color-container);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.close-btn {
  position: absolute;
  top: 20px;
  right: 20px;
  width: 32px;
  height: 32px;
  border: none;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  transition: all 0.2s ease;
  z-index: 10;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.settings-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.settings-sidebar {
  width: 200px;
  background: var(--td-bg-color-settings-modal);
  border-right: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}

.sidebar-header {
  padding: 24px 20px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.sidebar-title {
  margin: 0;
  font-family: var(--app-font-family);
  font-size: 18px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.settings-nav {
  flex: 1;
  padding: 12px 8px;
  overflow-y: auto;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 10px 12px;
  margin-bottom: 4px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  font-family: var(--app-font-family);
  font-size: 14px;
  color: var(--td-text-color-secondary);

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.nav-icon {
  margin-right: 8px;
  font-size: 18px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.nav-label {
  flex: 1;
}

.settings-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding: 24px 32px;
}

.section {
  width: 100%;
}

// 与知识库设置一致的 section-header 样式
.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;

    .doc-link {
      margin-left: 8px;
    }
  }
}

// 与知识库设置一致的 settings-group 样式
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
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }

  &.setting-row-vertical {
    flex-direction: column;
    gap: 12px;
    
    .setting-info {
      max-width: 100%;
      padding-right: 0;
    }
  }

  // 强调行：用于"智能体类型"这类对用户影响最大、需要突出的关键配置。
  // 视觉上只做极轻度强调 —— 左侧 3px 品牌色竖条 + label 加粗。
  &.setting-row--emphasize {
    position: relative;
    padding-left: 14px;

    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 22px;
      bottom: 22px;
      width: 3px;
      border-radius: 2px;
      background: var(--td-brand-color, #0052d9);
    }

    .setting-info label {
      font-weight: 600;
    }
  }
}

.setting-info {
  flex: 1;
  max-width: 55%;
  padding-right: 24px;

  &.full-width {
    max-width: 100%;
    padding-right: 0;
  }

  .setting-info-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
    
    label {
      margin-bottom: 0;
    }
  }

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;

    .required {
      color: var(--td-error-color);
      margin-left: 2px;
    }
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 360px;
  display: flex;
  justify-content: flex-end;
  align-items: flex-start;

  &.setting-control-full {
    width: 100%;
    min-width: 100%;
    justify-content: flex-start;
  }

  // 让 select 和 input 占满控件区域
  :deep(.t-select),
  :deep(.t-input),
  :deep(.t-textarea) {
    width: 100%;
  }

  :deep(.t-input-number) {
    width: 120px;
  }
}

.select-option-with-tag {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  gap: 8px;
}

.go-settings-link {
  font-size: 12px;
  color: var(--td-brand-color);
  margin-top: 4px;
  text-decoration: none;
  &:hover {
    text-decoration: underline;
  }
}

// 名称输入框带头像预览
.name-input-wrapper {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;

  .name-input {
    flex: 1;
  }
}

.settings-footer {
  padding: 16px 32px;
  border-top: 1px solid var(--td-component-stroke);
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
}

// 模式提示样式
.mode-hint {
  display: flex;
  align-items: center;
  padding: 10px 14px;
  background: var(--td-success-color-light);
  border-radius: 6px;
  border: 1px solid var(--td-success-color-focus);
  color: var(--td-brand-color);
  font-size: 13px;
  line-height: 1.5;
}

// 过渡动画
.modal-enter-active,
.modal-leave-active {
  transition: all 0.3s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.95);
  }
}

// Slider 样式
.slider-wrapper {
  display: flex;
  align-items: center;
  gap: 16px;
  width: 100%;

  :deep(.t-slider) {
    flex: 1;
  }
}

.slider-value {
  width: 40px;
  text-align: right;
  font-family: var(--app-font-family-mono);
  font-size: 14px;
  color: var(--td-text-color-primary);
}

// 推荐问题列表
.suggested-prompts-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.prompt-item {
  display: flex;
  align-items: center;
  gap: 8px;

  :deep(.t-input) {
    flex: 1;
  }
}

// ===== 工具配置：overview 面板 =====
.tools-overview {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 20px;
  padding: 14px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke);
}

.tools-overview-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;

  &--preset {
    border-top: 1px dashed var(--td-component-stroke);
    padding-top: 10px;
    justify-content: space-between;
  }
}

.tools-status-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-container);
  border-radius: 999px;
  border: 1px solid var(--td-component-stroke);

  .t-icon {
    color: var(--td-text-color-secondary);
    font-size: 14px;
  }

  .tools-status-metric {
    display: inline-flex;
    align-items: baseline;
    gap: 4px;

    strong {
      font-size: 14px;
      font-weight: 600;
      color: var(--td-text-color-primary);
    }
  }

  .tools-status-sep {
    color: var(--td-text-color-placeholder);
  }

  &--warn {
    color: var(--td-warning-color);
    background: var(--td-warning-color-1, rgba(237, 118, 20, 0.08));
    border-color: var(--td-warning-color-light, #fcd7b6);

    .t-icon { color: var(--td-warning-color); }
  }
}

// ===== 按组的卡片网格 =====
.tool-groups {
  display: flex;
  flex-direction: column;
  gap: 22px;
  width: 100%;
}

.tool-group {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.tool-group-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 2px;

  .tool-group-bar {
    display: inline-block;
    width: 3px;
    height: 14px;
    border-radius: 2px;
    background: var(--td-brand-color);
  }

  .tool-group-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    letter-spacing: 0.2px;
  }

  .tool-group-count {
    min-width: 20px;
    padding: 0 6px;
    font-size: 11px;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 999px;
    text-align: center;
    line-height: 18px;
  }

  .tool-group-warning {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px;
    font-size: 12px;
    color: var(--td-warning-color);
    background: var(--td-warning-color-1, rgba(237, 118, 20, 0.08));
    border: 1px solid var(--td-warning-color-light, #fcd7b6);
    border-radius: 999px;

    .t-icon { font-size: 13px; }
  }
}

// 不同分组的左侧色条
.tool-group--base      .tool-group-bar { background: var(--td-gray-color-6, #a0a7ab); }
.tool-group--rag       .tool-group-bar { background: var(--td-brand-color); }
.tool-group--wiki_read .tool-group-bar { background: var(--td-success-color, #2ba471); }
.tool-group--wiki_edit .tool-group-bar { background: var(--td-warning-color, #ed7b2f); }
.tool-group--wiki_issue .tool-group-bar { background: var(--td-purple-5, #8e56dd); }
.tool-group--data      .tool-group-bar { background: var(--td-cyan-6, #09a3b7); }

// 统一两列网格；小屏退化单列
.tool-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  width: 100%;

  @media (max-width: 720px) {
    grid-template-columns: 1fr;
  }
}

// ===== 工具卡片（基于 t-checkbox 的 label 结构） =====
.tool-card {
  margin: 0; // 清掉 TDesign checkbox 默认外边距
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  border: 1px solid var(--td-component-stroke);
  transition: border-color .2s, background .2s;
  cursor: pointer;
  overflow: hidden;

  &:hover:not(.tool-card--disabled) {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-1, rgba(0, 82, 217, 0.04));
  }

  // checkbox 的勾选框 + label 改造
  :deep(.t-checkbox__input) {
    margin-top: 2px;
    flex-shrink: 0;
  }

  :deep(.t-checkbox__label) {
    flex: 1;
    min-width: 0;
    padding-left: 10px;
  }

  &.t-is-checked {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-1, rgba(0, 82, 217, 0.06));
  }

  &--disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }

  &--danger {
    border-color: var(--td-warning-color-light, #fcd7b6);

    &:hover:not(.tool-card--disabled) {
      border-color: var(--td-warning-color);
      background: var(--td-warning-color-1, rgba(237, 118, 20, 0.06));
    }

    &.t-is-checked {
      border-color: var(--td-warning-color);
      background: var(--td-warning-color-1, rgba(237, 118, 20, 0.08));
    }
  }
}

.tool-card-body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.tool-card-head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.tool-card-name {
  font-size: 13.5px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 0 1 auto;
  min-width: 0;
}

.tool-card-badge {
  flex: 0 0 auto;
  font-size: 10.5px;
  line-height: 1;
  padding: 3px 6px;
  color: var(--td-warning-color);
  background: transparent;
  border: 1px solid var(--td-warning-color-light, #fcd7b6);
  border-radius: 4px;
  letter-spacing: 0.3px;
}

.tool-card-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.tool-card-hint {
  font-size: 11px;
  color: var(--td-warning-color);
  font-style: italic;
  line-height: 1.4;
}

.tool-card--disabled {
  .tool-card-name,
  .tool-card-desc {
    color: var(--td-text-color-placeholder);
  }
}

// ===== 有效工具预览（芯片组）=====
.effective-tools {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 12px;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  border: 1px dashed var(--td-component-stroke);
  min-height: 52px;
  align-items: flex-start;
}

.effective-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-brand-color);
  background: var(--td-brand-color-1, rgba(0, 82, 217, 0.08));
  border: 1px solid var(--td-brand-color-2, rgba(0, 82, 217, 0.16));
  border-radius: 999px;
  max-width: 100%;
}

.effective-chip-label {
  font-weight: 500;
}

.effective-chip-reason {
  font-size: 11px;
  color: var(--td-warning-color);
  font-style: normal;

  &::before {
    content: "· ";
    color: var(--td-text-color-placeholder);
    margin-right: 2px;
  }
}

.effective-chip--inactive {
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  border-color: var(--td-component-stroke);

  .effective-chip-label {
    text-decoration: line-through;
  }
}

.effective-tools-empty {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-style: italic;
}

// Skills 选择样式
.skills-checkbox-group {
  display: grid;
  grid-template-columns: 1fr;
  gap: 12px;
  width: 100%;
}

.skill-checkbox-item {
  display: flex;
  align-items: flex-start;
  padding: 12px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  border: 1px solid var(--td-component-stroke);
  transition: all 0.2s ease;

  &:hover {
    border-color: var(--td-brand-color);
    background: var(--td-success-color-light);
  }

  :deep(.t-checkbox__input) {
    margin-top: 2px;
  }

  :deep(.t-checkbox__label) {
    flex: 1;
  }
}

.skill-item-content {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.skill-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.skill-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.skill-info-box {
  display: flex;
  gap: 12px;
  padding: 16px;
  background: var(--td-brand-color-light);
  border-radius: 8px;
  border: 1px solid var(--td-brand-color-focus);
  margin-top: 16px;

  .info-icon {
    font-size: 20px;
    color: var(--td-brand-color);
    flex-shrink: 0;
    margin-top: 2px;
  }

  .info-content {
    flex: 1;

    p {
      margin: 0;
      font-size: 13px;
      color: var(--td-text-color-secondary);
      line-height: 1.6;

      &:first-child {
        margin-bottom: 4px;
      }

      strong {
        color: var(--td-brand-color);
      }
    }
  }
}

.empty-hint {
  color: var(--td-text-color-placeholder);
  font-style: italic;
}


// textarea 与模板选择器容器
.textarea-with-template {
  position: relative;
  width: 100%;
}

// 系统提示词输入框样式
.system-prompt-textarea {
  width: 100%;
  font-family: var(--app-font-family-mono);
  font-size: 13px;

  :deep(textarea) {
    resize: vertical !important;
    min-height: 200px;
  }
}

// 占位符标签组样式
.placeholder-tags {
  margin-top: 6px;
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  line-height: 1.4;
  overflow-x: auto;
  white-space: nowrap;
  padding-bottom: 4px;
  
  // 隐藏滚动条但保持可滚动
  scrollbar-width: thin;
  &::-webkit-scrollbar {
    height: 4px;
  }
  &::-webkit-scrollbar-thumb {
    background: rgba(0, 0, 0, 0.1);
    border-radius: 2px;
  }

  .placeholder-label {
    color: var(--td-text-color-secondary, #666);
    flex-shrink: 0;
  }

  .placeholder-hint {
    color: var(--td-text-color-placeholder, #999);
    font-size: 11px;
    user-select: none;
    flex-shrink: 0;
  }

  .placeholder-tag {
    display: inline-flex;
    align-items: center;
    padding: 1px 5px;
    border-radius: 3px;
    font-family: var(--app-font-family-mono);
    font-size: 11px;
    color: var(--td-text-color-primary, #333);
    background-color: var(--td-bg-color-secondarycontainer, #f3f3f3);
    cursor: pointer;
    transition: all 0.2s;
    user-select: none;
    border: 1px solid transparent;
    flex-shrink: 0;

    &:hover {
      color: var(--td-brand-color, #0052d9);
      background-color: var(--td-brand-color-light, #ecf2fe);
      border-color: var(--td-brand-color-focus, #d0e0fd);
    }

    &:active {
      background-color: var(--td-brand-color-focus, #d0e0fd);
    }
  }
}

.placeholder-popup-wrapper {
  position: fixed;
  z-index: 10001;
  pointer-events: auto;
}

.placeholder-popup {
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-stroke, #e5e7eb);
  border-radius: 6px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
  max-width: 320px;
  max-height: 240px;
  overflow-y: auto;
  padding: 4px;
}

.placeholder-item {
  padding: 6px 10px;
  cursor: pointer;
  transition: background-color 0.15s;
  border-radius: 4px;

  &:hover,
  &.active {
    background-color: var(--td-bg-color-container-hover, #f5f7fa);
  }

  .placeholder-name {
    margin-bottom: 2px;

    code {
      background: var(--td-bg-color-container-hover, #f5f7fa);
      padding: 2px 5px;
      border-radius: 3px;
      font-family: var(--app-font-family-mono);
      font-size: 11px;
      color: var(--td-brand-color, #0052d9);
    }
  }

  .placeholder-desc {
    font-size: 11px;
    color: var(--td-text-color-secondary, #666);
  }
}

// 内置智能体提示
.builtin-agent-notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: var(--td-warning-color-light);
  border: 1px solid var(--td-warning-color-focus);
  border-radius: 8px;
  margin-bottom: 16px;
  color: var(--td-warning-color);
  font-size: 14px;

  .t-icon {
    font-size: 16px;
    flex-shrink: 0;
  }
}

// 内置智能体头像
.builtin-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border-radius: 12px;
  flex-shrink: 0;
  
  &.normal {
    background: linear-gradient(135deg, rgba(7, 192, 95, 0.15) 0%, rgba(7, 192, 95, 0.08) 100%);
    color: var(--td-brand-color-active);
  }
  
  &.agent {
    background: linear-gradient(135deg, rgba(124, 77, 255, 0.15) 0%, rgba(124, 77, 255, 0.08) 100%);
    color: var(--td-brand-color);
  }
}

// 提示词开关
.prompt-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;

  .prompt-toggle-label {
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }
}

// 提示词禁用提示
.prompt-disabled-hint {
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  font-style: italic;
  padding: 12px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
}

// 系统提示词Tabs
.system-prompt-tabs {
  width: 100%;

  .prompt-variant-tabs {
    :deep(.t-tabs__nav) {
      margin-bottom: 12px;
    }
  }
}

// 知识库选项样式
.kb-option-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 2px 0;
}

.kb-option-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  font-size: 14px;
  
  // Document KB
  &.doc-icon {
    background: rgba(16, 185, 129, 0.1);
    color: var(--td-success-color);
  }
  
  // FAQ KB
  &.faq-icon {
    background: rgba(0, 82, 217, 0.1);
    color: var(--td-brand-color);
  }
}

.kb-option-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  color: var(--td-text-color-primary);
}

.kb-option-org {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  padding: 1px 6px;
  border-radius: 4px;
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-option-disabled-hint {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-warning-color-6, #d46b08);
  background: var(--td-warning-color-1, #fff7e6);
  padding: 1px 6px;
  border-radius: 4px;
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-type-preset-desc {
  margin-top: 4px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.agent-type-select {
  width: 100%;
  min-width: 240px;
  max-width: 360px;
}

.kb-option-count {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  padding: 1px 6px;
  border-radius: 4px;
}

.kb-option-tag {
  flex-shrink: 0;
  font-size: 10px;
  font-weight: 500;
  padding: 0 5px;
  border-radius: 3px;
  line-height: 18px;
}

.tag-rag {
  color: #165dff;
  background: rgba(22, 93, 255, 0.1);
}

.tag-wiki {
  color: #00b42a;
  background: rgba(0, 180, 42, 0.1);
}

// FAQ 策略区域样式
.faq-strategy-section {
  margin-top: 24px;
  padding: 16px;
  background: rgba(0, 82, 217, 0.04);
  border: 1px solid rgba(0, 82, 217, 0.15);
  border-radius: 8px;
}

.faq-strategy-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-brand-color);
  
  .faq-icon {
    font-size: 18px;
  }
  
  .help-icon {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    cursor: help;
  }
}

.faq-strategy-section .setting-row {
  padding: 12px 0;
  border-bottom: 1px solid rgba(0, 82, 217, 0.1);
  
  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
  
  &:first-of-type {
    padding-top: 0;
  }
}
</style>

<!-- Non-scoped styles: TDesign teleports the popup outside this component, so
     scoped selectors can't reach .agent-type-popup .t-select-option. -->
<style lang="less">
.agent-type-popup {
  .t-select-option {
    // 默认 option 是 32px 单行；我们要双行显示，取消固定高度并放宽 padding
    height: auto !important;
    min-height: 48px;
    line-height: 1.4;
    padding: 8px 12px;
    white-space: normal;
  }
}

.agent-type-option {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  width: 100%;
}

.agent-type-option-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;
}

.agent-type-option-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.4;
  white-space: normal;
  overflow-wrap: break-word;
}
</style>
