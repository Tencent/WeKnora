import assert from 'node:assert/strict';
import test from 'node:test';

import {
  mapMentionFolderSearchPage,
  mapMentionFolderSearchResults,
} from './mentionFolderSearch';

test('maps typed server folder results without recursively loading the folder tree', () => {
  const items = mapMentionFolderSearchResults([
    {
      id: 'folder-1',
      name: '发布说明',
      path: '产品资料/发布说明',
      parent_id: 'folder-0',
      knowledge_base_id: 'kb-1',
      knowledge_base_name: '产品知识库',
    },
  ], {
    allowedKbIds: ['kb-1'],
    orgNameByKbId: { 'kb-1': '共享空间' },
  });

  assert.deepEqual(items, [{
    id: 'folder-1',
    name: '发布说明',
    type: 'folder',
    kbId: 'kb-1',
    kbName: '产品知识库',
    orgName: '共享空间',
    folderPath: '产品资料/发布说明',
    parentId: 'folder-0',
    hasChildren: false,
  }]);
});

test('drops a server result outside the client requested KB subset', () => {
  const items = mapMentionFolderSearchResults([
    {
      id: 'folder-1',
      name: '设计',
      path: '设计',
      parent_id: '',
      knowledge_base_id: 'kb-not-selected',
      knowledge_base_name: '未选择知识库',
    },
  ], {
    allowedKbIds: ['kb-selected'],
  });

  assert.deepEqual(items, []);
});

test('preserves independent folder pagination metadata', () => {
  const page = mapMentionFolderSearchPage({
    data: [
      {
        id: 'folder-allowed',
        name: '设计',
        path: '产品/设计',
        parent_id: 'folder-parent',
        knowledge_base_id: 'kb-allowed',
        knowledge_base_name: '产品知识库',
      },
      {
        id: 'folder-filtered',
        name: '设计归档',
        path: '归档/设计',
        parent_id: '',
        knowledge_base_id: 'kb-filtered',
        knowledge_base_name: '其他知识库',
      },
    ],
    total: 21,
    has_more: true,
  }, {
    allowedKbIds: ['kb-allowed'],
  });

  assert.equal(page.items.length, 1);
  assert.equal(page.total, 21);
  assert.equal(page.hasMore, true);
  assert.equal(page.consumed, 2);
});
