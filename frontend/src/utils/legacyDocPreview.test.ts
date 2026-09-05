import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { getPreviewMimeType, resolveKnowledgePreviewExt, resolvePreviewKind } from './filePreview';

test('real OLE and DOCX fixtures route by returned representation', () => {
  const ole = readFileSync(new URL('../../../docreader/tests/fixtures/legacy_preview.doc', import.meta.url));
  const docx = readFileSync(new URL('../../../docreader/tests/fixtures/legacy_preview.docx', import.meta.url));
  assert.equal(ole.subarray(0, 8).toString('hex'), 'd0cf11e0a1b11ae1');
  assert.equal(docx.subarray(0, 2).toString(), 'PK');
  for (const [blob, knowledge, expected] of [
    [new Blob([ole], { type: 'application/msword' }), true, 'unsupported'],
    [new Blob([docx], { type: getPreviewMimeType('docx') }), true, 'docx'],
    [new Blob([docx], { type: getPreviewMimeType('docx') }), false, 'unsupported'],
  ] as const) {
    assert.equal(resolvePreviewKind(resolveKnowledgePreviewExt('doc', blob.type, knowledge)), expected);
  }
});

test('request scope discards out-of-order responses and aborts retired work', async () => {
  const { PreviewRequestScope } = await import('./previewRequest');
  const scope = new PreviewRequestScope();
  let resolveA!: (value: string) => void;
  let resolveB!: (value: string) => void;
  const a = new Promise<string>(resolve => { resolveA = resolve; });
  const b = new Promise<string>(resolve => { resolveB = resolve; });
  const published: string[] = [];
  const signalA = scope.start();
  let aborts = 0;
  signalA.addEventListener('abort', () => { aborts++; });
  const runA = a.then(value => { if (scope.isCurrent(signalA)) published.push(value); });
  const signalB = scope.start();
  const runB = b.then(value => { if (scope.isCurrent(signalB)) published.push(value); });
  resolveB('B'); await runB;
  resolveA('A'); await runA;
  assert.deepEqual(published, ['B']);
  assert.equal(aborts, 1);
  scope.cancel();
  assert.equal(signalB.aborted, true);
  assert.equal(scope.isCurrent(signalB), false);
});

test('only explicit conversion fallback is unsupported', async () => {
  const { isUnsupportedPreviewError } = await import('./previewRequest');
  assert.equal(isUnsupportedPreviewError({ status: 415, code: 'preview_unsupported' }), true);
  for (const error of [
    { status: 401 }, { status: 403 }, { status: 500 },
    { status: 415, message: 'private error' }, { message: 'network failure' }, null,
  ]) assert.equal(isUnsupportedPreviewError(error), false);
});

test('component connects cancellation and uses a detached DOCX render target', () => {
  const component = readFileSync(new URL('../components/document-preview.vue', import.meta.url), 'utf8');
  assert.match(component, /previewKnowledgeFile\(props.knowledgeId, signal, retry\)/);
  assert.match(component, /previewRequests.isCurrent\(signal\)/);
  assert.match(component, /renderDocument\(parsedDocument, target/);
  assert.match(component, /previewRequests.cancel\(\)/);
});
