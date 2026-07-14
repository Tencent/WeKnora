<template>
  <div ref="containerRef" class="embed-user-msg" :class="{ 'is-embedded': embeddedMode }">
    <div v-if="hasImages" class="user_images">
      <img v-for="(img, idx) in displayImages" :key="idx" :src="img.url" class="user_image_thumb" alt=""
        @click="previewImage($event)" />
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
    <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { hydrateProtectedFileImages } from '@/utils/security'
import picturePreview from '@/components/picture-preview.vue'

type EmbedImage = { url?: string; data?: string }
type EmbedAttachment = { file_name: string; file_size?: number }

const props = withDefaults(
  defineProps<{
    content?: string
    mentioned_items?: unknown[]
    images?: EmbedImage[]
    attachments?: EmbedAttachment[]
    embeddedMode?: boolean
    embedChannelId?: string
    embedToken?: string
  }>(),
  {
    content: '',
    mentioned_items: () => [],
    images: () => [],
    attachments: () => [],
    embeddedMode: true,
    embedChannelId: '',
    embedToken: '',
  },
)

const containerRef = ref<HTMLElement | null>(null)
const reviewImg = ref(false)
const reviewUrl = ref('')

const displayImages = computed(() =>
  (props.images || [])
    .map((img) => ({ url: String(img.url || img.data || '').trim() }))
    .filter((img) => img.url.length > 0),
)

const hasImages = computed(() => displayImages.value.length > 0)
const hasAttachments = computed(() => (props.attachments?.length ?? 0) > 0)

const hydrateImages = async () => {
  await nextTick()
  if (!props.embedChannelId || !props.embedToken) return
  await hydrateProtectedFileImages(containerRef.value, {
    channelId: props.embedChannelId,
    token: props.embedToken,
  })
}

watch(() => props.images, hydrateImages, { deep: true })
onMounted(hydrateImages)

const previewImage = (event: MouseEvent) => {
  const src = (event.target as HTMLImageElement | null)?.src
  if (!src) return
  reviewUrl.value = src
  reviewImg.value = true
}

const closePreImg = () => {
  reviewImg.value = false
  reviewUrl.value = ''
}

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
  max-width: min(76%, 680px);
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  margin-left: auto;
  color: var(--td-text-color-primary);
  font-size: 16px;
  line-height: 1.6;
  text-align: left;
  word-break: break-word;
  overflow-wrap: anywhere;
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
  cursor: pointer;
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
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}
</style>
