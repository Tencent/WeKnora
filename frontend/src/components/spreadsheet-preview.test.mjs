import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';
import { effectScope, nextTick, reactive, ref, shallowRef, watch, computed } from 'vue';
import { parse, compileTemplate } from '@vue/compiler-sfc';

const source = readFileSync(new URL('./spreadsheet-preview.vue', import.meta.url), 'utf8');
const { descriptor } = parse(source);
const script = descriptor.scriptSetup.content.slice(descriptor.scriptSetup.content.indexOf('const props ='))
  .replace('import.meta.url', "'http://localhost/spreadsheet-preview.vue'");
const executable = ts.transpile(script, { target: ts.ScriptTarget.ES2022 });

async function flush() { await nextTick(); await nextTick(); await Promise.resolve(); }
function setup(t, blob = new Blob(['test'])) {
  const props = reactive({ blob, fileType: 'xlsx', active: true });
  const workers = [], timers = new Map(), unmount = [];
  class Worker {
    messages = [];
    terminated = false;
    onmessage = null;
    onerror = null;
    constructor() { workers.push(this); }
    postMessage(data, transfer) { this.messages.push({ data, transfer }); }
    terminate() { this.terminated = true; }
    reply(data) { this.onmessage({ data }); }
  }
  const scope = effectScope();
  const state = scope.run(() => vm.runInNewContext(`${executable}; ({ loading, error, page, sheets, navigate, load })`, {
    defineProps: () => props, useI18n: () => ({ t: key => key }),
    ref, shallowRef, computed, watch, onUnmounted: cb => unmount.push(cb), Worker, URL,
    PREVIEW_ROWS: 50, PREVIEW_COLUMNS: 25, PREVIEW_MAX_BYTES: 32 * 1024 * 1024, PREVIEW_TIMEOUT_MS: 30000,
    setTimeout: cb => { const id = {}; timers.set(id, cb); return id; },
    clearTimeout: id => timers.delete(id),
  }));
  const dispose = () => { unmount.forEach(fn => fn()); scope.stop(); };
  t.after(dispose);
  return { ...state, props, workers, timers, dispose };
}
function respond(worker, id, number = 1) {
  worker.reply({ id, sheets: [{ name: 'Sheet', rows: 200, columns: 40 }], sheetIndex: 0,
    page: { columns: ['A'], rows: [{ number, cells: ['value'] }], page: number, columnPage: 1, truncated: false } });
}

test('loads off-thread with a transferred buffer and clears the pending timeout', async t => {
  const s = setup(t); await flush();
  assert.equal(s.workers.length, 1);
  const w = s.workers[0], { data, transfer } = w.messages[0];
  assert.equal(data.type, 'load'); assert.equal(data.fileType, 'xlsx');
  assert.equal(transfer[0], data.buffer);
  respond(w, data.id);
  assert.equal(s.loading.value, false); assert.equal(s.timers.size, 0);
});

test('rejects oversized files before reading or creating a worker', async t => {
  const s = setup(t, { size: 33 * 1024 * 1024, arrayBuffer: () => { throw Error('should not read'); } });
  await flush();
  assert.equal(s.error.value, 'preview.spreadsheet.tooLarge');
  assert.equal(s.loading.value, false); assert.equal(s.workers.length, 0);
});

test('switching blobs before arrayBuffer resolves never starts obsolete work', async t => {
  let resolve;
  const s = setup(t, { size: 1, arrayBuffer: () => new Promise(done => { resolve = done; }) });
  s.props.blob = new Blob(['replacement']); await flush();
  resolve(new ArrayBuffer(1)); await flush();
  assert.equal(s.workers.length, 1);
});

test('ignores obsolete page replies while the newest page is pending', async t => {
  const s = setup(t); await flush(); const w = s.workers[0];
  respond(w, w.messages[0].data.id);
  s.navigate(2); const older = w.messages.at(-1).data.id;
  s.navigate(3); const latest = w.messages.at(-1).data.id;
  respond(w, older, 2); assert.equal(s.page.value.page, 1); assert.equal(s.loading.value, true);
  respond(w, latest, 3); assert.equal(s.page.value.page, 3); assert.equal(s.loading.value, false);
});

test('timeout terminates a stalled worker and rejects its late reply', async t => {
  const s = setup(t); await flush(); const w = s.workers[0];
  [...s.timers.values()][0]();
  assert.equal(w.terminated, true); assert.equal(s.error.value, 'preview.spreadsheet.timeout');
  respond(w, w.messages[0].data.id); assert.equal(s.page.value, null);
});

test('switching sources terminates the old worker and ignores its late errors', async t => {
  const s = setup(t); await flush(); const old = s.workers[0];
  s.props.blob = new Blob(['next']); await flush();
  assert.equal(old.terminated, true);
  old.onerror(); assert.equal(s.error.value, ''); assert.equal(s.workers[1].terminated, false);
});

test('hiding cancels the worker; reactivation starts fresh; unmount cancels timers', async t => {
  const s = setup(t); await flush();
  s.props.active = false; await flush(); assert.equal(s.workers[0].terminated, true);
  assert.equal(s.timers.size, 0);
  s.props.active = true; await flush(); assert.equal(s.workers.length, 2);
  s.dispose(); assert.equal(s.workers[1].terminated, true); assert.equal(s.timers.size, 0);
});

test('worker parse errors release the workbook and expose a retryable error', async t => {
  const s = setup(t); await flush(); const w = s.workers[0];
  w.reply({ id: w.messages[0].data.id, error: 'Invalid workbook' });
  assert.equal(s.error.value, 'preview.loadFailed'); assert.equal(w.terminated, true);
  await s.load(); assert.equal(s.error.value, ''); assert.equal(s.workers.length, 2);
});

test('row pagination consumes the TDesign page-info object, preserving the column page', () => {
  const expression = descriptor.template.content.match(/@change="(\(value: \{ current: number \}\)[^"]+)"/)[1];
  const calls = [];
  const handler = vm.runInNewContext(ts.transpile(`const handler = ${expression}; handler;`), {
    navigate: (...args) => calls.push(args), page: { columnPage: 2 },
  });
  handler({ current: 3 }); assert.deepEqual(calls, [[3, 2]]);
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: 'spreadsheet-preview.vue', id: 'sheet' }).errors, []);
  assert.doesNotMatch(descriptor.template.content, /v-html/);
});
