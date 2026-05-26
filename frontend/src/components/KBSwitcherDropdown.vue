<template>
  <t-popup
    trigger="click"
    placement="bottom-left"
    :overlay-style="{ padding: 0 }"
    :overlay-inner-style="{ padding: 0 }"
  >
    <template #content>
      <div class="kb-switcher-card">
        <div class="kb-switcher-list">
          <button
            v-for="item in sortedList"
            :key="item.id"
            type="button"
            class="kb-switcher-row"
            :class="{ active: item.id === currentKbId }"
            @click="handleSelect(item.id)"
          >
            <t-icon
              :name="iconFor(item.type)"
              class="kb-switcher-row-icon"
              size="16px"
            />
            <span class="kb-switcher-row-name" :title="item.name">{{ item.name }}</span>
            <t-icon
              v-if="item.id === currentKbId"
              name="check"
              class="kb-switcher-row-check"
              size="14px"
            />
          </button>
          <div v-if="!sortedList.length" class="kb-switcher-empty">
            {{ t('common.noData') }}
          </div>
        </div>
      </div>
    </template>
    <slot />
  </t-popup>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

interface KBEntry {
  id: string
  name: string
  type?: string
}

const props = defineProps<{
  kbList: KBEntry[]
  currentKbId: string
}>()

const emit = defineEmits<{
  (e: 'select', kbId: string): void
}>()

const { t } = useI18n()

// Sort the list with the current KB pinned to the top so users always
// see "where they are" without scrolling. The rest preserves the
// caller's order (typically "mine first, then shared"); we don't
// re-sort alphabetically because that loses the recency / share-source
// signal embedded in the input order.
const sortedList = computed<KBEntry[]>(() => {
  const all = props.kbList || []
  const current = all.find((kb) => kb.id === props.currentKbId)
  if (!current) return all
  return [current, ...all.filter((kb) => kb.id !== props.currentKbId)]
})

const iconFor = (type?: string): string => {
  if (type === 'faq') return 'chat-bubble-help'
  return 'folder'
}

const handleSelect = (id: string): void => {
  if (id === props.currentKbId) return
  emit('select', id)
}
</script>

<style scoped lang="less">
.kb-switcher-card {
  min-width: 220px;
  max-width: 320px;
  /* Mirror the info popover's cap so both header surfaces stay inside
     the viewport on shorter laptops. */
  max-height: min(60vh, 420px);
  display: flex;
  flex-direction: column;
  padding: 6px;
  overflow: hidden;
}

.kb-switcher-list {
  flex: 1 1 auto;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.kb-switcher-row {
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

.kb-switcher-row-icon {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);

  .kb-switcher-row.active & {
    color: var(--td-brand-color);
  }
}

.kb-switcher-row-name {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-switcher-row-check {
  flex: 0 0 auto;
  color: var(--td-brand-color);
}

.kb-switcher-empty {
  padding: 16px;
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
</style>
