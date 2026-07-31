<template>
  <t-dialog v-model:visible="dialogVisible" :header="dialogTitle" :on-confirm="handleConfirm" width="400px">
    <t-form ref="formRef" :data="formData" :rules="rules" label-width="80px">
      <t-form-item :label="$t('knowledgeFolder.folderName')" name="name">
        <t-input v-model="formData.name" :placeholder="$t('knowledgeFolder.folderNamePlaceholder')" maxlength="255" />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { createFolder, updateFolder } from '@/api/knowledge-folder';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

const { t } = useI18n();

interface Props {
  visible: boolean;
  kbId: string;
  mode: 'create' | 'edit';
  folder?: KnowledgeFolder | null;
  parentFolderId?: string | null;
}

const props = withDefaults(defineProps<Props>(), {
  folder: null,
  parentFolderId: null,
});

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'success'): void;
}>();

const formRef = ref();
const formData = ref({
  name: '',
  parent_folder_id: null as string | null,
});

const dialogVisible = computed({
  get: () => props.visible,
  set: (value) => emit('update:visible', value),
});

const dialogTitle = computed(() => props.mode === 'create' ? t('knowledgeFolder.createFolder') : t('knowledgeFolder.editFolder'));

const rules = {
  name: [{ required: true, message: t('knowledgeFolder.folderNameRequired'), type: 'error' }],
};

watch(() => props.visible, (visible) => {
  if (visible) {
    formData.value = {
      name: props.mode === 'create' ? '' : (props.folder?.name || ''),
      parent_folder_id: props.mode === 'create' ? (props.parentFolderId || null) : null,
    };
    formRef.value?.clearValidate();
  }
});

const handleConfirm = async () => {
  try {
    const valid = await formRef.value?.validate();
    if (!valid) return false;

    if (props.mode === 'create') {
      await createFolder(props.kbId, formData.value);
      MessagePlugin.success(t('knowledgeFolder.folderCreatedSuccess'));
    } else if (props.folder) {
      await updateFolder(props.kbId, props.folder.id, { name: formData.value.name });
      MessagePlugin.success(t('knowledgeFolder.folderUpdatedSuccess'));
    }

    emit('success');
    dialogVisible.value = false;
  } catch (error: any) {
    MessagePlugin.error(error.message || t('knowledgeFolder.folderCreatedFailed'));
    return false;
  }
};
</script>
