import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('content types use a fixed enum and Chinese labels', () => {
  const types = read('../types/contentType.ts');
  const zh = read('../i18n/locales/zh-CN.ts');
  for (const value of ['article', 'book', 'webpage', 'meeting_notes', 'report', 'presentation', 'spreadsheet', 'manual', 'other']) {
    assert.match(types, new RegExp(`'${value}'`));
    assert.match(zh, new RegExp(`${value}:`));
  }
});

test('detail drawer supports rename and content type editing', () => {
  const drawer = read('./doc-content.vue');
  assert.match(drawer, /updateKnowledgeContentType/);
  assert.match(drawer, /updateKnowledge\(props\.details\.id/);
  assert.match(drawer, /knowledgeBase\.fileTypeLabel/);
  assert.match(drawer, /knowledgeBase\.contentTypeLabel/);
  assert.match(drawer, /class="doc-content-type-select"/);
  assert.match(drawer, /@change="submitContentType"/);
  assert.doesNotMatch(drawer, /contentTypeDialogVisible/);
});

test('batch toolbar exposes content type assignment', () => {
  const bar = read('../views/knowledge/components/DocumentBatchBar.vue');
  const page = read('../views/knowledge/KnowledgeBase.vue');
  assert.match(bar, /emit\('content-type'\)/);
  assert.match(page, /updateKnowledgeContentTypeBatch/);
  assert.match(page, /batchContentTypeDialogVisible/);
});
