<template>
  <!-- 全局右上角"待处理邀请"铃铛。
       - 与原先 UserMenu 中铃铛的逻辑一致：只在 pendingInvitationCount > 0 时渲染，
         空收件箱场景不占用角落像素。
       - 固定定位、z-index 远低于 t-drawer 默认 2500，业务页面右侧抽屉（FAQ、KB 调试、
         Tenant 审计、SettingDrawer 等）弹出时会自然覆盖铃铛，不需要特意联动隐藏。
       - 点击铃铛复用同一份 MyInvitationsDialog，行为与之前一致。 -->
  <template v-if="pendingInvitationCount > 0">
    <t-badge :count="pendingInvitationCount" :max-count="99" :offset="[6, 4]"
      class="global-invitation-bell">
      <button type="button" class="global-invitation-bell__btn"
        :title="$t('tenantInvitation.inboxTooltip')" @click="openDialog">
        <t-icon name="notification" size="18px" />
      </button>
    </t-badge>
  </template>
  <MyInvitationsDialog v-model:visible="dialogVisible" />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import MyInvitationsDialog from '@/components/MyInvitationsDialog.vue'

const authStore = useAuthStore()

const pendingInvitationCount = computed(() => authStore.pendingInvitationCount)

const dialogVisible = ref(false)
const openDialog = () => {
  dialogVisible.value = true
}
</script>

<style lang="less" scoped>
.global-invitation-bell {
  position: fixed;
  top: 12px;
  right: 16px;
  /* 远低于 TDesign 抽屉的默认 z-index (2500)，确保业务页右侧抽屉弹出时能正常盖住铃铛。
     高于普通页面内容（一般 0~10），避免被列表卡片覆盖。 */
  z-index: 100;
}

.global-invitation-bell__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  /* 用 container 背景而非透明，铃铛悬在内容区上方时和不同颜色的页面背景都能看清。 */
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.04);
  transition: background-color 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}
</style>
