import assert from 'node:assert/strict';
import test from 'node:test';
import { effectScope, reactive } from 'vue';
import { useMentionFileBrowser, type FileBrowserApi } from './useMentionFileBrowser';
import { adaptFilePage, adaptFolderTree, fileDisabledReason, fitBrowserViewport } from '../utils/mentionFileBrowser';
import { buildListKnowledgeFilesQuery } from '../api/knowledge-base/knowledgeFileListQuery';
import { withKnowledgeReadContext } from '../api/knowledge-base/knowledgeReadOptions';

const kb = { id: 'kb', name: 'Library' };
test('directory popup stays within the mobile visual viewport and shrinks for the keyboard', () => {
  const anchor = { left: 79, top: 355, width: 300 };
  const viewport = { left: 0, top: 0, width: 390, height: 664 };
  const style = fitBrowserViewport(anchor, viewport);
  assert.equal(Number.parseFloat(style.top) + Number.parseFloat(style.maxHeight), 656);
  assert.equal(style.minHeight, '0');
  const keyboard = fitBrowserViewport(anchor, { ...viewport, height: 250 });
  assert.equal(Number.parseFloat(keyboard.top) + Number.parseFloat(keyboard.maxHeight), 242);
  const zoomed = fitBrowserViewport({ left: -10, top: -10, width: 300 }, viewport, 1.25);
  assert.equal(Number.parseFloat(zoomed.left) * 1.25, 8);
  assert.equal(Number.parseFloat(zoomed.top) * 1.25, 8);
});
const tree = { success: true, data: { root_document_count: 1, total_document_count: 30,
  folders: [{ path: '中文/%_+#', name: '%_+#', document_count: 1, total_count: 2, children: [
    { path: '中文/%_+#/child', name: 'child', document_count: 1, total_count: 1 },
  ] }] } };
const document = (id = 'one', path = '', kbId = 'kb') => ({ id, knowledge_base_id: kbId,
  folder_path: path, file_name: 'Same.pdf', file_type: 'pdf', title: '', type: 'file', parse_status: 'processing' });
const page = (data = [document()], number = 1, total = data.length) => ({ success: true, data, page: number, page_size: 20, total });
function deferred<T = unknown>() {
  let resolve!: (value: T) => void, reject!: (value: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function harness(api?: Partial<FileBrowserApi>) {
  const access = reactive({ key: 'user:tenant:agent:session', agent_id: 'agent', agent_source_tenant_id: '2' });
  const calls: any[] = [];
  const scope = effectScope();
  const browser = scope.run(() => useMentionFileBrowser(() => access, {
    folders: async () => tree,
    files: async (id, params, options) => { calls.push({ id, params, options }); return page([document('one', params.folder_path)], params.page); },
    ...api,
  }))!;
  return { ...browser, access, calls, stop: () => scope.stop() };
}

test('root, subtree and special characters preserve the existing wire contract and agent context', () => {
  const signal = new AbortController().signal;
  const url = withKnowledgeReadContext('/knowledge?' + buildListKnowledgeFilesQuery({ page: 1, page_size: 20,
    folder_path: '中文/%_+#', folder_recursive: true }), { agent_id: 'agent &1', agent_source_tenant_id: '2', signal });
  const query = new URL(url, 'http://localhost').searchParams;
  assert.equal(query.get('folder_path'), '中文/%_+#');
  assert.equal(query.get('folder_recursive'), 'true');
  assert.equal(query.get('agent_id'), 'agent &1');
  assert.equal(query.has('signal'), false);
  assert.equal(new URLSearchParams(buildListKnowledgeFilesQuery({ page: 1, page_size: 20, folder_path: '' })).get('folder_path'), '');
  assert.equal(new URLSearchParams(buildListKnowledgeFilesQuery({ page: 1, page_size: 20 })).has('folder_path'), false);
});

test('adapters reject error envelopes, global-search envelopes and wrong KB ownership', () => {
  assert.throws(() => adaptFolderTree({ data: { folders: [] } }));
  assert.throws(() => adaptFilePage({ data: [], has_more: true }, kb));
  assert.throws(() => adaptFilePage(page([document('other', '', 'private')]), kb));
  assert.equal(adaptFilePage(page([document('a'), document('b')]), kb).items.length, 2, 'same names retain distinct IDs');
});

test('browsing is exact; searching is recursive; clearing the query restores the same directory', async () => {
  const h = harness();
  try {
    await h.selectKb(kb);
    assert.equal(h.calls[0].params.folder_path, '');
    assert.equal(h.calls[0].params.folder_recursive, false);
    assert.equal(h.calls[0].options.agent_id, 'agent');
    assert.equal(h.calls[0].options.agent_source_tenant_id, '2');
    await h.navigate('中文/%_+#');
    assert.equal(h.folders.value.length, 1);
    await h.search('query');
    assert.equal(h.calls.at(-1).params.folder_recursive, true);
    assert.equal(h.folders.value.length, 0);
    await h.search('');
    assert.equal(h.state.path, '中文/%_+#');
    assert.equal(h.calls.at(-1).params.folder_recursive, false);
    assert.equal(h.folders.value.length, 1);
  } finally { h.stop(); }
});

test('file capability follows formats, aliases and deletion, not parse completion alone', () => {
  const file = adaptFilePage(page(), kb).items[0]!;
  assert.equal(fileDisabledReason(file, []), '');
  assert.equal(fileDisabledReason(file, ['csv']), 'unsupported');
  assert.equal(fileDisabledReason({ ...file, fileName: 'sheet.xls' }, ['xlsx']), '');
  assert.equal(fileDisabledReason({ ...file, parseStatus: 'deleting' }, []), 'deleting');
  assert.equal(fileDisabledReason(file, [], [file.id]), 'selected');
});

test('file format eligibility matches global search filenames, not editable titles or parser types', () => {
  const file = (name: string, title: string, type: string, source = 'file') => adaptFilePage(page([
    { ...document(), file_name: name, title, file_type: type, type: source },
  ]), kb).items[0]!;
  assert.equal(fileDisabledReason(file('report.csv', 'report.pdf', 'pdf'), ['pdf']), 'unsupported');
  assert.equal(fileDisabledReason(file('report.PDF', 'Report', ''), ['.PDF']), '');
  assert.equal(fileDisabledReason(file('pdf', 'Report', 'pdf'), ['pdf']), 'unsupported');
  assert.equal(fileDisabledReason(file('sheet.XLS', 'Report', 'text'), ['xlsx']), '');
  assert.equal(fileDisabledReason(file('', 'Web page', 'html', 'url'), ['html']), '');
  assert.equal(fileDisabledReason(file('page.html', 'Web page', 'html'), ['html']), 'unsupported');
});

test('pagination uses server pages even when most rows cannot be selected; requests are bounded', async () => {
  let count = 0;
  const h = harness({ files: async (_id, params) => { count++; return page(Array.from({ length: 20 }, (_, i) => document(String(params.page * 20 + i))), params.page, 50); } });
  try {
    await h.selectKb(kb);
    assert.equal(h.state.items.filter(file => !fileDisabledReason(file, ['csv'])).length, 0);
    await Promise.all([h.next(), h.next()]);
    assert.equal(count, 2);
    assert.equal(h.state.page, 2);
    assert.equal(h.state.items.length, 20);
    await h.previous();
    assert.equal(h.state.page, 1);
  } finally { h.stop(); }
});

test('a failed next page retries that page without skipping or committing it', async () => {
  let fail = true;
  const requested: number[] = [];
  const h = harness({ files: async (_id, params) => {
    requested.push(params.page);
    if (params.page === 2 && fail) throw { status: 500 };
    return page([document()], params.page, 40);
  } });
  try {
    await h.selectKb(kb); await h.next();
    assert.equal(h.state.page, 1); assert.equal(h.state.items.length, 0);
    assert.equal(h.state.error, 'error');
    fail = false; await h.retry();
    assert.deepEqual(requested, [1, 2, 2]); assert.equal(h.state.page, 2);
  } finally { h.stop(); }
});

test('old KB responses and finally cannot replace the new KB or its loading state', async () => {
  const old = deferred(), fresh = deferred();
  const h = harness({ files: id => id === 'kb' ? old.promise : fresh.promise });
  try {
    const first = h.selectKb(kb);
    const second = h.selectKb({ id: 'new', name: 'New' });
    old.resolve(page()); await first;
    assert.equal(h.state.kb?.id, 'new'); assert.equal(h.state.loading, true); assert.equal(h.state.items.length, 0);
    fresh.resolve(page([document('fresh', '', 'new')])); await second;
    assert.equal(h.state.items[0]?.id, 'fresh'); assert.equal(h.state.loading, false);
  } finally { h.stop(); }
});

test('old keyword failures are ignored, including finally', async () => {
  const old = deferred(), fresh = deferred();
  const h = harness({ files: (_id, params) => params.keyword === 'old' ? old.promise : params.keyword === 'new' ? fresh.promise : Promise.resolve(page()) });
  try {
    await h.selectKb(kb);
    const a = h.search('old'), b = h.search('new');
    old.reject(new Error('network')); await a;
    assert.equal(h.state.error, ''); assert.equal(h.state.loading, true);
    fresh.resolve(page()); await b; assert.equal(h.state.keyword, 'new');
  } finally { h.stop(); }
});

for (const action of ['close', 'user', 'tenant', 'agent', 'session', 'unmount']) {
  test(`${action} invalidates pending requests without a cancellation error`, async () => {
    const wait = deferred(); let signal: AbortSignal | undefined;
    const h = harness({ files: (_id, _params, options) => { signal = options.signal; return wait.promise; } });
    const loading = h.selectKb(kb);
    if (action === 'close') h.reset(); else if (action === 'unmount') h.stop(); else h.access.key = action;
    assert.equal(signal?.aborted, true);
    wait.reject(new Error('request wrapper reports cancellation as network error')); await loading;
    assert.equal(h.state.kb, null); assert.equal(h.state.items.length, 0); assert.equal(h.state.error, '');
    h.stop();
  });
}

for (const [status, expected] of [[403, 'forbidden'], [404, 'missing'], [500, 'error']] as const) {
  test(`${status} remains distinct from an empty directory`, async () => {
    const h = harness({ files: async () => { throw { status }; } });
    try { await h.selectKb(kb); assert.equal(h.state.error, expected); assert.equal(h.state.items.length, 0); }
    finally { h.stop(); }
  });
}

test('empty root keeps subfolders visible; missing paths and out-of-folder responses fail closed', async () => {
  const h = harness({ files: async () => page([]) });
  try {
    await h.selectKb(kb); assert.equal(h.state.error, ''); assert.equal(h.folders.value.length, 1);
    await h.navigate('renamed'); assert.equal(h.state.error, 'missing');
  } finally { h.stop(); }
  const wrong = harness({ files: async () => page([document('outside', 'elsewhere')]) });
  try { await wrong.selectKb(kb); assert.equal(wrong.state.error, 'error'); assert.equal(wrong.state.items.length, 0); }
  finally { wrong.stop(); }
});
