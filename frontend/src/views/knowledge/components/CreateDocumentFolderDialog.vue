<template>
  <t-dialog attach="body" :visible="state.visible" :header="t('knowledgeBase.folderCreate.title')" :footer="false"
    width="440px" dialog-class-name="document-folder-create-dialog" :close-btn="!state.pending"
    :close-on-overlay-click="false" :close-on-esc-keydown="!state.pending" destroy-on-close @close="controller.cancel">
    <form @submit.prevent="submit">
      <p class="folder-create-target">
        <span>{{ t('knowledgeBase.folderCreate.parent') }}</span>
        <span>{{ parentLabel }}</span>
      </p>
      <label class="folder-create-label" for="document-folder-name">{{ t('knowledgeBase.folderCreate.name') }}</label>
      <input id="document-folder-name" ref="nameInput" v-model="state.name" class="folder-create-input"
        autocomplete="off" :disabled="state.pending" :aria-invalid="!!state.error"
        :aria-describedby="state.error ? 'document-folder-name-error' : undefined" @input="state.error = null" />
      <p v-if="state.error" id="document-folder-name-error" class="folder-create-error" role="alert">{{ errorMessage }}</p>
      <div class="folder-create-actions">
        <t-button type="button" variant="outline" :disabled="state.pending" @click="controller.cancel">{{ t('common.cancel') }}</t-button>
        <t-button type="submit" theme="primary" :loading="state.pending" :disabled="state.pending">{{ t('knowledgeBase.folderCreate.title') }}</t-button>
      </div>
    </form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { createKnowledgeFolder } from '@/api/knowledge-base';
import { createDocumentFolderController, createDocumentFolderState, type CreatedDocumentFolder } from '../folderCreateState';

const props = defineProps<{
  visible: boolean;
  kbId: string;
  kbName?: string;
  parentPath: string;
  canEdit: boolean;
  folderPaths: string[];
  afterCreate: (folder: CreatedDocumentFolder) => Promise<void> | void;
}>();
const emit = defineEmits<{
  (event: 'update:visible', visible: boolean): void;
  (event: 'busy-change', pending: boolean): void;
}>();
const { t } = useI18n();
const state = reactive(createDocumentFolderState());
const nameInput = ref<HTMLInputElement>();
const controller = createDocumentFolderController(state, {
  currentContext: () => ({ kbId: props.kbId, parentPath: props.parentPath, canEdit: props.canEdit }),
  existingPaths: () => props.folderPaths,
  create: createKnowledgeFolder,
  onCreated: folder => props.afterCreate(folder),
});
const parentLabel = computed(() => [props.kbName || t('knowledgeBase.folderTree.rootRow'), state.parentPath].filter(Boolean).join(' / '));
const errorMessage = computed(() => ({
  invalid: t('knowledgeBase.folderCreate.invalid'),
  tooLong: t('knowledgeBase.folderCreate.tooLong'),
  tooDeep: t('knowledgeBase.folderCreate.tooDeep'),
  duplicate: t('knowledgeBase.folderCreate.duplicate'),
  parentMissing: t('knowledgeBase.folderCreate.parentMissing'),
  forbidden: t('knowledgeBase.folderCreate.forbidden'),
  failed: t('knowledgeBase.folderCreate.failed'),
})[state.error || 'failed']);

watch(() => state.visible, async visible => {
  emit('update:visible', visible);
  if (visible) { await nextTick(); nameInput.value?.focus(); }
});
watch(() => state.pending, pending => emit('busy-change', pending));
watch(() => [props.kbId, props.parentPath, props.canEdit], controller.contextChanged, { flush: 'sync' });
watch(() => props.visible, visible => {
  if (visible) {
    if (!state.visible && !controller.open()) emit('update:visible', false);
  } else if (!state.pending) controller.cancel();
}, { immediate: true });

async function submit() {
  const result = await controller.submit();
  if (result === 'created') MessagePlugin.success(t('knowledgeBase.folderCreate.success'));
  else if (result === 'refreshFailed') MessagePlugin.warning(t('knowledgeBase.folderCreate.refreshFailed'));
}
</script>

<style>
.document-folder-create-dialog { max-width: calc(100vw - 24px); }
</style>
<style scoped>
.folder-create-target { display: flex; gap: 12px; margin: 0 0 18px; color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
.folder-create-target > :first-child { flex-shrink: 0; }
.folder-create-target > :last-child { overflow-wrap: anywhere; }
.folder-create-label { display: block; margin-bottom: 8px; color: var(--td-text-color-primary); }
.folder-create-input { box-sizing: border-box; width: 100%; height: 36px; padding: 0 10px; border: 1px solid var(--td-component-stroke); border-radius: var(--app-radius-xs); background: var(--td-bg-color-container); color: var(--td-text-color-primary); font: inherit; }
.folder-create-input:focus { outline: 2px solid var(--td-brand-color); outline-offset: 1px; }
.folder-create-input[aria-invalid="true"] { border-color: var(--td-error-color); }
.folder-create-error { margin: 8px 0 0; color: var(--td-error-color); font-size: var(--app-text-sm); }
.folder-create-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 24px; }
</style>
