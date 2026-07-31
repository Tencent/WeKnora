import assert from 'node:assert/strict';
import test from 'node:test';

import {
  useFolderUploadBatch,
  type FolderUploadBatchDependencies,
} from './useFolderUploadBatch';

const makeFile = (path: string) => {
  const name = path.split('/').at(-1) || path;
  const file = new File(['content'], name);
  Object.defineProperty(file, 'webkitRelativePath', { value: path });
  return file;
};

const makeFolder = (id: string, parentId: string, name: string) => ({
  id,
  tenant_id: 1,
  knowledge_base_id: 'kb-1',
  parent_id: parentId,
  name,
  path: name,
  depth: 1,
  created_at: '',
  updated_at: '',
});

const makeNotifications = () => {
  const calls: Array<{ level: string; message: string }> = [];
  return {
    calls,
    notify: {
      success: (message: string) => calls.push({ level: 'success', message }),
      warning: (message: string) => calls.push({ level: 'warning', message }),
      error: (message: string) => calls.push({ level: 'error', message }),
      info: (message: string) => calls.push({ level: 'info', message }),
    },
  };
};

test('failed folder upload removes newly-created empty folders from leaf to root', async () => {
  const created: Array<{ id: string; parentId: string; name: string }> = [];
  const deleted: string[] = [];
  const notifications = makeNotifications();
  let folderRefreshes = 0;
  let uploading = false;

  const dependencies: FolderUploadBatchDependencies = {
    createFolder: async (_kbId, parentId, name) => {
      const id = `folder-${created.length + 1}`;
      created.push({ id, parentId, name });
      return makeFolder(id, parentId, name);
    },
    deleteFolder: async (_kbId, folderId) => {
      deleted.push(folderId);
    },
    listFolders: async () => ({ folders: [], has_more: false }),
    uploadFile: async () => ({ error: { message: 'upload failed' } }),
    notify: notifications.notify,
  };
  const batch = useFolderUploadBatch({
    getKnowledgeBaseId: () => 'kb-1',
    getBaseFolderId: () => '',
    getSelectedTagIds: () => [],
    setUploading: value => { uploading = value; },
    translate: key => key,
    onKnowledgeUploaded: () => assert.fail('failed upload must not emit success'),
    onFoldersChanged: () => { folderRefreshes += 1; },
  }, dependencies);

  const result = await batch.executeUploadBatch([
    makeFile('root/child/document.txt'),
  ]);

  assert.deepEqual(
    created.map(({ parentId, name }) => ({ parentId, name })),
    [
      { parentId: '', name: 'root' },
      { parentId: 'folder-1', name: 'child' },
    ],
  );
  assert.deepEqual(deleted, ['folder-2', 'folder-1']);
  assert.equal(result.successCount, 0);
  assert.equal(result.failCount, 1);
  assert.equal(batch.uploadBatchState.phase, 'done');
  assert.equal(batch.uploadBatchState.completed, 1);
  assert.equal(uploading, false);
  assert.equal(folderRefreshes, 1);
  assert.equal(notifications.calls.at(-1)?.level, 'error');
});

test('cancelling a running batch finishes the active upload and skips queued files', async () => {
  const notifications = makeNotifications();
  let releaseUpload!: (value: unknown) => void;
  const uploadGate = new Promise(resolve => { releaseUpload = resolve; });
  let markStarted!: () => void;
  const started = new Promise<void>(resolve => { markStarted = resolve; });
  let uploadCalls = 0;
  let uploadedEventCount = 0;

  const dependencies: FolderUploadBatchDependencies = {
    createFolder: async (_kbId, parentId, name) => makeFolder('unused', parentId, name),
    deleteFolder: async () => {},
    listFolders: async () => ({ folders: [], has_more: false }),
    uploadFile: async () => {
      uploadCalls += 1;
      markStarted();
      return uploadGate;
    },
    notify: notifications.notify,
  };
  const batch = useFolderUploadBatch({
    getKnowledgeBaseId: () => 'kb-1',
    getBaseFolderId: () => '',
    getSelectedTagIds: () => [],
    setUploading: () => {},
    translate: key => key,
    onKnowledgeUploaded: () => { uploadedEventCount += 1; },
    onFoldersChanged: () => {},
  }, dependencies);

  const running = batch.executeUploadBatch([
    makeFile('first.txt'),
    makeFile('second.txt'),
  ]);
  await started;
  batch.cancelUploadBatch();
  releaseUpload({ success: true });
  const result = await running;

  assert.equal(uploadCalls, 1);
  assert.equal(result.successCount, 1);
  assert.equal(result.cancelledCount, 1);
  assert.equal(batch.uploadBatchState.completed, 2);
  assert.equal(batch.uploadBatchState.cancelled, 1);
  assert.equal(batch.uploadBatchState.running, false);
  assert.equal(uploadedEventCount, 1);
  assert.equal(notifications.calls.at(-1)?.level, 'info');
});

test('folder uploads submit files sequentially', async () => {
  const notifications = makeNotifications();
  let activeUploads = 0;
  let maxActiveUploads = 0;
  let releaseUploads!: () => void;
  const uploadGate = new Promise<void>(resolve => { releaseUploads = resolve; });
  let markUploadStarted!: () => void;
  const uploadStarted = new Promise<void>(resolve => { markUploadStarted = resolve; });

  const dependencies: FolderUploadBatchDependencies = {
    createFolder: async (_kbId, parentId, name) => makeFolder('folder-root', parentId, name),
    deleteFolder: async () => {},
    listFolders: async () => ({ folders: [], has_more: false }),
    uploadFile: async () => {
      activeUploads += 1;
      maxActiveUploads = Math.max(maxActiveUploads, activeUploads);
      markUploadStarted();
      await uploadGate;
      activeUploads -= 1;
      return { success: true };
    },
    notify: notifications.notify,
  };
  const batch = useFolderUploadBatch({
    getKnowledgeBaseId: () => 'kb-1',
    getBaseFolderId: () => '',
    getSelectedTagIds: () => [],
    setUploading: () => {},
    translate: key => key,
    onKnowledgeUploaded: () => {},
    onFoldersChanged: () => {},
  }, dependencies);

  const running = batch.executeUploadBatch([
    makeFile('root/first.txt'),
    makeFile('root/second.txt'),
    makeFile('root/third.txt'),
  ]);
  await uploadStarted;
  const observedMaxActiveUploads = maxActiveUploads;
  releaseUploads();
  await running;

  assert.equal(observedMaxActiveUploads, 1);
});
