import assert from 'node:assert/strict';
import test from 'node:test';

import type { DocumentFolderSelection } from '../components/documentFolderViewTypes';
import {
  documentFolderSelectionFromRouteQuery,
  hasSameDocumentFolderRouteQuery,
  isSameDocumentFolderLocation,
  withDocumentFolderSelectionRouteQuery,
} from './documentFolderRouteState.ts';

const labels = { root: '根目录', all: '全部文档' };

test('folder route state round-trips the current directory and its breadcrumb trail', () => {
  const selection: DocumentFolderSelection = {
    id: 'folder-child',
    kind: 'folder',
    name: '接口设计',
    path: ['研发资料', '接口设计'],
    trail: [
      { id: 'folder-parent', name: '研发资料' },
      { id: 'folder-child', name: '接口设计' },
    ],
  };

  const query = withDocumentFolderSelectionRouteQuery({ tab: 'documents' }, selection);

  assert.deepEqual(query, {
    tab: 'documents',
    folder_id: 'folder-child',
    folder_path: '研发资料/接口设计',
    folder_trail: 'folder-parent/folder-child',
  });
  assert.deepEqual(documentFolderSelectionFromRouteQuery(query, labels), selection);
});

test('a folder URL without ancestor IDs still restores the requested folder view', () => {
  assert.deepEqual(
    documentFolderSelectionFromRouteQuery({
      folder_id: 'folder-child',
      folder_path: '研发资料/接口设计',
    }, labels),
    {
      id: 'folder-child',
      kind: 'folder',
      name: '接口设计',
      path: ['研发资料', '接口设计'],
      trail: [],
    },
  );
});

test('root and all-documents views remove stale folder metadata', () => {
  const staleQuery = {
    tab: 'graph',
    folder_id: 'stale',
    folder_path: '旧目录',
    folder_trail: 'stale',
  };

  assert.deepEqual(
    withDocumentFolderSelectionRouteQuery(staleQuery, {
      id: '',
      kind: 'root',
      name: '根目录',
      path: [],
      trail: [],
    }),
    { tab: 'graph' },
  );

  const allQuery = withDocumentFolderSelectionRouteQuery(staleQuery, {
    id: undefined,
    kind: 'all',
    name: '全部文档',
    path: [],
    trail: [],
  });
  assert.deepEqual(allQuery, { tab: 'graph', folder_scope: 'all' });
  assert.equal(documentFolderSelectionFromRouteQuery(allQuery, labels).kind, 'all');
});

test('malformed folder route state falls back to root', () => {
  assert.equal(
    documentFolderSelectionFromRouteQuery({ folder_id: 'folder-child' }, labels).kind,
    'root',
  );
  assert.equal(
    documentFolderSelectionFromRouteQuery({
      folder_id: 'folder-child',
      folder_path: '研发资料//接口设计',
    }, labels).kind,
    'root',
  );
});

test('route comparison distinguishes navigation from breadcrumb enrichment', () => {
  const partial = documentFolderSelectionFromRouteQuery({
    folder_id: 'folder-child',
    folder_path: '研发资料/接口设计',
  }, labels);
  const complete = documentFolderSelectionFromRouteQuery({
    folder_id: 'folder-child',
    folder_path: '研发资料/接口设计',
    folder_trail: 'folder-parent/folder-child',
  }, labels);

  assert.equal(isSameDocumentFolderLocation(partial, complete), true);
  assert.equal(
    hasSameDocumentFolderRouteQuery(
      { folder_id: 'folder-child', folder_path: '研发资料/接口设计' },
      {
        folder_id: 'folder-child',
        folder_path: '研发资料/接口设计',
        folder_trail: 'folder-parent/folder-child',
      },
    ),
    false,
  );
});
