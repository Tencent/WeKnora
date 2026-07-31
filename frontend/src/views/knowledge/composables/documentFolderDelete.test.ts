import assert from 'node:assert/strict';
import test from 'node:test';

import { buildDocumentFolderDeletePlan } from './documentFolderDelete';

test('keeps the legacy confirmation for one empty folder', () => {
  assert.deepEqual(buildDocumentFolderDeletePlan({
    folder_count: 1,
    document_count: 0,
    active_document_count: 0,
  }), {
    kind: 'empty-folder',
    defaultMode: undefined,
    keepDocumentsDisabled: false,
  });
});

test('uses safe recursive deletion for an empty folder tree', () => {
  assert.deepEqual(buildDocumentFolderDeletePlan({
    folder_count: 4,
    document_count: 0,
    active_document_count: 0,
  }), {
    kind: 'empty-tree',
    defaultMode: 'keep_documents',
    keepDocumentsDisabled: false,
  });
});

test('defaults document-bearing folders to preserving documents at root', () => {
  assert.deepEqual(buildDocumentFolderDeletePlan({
    folder_count: 4,
    document_count: 326,
    active_document_count: 0,
  }), {
    kind: 'documents',
    defaultMode: 'keep_documents',
    keepDocumentsDisabled: false,
  });
});

test('requires an explicit destructive choice while documents are parsing', () => {
  assert.deepEqual(buildDocumentFolderDeletePlan({
    folder_count: 2,
    document_count: 3,
    active_document_count: 1,
  }), {
    kind: 'documents',
    defaultMode: '',
    keepDocumentsDisabled: true,
  });
});
