// 16-bit PCM WAV encoding for dictated audio. WAV is accepted by every
// OpenAI-compatible /audio/transcriptions backend, unlike the webm/opus or mp4
// containers MediaRecorder produces depending on the browser.

export const VOICE_SAMPLE_RATE = 16000;

const writeAscii = (view: DataView, offset: number, text: string) => {
  for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i));
};

export function encodeWav(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const bytesPerSample = 2;
  const dataSize = samples.length * bytesPerSample;
  const buffer = new ArrayBuffer(44 + dataSize);
  const view = new DataView(buffer);

  writeAscii(view, 0, 'RIFF');
  view.setUint32(4, 36 + dataSize, true);
  writeAscii(view, 8, 'WAVE');
  writeAscii(view, 12, 'fmt ');
  view.setUint32(16, 16, true); // PCM chunk size
  view.setUint16(20, 1, true); // PCM format
  view.setUint16(22, 1, true); // mono
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * bytesPerSample, true);
  view.setUint16(32, bytesPerSample, true);
  view.setUint16(34, 16, true);
  writeAscii(view, 36, 'data');
  view.setUint32(40, dataSize, true);

  let offset = 44;
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    offset += bytesPerSample;
  }
  return buffer;
}

// ASR models hallucinate on silence and room noise (qwen3-asr answers them
// with 《小猫钓鱼》 or 嗯。), so such clips must never be sent. Microphone gain
// varies too much for a fixed level, so a frame counts as voiced only when it
// clears both an absolute floor and a multiple of the clip's own noise floor
// (its quietest frames). 150ms of voiced 20ms frames make a clip speech.
const SPEECH_FRAME_MS = 20;
const SPEECH_RMS_FLOOR = 0.01;
const SPEECH_NOISE_RATIO = 4;
const SPEECH_NOISE_PERCENTILE = 0.2;
const SPEECH_MIN_MS = 150;

export interface SpeechStats {
  speech: boolean;
  noiseFloor: number;
  threshold: number;
  peak: number;
  voicedMs: number;
}

export function analyzeSpeech(samples: Float32Array, sampleRate: number): SpeechStats {
  const frame = Math.max(1, Math.round((sampleRate * SPEECH_FRAME_MS) / 1000));
  const levels: number[] = [];
  for (let start = 0; start + frame <= samples.length; start += frame) {
    let sum = 0;
    for (let i = start; i < start + frame; i++) sum += samples[i] * samples[i];
    levels.push(Math.sqrt(sum / frame));
  }
  if (levels.length === 0) return { speech: false, noiseFloor: 0, threshold: 0, peak: 0, voicedMs: 0 };

  const sorted = [...levels].sort((a, b) => a - b);
  const noiseFloor = sorted[Math.floor(SPEECH_NOISE_PERCENTILE * (sorted.length - 1))];
  const threshold = Math.max(SPEECH_RMS_FLOOR, noiseFloor * SPEECH_NOISE_RATIO);
  const voicedMs = levels.filter(level => level >= threshold).length * SPEECH_FRAME_MS;
  return {
    speech: voicedMs >= SPEECH_MIN_MS,
    noiseFloor,
    threshold,
    peak: sorted[sorted.length - 1],
    voicedMs,
  };
}

export function hasSpeech(samples: Float32Array, sampleRate: number): boolean {
  return analyzeSpeech(samples, sampleRate).speech;
}

// Some ASR models drop the first or last syllable when speech touches the edge
// of the clip, so the uploaded WAV gets a short lead-in and lead-out.
export const VOICE_PAD_MS = 100;

export function padWithSilence(samples: Float32Array, sampleRate: number, padMs = VOICE_PAD_MS): Float32Array {
  const pad = Math.round((sampleRate * padMs) / 1000);
  const padded = new Float32Array(samples.length + pad * 2);
  padded.set(samples, pad);
  return padded;
}

export interface VoiceClip {
  wav: Blob;
  speech: boolean;
  stats: SpeechStats;
}

// Decodes whatever MediaRecorder produced and renders it to 16kHz mono WAV.
// OfflineAudioContext with one output channel downmixes and resamples.
export async function recordingToWav(recording: Blob): Promise<VoiceClip> {
  const decodeCtx = new AudioContext();
  let decoded: AudioBuffer;
  try {
    decoded = await decodeCtx.decodeAudioData(await recording.arrayBuffer());
  } finally {
    void decodeCtx.close();
  }
  const frames = Math.max(1, Math.ceil(decoded.duration * VOICE_SAMPLE_RATE));
  const offline = new OfflineAudioContext(1, frames, VOICE_SAMPLE_RATE);
  const source = offline.createBufferSource();
  source.buffer = decoded;
  source.connect(offline.destination);
  source.start();
  const rendered = await offline.startRendering();
  const samples = rendered.getChannelData(0);
  // Analyze before padding: the added silence would drag the noise floor down.
  const stats = analyzeSpeech(samples, VOICE_SAMPLE_RATE);
  const padded = padWithSilence(samples, VOICE_SAMPLE_RATE);
  return {
    wav: new Blob([encodeWav(padded, VOICE_SAMPLE_RATE)], { type: 'audio/wav' }),
    speech: stats.speech,
    stats,
  };
}
