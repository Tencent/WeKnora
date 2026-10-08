<template>
  <div class="knowledge-tag-anchor" @click.stop @mousedown.stop>
    <t-popup :visible="visible" :disabled="disabled" trigger="click" placement="bottom-left" attach="body"
      destroy-on-close overlay-class-name="knowledge-tag-popover" :overlay-inner-style="{ padding: '12px' }"
      @visible-change="changeVisible">
      <slot />
      <template #content>
        <section v-if="visible" class="tag-popover-panel" :aria-label="$t('knowledgeBase.tagEditDialogHeading')">
          <KnowledgeTagPicker :kb-id="kbId" :selected-ids="selectedIds" @update:selected-ids="selectedIds = $event"
            @busy-change="busy = $event" @changed="emit('changed', $event)" />
          <footer class="tag-popover-footer">
            <span>{{ $t('knowledgeBase.tagSelectedCount', { count: selectedIds.length }) }}</span>
            <t-button size="small" variant="text" theme="default" :disabled="busy || saving" @click="changeVisible(false)">{{ $t('common.cancel') }}</t-button>
            <t-button size="small" theme="primary" :disabled="busy" :loading="saving" @click="save">{{ $t('common.confirm') }}</t-button>
          </footer>
        </section>
      </template>
    </t-popup>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { updateKnowledgeTagBatch } from '@/api/knowledge-base';
import KnowledgeTagPicker from './KnowledgeTagPicker.vue';
const props = defineProps<{
  kbId: string;
  knowledgeId: string;
  tags: { id: string }[];
  visible: boolean;
  disabled?: boolean;
}>();
const emit = defineEmits<{
  'update:visible': [visible: boolean];
  changed: [payload?: { deletedTagId?: string }];
}>();
const { t } = useI18n();
const selectedIds = ref<string[]>([]);
const busy = ref(false);
const saving = ref(false);
watch(() => props.visible, open => {
  if (open) selectedIds.value = props.tags.map(tag => tag.id);
}, { immediate: true });
function changeVisible(visible: boolean) {
  if (busy.value || saving.value || (visible && props.disabled)) return;
  emit('update:visible', visible);
}
async function save() {
  if (busy.value || saving.value) return;
  saving.value = true;
  try {
    await updateKnowledgeTagBatch({ updates: { [props.knowledgeId]: [...selectedIds.value] } });
    MessagePlugin.success(t('knowledgeBase.tagUpdateSuccess'));
    emit('update:visible', false);
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally { saving.value = false; }
}
</script>

<style scoped lang="less">
.knowledge-tag-anchor { min-width: 0; width: 100%; }
.tag-popover-panel { width: min(300px, calc(100vw - 56px)); }
.tag-popover-panel :deep(.knowledge-tag-picker) { margin-top: 0; }
.tag-popover-panel :deep(.tag-picker-list) { max-height: min(240px, 40vh); }
.tag-popover-footer {
  display: flex; align-items: center; gap: 6px; margin-top: 10px; padding-top: 10px;
  border-top: 1px solid var(--td-component-stroke);
  > span { flex: 1; font-size: var(--app-text-sm); color: var(--td-text-color-placeholder); }
}
</style>
