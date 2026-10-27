import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import { brandConfig } from '@/brand'
import { productLineStorageKey, resolveLine } from '@/brand/productLines'
import type { ProductLine } from '@/brand/types'
import { useAuthStore } from '@/stores/auth'

/**
 * 客户当前选的产线。
 *
 * 产线切换本期只影响界面（选中态、欢迎区、示例问题），不切向量库、不重建会话；
 * 这里刻意不调用 settingsStore.selectAgent，避免把产线状态和会话/知识库/文件选择绑在一起（AC-18 / C7）。
 */
export const useProductLineStore = defineStore('productLine', () => {
  const authStore = useAuthStore()

  /** 产线数据只有一个来源：运行时品牌配置（T01 已校验并冻结）。 */
  const lines = computed<readonly ProductLine[]>(() => brandConfig.productLines)

  const currentLineId = ref('')

  /** 未登录时返回空串：不读也不写共享键，避免不同账号串数据。 */
  function currentStorageKey(): string {
    const userId = authStore.currentUserId
    return userId ? productLineStorageKey(userId) : ''
  }

  function persist(lineId: string): void {
    const key = currentStorageKey()
    if (!key || typeof localStorage === 'undefined') return
    try {
      localStorage.setItem(key, lineId)
    } catch (error) {
      // 隐私模式/配额满时 setItem 会抛错；偏好保存失败不影响当前会话内的产线切换（M10）。
      console.warn('[product-line] 保存产线偏好失败，仅保留内存态:', error)
    }
  }

  /**
   * 按当前用户读一次偏好。
   * 第一次使用 store 时调用（不是模块顶层），因此登录信息已经由 auth store 同步恢复；
   * 之后账号切换时再由下面的 watch 重新水合。
   */
  function hydrate(): void {
    const key = currentStorageKey()
    let stored: string | null = null

    if (key && typeof localStorage !== 'undefined') {
      try {
        stored = localStorage.getItem(key)
      } catch {
        // localStorage 被禁用时按「没有记录」处理，界面仍可用。
        stored = null
      }
    }

    const resolved = resolveLine(lines.value, stored)
    currentLineId.value = resolved?.id ?? ''

    // 存储里是坏值（产线被删、手改）时回退第一条并覆盖，避免下次继续读到同一个坏值。
    if (key && stored !== null && resolved && stored !== resolved.id) {
      persist(resolved.id)
    }
  }

  /**
   * 只改当前产线（以及按用户持久化），不碰智能体、知识库、文件、会话。
   * 传入当前配置里不存在的 id 时回退第一条。
   */
  function selectLine(id: string): void {
    const resolved = resolveLine(lines.value, id)
    if (!resolved) return
    currentLineId.value = resolved.id
    persist(resolved.id)
  }

  const currentLine = computed<ProductLine | null>(() => resolveLine(lines.value, currentLineId.value))

  hydrate()
  // 账号切换（登出 → 另一个用户登录）时按新用户 id 重新读，两个账号的偏好互不影响。
  watch(() => authStore.currentUserId, () => hydrate())

  return {
    lines,
    currentLineId,
    currentLine,
    selectLine,
    hydrate,
  }
})
