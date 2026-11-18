import assert from 'node:assert/strict'
import test, { mock } from 'node:test'

import { useVoiceInput, VOICE_TAIL_MS, VOICE_WARM_MS, type VoiceInputError } from './useVoiceInput.ts'

const flush = () => new Promise(resolve => setImmediate(resolve))

class FakeTrack {
  readyState = 'live'
  stop() { this.readyState = 'ended' }
}

class FakeMediaRecorder {
  static instances: FakeMediaRecorder[] = []
  state = 'inactive'
  mimeType = 'audio/webm'
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstart: (() => void) | null = null
  onstop: (() => void) | null = null
  constructor() { FakeMediaRecorder.instances.push(this) }
  start() {
    this.state = 'recording'
    this.onstart?.()
  }
  stop() {
    this.state = 'inactive'
    this.ondataavailable?.({ data: new Blob(['pcm']) })
    this.onstop?.()
  }
}

function setup(opts: { getUserMedia?: () => Promise<unknown>; speech?: boolean } = {}) {
  const tracks: FakeTrack[] = []
  let opens = 0
  const getUserMedia = opts.getUserMedia ?? (async () => {
    opens++
    const track = new FakeTrack()
    tracks.push(track)
    return { getAudioTracks: () => [track], getTracks: () => [track] }
  })
  Object.defineProperty(globalThis, 'navigator', {
    value: { mediaDevices: { getUserMedia } }, configurable: true,
  })
  Object.defineProperty(globalThis, 'window', { value: { isSecureContext: true }, configurable: true })
  Object.assign(globalThis, { MediaRecorder: FakeMediaRecorder, OfflineAudioContext: class {} })
  FakeMediaRecorder.instances = []

  const texts: string[] = []
  const errors: VoiceInputError[] = []
  const sent: Array<{ locale?: string }> = []
  const voice = useVoiceInput({
    target: () => ({ agentId: 'agent-1', locale: 'zh-CN' }),
    transcribe: async (_wav, target) => {
      sent.push(target)
      return ' 你好 '
    },
    toWav: async blob => ({
      wav: blob,
      speech: opts.speech ?? true,
      stats: { speech: opts.speech ?? true, noiseFloor: 0, threshold: 0, peak: 0, voicedMs: 0 },
    }),
    onText: text => texts.push(text),
    onError: error => errors.push(error),
  })
  return { voice, texts, errors, sent, tracks, opens: () => opens }
}

test('release keeps recording for the tail, then transcribes with the locale', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval', 'Date'] })
  const { voice, texts, sent } = setup()

  await voice.start()
  assert.equal(voice.state.value, 'recording')
  t.mock.timers.tick(1000)

  voice.stop()
  assert.equal(voice.state.value, 'recording', 'still capturing the trailing syllable')
  assert.equal(FakeMediaRecorder.instances[0].state, 'recording')
  t.mock.timers.tick(VOICE_TAIL_MS)
  await flush()

  assert.deepEqual(texts, ['你好'])
  assert.equal(sent[0].locale, 'zh-CN')
  assert.equal(voice.state.value, 'idle')
})

test('the microphone stays open between presses and closes after the warm window', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval', 'Date'] })
  const { voice, tracks, opens } = setup()

  for (let i = 0; i < 2; i++) {
    await voice.start()
    t.mock.timers.tick(800)
    voice.stop()
    t.mock.timers.tick(VOICE_TAIL_MS)
    await flush()
  }
  assert.equal(opens(), 1, 'second press reuses the warm stream')
  assert.equal(tracks[0].readyState, 'live')

  t.mock.timers.tick(VOICE_WARM_MS)
  assert.equal(tracks[0].readyState, 'ended')
})

test('releasing while the microphone is opening tells the user and sends nothing', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval', 'Date'] })
  let resolveStream: (value: unknown) => void = () => {}
  const { voice, errors, sent, tracks } = setup({
    getUserMedia: () => new Promise(resolve => { resolveStream = resolve }),
  })

  const starting = voice.start()
  assert.equal(voice.state.value, 'preparing')
  voice.stop()
  const track = new FakeTrack()
  tracks.push(track)
  resolveStream({ getAudioTracks: () => [track], getTracks: () => [track] })
  await starting

  assert.deepEqual(errors, ['releasedEarly'])
  assert.equal(sent.length, 0)
  assert.equal(FakeMediaRecorder.instances.length, 0)
  assert.equal(voice.state.value, 'idle')
})

test('a silent clip is not sent, so the model cannot hallucinate text', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval', 'Date'] })
  const { voice, sent, texts, errors } = setup({ speech: false })

  await voice.start()
  t.mock.timers.tick(2000)
  voice.stop()
  t.mock.timers.tick(VOICE_TAIL_MS)
  await flush()

  assert.equal(sent.length, 0)
  assert.deepEqual(texts, [])
  assert.deepEqual(errors, ['empty'])
  assert.equal(voice.state.value, 'idle')
})

test('a clip shorter than the minimum is dropped', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval', 'Date'] })
  const { voice, sent, texts } = setup()

  await voice.start()
  voice.cancel()
  await flush()
  assert.equal(sent.length, 0)
  assert.deepEqual(texts, [])
  assert.equal(voice.state.value, 'idle')
})

test('getUserMedia errors map to permission and device', async () => {
  for (const [name, want] of [['NotAllowedError', 'permission'], ['NotFoundError', 'device']] as const) {
    const { voice, errors } = setup({
      getUserMedia: async () => { throw Object.assign(new Error(name), { name }) },
    })
    await voice.start()
    assert.deepEqual(errors, [want])
    assert.equal(voice.state.value, 'idle')
  }
})

test.after(() => mock.timers.reset())
