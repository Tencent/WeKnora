<template>
  <svg :key="`${iconName}-${!!active}`" class="chat-timeline-icon" :class="{ 'is-active': active }" :data-icon="iconName"
    width="18" height="18" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"
    aria-hidden="true" focusable="false">
    <g class="icon-still">
      <path v-for="(path, index) in paths" :key="index" :d="path" />
    </g>
    <g v-if="active" class="icon-motion">
      <g v-for="(part, index) in motionParts" :key="index">
        <animateTransform v-if="part.transform" attributeName="transform" :type="part.transform.type"
          :values="part.transform.values.join(';')" :keyTimes="part.transform.times"
          :keySplines="part.transform.splines" calcMode="spline"
          :dur="TIMELINE_ICON_CYCLE" :begin="`${part.delay || 0}s`" repeatCount="indefinite" />
        <path :d="part.path" :pathLength="part.draw ? 1 : undefined"
          :stroke-dasharray="part.draw ? 1 : undefined" :stroke-dashoffset="part.draw ? 1 : undefined"
          :opacity="part.animations?.find(animation => animation.attribute === 'opacity')?.values[0]">
          <animate v-for="animation in part.animations" :key="animation.attribute"
            :attributeName="animation.attribute" :values="animation.values.join(';')"
            :keyTimes="animation.times" :keySplines="animation.splines" calcMode="spline"
            :dur="TIMELINE_ICON_CYCLE" :begin="`${part.delay || 0}s`" repeatCount="indefinite" />
        </path>
      </g>
    </g>
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getTimelineIconMotion, TIMELINE_ICON_CYCLE } from '../utils/chatTimelineIconMotion'

const props = defineProps<{ name: string; active?: boolean }>()

// One stroke and viewport for every timeline mark, including browser, agent
// and approval rows. Closed shapes use a 16-unit keyline; open symbols are
// inset further and tall silhouettes retain their natural proportions.
// Draw at these coordinates instead of scaling paths (and their strokes).
const bulb = [
  'M9 17v-1c0-1.2-.8-2.1-1.7-3a6.5 6.5 0 1 1 9.4 0c-.9.9-1.7 1.8-1.7 3v1H9Z',
  'M9 19h6m-5 2h4',
]
const file = 'M12 4H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-7l-7-7Zm0 0v7h7'
const circle = 'M20 12a8 8 0 1 1-16 0 8 8 0 0 1 16 0Z'
const icons: Record<string, string[]> = {
  lightbulb: bulb,
  'ai-search': bulb,
  bookmark: ['M18 20l-6-3-6 3V6a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v14Z'],
  search: ['M16 10a6 6 0 1 1-12 0 6 6 0 0 1 12 0Zm-1.5 4.5L20 20'],
  internet: [circle, 'M4 12h16', 'M12 4c-4 4-4 12 0 16 4-4 4-12 0-16Z'],
  'data-search': [
    'M4 7c0-4 13-4 13 0S4 11 4 7Zm0 0v10c0 2 3 3 6 3M4 12c0 2 3 3 6 3m7-8v3',
    'M19 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm-1 2 2 2',
  ],
  'file-search': [
    'M9 20H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h6l6 6M12 4v6h6',
    'M19 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm-1 2 2 2',
  ],
  file: [file],
  'file-paste': [file, 'M8 15h8', 'M8 18h5'],
  task: [
    'M9 5H7a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2',
    'M9 3h6v4H9V3Z', 'm8 12 1 1 2-2', 'M13 12h3m-8 5h8',
  ],
  folder: ['M4 8V6a2 2 0 0 1 2-2h3l2 3h7a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8Z'],
  edit: ['m14 5 5 5M5 19l4-1L19 8a2.8 2.8 0 0 0-4-4L5 14v5Z'],
  attach: ['m9 12 5-5a2.8 2.8 0 0 1 4 4l-7 7a4.2 4.2 0 0 1-6-6l8-8m-4 8a1.4 1.4 0 0 0 2 2l4-4'],
  terminal: ['m5 6 6 6-6 6', 'M14 18h5'],
  code: ['m8 7-4 5 4 5', 'm16 7 4 5-4 5', 'm13 4-2 16'],
  chat: ['M20 11a8 7 0 0 1-8 7H9l-5 3 1-6a7 7 0 0 1-1-4 8 7 0 0 1 16 0Z', 'M8 11h8'],
  agent: [
    'M15 8a3 3 0 1 1-6 0 3 3 0 0 1 6 0ZM6 20v-2a6 6 0 0 1 12 0v2',
    'M18 5a3 3 0 0 1 0 6m3 7v-1a5 5 0 0 0-2-4',
  ],
  browser: [
    'M9 19H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v5M4 9h16M7 6.5h.01M10 6.5h.01',
    'm12 12 3 8 1.5-3.5L20 15l-8-3Z',
  ],
  jump: ['M14 5h5v5m0-5-8 8', 'M10 5H7a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3'],
  compaction: ['M5 4h14M5 20h14', 'M8 7l4 4 4-4', 'M8 17l4-4 4 4'],
  ellipsis: ['M6 12h.01', 'M12 12h.01', 'M18 12h.01'],
  'check-circle': [circle, 'm8.5 12 2.5 2.5 4.5-5'],
  'play-circle': [circle, 'm10 8.5 5 3.5-5 3.5v-7Z'],
  time: [circle, 'M12 7v5l3 2'],
  'minus-circle': [circle, 'M8.5 12h7'],
  'close-circle': [circle, 'm9 9 6 6m-6 0 6-6'],
  'help-circle': [circle, 'M9.5 9a2.5 2.5 0 0 1 5 0c0 2-2.5 2-2.5 4m0 3h.01'],
  secured: ['M12 4 5 7v5c0 4 7 8 7 8s7-4 7-8V7l-7-3Z', 'm8.5 12 2.5 2.5 4.5-5'],
  'lock-on': [
    'M8 10V8a4 4 0 0 1 8 0v2M7 10h10a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-6a2 2 0 0 1 2-2Z',
    'M12 14v3',
  ],
  'chevron-right': ['m9 6 6 6-6 6'],
  'chevron-down': ['m6 9 6 6 6-6'],
}
const iconName = computed(() => Object.hasOwn(icons, props.name) ? props.name : 'file-paste')
const paths = computed(() => icons[iconName.value])
// A new SVG time container starts each run from its first frame. Completed
// records render only the original glyph; no animation survives in history.
const motionParts = computed(() => props.active ? getTimelineIconMotion(iconName.value, paths.value) : [])
</script>

<style scoped lang="less">
.icon-motion { display: none; }

// Opt in to movement. Reduced-motion users (and older media-query engines)
// see the exact static glyph, including its full checkmarks and text strokes.
@media (prefers-reduced-motion: no-preference) {
  .is-active > .icon-still { display: none; }
  .icon-motion { display: inline; }
}
</style>
