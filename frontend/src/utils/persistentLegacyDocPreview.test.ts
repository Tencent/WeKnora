import test from 'node:test';
import assert from 'node:assert/strict';

import {
  PreviewProtocolError,
  PreviewWaitTimeoutError,
  decodeKnowledgePreviewControl,
  waitForKnowledgePreview,
} from './previewRequest';

function pendingBlob(retryAfter = 2): Blob {
  return new Blob(
    [JSON.stringify({ code: 'preview_pending', retry_after: retryAfter })],
    { type: 'application/json; charset=utf-8' },
  );
}

test('persistent knowledge preview polls pending response until DOCX is ready', async () => {
  const ready = new Blob(['PK\u0003\u0004docx'], {
    type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  });
  const attempts: number[] = [];
  let pendingCount = 0;

  const result = await waitForKnowledgePreview(
    async (_signal, attempt) => {
      attempts.push(attempt);
      return attempt === 0 ? pendingBlob() : ready;
    },
    {
      signal: new AbortController().signal,
      sleep: async () => {},
      onPending: () => { pendingCount += 1; },
    },
  );

  assert.equal(result, ready);
  assert.deepEqual(attempts, [0, 1]);
  assert.equal(pendingCount, 1);
});

test('persistent knowledge preview stops polling on terminal unsupported response', async () => {
  const terminal = { status: 415, code: 'preview_unsupported' };
  let calls = 0;

  await assert.rejects(
    waitForKnowledgePreview(
      async () => {
        calls += 1;
        if (calls === 1) return pendingBlob();
        throw terminal;
      },
      { signal: new AbortController().signal, sleep: async () => {} },
    ),
    error => error === terminal,
  );
  assert.equal(calls, 2);
});

test('JSON control responses never pass through as renderable document blobs', async () => {
  const secret = 'private backend failure';
  for (const blob of [
    new Blob(['{broken'], { type: 'application/json' }),
    new Blob([JSON.stringify({ code: 'unknown', message: secret })], { type: 'application/json' }),
  ]) {
    await assert.rejects(
      decodeKnowledgePreviewControl(blob),
      error => error instanceof PreviewProtocolError && !error.message.includes(secret),
    );
  }
});

test('persistent knowledge preview wait is bounded', async () => {
  let elapsed = 0;
  let calls = 0;

  await assert.rejects(
    waitForKnowledgePreview(
      async () => {
        calls += 1;
        return pendingBlob();
      },
      {
        signal: new AbortController().signal,
        maxWaitMs: 5000,
        now: () => elapsed,
        sleep: async delay => { elapsed += delay; },
      },
    ),
    PreviewWaitTimeoutError,
  );
  assert.equal(calls, 3);
  assert.equal(elapsed, 4000);
});

test('aborting while waiting stops the active preview loop', async () => {
  const controller = new AbortController();

  await assert.rejects(
    waitForKnowledgePreview(
      async () => pendingBlob(),
      {
        signal: controller.signal,
        onPending: () => controller.abort(),
      },
    ),
    error => error instanceof Error && error.name === 'AbortError',
  );
});

test('overall deadline aborts a stalled in-flight preview request', async () => {
  let requestAborted = false;

  await assert.rejects(
    waitForKnowledgePreview(
      signal => new Promise<Blob>((_resolve, reject) => {
        signal.addEventListener('abort', () => {
          requestAborted = true;
          reject(new Error('transport aborted'));
        }, { once: true });
      }),
      {
        signal: new AbortController().signal,
        maxWaitMs: 10,
      },
    ),
    PreviewWaitTimeoutError,
  );
  assert.equal(requestAborted, true);
});

test('successful wait removes deadline and external abort listeners', async () => {
  const controller = new AbortController();
  let requestSignal: AbortSignal | undefined;
  const ready = new Blob(['ready'], { type: 'application/octet-stream' });

  const result = await waitForKnowledgePreview(
    async signal => {
      requestSignal = signal;
      return ready;
    },
    { signal: controller.signal, maxWaitMs: 10 },
  );
  assert.equal(result, ready);
  assert.equal(requestSignal?.aborted, false);

  controller.abort();
  await new Promise(resolve => setTimeout(resolve, 15));
  assert.equal(requestSignal?.aborted, false);
});

test('external abort remains an abort and cleans up the deadline', async () => {
  const controller = new AbortController();
  let requestSignal: AbortSignal | undefined;
  const waiting = waitForKnowledgePreview(
    signal => {
      requestSignal = signal;
      return new Promise<Blob>(() => {});
    },
    { signal: controller.signal, maxWaitMs: 20 },
  );

  controller.abort();
  await assert.rejects(
    waiting,
    error => error instanceof Error && error.name === 'AbortError',
  );
  assert.equal(requestSignal?.aborted, true);

  await new Promise(resolve => setTimeout(resolve, 25));
});

test('component binds persistent wait, explicit retry, and request cancellation', async () => {
  const { readFileSync } = await import('node:fs');
  const component = readFileSync(new URL('../components/document-preview.vue', import.meta.url), 'utf8');
  const api = readFileSync(new URL('../api/knowledge-base/index.ts', import.meta.url), 'utf8');

  assert.match(component, /waitForKnowledgePreview/);
  assert.match(component, /retryFailed && attempt === 0/);
  assert.match(component, /previewRequests\.isCurrent\(signal\)/);
  assert.match(component, /previewRequests\.cancel\(\)/);
  assert.match(api, /retry \? '\?retry=1' : ''/);
});
