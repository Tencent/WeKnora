import { h, onScopeDispose, ref, watch } from 'vue';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  createDocumentFolder,
  deleteDocumentFolder,
  getDocumentFolderDeleteImpact,
  type DocumentFolderDeleteImpact,
  type DocumentFolderDeleteMode,
  updateDocumentFolder,
} from '@/api/knowledge-base';
import DocumentFolderDeleteDialogContent from '../components/DocumentFolderDeleteDialogContent.vue';
import { buildDocumentFolderDeletePlan } from './documentFolderDelete';

type Translate = (key: string, params?: Record<string, unknown>) => string;
type AfterMutation = () => Promise<void> | void;

const folderDeleteMaxPollIntervalMs = 10_000;

interface DocumentFolderMutationOptions {
  knowledgeBaseId: () => string;
  t: Translate;
  onChanged?: () => void;
}

export function useDocumentFolderMutations(options: DocumentFolderMutationOptions) {
  const submitting = ref(false);
  let pollingDisposed = false;
  onScopeDispose(() => {
    pollingDisposed = true;
  });

  async function createFolder(parentId: string, name: string, afterCreate: AfterMutation) {
    if (submitting.value) return false;
    submitting.value = true;
    try {
      await createDocumentFolder(options.knowledgeBaseId(), parentId, name);
      MessagePlugin.success(options.t('knowledgeBase.createFolderSuccess'));
      await afterCreate();
      options.onChanged?.();
      return true;
    } catch (error: any) {
      MessagePlugin.error(error?.message || options.t('knowledgeBase.createFolderFailed'));
      return false;
    } finally {
      submitting.value = false;
    }
  }

  async function renameFolder(
    folderId: string,
    currentName: string,
    name: string,
    afterRename: AfterMutation,
  ) {
    if (submitting.value || name === currentName) return false;
    submitting.value = true;
    try {
      await updateDocumentFolder(options.knowledgeBaseId(), folderId, { name });
      MessagePlugin.success(options.t('knowledgeBase.renameFolderSuccess'));
      await afterRename();
      options.onChanged?.();
      return true;
    } catch (error: any) {
      MessagePlugin.error(error?.message || options.t('knowledgeBase.renameFailed'));
      return false;
    } finally {
      submitting.value = false;
    }
  }

  function simpleDeleteBody(impact: DocumentFolderDeleteImpact, folderName: string) {
    if (impact.folder_count > 1) {
      return options.t('knowledgeBase.deleteFolderEmptyTreeConfirm', {
        count: impact.folder_count,
      });
    }
    return options.t('knowledgeBase.deleteFolderConfirm', { name: folderName });
  }

  async function waitForFolderToDisappear(kbId: string, folderId: string) {
    let attempt = 0;
    // A queued task can wait before its two-hour execution window and may be
    // retried. Keep following it for the lifetime of this view instead of
    // assuming that the worker timeout starts when the API returns 202.
    while (!pollingDisposed) {
      const delay = attempt === 0
        ? 500
        : Math.min(1_000 + Math.floor((attempt - 1) / 30) * 1_000, folderDeleteMaxPollIntervalMs);
      await new Promise(resolve => setTimeout(resolve, delay));
      if (pollingDisposed) return false;
      attempt += 1;
      try {
        await getDocumentFolderDeleteImpact(kbId, folderId);
      } catch (error: any) {
        if (Number(error?.status || error?.response?.status) === 404) return true;
        if ([401, 403].includes(Number(error?.status || error?.response?.status))) return false;
      }
    }
    return false;
  }

  function trackQueuedDeletion(
    kbId: string,
    folderId: string,
    afterDelete: AfterMutation,
  ) {
    void waitForFolderToDisappear(kbId, folderId)
      .then(async (deleted) => {
        if (!deleted) return;
        await afterDelete();
        MessagePlugin.success(options.t('knowledgeBase.deleteFolderSuccess'));
        options.onChanged?.();
      })
      .catch(() => {
        // The server accepted the task; a later manual refresh still reflects
        // completion if this best-effort UI poll cannot update the view.
      });
  }

  function openSimpleDeleteDialog(
    folderId: string,
    folderName: string,
    impact: DocumentFolderDeleteImpact,
    mode: DocumentFolderDeleteMode | undefined,
    afterDelete: AfterMutation,
  ) {
    const dialog = DialogPlugin.confirm({
      header: options.t('knowledgeBase.deleteFolder'),
      body: simpleDeleteBody(impact, folderName),
      confirmBtn: {
        content: options.t('common.confirm'),
        theme: 'danger',
      },
      cancelBtn: {
        content: options.t('common.cancel'),
      },
      onClosed: () => {
        submitting.value = false;
      },
      onConfirm: async () => {
        dialog.update({
          confirmBtn: {
            content: options.t('common.confirm'),
            theme: 'danger',
            loading: true,
          },
        });
        try {
          const kbId = options.knowledgeBaseId();
          await deleteDocumentFolder(kbId, folderId, mode);
          dialog.destroy();
          if (mode) {
            MessagePlugin.success(options.t('knowledgeBase.deleteFolderTaskSubmitted'));
            trackQueuedDeletion(kbId, folderId, afterDelete);
          } else {
            await afterDelete();
            MessagePlugin.success(options.t('knowledgeBase.deleteFolderSuccess'));
            options.onChanged?.();
          }
        } catch (error: any) {
          MessagePlugin.error(error?.message || options.t('knowledgeBase.deleteFolderFailed'));
          dialog.update({
            confirmBtn: {
              content: options.t('common.confirm'),
              theme: 'danger',
              loading: false,
            },
          });
        }
      },
    });
  }

  function openDocumentDeleteDialog(
    folderId: string,
    folderName: string,
    impact: DocumentFolderDeleteImpact,
    afterDelete: AfterMutation,
  ) {
    const plan = buildDocumentFolderDeletePlan(impact);
    const mode = ref<DocumentFolderDeleteMode | ''>(plan.defaultMode || '');
    const renderBody = () => h(DocumentFolderDeleteDialogContent, {
      folderName,
      impact,
      keepDocumentsDisabled: plan.keepDocumentsDisabled,
      modelValue: mode.value,
      'onUpdate:modelValue': (value: DocumentFolderDeleteMode) => {
        mode.value = value;
      },
    });
    const confirmButton = (loading = false) => ({
      content: mode.value === 'delete_all'
        ? options.t('knowledgeBase.deleteFolderDeleteAllAction')
        : options.t('knowledgeBase.deleteFolderConfirmAction'),
      theme: mode.value === 'delete_all' ? 'danger' as const : 'primary' as const,
      disabled: !mode.value,
      loading,
    });
    let stopModeWatch = () => {};
    const dialog = DialogPlugin.confirm({
      header: options.t('knowledgeBase.deleteFolderWithName', { name: folderName }),
      width: 540,
      body: renderBody,
      confirmBtn: confirmButton(),
      cancelBtn: {
        content: options.t('common.cancel'),
      },
      closeOnOverlayClick: false,
      onClosed: () => {
        stopModeWatch();
        submitting.value = false;
      },
      onConfirm: async () => {
        if (!mode.value) return;
        dialog.update({ confirmBtn: confirmButton(true) });
        try {
          const kbId = options.knowledgeBaseId();
          await deleteDocumentFolder(kbId, folderId, mode.value);
          dialog.destroy();
          MessagePlugin.success(options.t('knowledgeBase.deleteFolderTaskSubmitted'));
          trackQueuedDeletion(kbId, folderId, afterDelete);
        } catch (error: any) {
          MessagePlugin.error(error?.message || options.t('knowledgeBase.deleteFolderFailed'));
          dialog.update({ confirmBtn: confirmButton(false) });
        }
      },
    });
    stopModeWatch = watch(mode, () => {
      dialog.update({
        body: renderBody,
        confirmBtn: confirmButton(),
      });
    });
  }

  async function confirmDelete(folderId: string, folderName: string, afterDelete: AfterMutation) {
    if (submitting.value) return;
    submitting.value = true;
    try {
      const impact = await getDocumentFolderDeleteImpact(options.knowledgeBaseId(), folderId);
      const plan = buildDocumentFolderDeletePlan(impact);
      if (plan.kind === 'documents') {
        openDocumentDeleteDialog(folderId, folderName, impact, afterDelete);
        return;
      }
      openSimpleDeleteDialog(
        folderId,
        folderName,
        impact,
        plan.defaultMode || undefined,
        afterDelete,
      );
    } catch (error: any) {
      submitting.value = false;
      MessagePlugin.error(error?.message || options.t('knowledgeBase.deleteFolderFailed'));
    }
  }

  return {
    submitting,
    createFolder,
    renameFolder,
    confirmDelete,
  };
}
