<template>
  <article class="video-card" tabindex="0" role="button" @click="$emit('select')" @keydown.enter="$emit('select')">
    <div class="video-card__cover">
      <img v-if="video.poster_url && !coverFailed" :src="video.poster_url" :alt="video.title" @error="coverFailed = true" />
      <div v-else class="video-card__fallback" aria-hidden="true">▶</div>
      <span class="video-card__duration">{{ video.duration }}</span>
    </div>
    <div class="video-card__body">
      <h3>{{ video.title }}</h3>
      <div class="video-card__meta">
        <span v-if="video.categoryName" class="video-card__category">{{ video.categoryName }}</span>
        <time>{{ video.created_at }}</time>
      </div>
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
.video-card { overflow: hidden; cursor: pointer; border: var(--border-width-hairline, .5px) solid var(--td-component-stroke); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); transition: border-color .15s ease, background-color .15s ease, box-shadow .15s ease; }
.video-card:hover, .video-card:focus-visible { border-color: var(--td-component-border); background: var(--td-bg-color-container-hover); box-shadow: var(--td-shadow-1); outline: none; }
.video-card:focus-visible { border-color: var(--td-brand-color); }
.video-card__cover { position: relative; aspect-ratio: 16 / 9; overflow: hidden; background: var(--td-bg-color-component); }
.video-card__cover img { width: 100%; height: 100%; display: block; object-fit: cover; }
.video-card__fallback { width: 100%; height: 100%; display: grid; place-items: center; color: var(--td-brand-color); background: linear-gradient(135deg, var(--td-brand-color-1), var(--td-bg-color-component)); font-size: 36px; }
.video-card__duration { position: absolute; right: var(--td-comp-margin-s); bottom: var(--td-comp-margin-s); padding: 2px var(--td-comp-margin-s); border: 1px solid var(--td-component-stroke); border-radius: var(--td-radius-round); background: color-mix(in srgb, var(--td-bg-color-container) 88%, transparent); color: var(--td-text-color-primary); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); backdrop-filter: blur(8px); }
.video-card__body { padding: calc(var(--td-comp-margin-s) * 1.5) calc(var(--td-comp-margin-s) * 2) calc(var(--td-comp-margin-s) * 2); }
.video-card h3 { margin: 0 0 var(--td-comp-margin-s); overflow: hidden; color: var(--td-text-color-primary); font-size: var(--td-font-size-title-medium); font-weight: 400; line-height: var(--td-line-height-title-medium); text-overflow: ellipsis; white-space: nowrap; }
.video-card__meta { display: flex; align-items: center; gap: var(--td-comp-margin-s); min-width: 0; }
.video-card__category { flex: none; padding: 1px 6px; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); }
.video-card time { overflow: hidden; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); line-height: var(--td-line-height-body-small); text-overflow: ellipsis; white-space: nowrap; }
</style>
