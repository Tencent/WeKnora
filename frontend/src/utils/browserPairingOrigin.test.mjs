import assert from 'node:assert/strict'
import test from 'node:test'

import { pairingPageOrigin, preferredDesktopAPIBase } from './browserPairingOrigin.ts'

test('pairingPageOrigin keeps an http or https page', () => {
  assert.equal(pairingPageOrigin('https://weknora.example'), 'https://weknora.example')
  assert.equal(pairingPageOrigin('http://127.0.0.1:8080'), 'http://127.0.0.1:8080')
})

test('pairingPageOrigin uses the desktop API host when the page is the Wails shell', () => {
  assert.equal(
    pairingPageOrigin('wails://wails.localhost', 'http://127.0.0.1:53124/api/v1'),
    'http://127.0.0.1:53124',
  )
})

test('pairingPageOrigin leaves a non-http page alone when no API base is known', () => {
  assert.equal(pairingPageOrigin('wails://wails.localhost', ''), 'wails://wails.localhost')
})

test('pairingPageOrigin ignores a non-loopback API base for the Wails shell', () => {
  assert.equal(
    pairingPageOrigin('wails://wails.localhost', 'https://evil.example/api/v1'),
    'wails://wails.localhost',
  )
})

test('preferredDesktopAPIBase uses the native binding before the page global', () => {
  assert.equal(
    preferredDesktopAPIBase('http://127.0.0.1:53124/api/v1', 'https://evil.example/api/v1'),
    'http://127.0.0.1:53124/api/v1',
  )
  assert.equal(preferredDesktopAPIBase('', 'http://127.0.0.1:53124/api/v1'), 'http://127.0.0.1:53124/api/v1')
})
