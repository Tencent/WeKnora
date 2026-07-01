import { ref, onMounted, onUnmounted, readonly } from 'vue'

/**
 * 响应式断点检测 composable
 *
 * 断点定义：
 *  - isMobile:  < 768px  (安卓手机)
 *  - isTablet:  768-1023px (平板)
 *  - isDesktop: ≥ 1024px (PC)
 *
 * 使用方式：
 *   const { isMobile, isTablet, isDesktop } = useBreakpoint()
 *
 * 组件卸载时自动移除监听器。
 */

const MOBILE_MAX = 767
const TABLET_MAX = 1023

// 全局共享状态：全应用保持唯一 matchMedia 监听器
// 避免每个组件都创建自己的 MediaQueryList
let listeners = 0
let mobileMQL: MediaQueryList | null = null
let tabletMQL: MediaQueryList | null = null

const isMobile = ref(false)
const isTablet = ref(false)
const isDesktop = ref(true)

function evalBreakpoint() {
  // SSR / 测试环境安全回退
  if (typeof window === 'undefined' || !window.matchMedia) {
    isMobile.value = false
    isTablet.value = false
    isDesktop.value = true
    return
  }

  const width = window.innerWidth
  isMobile.value = width <= MOBILE_MAX
  isTablet.value = width > MOBILE_MAX && width <= TABLET_MAX
  isDesktop.value = width > TABLET_MAX
}

function onMobileChange(e: MediaQueryListEvent) {
  evalBreakpoint()
}

function onTabletChange(e: MediaQueryListEvent) {
  evalBreakpoint()
}

function startListening() {
  if (typeof window === 'undefined' || !window.matchMedia) return

  if (listeners === 0) {
    mobileMQL = window.matchMedia(`(max-width: ${MOBILE_MAX}px)`)
    tabletMQL = window.matchMedia(`(min-width: ${MOBILE_MAX + 1}px) and (max-width: ${TABLET_MAX}px)`)

    mobileMQL.addEventListener('change', onMobileChange)
    tabletMQL.addEventListener('change', onTabletChange)
  }
  listeners++

  // 立即评估一次
  evalBreakpoint()
}

function stopListening() {
  if (typeof window === 'undefined') return

  listeners--
  if (listeners <= 0) {
    listeners = 0
    mobileMQL?.removeEventListener('change', onMobileChange)
    tabletMQL?.removeEventListener('change', onTabletChange)
    mobileMQL = null
    tabletMQL = null
  }
}

export function useBreakpoint() {
  // 首次使用时启动监听
  if (listeners === 0) {
    startListening()
  }

  // 但这里有个问题：如果页面没有组件调用 useBreakpoint，监听不会启动。
  // 解决方案：在应用入口处（App.vue onMounted）主动调用 evalBreakpoint + startListening
  // 这里只做组件级别的清理。

  onMounted(() => {
    if (listeners === 0) {
      startListening()
    }
  })

  onUnmounted(() => {
    stopListening()
  })

  return {
    isMobile: readonly(isMobile),
    isTablet: readonly(isTablet),
    isDesktop: readonly(isDesktop),
    // 暴露手动刷新，供 App.vue 初始化 / resize 兜底
    refresh: evalBreakpoint,
    startListening,
  } as const
}

/**
 * 供 store 等非组件代码使用的直接访问接口
 * 注意：值始终是最新的（共享同一个 ref），
 * 但在应用未挂载任何 useBreakpoint 之前不会自动更新。
 * App.vue onMounted 会启动全局监听。
 */
export { isMobile, isTablet, isDesktop, evalBreakpoint, startListening }

export { MOBILE_MAX, TABLET_MAX }
