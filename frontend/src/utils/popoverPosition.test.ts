import assert from 'node:assert/strict'
import test from 'node:test'

import {
  clampPopoverLeft,
  clampPopoverWidth,
  computeAnchoredPopoverLayout,
} from './popoverPosition'

const anchor = {
  top: 100,
  right: 220,
  bottom: 130,
  left: 100,
  width: 120,
  height: 30,
}

test('popover width and horizontal position stay inside viewport edges', () => {
  assert.equal(clampPopoverWidth(360, 320), 304)
  assert.equal(clampPopoverLeft(300, 220, 320), 92)
  assert.equal(clampPopoverLeft(-20, 220, 320), 8)
})

test('tiny and invalid viewports produce finite non-negative geometry', () => {
  assert.equal(clampPopoverWidth(100, 10), 0)
  assert.equal(clampPopoverLeft(Number.NaN, 100, 10), 5)

  const layout = computeAnchoredPopoverLayout(anchor, {
    width: Number.NaN,
    height: -1,
  }, {
    preferredWidth: Number.POSITIVE_INFINITY,
    preferredHeight: Number.NaN,
  })

  assert.equal(layout.width, 0)
  assert.equal(layout.maxHeight, 0)
  assert.ok(Object.values(layout.style).every(value => !value.includes('NaN')))
})

test('popover opens below when there is sufficient visible space', () => {
  const layout = computeAnchoredPopoverLayout(anchor, { width: 400, height: 800 }, {
    preferredWidth: 220,
    preferredHeight: 280,
    minSpaceBelow: 160,
    maxHeightRatio: 0.62,
    offsetY: 8,
  })

  assert.equal(layout.openBelow, true)
  assert.equal(layout.left, 100)
  assert.equal(layout.maxHeight, 280)
  assert.equal(layout.style.top, '138px')
  assert.equal(layout.style.bottom, 'auto')
})

test('popover opens above near the bottom and supports end alignment', () => {
  const bottomAnchor = { ...anchor, top: 700, bottom: 730, left: 250, right: 370 }
  const layout = computeAnchoredPopoverLayout(bottomAnchor, { width: 400, height: 800 }, {
    preferredWidth: 220,
    preferredHeight: 280,
    align: 'end',
    minSpaceBelow: 160,
    offsetY: 8,
  })

  assert.equal(layout.openBelow, false)
  assert.equal(layout.left, 150)
  assert.equal(layout.maxHeight, 280)
  assert.equal(layout.style.top, 'auto')
  assert.equal(layout.style.bottom, '108px')
})
