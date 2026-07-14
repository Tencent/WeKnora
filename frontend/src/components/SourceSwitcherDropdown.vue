<template>
  <t-popup
    v-model:visible="visible"
    trigger="click"
    placement="bottom-left"
    :overlay-style="{ padding: 0 }"
    :overlay-inner-style="{ padding: 0 }"
  >
    <template #content>
      <div class="source-switcher-card">
        <div class="source-switcher-list">
          <button
            v-for="item in sortedList"
            :key="item.value"
            type="button"
            class="source-switcher-row"
            :class="{ active: item.value === current }"
            @click="handleSelect(item.value)"
          >
            <img
              v-if="item.logo"
              :src="item.logo"
              :alt="item.label"
              class="source-switcher-row-logo"
            />
            <t-icon
              v-else
              name="user"
              class="source-switcher-row-icon"
              size="16px"
            />
            <span class="source-switcher-row-name" :title="item.label">{{ item.label }}</span>
            <t-icon
              v-if="item.value === current"
              name="check"
              class="source-switcher-row-check"
              size="14px"
            />
          </button>
        </div>
      </div>
    </template>
    <slot />
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

// A dumb scope switcher mirroring KBSwitcherDropdown's visual grammar. Logos are
// pre-resolved by the caller; web (no logo) falls back to a generic icon.
interface SourceItem {
  value: string
  label: string
  logo?: string
}

const props = defineProps<{
  sources: SourceItem[]
  current: string
}>()

const emit = defineEmits<{
  (e: 'select', value: string): void
}>()

// t-popup only closes on outside click; selecting an item must close it
// explicitly, otherwise the panel lingers and overlays the list below
// (switching source reloads in place, so nothing else dismisses it).
const visible = ref(false)

// Pin the current source to the top so users always see "where they are" without
// scrolling — same as KBSwitcherDropdown. The rest keeps the caller's order
// (web first, then configured platforms).
const sortedList = computed<SourceItem[]>(() => {
  const all = props.sources || []
  const current = all.find((s) => s.value === props.current)
  if (!current) return all
  return [current, ...all.filter((s) => s.value !== props.current)]
})

const handleSelect = (value: string): void => {
  visible.value = false
  if (value === props.current) return
  emit('select', value)
}
</script>

<style scoped lang="less">
/* Mirrors KBSwitcherDropdown so both scope switchers stay visually identical. */
.source-switcher-card {
  min-width: 200px;
  max-width: 300px;
  max-height: min(60vh, 420px);
  display: flex;
  flex-direction: column;
  padding: 6px;
  overflow: hidden;
}

.source-switcher-list {
  flex: 1 1 auto;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.source-switcher-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  font-size: 13px;
  line-height: 1.4;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
  text-align: left;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.active {
    background: var(--td-brand-color-light, rgba(0, 82, 217, 0.08));
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.source-switcher-row-logo {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  border-radius: 3px;
  object-fit: contain;
}

.source-switcher-row-icon {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);

  .source-switcher-row.active & {
    color: var(--td-brand-color);
  }
}

.source-switcher-row-name {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-switcher-row-check {
  flex: 0 0 auto;
  color: var(--td-brand-color);
}
</style>
