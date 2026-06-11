<template>
  <div class="embed-user-msg" :class="{ 'is-embedded': embeddedMode }">
    <div v-if="hasImages" class="user_images">
      <img
        v-for="(img, idx) in images"
        :key="idx"
        :src="img.url"
        class="user_image_thumb"
        alt=""
      />
    </div>
    <div v-if="hasAttachments" class="user_attachments">
      <div v-for="(att, idx) in attachments" :key="idx" class="user_attachment_card">
        <div class="attachment_card_info">
          <div class="attachment_card_name">{{ att.file_name }}</div>
          <div v-if="att.file_size" class="attachment_card_meta">{{ formatFileSize(att.file_size) }}</div>
        </div>
      </div>
    </div>
    <div class="user_msg">{{ content }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

type EmbedImage = { url: string }
type EmbedAttachment = { file_name: string; file_size?: number }

const props = withDefaults(
  defineProps<{
    content?: string
    mentioned_items?: unknown[]
    images?: EmbedImage[]
    attachments?: EmbedAttachment[]
    embeddedMode?: boolean
  }>(),
  {
    content: '',
    mentioned_items: () => [],
    images: () => [],
    attachments: () => [],
    embeddedMode: true,
  },
)

const hasImages = computed(() => (props.images?.length ?? 0) > 0)
const hasAttachments = computed(() => (props.attachments?.length ?? 0) > 0)

const formatFileSize = (bytes: number): string => {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
</script>

<style scoped lang="less">
.embed-user-msg {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
  width: 100%;

  &.is-embedded .user_msg {
    max-width: 100%;
  }
}

.user_msg {
  width: max-content;
  max-width: 776px;
  padding: 10px 12px;
  border-radius: 4px;
  background: #8ce97f;
  margin-left: auto;
  color: #000000e6;
  font-size: 15px;
  text-align: justify;
  word-break: break-all;
  box-sizing: border-box;
  white-space: pre-wrap;
}

.user_images {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  justify-content: flex-end;
  max-width: 100%;
}

.user_image_thumb {
  width: 120px;
  height: 120px;
  object-fit: cover;
  border-radius: 6px;
  border: 1px solid var(--td-border-level-2-color, #e7e7e7);
}

.user_attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: flex-end;
  max-width: 100%;
}

.user_attachment_card {
  padding: 8px 12px;
  border-radius: 8px;
  border: 1px solid var(--td-border-level-1-color, #e7e7e7);
  background: var(--td-bg-color-container, #fff);
  max-width: 260px;
  min-width: 120px;
}

.attachment_card_name {
  font-size: 13px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.attachment_card_meta {
  font-size: 11px;
  color: var(--td-text-color-secondary, #999);
}

html[theme-mode='dark'] .user_msg {
  background: var(--td-brand-color-3);
  color: rgba(255, 255, 255, 0.9);
}
</style>
