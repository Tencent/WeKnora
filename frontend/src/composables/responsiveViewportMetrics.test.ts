import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveResponsiveViewportGeometry } from './responsiveViewportMetrics'

test('normal mobile viewport follows VisualViewport and detects keyboard inset', () => {
  const normal = resolveResponsiveViewportGeometry({
    innerWidth: 390,
    innerHeight: 844,
    visualWidth: 390,
    visualHeight: 844,
    visualScale: 1,
  })

  assert.deepEqual(normal, {
    layoutWidth: 390,
    layoutHeight: 844,
    visualWidth: 390,
    visualHeight: 844,
    visualOffsetTop: 0,
    visualScale: 1,
    keyboardInset: 0,
    isPinchZoomed: false,
  })

  const keyboard = resolveResponsiveViewportGeometry({
    innerWidth: 390,
    innerHeight: 844,
    visualWidth: 390,
    visualHeight: 500,
    visualOffsetTop: 0,
    visualScale: 1,
  })

  assert.equal(keyboard.visualHeight, 500)
  assert.equal(keyboard.keyboardInset, 344)
  assert.equal(keyboard.isPinchZoomed, false)
})

test('pinch zoom does not shrink the application layout or imitate a keyboard', () => {
  const zoomed = resolveResponsiveViewportGeometry({
    innerWidth: 390,
    innerHeight: 844,
    visualWidth: 195,
    visualHeight: 422,
    visualOffsetTop: 80,
    visualScale: 2,
  })

  assert.equal(zoomed.isPinchZoomed, true)
  assert.equal(zoomed.visualScale, 2)
  assert.equal(zoomed.visualWidth, 390)
  assert.equal(zoomed.visualHeight, 844)
  assert.equal(zoomed.visualOffsetTop, 0)
  assert.equal(zoomed.keyboardInset, 0)
})

test('small VisualViewport scale noise does not trigger pinch mode', () => {
  const noisy = resolveResponsiveViewportGeometry({
    innerWidth: 390,
    innerHeight: 844,
    visualWidth: 388,
    visualHeight: 840,
    visualScale: 1.01,
  })

  assert.equal(noisy.isPinchZoomed, false)
  assert.equal(noisy.visualWidth, 388)
  assert.equal(noisy.visualHeight, 840)
})

test('CSS root zoom and browser pinch zoom remain separate coordinate systems', () => {
  const rootZoomOnly = resolveResponsiveViewportGeometry({
    innerWidth: 500,
    innerHeight: 1000,
    rootZoom: 1.25,
    visualWidth: 500,
    visualHeight: 750,
    visualScale: 1,
  })

  assert.equal(rootZoomOnly.layoutWidth, 400)
  assert.equal(rootZoomOnly.layoutHeight, 800)
  assert.equal(rootZoomOnly.visualHeight, 600)
  assert.equal(rootZoomOnly.keyboardInset, 200)
  assert.equal(rootZoomOnly.isPinchZoomed, false)

  const combinedZoom = resolveResponsiveViewportGeometry({
    innerWidth: 500,
    innerHeight: 1000,
    rootZoom: 1.25,
    visualWidth: 250,
    visualHeight: 500,
    visualOffsetTop: 50,
    visualScale: 2,
  })

  assert.equal(combinedZoom.layoutWidth, 400)
  assert.equal(combinedZoom.layoutHeight, 800)
  assert.equal(combinedZoom.visualWidth, 400)
  assert.equal(combinedZoom.visualHeight, 800)
  assert.equal(combinedZoom.visualOffsetTop, 0)
  assert.equal(combinedZoom.keyboardInset, 0)
  assert.equal(combinedZoom.isPinchZoomed, true)
})