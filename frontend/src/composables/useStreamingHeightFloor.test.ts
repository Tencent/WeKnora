import assert from 'node:assert/strict'
import { test } from 'node:test'
import { nextHeightFloor } from './useStreamingHeightFloor'

test('streaming height floor grows at once and absorbs shrinks up to the cap', () => {
  let floor = 0
  floor = nextHeightFloor(floor, 400, 200)
  assert.equal(floor, 400)
  floor = nextHeightFloor(floor, 300, 200)
  assert.equal(floor, 400, 'a folded step leaves slack instead of shrinking')
  floor = nextHeightFloor(floor, 350, 200)
  assert.equal(floor, 400, 'the next step fills the slack before the reply grows')
  floor = nextHeightFloor(floor, 450, 200)
  assert.equal(floor, 450)
  floor = nextHeightFloor(floor, 100, 200)
  assert.equal(floor, 300, 'slack never exceeds the cap, keeping new content in view')
})
