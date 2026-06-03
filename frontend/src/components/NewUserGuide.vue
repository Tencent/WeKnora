<template>
  <Teleport to="body">
    <Transition name="guide-fade">
      <div v-if="active" class="guide" role="dialog" aria-modal="true"
        :aria-label="t('newUserGuide.steps.welcome.title')" @keydown.esc.prevent="dismiss" @keydown.left.prevent="prev"
        @keydown.right.prevent="next" tabindex="-1" ref="rootRef">
        <!-- 四块暗色挡板围住高亮区：中间留出真实空洞（DOM 上无任何元素），
             点击直接穿透到下层控件；四周挡板拦截点击，避免引导期间误触。 -->
        <template v-if="hole">
          <!-- box-shadow 镂空：内缘与描边同为圆角，避免矩形挖洞 + 圆角描边的直角缺口 -->
          <div class="guide__spot" :style="spotStyle" aria-hidden="true" />
          <div v-for="(piece, i) in backdropPieces" :key="i" class="guide__backdrop guide__backdrop--hit"
            :style="piece" />
        </template>
        <div v-else class="guide__backdrop guide__backdrop--full" />

        <!-- 高亮描边 -->
        <div v-if="hole" class="guide__ring" :style="ringStyle" aria-hidden="true" />

        <!-- 说明卡片 -->
        <div ref="cardRef" class="guide__card" :class="{ 'guide__card--center': !hole }" :style="cardStyle">
          <button type="button" class="guide__close" :aria-label="t('newUserGuide.skip')" @click="dismiss">
            <t-icon name="close" size="18px" />
          </button>

          <div class="guide__progress">
            <span v-for="(s, i) in steps" :key="s.key" class="guide__dot"
              :class="{ 'is-active': i === index, 'is-done': i < index }" />
          </div>

          <p class="guide__step-label">{{ t('newUserGuide.stepOf', { current: index + 1, total: steps.length }) }}</p>
          <h3 class="guide__title">{{ stepTitle }}</h3>
          <p class="guide__desc">{{ stepDesc }}</p>

          <div class="guide__actions">
            <button type="button" class="guide__skip" @click="dismiss">{{ t('newUserGuide.skip') }}</button>
            <div class="guide__actions-main">
              <t-button v-if="index > 0" size="small" variant="outline" @click="prev">
                {{ t('newUserGuide.prev') }}
              </t-button>
              <t-button v-if="!isLast" size="small" theme="primary" @click="next">
                {{ t('newUserGuide.next') }}
              </t-button>
              <t-button v-else size="small" theme="primary" @click="finish">
                {{ t('newUserGuide.done') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'

const STORAGE_KEY = 'weknora:new-user-guide-done:v1'
const OPEN_EVENT = 'weknora:open-new-user-guide'
const CARD_WIDTH = 340
const GAP = 16
const EDGE = 16
const PAD = 8
/** 与侧栏 menu_item（4px）+ 内边距协调，略大于目标圆角即可 */
const holeRadius = 8
const BACKDROP_COLOR = 'rgba(15, 18, 22, 0.58)'

type Placement = 'right' | 'left' | 'bottom' | 'top'

interface GuideStep {
  key: string
  /** 高亮目标的 CSS 选择器；缺省表示居中卡片（欢迎 / 结束） */
  target?: string
  /** 优先放置方位 */
  placement?: Placement
  /** 进入该步骤前的准备动作（如展开侧栏、切换路由） */
  before?: () => void | Promise<void>
  /** 目标不存在时是否跳过该步骤（角色/版本可能隐藏入口） */
  optional?: boolean
}

const { t } = useI18n()
const uiStore = useUIStore()

let settingsOpenedByGuide = false

// 顺序按真实使用依赖编排：先配模型（硬前置）→ 建知识库 → 对话 → 智能体
// → 设置入口 → 完成。
const steps = computed<GuideStep[]>(() => [
  { key: 'welcome' },
  {
    key: 'knowledge',
    target: '[data-guide="nav-knowledge-bases"]',
    placement: 'right',
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'agents',
    target: '[data-guide="nav-agents"]',
    placement: 'right',
    optional: true,
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'chat',
    target: '[data-guide="nav-creatChat"]',
    placement: 'right',
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'settings',
    target: '[data-guide="user-menu"]',
    placement: 'right',
    before: () => uiStore.expandSidebar(),
  },
  {
    // 模型配置在「设置」弹窗内：引导自动打开弹窗并高亮「添加模型」。
    key: 'models',
    target: '[data-guide="settings-add-model"], [data-guide="settings-models"]',
    placement: 'left',
    before: () => {
      uiStore.openSettings('models')
      settingsOpenedByGuide = true
    },
  },
  { key: 'done' },
])

const active = ref(false)
const index = ref(0)
const vw = ref(window.innerWidth)
const vh = ref(window.innerHeight)
const targetRect = ref<DOMRect | null>(null)
const targetEl = ref<HTMLElement | null>(null)
const cardSize = ref({ width: CARD_WIDTH, height: 220 })

type HoleRect = { x: number; y: number; width: number; height: number }

/** 目标与相邻节点之间的可用间距（不含 margin 折叠到 border 外的部分） */
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

/**
 * 高亮框四边留白一致：受相邻项间距限制时同步缩小；
 * 贴边 clamp 时同步收窄宽高，避免侧栏靠左时「左贴边、右留白」。
 */
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

const step = computed(() => steps.value[index.value] ?? steps.value[0])
const isLast = computed(() => index.value === steps.value.length - 1)
const stepTitle = computed(() => t(`newUserGuide.steps.${step.value.key}.title`))
const stepDesc = computed(() => t(`newUserGuide.steps.${step.value.key}.desc`))

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
    // 上
    { top: '0px', left: '0px', width: `${w}px`, height: `${h.y}px` },
    // 下
    { top: `${h.y + h.height}px`, left: '0px', width: `${w}px`, height: `${Math.max(0, v - h.y - h.height)}px` },
    // 左
    { top: `${h.y}px`, left: '0px', width: `${h.x}px`, height: `${h.height}px` },
    // 右
    { top: `${h.y}px`, left: `${h.x + h.width}px`, width: `${Math.max(0, w - h.x - h.width)}px`, height: `${h.height}px` },
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

  // 兜底：底部居中
  const left = Math.min(Math.max(EDGE, (vw.value - w) / 2), vw.value - w - EDGE)
  const top = Math.min(Math.max(EDGE, holeBox.bottom + GAP), vh.value - h - EDGE)
  return { width: `${w}px`, left: `${left}px`, top: `${top}px` }
})

// 支持以逗号分隔的多个候选选择器，按书写顺序优先匹配（用于「优先高亮按钮、
// 否则退化为标题」这类带降级的目标）。
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
    // 找不到目标：可选步骤跳过，否则退化为居中卡片
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

const closeGuideSettings = () => {
  if (settingsOpenedByGuide) {
    uiStore.closeSettings()
    settingsOpenedByGuide = false
  }
}

const goTo = async (i: number) => {
  if (i < 0 || i >= steps.value.length) return
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  index.value = i
  // 离开模型步骤时，关闭由引导自己打开的设置弹窗
  if (step.value.key !== 'models') {
    closeGuideSettings()
  }
  await step.value.before?.()
  // 等路由/侧栏/弹窗过渡稳定
  await new Promise((r) => setTimeout(r, step.value.before ? 280 : 0))
  await locate()
  await nextTick()
  rootRef.value?.focus()
}

const next = () => goTo(index.value + 1)
const prev = () => goTo(index.value - 1)

const open = async () => {
  active.value = true
  index.value = 0
  await nextTick()
  await goTo(0)
}

const close = () => {
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  closeGuideSettings()
  active.value = false
  targetEl.value = null
  targetRect.value = null
}

const finish = () => {
  localStorage.setItem(STORAGE_KEY, '1')
  close()
}

const dismiss = () => finish()

const onViewportChange = () => {
  if (!active.value) return
  locate()
}

const handleOpenEvent = () => {
  if (active.value) return
  open()
}

onMounted(() => {
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
  window.addEventListener(OPEN_EVENT, handleOpenEvent)
  if (localStorage.getItem(STORAGE_KEY) !== '1') {
    window.setTimeout(() => {
      if (localStorage.getItem(STORAGE_KEY) !== '1') open()
    }, 700)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
  window.removeEventListener(OPEN_EVENT, handleOpenEvent)
  closeGuideSettings()
  if (retryTimer) clearTimeout(retryTimer)
})
</script>

<style lang="less" scoped>
.guide {
  position: fixed;
  inset: 0;
  z-index: 5000;
  outline: none;
  pointer-events: none;
}

// 四块透明挡板拦截点击；中间空洞无元素，点击穿透到下层控件。
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

// 仅负责圆角遮罩绘制；不拦截指针（box-shadow 本身也不参与命中）。
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
