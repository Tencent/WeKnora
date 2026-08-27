import assert from 'node:assert/strict'
import test from 'node:test'

import {
  ACTIVE_QUESTION_TOP_OFFSET_PX,
  QUESTION_MINIMAP_TRACK_MAX_PX,
  QUESTION_TICK_GAP_PX,
  QUESTION_TICK_MOUNTAIN_GAIN,
  activeQuestionId,
  answerPreviewText,
  collectUserQuestions,
  isChatOverflowing,
  mapQuestionTicks,
  nearestTickId,
  offsetFromScrollContent,
  questionDisplayText,
  questionMinimapTrackHeight,
  shouldShowQuestionMinimap,
  tickMountainScale,
  viewportBand,
} from './chatQuestionMinimap.ts'

test('isChatOverflowing is false when content fits or matches the viewport', () => {
  assert.equal(isChatOverflowing(800, 800), false)
  assert.equal(isChatOverflowing(799, 800), false)
  assert.equal(isChatOverflowing(801, 800), true)
})

test('questionMinimapTrackHeight packs ticks with a small gap instead of filling the rail', () => {
  assert.equal(questionMinimapTrackHeight(1, 800), QUESTION_TICK_GAP_PX)
  assert.equal(questionMinimapTrackHeight(3, 800), 2 * QUESTION_TICK_GAP_PX)
  assert.equal(questionMinimapTrackHeight(0, 800), 0)
  assert.equal(questionMinimapTrackHeight(3, 0), 0)
})

test('questionMinimapTrackHeight still caps a long cluster to the centered rail', () => {
  assert.equal(questionMinimapTrackHeight(100, 800), QUESTION_MINIMAP_TRACK_MAX_PX)
  assert.equal(questionMinimapTrackHeight(100, 400), 200)
})

test('shouldShowQuestionMinimap requires overflow and at least one question', () => {
  assert.equal(shouldShowQuestionMinimap(true, 1), true)
  assert.equal(shouldShowQuestionMinimap(true, 0), false)
  assert.equal(shouldShowQuestionMinimap(false, 4), false)
})

test('collectUserQuestions keeps loaded user turns with ids, in list order', () => {
  const questions = collectUserQuestions([
    { role: 'assistant', id: 'a1', content: 'hi' },
    { role: 'user', content: 'no id yet' },
    { role: 'user', id: 'u1', content: 'first', images: [{}] },
    { role: 'assistant', id: 'a2', content: 'answer one' },
    { role: 'user', id: 'u2', content: '  ', attachments: [{}] },
  ])
  assert.deepEqual(
    questions.map((q) => ({ id: q.id, hasAttachments: q.hasAttachments, answerContent: q.answerContent })),
    [
      { id: 'u1', hasAttachments: true, answerContent: 'answer one' },
      { id: 'u2', hasAttachments: true, answerContent: '' },
    ],
  )
})

test('answerPreviewText strips markdown and uses the pending placeholder when empty', () => {
  assert.equal(
    answerPreviewText('## Hello\n**world** and [link](https://x)', '生成中'),
    'Hello world and link',
  )
  assert.equal(answerPreviewText('   ', '生成中'), '生成中')
  assert.equal(answerPreviewText(undefined, '生成中'), '生成中')
})

test('tickMountainScale is 1 without a pointer and peaks at the hovered tick', () => {
  assert.equal(tickMountainScale(100, null), 1)
  assert.equal(tickMountainScale(100, 100), 1 + QUESTION_TICK_MOUNTAIN_GAIN)
  assert.ok(tickMountainScale(100, 100) > tickMountainScale(135, 100))
  assert.ok(tickMountainScale(135, 100) > tickMountainScale(200, 100))
})

test('nearestTickId picks the closest tick to the pointer', () => {
  const ticks = [
    { id: 'u1', yPx: 10 },
    { id: 'u2', yPx: 45 },
    { id: 'u3', yPx: 80 },
  ]
  assert.equal(nearestTickId(ticks, 12), 'u1')
  assert.equal(nearestTickId(ticks, 50), 'u2')
  assert.equal(nearestTickId([], 10), null)
})

test('questionDisplayText uses trimmed content, else the placeholder', () => {
  assert.equal(questionDisplayText('  hello\nworld  ', '(附件)'), 'hello world')
  assert.equal(questionDisplayText('   ', '(附件)'), '(附件)')
  assert.equal(questionDisplayText(undefined, '(附件)'), '(附件)')
})

test('offsetFromScrollContent is viewport-relative plus scrollTop', () => {
  assert.equal(offsetFromScrollContent(120, 40, 80), 160)
})

test('mapQuestionTicks clusters ticks in the center with a compact equal gap', () => {
  const ticks = mapQuestionTicks(
    [
      { id: 'u1' },
      { id: 'u2' },
      { id: 'u3' },
    ],
    200,
  )
  assert.equal(ticks[1].yPx - ticks[0].yPx, QUESTION_TICK_GAP_PX)
  assert.equal(ticks[2].yPx - ticks[1].yPx, QUESTION_TICK_GAP_PX)
  assert.equal((ticks[0].yPx + ticks[2].yPx) / 2, 100)
})

test('mapQuestionTicks ignores message offsets and keeps equal gaps', () => {
  const ticks = mapQuestionTicks(
    [
      { id: 'u1', offsetTop: 0 },
      { id: 'u2', offsetTop: 2 },
    ],
    200,
  )
  assert.equal(ticks[1].yPx - ticks[0].yPx, QUESTION_TICK_GAP_PX)
  assert.equal((ticks[0].yPx + ticks[1].yPx) / 2, 100)
})

test('mapQuestionTicks still fits many ticks inside a short rail', () => {
  const ticks = mapQuestionTicks(
    [
      { id: 'a' },
      { id: 'b' },
      { id: 'c' },
      { id: 'd' },
    ],
    20,
  )
  assert.equal(ticks.length, 4)
  assert.ok(ticks[0].yPx >= 0)
  assert.ok(ticks[3].yPx <= 20)
  const gap = ticks[1].yPx - ticks[0].yPx
  for (let i = 1; i < ticks.length; i++) {
    assert.ok(Math.abs(ticks[i].yPx - ticks[i - 1].yPx - gap) < 1e-9)
  }
})

test('viewportBand maps the visible window onto the track with a 16px floor', () => {
  const band = viewportBand(250, 250, 1000, 200)
  assert.equal(band.topPx, 50)
  assert.equal(band.heightPx, 50)

  const tiny = viewportBand(0, 10, 1000, 200)
  assert.equal(tiny.topPx, 0)
  assert.equal(tiny.heightPx, 16)
})

test('activeQuestionId picks the last question at or above the top offset', () => {
  const items = [
    { id: 'u1', offsetTop: 0 },
    { id: 'u2', offsetTop: 400 },
    { id: 'u3', offsetTop: 800 },
  ]
  assert.equal(activeQuestionId(items, 0), 'u1')
  assert.equal(activeQuestionId(items, 400 - ACTIVE_QUESTION_TOP_OFFSET_PX), 'u2')
  assert.equal(activeQuestionId(items, 900), 'u3')
  assert.equal(activeQuestionId([], 0), null)
})
