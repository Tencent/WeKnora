import { computed, watch } from 'vue'
import { useProductLineStore } from '@/stores/productLine'
import { useSettingsStore } from '@/stores/settings'

/**
 * 客户视角进入平台时，把当前智能体对齐到产线配置。
 *
 * 为什么单独做成 composable：产线切换、页面刷新、从设置页回到对话都要覆盖，
 * 放在某个输入框组件里会漏掉不挂输入区的页面；同时保证全项目只有这一处设置客户智能体。
 *
 * 只改 selectedAgentId，不调用 toggleAgent：
 * `builtin-quick-answer` 是快速问答智能体，请求里本来就该是 `agent_enabled:false`（T06 实测）。
 * 会话页切线自动开新会话由 chat 视图层负责（customer-model-lines AC-13）；这里不切向量库/检索（C7）。
 *
 * @param isEnabled 客户视角判定。传函数而不是布尔值，是为了角色水合晚于挂载时也能补上；
 *                  管理员传 false 时不会创建产线 store，也不会读写产线偏好。
 */
export function useCustomerAgent(isEnabled: () => boolean): void {
  const settingsStore = useSettingsStore()

  // 懒创建：只有真的进入客户视角才实例化 productLine store（管理员不留下任何产线数据）。
  const productLineStore = computed(() => (isEnabled() ? useProductLineStore() : null))

  watch(
    [isEnabled, () => productLineStore.value?.currentLine?.agentId],
    ([enabled, agentId]) => {
      if (!enabled || !agentId) return
      // 已经是目标智能体就不动，避免无谓地重置输入区状态。
      if (settingsStore.selectedAgentId === agentId) return
      try {
        settingsStore.selectAgent(agentId)
      } catch (error) {
        // 对齐失败只记录，不阻断提问；发送时 selectedAgentId 仍有默认值可用。
        console.warn('[customer-agent] 产线智能体对齐失败，继续使用当前智能体:', error)
      }
    },
    { immediate: true },
  )
}
