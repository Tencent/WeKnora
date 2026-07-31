import assert from 'node:assert/strict';
import test from 'node:test';

import type { DocumentFolderNode } from '@/api/knowledge-base';
import { useDocumentFolderPages } from './useDocumentFolderPages';

function folder(id: string): DocumentFolderNode {
  return {
    id,
    name: id,
    path: id,
    tenant_id: 1,
    knowledge_base_id: 'kb-1',
    parent_id: '',
    depth: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    document_count: 0,
    has_children: false,
  };
}

test('loads and appends cursor pages without duplicate folder ids', async () => {
  const requestedCursors: Array<string | undefined> = [];
  const pages = useDocumentFolderPages({
    knowledgeBaseId: () => 'kb-1',
    mapFolder: item => item,
    errorMessage: () => 'failed',
    fetchPage: async (_kbId, _parentId, options) => {
      requestedCursors.push(options?.cursor);
      if (!options?.cursor) {
        return {
          parent_id: '',
          folders: [folder('one'), folder('two')],
          next_cursor: 'next',
          has_more: true,
        };
      }
      return {
        parent_id: '',
        folders: [folder('two'), folder('three')],
        next_cursor: '',
        has_more: false,
      };
    },
  });

  await pages.load();
  await pages.load({ append: true });

  assert.deepEqual(pages.items.value.map(item => item.id), ['one', 'two', 'three']);
  assert.deepEqual(requestedCursors, [undefined, 'next']);
  assert.equal(pages.hasMore.value, false);
});

test('ignores a stale folder response after navigation starts a newer load', async () => {
  const resolvers = new Map<string, (value: any) => void>();
  let parentId = 'first';
  const pages = useDocumentFolderPages({
    knowledgeBaseId: () => 'kb-1',
    parentId: () => parentId,
    mapFolder: item => item,
    errorMessage: () => 'failed',
    fetchPage: async (_kbId, requestedParentId) => new Promise((resolve) => {
      resolvers.set(requestedParentId, resolve);
    }),
  });

  const firstLoad = pages.load();
  parentId = 'second';
  const secondLoad = pages.load();
  resolvers.get('second')?.({
    parent_id: 'second',
    folders: [folder('new')],
    next_cursor: '',
    has_more: false,
  });
  await secondLoad;
  resolvers.get('first')?.({
    parent_id: 'first',
    folders: [folder('stale')],
    next_cursor: '',
    has_more: false,
  });
  await firstLoad;

  assert.deepEqual(pages.items.value.map(item => item.id), ['new']);
  assert.equal(pages.error.value, '');
});

test('reuses a recent cached folder page and preserves cache across reset', async () => {
  let calls = 0;
  const pages = useDocumentFolderPages({
    knowledgeBaseId: () => 'kb-1',
    mapFolder: item => item,
    errorMessage: () => 'failed',
    cacheTtlMs: 30_000,
    fetchPage: async () => {
      calls += 1;
      return {
        parent_id: '',
        folders: [folder('cached')],
        next_cursor: '',
        has_more: false,
      };
    },
  });

  await pages.load();
  pages.reset();
  await pages.load();

  assert.equal(calls, 1);
  assert.deepEqual(pages.items.value.map(item => item.id), ['cached']);
});
