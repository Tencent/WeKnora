<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

import type { DocumentMetadataField } from '@/api/knowledge-base';

const props = defineProps<{ field: DocumentMetadataField }>();
const { t } = useI18n();
const isPending = computed(() => props.field.value?.review_status === 'pending');
const isAutomatic = computed(() => props.field.value?.source === 'automatic');
</script>

<template>
  <div class="metadata-status">
    <t-tag v-if="field.definition.status === 'archived'" size="small" theme="default" variant="light-outline">
      {{ t('metadata.status.archived') }}
    </t-tag>
    <t-tag v-if="field.completion_status === 'incomplete'" size="small" theme="warning" variant="light-outline">
      {{ t('metadata.status.incomplete') }}
    </t-tag>
    <t-tag v-if="isAutomatic" size="small" theme="primary" variant="light-outline">
      {{ t('metadata.status.automatic') }}
    </t-tag>
    <t-tag v-if="isPending" size="small" theme="warning" variant="light-outline">
      {{ t('metadata.status.pending') }}
    </t-tag>
    <t-tag v-else-if="field.value" size="small" theme="success" variant="light-outline">
      {{ t('metadata.status.confirmed') }}
    </t-tag>
    <span v-if="field.value" class="overwrite-state">
      <t-icon :name="field.value.allow_auto_overwrite ? 'refresh' : 'lock-on'" />
      {{ field.value.allow_auto_overwrite ? t('metadata.status.overwriteAllowed') : t('metadata.status.overwriteLocked') }}
    </span>
  </div>
</template>

<style scoped lang="less">
.metadata-status { display: flex; align-items: center; flex-wrap: wrap; gap: 5px; min-height: 22px; }
.overwrite-state { display: inline-flex; align-items: center; gap: 3px; color: var(--td-text-color-placeholder); font-size: 11px; }
</style>
