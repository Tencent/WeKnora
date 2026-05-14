<template>
  <div class="skill-output">
    <!-- 执行摘要 -->
    <div class="execution-summary" :class="{ success: isSuccess, error: !isSuccess }">
      <div class="summary-header">
        <div class="summary-left">
          <span class="skill-icon">⚡</span>
          <span class="skill-name">{{ data.skill_name }}</span>
          <span class="exit-badge" :class="isSuccess ? 'success' : 'error'">
            {{ isSuccess ? t('skillOutput.exitSuccess') : t('skillOutput.exitFailed', { code: data.exit_code }) }}
          </span>
        </div>
        <div class="summary-right">
          <span class="duration">{{ formatDuration(data.duration_ms) }}</span>
        </div>
      </div>
      <div v-if="commandText" class="command-line">
        <code>{{ commandText }}</code>
      </div>
    </div>

    <!-- 产物文件列表 -->
    <div v-if="outputFiles.length > 0" class="artifacts-section">
      <div class="section-title">
        <span class="section-icon">📦</span>
        {{ t('skillOutput.outputFiles', { count: outputFiles.length }) }}
      </div>
      <div class="artifacts-list">
        <div
          v-for="(file, index) in outputFiles"
          :key="file.name"
          class="artifact-card"
        >
          <div class="artifact-header" @click="toggleFile(index)">
            <div class="artifact-info">
              <span class="file-icon">{{ getFileIcon(file) }}</span>
              <span class="file-name">{{ file.name }}</span>
              <span class="file-meta">
                <span class="file-type">{{ file.mime_type }}</span>
                <span class="file-size">{{ formatSize(file.size_bytes) }}</span>
              </span>
            </div>
            <div class="artifact-actions">
              <button
                class="download-btn"
                :class="{ downloading: downloadingFiles[file.name] }"
                :disabled="downloadingFiles[file.name]"
                :title="t('skillOutput.download')"
                @click.stop="downloadFile(file)"
              >
                <svg v-if="!downloadingFiles[file.name]" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                  <polyline points="7 10 12 15 17 10"/>
                  <line x1="12" y1="15" x2="12" y2="3"/>
                </svg>
                <t-loading v-else size="12px" />
                {{ downloadingFiles[file.name] ? t('skillOutput.downloading') : t('skillOutput.download') }}
              </button>
              <t-icon
                v-if="file.is_text && file.content"
                :name="isFileExpanded(index) ? 'chevron-up' : 'chevron-down'"
                class="expand-icon"
              />
            </div>
          </div>

          <!-- 文本文件内容预览 -->
          <div
            v-if="file.is_text && file.content && isFileExpanded(index)"
            class="artifact-content"
          >
            <div class="content-toolbar">
              <span class="content-label">{{ t('skillOutput.preview') }}</span>
              <button class="copy-btn" @click="copyContent(file.content)">
                {{ copyState[index] ? t('skillOutput.copied') : t('skillOutput.copy') }}
              </button>
            </div>
            <pre class="content-code"><code>{{ truncateContent(file.content) }}</code></pre>
            <div v-if="file.content.length > maxPreviewLength" class="content-truncated">
              {{ t('skillOutput.truncated', { total: formatSize(file.content.length) }) }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- stdout/stderr 折叠区域 -->
    <div v-if="data.stdout || data.stderr" class="output-section">
      <div class="section-toggle" @click="showOutput = !showOutput">
        <t-icon :name="showOutput ? 'chevron-up' : 'chevron-down'" class="toggle-icon" />
        <span>{{ t('skillOutput.executionOutput') }}</span>
      </div>
      <div v-if="showOutput" class="output-content">
        <div v-if="data.stdout" class="output-block">
          <div class="output-label">stdout</div>
          <pre class="output-text">{{ data.stdout }}</pre>
        </div>
        <div v-if="data.stderr" class="output-block stderr">
          <div class="output-label">stderr</div>
          <pre class="output-text">{{ data.stderr }}</pre>
        </div>
      </div>
    </div>

    <!-- 错误信息（如果执行失败且没有产物） -->
    <div v-if="!isSuccess && data.stderr && outputFiles.length === 0" class="error-section">
      <div class="error-title">{{ t('skillOutput.errorTitle') }}</div>
      <pre class="error-content">{{ data.stderr }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, reactive } from 'vue';
import type { SkillOutputData } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';
import { downloadArtifact } from '@/api/skill';

interface Props {
  data: SkillOutputData;
}

const props = defineProps<Props>();
const { t } = useI18n();

const maxPreviewLength = 4096;
const showOutput = ref(false);
const expandedFiles = ref<Set<number>>(new Set([0])); // 默认展开第一个文件
const copyState = reactive<Record<number, boolean>>({});
const downloadingFiles = reactive<Record<string, boolean>>({});

const isSuccess = computed(() => props.data.exit_code === 0);

const commandText = computed(() => {
  if (props.data.command) return props.data.command;
  if (props.data.script_path) {
    const args = props.data.args?.join(' ') || '';
    return args ? `${props.data.script_path} ${args}` : props.data.script_path;
  }
  return '';
});

interface OutputFileItem {
  name: string;
  mime_type: string;
  size_bytes: number;
  is_text: boolean;
  content?: string;
}

const outputFiles = computed<OutputFileItem[]>(() => {
  return (props.data.output_files || []) as OutputFileItem[];
});

const toggleFile = (index: number) => {
  const file = outputFiles.value[index];
  if (!file?.is_text || !file?.content) return;
  if (expandedFiles.value.has(index)) {
    expandedFiles.value.delete(index);
  } else {
    expandedFiles.value.add(index);
  }
};

const isFileExpanded = (index: number): boolean => expandedFiles.value.has(index);

const downloadFile = async (file: OutputFileItem) => {
  const sessionId = props.data.artifact_session_id;
  
  // 防止重复点击
  if (downloadingFiles[file.name]) return;
  downloadingFiles[file.name] = true;
  
  try {
    if (sessionId) {
      // 通过带认证的 artifact API 下载
      const blobData = await downloadArtifact(sessionId, file.name);
      const blob = blobData instanceof Blob ? blobData : new Blob([blobData as any], { type: file.mime_type || 'application/octet-stream' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = file.name;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } else if (file.is_text && file.content) {
      // 回退：直接从内容创建下载
      const blob = new Blob([file.content], { type: file.mime_type || 'text/plain' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = file.name;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    }
  } catch (err) {
    console.error('Failed to download artifact:', err);
  } finally {
    downloadingFiles[file.name] = false;
  }
};

const copyContent = async (content: string, index?: number) => {
  try {
    await navigator.clipboard.writeText(content);
    if (index !== undefined) {
      copyState[index] = true;
      setTimeout(() => { copyState[index] = false; }, 2000);
    }
  } catch {
    // 回退方案
    const textarea = document.createElement('textarea');
    textarea.value = content;
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand('copy');
    document.body.removeChild(textarea);
  }
};

const truncateContent = (content: string): string => {
  if (!content) return '';
  if (content.length <= maxPreviewLength) return content;
  return content.substring(0, maxPreviewLength) + '\n...';
};

const formatDuration = (ms?: number): string => {
  if (!ms) return '';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
};

const formatSize = (bytes?: number): string => {
  if (!bytes || bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let size = bytes;
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024;
    i++;
  }
  return `${size.toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
};

const getFileIcon = (file: OutputFileItem): string => {
  const mime = file.mime_type || '';
  const name = file.name || '';
  
  if (mime.startsWith('image/')) return '🖼️';
  if (mime === 'application/pdf') return '📄';
  if (mime.includes('spreadsheet') || name.endsWith('.csv') || name.endsWith('.xlsx')) return '📊';
  if (mime.includes('zip') || mime.includes('tar') || mime.includes('gzip')) return '📦';
  if (mime.includes('json')) return '📋';
  if (mime.includes('html')) return '🌐';
  if (mime.startsWith('text/') || file.is_text) return '📝';
  if (mime.startsWith('audio/')) return '🎵';
  if (mime.startsWith('video/')) return '🎬';
  return '📎';
};
</script>

<style lang="less" scoped>
@import './tool-results.less';

.skill-output {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 6px 6px 0 6px;
}

// 执行摘要
.execution-summary {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 10px 12px;
  
  &.success {
    border-left: 3px solid var(--td-success-color);
  }
  
  &.error {
    border-left: 3px solid var(--td-error-color);
  }
}

.summary-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.summary-left {
  display: flex;
  align-items: center;
  gap: 6px;
}

.skill-icon {
  font-size: 14px;
}

.skill-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.exit-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 600;
  line-height: 1.6;
  
  &.success {
    background: rgba(0, 168, 112, 0.1);
    color: var(--td-success-color);
  }
  
  &.error {
    background: rgba(227, 77, 89, 0.1);
    color: var(--td-error-color);
  }
}

.duration {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.command-line {
  margin-top: 6px;
  
  code {
    font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
    font-size: 11px;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    padding: 2px 6px;
    border-radius: 3px;
    word-break: break-all;
  }
}

// 产物文件区域
.artifacts-section {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  overflow: hidden;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px;
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

.section-icon {
  font-size: 14px;
}

.artifacts-list {
  display: flex;
  flex-direction: column;
}

.artifact-card {
  border-bottom: 1px solid var(--td-component-stroke);
  
  &:last-child {
    border-bottom: none;
  }
}

.artifact-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  cursor: pointer;
  transition: background-color 0.15s ease;
  
  &:hover {
    background: rgba(7, 192, 95, 0.04);
  }
}

.artifact-info {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
}

.file-icon {
  font-size: 16px;
  flex-shrink: 0;
}

.file-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.file-type {
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  padding: 1px 4px;
  border-radius: 2px;
}

.file-size {
  font-size: 10px;
  color: var(--td-text-color-placeholder);
}

.artifact-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.download-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  font-size: 11px;
  font-weight: 500;
  border-radius: 4px;
  border: 1px solid var(--td-brand-color);
  background: rgba(7, 192, 95, 0.06);
  color: var(--td-brand-color);
  cursor: pointer;
  transition: all 0.15s ease;
  white-space: nowrap;
  
  &.downloading {
    opacity: 0.6;
    cursor: not-allowed;
    pointer-events: none;
  }
  
  &:hover:not(.downloading) {
    background: rgba(7, 192, 95, 0.12);
    box-shadow: 0 1px 3px rgba(7, 192, 95, 0.15);
  }
  
  svg {
    flex-shrink: 0;
  }
}

.expand-icon {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  transition: transform 0.15s ease;
}

// 文件内容预览
.artifact-content {
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

.content-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.content-label {
  font-size: 10px;
  font-weight: 600;
  color: var(--td-text-color-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.copy-btn {
  padding: 2px 8px;
  font-size: 10px;
  border-radius: 3px;
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: all 0.15s ease;
  
  &:hover {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }
}

.content-code {
  margin: 0;
  padding: 10px 12px;
  font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
  font-size: 11px;
  line-height: 1.5;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 300px;
  overflow-y: auto;
  
  &::-webkit-scrollbar {
    width: 6px;
  }
  
  &::-webkit-scrollbar-track {
    background: transparent;
  }
  
  &::-webkit-scrollbar-thumb {
    background: var(--td-component-border);
    border-radius: 3px;
  }
}

.content-truncated {
  padding: 4px 12px 8px;
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  font-style: italic;
}

// stdout/stderr 输出区域
.output-section {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  overflow: hidden;
}

.section-toggle {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px 12px;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  user-select: none;
  transition: background-color 0.15s ease;
  
  &:hover {
    background: rgba(0, 0, 0, 0.02);
  }
}

.toggle-icon {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.output-content {
  border-top: 1px solid var(--td-component-stroke);
}

.output-block {
  padding: 0;
  
  &.stderr {
    border-top: 1px solid var(--td-component-stroke);
    
    .output-label {
      color: var(--td-error-color);
    }
    
    .output-text {
      color: var(--td-error-color);
      background: rgba(227, 77, 89, 0.04);
    }
  }
}

.output-label {
  padding: 4px 12px;
  font-size: 10px;
  font-weight: 600;
  color: var(--td-text-color-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
}

.output-text {
  margin: 0;
  padding: 8px 12px;
  font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
  font-size: 11px;
  line-height: 1.5;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
  
  &::-webkit-scrollbar {
    width: 6px;
  }
  
  &::-webkit-scrollbar-track {
    background: transparent;
  }
  
  &::-webkit-scrollbar-thumb {
    background: var(--td-component-border);
    border-radius: 3px;
  }
}

// 错误区域
.error-section {
  background: rgba(227, 77, 89, 0.04);
  border: 1px solid rgba(227, 77, 89, 0.2);
  border-radius: 6px;
  overflow: hidden;
}

.error-title {
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 600;
  color: var(--td-error-color);
  background: rgba(227, 77, 89, 0.06);
  border-bottom: 1px solid rgba(227, 77, 89, 0.15);
}

.error-content {
  margin: 0;
  padding: 8px 12px;
  font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
  font-size: 11px;
  line-height: 1.5;
  color: var(--td-error-color);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
}
</style>
