/** Owns the lifetime of a mounted preview's asynchronous work. */
export class PreviewRequestScope {
  private controller?: AbortController;

  start(): AbortSignal {
    this.cancel();
    this.controller = new AbortController();
    return this.controller.signal;
  }

  isCurrent(signal: AbortSignal): boolean {
    return this.controller?.signal === signal && !signal.aborted;
  }

  cancel(): void {
    this.controller?.abort();
    this.controller = undefined;
  }
}

const MAX_PREVIEW_CONTROL_BYTES = 4096;
const DEFAULT_PREVIEW_RETRY_SECONDS = 2;
const MAX_PREVIEW_RETRY_SECONDS = 10;

export type KnowledgePreviewControl =
  | { kind: 'pending'; retryAfterSeconds: number }
  | { kind: 'unsupported' };

export class PreviewProtocolError extends Error {
  constructor() {
    super('Invalid document preview response');
    this.name = 'PreviewProtocolError';
  }
}

export class PreviewWaitTimeoutError extends Error {
  constructor() {
    super('Document preview generation timed out');
    this.name = 'PreviewWaitTimeoutError';
  }
}

function abortError(): Error {
  const error = new Error('Document preview request aborted');
  error.name = 'AbortError';
  return error;
}

function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) throw abortError();
}

function isJSONBlob(blob: Blob): boolean {
  return blob.type.toLowerCase().split(';', 1)[0].trim() === 'application/json';
}

/** Decode the small JSON control messages returned only by knowledge previews. */
export async function decodeKnowledgePreviewControl(blob: Blob): Promise<KnowledgePreviewControl | null> {
  if (!isJSONBlob(blob)) return null;
  if (blob.size > MAX_PREVIEW_CONTROL_BYTES) throw new PreviewProtocolError();

  let body: unknown;
  try {
    body = JSON.parse(await blob.text());
  } catch {
    throw new PreviewProtocolError();
  }
  if (!body || typeof body !== 'object') throw new PreviewProtocolError();

  const value = body as { code?: unknown; retry_after?: unknown };
  if (value.code === 'preview_unsupported') return { kind: 'unsupported' };
  if (value.code !== 'preview_pending') throw new PreviewProtocolError();

  const requestedSeconds = typeof value.retry_after === 'number' && Number.isFinite(value.retry_after)
    ? value.retry_after
    : DEFAULT_PREVIEW_RETRY_SECONDS;
  return {
    kind: 'pending',
    retryAfterSeconds: Math.min(MAX_PREVIEW_RETRY_SECONDS, Math.max(1, requestedSeconds)),
  };
}

type PreviewFetch = (signal: AbortSignal, attempt: number) => Promise<Blob>;

type PreviewWaitOptions = {
  signal: AbortSignal;
  maxWaitMs?: number;
  onPending?: (control: Extract<KnowledgePreviewControl, { kind: 'pending' }>) => void;
  now?: () => number;
  sleep?: (delayMs: number, signal: AbortSignal) => Promise<void>;
};

type PreviewWaitCancellation = {
  signal: AbortSignal;
  stop: Promise<never>;
  timedOut: () => boolean;
  cleanup: () => void;
};

function createPreviewWaitCancellation(externalSignal: AbortSignal, maxWaitMs: number): PreviewWaitCancellation {
  throwIfAborted(externalSignal);
  const controller = new AbortController();
  let deadlineReached = false;
  let rejectStop!: (reason?: unknown) => void;
  const stop = new Promise<never>((_, reject) => {
    rejectStop = reject;
  });
  // A stop may occur between awaited operations. Attach a handler immediately
  // while retaining the original rejecting promise for Promise.race below.
  void stop.catch(() => {});

  const onExternalAbort = () => {
    rejectStop(abortError());
    controller.abort();
  };
  externalSignal.addEventListener('abort', onExternalAbort, { once: true });

  const deadlineTimer = globalThis.setTimeout(() => {
    deadlineReached = true;
    rejectStop(new PreviewWaitTimeoutError());
    controller.abort();
  }, Math.max(0, maxWaitMs));

  return {
    signal: controller.signal,
    stop,
    timedOut: () => deadlineReached,
    cleanup: () => {
      globalThis.clearTimeout(deadlineTimer);
      externalSignal.removeEventListener('abort', onExternalAbort);
    },
  };
}

function waitUntilStopped<T>(operation: Promise<T>, cancellation: PreviewWaitCancellation): Promise<T> {
  return Promise.race([operation, cancellation.stop]);
}

function abortableDelay(delayMs: number, signal: AbortSignal): Promise<void> {
  throwIfAborted(signal);
  return new Promise((resolve, reject) => {
    const timer = globalThis.setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, delayMs);
    const onAbort = () => {
      globalThis.clearTimeout(timer);
      reject(abortError());
    };
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

/** Poll a knowledge DOC preview until its persistent DOCX copy is ready. */
export async function waitForKnowledgePreview(
  fetchPreview: PreviewFetch,
  options: PreviewWaitOptions,
): Promise<Blob> {
  const maxWaitMs = options.maxWaitMs ?? 60_000;
  const now = options.now ?? Date.now;
  const sleep = options.sleep ?? abortableDelay;
  const startedAt = now();
  const cancellation = createPreviewWaitCancellation(options.signal, maxWaitMs);

  try {
    for (let attempt = 0; ; attempt += 1) {
      throwIfAborted(cancellation.signal);
      const blob = await waitUntilStopped(fetchPreview(cancellation.signal, attempt), cancellation);
      throwIfAborted(cancellation.signal);
      if (now() - startedAt > maxWaitMs) throw new PreviewWaitTimeoutError();
      const control = await waitUntilStopped(decodeKnowledgePreviewControl(blob), cancellation);
      throwIfAborted(cancellation.signal);
      if (!control) return blob;
      if (control.kind === 'unsupported') {
        throw { status: 415, code: 'preview_unsupported' };
      }

      options.onPending?.(control);
      throwIfAborted(cancellation.signal);
      const serverDelayMs = control.retryAfterSeconds * 1000;
      const backoffMs = Math.min(5000, 1000 * (2 ** Math.min(attempt, 3)));
      const delayMs = Math.max(serverDelayMs, backoffMs);
      if (now() - startedAt + delayMs > maxWaitMs) throw new PreviewWaitTimeoutError();
      await waitUntilStopped(sleep(delayMs, cancellation.signal), cancellation);
    }
  } catch (error) {
    if (cancellation.timedOut() && !options.signal.aborted) throw new PreviewWaitTimeoutError();
    throw error;
  } finally {
    cancellation.cleanup();
  }
}

export function isUnsupportedPreviewError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const value = error as { status?: number; code?: string };
  return value.status === 415 && value.code === 'preview_unsupported';
}
