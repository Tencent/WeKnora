<template>
  <div class="skills-hub-container">
    <div class="skills-hub-content">
      <!-- 头部 -->
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <div class="title-row" style="--wails-draggable: drag">
            <h2 style="--wails-draggable: drag">{{ $t('skills.hub.title') }}</h2>
            <div class="header-actions" style="--wails-draggable: no-drag">
              <t-button variant="text" theme="default" size="small" @click="handleRefresh" :loading="refreshing">
                <template #icon><t-icon name="refresh" /></template>
              </t-button>
              <t-button variant="outline" size="small" @click="showInstallDialog = true">
                <template #icon><t-icon name="add" /></template>
                {{ $t('skills.hub.install') }}
              </t-button>
              <t-button variant="outline" size="small" @click="showUploadDialog = true">
                <template #icon><t-icon name="upload" /></template>
                {{ $t('skills.hub.upload') }}
              </t-button>
            </div>
          </div>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('skills.hub.subtitle') }}</p>
        </div>
      </div>

      <!-- 过滤器 -->
      <div class="filter-bar">
        <t-input
          v-model="searchQuery"
          :placeholder="$t('skills.hub.searchPlaceholder')"
          clearable
          size="small"
          class="search-input"
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-radio-group v-model="sourceFilter" variant="default-filled" size="small">
          <t-radio-button value="all">{{ $t('skills.hub.filterAll') }}</t-radio-button>
          <t-radio-button value="preloaded">{{ $t('skills.hub.filterPreloaded') }}</t-radio-button>
          <t-radio-button value="installed">{{ $t('skills.hub.filterInstalled') }}</t-radio-button>
        </t-radio-group>
      </div>

      <!-- 骨架屏 -->
      <div v-if="loading && skillsList.length === 0" class="skills-grid">
        <div v-for="n in 6" :key="'skel-'+n" class="skill-card skill-card-skeleton">
          <t-skeleton animation="gradient" :row-col="[
            [{ width: '40px', height: '40px', type: 'rect' }, { width: '60%', height: '20px' }],
            { width: '100%', height: '14px' },
            { width: '80%', height: '14px' },
          ]" />
        </div>
      </div>

      <!-- Skills 列表 -->
      <div v-else-if="filteredSkills.length > 0" class="skills-grid">
        <div
          v-for="skill in filteredSkills"
          :key="skill.name"
          class="skill-card"
          :class="{ 'is-selected': selectedSkill?.name === skill.name }"
          @click="handleSelectSkill(skill)"
        >
          <div class="skill-card-header">
            <div class="skill-icon">
              <t-icon name="lightbulb" size="24px" />
            </div>
            <div class="skill-meta">
              <h3 class="skill-name">{{ skill.name }}</h3>
              <t-tag
                :theme="skill.source === 'preloaded' ? 'primary' : 'success'"
                variant="light"
                size="small"
              >
                {{ skill.source === 'preloaded' ? $t('skills.hub.preloaded') : $t('skills.hub.installed') }}
              </t-tag>
            </div>
          </div>
          <p class="skill-description">{{ skill.description }}</p>
          <div class="skill-card-actions">
            <t-button variant="text" size="small" @click.stop="handleExport(skill)">
              <template #icon><t-icon name="download" /></template>
              {{ $t('skills.hub.export') }}
            </t-button>
            <t-button
              v-if="skill.source === 'installed'"
              variant="text"
              size="small"
              theme="danger"
              @click.stop="handleUninstall(skill)"
            >
              <template #icon><t-icon name="delete" /></template>
              {{ $t('skills.hub.uninstall') }}
            </t-button>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <div v-else class="empty-state">
        <t-icon name="lightbulb" size="48px" class="empty-icon" />
        <p>{{ searchQuery ? $t('skills.hub.noSearchResults') : $t('skills.hub.noSkills') }}</p>
      </div>
    </div>

    <!-- 右侧详情面板 -->
    <transition name="slide-right">
      <div v-if="selectedSkill && skillDetail" class="skill-detail-panel">
        <div class="detail-header">
          <div class="detail-title-row">
            <h3>{{ skillDetail.name }}</h3>
            <t-button variant="text" size="small" @click="selectedSkill = null; skillDetail = null">
              <t-icon name="close" />
            </t-button>
          </div>
          <t-tag
            :theme="skillDetail.source === 'preloaded' ? 'primary' : 'success'"
            variant="light"
            size="small"
          >
            {{ skillDetail.source === 'preloaded' ? $t('skills.hub.preloaded') : $t('skills.hub.installed') }}
          </t-tag>
          <p class="detail-description">{{ skillDetail.description }}</p>
        </div>

        <!-- 标签页 -->
        <t-tabs v-model="detailTab" class="detail-tabs">
          <t-tab-panel value="instructions" :label="$t('skills.hub.instructions')">
            <div class="detail-content markdown-body" v-html="renderedInstructions"></div>
          </t-tab-panel>
          <t-tab-panel value="files" :label="$t('skills.hub.files') + ` (${skillDetail.files?.length || 0})`">
            <div class="file-list">
              <div v-for="file in skillDetail.files" :key="file" class="file-item">
                <t-icon :name="getFileIcon(file)" size="16px" />
                <span>{{ file }}</span>
              </div>
              <div v-if="!skillDetail.files?.length" class="empty-hint">
                {{ $t('skills.hub.noFiles') }}
              </div>
            </div>
          </t-tab-panel>
          <t-tab-panel value="docs" :label="$t('skills.hub.docs') + ` (${skillDetail.docs?.length || 0})`">
            <div v-for="doc in skillDetail.docs" :key="doc.path" class="doc-item">
              <h4>{{ doc.path }}</h4>
              <pre class="doc-content">{{ doc.content }}</pre>
            </div>
            <div v-if="!skillDetail.docs?.length" class="empty-hint">
              {{ $t('skills.hub.noDocs') }}
            </div>
          </t-tab-panel>
        </t-tabs>

        <!-- 操作按钮 -->
        <div class="detail-actions">
          <t-button block @click="handleExport(selectedSkill!)">
            <template #icon><t-icon name="download" /></template>
            {{ $t('skills.hub.exportSkill') }}
          </t-button>
          <t-button
            v-if="selectedSkill?.source === 'installed'"
            block
            theme="danger"
            variant="outline"
            @click="handleUninstall(selectedSkill!)"
          >
            <template #icon><t-icon name="delete" /></template>
            {{ $t('skills.hub.uninstallSkill') }}
          </t-button>
        </div>
      </div>
    </transition>

    <!-- 安装对话框 -->
    <t-dialog
      v-model:visible="showInstallDialog"
      :header="$t('skills.hub.installFromUrl')"
      :confirm-btn="$t('skills.hub.install')"
      :cancel-btn="$t('common.cancel')"
      :confirm-on-enter="true"
      :on-confirm="handleInstallFromUrl"
      :loading="installing"
    >
      <t-form :data="installForm" label-align="top">
        <t-form-item :label="$t('skills.hub.skillName')" name="name">
          <t-input v-model="installForm.name" :placeholder="$t('skills.hub.skillNamePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('skills.hub.skillUrl')" name="url">
          <t-input v-model="installForm.url" :placeholder="$t('skills.hub.skillUrlPlaceholder')" />
        </t-form-item>
      </t-form>
    </t-dialog>

    <!-- 上传对话框 -->
    <t-dialog
      v-model:visible="showUploadDialog"
      :header="$t('skills.hub.uploadSkill')"
      :confirm-btn="$t('skills.hub.upload')"
      :cancel-btn="$t('common.cancel')"
      :on-confirm="handleUploadSkill"
      :loading="uploading"
    >
      <t-form :data="uploadForm" label-align="top">
        <t-form-item :label="$t('skills.hub.skillName')" name="name">
          <t-input v-model="uploadForm.name" :placeholder="$t('skills.hub.skillNamePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('skills.hub.skillFile')" name="file">
          <t-upload
            v-model="uploadForm.files"
            :auto-upload="false"
            :multiple="false"
            accept=".zip,.tar,.tar.gz,.tgz"
            theme="file"
          />
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listSkills,
  getSkillDetail,
  installSkill,
  uploadSkill,
  uninstallSkill,
  refreshSkills,
  downloadSkill,
  type SkillInfo,
  type SkillDetail,
} from '@/api/skill';

const { t } = useI18n();

// 状态
const loading = ref(false);
const refreshing = ref(false);
const installing = ref(false);
const uploading = ref(false);
const skillsList = ref<SkillInfo[]>([]);
const selectedSkill = ref<SkillInfo | null>(null);
const skillDetail = ref<SkillDetail | null>(null);
const searchQuery = ref('');
const sourceFilter = ref('all');
const detailTab = ref('instructions');

// 对话框
const showInstallDialog = ref(false);
const showUploadDialog = ref(false);
const installForm = ref({ name: '', url: '' });
const uploadForm = ref<{ name: string; files: any[] }>({ name: '', files: [] });

// 计算属性
const filteredSkills = computed(() => {
  let result = skillsList.value;

  // 按来源过滤
  if (sourceFilter.value !== 'all') {
    result = result.filter(s => s.source === sourceFilter.value);
  }

  // 按搜索词过滤
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase();
    result = result.filter(s =>
      s.name.toLowerCase().includes(query) ||
      s.description.toLowerCase().includes(query)
    );
  }

  return result;
});

// 渲染 Markdown 指令（简单处理）
const renderedInstructions = computed(() => {
  if (!skillDetail.value?.instructions) return '';
  // 简单的 Markdown 渲染：转义 HTML，处理标题、粗体、代码块
  let text = skillDetail.value.instructions;
  text = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  text = text.replace(/^### (.+)$/gm, '<h4>$1</h4>');
  text = text.replace(/^## (.+)$/gm, '<h3>$1</h3>');
  text = text.replace(/^# (.+)$/gm, '<h2>$1</h2>');
  text = text.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  text = text.replace(/`([^`]+)`/g, '<code>$1</code>');
  text = text.replace(/```[\s\S]*?```/g, (match) => {
    const content = match.replace(/```\w*\n?/, '').replace(/\n?```$/, '');
    return `<pre><code>${content}</code></pre>`;
  });
  text = text.replace(/\n/g, '<br>');
  return text;
});

// 方法
const loadSkills = async () => {
  loading.value = true;
  try {
    const res = await listSkills();
    skillsList.value = res.data || [];
  } catch (e) {
    console.error('Failed to load skills', e);
    MessagePlugin.error(t('skills.hub.loadFailed'));
  } finally {
    loading.value = false;
  }
};

const handleRefresh = async () => {
  refreshing.value = true;
  try {
    await refreshSkills();
    await loadSkills();
    MessagePlugin.success(t('skills.hub.refreshSuccess'));
  } catch (e) {
    console.error('Failed to refresh skills', e);
    MessagePlugin.error(t('skills.hub.refreshFailed'));
  } finally {
    refreshing.value = false;
  }
};

const handleSelectSkill = async (skill: SkillInfo) => {
  selectedSkill.value = skill;
  detailTab.value = 'instructions';
  try {
    const res = await getSkillDetail(skill.name);
    skillDetail.value = res.data;
  } catch (e) {
    console.error('Failed to load skill detail', e);
    MessagePlugin.error(t('skills.hub.loadDetailFailed'));
  }
};

const handleInstallFromUrl = async () => {
  if (!installForm.value.name || !installForm.value.url) {
    MessagePlugin.warning(t('skills.hub.fillRequired'));
    return;
  }
  installing.value = true;
  try {
    await installSkill(installForm.value.name, installForm.value.url);
    MessagePlugin.success(t('skills.hub.installSuccess'));
    showInstallDialog.value = false;
    installForm.value = { name: '', url: '' };
    await loadSkills();
  } catch (e: any) {
    console.error('Failed to install skill', e);
    MessagePlugin.error(e?.message || t('skills.hub.installFailed'));
  } finally {
    installing.value = false;
  }
};

const handleUploadSkill = async () => {
  if (!uploadForm.value.name || !uploadForm.value.files?.length) {
    MessagePlugin.warning(t('skills.hub.fillRequired'));
    return;
  }
  uploading.value = true;
  try {
    // TDesign t-upload 组件在 auto-upload: false 模式下，文件对象结构为:
    // { raw: File, name: string, status: string, ... }
    const uploadFile = uploadForm.value.files[0];
    const file = uploadFile?.raw || uploadFile?.file || uploadFile;
    
    if (!(file instanceof File)) {
      console.error('Invalid file object:', uploadFile);
      MessagePlugin.error(t('skills.hub.invalidFile') || 'Invalid file, please re-select');
      return;
    }
    
    await uploadSkill(uploadForm.value.name, file);
    MessagePlugin.success(t('skills.hub.uploadSuccess'));
    showUploadDialog.value = false;
    uploadForm.value = { name: '', files: [] };
    await loadSkills();
  } catch (e: any) {
    console.error('Failed to upload skill', e);
    MessagePlugin.error(e?.message || t('skills.hub.uploadFailed'));
  } finally {
    uploading.value = false;
  }
};

const handleUninstall = async (skill: SkillInfo) => {
  try {
    await uninstallSkill(skill.name);
    MessagePlugin.success(t('skills.hub.uninstallSuccess'));
    if (selectedSkill.value?.name === skill.name) {
      selectedSkill.value = null;
      skillDetail.value = null;
    }
    await loadSkills();
  } catch (e: any) {
    console.error('Failed to uninstall skill', e);
    MessagePlugin.error(e?.message || t('skills.hub.uninstallFailed'));
  }
};

const handleExport = async (skill: SkillInfo) => {
  try {
    const blob = await downloadSkill(skill.name);
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${skill.name}.zip`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  } catch (e: any) {
    console.error('Failed to export skill', e);
    MessagePlugin.error(e?.message || t('skills.hub.exportFailed'));
  }
};

const getFileIcon = (file: string) => {
  const ext = file.split('.').pop()?.toLowerCase();
  switch (ext) {
    case 'md': return 'file';
    case 'py': return 'logo-python';
    case 'js':
    case 'ts': return 'logo-javascript';
    case 'sh':
    case 'bash': return 'terminal';
    default: return 'file';
  }
};

// 生命周期
onMounted(() => {
  loadSkills();
});
</script>

<style scoped lang="less">
.skills-hub-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.skills-hub-content {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
}

.header {
  margin-bottom: 24px;
}

.header-title {
  .title-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;

    h2 {
      font-size: 20px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin: 0;
    }
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .header-subtitle {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 4px 0 0;
  }
}

.filter-bar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;

  .search-input {
    max-width: 300px;
  }
}

.skills-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

.skill-card {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 12px;
  padding: 20px;
  cursor: pointer;
  transition: all 0.2s ease;

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
    transform: translateY(-2px);
  }

  &.is-selected {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.skill-card-skeleton {
  cursor: default;
  &:hover {
    transform: none;
    box-shadow: none;
  }
}

.skill-card-header {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 12px;
}

.skill-icon {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.skill-meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.skill-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-description {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
  margin: 0 0 12px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.skill-card-actions {
  display: flex;
  gap: 4px;
  border-top: 1px solid var(--td-component-stroke);
  padding-top: 12px;
}

// 空状态
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 80px 20px;
  color: var(--td-text-color-placeholder);

  .empty-icon {
    margin-bottom: 16px;
    opacity: 0.4;
  }

  p {
    font-size: 14px;
  }
}

// 右侧详情面板
.skill-detail-panel {
  width: 420px;
  border-left: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  display: flex;
  flex-direction: column;
  overflow-y: auto;
}

.detail-header {
  padding: 24px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.detail-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;

  h3 {
    font-size: 18px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }
}

.detail-description {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
  margin: 12px 0 0;
}

.detail-tabs {
  flex: 1;
  overflow-y: auto;

  :deep(.t-tabs__content) {
    padding: 16px 24px;
  }
}

.detail-content {
  font-size: 13px;
  line-height: 1.7;
  color: var(--td-text-color-primary);

  :deep(h2), :deep(h3), :deep(h4) {
    margin: 16px 0 8px;
    font-weight: 600;
  }

  :deep(code) {
    background: var(--td-bg-color-secondarycontainer);
    padding: 2px 6px;
    border-radius: 4px;
    font-size: 12px;
  }

  :deep(pre) {
    background: var(--td-bg-color-secondarycontainer);
    padding: 12px;
    border-radius: 8px;
    overflow-x: auto;

    code {
      background: none;
      padding: 0;
    }
  }
}

.file-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.file-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  font-size: 13px;
  color: var(--td-text-color-primary);
}

.doc-item {
  margin-bottom: 16px;

  h4 {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px;
  }
}

.doc-content {
  background: var(--td-bg-color-secondarycontainer);
  padding: 12px;
  border-radius: 8px;
  font-size: 12px;
  line-height: 1.6;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.empty-hint {
  text-align: center;
  padding: 24px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.detail-actions {
  padding: 16px 24px;
  border-top: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

// 过渡动画
.slide-right-enter-active,
.slide-right-leave-active {
  transition: all 0.3s ease;
}

.slide-right-enter-from,
.slide-right-leave-to {
  transform: translateX(100%);
  opacity: 0;
}
</style>
