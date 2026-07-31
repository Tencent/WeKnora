import assert from 'node:assert/strict';
import test from 'node:test';

import {
  applyDocumentFolderSelectionChange,
  getDocumentFolderMentionBlockReason,
  resolveDocumentFolderListFilter,
  runDocumentFolderCapabilityCheck,
  shouldFetchStarterSuggestions,
} from './documentFolderCapability';

test('folder list omits scope until the capability is enabled', () => {
  assert.equal(resolveDocumentFolderListFilter(false, true, ''), undefined);
  assert.equal(resolveDocumentFolderListFilter(false, true, 'folder-a'), undefined);
});

test('folder list omits scope until a document knowledge base is ready', () => {
  assert.equal(resolveDocumentFolderListFilter(true, false, ''), undefined);
  assert.equal(resolveDocumentFolderListFilter(true, false, 'folder-a'), undefined);
});

test('folder list preserves all three states when enabled', () => {
  assert.equal(resolveDocumentFolderListFilter(true, true, undefined), undefined);
  assert.equal(resolveDocumentFolderListFilter(true, true, ''), '');
  assert.equal(resolveDocumentFolderListFilter(true, true, 'folder-a'), 'folder-a');
});

test('folder mention gate allows requests without a folder selection', () => {
  assert.equal(getDocumentFolderMentionBlockReason(false, false, true), null);
});

test('folder mention gate blocks restored scope while capability is unavailable', () => {
  assert.equal(
    getDocumentFolderMentionBlockReason(true, false, false),
    'capability-unavailable',
  );
});

test('folder mention gate blocks scope in smart reasoning mode', () => {
  assert.equal(
    getDocumentFolderMentionBlockReason(true, true, true),
    'smart-reasoning',
  );
});

test('folder mention gate allows scope in quick answer mode', () => {
  assert.equal(getDocumentFolderMentionBlockReason(true, true, false), null);
});

test('starter suggestions are disabled while a folder scope is selected', () => {
  assert.equal(shouldFetchStarterSuggestions(0), true);
  assert.equal(shouldFetchStarterSuggestions(1), false);
});

test('capability check is single-flight and releases its lock', async () => {
  const lock = { value: false };
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });

  const first = runDocumentFolderCapabilityCheck(lock, () => pending);
  assert.equal(lock.value, true);
  assert.equal(await runDocumentFolderCapabilityCheck(lock, async () => undefined), false);

  release();
  assert.equal(await first, true);
  assert.equal(lock.value, false);
});

test('capability check releases its lock after an error', async () => {
  const lock = { value: false };

  await assert.rejects(
    runDocumentFolderCapabilityCheck(lock, async () => {
      throw new Error('system info unavailable');
    }),
    /system info unavailable/,
  );
  assert.equal(lock.value, false);
});

test('restored folder changes stay transient while ordinary changes persist', () => {
  const calls: string[] = [];

  applyDocumentFolderSelectionChange(
    true,
    () => calls.push('transient'),
    () => calls.push('persistent'),
  );
  applyDocumentFolderSelectionChange(
    false,
    () => calls.push('transient'),
    () => calls.push('persistent'),
  );

  assert.deepEqual(calls, ['transient', 'persistent']);
});
