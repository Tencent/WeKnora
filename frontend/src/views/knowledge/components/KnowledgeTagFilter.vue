<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

type FilterTag = { id: string; name: string; knowledge_count?: number; chunk_count?: number }
const props = defineProps<{
  tags: FilterTag[]
  selectedIds: string[]
  total: number
  loading: boolean
  loadingMore: boolean
  hasMore: boolean
  canManage: boolean
  variant: 'documents' | 'faq'
}>()
const search = defineModel<string>('search', { required: true })
const cleared = defineModel<boolean>('cleared', { required: true })
const emit = defineEmits<{
  change: [ids: string[]]
  'load-more': []
  manage: []
}>()
const { t } = useI18n()
const visible = ref(false)
const hovered = ref(false)
const tagMap = computed(() => new Map(props.tags.map(tag => [tag.id, tag])))
const isPlaceholder = computed(() => props.selectedIds.length === 0 && cleared.value)
const label = computed(() => {
  if (!props.selectedIds.length) return t(cleared.value ? 'knowledgeBase.tagFilterPlaceholder' : 'knowledgeBase.allTags')
  if (props.selectedIds.length === 1) return tagMap.value.get(props.selectedIds[0]!)?.name || t('knowledgeBase.allTags')
  return t('knowledgeBase.tagFilterMulti', { count: props.selectedIds.length })
})
const title = computed(() => {
  const names = props.selectedIds.map(id => tagMap.value.get(id)?.name).filter(Boolean)
  return names.length ? names.join('、') : t('knowledgeBase.tagFilterTitle')
})
const countOf = (tag: FilterTag) => (props.variant === 'documents' ? tag.knowledge_count : tag.chunk_count) || 0
const toggle = (id: string) => {
  const next = new Set(props.selectedIds)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  if (next.size) cleared.value = false
  emit('change', [...next])
}
const clear = () => {
  cleared.value = true
  emit('change', [])
}
const manage = () => {
  visible.value = false
  emit('manage')
}
</script>

<template>
  <t-popup v-model:visible="visible" trigger="click" placement="bottom-left"
    overlay-class-name="tag-filter-popup" :overlay-inner-style="{ padding: 0 }">
    <template #content>
      <div class="tag-filter-panel" :class="{ 'tag-filter-panel--documents': variant === 'documents' }" @click.stop>
        <div class="tag-filter-panel__header">
          <div class="tag-filter-panel__title">
            <span>{{ $t('knowledgeBase.tagFilterTitle') }}</span>
            <span class="tag-filter-panel__count">({{ total || tags.length }})</span>
          </div>
        </div>
        <div class="tag-search-bar">
          <t-input v-model.trim="search" size="small"
            :placeholder="$t('knowledgeBase.tagSearchPlaceholder')" clearable>
            <template #prefix-icon>
              <t-icon name="search" size="14px" />
            </template>
          </t-input>
        </div>
        <div class="tag-filter-panel__body">
          <template v-if="loading && !tags.length">
            <div class="tag-filter-chips">
              <div v-for="n in 8" :key="'skel-tag-' + n" class="tag-filter-chip-skeleton">
                <t-skeleton animation="gradient"
                  :row-col="[{ width: '56px', height: '24px', type: 'rect' }]" />
              </div>
            </div>
          </template>
          <template v-else>
            <div class="tag-filter-chips">
              <button
                v-for="tag in tags"
                :key="tag.id"
                type="button"
                class="tag-filter-chip"
                :class="{ active: selectedIds.includes(tag.id) }"
                :title="`${tag.name} (${countOf(tag)})`"
                @click="toggle(tag.id)"
              >
                <span class="tag-filter-chip__label">{{ tag.name }}</span>
                <span class="tag-filter-chip__count">{{ countOf(tag) }}</span>
              </button>
            </div>
            <div v-if="!tags.length" class="tag-empty-state">
              {{ $t('knowledgeBase.tagEmptyResult') }}
            </div>
            <div v-if="hasMore" class="tag-load-more">
              <t-button variant="text" size="small" :loading="loadingMore"
                @click.stop="emit('load-more')">
                {{ $t('tenant.loadMore') }}
              </t-button>
            </div>
          </template>
        </div>
        <div v-if="canManage" class="tag-filter-panel__footer">
          <t-button variant="text" size="small" class="tag-manage-link" @click="manage">
            {{ $t('knowledgeBase.tagManageLink') }}
          </t-button>
        </div>
      </div>
    </template>
    <div class="doc-filter-field" :class="{ 'doc-filter-field--documents': variant === 'documents' }">
      <button type="button" class="doc-tag-filter-trigger doc-filter-field__control"
        :class="{ open: visible, 'is-placeholder': isPlaceholder }"
        :aria-label="$t('knowledgeBase.tagFilterTitle')"
        :title="title"
        @mouseenter="hovered = true"
        @mouseleave="hovered = false">
        <span class="doc-tag-filter-trigger__prefix" aria-hidden="true">
          <t-icon name="discount" size="16px" />
        </span>
        <span class="doc-tag-filter-trigger__label">{{ label }}</span>
        <span class="doc-tag-filter-trigger__suffix">
          <span
            v-if="selectedIds.length > 0 && hovered"
            class="t-input__suffix t-input__suffix-icon t-input__clear"
            :aria-label="$t('common.clear')"
            @click.stop="clear"
            @mousedown.stop
          >
            <t-icon name="close-circle-filled" class="t-input__suffix-clear" />
          </span>
          <t-icon
            v-else
            name="chevron-down"
            size="16px"
            class="doc-tag-filter-trigger__caret"
            :class="{ open: visible }"
          />
        </span>
      </button>
    </div>
  </t-popup>
</template>

<style lang="less">
.tag-filter-popup {
  z-index: 5500 !important;
}

.tag-filter-popup .t-popup__content {
  padding: 0 !important;
  border-radius: 8px !important;
  background: var(--td-bg-color-container) !important;
  border: 0.5px solid var(--td-component-stroke) !important;
  box-shadow:
    0 0 0 0.5px rgba(0, 0, 0, 0.03),
    0 2px 4px rgba(0, 0, 0, 0.04),
    0 8px 24px rgba(0, 0, 0, 0.1) !important;
}
</style>
<style scoped lang="less">
.doc-filter-field {
  width: 140px;
  flex-shrink: 0;
}
.doc-tag-filter-trigger {
  display: inline-flex;
  align-items: center;
  box-sizing: border-box;
  width: 100%;
  height: 32px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-radius: var(--td-radius-default);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  transition: background 0.2s ease, border-color 0.2s ease;

  &:hover,
  &.open {
    background: var(--td-bg-color-secondarycontainer);
    border-color: transparent;
  }

  &.is-placeholder {
    color: var(--td-text-color-placeholder);
  }

  &__prefix {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    margin-right: var(--td-comp-margin-s);
    color: var(--td-text-color-placeholder);
  }

  &__label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: left;
  }

  &__suffix {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    margin-left: var(--td-comp-margin-s);
  }

  &__caret {
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
    transition: transform 0.2s ease, color 0.2s ease;

    &.open {
      color: var(--td-brand-color);
      transform: rotate(180deg);
    }
  }
}
.doc-filter-field--documents .doc-tag-filter-trigger__suffix {
  :deep(.t-input__suffix) {
    margin-left: 0;
  }
  :deep(.t-input__suffix-clear) {
    font-size: 16px;
  }
}

.tag-filter-panel {
  width: 320px;
  max-width: min(320px, calc(100vw - 32px));
  max-height: min(70vh, 480px);
  display: flex;
  flex-direction: column;
  padding: 12px 14px;
  box-sizing: border-box;
  font-size: 12px;
  color: var(--td-text-color-primary);
}

.tag-filter-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.tag-filter-panel__title {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
}

.tag-filter-panel__count {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-weight: 400;
}

.tag-filter-panel .tag-search-bar {
  margin-bottom: 10px;
}

.tag-filter-panel__body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.tag-filter-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.tag-filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 11px;
  cursor: pointer;
}

.tag-filter-chip.active {
  border-color: color-mix(in srgb, var(--td-brand-color) 35%, var(--td-component-stroke));
  color: var(--td-brand-color);
  background-color: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
}

.tag-filter-chip__label {
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tag-filter-chip__count {
  font-size: 10px;
  color: var(--td-text-color-placeholder);
}

.tag-filter-panel__footer {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--td-component-stroke);
}

.tag-empty-state {
  text-align: center;
  padding: 10px 6px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.tag-load-more {
  display: flex;
  justify-content: center;
  padding-top: 2px;
}
.tag-filter-panel--documents .tag-filter-panel__header {
  padding: 0;
  color: var(--td-text-color-primary);
}
.tag-filter-panel--documents .tag-filter-panel__title {
  letter-spacing: 0.5px;
}
.tag-filter-panel--documents .tag-search-bar {
  padding: 0;
}
.tag-filter-panel--documents .tag-search-bar :deep(.t-input) {
  font-size: 13px;
  background-color: var(--td-bg-color-secondarycontainer);
  border-color: transparent;
  border-radius: 6px;
  box-shadow: none !important;
}
.tag-filter-panel--documents .tag-search-bar :deep(.t-input):hover,
.tag-filter-panel--documents .tag-search-bar :deep(.t-input):focus,
.tag-filter-panel--documents .tag-search-bar :deep(.t-input).t-is-focused {
  border-color: var(--td-component-border);
  background-color: var(--td-bg-color-container);
  box-shadow: none !important;
}
.tag-filter-panel--documents .tag-search-bar :deep(.t-input__inner) {
  font-size: 13px;
}
.tag-filter-panel--documents .tag-search-bar :deep(.t-input__prefix-icon) {
  margin-right: 0;
}
.tag-filter-panel--documents .tag-filter-panel__body {
  overflow-x: hidden;
  scrollbar-width: thin;
}
.tag-filter-panel--documents .tag-filter-panel__body::-webkit-scrollbar {
  width: 4px;
}
.tag-filter-panel--documents .tag-filter-panel__body::-webkit-scrollbar-thumb {
  border-radius: 2px;
  background: var(--td-scrollbar-color);
}
.tag-filter-panel--documents .tag-filter-chips {
  align-items: flex-start;
}
.tag-filter-panel--documents .tag-filter-chip-skeleton {
  flex-shrink: 0;
}
.tag-filter-panel--documents .tag-filter-chip {
  box-sizing: border-box;
  max-width: 100%;
  font-family: var(--app-font-family);
  font-weight: 400;
  line-height: 24px;
  outline: none;
  transition: background 0.15s ease, color 0.15s ease, border-color 0.15s ease;
  -webkit-font-smoothing: antialiased;
}
.tag-filter-panel--documents .tag-filter-chip:hover:not(.active) {
  border-color: var(--td-component-border);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}
.tag-filter-panel--documents .tag-filter-chip:focus-visible {
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-component-stroke) 60%, transparent);
}
.tag-filter-panel--documents .tag-filter-chip.active {
  font-weight: 500;
}
.tag-filter-panel--documents .tag-filter-chip.active .tag-filter-chip__count {
  color: color-mix(in srgb, var(--td-brand-color) 72%, var(--td-text-color-secondary));
}
.tag-filter-panel--documents .tag-filter-chip.active:hover {
  background-color: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
}
.tag-filter-panel--documents .tag-filter-chip__label {
  min-width: 0;
}
.tag-filter-panel--documents .tag-filter-chip__count {
  flex-shrink: 0;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}
.tag-filter-panel--documents .tag-filter-chip__count::before {
  content: '·';
  margin-right: 2px;
  opacity: 0.65;
}
.tag-filter-panel--documents .tag-filter-panel__footer {
  display: flex;
  justify-content: flex-start;
}
.tag-filter-panel--documents .tag-filter-panel__footer :deep(.tag-manage-link.t-button) {
  padding: 0;
  height: auto;
  min-height: 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  border: none !important;
  background: transparent !important;
  box-shadow: none !important;
  transition: color 0.15s ease;
}
.tag-filter-panel--documents .tag-filter-panel__footer :deep(.tag-manage-link.t-button):hover,
.tag-filter-panel--documents .tag-filter-panel__footer :deep(.tag-manage-link.t-button):focus-visible {
  color: var(--td-brand-color) !important;
  background: transparent !important;
  border-color: transparent !important;
  text-decoration: none;
}
.tag-filter-panel--documents .tag-load-more :deep(.t-button) {
  padding: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
.tag-filter-panel--documents .tag-empty-state {
  padding: 6px 0;
  font-size: 12px;
}

</style>
