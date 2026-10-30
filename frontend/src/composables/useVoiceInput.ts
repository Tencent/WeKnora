import { getCurrentInstance, onBeforeUnmount, ref } from 'vue';
import { recordingToWav, type VoiceClip } from '../utils/wavEncoder.ts';

// preparing: the microphone is being opened; speech is not captured yet.
export type VoiceInputState = 'idle' | 'preparing' | 'recording' | 'transcribing';
export type VoiceInputError =
  | 'unsupported' | 'insecure' | 'permission' | 'device' | 'decode' | 'transcribe' | 'empty' | 'releasedEarly';

export interface VoiceInputTarget {
  agentId: string;
  agentSourceTenantId?: string;
  locale?: string;
}

const MIN_CLIP_MS = 300;
const DEFAULT_MAX_MS = 60_000;
// Speech trails off after the key comes up; keep recording briefly so the
// last syllable is not cut.
export const VOICE_TAIL_MS = 300;
// Opening a Bluetooth or Continuity microphone can take a second, which used
// to swallow the first words. Keep the stream open briefly between presses.
export const VOICE_WARM_MS = 500;

export function isVoiceInputSupported(): boolean {
  return typeof navigator !== 'undefined'
    && !!navigator.mediaDevices?.getUserMedia
    && typeof MediaRecorder !== 'undefined'
    && typeof OfflineAudioContext !== 'undefined';
}

export function useVoiceInput(options: {
  target: () => VoiceInputTarget | null;
  transcribe: (wav: Blob, target: VoiceInputTarget) => Promise<string>;
  onText: (text: string) => void;
  onError: (error: VoiceInputError, detail?: unknown) => void;
  maxDurationMs?: number;
  toWav?: (recording: Blob) => Promise<VoiceClip>;
}) {
  const toWav = options.toWav ?? recordingToWav;
  const state = ref<VoiceInputState>('idle');
  const elapsedMs = ref(0);
  const maxDurationMs = options.maxDurationMs ?? DEFAULT_MAX_MS;

  let stream: MediaStream | null = null;
  let recorder: MediaRecorder | null = null;
  let chunks: Blob[] = [];
  let startedAt: number | null = null;
  let tickTimer: ReturnType<typeof setInterval> | null = null;
  let tailTimer: ReturnType<typeof setTimeout> | null = null;
  let releaseTimer: ReturnType<typeof setTimeout> | null = null;
  // Set when the key comes up while the microphone is still opening.
  let releasedWhilePreparing = false;
  let discard = false;
  let disposed = false;

  const streamIsLive = () => !!stream?.getAudioTracks().some(track => track.readyState === 'live');

  const releaseStream = () => {
    if (releaseTimer) clearTimeout(releaseTimer);
    releaseTimer = null;
    stream?.getTracks().forEach(track => track.stop());
    stream = null;
  };

  const scheduleRelease = () => {
    if (releaseTimer) clearTimeout(releaseTimer);
    releaseTimer = setTimeout(releaseStream, VOICE_WARM_MS);
  };

  const clearRecordingTimers = () => {
    if (tickTimer) clearInterval(tickTimer);
    if (tailTimer) clearTimeout(tailTimer);
    tickTimer = null;
    tailTimer = null;
  };

  const transcribe = async (recording: Blob, durationMs: number) => {
    const target = options.target();
    if (durationMs < MIN_CLIP_MS || !target) {
      state.value = 'idle';
      return;
    }
    state.value = 'transcribing';
    let clip: VoiceClip;
    try {
      clip = await toWav(recording);
    } catch (err) {
      state.value = 'idle';
      options.onError('decode', err);
      return;
    }
    console.debug('[voice input] speech check', clip.stats);
    if (!clip.speech) {
      state.value = 'idle';
      options.onError('empty');
      return;
    }
    try {
      const text = (await options.transcribe(clip.wav, target)).trim();
      if (text) options.onText(text);
      else options.onError('empty');
    } catch (err) {
      options.onError('transcribe', err);
    } finally {
      state.value = 'idle';
    }
  };

  const openStream = async (): Promise<boolean> => {
    if (streamIsLive()) return true;
    releaseStream();
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
      });
      return true;
    } catch (err) {
      const name = (err as DOMException)?.name;
      options.onError(name === 'NotAllowedError' || name === 'SecurityError' ? 'permission' : 'device', err);
      return false;
    }
  };

  const start = async () => {
    if (state.value !== 'idle') return;
    if (typeof window !== 'undefined' && !window.isSecureContext) {
      options.onError('insecure');
      return;
    }
    if (!isVoiceInputSupported()) {
      options.onError('unsupported');
      return;
    }
    if (releaseTimer) clearTimeout(releaseTimer);
    releaseTimer = null;
    state.value = 'preparing';
    elapsedMs.value = 0;
    releasedWhilePreparing = false;
    discard = false;

    if (!(await openStream())) {
      state.value = 'idle';
      return;
    }
    if (disposed) {
      releaseStream();
      return;
    }
    if (state.value !== 'preparing' || releasedWhilePreparing || discard) {
      if (releasedWhilePreparing && !discard) options.onError('releasedEarly');
      scheduleRelease();
      state.value = 'idle';
      return;
    }

    chunks = [];
    const active = new MediaRecorder(stream!);
    recorder = active;
    active.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data); };
    active.onstart = () => {
      const begin = Date.now();
      startedAt = begin;
      state.value = 'recording';
      tickTimer = setInterval(() => {
        elapsedMs.value = Date.now() - begin;
        if (elapsedMs.value >= maxDurationMs && active.state === 'recording') active.stop();
      }, 200);
    };
    active.onstop = () => {
      const durationMs = startedAt === null ? 0 : Date.now() - startedAt;
      const type = active.mimeType || chunks[0]?.type || '';
      clearRecordingTimers();
      recorder = null;
      startedAt = null;
      if (disposed) releaseStream();
      else scheduleRelease();
      if (discard) {
        state.value = 'idle';
        return;
      }
      void transcribe(new Blob(chunks, { type }), durationMs);
    };
    active.start();
  };

  const stop = () => {
    if (state.value === 'preparing') {
      if (recorder) {
        // Recorder created but onstart not fired yet: nothing useful captured.
        discard = true;
        recorder.stop();
      } else {
        releasedWhilePreparing = true;
      }
      return;
    }
    if (state.value !== 'recording' || !recorder || tailTimer) return;
    const active = recorder;
    tailTimer = setTimeout(() => {
      tailTimer = null;
      if (active.state !== 'inactive') active.stop();
    }, VOICE_TAIL_MS);
  };

  const cancel = () => {
    if (state.value !== 'preparing' && state.value !== 'recording') return;
    discard = true;
    clearRecordingTimers();
    if (recorder && recorder.state !== 'inactive') recorder.stop();
  };

  if (getCurrentInstance()) {
    onBeforeUnmount(() => {
      disposed = true;
      cancel();
      releaseStream();
    });
  }

  return { state, elapsedMs, start, stop, cancel };
}
