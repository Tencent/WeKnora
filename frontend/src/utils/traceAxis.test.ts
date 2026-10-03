import assert from 'node:assert/strict'
import test from 'node:test'

import {
  axisGridStepPct,
  buildAxisTicks,
  computeTraceAxis,
  formatAxisLabel,
  niceAxisStep,
} from './traceAxis.ts'

test('rounds rough intervals onto the 1 / 2 / 2.5 / 5 ladder', () => {
  assert.equal(niceAxisStep(98.8), 100)
  assert.equal(niceAxisStep(1), 1)
  assert.equal(niceAxisStep(1.4), 2)
  assert.equal(niceAxisStep(2.4), 2.5)
  assert.equal(niceAxisStep(4), 5)
  assert.equal(niceAxisStep(7), 10)
  assert.equal(niceAxisStep(6000), 10000)
})

test('degenerate durations produce an empty axis rather than NaN', () => {
  for (const bad of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.deepEqual(computeTraceAxis(bad), { stepMs: 0, maxMs: 0 })
    assert.deepEqual(buildAxisTicks(computeTraceAxis(bad)), [])
  }
  assert.equal(axisGridStepPct({ stepMs: 0, maxMs: 0 }), '25%')
})

test('the axis maximum is a whole number of steps and covers the total', () => {
  for (const total of [494, 1, 51, 4200, 42300, 300000, 987654]) {
    const { stepMs, maxMs } = computeTraceAxis(total)
    assert.ok(maxMs >= total, `${total}: axis ${maxMs} clips the trace`)
    assert.ok(
      Math.abs(maxMs / stepMs - Math.round(maxMs / stepMs)) < 1e-9,
      `${total}: axis ${maxMs} is not a whole number of ${stepMs} steps`,
    )
  }
})

test('the 494ms failure case rules to round hundreds', () => {
  const axis = computeTraceAxis(494)
  assert.deepEqual(axis, { stepMs: 100, maxMs: 500 })
  assert.deepEqual(
    buildAxisTicks(axis).map((t) => t.label),
    ['0ms', '100ms', '200ms', '300ms', '400ms', '500ms'],
  )
  assert.deepEqual(
    buildAxisTicks(axis).map((t) => t.left),
    ['0%', '20%', '40%', '60%', '80%', '100%'],
  )
  assert.equal(axisGridStepPct(axis), '20%')
})

test('one unit for the whole ruler, picked from the axis maximum', () => {
  assert.equal(formatAxisLabel(0, 500), '0ms')
  assert.equal(formatAxisLabel(250, 500), '250ms')
  // Seconds once the axis reaches 1s — including the zero tick, which
  // would otherwise read "0ms" next to "10s".
  assert.equal(formatAxisLabel(0, 50000), '0s')
  assert.equal(formatAxisLabel(10000, 50000), '10s')
  assert.equal(formatAxisLabel(2500, 12500), '2.5s')
  // Minutes past a minute, with a carry so nothing reads "1m60s".
  assert.equal(formatAxisLabel(0, 300000), '0m00s')
  assert.equal(formatAxisLabel(75000, 300000), '1m15s')
  assert.equal(formatAxisLabel(119999, 300000), '2m00s')
})

test('tick count stays bounded and labelled in one unit', () => {
  for (const total of [494, 42300, 300000, 7_200_000]) {
    const axis = computeTraceAxis(total)
    const ticks = buildAxisTicks(axis)
    assert.ok(ticks.length >= 2 && ticks.length <= 12, `${total}: ${ticks.length} ticks`)
    const units = new Set(ticks.map((t) => t.label.replace(/[\d.]/g, '')))
    assert.equal(units.size, 1, `${total}: mixed units ${[...units].join(' / ')}`)
  }
})
