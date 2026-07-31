import assert from 'node:assert/strict';
import test from 'node:test';

import { createDocumentListPager } from './documentListPager';

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

test('a page-1 refresh cancels an in-flight page 2 and restarts scrolling at page 2', async () => {
  const pager = createDocumentListPager();
  pager.beginFirstPage();
  const stalePageTwo = pager.beginNextPage();
  assert.equal(stalePageTwo?.page, 2);
  assert.equal(pager.isLoadingMore(), true);

  const pending = deferred();
  const staleCompletion = pending.promise.finally(() => {
    if (stalePageTwo) pager.finishNextPage(stalePageTwo);
  });

  const refresh = pager.beginFirstPage();
  assert.equal(refresh.page, 1);
  assert.equal(pager.currentPage(), 1);
  assert.equal(pager.isLoadingMore(), false);

  const currentPageTwo = pager.beginNextPage();
  assert.equal(currentPageTwo?.page, 2);
  assert.equal(pager.isLoadingMore(), true);

  pending.resolve();
  await staleCompletion;
  assert.equal(pager.isLoadingMore(), true);

  if (currentPageTwo) pager.finishNextPage(currentPageTwo);
  assert.equal(pager.isLoadingMore(), false);
});
