<template>
  <article class="video-card" tabindex="0" role="button" @click="$emit('select')" @keydown.enter="$emit('select')">
    <div class="video-card__cover">
      <img v-if="(video.cover_url || video.poster_url) && !coverFailed" :src="video.cover_url || video.poster_url" :alt="video.title" @error="coverFailed = true" />
      <div v-else class="video-card__fallback" aria-hidden="true">▶</div>
      <span v-if="statusLabel" class="video-card__status" :class="`video-card__status--${statusTone}`">{{ statusLabel }}</span>
      <span class="video-card__duration">{{ video.durationSeconds > 0 ? formatDuration(video.durationSeconds) : '时长待生成' }}</span>
    </div>
    <div class="video-card__body">
      <h3>{{ video.title }}</h3>
      <div class="video-card__meta">
        <span v-if="video.categoryName" class="video-card__category">{{ video.categoryName }}</span>
        <time>{{ video.created_at }}</time>
        <t-button
          class="video-card__delete"
          variant="text"
          theme="danger"
          shape="circle"
          size="small"
          aria-label="删除视频"
          title="删除视频"
          @click.stop="$emit('delete')"
        >
          <template #icon><t-icon name="delete" /></template>
        </t-button>
      </div>
      <p v-if="video.status === 'failed' && video.processing_error_summary" class="video-card__error">
        {{ video.processing_error_summary }}
      </p>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { VideoData } from '@/types/videohub'
import { formatDuration } from '@/api/videohub/videoMapping'

const props = defineProps<{ video: VideoData }>()
defineEmits<{ select: []; delete: [] }>()
const coverFailed = ref(false)
const statusLabel = computed(() => {
  const map: Record<string, string> = {
    uploading: '上传中',
    uploaded: '处理中',
    initializing: '处理中',
    ready: '可播放',
    processing: '处理中',
    completed: '可播放',
    failed: '失败',
  }
  return map[props.video.status || ''] || ''
})
const statusTone = computed(() => {
  if (props.video.status === 'failed') return 'failed'
  if (['uploaded', 'initializing', 'uploading', 'processing'].includes(props.video.status || '')) return 'processing'
  return 'ready'
})
</script>

<style scoped>
.video-card { position: relative; overflow: hidden; cursor: pointer; border: 1px solid rgba(255,255,255,.56); border-radius: var(--td-radius-extraLarge); background: rgba(255,255,255,.12); backdrop-filter: blur(14px) saturate(112%); -webkit-backdrop-filter: blur(14px) saturate(112%); transition: transform .18s cubic-bezier(.2,0,0,1), box-shadow .18s ease, border-color .18s ease, background-color .18s ease; }
.video-card:hover { z-index: 1; transform: translateY(-3px); border-color: rgba(255,255,255,.92); background: rgba(255,255,255,.28); outline: none; box-shadow: 0 14px 30px rgba(255,255,255,.72), 0 3px 10px rgba(0,0,0,.06); }
.video-card:focus-visible:not(:hover) { border-color: rgba(255,255,255,.92); outline: 2px solid var(--td-brand-color-focus); outline-offset: 2px; box-shadow: none; }
.video-card__cover { position: relative; aspect-ratio: 16 / 9; overflow: hidden; background: var(--td-bg-color-component); }
.video-card__cover::after { content: ""; position: absolute; inset: 0; pointer-events: none; background: linear-gradient(180deg, transparent 60%, color-mix(in srgb, var(--td-gray-color-14) 36%, transparent)); }
.video-card__cover img { width: 100%; height: 100%; display: block; object-fit: cover; filter: saturate(.78); }
.video-card__fallback { width: 100%; height: 100%; display: grid; place-items: center; color: var(--td-brand-color-7); background: color-mix(in srgb, var(--td-brand-color-light) 70%, var(--td-bg-color-component)); font-size: 30px; }
.video-card__status { position: absolute; z-index: 1; left: 10px; top: 10px; padding: 4px 9px; border: 1px solid rgba(255, 255, 255, .7); border-radius: var(--td-radius-large); color: var(--td-text-color-anti); background: color-mix(in srgb, var(--td-gray-color-14) 24%, transparent); backdrop-filter: blur(10px); font-size: 11px; line-height: 1.45; }
.video-card__status--processing { color: var(--td-text-color-primary); background: color-mix(in srgb, var(--td-bg-color-container) 70%, transparent); }
.video-card__status--ready { color: var(--td-text-color-anti); background: color-mix(in srgb, var(--td-brand-color-7) 72%, transparent); }
.video-card__status--failed { color: var(--td-text-color-anti); background: color-mix(in srgb, var(--td-error-color) 76%, transparent); }
.video-card__duration { position: absolute; z-index: 1; right: 10px; bottom: 9px; padding: 3px 7px; border: 1px solid rgba(255, 255, 255, .7); border-radius: var(--td-radius-round); color: var(--td-text-color-anti); background: color-mix(in srgb, var(--td-gray-color-14) 40%, transparent); font-family: var(--app-font-family-mono); font-size: 10px; line-height: 1.4; }
.video-card__body { padding: 14px 15px 15px; background: rgba(255,255,255,.08); }
.video-card h3 { margin: 0; overflow: hidden; color: var(--td-text-color-primary); font-size: 15px; font-weight: 400; line-height: 1.55; text-overflow: ellipsis; white-space: nowrap; }
.video-card__meta { display: flex; align-items: center; gap: 8px; min-width: 0; margin-top: 9px; }
.video-card__category { flex: none; padding: 3px 7px; border-radius: var(--td-radius-medium); color: var(--td-brand-color-7); background: color-mix(in srgb, var(--td-brand-color-light) 76%, transparent); font-size: 11px; line-height: 1.45; }
.video-card time { overflow: hidden; color: var(--td-text-color-secondary); font-size: 11px; line-height: 1.45; text-overflow: ellipsis; white-space: nowrap; }
.video-card__delete { flex: none; margin-left: auto; color: var(--td-text-color-placeholder); }
.video-card__delete:hover { color: var(--td-error-color); background: var(--td-error-color-light); }
.video-card__error { margin: 8px 0 0; overflow: hidden; color: var(--td-error-color); font-size: 11px; line-height: 1.45; text-overflow: ellipsis; white-space: nowrap; }
</style>
