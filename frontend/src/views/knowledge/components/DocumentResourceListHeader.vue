<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';

withDefaults(defineProps<{
  checked?: boolean;
  indeterminate?: boolean;
  disabled?: boolean;
}>(), {
  checked: false,
  indeterminate: false,
  disabled: false,
});

const emit = defineEmits<{
  (event: 'toggleAll', checked: boolean): void;
}>();

const { t } = useI18n();
const stickySentinel = ref<HTMLElement | null>(null);
const headerStuck = ref(false);
let stickyObserver: IntersectionObserver | null = null;

onMounted(() => {
  if (!stickySentinel.value || typeof IntersectionObserver === 'undefined') return;
  stickyObserver = new IntersectionObserver(
    entries => {
      headerStuck.value = !entries[0].isIntersecting;
    },
    { threshold: 0 },
  );
  stickyObserver.observe(stickySentinel.value);
});

onBeforeUnmount(() => {
  stickyObserver?.disconnect();
  stickyObserver = null;
});
</script>

<template>
  <div ref="stickySentinel" class="resource-list-sticky-sentinel" aria-hidden="true" />
  <div class="resource-list-header" :class="{ 'is-stuck': headerStuck }">
    <div class="resource-list-cell resource-list-cell--check" @click.stop>
      <t-checkbox
        class="resource-list-check"
        size="small"
        :checked="checked"
        :indeterminate="indeterminate"
        :disabled="disabled"
        :title="t('knowledgeBase.selectAll')"
        @change="(value: boolean) => emit('toggleAll', value)"
      />
    </div>
    <div class="resource-list-cell">{{ t('knowledgeBase.columnName') }}</div>
    <div class="resource-list-cell">{{ t('knowledgeBase.columnTag') }}</div>
    <div class="resource-list-cell">{{ t('knowledgeBase.columnSource') }}</div>
    <div class="resource-list-cell resource-list-cell--numeric">
      {{ t('knowledgeBase.columnSize') }}
    </div>
    <div class="resource-list-cell">{{ t('knowledgeBase.columnStatus') }}</div>
    <div class="resource-list-cell resource-list-cell--numeric">
      {{ t('knowledgeBase.columnUpdatedAt') }}
    </div>
    <div class="resource-list-cell resource-list-cell--actions">
      {{ t('knowledgeBase.columnActions') }}
    </div>
  </div>
</template>

<style scoped lang="less">
@import (reference) "./document-resource-list.less";

.resource-list-sticky-sentinel {
  height: 0;
  margin: 0;
  padding: 0;
  border: 0;
  pointer-events: none;
}

.resource-list-header {
  .document-resource-list-grid();
  position: sticky;
  z-index: 3;
  top: 0;
  height: 40px;
  flex: 0 0 auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px 8px 0 0;
  background: var(--td-bg-color-secondarycontainer);
  box-shadow: 0 2px 8px rgba(0, 0, 0, .04);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 12px;
  font-weight: 500;
  transition: border-radius .15s ease, box-shadow .2s ease;

  &.is-stuck {
    border-radius: 0;
    box-shadow: 0 4px 10px rgba(0, 0, 0, .08);
  }
}

.resource-list-cell {
  .document-resource-list-cell();
}

.resource-list-cell--check {
  justify-content: center;
  padding: 0;
}

.resource-list-cell--numeric {
  justify-content: flex-end;
}

.resource-list-cell--actions {
  justify-content: flex-end;
}

.resource-list-check {
  .document-resource-list-checkbox();
}
</style>
