import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DEFAULT_DOCUMENT_SORT,
  DOCUMENT_SORT_OPTIONS,
  getDocumentSortOption,
  getDocumentSortParams,
} from './documentSorting';

test('文档排序默认使用更新时间倒序', () => {
  assert.equal(DEFAULT_DOCUMENT_SORT, 'updated_desc');
  assert.deepEqual(getDocumentSortParams(DEFAULT_DOCUMENT_SORT), {
    sort_by: 'updated_at',
    sort_order: 'desc',
  });
});

test('文档排序提供三组共六个选项并映射到服务端白名单参数', () => {
  assert.equal(DOCUMENT_SORT_OPTIONS.length, 6);
  assert.deepEqual(
    DOCUMENT_SORT_OPTIONS.map(({ value, sortBy, sortOrder }) => [value, sortBy, sortOrder]),
    [
      ['updated_desc', 'updated_at', 'desc'],
      ['updated_asc', 'updated_at', 'asc'],
      ['created_desc', 'created_at', 'desc'],
      ['created_asc', 'created_at', 'asc'],
      ['name_asc', 'file_name', 'asc'],
      ['name_desc', 'file_name', 'desc'],
    ],
  );
});

test('未知排序值会安全回退到默认选项', () => {
  assert.equal(getDocumentSortOption('invalid' as never).value, DEFAULT_DOCUMENT_SORT);
});
