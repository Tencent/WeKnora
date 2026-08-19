<template>
  <article class="video-card" tabindex="0" role="button" @click="$emit('select')" @keydown.enter="$emit('select')">
    <div class="video-card__cover">
      <img v-if="video.poster_url && !coverFailed" :src="video.poster_url" :alt="video.title" @error="coverFailed = true" />
      <div v-else class="video-card__fallback" aria-hidden="true">▶</div>
      <span class="video-card__duration">{{ video.duration }}</span>
    </div>
    <div class="video-card__body">
      <h3>{{ video.title }}</h3>
      <time>{{ video.created_at }}</time>
    </div>
  </article>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { VideoData } from '@/types/videohub'

defineProps<{ video: VideoData }>()
defineEmits<{ select: [] }>()
const coverFailed = ref(false)
</script>

<style scoped>
.video-card { overflow: hidden; cursor: pointer; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); transition: border-color .15s ease, background-color .15s ease, box-shadow .15s ease; }
.video-card:hover, .video-card:focus-visible { border-color: var(--td-border-level-2-color); background: var(--td-bg-color-container-hover); box-shadow: var(--td-shadow-1); outline: none; }
.video-card:focus-visible { border-color: var(--td-brand-color); }
.video-card__cover { position: relative; aspect-ratio: 16 / 9; overflow: hidden; background: var(--td-bg-color-component); }
.video-card__cover img { width: 100%; height: 100%; display: block; object-fit: cover; }
.video-card__fallback { width: 100%; height: 100%; display: grid; place-items: center; color: var(--td-brand-color); background: linear-gradient(135deg, var(--td-brand-color-1), var(--td-bg-color-component)); font-size: 36px; }
.video-card__duration { position: absolute; right: 8px; bottom: 8px; padding: 2px 8px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-round); background: color-mix(in srgb, var(--td-bg-color-container) 88%, transparent); color: var(--td-text-color-primary); font-size: 12px; line-height: 20px; backdrop-filter: blur(8px); }
.video-card__body { padding: 12px 16px 16px; }
.video-card h3 { margin: 0 0 8px; overflow: hidden; color: var(--td-text-color-primary); font-size: 16px; font-weight: 400; line-height: 1.5; text-overflow: ellipsis; white-space: nowrap; }
.video-card time { color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; }
</style>
