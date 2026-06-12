<template>
  <div
    :class="[
      'submenu_item',
      !batchMode && activePath === item.path ? 'submenu_item_active' : '',
      batchMode && selectedIds.includes(item.id) ? 'submenu_item_selected' : '',
      batchMode ? 'submenu_item_batch' : '',
    ]"
    @mouseenter="emit('hover-in')"
    @mouseleave="emit('hover-out')"
    @click="batchMode ? emit('toggle-select') : emit('navigate')"
  >
    <t-checkbox
      v-if="batchMode"
      class="batch-checkbox"
      :checked="selectedIds.includes(item.id)"
      @click.stop
      @change="emit('toggle-select')"
    />
    <span class="submenu_title" :class="batchMode ? 'submenu_title--batch' : ''" :title="item.title">
      <t-icon v-if="item.is_pinned" name="pin" class="submenu_pin_icon" />
      <span class="submenu_title-text">{{ item.title }}</span>
    </span>
    <t-dropdown
      v-if="!batchMode"
      :options="menuOptions"
      placement="bottom-right"
      trigger="click"
      @click="emit('menu-click', $event)"
    >
      <div @click.stop class="menu-more-wrap">
        <t-icon name="ellipsis" class="menu-more" />
      </div>
    </t-dropdown>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  item: { id: string; path: string; title: string; is_pinned?: boolean }
  batchMode: boolean
  activePath: string
  selectedIds: string[]
  menuOptions: any[]
  /** 渠道文件夹下的会话（样式与聊天区会话共用文案列对齐） */
  nested?: boolean
}>()

const emit = defineEmits<{
  (e: 'navigate'): void
  (e: 'toggle-select'): void
  (e: 'menu-click', data: { value: string }): void
  (e: 'hover-in'): void
  (e: 'hover-out'): void
}>()
</script>
