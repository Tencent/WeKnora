import { computed, reactive } from 'vue';

import {
  type DocumentFolder,
  type DocumentFolderNode,
} from '@/api/knowledge-base';
import type { UploadFolderConflictStrategy } from '@/stores/uploadConfirm';
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess';
import {
  getLegacyFolderUploadFileName,
  getUploadDirectorySegments,
  getUploadFileDisplayName,
  makeUploadFolderCopyName,
} from '../utils/folderUploadPaths';

export type UploadIssueKind = 'failed' | 'skipped' | 'cancelled';

export interface UploadIssue {
  name: string;
  reason: string;
  kind: UploadIssueKind;
}

export interface UploadBatchState {
  visible: boolean;
  running: boolean;
  phase: 'idle' | 'preparing' | 'uploading' | 'done';
  cancelRequested: boolean;
  total: number;
  completed: number;
  success: number;
  skipped: number;
  cancelled: number;
  issues: UploadIssue[];
}

export interface UploadBatchResult {
  successCount: number;
  failCount: number;
  skippedCount?: number;
  cancelledCount?: number;
  issues?: UploadIssue[];
}

interface FolderUploadCache {
  childrenByLookup: Map<string, DocumentFolderNode[]>;
  folderIdByParentAndName: Map<string, string>;
  parentByFolderId: Map<string, string>;
  skippedLookupKeys: Set<string>;
  createdFolders: Array<{ id: string; parentId: string }>;
  createdCount: number;
}

interface UploadNotification {
  success(message: string): unknown;
  warning(message: string): unknown;
  error(message: string): unknown;
  info(message: string): unknown;
}

export interface FolderUploadBatchDependencies {
  createFolder(
    knowledgeBaseId: string,
    parentId: string,
    name: string,
  ): Promise<DocumentFolder>;
  deleteFolder(knowledgeBaseId: string, folderId: string): Promise<unknown>;
  listFolders(
    knowledgeBaseId: string,
    parentId: string,
    options: { keyword: string; cursor: string; page_size: number },
  ): Promise<{
    folders?: DocumentFolderNode[];
    next_cursor?: string;
    has_more?: boolean;
  }>;
  uploadFile(
    knowledgeBaseId: string,
    data: {
      file: File;
      tag_ids?: string[];
      process_config?: KnowledgeProcessOverrides;
      folder_id?: string;
      fileName?: string;
    },
    onProgress?: (progressEvent: any) => void,
    signal?: AbortSignal,
  ): Promise<unknown>;
  notify: UploadNotification;
}

export interface UseFolderUploadBatchOptions {
  getKnowledgeBaseId(): string;
  getBaseFolderId(): string;
  getSelectedTagIds(): string[];
  setUploading(uploading: boolean): void;
  translate(key: string, params?: Record<string, unknown>): string;
  onKnowledgeUploaded(knowledgeBaseId: string): void;
  onFoldersChanged(): void;
  shouldCreateDocumentFolders?(): boolean;
}

class FolderUploadSkipError extends Error {}

const getFolderCacheKey = (parentId: string, name: string) => JSON.stringify([parentId, name]);

const isDocumentFolderConflict = (error: any) => (
  error?.status === 409
  || error?.response?.status === 409
);

const uploadErrorCode = (value: any) => (
  value?.code
  || value?.error?.code
  || value?.response?.data?.code
  || value?.response?.data?.error?.code
  || ''
);

const isUploadCancelledError = (value: any) => (
  uploadErrorCode(value) === 'ERR_CANCELED'
  || value?.name === 'CanceledError'
);

export function useFolderUploadBatch(
  options: UseFolderUploadBatchOptions,
  dependencies: FolderUploadBatchDependencies,
) {
  const uploadBatchState = reactive<UploadBatchState>({
    visible: false,
    running: false,
    phase: 'idle',
    cancelRequested: false,
    total: 0,
    completed: 0,
    success: 0,
    skipped: 0,
    cancelled: 0,
    issues: [],
  });
  const activeUploadControllers = new Set<AbortController>();
  const t = options.translate;

  const uploadBatchPercent = computed(() => (
    uploadBatchState.total > 0
      ? Math.round((uploadBatchState.completed / uploadBatchState.total) * 100)
      : 0
  ));
  const uploadFailedCount = computed(() => (
    uploadBatchState.issues.filter(issue => issue.kind === 'failed').length
  ));
  const uploadBatchTitle = computed(() => {
    if (uploadBatchState.phase === 'preparing') return t('knowledgeBase.uploadPreparingFolders');
    if (uploadBatchState.running) return t('knowledgeBase.uploadBatchRunning');
    return t('knowledgeBase.uploadBatchComplete');
  });

  const rememberUploadFolder = (
    cache: FolderUploadCache,
    lookupKey: string,
    parentId: string,
    folderId: string,
  ) => {
    cache.folderIdByParentAndName.set(lookupKey, folderId);
    cache.parentByFolderId.set(folderId, parentId);
  };

  const rememberCreatedUploadFolder = (
    cache: FolderUploadCache,
    lookupKey: string,
    parentId: string,
    folderId: string,
  ) => {
    cache.createdCount += 1;
    cache.createdFolders.push({ id: folderId, parentId });
    rememberUploadFolder(cache, lookupKey, parentId, folderId);
  };

  const createRenamedUploadFolder = async (
    targetKbId: string,
    parentId: string,
    sourceName: string,
    lookupKey: string,
    visibleSiblings: DocumentFolderNode[],
    cache: FolderUploadCache,
  ) => {
    let copyIndex = 1;
    const escapedName = sourceName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const suffixPattern = new RegExp(`^${escapedName} \\((\\d+)\\)$`);
    for (const sibling of visibleSiblings) {
      const match = suffixPattern.exec(sibling.name);
      if (match) copyIndex = Math.max(copyIndex, Number(match[1]) + 1);
    }

    for (; copyIndex <= 5000; copyIndex += 1) {
      const candidateName = makeUploadFolderCopyName(sourceName, copyIndex);
      try {
        const created = await dependencies.createFolder(targetKbId, parentId, candidateName);
        rememberCreatedUploadFolder(cache, lookupKey, parentId, created.id);
        return created.id;
      } catch (error) {
        if (!isDocumentFolderConflict(error)) throw error;
      }
    }

    throw new Error(t('knowledgeBase.createFolderFailed'));
  };

  const resolveExistingUploadFolder = async (
    targetKbId: string,
    parentId: string,
    name: string,
    cacheKey: string,
    existing: DocumentFolderNode,
    siblings: DocumentFolderNode[],
    cache: FolderUploadCache,
    conflictStrategy: UploadFolderConflictStrategy,
  ) => {
    if (conflictStrategy === 'skip') {
      cache.skippedLookupKeys.add(cacheKey);
      throw new FolderUploadSkipError(t('knowledgeBase.folderUploadConflictSkipped'));
    }
    if (conflictStrategy === 'rename') {
      return createRenamedUploadFolder(
        targetKbId,
        parentId,
        name,
        cacheKey,
        siblings,
        cache,
      );
    }
    rememberUploadFolder(cache, cacheKey, parentId, existing.id);
    return existing.id;
  };

  const loadUploadFolderChildren = async (
    targetKbId: string,
    parentId: string,
    name: string,
    cache: FolderUploadCache,
    force = false,
  ) => {
    const lookupKey = getFolderCacheKey(parentId, name);
    if (!force) {
      const cached = cache.childrenByLookup.get(lookupKey);
      if (cached) return cached;
    }

    const children: DocumentFolderNode[] = [];
    let cursor = '';
    while (true) {
      const response = await dependencies.listFolders(targetKbId, parentId, {
        keyword: name,
        cursor,
        page_size: 200,
      });
      children.push(...(response?.folders ?? []));
      if (children.some(folder => folder.name === name)) break;

      const nextCursor = response?.next_cursor || '';
      if (!response?.has_more || !nextCursor || nextCursor === cursor) break;
      cursor = nextCursor;
    }
    cache.childrenByLookup.set(lookupKey, children);
    return children;
  };

  const ensureUploadFolder = async (
    targetKbId: string,
    parentId: string,
    rawName: string,
    cache: FolderUploadCache,
    conflictStrategy: UploadFolderConflictStrategy,
  ) => {
    const name = rawName.trim();
    const cacheKey = getFolderCacheKey(parentId, name);
    if (cache.skippedLookupKeys.has(cacheKey)) {
      throw new FolderUploadSkipError(t('knowledgeBase.folderUploadConflictSkipped'));
    }
    const cachedFolderId = cache.folderIdByParentAndName.get(cacheKey);
    if (cachedFolderId) return cachedFolderId;

    let children = await loadUploadFolderChildren(targetKbId, parentId, name, cache);
    let existing = children.find(folder => folder.name === name);
    if (existing) {
      return resolveExistingUploadFolder(
        targetKbId,
        parentId,
        name,
        cacheKey,
        existing,
        children,
        cache,
        conflictStrategy,
      );
    }

    try {
      const created = await dependencies.createFolder(targetKbId, parentId, name);
      rememberCreatedUploadFolder(cache, cacheKey, parentId, created.id);
      cache.childrenByLookup.set(cacheKey, [
        ...children,
        {
          ...created,
          document_count: 0,
          has_children: false,
        },
      ]);
      return created.id;
    } catch (error) {
      if (!isDocumentFolderConflict(error)) throw error;
      children = await loadUploadFolderChildren(targetKbId, parentId, name, cache, true);
      existing = children.find(folder => folder.name === name);
      if (!existing) throw error;
      return resolveExistingUploadFolder(
        targetKbId,
        parentId,
        name,
        cacheKey,
        existing,
        children,
        cache,
        conflictStrategy,
      );
    }
  };

  const resolveUploadFolder = async (
    targetKbId: string,
    baseFolderId: string,
    segments: string[],
    cache: FolderUploadCache,
    conflictStrategy: UploadFolderConflictStrategy,
  ) => {
    let parentId = baseFolderId;
    for (const segment of segments) {
      parentId = await ensureUploadFolder(
        targetKbId,
        parentId,
        segment,
        cache,
        conflictStrategy,
      );
    }
    return parentId;
  };

  const cleanupUnusedCreatedUploadFolders = async (
    targetKbId: string,
    cache: FolderUploadCache,
    successfulDestinationIds: Set<string>,
  ) => {
    if (cache.createdFolders.length === 0) return;

    const createdIds = new Set(cache.createdFolders.map(folder => folder.id));
    const foldersToKeep = new Set<string>();
    for (const destinationId of successfulDestinationIds) {
      let currentId = destinationId;
      while (currentId) {
        if (createdIds.has(currentId)) foldersToKeep.add(currentId);
        currentId = cache.parentByFolderId.get(currentId) || '';
      }
    }

    for (let index = cache.createdFolders.length - 1; index >= 0; index -= 1) {
      const folder = cache.createdFolders[index];
      if (foldersToKeep.has(folder.id)) continue;
      try {
        await dependencies.deleteFolder(targetKbId, folder.id);
      } catch {
        // A concurrent writer may have made the folder non-empty. Preserving
        // it is safer than deleting content that this batch does not own.
      }
    }
  };

  const showUploadResultMessages = (
    successCount: number,
    failCount: number,
    totalCount: number,
    mode: 'document' | 'folder',
  ) => {
    if (mode === 'folder') {
      if (failCount === 0) {
        dependencies.notify.success(t('knowledgeBase.uploadAllSuccess', { count: successCount }));
      } else if (successCount > 0) {
        dependencies.notify.warning(t('knowledgeBase.uploadPartialSuccess', {
          success: successCount,
          fail: failCount,
        }));
      } else {
        dependencies.notify.error(t('knowledgeBase.uploadAllFailed'));
      }
      return;
    }

    if (totalCount === 1) {
      if (successCount === 1) {
        dependencies.notify.success(t('knowledgeBase.uploadSuccess'));
      }
      return;
    }

    if (failCount === 0) {
      dependencies.notify.success(t('knowledgeBase.allUploadSuccess', { count: successCount }));
    } else if (successCount > 0) {
      dependencies.notify.warning(t('knowledgeBase.partialUploadSuccess', {
        success: successCount,
        fail: failCount,
      }));
    } else {
      dependencies.notify.error(t('knowledgeBase.allUploadFailed', { count: failCount }));
    }
  };

  const beginUploadBatch = (total: number) => {
    activeUploadControllers.forEach(controller => controller.abort());
    activeUploadControllers.clear();
    uploadBatchState.visible = true;
    uploadBatchState.running = true;
    uploadBatchState.phase = 'preparing';
    uploadBatchState.cancelRequested = false;
    uploadBatchState.total = total;
    uploadBatchState.completed = 0;
    uploadBatchState.success = 0;
    uploadBatchState.skipped = 0;
    uploadBatchState.cancelled = 0;
    uploadBatchState.issues = [];
    options.setUploading(true);
  };

  const addUploadIssue = (file: File, reason: string, kind: UploadIssueKind) => {
    uploadBatchState.issues.push({
      name: getUploadFileDisplayName(file),
      reason,
      kind,
    });
    if (kind === 'skipped') uploadBatchState.skipped += 1;
    if (kind === 'cancelled') uploadBatchState.cancelled += 1;
  };

  const uploadErrorMessage = (value: any) => (
    value?.error?.message
    || value?.response?.data?.error?.message
    || value?.message
    || t('knowledgeBase.uploadFailed')
  );

  const cancelUploadBatch = () => {
    if (!uploadBatchState.running || uploadBatchState.cancelRequested) return;
    uploadBatchState.cancelRequested = true;
  };

  const closeUploadBatch = () => {
    if (uploadBatchState.running) return;
    uploadBatchState.visible = false;
  };

  const disposeUploadBatch = () => {
    activeUploadControllers.forEach(controller => controller.abort());
    activeUploadControllers.clear();
  };

  const executeUploadBatch = async (
    files: File[],
    batchOptions: {
      processConfig?: KnowledgeProcessOverrides;
      folderConflictStrategy?: UploadFolderConflictStrategy;
      tagIds?: string[];
    } = {},
  ): Promise<UploadBatchResult> => {
    const targetKbId = options.getKnowledgeBaseId();
    if (!targetKbId || files.length === 0) {
      return { successCount: 0, failCount: files.length };
    }

    beginUploadBatch(files.length);
    const selectedTagIds = batchOptions.tagIds ?? options.getSelectedTagIds();
    const tagIdsToUpload = selectedTagIds.length > 0 ? [...selectedTagIds] : undefined;
    const totalCount = files.length;
    const createDocumentFolders = options.shouldCreateDocumentFolders?.() ?? true;
    const hasFolderPaths = files.some(file => getUploadDirectorySegments(file).length > 0);
    const folderCache: FolderUploadCache = {
      childrenByLookup: new Map(),
      folderIdByParentAndName: new Map(),
      parentByFolderId: new Map(),
      skippedLookupKeys: new Set(),
      createdFolders: [],
      createdCount: 0,
    };
    const folderConflictStrategy = batchOptions.folderConflictStrategy || 'merge';
    const prepared: Array<{ file: File; destinationFolderId: string }> = [];
    let preparationIndex = 0;
    for (; preparationIndex < files.length; preparationIndex += 1) {
      const file = files[preparationIndex];
      if (uploadBatchState.cancelRequested) break;
      const directorySegments = getUploadDirectorySegments(file);
      let destinationFolderId = options.getBaseFolderId();

      if (createDocumentFolders && directorySegments.length > 0) {
        try {
          destinationFolderId = await resolveUploadFolder(
            targetKbId,
            options.getBaseFolderId(),
            directorySegments,
            folderCache,
            folderConflictStrategy,
          );
        } catch (error: any) {
          uploadBatchState.completed += 1;
          if (error instanceof FolderUploadSkipError) {
            addUploadIssue(file, error.message, 'skipped');
          } else {
            addUploadIssue(
              file,
              t('knowledgeBase.folderUploadPrepareFailed', {
                path: getUploadFileDisplayName(file),
                error: error?.message || t('knowledgeBase.createFolderFailed'),
              }),
              'failed',
            );
          }
          continue;
        }
      }
      prepared.push({ file, destinationFolderId });
    }

    if (uploadBatchState.cancelRequested) {
      for (let index = preparationIndex; index < files.length; index += 1) {
        addUploadIssue(files[index], t('knowledgeBase.uploadCancelledItem'), 'cancelled');
        uploadBatchState.completed += 1;
      }
    }

    uploadBatchState.phase = 'uploading';
    let nextPreparedIndex = 0;
    const successfulDestinationIds = new Set<string>();
    const uploadOne = async (file: File, destinationFolderId: string) => {
      const controller = new AbortController();
      activeUploadControllers.add(controller);
      try {
        const uploadData: {
          file: File;
          tag_ids?: string[];
          process_config?: KnowledgeProcessOverrides;
          folder_id?: string;
          fileName?: string;
        } = { file, tag_ids: tagIdsToUpload };
        if (createDocumentFolders) {
          uploadData.folder_id = destinationFolderId;
        } else {
          const fileName = getLegacyFolderUploadFileName(file);
          if (fileName) uploadData.fileName = fileName;
        }
        if (batchOptions.processConfig) {
          uploadData.process_config = batchOptions.processConfig;
        }

        const responseData: any = await dependencies.uploadFile(
          targetKbId,
          uploadData,
          undefined,
          controller.signal,
        );
        const isSuccess = responseData?.success
          || responseData?.code === 200
          || responseData?.status === 'success'
          || (!responseData?.error && responseData);
        if (isSuccess) {
          uploadBatchState.success += 1;
          successfulDestinationIds.add(destinationFolderId);
        } else {
          const code = uploadErrorCode(responseData);
          if (code === 'duplicate_file') {
            addUploadIssue(file, t('knowledgeBase.fileExists'), 'skipped');
          } else {
            addUploadIssue(file, uploadErrorMessage(responseData), 'failed');
          }
        }
      } catch (error: any) {
        const code = uploadErrorCode(error);
        if (isUploadCancelledError(error)) {
          addUploadIssue(file, t('knowledgeBase.uploadCancelledItem'), 'cancelled');
        } else if (code === 'duplicate_file') {
          addUploadIssue(file, t('knowledgeBase.fileExists'), 'skipped');
        } else {
          addUploadIssue(file, uploadErrorMessage(error), 'failed');
        }
      } finally {
        activeUploadControllers.delete(controller);
        uploadBatchState.completed += 1;
      }
    };

    for (; nextPreparedIndex < prepared.length; nextPreparedIndex += 1) {
      if (uploadBatchState.cancelRequested) break;
      const item = prepared[nextPreparedIndex];
      await uploadOne(item.file, item.destinationFolderId);
    }

    if (uploadBatchState.cancelRequested) {
      for (let index = nextPreparedIndex; index < prepared.length; index += 1) {
        addUploadIssue(prepared[index].file, t('knowledgeBase.uploadCancelledItem'), 'cancelled');
        uploadBatchState.completed += 1;
      }
    }

    await cleanupUnusedCreatedUploadFolders(
      targetKbId,
      folderCache,
      successfulDestinationIds,
    );

    const successCount = uploadBatchState.success;
    const skippedCount = uploadBatchState.skipped;
    const cancelledCount = uploadBatchState.cancelled;
    const failCount = uploadBatchState.issues.filter(issue => issue.kind === 'failed').length;
    uploadBatchState.completed = totalCount;
    uploadBatchState.running = false;
    uploadBatchState.phase = 'done';
    options.setUploading(false);

    if (successCount > 0) {
      options.onKnowledgeUploaded(targetKbId);
    }
    if (successCount > 0 || folderCache.createdCount > 0) {
      options.onFoldersChanged();
    }

    if (cancelledCount > 0) {
      dependencies.notify.info(t('knowledgeBase.uploadCancelledSummary', {
        success: successCount,
        cancelled: cancelledCount,
      }));
    } else if (successCount === 0 && failCount === 0 && skippedCount > 0) {
      dependencies.notify.info(t('knowledgeBase.uploadAllSkippedSummary', {
        count: skippedCount,
      }));
    } else {
      showUploadResultMessages(
        successCount,
        failCount + skippedCount,
        totalCount,
        hasFolderPaths ? 'folder' : 'document',
      );
    }
    return {
      successCount,
      failCount,
      skippedCount,
      cancelledCount,
      issues: [...uploadBatchState.issues],
    };
  };

  return {
    uploadBatchState,
    uploadBatchPercent,
    uploadFailedCount,
    uploadBatchTitle,
    executeUploadBatch,
    cancelUploadBatch,
    closeUploadBatch,
    disposeUploadBatch,
  };
}
