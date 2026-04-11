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

const getFileIcon = (fileName: string): string => {
  const ext = fileName.split('.').pop()?.toLowerCase();
  if (['pdf'].includes(ext || '')) return 'file-pdf';
  if (['doc', 'docx'].includes(ext || '')) return 'file-word';
  if (['xls', 'xlsx'].includes(ext || '')) return 'file-excel';
  if (['ppt', 'pptx'].includes(ext || '')) return 'file-powerpoint';
  if (['txt', 'md'].includes(ext || '')) return 'file-text';
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
          <t-icon :name="getFileIcon(attachment.name)" />
        </div>
        <div class="attachment-preview-name">{{ attachment.name }}</div>
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
  width: 60px;
  height: 60px;
  border-radius: 8px;
  border: 1px solid var(--td-border-level-1-color, #e7e7e7);
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  overflow: hidden;
  cursor: default;

  .attachment-preview-icon {
    font-size: 22px;
    color: var(--td-brand-color, #07C05F);
    line-height: 1;
  }

  .attachment-preview-name {
    font-size: 10px;
    color: var(--td-text-color-secondary, #666);
    text-align: center;
    width: 100%;
    padding: 0 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    box-sizing: border-box;
  }

  .attachment-preview-remove {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 16px;
    height: 16px;
    background: rgba(0, 0, 0, 0.5);
    color: #fff;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    cursor: pointer;
    line-height: 1;

    &:hover {
      background: rgba(0, 0, 0, 0.7);
    }
  }
}
</style>
