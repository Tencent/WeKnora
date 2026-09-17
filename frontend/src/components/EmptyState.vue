<template>
  <div class="wk-empty-state" :class="{ 'wk-empty-state--compact': compact }">
    <div v-if="$slots.icon || icon || image" class="wk-empty-state__icon">
      <slot name="icon">
        <img v-if="image" :src="image" alt="" aria-hidden="true" />
        <t-icon v-else :name="icon" />
      </slot>
    </div>
    <div class="wk-empty-state__title">{{ title }}</div>
    <div v-if="description || $slots.description" class="wk-empty-state__desc">
      <slot name="description">{{ description }}</slot>
    </div>
    <div v-if="$slots.default" class="wk-empty-state__actions">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 统一的空状态：图标 / 标题 / 描述 / 操作区。
 * 列表页用 `icon`（TDesign 图标名），需要插图时用 `image`；`compact` 用于抽屉、卡片等小容器。
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
    icon?: string
    image?: string
    compact?: boolean
  }>(),
  { description: '', icon: '', image: '', compact: false },
)
</script>

<style lang="less" scoped>
.wk-empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 56px 20px;

  &--compact {
    padding: 32px 16px;
  }
}

.wk-empty-state__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  margin-bottom: 16px;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: 30px;

  img {
    width: 40px;
    height: 40px;
  }

  .wk-empty-state--compact & {
    width: 48px;
    height: 48px;
    margin-bottom: 12px;
    font-size: 22px;
  }
}

.wk-empty-state__title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-lg);
  font-weight: 600;
  line-height: 22px;
}

.wk-empty-state__desc {
  margin-top: 6px;
  max-width: 360px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  line-height: 20px;
}

.wk-empty-state__actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 20px;
}
</style>
