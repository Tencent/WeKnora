<script setup lang="ts">
import { ref, computed } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

export interface AttachmentFile {
  file: File;
  id: string;
  name: string;
  size: number;
  type: string;
  preview?: string;
}

const props = defineProps<{
  maxFiles?: number;
  maxSize?: number; // in MB
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:files', files: AttachmentFile[]): void;
  (e: 'remove', id: string): void;
}>();

const attachments = ref<AttachmentFile[]>([]);
const fileInputRef = ref<HTMLInputElement>();

// Supported file types (matching backend)
const SUPPORTED_TYPES = [
  // Documents
  '.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx',
  // Text
  '.txt', '.md', '.csv', '.json', '.xml', '.html',
  // Audio
  '.mp3', '.wav', '.m4a', '.flac', '.ogg', '.aac',
];

const maxFiles = computed(() => props.maxFiles || 5);
const maxSize = computed(() => (props.maxSize || 20) * 1024 * 1024); // Convert MB to bytes

const triggerFileSelect = () => {
  if (props.disabled) return;
  fileInputRef.value?.click();
};

const handleFileSelect = async (event: Event) => {
  const input = event.target as HTMLInputElement;
  if (!input.files) return;
  
  await addFiles(Array.from(input.files));
  input.value = ''; // Reset input
};

const addFiles = async (files: File[]) => {
  if (props.disabled) return;
  
  for (const file of files) {
    // Check max files limit
    if (attachments.value.length >= maxFiles.value) {
      MessagePlugin.warning(t('chat.attachmentTooMany', { max: maxFiles.value }));
      break;
    }
    
    // Check file size
    if (file.size > maxSize.value) {
      MessagePlugin.warning(t('chat.attachmentTooLarge', { name: file.name, max: props.maxSize || 20 }));
      continue;
    }
    
    // Check file type
    const ext = '.' + file.name.split('.').pop()?.toLowerCase();
    if (!SUPPORTED_TYPES.includes(ext)) {
      MessagePlugin.warning(t('chat.attachmentTypeNotSupported', { name: file.name }));
      continue;
    }
    
    const attachment: AttachmentFile = {
      file,
      id: `${Date.now()}-${Math.random()}`,
      name: file.name,
      size: file.size,
      type: file.type || ext,
    };
    
    attachments.value.push(attachment);
  }
  
  emit('update:files', attachments.value);
};

const removeAttachment = (id: string) => {
  const index = attachments.value.findIndex(a => a.id === id);
  if (index !== -1) {
    attachments.value.splice(index, 1);
    emit('update:files', attachments.value);
    emit('remove', id);
  }
};

const formatFileSize = (bytes: number): string => {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
};

const getFileExt = (fileName: string): string => {
  return fileName.split('.').pop()?.toUpperCase() || 'FILE';
};

const getFileIcon = (fileName: string): string => {
  const ext = fileName.split('.').pop()?.toLowerCase();
  if (['pdf'].includes(ext || '')) return 'file-pdf';
  if (['doc', 'docx'].includes(ext || '')) return 'file-word';
  if (['xls', 'xlsx'].includes(ext || '')) return 'file-excel';
  if (['ppt', 'pptx'].includes(ext || '')) return 'file-powerpoint';
  if (['txt', 'md'].includes(ext || '')) return 'file';
  if (['mp3', 'wav', 'm4a', 'flac', 'ogg', 'aac'].includes(ext || '')) return 'sound';
  return 'file';
};

defineExpose({
  attachments,
  triggerFileSelect,
  clear: () => {
    attachments.value = [];
    emit('update:files', []);
  }
});
</script>

<template>
  <div class="attachment-upload">
    <!-- Hidden file input -->
    <input
      ref="fileInputRef"
      type="file"
      :accept="SUPPORTED_TYPES.join(',')"
      multiple
      style="display: none"
      @change="handleFileSelect"
    />
    
    <!-- Attachment list -->
    <div v-if="attachments.length > 0" class="attachment-preview-bar">
      <div
        v-for="attachment in attachments"
        :key="attachment.id"
        class="attachment-preview-item"
      >
        <div class="attachment-preview-icon">
          <svg viewBox="0 0 40 48" fill="none" xmlns="http://www.w3.org/2000/svg" width="32" height="38">
            <rect width="40" height="48" rx="4" fill="#4A90D9"/>
            <path d="M8 6h16l8 8v28a2 2 0 01-2 2H8a2 2 0 01-2-2V8a2 2 0 012-2z" fill="#5BA3E8"/>
            <path d="M24 6l8 8h-6a2 2 0 01-2-2V6z" fill="#3A7BC8"/>
            <rect x="10" y="20" width="20" height="2" rx="1" fill="white" fill-opacity="0.9"/>
            <rect x="10" y="26" width="20" height="2" rx="1" fill="white" fill-opacity="0.9"/>
            <rect x="10" y="32" width="14" height="2" rx="1" fill="white" fill-opacity="0.9"/>
          </svg>
        </div>
        <div class="attachment-preview-info">
          <div class="attachment-preview-name">{{ attachment.name }}</div>
          <div class="attachment-preview-meta">{{ getFileExt(attachment.name) }}&nbsp;·&nbsp;{{ formatFileSize(attachment.size) }}</div>
        </div>
        <span class="attachment-preview-remove" @click="removeAttachment(attachment.id)" :aria-label="$t('common.remove')">×</span>
      </div>
    </div>
    
    <!-- Upload button (shown in control bar) -->
    <slot name="trigger" :trigger="triggerFileSelect" :count="attachments.length" />
  </div>
</template>

<style scoped lang="less">
.attachment-upload {
  width: 100%;
}

.attachment-preview-bar {
  display: flex;
  gap: 8px;
  padding: 8px 12px 4px;
  flex-wrap: wrap;
}

.attachment-preview-item {
  position: relative;
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 10px;
  padding: 8px 32px 8px 10px;
  border-radius: 8px;
  border: 1px solid var(--td-border-level-1-color, #e7e7e7);
  background: var(--td-bg-color-container, #fff);
  max-width: 240px;
  min-width: 140px;
  cursor: default;

  .attachment-preview-icon {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .attachment-preview-info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .attachment-preview-name {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary, #333);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .attachment-preview-meta {
    font-size: 11px;
    color: var(--td-text-color-secondary, #999);
    white-space: nowrap;
  }

  .attachment-preview-remove {
    position: absolute;
    top: 4px;
    right: 6px;
    width: 18px;
    height: 18px;
    background: rgba(0, 0, 0, 0.18);
    color: #fff;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 13px;
    cursor: pointer;
    line-height: 1;

    &:hover {
      background: rgba(0, 0, 0, 0.4);
    }
  }
}
</style>
