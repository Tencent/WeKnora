<template>
  <section ref="playerRoot" class="video-player">
    <video ref="video" :src="src" :poster="poster" preload="metadata" @loadedmetadata="onLoaded" @timeupdate="onTimeUpdate" @play="playing = true" @pause="playing = false" @error="hasError = true" @click="togglePlayback" />
    <div v-if="title || chapterLabel" class="video-player__copy" aria-hidden="true">
      <strong v-if="title">{{ title }}</strong>
      <span v-if="chapterLabel">{{ chapterLabel }}</span>
    </div>
    <button v-if="!playing && !hasError" class="video-player__center-toggle" type="button" aria-label="播放" @click.stop="togglePlayback">
      <t-icon name="play" />
    </button>
    <div v-if="hasError" class="video-player__error">视频加载失败，请稍后重试</div>
    <div v-if="subtitlesEnabled && activeSubtitle" class="video-player__subtitle">{{ activeSubtitle.text }}</div>
    <div class="video-player__controls">
      <div class="video-player__progress">
        <input v-model.number="currentSeconds" type="range" min="0" :max="duration || durationHint" step="0.1" aria-label="视频进度" :title="formatTime(currentSeconds)" :style="{ background: progressTrack }" @input="seekTo(currentSeconds)" />
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

const props = withDefaults(defineProps<{ src: string; poster?: string; title?: string; chapterLabel?: string; subtitles?: SubtitleCue[]; durationHint?: number }>(), {
  poster: undefined,
  title: '',
  chapterLabel: '',
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
const progressPercent = computed(() => {
  const total = duration.value || props.durationHint
  return total > 0 ? Math.min(100, Math.max(0, (currentSeconds.value / total) * 100)) : 0
})
const progressTrack = computed(() => `linear-gradient(to right, var(--td-brand-color) 0%, var(--td-brand-color) ${progressPercent.value}%, rgba(255,255,255,.38) ${progressPercent.value}%, rgba(255,255,255,.38) 100%)`)

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
.video-player { position: relative; overflow: hidden; aspect-ratio: 16 / 9; border: 1px solid rgba(255,255,255,.9); border-radius: var(--td-radius-large); background: #20352e; color: var(--td-text-color-anti); }
.video-player::after { content: ''; position: absolute; z-index: 1; inset: 0; pointer-events: none; background: linear-gradient(180deg, rgba(9,19,15,.02) 35%, rgba(9,19,15,.68)); }
.video-player video { position: relative; z-index: 0; width: 100%; height: 100%; display: block; object-fit: contain; background: #20352e; }
.video-player__copy { position: absolute; z-index: 2; top: 22px; left: 24px; color: rgba(255,255,255,.92); pointer-events: none; }
.video-player__copy strong { display: block; font-size: 18px; font-weight: 500; line-height: 1.45; }
.video-player__copy span { display: block; margin-top: 5px; color: rgba(255,255,255,.7); font-size: 12px; line-height: 1.5; }
.video-player__center-toggle { position: absolute; z-index: 3; inset: 0; width: 64px; height: 64px; min-height: 0 !important; margin: auto; padding: 0 !important; border: 0 !important; border-radius: 50% !important; background: transparent !important; color: #fff; font-size: 44px; text-shadow: 0 2px 12px rgba(0,0,0,.28); cursor: pointer; }
.video-player__center-toggle:hover { color: rgba(255,255,255,.82); }
.video-player__error { position: absolute; z-index: 5; inset: 0; display: grid; place-items: center; background: #20352e; color: var(--td-error-color); }
.video-player__subtitle { position: absolute; z-index: 3; left: 50%; bottom: 72px; max-width: 76%; transform: translateX(-50%); padding: 6px 12px; border: 1px solid rgba(255,255,255,.6); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.78); color: var(--td-text-color-primary); font-size: 14px; line-height: 1.57; backdrop-filter: blur(8px); text-align: center; }
.video-player__controls { position: absolute; z-index: 4; right: 0; bottom: 0; left: 0; display: grid; gap: 9px; padding: 20px 18px 13px; background: transparent; }
.video-player__progress input { display: block; width: 100%; height: 4px; margin: 0; appearance: none; border: 0; border-radius: 3px; outline: 0; cursor: pointer; }
.video-player__progress input::-webkit-slider-thumb { width: 10px; height: 10px; appearance: none; border: 0; border-radius: 50%; background: var(--td-brand-color); opacity: 0; transition: opacity .15s ease; }
.video-player__progress input:hover::-webkit-slider-thumb, .video-player__progress input:focus-visible::-webkit-slider-thumb { opacity: 1; }
.video-player__progress input::-moz-range-thumb { width: 10px; height: 10px; border: 0; border-radius: 50%; background: var(--td-brand-color); opacity: 0; }
.video-player__toolbar { display: flex; align-items: center; gap: 4px; color: rgba(255,255,255,.88); font-size: 12px; line-height: 1.67; }
.video-player button, .video-player select { min-height: 26px; border: 0; border-radius: var(--td-radius-medium); background: transparent; color: rgba(255,255,255,.9); cursor: pointer; }
.video-player button:hover, .video-player select:hover { background: rgba(0,0,0,.3); }
.video-player button[aria-pressed="true"] { color: var(--td-brand-color-2); }
.video-player button { padding: 0 8px; }.video-player select { padding: 0 4px; }
.video-player__toolbar > span { margin-right: auto; color: inherit; }
.video-player__volume { display: flex; align-items: center; gap: 5px; color: inherit; }.video-player__volume input { width: 72px; accent-color: var(--td-brand-color); }
@media (max-width: 760px) { .video-player__volume, .video-player__toolbar > span { display: none; } .video-player__toolbar { overflow-x: auto; } }
</style>
