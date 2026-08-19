<template>
  <section ref="playerRoot" class="video-player">
    <video ref="video" :src="src" :poster="poster" preload="metadata" @loadedmetadata="onLoaded" @timeupdate="onTimeUpdate" @play="playing = true" @pause="playing = false" @error="hasError = true" @click="togglePlayback" />
    <div v-if="hasError" class="video-player__error">视频加载失败，请稍后重试</div>
    <div v-if="subtitlesEnabled && activeSubtitle" class="video-player__subtitle">{{ activeSubtitle.text }}</div>
    <div class="video-player__controls">
      <div class="video-player__progress">
        <input v-model.number="currentSeconds" type="range" min="0" :max="duration || durationHint" step="0.1" aria-label="视频进度" :title="formatTime(currentSeconds)" @input="seekTo(currentSeconds)" />
      </div>
      <div class="video-player__toolbar">
        <button type="button" :aria-label="playing ? '暂停' : '播放'" @click="togglePlayback">{{ playing ? 'Ⅱ' : '▶' }}</button>
        <button type="button" aria-label="后退 10 秒" @click="skip(-10)">−10s</button>
        <button type="button" aria-label="前进 10 秒" @click="skip(10)">+10s</button>
        <span>{{ formatTime(currentSeconds) }} / {{ formatTime(duration || durationHint) }}</span>
        <label class="video-player__volume">音量<input v-model.number="volume" type="range" min="0" max="1" step="0.05" aria-label="音量" @input="setVolume" /></label>
        <select v-model.number="playbackRate" aria-label="播放速度" @change="setPlaybackRate">
          <option v-for="rate in rates" :key="rate" :value="rate">{{ rate }}x</option>
        </select>
        <button type="button" :aria-pressed="subtitlesEnabled" @click="subtitlesEnabled = !subtitlesEnabled">字幕</button>
        <button type="button" aria-label="全屏" @click="toggleFullscreen">全屏</button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SubtitleCue } from '@/types/videohub'

const props = withDefaults(defineProps<{ src: string; poster?: string; subtitles?: SubtitleCue[]; durationHint?: number }>(), {
  poster: undefined,
  subtitles: () => [],
  durationHint: 0,
})
const emit = defineEmits<{ timeupdate: [seconds: number] }>()
const video = ref<HTMLVideoElement | null>(null)
const playerRoot = ref<HTMLElement | null>(null)
const playing = ref(false)
const currentSeconds = ref(0)
const duration = ref(0)
const volume = ref(1)
const playbackRate = ref(1)
const subtitlesEnabled = ref(true)
const hasError = ref(false)
const rates = [0.5, 0.75, 1, 1.25, 1.5, 2]
const activeSubtitle = computed(() => props.subtitles.find(cue => currentSeconds.value >= cue.start_seconds && currentSeconds.value < cue.end_seconds))

function clamp(seconds: number) {
  return Math.min(Math.max(Number.isFinite(seconds) ? seconds : 0, 0), duration.value || props.durationHint || 0)
}

function seekTo(seconds: number) {
  const next = clamp(seconds)
  currentSeconds.value = next
  if (video.value) video.value.currentTime = next
  emit('timeupdate', next)
}

function onLoaded() {
  duration.value = Number.isFinite(video.value?.duration) ? video.value?.duration || 0 : props.durationHint
  hasError.value = false
}

function onTimeUpdate() {
  currentSeconds.value = video.value?.currentTime || 0
  emit('timeupdate', currentSeconds.value)
}

async function togglePlayback() {
  if (!video.value) return
  if (video.value.paused) await video.value.play().catch(() => { hasError.value = true })
  else video.value.pause()
}

function skip(offset: number) { seekTo(currentSeconds.value + offset) }
function setVolume() { if (video.value) video.value.volume = volume.value }
function setPlaybackRate() { if (video.value) video.value.playbackRate = playbackRate.value }
async function toggleFullscreen() {
  if (document.fullscreenElement) await document.exitFullscreen()
  else await playerRoot.value?.requestFullscreen()
}
function formatTime(value: number) {
  const safe = Math.max(0, Math.floor(value || 0))
  return `${String(Math.floor(safe / 60)).padStart(2, '0')}:${String(safe % 60).padStart(2, '0')}`
}

defineExpose({ seekTo })
</script>

<style scoped>
.video-player { position: relative; overflow: hidden; aspect-ratio: 16 / 9; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-component); color: var(--td-text-color-anti); }
.video-player video { width: 100%; height: 100%; display: block; object-fit: contain; background: linear-gradient(135deg, var(--td-brand-color-1), var(--td-bg-color-component)); }
.video-player__error { position: absolute; inset: 0; display: grid; place-items: center; background: var(--td-bg-color-component); color: var(--td-error-color); }
.video-player__subtitle { position: absolute; left: 50%; bottom: 72px; max-width: 76%; transform: translateX(-50%); padding: 6px 12px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-bg-color-container) 88%, transparent); color: var(--td-text-color-primary); font-size: 14px; line-height: 1.57; backdrop-filter: blur(8px); text-align: center; }
.video-player__controls { position: absolute; right: 0; bottom: 0; left: 0; padding: 20px 12px 10px; background: linear-gradient(transparent, color-mix(in srgb, var(--td-bg-color-component) 94%, transparent)); }
.video-player__progress input { width: 100%; accent-color: var(--td-brand-color); }
.video-player__toolbar { display: flex; align-items: center; gap: 4px; color: var(--td-text-color-primary); font-size: 12px; line-height: 1.67; }
.video-player button, .video-player select { min-height: 28px; border: 1px solid transparent; border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-bg-color-container) 88%, transparent); color: var(--td-text-color-primary); cursor: pointer; backdrop-filter: blur(8px); }
.video-player button:hover, .video-player select:hover { border-color: var(--td-border-level-1-color); background: var(--td-bg-color-container-hover); }
.video-player button[aria-pressed="true"] { border-color: var(--td-brand-color); color: var(--td-brand-color); }
.video-player button { padding: 0 8px; }.video-player select { padding: 0 4px; }
.video-player__toolbar > span { margin-right: auto; }
.video-player__volume { display: flex; align-items: center; gap: 5px; }.video-player__volume input { width: 72px; accent-color: var(--td-brand-color); }
@media (max-width: 760px) { .video-player__volume, .video-player__toolbar > span { display: none; } .video-player__toolbar { overflow-x: auto; } }
</style>
