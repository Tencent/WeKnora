<template>
  <SettingDrawer :visible="dialogVisible" :title="isEdit ? $t('model.editor.editTitle') : $t('model.editor.addTitle')"
    :description="getModalDescription()" :icon="modelTypeIcon" :confirm-loading="saving"
    :confirm-disabled="formData.provider === 'weknoracloud' && wkcCredentialState !== 'configured'"
    @update:visible="(v: boolean) => dialogVisible = v" @confirm="handleConfirm" @cancel="handleCancel">

    <!--
      Footer-left slot: connection-test button lives here so it sits next to
      Save/Cancel — primary actions all aligned along the bottom of the
      drawer. Avoids the "test, then scroll back down to save" dance.
      Mirrors the pattern used in WebSearchSettings' provider drawer.
    -->
    <template v-if="formData.source === 'remote'" #footer-left>
      <t-button variant="outline" @click="checkRemoteAPI" :loading="checking"
        :disabled="!formData.modelName || (!formData.baseUrl && formData.provider !== 'weknoracloud') || (formData.provider === 'weknoracloud' && wkcCredentialState !== 'configured')">
        <template #icon>
          <t-icon v-if="!checking && remoteChecked && remoteAvailable" name="check-circle-filled"
            class="status-icon available" />
          <t-icon v-else-if="!checking && remoteChecked && !remoteAvailable" name="close-circle-filled"
            class="status-icon unavailable" />
        </template>
        {{ checking ? $t('model.editor.testing') : $t('model.editor.testConnection') }}
      </t-button>
      <span v-if="remoteChecked" :class="['footer-test-message', remoteAvailable ? 'success' : 'error']"
        :title="remoteMessage">
        {{ remoteMessage }}
      </span>
    </template>

    <t-form ref="formRef" :data="formData" layout="vertical">

      <section v-if="!isEdit" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionType') }}</h4>
        <div class="model-type-options" role="radiogroup" :aria-label="$t('model.editor.typeLabel')">
          <button
            v-for="opt in modelTypeChoices"
            :key="opt.value"
            type="button"
            class="model-type-option"
            :class="{ 'is-active': activeModelType === opt.value }"
            role="radio"
            :aria-checked="activeModelType === opt.value"
            @click="selectModelType(opt.value)"
          >
            <t-icon :name="opt.icon" class="model-type-option__icon" />
            <span class="model-type-option__label">{{ opt.label }}</span>
          </button>
        </div>
      </section>

      <!--
        Section 1 — 接入配置：厂商决定一切（ollama=本地分支，其余=远程分支）。
        2026-09-13 裁定：「模型来源」控件退役，source 由 provider 派生。
      -->
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionProvider') }}</h4>

          <!-- 厂商选择器 -->
          <div class="form-item">
            <label class="form-label">{{ $t('model.editor.providerLabel') }}</label>
            <t-select v-model="formData.provider" :placeholder="$t('model.editor.providerPlaceholder')"
              @change="handleProviderChange" :popup-props="{ overlayClassName: 'provider-select-popup' }">
              <!--
                show-overflow-tooltip=false: TDesign 默认在 hover 时给选项浮一个
                完整 label 的小气泡，但这里选项本身就是双行（主名 + 描述），不会
                出现省略，tooltip 只会和已经命中的灰底打架。直接关掉。
              -->
              <t-option v-for="opt in providerOptions" :key="opt.value" :value="opt.value" :label="opt.label"
                :show-overflow-tooltip="false">
                <div class="provider-option">
                  <!-- #15：厂商 LOGO（无资源回落首字母徽章） -->
                  <img v-if="providerLogoMatch(opt.value)?.mode === 'color'" :src="providerLogoMatch(opt.value)!.url"
                    :alt="opt.label" class="provider-option__logo" />
                  <span v-else-if="providerLogoMatch(opt.value)?.mode === 'mono'"
                    class="provider-option__logo provider-option__logo--mono"
                    :style="{ '--logo-url': `url('${providerLogoMatch(opt.value)!.url}')` }" />
                  <span v-else class="provider-option__logo provider-option__logo--badge">{{ opt.label.charAt(0) }}</span>
                  <span class="provider-option__text">
                    <span class="provider-name">{{ opt.label }}</span>
                    <span class="provider-desc">{{ opt.description }}</span>
                  </span>
                </div>
              </t-option>
            </t-select>
          </div>

          <!-- WeKnoraCloud 提示信息 -->
          <template v-if="formData.provider === 'weknoracloud'">
            <!-- 凭证已配置 -->
            <div v-if="wkcCredentialState === 'configured'" class="weknoracloud-hint weknoracloud-hint--ok">
              <t-icon name="check-circle-filled" class="hint-icon hint-icon--ok" />
              <div>
                {{ $t('settings.weknoraCloud.modelHintConfigured') }}
                <a href="https://developers.weixin.qq.com/doc/aispeech/knowledge/atomic_capability/atomic_interface.html"
                  target="_blank" rel="noopener noreferrer" class="doc-link">
                  {{ $t('settings.weknoraCloud.modelHintDocsLink') }}
                  <t-icon name="link" class="link-icon" />
                </a>
              </div>
            </div>

            <!-- 未配置 / 失效 -->
            <div v-else-if="wkcCredentialState !== 'loading'" class="weknoracloud-hint weknoracloud-hint--warn">
              <t-icon name="error-circle-filled" class="hint-icon hint-icon--warn" />
              <div style="flex: 1;">
                <template v-if="wkcCredentialState === 'expired'">
                  {{ $t('settings.weknoraCloud.credentialExpired') }}
                </template>
                <template v-else>
                  {{ $t('settings.weknoraCloud.credentialUnconfigured') }}
                </template>
                <div style="margin-top: 8px;">
                  <t-button variant="text" size="small" @click="goToWeKnoraCloudSettings"
                    style="padding: 0; height: auto;">
                    <template #icon><t-icon name="jump" /></template>
                    {{ $t('settings.weknoraCloud.goToSettings') }}
                  </t-button>
                </div>
              </div>
            </div>

            <!-- 加载中 -->
            <div v-else class="weknoracloud-hint">
              <t-icon name="loading" class="spinning hint-icon hint-icon--loading" />
              <span>{{ $t('settings.weknoraCloud.checkingStatus') }}</span>
            </div>
          </template>

        <!-- 本地 Ollama 分支（2026-09-13 起以厂商身份出现在厂商列表） -->
        <template v-else-if="isOllamaProvider">
          <!-- ReRank模型不支持Ollama的提示信息 -->
          <div v-if="activeModelType === 'rerank'" class="ollama-unavailable-tip rerank-tip">
            <t-icon name="info-circle-filled" class="tip-icon info" />
            <span class="tip-text">{{ $t('model.editor.ollamaNotSupportRerank') }}</span>
          </div>

          <!-- Ollama不可用时的提示信息 -->
          <div v-else-if="shouldShowOllamaUnavailableTip(isOllamaProvider ? 'local' : 'remote', activeModelType, ollamaServiceStatus)"
            class="ollama-unavailable-tip">
            <t-icon name="error-circle-filled" class="tip-icon" />
            <span class="tip-text">{{ $t('model.editor.ollamaUnavailable') }}</span>
            <t-button variant="text" size="small" @click="goToOllamaSettings" class="tip-link">
              <template #icon><t-icon name="jump" /></template>
              {{ $t('model.editor.goToOllamaSettings') }}
            </t-button>
          </div>



        <!-- Ollama 本地模型选择器 -->
        <div class="form-item">
          <label class="form-label required">{{ $t('model.modelName') }}</label>
          <div class="model-select-row">
            <t-select v-model="formData.modelName" :loading="loadingOllamaModels" :class="{ 'downloading': downloading }"
              :style="downloading ? `--progress: ${downloadProgress}%` : ''" filterable :filter="handleModelFilter"
              :placeholder="$t('model.searchPlaceholder')" @focus="loadOllamaModels"
              @visible-change="handleDropdownVisibleChange">
              <!-- 已下载的模型 -->
              <t-option v-for="model in filteredOllamaModels" :key="model.name" :value="model.name" :label="model.name">
                <div class="model-option">
                  <t-icon name="check-circle-filled" class="downloaded-icon" />
                  <span class="model-name">{{ model.name }}</span>
                  <span class="model-size">{{ formatModelSize(model.size) }}</span>
                </div>
              </t-option>

              <!-- 下载新模型选项（仅当搜索词不在列表中时显示） -->
              <t-option v-if="showDownloadOption" :value="`__download__${searchKeyword}`"
                :label="$t('model.editor.downloadLabel', { keyword: searchKeyword })" class="download-option">
                <div class="model-option download">
                  <t-icon name="download" class="download-icon" />
                  <span class="model-name">{{ $t('model.editor.downloadLabel', { keyword: searchKeyword }) }}</span>
                </div>
              </t-option>

              <!-- 下载进度后缀 -->
              <template v-if="downloading" #suffix>
                <div class="download-suffix">
                  <t-icon name="loading" class="spinning" />
                  <span class="progress-text">{{ downloadProgress.toFixed(1) }}%</span>
                </div>
              </template>
            </t-select>

            <!-- 刷新按钮 -->
            <t-button variant="text" size="small" :loading="loadingOllamaModels" @click="refreshOllamaModels"
              class="refresh-btn">
              <t-icon name="refresh" />
              {{ $t('model.editor.refreshList') }}
            </t-button>
          </div>
        </div>
        </template>

        <!-- 远程分支：连接组 + 模型名称/显示名称 -->
        <template v-else>
          <div v-if="formData.provider !== 'weknoracloud'" class="form-item">
            <label class="form-label required">{{ $t('model.editor.baseUrlLabel') }}</label>
            <t-input v-model="formData.baseUrl" :placeholder="getBaseUrlPlaceholder()" />
          </div>

          <!--
            凭证表单：按所选厂商 Credentials spec（/models/providers 下发，
            design §6.8）动态渲染，切厂商字段组跟随。weknoracloud spec=[]
            → 整块不渲染（沿用空间级设置 + wkcCredentialState 状态提示）。
            Edit mode: credentials live behind the /credentials subresource
            of the model — managed by the shared CredentialResource card
            (Configured 状态元数据卡片，不做掩码字符串).
            Create mode: the resource doesn't exist yet, so each spec slot
            renders a plain password input with a leading lock icon and a
            trailing show/hide eye toggle. 空值提交 = 不携带（不修改）。
          -->
          <div v-if="credentialBlockVisible" class="form-item">
            <!-- 编辑模式：单字段时父级出 label（与 CredentialResource 约定一致） -->
            <template v-if="isEdit && props.modelData?.id">
              <label v-if="credentialFields.length === 1" class="form-label">
                {{ credentialFields[0].label }}
              </label>
              <CredentialResource :api="credentialApi" :fields="credentialFields" :meta="credentialMeta" />
            </template>
            <!-- 创建模式：按 spec 逐槽渲染密码输入 -->
            <template v-else>
              <div v-for="field in credentialFields" :key="field.key" class="form-item credential-create-item">
                <label class="form-label" :class="{ required: isCredentialRequired(field.key) }">{{ field.label }}</label>
                <t-input
                  :model-value="credentialValue(field.key)"
                  :type="revealedCredentialKeys.has(field.key) ? 'text' : 'password'"
                  :placeholder="credentialPlaceholder(field.key)"
                  class="api-key-input" autocomplete="off" spellcheck="false"
                  @update:model-value="(v: string) => setCredentialValue(field.key, v)"
                >
                  <template #prefix-icon><t-icon name="lock-on" /></template>
                  <template #suffix-icon>
                    <t-icon
                      :name="revealedCredentialKeys.has(field.key) ? 'browse-off' : 'browse'"
                      class="api-key-toggle"
                      :aria-label="revealedCredentialKeys.has(field.key) ? 'Hide' : 'Show'"
                      @click.stop="toggleCredentialReveal(field.key)"
                    />
                  </template>
                </t-input>
              </div>
              <p v-if="isSignedRerank" class="form-desc">{{ signedRerankCredentialHint }}</p>
            </template>
          </div>

          <div v-if="isLkeapRerank" class="form-item">
            <label class="form-label">{{ $t('model.editor.lkeap.regionLabel') }}</label>
            <t-input v-model="formData.lkeapRegion" :placeholder="$t('model.editor.lkeap.regionPlaceholder')" />
            <p class="form-desc">{{ $t('model.editor.lkeap.regionDesc') }}</p>
          </div>

          <!-- 厂商动态扩展字段（providers extraFields 下发，如 azure api_version；
               值进模型 extra_config，测试连接与生产调用同源透传） -->
          <div v-for="f in activeExtraFields" :key="f.key" class="form-item">
            <label class="form-label" :class="{ required: f.required }">{{ f.label }}</label>
            <t-input v-model="formData.extraConfig![f.key]" :placeholder="f.placeholder || f.default" />
          </div>

          <!-- 自定义 HTTP Header（类似 OpenAI Python SDK 的 extra_headers） -->
          <div v-if="formData.provider !== 'weknoracloud'" class="form-item">
            <div class="custom-headers-header">
              <label class="form-label" style="margin-bottom: 0;">{{ $t('model.editor.customHeadersLabel') }}</label>
              <t-button variant="text" size="small" theme="primary" @click="addCustomHeader">
                <template #icon><t-icon name="add" /></template>
                {{ $t('model.editor.customHeadersAdd') }}
              </t-button>
            </div>
            <p class="form-desc custom-headers-desc">{{ $t('model.editor.customHeadersDesc') }}</p>
            <div v-if="formData.customHeaders && formData.customHeaders.length > 0" class="custom-headers-list">
              <div v-for="(item, idx) in formData.customHeaders" :key="idx" class="custom-header-row">
                <t-input v-model="item.key" :placeholder="$t('model.editor.customHeadersKeyPlaceholder')"
                  class="custom-header-key" />
                <t-input v-model="item.value" :placeholder="$t('model.editor.customHeadersValuePlaceholder')"
                  class="custom-header-value" />
                <t-button variant="text" shape="square" size="small" class="custom-header-remove"
                  @click="removeCustomHeader(idx)" :aria-label="$t('common.delete')">
                  <t-icon name="close" />
                </t-button>
              </div>
            </div>
          </div>
          <!-- 模型名称 / 模型 ID。远程 chat/vllm 用可搜索下拉（远端列表探测）+ 自由输入。 -->
          <div class="form-item">
            <label class="form-label required">{{ $t('model.modelName') }}</label>
            <t-select
              v-if="showRemoteModelSelect"
              v-model="formData.modelName"
              filterable
              creatable
              clearable
              :loading="probingRemoteModels"
              :options="remoteModelOptions"
              :placeholder="getModelNamePlaceholder()"
              :disabled="formData.provider === 'weknoracloud' && wkcCredentialState !== 'configured'"
              @create="onRemoteModelCreate"
            />
            <t-input v-else v-model="formData.modelName" :placeholder="getModelNamePlaceholder()"
              :disabled="formData.provider === 'weknoracloud' && wkcCredentialState !== 'configured'" />
          </div>

          <div class="form-item">
            <label class="form-label">
              {{ $t('model.editor.displayNameLabel') }}
              <span v-if="prefillBadge('displayName')" class="prefill-source">{{ prefillBadge('displayName') }}</span>
            </label>
            <t-input v-model="formData.displayName" :placeholder="$t('model.editor.displayNamePlaceholder')"
              @change="markManualField('displayName')" />
            <p class="form-desc">{{ $t('model.editor.displayNameDesc') }}</p>
          </div>
        </template>

        <!--
          Connection test action moved to the drawer footer (footer-left
          slot above) so primary actions live in one row at the bottom.
        -->
      </section>


      <!-- Section 3 — 模型参数设置（2026-09-13 裁定：上下文/预算/模态/思考
           属模型参数；并发上限单独留"高级选项"） -->
      <section v-if="['embedding', 'chat', 'vllm'].includes(activeModelType)" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionParameters') }}</h4>

        <!-- Embedding 专用：维度 -->
        <div v-if="activeModelType === 'embedding'" class="form-item">
          <label class="form-label">{{ $t('model.editor.dimensionLabel') }}</label>
          <div class="dimension-control">
            <t-input v-model.number="formData.dimension" type="number" :min="128" :max="4096"
              :placeholder="$t('model.editor.dimensionPlaceholder')"
              :disabled="!formData.supportsDimensionOverride || (formData.source === 'local' && checking)" />
            <!-- Ollama 本地模型：自动检测维度按钮 -->
            <t-button v-if="formData.source === 'local' && formData.modelName" variant="text" size="small"
              :loading="checking" @click="checkOllamaDimension" class="dimension-check-btn">
              <t-icon name="refresh" />
              {{ $t('model.editor.checkDimension') }}
            </t-button>
          </div>
          <p v-if="dimensionChecked && dimensionMessage" class="dimension-hint" :class="{ success: dimensionSuccess }">
            {{ dimensionMessage }}
          </p>
        </div>

        <div v-if="activeModelType === 'embedding'" class="form-item">
          <label class="form-label">{{ $t('model.editor.dimensionOverrideLabel') }}</label>
          <div class="vision-toggle">
            <t-switch v-model="formData.supportsDimensionOverride" :disabled="dimensionOverrideDisabled" />
            <span class="form-desc form-desc--inline">{{ $t('model.editor.dimensionOverrideDesc') }}</span>
          </div>
          <p v-if="dimensionOverrideDisabled" class="form-desc">{{ $t('model.editor.dimensionOverrideDisabledHint') }}</p>
        </div>

        <!-- Chat / VLM: context window + max output tokens. Agent compaction sizes itself from this. -->
        <div v-if="activeModelType === 'chat' || activeModelType === 'vllm'" class="form-item">
          <label class="form-label">
            {{ $t('model.editor.contextWindowLabel') }}
            <span v-if="prefillBadge('contextWindow')" class="prefill-source">{{ prefillBadge('contextWindow') }}</span>
          </label>
          <t-input v-model.number="formData.contextWindow" type="number" :min="1024" :max="10000000"
            :placeholder="$t('model.editor.contextWindowPlaceholder', { value: DEFAULT_MODEL_CONTEXT_WINDOW })"
            @change="markManualField('contextWindow')" />
          <p class="form-desc">{{ $t('model.editor.contextWindowDesc') }}</p>
        </div>

        <div v-if="activeModelType === 'chat' || activeModelType === 'vllm'" class="form-item">
          <label class="form-label">
            {{ $t('model.editor.maxOutputTokensLabel') }}
            <span v-if="prefillBadge('maxOutputTokens')" class="prefill-source">{{ prefillBadge('maxOutputTokens') }}</span>
          </label>
          <t-input v-model.number="formData.maxOutputTokens" type="number" :min="1" :max="10000000"
            :placeholder="$t('model.editor.maxOutputTokensPlaceholder')"
            @change="markManualField('maxOutputTokens')" />
          <p class="form-desc">{{ $t('model.editor.maxOutputTokensDesc') }}</p>
        </div>

        <!-- Chat/VLLM: 输入模态多选（2026-09-13 裁定 #2/#3：自由编辑，列表
             未提供模态不设限；LLM 默认文本。存 chat 分片 input_modalities） -->
        <div v-if="activeModelType === 'chat' || activeModelType === 'vllm'" class="form-item">
          <label class="form-label">
            {{ $t('model.editor.inputModalitiesLabel') }}
            <span v-if="prefillBadge('inputModalities')" class="prefill-source">{{ prefillBadge('inputModalities') }}</span>
          </label>
          <t-checkbox-group v-model="formData.inputModalities" @change="markManualField('inputModalities')">
            <t-checkbox value="text">{{ $t('model.editor.modalityText') }}</t-checkbox>
            <t-checkbox value="image">{{ $t('model.editor.modalityImage') }}</t-checkbox>
            <t-checkbox value="audio">{{ $t('model.editor.modalityAudio') }}</t-checkbox>
            <t-checkbox value="video">{{ $t('model.editor.modalityVideo') }}</t-checkbox>
          </t-checkbox-group>
          <p class="form-desc">{{ $t('model.editor.inputModalitiesDesc') }}</p>
        </div>

        <!-- Chat + 远程 API：思考开关与档位（能力声明驱动；共享组件 design §8.1.1，
             levels 多选形态 + 徽章插槽，chat+remote 门控保留在本调用侧） -->
        <template v-if="showThinkingSection">
          <ThinkingControls
            v-model="thinkingControlsValue"
            edit-mode="levels"
            :caps="chatThinkingCaps"
            @manual="onThinkingManual"
          >
            <template #badge="{ field }">
              <span v-if="field === 'selectedLevels' && prefillBadge('selectedLevels')"
                class="prefill-source">{{ prefillBadge('selectedLevels') }}</span>
              <span v-else-if="field === 'level' && prefillBadge('thinkingLevel')"
                class="prefill-source">{{ prefillBadge('thinkingLevel') }}</span>
            </template>
          </ThinkingControls>
        </template>

        <!--
          Background concurrency cap for this model. Only chat / embedding / vllm
          are gated by the governor (see internal/models/limiter), so we surface
          it just for those three. 0 = fall back to the global default.
        -->
      </section>

      <!-- Section 4 — 高级选项（仅后台并发上限：治理治理面，非模型参数） -->
      <section v-if="['chat', 'embedding', 'vllm'].includes(activeModelType)" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('model.editor.sectionAdvanced') }}</h4>
        <div class="form-item">
          <label class="form-label">{{ $t('model.editor.maxConcurrencyLabel') }}</label>
          <t-input v-model.number="formData.maxConcurrency" type="number" :min="0" :max="4096"
            :placeholder="$t('model.editor.maxConcurrencyPlaceholder')" />
          <p class="form-desc">{{ $t('model.editor.maxConcurrencyDesc') }}</p>
        </div>
      </section>

    </t-form>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { ref, watch, computed, onUnmounted, nextTick } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { checkOllamaModels, checkRemoteModel, testEmbeddingModel, checkRerankModel, checkASRModel, listOllamaModels, downloadOllamaModel, getDownloadProgress, checkOllamaStatus, listModelProviders, type OllamaModelInfo, type ModelProviderOption } from '@/api/initialization'
import {
  getWeKnoraCloudStatus,
  putModelCredentials,
  deleteModelCredentialField,
  probeRemoteCatalog,
  fetchModelCatalog,
  type ModelCredentialField,
  type RemoteCatalogModel,
  type CatalogModelEntry,
} from '@/api/model'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'
import { DEFAULT_MODEL_CONTEXT_WINDOW } from '@/utils/contextWindow'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import CredentialResource, {
  type CredentialFieldDef,
  type CredentialResourceApi,
} from '@/components/credentials/CredentialResource.vue'
import { shouldShowOllamaUnavailableTip } from '@/components/modelEditorSourceState'
import ThinkingControls from '@/components/ThinkingControls.vue'
import { providerLogo } from '@/views/settings/providerLogos'

interface CustomHeaderItem {
  key: string
  value: string
}

interface ModelFormData {
  id: string
  name: string
  source: 'local' | 'remote'
  provider?: string // Provider identifier: openai, aliyun, zhipu, generic, etc.
  modelName: string
  displayName?: string
  baseUrl?: string
  apiKey?: string
  dimension?: number
  supportsDimensionOverride?: boolean
  interfaceType?: 'ollama' | 'openai'
  isDefault: boolean
  supportsVision?: boolean
  /** 对话/VLM 上下文窗口（token）。空/0 表示使用默认 200000。 */
  contextWindow?: number
  /** 后台任务对该模型的并发上限；0/undefined 表示沿用全局默认。仅 chat/embedding/vllm 生效。 */
  maxConcurrency?: number
  /** chat 分片：单次回复最大输出 token */
  maxOutputTokens?: number
  /** chat 分片：思考开关 */
  thinkingEnabled?: boolean
  /** chat 分片：默认思考档位（空 = 适配器自决） */
  thinkingLevel?: string
  /** chat 分片：该模型支持的档位子集 */
  selectedLevels?: string[]
  /** 凭证槽位：App ID（design §6.8 三槽之一） */
  appId?: string
  inputModalities?: string[]
  // 自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）
  customHeaders?: CustomHeaderItem[]
  /** LKEAP Rerank：腾讯云 SecretKey（创建时写入 app_secret） */
  appSecret?: string
  /** LKEAP Rerank：地域，如 ap-guangzhou */
  lkeapRegion?: string
  /** 厂商动态扩展字段（provider extraFields → 模型 extra_config，如 azure api_version） */
  extraConfig?: Record<string, string>
}

type EditorModelType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'

interface Props {
  visible: boolean
  modelType: EditorModelType
  modelData?: ModelFormData | null
}

const { t, te } = useI18n()
const uiStore = useUIStore()

const props = withDefaults(defineProps<Props>(), {
  visible: false,
  modelData: null
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  'confirm': [data: ModelFormData & { modelType?: EditorModelType }]
}>()

const draftModelType = ref<EditorModelType>(props.modelType)

const isEdit = computed(() => !!props.modelData)

const activeModelType = computed(() => (
  isEdit.value ? props.modelType : draftModelType.value
))

const modelTypeChoices = computed(() => ([
  { value: 'chat' as const, label: t('modelSettings.typeShort.chat'), icon: 'chat' },
  { value: 'embedding' as const, label: t('modelSettings.typeShort.embedding'), icon: 'chart-bubble' },
  { value: 'rerank' as const, label: t('modelSettings.typeShort.rerank'), icon: 'filter-sort' },
  { value: 'vllm' as const, label: t('modelSettings.typeShort.vllm'), icon: 'image' },
  { value: 'asr' as const, label: t('modelSettings.typeShort.asr'), icon: 'sound' },
]))

// API 返回的 Provider 列表
const apiProviderOptions = ref<ModelProviderOption[]>([])
const loadingProviders = ref(false)

// 兜底厂商：仅保留「自定义 (OpenAI 兼容)」。预置厂商清单以能力声明 API
// （/models/providers）为唯一来源；API 不可用时长尾厂商不再前端硬编码，
// 用户可先用 generic 手填 Base URL 接入。
const fallbackProviderOptions = computed<ModelProviderOption[]>(() => [
  {
    value: 'generic',
    label: t('model.editor.providers.generic.label'),
    defaultUrls: {},
    description: t('model.editor.providers.generic.description'),
    modelTypes: ['chat', 'embedding', 'rerank', 'vllm', 'asr']
  },
  {
    value: 'ollama',
    label: t('model.editor.providers.ollama.label'),
    defaultUrls: {
      chat: 'http://localhost:11434',
      embedding: 'http://localhost:11434',
      vllm: 'http://localhost:11434',
    } as Record<string, string>,
    description: t('model.editor.providers.ollama.description'),
    modelTypes: ['chat', 'embedding', 'vllm']
  },
])

// 从 API 获取 Provider 列表
const loadProviders = async () => {
  loadingProviders.value = true
  try {
    const providers = await listModelProviders(activeModelType.value)
    // 无条件赋值（2026-09-13 审查）：失败/空返回时保留旧列表会让用户给
    // embedding 挑一个 chat 专属厂商；清空后 computed 自动回落 generic 兜底。
    apiProviderOptions.value = providers
  } catch (error) {
    console.error('Failed to load providers from API, using fallback', error)
  } finally {
    loadingProviders.value = false
  }
}

// 根据当前模型类型过滤的 Provider 列表
// API 返回的 defaultUrls/modelTypes 数据优先，但 label/description 使用 i18n
const providerOptions = computed(() => {
  // API 数据可用时，用 API 的结构数据 + i18n 的显示文本
  if (apiProviderOptions.value.length > 0) {
    return apiProviderOptions.value.map(p => ({
      ...p,
      label: te(`model.editor.providers.${p.value}.label`)
        ? t(`model.editor.providers.${p.value}.label`)
        : p.label,
      description: te(`model.editor.providers.${p.value}.description`)
        ? t(`model.editor.providers.${p.value}.description`)
        : p.description,
    }))
  }
  // 回退到硬编码值，按 modelTypes 过滤
  return fallbackProviderOptions.value.filter(p =>
    p.modelTypes.includes(activeModelType.value)
  )
})

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})
/** 正在从 modelData 灌入表单，忽略厂商/来源控件的程序化 change 副作用 */
const hydratingForm = ref(false)

// ---- 能力声明（来源：/models/providers，前端零厂商字段知识） ----
const activeProviderCaps = computed(() =>
  apiProviderOptions.value.find(p => p.value === formData.value.provider)?.capabilities
)

// #15：厂商 LOGO 查询（assets/img/providers/*/model/<value>.svg）
const providerLogoMatch = (value: string) => {
  try {
    return providerLogo('model', value)
  } catch {
    return undefined
  }
}

/** 当前厂商的动态扩展字段（跳过已有专属控件/专属汇入逻辑的键）。 */
const activeExtraFields = computed(() => {
  const provider = providerOptions.value.find(p => p.value === formData.value.provider)
  return (provider?.extraFields ?? []).filter(f => {
    if (f.key === 'region' && isLkeapRerank.value) return false // lkeapRegion 专属控件
    return true
  })
})

/** Chat 类型远程模型的思考能力声明（选项/开关的渲染依据）。 */
const chatThinkingCaps = computed(() => (
  activeModelType.value === 'chat' && formData.value.source === 'remote'
    ? activeProviderCaps.value?.chat?.thinking
    : undefined
))

const showThinkingSection = computed(() => chatThinkingCaps.value?.supported === true)

/** ThinkingControls（levels 形态）的双向桥：值落在 chat 分片对应表单字段上。 */
const thinkingControlsValue = computed({
  get: () => ({
    enabled: formData.value.thinkingEnabled,
    level: formData.value.thinkingLevel,
    selectedLevels: formData.value.selectedLevels,
  }),
  set: (v) => {
    formData.value.thinkingEnabled = v.enabled
    formData.value.thinkingLevel = v.level ?? ''
    formData.value.selectedLevels = v.selectedLevels ?? []
  },
})

/** 组件字段名 → 预填追踪字段名（徽章来源标注清除）。 */
const onThinkingManual = (field: 'enabled' | 'selectedLevels' | 'level') => {
  markManualField(field === 'enabled' ? 'thinkingEnabled' : field === 'level' ? 'thinkingLevel' : 'selectedLevels')
}

// 输入模态自由编辑（2026-09-13 裁定 #2/#3）：模型列表/目录未提供模态
// 时不限制用户勾选；LLM 默认勾选文本（所有模型的基础能力）。supportsVision
// 保持与模态数组同步（payload 的 vllm 扁平字段与列表页图标消费它）。
watch(() => formData.value.inputModalities, (mods) => {
  formData.value.supportsVision = !!mods?.includes('image')
})

const dimensionOverrideDisabled = computed(() =>
  activeModelType.value === 'embedding'
  && activeProviderCaps.value?.embedding
  && activeProviderCaps.value.embedding.can_override_dimension === false
)

// ---- 目录预填（design §5.3 取值链 / ADR 0001 保存即终态） ----
type PrefillSource = 'catalog' | 'remote'
const PREFILL_FIELDS = ['contextWindow', 'maxOutputTokens', 'inputModalities', 'selectedLevels', 'thinkingLevel', 'thinkingEnabled', 'displayName'] as const
type PrefillField = (typeof PREFILL_FIELDS)[number]
const PREFILL_BADGE_KEYS: Record<PrefillSource, string> = {
  catalog: 'model.editor.sourceCatalog',
  remote: 'model.editor.sourceRemote',
}

const prefillSource = ref<Partial<Record<PrefillField, PrefillSource>>>({})
const manualFields = ref<Set<PrefillField>>(new Set())
/** 编辑已存模型打开后为 true：已存值优先，预填不覆盖；换模型 ID 视为新选择后解除（D1 推论）。 */
const prefillBlocked = ref(false)

const prefillBadge = (field: PrefillField) => {
  const src = prefillSource.value[field]
  return src ? t(PREFILL_BADGE_KEYS[src]) : ''
}

const markManualField = (field: PrefillField) => {
  manualFields.value.add(field)
  delete prefillSource.value[field]
}

function applyPrefill(meta: CatalogModelEntry, source: PrefillSource) {
  if (prefillBlocked.value) return
  const f = formData.value
  const set = (field: PrefillField, hasValue: boolean, assign: () => void) => {
    // 手改 > 已预填（接口元数据优先于目录） > 未填
    if (!hasValue || manualFields.value.has(field) || prefillSource.value[field]) return
    assign()
    prefillSource.value[field] = source
  }
  set('contextWindow', !!meta.context_window, () => { f.contextWindow = meta.context_window })
  set('maxOutputTokens', !!meta.max_output_tokens, () => { f.maxOutputTokens = meta.max_output_tokens })
  set('inputModalities', !!meta.input_modalities?.length, () => {
    f.inputModalities = [...(meta.input_modalities as string[])]
    f.supportsVision = meta.input_modalities?.includes('image') ?? false
  })
  if (meta.thinking?.supported && showThinkingSection.value) {
    const providerLevels = chatThinkingCaps.value?.supported_levels
    const levels = (meta.thinking.levels ?? []).filter(
      l => !providerLevels?.length || providerLevels.includes(l),
    )
    set('selectedLevels', levels.length > 0, () => { f.selectedLevels = [...levels] })
    const defaultLevel = meta.thinking.default_level
    set('thinkingLevel', !!defaultLevel && levels.includes(defaultLevel), () => {
      f.thinkingLevel = defaultLevel || ''
      if (f.thinkingLevel && f.thinkingEnabled === undefined) f.thinkingEnabled = true
    })
  }
}

// ---- 远端模型列表探测（可搜索下拉数据源，POST /models/remote-catalog） ----
const remoteModels = ref<RemoteCatalogModel[]>([])
const probingRemoteModels = ref(false)
let probeSequence = 0

// 2026-09-12 裁定③：所有远程模型列表加载按当前编辑的模型类型过滤——
// 五类型全放开远程下拉（探测请求携带 model_type；无过滤能力的厂商由
// 适配器忽略，失败降级手输不变）。
const showRemoteModelSelect = computed(() =>
  !isOllamaProvider.value
  && formData.value.provider !== 'weknoracloud'
)

// #反馈（2026-09-14）：服务端类型过滤只有 aliyun 能力码生效，其余厂商的
// 目录接口不带类型元数据——按模型名启发式在客户端过滤（creatable 手输
// 仍是逃逸口）。chat/vllm 做排除式（避免误杀命名特别的对话模型）。
const REMOTE_MODEL_TYPE_HINTS: Record<string, RegExp> = {
  embedding: /embed/i,
  rerank: /rerank/i,
  asr: /(whisper|paraformer|sensevoice|\basr\b|transcri|speech)/i,
}
const remoteModelMatchesType = (id: string, type: string): boolean => {
  const lower = id.toLowerCase()
  const isEmbed = /embed/i.test(lower)
  const isRerank = /rerank/i.test(lower)
  const isAsrTts = /(whisper|paraformer|sensevoice|asr|transcri|speech|tts|audio)/i.test(lower)
  switch (type) {
    case 'embedding':
      return isEmbed
    case 'rerank':
      return isRerank
    case 'asr':
      return isAsrTts
    default: // chat / vllm：排除明确的非对话族
      return !isEmbed && !isRerank && !isAsrTts
  }
}

const remoteModelOptions = computed(() =>
  remoteModels.value
    .filter(m => remoteModelMatchesType(m.id, activeModelType.value))
    .map(m => ({ value: m.id, label: m.id }))
)

const onRemoteModelCreate = (value: string | number) => {
  formData.value.modelName = String(value)
}

const probeRemoteModels = async () => {
  if (!showRemoteModelSelect.value || !formData.value.provider) return
  const seq = ++probeSequence
  probingRemoteModels.value = true
  try {
    // 编辑模式 apiKey 不在表单里：传 model_id 让后端用存储凭证兜底
    const payload = isEdit.value && props.modelData?.id
      ? { provider: formData.value.provider, model_id: props.modelData.id, base_url: formData.value.baseUrl || '', model_type: activeModelType.value }
      : { provider: formData.value.provider, base_url: formData.value.baseUrl || '', api_key: formData.value.apiKey || '', model_type: activeModelType.value }
    const result = await probeRemoteCatalog(payload)
    if (seq !== probeSequence) return
    remoteModels.value = result.available ? (result.models ?? []) : []
  } catch {
    if (seq === probeSequence) remoteModels.value = []
  } finally {
    if (seq === probeSequence) probingRemoteModels.value = false
  }
}

// ---- models.json 目录查询（GET /models/catalog?provider=X） ----
const catalogCache = ref<Record<string, Record<string, CatalogModelEntry>>>({})

const normalizeModelId = (id: string) =>
  id.trim().toLowerCase().replace(/-latest$/i, '').replace(/-\d{8}$/i, '')

const lookupCatalogEntry = (provider: string, modelId: string): CatalogModelEntry | null => {
  const models = catalogCache.value[provider]
  if (!models) return null
  const id = modelId.trim()
  if (!id) return null
  return models[id] ?? models[id.toLowerCase()] ?? models[normalizeModelId(id)] ?? null
}

const ensureCatalogLoaded = async (provider: string) => {
  if (!provider || catalogCache.value[provider]) return
  try {
    const result = await fetchModelCatalog(provider)
    if (result.available && result.providers) {
      catalogCache.value = {
        ...catalogCache.value,
        ...Object.fromEntries(
          Object.entries(result.providers).map(([key, value]) => [key, value.models ?? {}]),
        ),
      }
    } else {
      catalogCache.value[provider] = {}
    }
  } catch {
    catalogCache.value[provider] = {}
  }
}

// 后端 RemoteModel 摊平字段 → 目录条目形状（applyPrefill 的输入）。
// 后端不发 thinking 的 supported/can_disable/default_level：有 levels 即视为
// supported，can_disable 取宽（可被取值链第 2 级目录修正）。
const remoteModelPrefill = (m: RemoteCatalogModel): CatalogModelEntry => ({
  context_window: m.context_window,
  max_output_tokens: m.max_output_tokens,
  input_modalities: m.modalities,
  thinking: m.thinking_levels?.length
    ? { supported: true, can_disable: true, levels: m.thinking_levels }
    : undefined,
})

/** 换模型 ID = 新选择：按取值链预填（接口元数据 → 目录 → 留空）。 */
const prefillForModelId = async (modelId: string) => {
  if (!showRemoteModelSelect.value || !modelId.trim() || !formData.value.provider) return
  // 新一轮预填：清掉上一轮的来源标注（手动字段保留标注清除权）
  for (const field of PREFILL_FIELDS) {
    if (!manualFields.value.has(field)) delete prefillSource.value[field]
  }
  const provider = formData.value.provider
  const id = modelId.trim()
  // 1) 接口元数据（后端已摊平为扁平字段，meta 信封已删除——取值链第 1 级）
  const remoteHit = remoteModels.value.find(m => m.id === id)
  if (remoteHit) applyPrefill(remoteModelPrefill(remoteHit), 'remote')
  // 展示名 → 显示名称（2026-09-14 反馈）：选模型即带出厂商的展示名；
  // 手动改过 displayName 的不再覆盖。
  if (remoteHit?.display_name && !manualFields.value.has('displayName')) {
    formData.value.displayName = remoteHit.display_name
    prefillSource.value.displayName = 'remote'
  }
  // 2) models.json 目录（补齐接口元数据未覆盖的字段）
  await ensureCatalogLoaded(provider)
  const entry = lookupCatalogEntry(provider, id)
  if (entry) applyPrefill(entry, 'catalog')
}



// Header icon for the SettingDrawer — uses the same TDesign icon name table
// as the model card list, so the drawer's leading badge visually matches the
// card the user just clicked on.
const modelTypeIcon = computed(() => {
  const map: Record<string, string> = {
    chat: 'chat',
    embedding: 'chart-bubble',
    rerank: 'filter-sort',
    vllm: 'image',
    asr: 'sound',
  }
  return map[activeModelType.value] || 'setting'
})

const isLkeapRerank = computed(
  () => activeModelType.value === 'rerank' && formData.value.provider === 'lkeap',
)
const isVolcengineRerank = computed(
  () => activeModelType.value === 'rerank' && formData.value.provider === 'volcengine',
)
const isSignedRerank = computed(
  () => isLkeapRerank.value || isVolcengineRerank.value,
)
const signedRerankAccessKeyLabel = computed(() => (
  isVolcengineRerank.value
    ? t('model.editor.volcengine.accessKeyLabel')
    : t('model.editor.lkeap.secretIdLabel')
))
const signedRerankAccessKeyPlaceholder = computed(() => (
  isVolcengineRerank.value
    ? t('model.editor.volcengine.accessKeyPlaceholder')
    : t('model.editor.lkeap.secretIdPlaceholder')
))
const signedRerankSecretKeyLabel = computed(() => (
  isVolcengineRerank.value
    ? t('model.editor.volcengine.secretKeyLabel')
    : t('model.editor.lkeap.secretKeyLabel')
))
const signedRerankSecretKeyPlaceholder = computed(() => (
  isVolcengineRerank.value
    ? t('model.editor.volcengine.secretKeyPlaceholder')
    : t('model.editor.lkeap.secretKeyPlaceholder')
))
const signedRerankCredentialHint = computed(() => (
  isVolcengineRerank.value
    ? t('model.editor.volcengine.rerankCredentialHint')
    : t('model.editor.lkeap.rerankCredentialHint')
))

// ---- 凭证表单动态渲染（design §6.8：按所选厂商 Credentials spec，切厂商字段组跟随） ----

/** 三槽固定词表（后端 CredentialFieldSpec.Key）：槽位 → 表单字段。 */
const CREDENTIAL_SLOT_FIELDS: Record<string, keyof ModelFormData> = {
  api_key: 'apiKey',
  app_id: 'appId',
  app_secret: 'appSecret',
}
const CREDENTIAL_SLOT_KEYS = Object.keys(CREDENTIAL_SLOT_FIELDS) as ModelCredentialField[]

/** 厂商声明的凭证槽位 spec；能力声明未加载（API 兜底路径）时为 undefined。 */
const providerCredentialSpec = computed(() => activeProviderCaps.value?.credentials)

/**
 * 槽位 label：签名 rerank 专属标注 > spec.label_key > 槽位默认文案。
 * api_key 的"（可选）"后缀随 spec 的必填声明走（2026-09-13 反馈 #2）：
 * required=true → 「API Key」；false → 「API Key（可选）」；spec 未加载
 * （undefined）→ 中性「API Key」，不声称可选。
 */
const credentialLabel = (key: ModelCredentialField, labelKey?: string, required?: boolean): string => {
  if (isSignedRerank.value && key === 'api_key') return signedRerankAccessKeyLabel.value
  if (isSignedRerank.value && key === 'app_secret') return signedRerankSecretKeyLabel.value
  if (labelKey && te(labelKey)) return t(labelKey)
  if (key === 'app_id') return t('model.editor.appIdLabel')
  if (key === 'app_secret') return t('model.editor.appSecretLabel')
  if (required === false) return t('model.editor.apiKeyOptional')
  return t('model.credentials.apiKey')
}

const credentialFields = computed<CredentialFieldDef<ModelCredentialField>[]>(() => {
  const spec = providerCredentialSpec.value
  if (!spec) {
    // 兜底：能力声明不可用时维持 v1 字段组
    const fields: CredentialFieldDef<ModelCredentialField>[] = [
      { key: 'api_key', label: credentialLabel('api_key') },
    ]
    if (isSignedRerank.value) {
      fields.push({ key: 'app_secret', label: signedRerankSecretKeyLabel.value as string })
    }
    return fields
  }
  const fields = spec
    .filter(f => (CREDENTIAL_SLOT_KEYS as string[]).includes(f.key))
    .map(f => ({
      key: f.key as ModelCredentialField,
      label: credentialLabel(f.key as ModelCredentialField, f.label_key, f.required),
    }))
  return fields
})

/** spec 为权威可见性来源（weknoracloud spec=[] → 整块隐藏）；spec 未加载时沿用 v1 判断。 */
const credentialBlockVisible = computed(() => {
  if (formData.value.source !== 'remote') return false
  if (providerCredentialSpec.value) return credentialFields.value.length > 0
  return formData.value.provider !== 'weknoracloud'
})

const isCredentialRequired = (key: string): boolean => {
  // 签名 rerank（lkeap/volcengine）：app_secret 在 rerank 调用侧必填
  //（SecretId/SecretKey 缺一即构造失败）；spec 是 provider 级声明、无类型
  // 维度——类型相关的必填在这里补齐（2026-09-14 裁定）。
  if (key === 'app_secret' && isSignedRerank.value) return true
  return providerCredentialSpec.value?.find(f => f.key === key)?.required === true
}

const credentialValue = (key: string) =>
  formData.value[CREDENTIAL_SLOT_FIELDS[key]] as string | undefined ?? ''

const setCredentialValue = (key: string, value: string) => {
  ;(formData.value as any)[CREDENTIAL_SLOT_FIELDS[key]] = value
}

const credentialPlaceholder = (key: string) => {
  if (key === 'api_key' && isSignedRerank.value) return signedRerankAccessKeyPlaceholder.value
  if (key === 'app_secret' && isSignedRerank.value) return signedRerankSecretKeyPlaceholder.value
  return t('model.editor.apiKeyPlaceholder')
}

// Create 模式密码输入的明文预览开关（按槽位记忆，关抽屉时清空）。
const revealedCredentialKeys = ref<Set<string>>(new Set())
const toggleCredentialReveal = (key: string) => {
  const next = new Set(revealedCredentialKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  revealedCredentialKeys.value = next
}

const credentialApi = computed<CredentialResourceApi<ModelCredentialField>>(() => {
  const id = props.modelData?.id ?? ''
  return {
    save: async (patch) => {
      const meta = await putModelCredentials(id, patch)
      return meta.fields
    },
    remove: async (field) => {
      await deleteModelCredentialField(id, field)
    },
  }
})

// Initial credential metadata. ModelSettings.convertToLegacyFormat
// preserves `credentials` from the main ListModels response so the card
// renders the correct "Configured" state on dialog open.
const credentialMeta = computed(() => {
  const stored = (props.modelData as any)?.credentials ?? {}
  const meta: Record<string, { configured: boolean }> = {}
  for (const field of credentialFields.value) {
    meta[field.key] = stored[field.key] ?? { configured: false }
  }
  return meta
})

const formRef = ref()
const saving = ref(false)
// Create 模式明文预览开关 revealedCredentialKeys 声明见凭证动态渲染区；
// 每次抽屉关闭时重置（见 visible watcher 的 reset 块），避免跨编辑会话残留。
const modelChecked = ref(false)
const modelAvailable = ref(false)
const checking = ref(false)
const remoteChecked = ref(false)
const remoteAvailable = ref(false)
const remoteMessage = ref('')
const dimensionChecked = ref(false)
const dimensionSuccess = ref(false)
const dimensionMessage = ref('')

// Ollama 模型状态
const ollamaModelList = ref<OllamaModelInfo[]>([])
const loadingOllamaModels = ref(false)
const searchKeyword = ref('')
const downloading = ref(false)
const downloadProgress = ref(0)
const currentDownloadModel = ref('')
let downloadInterval: any = null

// Ollama 服务状态
const ollamaServiceStatus = ref<boolean | null>(null)
const checkingOllamaStatus = ref(false)

// WeKnoraCloud 凭证状态
const wkcCredentialState = ref<'loading' | 'unconfigured' | 'configured' | 'expired'>('loading')

const checkWkcCredentialStatus = async () => {
  wkcCredentialState.value = 'loading'
  try {
    const status = await getWeKnoraCloudStatus()
    if (status.needs_reinit) {
      wkcCredentialState.value = 'expired'
    } else if (status.has_models) {
      wkcCredentialState.value = 'configured'
    } else {
      wkcCredentialState.value = 'unconfigured'
    }
  } catch {
    wkcCredentialState.value = 'unconfigured'
  }
}

const goToWeKnoraCloudSettings = async () => {
  emit('update:visible', false)
  if (uiStore.showSettingsModal) {
    uiStore.closeSettings()
    await nextTick()
  }
  uiStore.openSettings('weknoracloud')
}

const formData = ref<ModelFormData>({
  id: '',
  name: '',
  source: 'remote',
  provider: 'generic',
  modelName: '',
  displayName: '',
  baseUrl: '',
  apiKey: '',
  appId: '',
  dimension: undefined,
  supportsDimensionOverride: false,
  interfaceType: 'ollama',
  isDefault: false,
  supportsVision: false,
  contextWindow: undefined,
  maxConcurrency: undefined,
  maxOutputTokens: undefined,
  thinkingEnabled: undefined,
  thinkingLevel: '',
  selectedLevels: [],
  inputModalities: ['text'],
  customHeaders: [],
  appSecret: '',
  lkeapRegion: 'ap-guangzhou',
})

// 获取弹窗描述文字
const getModalDescription = () => {
  const key = `model.editor.description.${activeModelType.value}` as const
  return t(key) || t('model.editor.description.default')
}

// 获取模型名称占位符
const getModelNamePlaceholder = () => {
  if (activeModelType.value === 'vllm') {
    return formData.value.source === 'local'
      ? t('model.editor.modelNamePlaceholder.localVllm')
      : t('model.editor.modelNamePlaceholder.remoteVllm')
  }
  if (activeModelType.value === 'asr') {
    return t('model.editor.modelNamePlaceholder.remoteAsr')
  }
  return formData.value.source === 'local'
    ? t('model.editor.modelNamePlaceholder.local')
    : t('model.editor.modelNamePlaceholder.remote')
}

const getBaseUrlPlaceholder = () => {
  if (activeModelType.value === 'vllm') {
    return t('model.editor.baseUrlPlaceholderVllm')
  }
  if (activeModelType.value === 'asr') {
    return t('model.editor.baseUrlPlaceholderAsr')
  }
  return t('model.editor.baseUrlPlaceholder')
}

// 检查Ollama服务状态
const checkOllamaServiceStatus = async () => {
  console.log('开始检查Ollama服务状态...')
  checkingOllamaStatus.value = true
  try {
    const result = await checkOllamaStatus()
    ollamaServiceStatus.value = result.available
    console.log('Ollama服务状态检查完成:', result.available)
  } catch (error) {
    console.error('检查Ollama服务状态失败:', error)
    ollamaServiceStatus.value = false
  } finally {
    checkingOllamaStatus.value = false
  }

  // Ollama 不可用时，新增场景下默认切换到 remote
  if (ollamaServiceStatus.value === false && !isEdit.value && formData.value.source === 'local') {
    formData.value.source = 'remote'
  }
}

// 打开Ollama设置窗口
const goToOllamaSettings = async () => {
  console.log('点击跳转到Ollama设置按钮')
  // 关闭当前弹窗
  emit('update:visible', false)

  // 先关闭设置弹窗（如果已打开）
  if (uiStore.showSettingsModal) {
    uiStore.closeSettings()
    // 等待 DOM 更新
    await nextTick()
  }

  // 打开设置窗口并直接跳转到Ollama设置
  console.log('调用uiStore.openSettings')
  uiStore.openSettings('ollama')
  console.log('uiStore.openSettings调用完成')
}

// 上一次打开时的 modelData id：用来判断切换模型/新增 vs. 同一次新增的连续打开
const lastOpenedModelId = ref<string | null>(null)

const selectModelType = async (type: EditorModelType) => {
  if (isEdit.value || draftModelType.value === type) return
  draftModelType.value = type

  if (type === 'rerank') {
    formData.value.source = 'remote'
  }
  if (type !== 'embedding') {
    formData.value.dimension = undefined
    formData.value.supportsDimensionOverride = false
    dimensionChecked.value = false
    dimensionSuccess.value = false
    dimensionMessage.value = ''
  }
  if (type !== 'chat') {
    formData.value.supportsVision = false
  }
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''

  await loadProviders()
  const supported = providerOptions.value.some(p => p.value === formData.value.provider)
  if (!supported) {
    formData.value.provider = 'generic'
    formData.value.baseUrl = ''
  } else {
    handleProviderChange(formData.value.provider || 'generic')
  }
}

// 监听 visible 变化，初始化表单
watch(() => props.visible, (val) => {
  if (val) {
    // 检查Ollama服务状态
    checkOllamaServiceStatus()

    // 从 API 加载 Model Provider 列表
    loadProviders()

    // 每次打开都清理上一次遗留的校验/检测结果，避免编辑别的模型时
    // 直接显示上一次的“连接成功”
    modelChecked.value = false
    modelAvailable.value = false
    remoteChecked.value = false
    remoteAvailable.value = false
    remoteMessage.value = ''
    dimensionChecked.value = false
    dimensionSuccess.value = false
    dimensionMessage.value = ''
    // 编辑路径不走 resetForm，上一会话探测出的远端模型列表会跨会话残留，
    // 用户可能选中错误厂商的模型 ID（2026-09-13 审查）
    remoteModels.value = []
    probingRemoteModels.value = false

    const currentId = props.modelData?.id ?? null
    draftModelType.value = props.modelType

    hydratingForm.value = true
    try {
      if (props.modelData) {
        // 编辑：始终用最新的 modelData 覆盖。apiKey field is left blank — in
        // edit mode the credential is owned by the <CredentialResource> card,
        // not by this form's apiKey field.
        formData.value = {
          ...props.modelData,
          apiKey: '',
          appId: '',
          appSecret: '',
          customHeaders: Array.isArray(props.modelData.customHeaders)
            ? props.modelData.customHeaders.map(h => ({ key: h.key, value: h.value }))
            : [],
          inputModalities: props.modelData.inputModalities
            ?? (props.modelData.supportsVision ? ['text', 'image'] : ['text']),
        }
        // 已存值优先：预填不覆盖（D1 保存即终态）；换模型 ID 时解除
        prefillBlocked.value = true
      } else if (lastOpenedModelId.value !== null || !formData.value.id) {
        // 上次是编辑某个模型，或第一次新增 → 重置成空白
        resetForm()
      }
      // 否则：连续两次"新增"打开（中间是点遮罩/ESC 关闭的）→ 保留上次填写

      lastOpenedModelId.value = currentId

      // 如果当前 provider 是 WeKnoraCloud，检查凭证状态
      if (formData.value.provider === 'weknoracloud') {
        checkWkcCredentialStatus()
      }

    } finally {
      nextTick(() => {
        hydratingForm.value = false
      })
    }
  }
})

// 「模型来源」控件已退役（2026-09-13 裁定）：ollama 即本地、其余即远程。
const isOllamaProvider = computed(() => formData.value.provider === 'ollama')

// source 派生自 provider（含灌入期——存量 local 记录若 provider 不一致会被
// 纠正为派生值，payload 出口再兜底一次）。
watch(() => formData.value.provider, (p) => {
  formData.value.source = p === 'ollama' ? 'local' : 'remote'
})

// 重置表单
const resetForm = () => {
  formData.value = {
    id: generateId(),
    name: '', // 保留字段但不使用，保存时用 modelName
    source: 'remote',
    provider: 'generic',
    modelName: '',
    displayName: '',
    baseUrl: '',
    apiKey: '',
    appId: '',
    dimension: undefined, // 默认不填，让用户手动输入或通过检测按钮获取
    supportsDimensionOverride: false,
    interfaceType: undefined,
    isDefault: false,
    supportsVision: false,
    contextWindow: undefined,
    maxConcurrency: undefined,
    maxOutputTokens: undefined,
    thinkingEnabled: undefined,
    thinkingLevel: '',
    selectedLevels: [],
    inputModalities: ['text'],
    customHeaders: [],
    appSecret: '',
    lkeapRegion: 'ap-guangzhou',
  }
  modelChecked.value = false
  modelAvailable.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
  revealedCredentialKeys.value = new Set()
  // 预填/探测状态一并清空
  manualFields.value = new Set()
  prefillSource.value = {}
  prefillBlocked.value = false
  remoteModels.value = []
  probingRemoteModels.value = false
}
// 处理厂商选择变化 (自动填充默认 URL)
const handleProviderChange = (value: string) => {
  const provider = providerOptions.value.find(opt => opt.value === value)
  if (provider && provider.defaultUrls) {
    // 根据当前模型类型获取对应的默认 URL；无默认值时清空——残留上一家
    // 厂商的地址会把 A 家的 key 发到 B 家的域（2026-09-14 裁定）
    const defaultUrl = provider.defaultUrls[activeModelType.value]
    if (defaultUrl) {
      formData.value.baseUrl = defaultUrl
    } else if (value !== 'weknoracloud') {
      formData.value.baseUrl = ''
    }
    // 动态扩展字段默认值预填（如 azure api_version 2024-10-21）
    for (const f of provider.extraFields ?? []) {
      if (f.default && !formData.value.extraConfig?.[f.key]) {
        if (!formData.value.extraConfig) formData.value.extraConfig = {}
        formData.value.extraConfig[f.key] = f.default
      }
    }
    if (value === 'lkeap' && activeModelType.value === 'rerank' && !formData.value.modelName?.trim()) {
      formData.value.modelName = 'lke-reranker-base'
    }
    if (value === 'volcengine' && activeModelType.value === 'rerank' && !formData.value.modelName?.trim()) {
      formData.value.modelName = 'doubao-seed-rerank'
    }
    // 重置校验状态
    remoteChecked.value = false
    remoteAvailable.value = false
    remoteMessage.value = ''
  }
  // WeKnoraCloud: 检查凭证状态
  if (value === 'weknoracloud') {
    checkWkcCredentialStatus()
  }
}

let probeTimer: ReturnType<typeof setTimeout> | null = null

watch(
  () => [formData.value.source, formData.value.provider, formData.value.modelName, activeModelType.value,
    formData.value.baseUrl, formData.value.apiKey] as const,
  ([source, provider, modelName, modelType, baseUrl, apiKey],
    [prevSource, prevProvider, prevModelName, prevModelType, prevBaseUrl, prevApiKey]) => {
    if (hydratingForm.value) return
    if (source === prevSource && provider === prevProvider && modelName === prevModelName
      && modelType === prevModelType && baseUrl === prevBaseUrl && apiKey === prevApiKey) return

    // 远端模型列表探测（防抖）：新建用已填 base_url+api_key，编辑用存储凭证；
    // 切换模型类型同样重探（裁定③：列表按当前编辑类型过滤）
    if (showRemoteModelSelect.value) {
      if (probeTimer) clearTimeout(probeTimer)
      probeTimer = setTimeout(() => { void probeRemoteModels() }, 500)
    }

    // 换模型 ID = 新选择：按取值链重新预填（D1 推论）；仅换来源/厂商不触发
    if (modelName !== prevModelName) {
      prefillBlocked.value = false
      void prefillForModelId(modelName)
    }
  },
)

// 厂商 caps 晚到（/models/providers 异步返回常慢于探测/目录）：思考区翻真
// 时重跑一次预填——applyPrefill 的 manualFields 守卫保证不覆盖手动值
// （2026-09-13 审查：此前预填只在 modelName 变化时执行一次，慢网下静默丢失）。
// 注意注册位置：watch 源在注册时立即求值一次，任何（传递性）读取 formData
// 的 watch 必须放在 const formData 声明之后——放早了会在 setup 期 TDZ 崩溃
// （2026-09-13 真机回归："添加模型"打不开，Cannot access 'formData' before
// initialization；最初插在 prefillForModelId 之后、formData 声明之前）。
watch(showThinkingSection, (supported) => {
  if (supported && !hydratingForm.value) {
    void prefillForModelId(formData.value.modelName)
  }
})

// 监听来源变化，重置校验状态（已合并到下面的 watch）

// 生成唯一ID
const generateId = () => {
  return `model_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
}

// 自定义 HTTP Header 编辑
const addCustomHeader = () => {
  if (!Array.isArray(formData.value.customHeaders)) {
    formData.value.customHeaders = []
  }
  formData.value.customHeaders.push({ key: '', value: '' })
}

const removeCustomHeader = (idx: number) => {
  if (!Array.isArray(formData.value.customHeaders)) return
  formData.value.customHeaders.splice(idx, 1)
}

// 过滤后的模型列表
const filteredOllamaModels = computed(() => {
  if (!searchKeyword.value) return ollamaModelList.value
  return ollamaModelList.value.filter(model =>
    model.name.toLowerCase().includes(searchKeyword.value.toLowerCase())
  )
})

// 是否显示"下载模型"选项
const showDownloadOption = computed(() => {
  if (!searchKeyword.value.trim()) return false
  // 检查搜索词是否已存在于模型列表中
  const exists = ollamaModelList.value.some(model =>
    model.name.toLowerCase() === searchKeyword.value.toLowerCase()
  )
  return !exists
})

// 自定义过滤逻辑（捕获搜索关键词）
const handleModelFilter = (filterWords: string) => {
  searchKeyword.value = filterWords
  return true // 让 TDesign 使用我们的 filteredOllamaModels
}

// 加载 Ollama 模型列表
const loadOllamaModels = async () => {
  // 只在选择 local 来源时加载
  if (formData.value.source !== 'local') return

  loadingOllamaModels.value = true
  try {
    const models = await listOllamaModels()
    ollamaModelList.value = models
  } catch (error) {
    console.error(t('model.editor.loadModelListFailed'), error)
    MessagePlugin.error(t('model.editor.loadModelListFailed'))
  } finally {
    loadingOllamaModels.value = false
  }
}

// 刷新模型列表
const refreshOllamaModels = async () => {
  ollamaModelList.value = [] // 清空以强制重新加载
  await loadOllamaModels()
  MessagePlugin.success(t('model.editor.listRefreshed'))
}

// 监听下拉框可见性变化
const handleDropdownVisibleChange = (visible: boolean) => {
  if (!visible) {
    searchKeyword.value = ''
  }
}

// 格式化模型大小
const formatModelSize = (bytes: number): string => {
  if (!bytes || bytes === 0) return ''
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}

// 检查模型状态（Ollama本地模型）
const checkModelStatus = async () => {
  if (!formData.value.modelName || formData.value.source !== 'local') {
    return
  }

  try {
    // 调用真实 Ollama API 检查模型是否存在
    const result = await checkOllamaModels([formData.value.modelName])
    modelChecked.value = true
    modelAvailable.value = result.models[formData.value.modelName] || false
  } catch (error) {
    console.error('检查模型状态失败:', error)
    modelChecked.value = false
    modelAvailable.value = false
  }
}

// 检查 Ollama 本地 Embedding 模型维度
const checkOllamaDimension = async () => {
  if (!formData.value.modelName || formData.value.source !== 'local' || activeModelType.value !== 'embedding') {
    return
  }

  checking.value = true
  dimensionChecked.value = false
  dimensionMessage.value = ''

  try {
    const result = await testEmbeddingModel({
      source: 'local',
      modelName: formData.value.modelName,
      dimension: formData.value.dimension,
      supportsDimensionOverride: formData.value.supportsDimensionOverride ?? false,
    })

    dimensionChecked.value = true
    dimensionSuccess.value = result.available || false

    if (result.available && result.dimension) {
      formData.value.dimension = result.dimension
      dimensionMessage.value = t('model.editor.dimensionDetected', { value: result.dimension })
      MessagePlugin.success(dimensionMessage.value)
    } else {
      if (result.message) {
        console.debug('Backend dimension message:', result.message)
      }
      dimensionMessage.value = t('model.editor.dimensionFailed')
      MessagePlugin.warning(dimensionMessage.value)
    }
  } catch (error: any) {
    console.error('Ollama dimension check failed:', error)
    dimensionChecked.value = true
    dimensionSuccess.value = false
    dimensionMessage.value = t('model.editor.dimensionFailed')
    MessagePlugin.error(dimensionMessage.value)
  } finally {
    checking.value = false
  }
}

// 检查 Remote API 连接（根据模型类型调用不同的接口）
const checkRemoteAPI = async () => {
  if (!formData.value.modelName || (!formData.value.baseUrl && formData.value.provider !== 'weknoracloud')) {
    MessagePlugin.warning(t('model.editor.fillModelAndUrl'))
    return
  }

  checking.value = true
  remoteChecked.value = false
  remoteMessage.value = ''

  try {
    let result: any

    // 把表单里 Key-Value 数组形式的自定义 Header 转成后端期望的 map。
    // 跟 ModelSettings.vue 保存时一致，空行自动丢弃，保证测试连接与真正保存后的
    // 生产调用使用完全相同的 Header 集合。
    const customHeaders: Record<string, string> = {}
    if (Array.isArray(formData.value.customHeaders)) {
      for (const item of formData.value.customHeaders) {
        const key = (item?.key ?? '').trim()
        const value = (item?.value ?? '').trim()
        if (key && value) customHeaders[key] = value
      }
    }
    // 只在非空时带上字段，避免在 URL query / 日志里出现空对象
    const headerPayload = Object.keys(customHeaders).length > 0
      ? { customHeaders }
      : {}

    // 根据模型类型调用不同的校验接口
    // 编辑模式下 apiKey 由 <CredentialResource> 独立管理、不在 formData 里。
    // 把 modelId 透传给后端，让它在 apiKey 为空时自动用存储的解密值兜底，
    // 避免出现"测试连接没带 apiKey 直接失败"的情况。
    const idPayload = isEdit.value && props.modelData?.id
      ? { modelId: props.modelData.id as string }
      : {}

    switch (activeModelType.value) {
      case 'chat':
        // 对话模型（KnowledgeQA）
        result = await checkRemoteModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          // azure api_version 等动态扩展字段随测试连接透传
          ...(formData.value.extraConfig && Object.keys(formData.value.extraConfig).length > 0
            ? { extraConfig: { ...formData.value.extraConfig } }
            : {}),
        })
        break

      case 'embedding':
        // Embedding 模型
        result = await testEmbeddingModel({
          source: 'remote',
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          dimension: formData.value.dimension,
          supportsDimensionOverride: formData.value.supportsDimensionOverride ?? false,
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
        })
        // 如果测试成功且返回了维度，自动填充
        if (result.available && result.dimension) {
          formData.value.dimension = result.dimension
          MessagePlugin.info(t('model.editor.remoteDimensionDetected', { value: result.dimension }))
        }
        break

      case 'rerank': {
        const signedRerankExtra = isSignedRerank.value
          ? {
              ...(isLkeapRerank.value
                ? {
                    extraConfig: {
                      region: (formData.value.lkeapRegion || 'ap-guangzhou').trim(),
                    },
                  }
                : {}),
              ...(formData.value.appSecret?.trim()
                ? { appSecret: formData.value.appSecret.trim() }
                : {}),
            }
          : {}
        result = await checkRerankModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
          ...signedRerankExtra,
        })
        break
      }

      case 'vllm':
        // VLLM 模型（多模态）
        // VLLM 使用 checkRemoteModel 进行基础连接测试
        result = await checkRemoteModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
        })
        break

      case 'asr':
        // ASR 模型（语音识别）— 使用专用的 ASR 测试接口（/v1/audio/transcriptions）
        result = await checkASRModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl || '',
          apiKey: formData.value.apiKey || '',
          provider: formData.value.provider,
          ...idPayload,
          ...headerPayload,
        })
        break

      default:
        MessagePlugin.error(t('model.editor.unsupportedModelType'))
        return
    }

    remoteChecked.value = true
    remoteAvailable.value = result.available || false
    // 之前这里把 backend 的错误 message 只丢到 console.debug，用户只能
    // 看到通用的 "连接失败" toast，根本看不出是 401 / 404 / 模型不存在
    // 还是别的什么。改成：成功时用 i18n 通用提示；失败时直接展示后端
    // 给到的具体原因（已经在后端 classifyConnectionError 中包了一层
    // 易读的中文 hint + 原始 SDK 报错），方便排查。
    if (result.available) {
      remoteMessage.value = t('model.editor.connectionSuccess')
      MessagePlugin.success(remoteMessage.value)
    } else {
      remoteMessage.value = result.message || t('model.editor.connectionFailed')
      console.debug('Backend message:', result.message)
      MessagePlugin.error(remoteMessage.value)
    }
  } catch (error: any) {
    console.error('Remote API check failed:', error)
    remoteChecked.value = true
    remoteAvailable.value = false
    // 后端 4xx/5xx（如 SSRF 校验失败）会走到这里。axios 拦截器把后端
    // { error: { message: "..." } } 提到了 error.message，里面已经包含
    // 易读 hint + 原因，直接展示出来，比通用 "请检查配置" 有用得多。
    remoteMessage.value = error?.message || t('model.editor.connectionConfigError')
    MessagePlugin.error(remoteMessage.value)
  } finally {
    checking.value = false
  }
}

// 确认保存
const handleConfirm = async () => {
  try {
    // 手动校验必填字段
    if (!formData.value.modelName || !formData.value.modelName.trim()) {
      MessagePlugin.warning(t('model.editor.validation.modelNameRequired'))
      return
    }

    if (formData.value.modelName.trim().length > 100) {
      MessagePlugin.warning(t('model.editor.validation.modelNameMax'))
      return
    }

    // 如果是 remote 类型且非 WeKnoraCloud，必须填写 baseUrl
    if (formData.value.source === 'remote' && formData.value.provider !== 'weknoracloud') {
      if (!formData.value.baseUrl || !formData.value.baseUrl.trim()) {
        MessagePlugin.warning(t('model.editor.remoteBaseUrlRequired'))
        return
      }

      // 校验 Base URL 格式
      try {
        new URL(formData.value.baseUrl.trim())
      } catch {
        MessagePlugin.warning(t('model.editor.validation.baseUrlInvalid'))
        return
      }
    }

    // 创建模式：spec 标记 required 的凭证槽位必须填写（编辑模式凭证由
    // CredentialResource 独立管理，不在此校验）。
    if (!isEdit.value && credentialBlockVisible.value) {
      const missing = credentialFields.value.find(
        f => isCredentialRequired(f.key) && !credentialValue(f.key).trim(),
      )
      if (missing) {
        MessagePlugin.warning(
          t('model.editor.validation.credentialRequired', { field: missing.label }),
        )
        return
      }
    }

    // 数字字段显式校验（2026-09-13 审查）：t-input 的 :min/:max 只约束步进
    // 箭头、挡不住键入——越界值此前被父组件静默丢弃（卡片显示默认值）或
    // 原样发往后端。字段留空仍交给父组件的门控语义。
    // 运行时形态是 number | ''（清空输入）| undefined，统一按 unknown 比较
    const cw = formData.value.contextWindow as unknown
    if (cw !== '' && cw != null && (Number.isNaN(Number(cw)) || Number(cw) < 1024 || Number(cw) > 10000000)) {
      MessagePlugin.warning(t('model.editor.validation.contextWindowRange'))
      return
    }
    const mo = formData.value.maxOutputTokens as unknown
    if (mo !== '' && mo != null && (Number.isNaN(Number(mo)) || Number(mo) < 1)) {
      MessagePlugin.warning(t('model.editor.validation.maxOutputTokensRange'))
      return
    }
    const mc = formData.value.maxConcurrency as unknown
    if (mc !== '' && mc != null && (Number.isNaN(Number(mc)) || Number(mc) < 0)) {
      MessagePlugin.warning(t('model.editor.validation.maxConcurrencyRange'))
      return
    }

    // Credential removal in edit mode is handled inline by the
    // CredentialResource card (it confirms + DELETEs to /credentials), so
    // the main save flow no longer needs to confirm or handle clear flags.

    saving.value = true

    // 如果是新增且没有 id，生成一个
    if (!formData.value.id) {
      formData.value.id = generateId()
    }

    emit('confirm', {
      ...formData.value,
      source: isOllamaProvider.value ? 'local' : 'remote',
      ...(isEdit.value ? {} : { modelType: activeModelType.value }),
    })
    // D1（2026-09-14 裁定）：保存失败不关抽屉——关闭与重置改由父组件在
    // 保存成功后调用 resetAfterSave()（此前 emit 后无条件关闭，校验/API
    // 失败时用户输入全丢）。
    // 移除此处的成功提示，由父组件统一处理
  } catch (error) {
    console.error('表单验证失败:', error)
  } finally {
    saving.value = false
  }
}

// 监听模型选择变化（处理下载逻辑和自动维度检测提示）
watch(() => formData.value.modelName, async (newValue, oldValue) => {
  if (hydratingForm.value) return // 编辑打开的灌入不触发下载/维度提示副作用
  if (!newValue) return

  // 处理下载逻辑
  if (newValue.startsWith('__download__')) {
    // 提取模型名称
    const modelName = newValue.replace('__download__', '')

    // 重置选择（避免显示 __download__ 前缀）
    formData.value.modelName = ''

    // 开始下载
    await startDownload(modelName)
    return
  }

  // 如果是 embedding 模型且选择的是 Ollama 本地模型，且模型名称发生了实际变化
  if (activeModelType.value === 'embedding' &&
    formData.value.source === 'local' &&
    newValue !== oldValue &&
    oldValue !== '') {
    // 提示用户可以检测维度
    MessagePlugin.info(t('model.editor.dimensionHint'))
  }
})

// 开始下载模型
const startDownload = async (modelName: string) => {
  downloading.value = true
  downloadProgress.value = 0
  currentDownloadModel.value = modelName

  try {
    // 启动下载
    const result = await downloadOllamaModel(modelName)
    const taskId = result.taskId

    MessagePlugin.success(t('model.editor.downloadStarted', { name: modelName }))

    // 轮询下载进度
    downloadInterval = setInterval(async () => {
      try {
        const progress = await getDownloadProgress(taskId)
        downloadProgress.value = progress.progress

        if (progress.status === 'completed') {
          // 下载完成
          clearInterval(downloadInterval)
          downloadInterval = null
          downloading.value = false

          MessagePlugin.success(t('model.editor.downloadCompleted', { name: modelName }))

          // 刷新模型列表
          await loadOllamaModels()

          // 自动选中新下载的模型——仅当抽屉仍打开且还在 local 新建流程：
          // ESC/遮罩关闭不清理下载轮询，完成后若用户已改开编辑表单，这里
          // 的无条件回填会把别人家表单的 modelName 写坏（2026-09-13 审查）。
          if (props.visible && formData.value.source === 'local' && !isEdit.value) {
            formData.value.modelName = modelName
          }

          // 重置状态
          downloadProgress.value = 0
          currentDownloadModel.value = ''

        } else if (progress.status === 'failed') {
          // 下载失败
          clearInterval(downloadInterval)
          downloadInterval = null
          downloading.value = false
          MessagePlugin.error(progress.message || t('model.editor.downloadFailed', { name: modelName }))
          downloadProgress.value = 0
          currentDownloadModel.value = ''
        }
      } catch (error) {
        console.error('获取下载进度失败:', error)
      }
    }, 1000) // 每秒查询一次

  } catch (error: any) {
    downloading.value = false
    downloadProgress.value = 0
    currentDownloadModel.value = ''
    console.error('Download start failed:', error)
    MessagePlugin.error(t('model.editor.downloadStartFailed'))
  }
}

// 组件卸载时清理定时器
onUnmounted(() => {
  if (downloadInterval) {
    clearInterval(downloadInterval)
  }
})

// 监听来源变化，清理所有状态
watch(() => formData.value.source, () => {
  // 重置校验状态
  modelChecked.value = false
  modelAvailable.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''

  // 清理下载状态
  searchKeyword.value = ''
  if (downloadInterval) {
    clearInterval(downloadInterval)
    downloadInterval = null
  }
  downloading.value = false
  downloadProgress.value = 0
  currentDownloadModel.value = ''

})

// 监听模型名称变化，清理维度检测状态
watch(() => formData.value.modelName, () => {
  if (hydratingForm.value) return // 编辑打开的灌入不是用户输入
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
})

// 取消（点击底部"取消"按钮触发；点遮罩/ESC 不触发，从而保留草稿）
const handleCancel = () => {
  resetForm()
  lastOpenedModelId.value = null
  dialogVisible.value = false
}

/**
 * D1 契约：父组件在保存成功（createModel/updateModel resolve）后调用。
 * 关抽屉 + 清草稿；失败路径不调用 → 抽屉保持打开、输入保留。
 */
const resetAfterSave = () => {
  dialogVisible.value = false
  resetForm()
  lastOpenedModelId.value = null
}
defineExpose({ resetAfterSave })
</script>

<style lang="less" scoped>
// 原生 t-form-item 容器置空（本组件使用自定义 .form-item + 手写 label）
:deep(.t-form) {
  .t-form-item {
    display: none;
  }
}

// 表单项样式
.form-item {
  // No bottom margin — vertical rhythm is owned by the parent
  // .setting-drawer__section's `gap`. That keeps the spacing inside a section
  // tight and the gap between sections visually distinct.
  margin-bottom: 0;
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  // TDesign-style required marker: leading asterisk before the label text,
  // matching the rest of the app's <t-form-item required ...> appearance.
  &.required::before {
    content: '*';
    color: var(--td-error-color);
    margin-right: 4px;
    font-weight: 500;
    line-height: 1;
  }
}

.model-type-options {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.model-type-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  min-height: 32px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.4;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease, background 0.15s ease;

  &__icon {
    font-size: 15px;
    flex-shrink: 0;
  }

  &__label {
    white-space: nowrap;
  }

  &:hover:not(.is-active) {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    color: var(--td-text-color-primary);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color);
    font-weight: 500;
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }
}

// 模型来源分段：紧凑单行 pill 形 segmented。容器自身是浅底圆角条，
// 选中按钮通过实色背景 + 主题色描边浮出，未选中态接近透明，节省纵向空间。
.source-options {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px;
  background: var(--td-bg-color-component);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

.source-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  height: 28px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1;
  transition: all 0.15s ease;

  &:hover:not(.is-disabled):not(.is-active) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    background: var(--td-bg-color-container);
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    font-weight: 500;
    box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  }

  &.is-disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
}

.source-option__icon {
  font-size: 14px;
  flex-shrink: 0;
}

.source-option__label {
  white-space: nowrap;
}

// 输入框样式：只在最外层 .t-input 上调字号，避免在内部 wrap/inner 上重复加边
// 与 border-radius，造成视觉上"嵌套圆角容器"的错觉
:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea),
:deep(.t-input-number) {
  width: 100%;
  font-size: 13px;
}

// 厂商选择器样式 — 移至非 scoped 块，因为 t-select popup 渲染到 body 下
// .provider-option 样式见文件末尾

// 复选框
:deep(.t-checkbox) {
  font-size: 13px;

  .t-checkbox__label {
    font-size: 13px;
    color: var(--td-text-color-primary);
  }
}

// API Key 输入：前置 lock 图标 + 后置可点击的"显示/隐藏"小眼睛。
// TDesign 默认会让 prefix-icon 显示成灰色，这里没动；suffix 上的眼睛
// 用 placeholder 色，hover 时切到主文本色，避免抢戏。
.api-key-input {
  :deep(.t-input__prefix) {
    color: var(--td-text-color-placeholder);
  }

  :deep(.t-input__suffix) {
    color: var(--td-text-color-placeholder);
  }

  .api-key-toggle {
    cursor: pointer;
    transition: color 0.15s ease;
    font-size: 16px;

    &:hover {
      color: var(--td-text-color-primary);
    }
  }
}

// API 测试区域 — 弱卡片化：用浅底 + dashed 边把"操作 + 反馈"框成一块，
// 让用户视觉上把它当成一个独立的"动作单元"，而不是又一个普通字段。
// （历史样式保留：仅当某个分支仍以 inline 方式渲染测试块时使用；当前 RemoteAPI
// 测试已上移到 SettingDrawer footer-left 槽，主流程不再走这块。）
.api-test-section {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  background: var(--td-bg-color-container-hover);
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;

  .test-message {
    font-size: 13px;
    line-height: 1.5;
    flex: 1;

    &.success {
      color: var(--td-brand-color-active);
    }

    &.error {
      color: var(--td-error-color);
    }
  }

  :deep(.t-button) {
    min-width: 88px;
    height: 32px;
    font-size: 13px;
    border-radius: 6px;
    flex-shrink: 0;
  }

  .status-icon {
    font-size: 16px;
    flex-shrink: 0;

    &.available {
      color: var(--td-brand-color);
    }

    &.unavailable {
      color: var(--td-error-color);
    }
  }
}

// Connection-test message rendered next to the test button in the drawer
// footer. Truncates with ellipsis so a long backend error doesn't push
// Save/Cancel off-screen — the full text is in the title attribute.
.footer-test-message {
  font-size: 12px;
  line-height: 1.4;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  &.success {
    color: var(--td-brand-color-active);
  }

  &.error {
    color: var(--td-error-color);
  }
}

// Status icon variant used inside the footer button.
.status-icon {
  font-size: 16px;
  flex-shrink: 0;

  &.available {
    color: var(--td-brand-color);
  }

  &.unavailable {
    color: var(--td-error-color);
  }
}

// WeKnoraCloud 提示信息
.weknoracloud-hint {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border-radius: 8px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;

  // Theming via tokens so the warn/ok states track light/dark switches
  // instead of fighting hardcoded `#fff7ed` etc.
  &--ok {
    background: var(--td-success-color-light);
    border: 1px solid var(--td-success-color-focus);
  }

  &--warn {
    background: var(--td-warning-color-light, #fff7ed);
    border: 1px solid var(--td-warning-color-focus, #fed7aa);
    border-left: 3px solid var(--td-warning-color, #f97316);
  }

  .hint-icon {
    font-size: 16px;
    flex-shrink: 0;
    margin-top: 2px;

    &--ok {
      color: var(--td-success-color);
    }

    &--warn {
      color: var(--td-warning-color, #f97316);
    }

    &--loading {
      color: var(--td-text-color-placeholder);
    }
  }
}

// Ollama 模型选择器样式
.model-option {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 4px 0;

  .downloaded-icon {
    font-size: 14px;
    color: var(--td-brand-color);
    flex-shrink: 0;
  }

  .download-icon {
    font-size: 14px;
    color: var(--td-brand-color);
    flex-shrink: 0;
  }

  .model-name {
    flex: 1;
    font-size: 13px;
    color: var(--td-text-color-primary);
  }

  .model-size {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    margin-left: auto;
  }

  &.download {
    .model-name {
      color: var(--td-brand-color);
      font-weight: 500;
    }
  }
}

// 下载进度后缀样式
.download-suffix {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 4px;

  .spinning {
    animation: spin 1s linear infinite;
    font-size: 14px;
    color: var(--td-brand-color);
  }

  .progress-text {
    font-size: 12px;
    font-weight: 500;
    color: var(--td-brand-color);
  }
}

// 下载中的选择框进度条效果
:deep(.t-select.downloading) {
  .t-input {
    position: relative;
    overflow: hidden;

    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 0;
      bottom: 0;
      width: var(--progress, 0%);
      background: linear-gradient(90deg, rgba(7, 192, 95, 0.08), rgba(7, 192, 95, 0.15));
      transition: width 0.3s ease;
      z-index: 0;
      border-radius: 5px 0 0 5px;
    }

    .t-input__inner,
    input {
      position: relative;
      z-index: 1;
      background: transparent !important;
    }
  }
}

.model-select-row {
  display: flex;
  align-items: center;
  gap: 8px;

  .t-select {
    flex: 1;
  }
}

.refresh-btn {
  flex-shrink: 0;
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }

  to {
    transform: rotate(360deg);
  }
}

// 维度控制样式
.dimension-control {
  display: flex;
  align-items: center;
  gap: 8px;

  :deep(.t-input) {
    flex: 1;
  }
}

.dimension-check-btn {
  flex-shrink: 0;
}

.dimension-hint {
  margin: 8px 0 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-error-color);

  &.success {
    color: var(--td-brand-color);
  }
}

// 自定义 HTTP Header 区域
.custom-headers-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.custom-headers-desc {
  margin: 0 0 10px 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.custom-headers-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.custom-header-row {
  display: flex;
  align-items: center;
  gap: 8px;

  .custom-header-key {
    flex: 0 0 38%;
  }

  .custom-header-value {
    flex: 1;
  }

  // Ghost icon button — matches the model-card "more" affordance: invisible
  // until hover/focus, then a subtle background pops in. Avoids painting a
  // permanent red splotch next to every header row.
  .custom-header-remove {
    flex-shrink: 0;
    width: 32px;
    height: 32px;
    padding: 0;
    color: var(--td-text-color-placeholder);
    border-radius: 6px;
    transition: all 0.18s ease;

    &:hover {
      background: var(--td-error-color-light);
      color: var(--td-error-color);
    }
  }
}

.form-desc {
  margin: 4px 0 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);

  // Inline with switches/checkboxes — drops the top margin so the label and
  // helper text sit on the same baseline.
  &--inline {
    margin: 0;
  }

  &--recommend {
    color: var(--td-brand-color);
  }

  &--warn {
    color: var(--td-warning-color);
  }
}

.vision-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
}

// Ollama不可用提示样式
.ollama-unavailable-tip {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  padding: 10px 12px;
  background: var(--td-error-color-light);
  border: 1px solid var(--td-error-color-focus);
  border-radius: 8px;
  font-size: 13px;

  .tip-icon {
    color: var(--td-error-color);
    font-size: 16px;
    flex-shrink: 0;
    margin-right: 2px;

    &.info {
      color: var(--td-brand-color);
    }
  }

  .tip-text {
    color: var(--td-error-color);
    flex: 1;
    line-height: 1.5;
  }

  // ReRank提示使用主题绿色风格，与主页面保持一致
  &.rerank-tip {
    background: var(--td-success-color-light);
    border: 1px solid var(--td-success-color-focus);
    border-left: 3px solid var(--td-brand-color);

    .tip-text {
      color: var(--td-success-color);
    }
  }

  :deep(.tip-link) {
    color: var(--td-brand-color);
    font-size: 13px;
    font-weight: 500;
    padding: 4px 6px 4px 10px !important;
    min-height: auto !important;
    height: auto !important;
    line-height: 1.4 !important;
    text-decoration: none;
    white-space: nowrap;
    display: inline-flex !important;
    align-items: center !important;
    gap: 1px;
    border-radius: 4px;
    transition: all 0.2s ease;

    &:hover {
      background: rgba(7, 192, 95, 0.08) !important;
      color: var(--td-brand-color-active) !important;
    }

    &:active {
      background: rgba(7, 192, 95, 0.12) !important;
    }

    .t-icon {
      font-size: 14px !important;
      margin: 0 !important;
      line-height: 1 !important;
      display: inline-flex !important;
      align-items: center !important;
    }
  }
}

// Destructive-action checkbox for "Remove this credential". Styled to match
// the pattern used in McpServiceDialog so the two dialogs read identically.
.clear-credential {
  display: inline-flex;
  margin-top: 8px;

  :deep(.t-checkbox__label) {
    color: var(--td-error-color);
    font-size: 13px;
  }
}

// 预填来源标注（目录 / 接口），跟在字段标签后面
.prefill-source {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 400;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  background-color: var(--td-bg-color-secondarycontainer);
  vertical-align: middle;
}
</style>

<!-- 非 scoped 样式：t-select popup 渲染到 body 下，scoped 样式无法覆盖 -->
<style lang="less">

.provider-select-popup {
  // 容器留点呼吸：避免选项贴着 popup 圆角
  padding: 4px;

  // TDesign 默认会在 t-select-option 上挂一个 overflow tooltip（浮在右侧
  // 显示完整 label）。我们的选项排版是「主名称 + 次描述」两行，永远不会
  // 触发省略，tooltip 反而成了视觉噪音 → 直接隐藏 popup 自带的提示。
  + .t-popup .t-tooltip,
  ~ .t-popup .t-tooltip {
    display: none !important;
  }

  .t-select-option {
    height: auto !important;
    padding: 8px 10px;
    border-radius: 6px;
    margin: 2px 0;
    outline: none;
    transition: background-color 0.15s ease;

    &:focus,
    &:focus-visible {
      outline: none;
    }

    // hover 态：用浅 brand 色而非强灰，跟主题色调一致
    &:hover:not(.t-is-selected) {
      background-color: var(--td-bg-color-container-hover);
    }
  }

  // 命中态：浅一点的底色 + 左侧主题色条作为 affordance，不再用全填的灰底
  .t-select-option.t-is-selected {
    background-color: var(--td-brand-color-light);
    color: var(--td-text-color-primary);
    font-weight: 500;
    position: relative;

    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 8px;
      bottom: 8px;
      width: 3px;
      background: var(--td-brand-color);
      border-radius: 0 2px 2px 0;
    }

    .provider-name {
      color: var(--td-brand-color);
    }
  }

  .provider-option {
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    width: 100%;
    min-width: 0;

    // #15：厂商 LOGO（color 直渲 / mono mask 染色 / 首字母徽章回落）
    .provider-option__logo {
      flex-shrink: 0;
      width: 20px;
      height: 20px;
      object-fit: contain;

      &--mono {
        background-color: currentColor;
        mask-image: var(--logo-url);
        mask-size: contain;
        mask-repeat: no-repeat;
        mask-position: center;
        -webkit-mask-image: var(--logo-url);
        -webkit-mask-size: contain;
        -webkit-mask-repeat: no-repeat;
        -webkit-mask-position: center;
      }

      &--badge {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        border-radius: 5px;
        background: var(--td-bg-color-secondarycontainer, #f0f0f0);
        color: var(--td-text-color-secondary);
        font-size: 11px;
        font-weight: 600;
      }
    }

    .provider-option__text {
      display: flex;
      flex-direction: column;
      gap: 2px;
      min-width: 0;
    }

    .provider-name {
      font-size: 13px;
      font-weight: 500;
      color: var(--td-text-color-primary);
      line-height: 20px;
    }

    .provider-desc {
      font-size: 12px;
      color: var(--td-text-color-placeholder);
      line-height: 18px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
}
</style>
