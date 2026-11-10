import assert from 'node:assert/strict'
import test from 'node:test'

import { encodeWav, hasSpeech, padWithSilence, VOICE_PAD_MS, VOICE_SAMPLE_RATE } from './wavEncoder.ts'

const tone = (ms: number, amplitude: number) =>
  Float32Array.from({ length: (VOICE_SAMPLE_RATE * ms) / 1000 }, (_, i) =>
    amplitude * Math.sin((2 * Math.PI * 220 * i) / VOICE_SAMPLE_RATE))

const concat = (...parts: Float32Array[]) => {
  const out = new Float32Array(parts.reduce((n, p) => n + p.length, 0))
  let offset = 0
  for (const p of parts) { out.set(p, offset); offset += p.length }
  return out
}

test('hasSpeech rejects silence and room noise', () => {
  assert.equal(hasSpeech(new Float32Array(VOICE_SAMPLE_RATE * 2), VOICE_SAMPLE_RATE), false)
  assert.equal(hasSpeech(tone(2000, 0.002), VOICE_SAMPLE_RATE), false)
})

// Deterministic hiss: a microphone whose noise floor is above the absolute floor.
const hiss = (ms: number, amplitude: number) => {
  let seed = 42
  return Float32Array.from({ length: (VOICE_SAMPLE_RATE * ms) / 1000 }, () => {
    seed = (seed * 1103515245 + 12345) % 2 ** 31
    return amplitude * ((seed / 2 ** 31) * 2 - 1)
  })
}

test('hasSpeech rejects steady noise louder than the absolute floor', () => {
  assert.equal(hasSpeech(hiss(2000, 0.05), VOICE_SAMPLE_RATE), false)
})

test('hasSpeech finds speech above a noisy floor', () => {
  const noisy = hiss(2000, 0.02)
  noisy.set(tone(400, 0.2).map((s, i) => s + noisy[8000 + i]), 8000)
  assert.equal(hasSpeech(noisy, VOICE_SAMPLE_RATE), true)
})

test('hasSpeech ignores a click shorter than 100ms', () => {
  assert.equal(hasSpeech(concat(tone(1000, 0), tone(60, 0.3), tone(1000, 0)), VOICE_SAMPLE_RATE), false)
})

test('hasSpeech accepts a voiced segment inside silence', () => {
  assert.equal(hasSpeech(concat(tone(800, 0), tone(300, 0.1), tone(800, 0)), VOICE_SAMPLE_RATE), true)
})

const ascii = (view: DataView, offset: number, length: number) =>
  String.fromCharCode(...Array.from({ length }, (_, i) => view.getUint8(offset + i)))

test('encodeWav writes a 16-bit mono PCM header', () => {
  const buffer = encodeWav(new Float32Array(10), VOICE_SAMPLE_RATE)
  const view = new DataView(buffer)

  assert.equal(buffer.byteLength, 44 + 20)
  assert.equal(ascii(view, 0, 4), 'RIFF')
  assert.equal(view.getUint32(4, true), 36 + 20)
  assert.equal(ascii(view, 8, 4), 'WAVE')
  assert.equal(ascii(view, 12, 4), 'fmt ')
  assert.equal(view.getUint16(20, true), 1)
  assert.equal(view.getUint16(22, true), 1)
  assert.equal(view.getUint32(24, true), 16000)
  assert.equal(view.getUint32(28, true), 32000)
  assert.equal(view.getUint16(32, true), 2)
  assert.equal(view.getUint16(34, true), 16)
  assert.equal(ascii(view, 36, 4), 'data')
  assert.equal(view.getUint32(40, true), 20)
})

test('encodeWav clamps and scales samples', () => {
  const view = new DataView(encodeWav(new Float32Array([0, 1, -1, 2, -2, 0.5]), 16000))
  const sample = (i: number) => view.getInt16(44 + i * 2, true)

  assert.equal(sample(0), 0)
  assert.equal(sample(1), 32767)
  assert.equal(sample(2), -32768)
  assert.equal(sample(3), 32767)
  assert.equal(sample(4), -32768)
  assert.equal(sample(5), Math.trunc(0.5 * 0x7fff))
})

test('padWithSilence adds the lead-in and lead-out without touching the speech', () => {
  const speech = Float32Array.from([0.5, -0.5, 0.25])
  const padded = padWithSilence(speech, VOICE_SAMPLE_RATE)
  const pad = (VOICE_SAMPLE_RATE * VOICE_PAD_MS) / 1000
  assert.equal(padded.length, speech.length + pad * 2)
  assert.ok(padded.subarray(0, pad).every(s => s === 0))
  assert.deepEqual(Array.from(padded.subarray(pad, pad + speech.length)), Array.from(speech))
  assert.ok(padded.subarray(pad + speech.length).every(s => s === 0))
})
