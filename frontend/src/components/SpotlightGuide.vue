<template>
  <Teleport to="body">
    <Transition name="guide-fade">
      <div v-if="active" class="guide" role="dialog" aria-modal="true" :aria-label="stepTitle"
        @keydown.esc.prevent="dismiss" @keydown.left.prevent="prev" @keydown.right.prevent="next" tabindex="-1"
        ref="rootRef">
        <template v-if="hole">
          <div class="guide__spot" :style="spotStyle" aria-hidden="true" />
          <div v-for="(piece, i) in backdropPieces" :key="i" class="guide__backdrop guide__backdrop--hit"
            :style="piece" />
        </template>
        <div v-else class="guide__backdrop guide__backdrop--full" />

        <div v-if="hole" class="guide__ring" :style="ringStyle" aria-hidden="true" />

        <div ref="cardRef" class="guide__card" :class="{ 'guide__card--center': !hole }" :style="cardStyle">
          <button type="button" class="guide__close" :aria-label="t(`${labelsPrefix}.skip`)" @click="dismiss">
            <t-icon name="close" size="18px" />
          </button>

          <div class="guide__progress">
            <span v-for="(s, i) in steps" :key="s.key" class="guide__dot"
              :class="{ 'is-active': i === index, 'is-done': i < index }" />
          </div>

          <p class="guide__step-label">{{ t(`${labelsPrefix}.stepOf`, { current: index + 1, total: steps.length }) }}
          </p>
          <h3 class="guide__title">{{ stepTitle }}</h3>
          <p class="guide__desc">{{ stepDesc }}</p>
          <p v-if="step.interact" class="guide__interact-hint">{{ t(`${labelsPrefix}.interactHint`) }}</p>

          <div class="guide__actions">
            <button type="button" class="guide__skip" @click="dismiss">{{ t(`${labelsPrefix}.skip`) }}</button>
            <div v-if="!step.interact" class="guide__actions-main">
              <t-button v-if="index > 0" size="small" variant="outline" @click="prev">
                {{ t(`${labelsPrefix}.prev`) }}
              </t-button>
              <t-button v-if="!isLast" size="small" theme="primary" @click="next">
                {{ t(`${labelsPrefix}.next`) }}
              </t-button>
              <t-button v-else size="small" theme="primary" @click="finish">
                {{ t(`${labelsPrefix}.done`) }}
              </t-button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const CARD_WIDTH = 340
const GAP = 16
const EDGE = 16
const PAD = 8
const holeRadius = 8
const BACKDROP_COLOR = 'rgba(15, 18, 22, 0.58)'

const props = withDefaults(
  defineProps<{
    active: boolean
    steps: SpotlightGuideStep[]
    /** i18n 前缀，步骤文案为 `${stepI18nPrefix}.${key}.title|desc` */
    stepI18nPrefix: string
    /** skip/prev/next/done/stepOf 所在前缀，默认 newUserGuide */
    labelsPrefix?: string
    /** 每步 before 执行后的等待毫秒数 */
    beforeDelayMs?: number
  }>(),
  {
    labelsPrefix: 'newUserGuide',
    beforeDelayMs: 280,
  },
)

const emit = defineEmits<{
  'update:active': [value: boolean]
  finish: []
  dismiss: []
  'step-change': [payload: { fromKey?: string; toKey: string; index: number }]
}>()

const { t } = useI18n()

const index = ref(0)
const vw = ref(window.innerWidth)
const vh = ref(window.innerHeight)
const targetRect = ref<DOMRect | null>(null)
const targetEl = ref<HTMLElement | null>(null)
const cardSize = ref({ width: CARD_WIDTH, height: 220 })

type HoleRect = { x: number; y: number; width: number; height: number }

const measureNeighborGap = (el: HTMLElement, r: DOMRect) => {
  let above = PAD
  const prev = el.previousElementSibling
  if (prev) {
    above = Math.max(0, r.top - prev.getBoundingClientRect().bottom)
  }

  let below = PAD
  const next = el.nextElementSibling
  if (next) {
    below = Math.max(0, next.getBoundingClientRect().top - r.bottom)
  } else {
    const mb = parseFloat(getComputedStyle(el).marginBottom) || 0
    below = Math.max(0, PAD - mb)
  }

  return { above, below }
}

const computeHighlightHole = (el: HTMLElement, r: DOMRect): HoleRect => {
  const { above, below } = measureNeighborGap(el, r)
  const inset = Math.min(PAD, above, below)

  let x = r.left - inset
  let y = r.top - inset
  let width = r.width + inset * 2
  let height = r.height + inset * 2

  if (x < 0) {
    width += x
    x = 0
  }
  if (y < 0) {
    height += y
    y = 0
  }
  const rightOverflow = x + width - vw.value
  if (rightOverflow > 0) {
    width -= rightOverflow
  }
  const bottomOverflow = y + height - vh.value
  if (bottomOverflow > 0) {
    height -= bottomOverflow
  }

  return { x, y, width, height }
}

const rootRef = ref<HTMLElement | null>(null)
const cardRef = ref<HTMLElement | null>(null)

let retryTimer: ReturnType<typeof setTimeout> | null = null

const step = computed(() => props.steps[index.value] ?? props.steps[0])
const isLast = computed(() => index.value === props.steps.length - 1)
const stepTitle = computed(() => t(`${props.stepI18nPrefix}.${step.value.key}.title`))
const stepDesc = computed(() => t(`${props.stepI18nPrefix}.${step.value.key}.desc`))

const hole = computed(() => {
  const el = targetEl.value
  const r = targetRect.value
  if (!el || !r) return null
  return computeHighlightHole(el, r)
})

const backdropPieces = computed(() => {
  const h = hole.value
  if (!h) return []
  const w = vw.value
  const v = vh.value
  return [
    { top: '0px', left: '0px', width: `${w}px`, height: `${h.y}px` },
    {
      top: `${h.y + h.height}px`,
      left: '0px',
      width: `${w}px`,
      height: `${Math.max(0, v - h.y - h.height)}px`,
    },
    { top: `${h.y}px`, left: '0px', width: `${h.x}px`, height: `${h.height}px` },
    {
      top: `${h.y}px`,
      left: `${h.x + h.width}px`,
      width: `${Math.max(0, w - h.x - h.width)}px`,
      height: `${h.height}px`,
    },
  ]
})

const holeFrameStyle = computed(() => {
  if (!hole.value) return {}
  return {
    left: `${hole.value.x}px`,
    top: `${hole.value.y}px`,
    width: `${hole.value.width}px`,
    height: `${hole.value.height}px`,
    borderRadius: `${holeRadius}px`,
  }
})

const spotStyle = computed(() => ({
  ...holeFrameStyle.value,
  boxShadow: `0 0 0 9999px ${BACKDROP_COLOR}`,
}))

const ringStyle = holeFrameStyle

const overlaps = (
  a: { left: number; top: number; right: number; bottom: number },
  b: { left: number; top: number; right: number; bottom: number },
) => !(a.right <= b.left || a.left >= b.right || a.bottom <= b.top || a.top >= b.bottom)

const cardStyle = computed(() => {
  const w = Math.min(CARD_WIDTH, vw.value - EDGE * 2)
  const h = cardSize.value.height
  const h0 = hole.value

  if (!h0) {
    return {
      width: `${w}px`,
      left: `${(vw.value - w) / 2}px`,
      top: `${Math.max(EDGE, vh.value * 0.32 - h / 2)}px`,
    }
  }

  const holeBox = { left: h0.x, top: h0.y, right: h0.x + h0.width, bottom: h0.y + h0.height }
  type Placement = 'right' | 'left' | 'bottom' | 'top'
  const order: Placement[] = (() => {
    const pref = step.value.placement ?? 'right'
    const all: Placement[] = ['right', 'left', 'bottom', 'top']
    return [pref, ...all.filter((p) => p !== pref)]
  })()

  const candidates: Record<Placement, { left: number; top: number }> = {
    right: { left: holeBox.right + GAP, top: h0.y + h0.height / 2 - h / 2 },
    left: { left: holeBox.left - w - GAP, top: h0.y + h0.height / 2 - h / 2 },
    bottom: { left: h0.x + h0.width / 2 - w / 2, top: holeBox.bottom + GAP },
    top: { left: h0.x + h0.width / 2 - w / 2, top: holeBox.top - h - GAP },
  }

  for (const place of order) {
    const c = candidates[place]
    const left = Math.min(Math.max(EDGE, c.left), vw.value - w - EDGE)
    const top = Math.min(Math.max(EDGE, c.top), vh.value - h - EDGE)
    const cardBox = { left, top, right: left + w, bottom: top + h }
    if (!overlaps(cardBox, holeBox)) {
      return { width: `${w}px`, left: `${left}px`, top: `${top}px` }
    }
  }

  const left = Math.min(Math.max(EDGE, (vw.value - w) / 2), vw.value - w - EDGE)
  const top = Math.min(Math.max(EDGE, holeBox.bottom + GAP), vh.value - h - EDGE)
  return { width: `${w}px`, left: `${left}px`, top: `${top}px` }
})

const queryTarget = (selector?: string): HTMLElement | null => {
  if (!selector) return null
  for (const part of selector.split(',').map((s) => s.trim()).filter(Boolean)) {
    const el = document.querySelector<HTMLElement>(part)
    if (!el) continue
    const r = el.getBoundingClientRect()
    if (r.width > 2 && r.height > 2) return el
  }
  return null
}

const measureCard = async () => {
  await nextTick()
  if (cardRef.value) {
    cardSize.value = {
      width: cardRef.value.offsetWidth,
      height: cardRef.value.offsetHeight,
    }
  }
}

const locate = async (retry = 0) => {
  vw.value = window.innerWidth
  vh.value = window.innerHeight

  const cur = step.value
  if (!cur.target) {
    targetEl.value = null
    targetRect.value = null
    await measureCard()
    return
  }

  const el = queryTarget(cur.target)
  if (!el) {
    if (retry < 12) {
      if (retryTimer) clearTimeout(retryTimer)
      retryTimer = setTimeout(() => locate(retry + 1), 120)
      return
    }
    if (cur.optional) {
      goTo(index.value + 1)
      return
    }
    targetEl.value = null
    targetRect.value = null
    await measureCard()
    return
  }

  el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' })
  targetEl.value = el
  targetRect.value = el.getBoundingClientRect()
  await measureCard()
}

const goTo = async (i: number) => {
  if (i < 0 || i >= props.steps.length) return
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  const fromKey = props.steps[index.value]?.key
  index.value = i
  emit('step-change', { fromKey, toKey: step.value.key, index: i })
  await step.value.before?.()
  const delay = step.value.before ? props.beforeDelayMs : 0
  if (delay > 0) {
    await new Promise((r) => setTimeout(r, delay))
  }
  await locate()
  await nextTick()
  rootRef.value?.focus()
}

const next = () => goTo(index.value + 1)
const prev = () => goTo(index.value - 1)

const close = () => {
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  emit('update:active', false)
  targetEl.value = null
  targetRect.value = null
}

const finish = () => {
  emit('finish')
  close()
}

const dismiss = () => {
  emit('dismiss')
  finish()
}

const onViewportChange = () => {
  if (!props.active) return
  locate()
}

const open = async () => {
  index.value = 0
  emit('update:active', true)
  await nextTick()
  await goTo(0)
}

watch(
  () => props.active,
  (val, old) => {
    if (val && !old) {
      open()
    } else if (!val && old) {
      if (retryTimer) {
        clearTimeout(retryTimer)
        retryTimer = null
      }
    }
  },
)

onBeforeUnmount(() => {
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
  if (retryTimer) clearTimeout(retryTimer)
})

watch(
  () => props.active,
  (val) => {
    if (val) {
      window.addEventListener('resize', onViewportChange)
      window.addEventListener('scroll', onViewportChange, true)
    } else {
      window.removeEventListener('resize', onViewportChange)
      window.removeEventListener('scroll', onViewportChange, true)
    }
  },
  { immediate: true },
)

defineExpose({ open, close })
</script>

<style lang="less" scoped>
.guide {
  position: fixed;
  inset: 0;
  z-index: 5000;
  outline: none;
  pointer-events: none;
}

.guide__backdrop {
  position: fixed;
  pointer-events: auto;
  background: rgba(15, 18, 22, 0.58);
  transition:
    top 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    left 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    width 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    height 0.28s cubic-bezier(0.4, 0, 0.2, 1);

  &--full {
    inset: 0;
  }

  &--hit {
    background: transparent;
  }
}

.guide__spot {
  position: fixed;
  box-sizing: border-box;
  pointer-events: none;
  background: transparent;
  transition:
    top 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    left 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    width 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    height 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    border-radius 0.28s cubic-bezier(0.4, 0, 0.2, 1);
}

.guide__ring {
  position: fixed;
  box-sizing: border-box;
  pointer-events: none;
  border: 2px solid var(--td-brand-color);
  box-shadow: 0 0 0 4px rgba(7, 192, 95, 0.18);
  transition:
    top 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    left 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    width 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    height 0.28s cubic-bezier(0.4, 0, 0.2, 1);
}

.guide__card {
  position: fixed;
  z-index: 1;
  pointer-events: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 18px 18px 14px;
  border-radius: 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  box-shadow: 0 20px 48px rgba(0, 0, 0, 0.18);
  color: var(--td-text-color-primary);
  max-height: calc(100vh - 32px);
  overflow-y: auto;
  transition:
    top 0.28s cubic-bezier(0.4, 0, 0.2, 1),
    left 0.28s cubic-bezier(0.4, 0, 0.2, 1);

  &--center {
    max-width: calc(100vw - 32px);
  }
}

.guide__close {
  position: absolute;
  top: 10px;
  right: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.guide__progress {
  display: flex;
  gap: 5px;
}

.guide__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-bg-color-component);
  transition: width 0.2s ease, background 0.2s ease;

  &.is-active {
    width: 16px;
    border-radius: 999px;
    background: var(--td-brand-color);
  }

  &.is-done {
    background: rgba(7, 192, 95, 0.4);
  }
}

.guide__step-label {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.guide__title {
  margin: 0;
  padding-right: 24px;
  font-size: 18px;
  font-weight: 600;
  line-height: 26px;
}

.guide__desc {
  margin: 0;
  font-size: 14px;
  line-height: 22px;
  color: var(--td-text-color-secondary);
}

.guide__interact-hint {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--td-brand-color);
  font-weight: 500;
}

.guide__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
  padding-top: 10px;
  border-top: 1px solid var(--td-component-stroke);
}

.guide__skip {
  border: none;
  background: transparent;
  padding: 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-secondary);
  }
}

.guide__actions-main {
  display: flex;
  gap: 8px;
}

.guide-fade-enter-active,
.guide-fade-leave-active {
  transition: opacity 0.2s ease;
}

.guide-fade-enter-from,
.guide-fade-leave-to {
  opacity: 0;
}

@media (max-width: 720px) {
  .guide__card {
    left: 16px !important;
    right: 16px;
    width: auto !important;
    top: auto !important;
    bottom: 16px;
    max-height: 56vh;
  }
}
</style>
