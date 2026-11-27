import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'

class FakeElement {
  constructor(tagName) {
    this.tagName = tagName
    this.attributes = new Map()
    this.children = []
    this.listeners = new Map()
    this.parentNode = null
    this.style = {}
    this.textContent = ''
    this.type = ''
    this._src = ''
    this.srcAssignments = 0
    this.contentWindow = { postMessage() {} }
  }

  get src() { return this._src }
  set src(value) {
    this._src = value
    this.srcAssignments++
  }

  setAttribute(name, value) { this.attributes.set(name, value) }
  getAttribute(name) { return this.attributes.get(name) ?? null }
  appendChild(child) {
    child.parentNode = this
    this.children.push(child)
    return child
  }
  removeChild(child) {
    this.children = this.children.filter((entry) => entry !== child)
    child.parentNode = null
  }
  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) ?? []
    listeners.push(listener)
    this.listeners.set(type, listeners)
  }
  dispatch(type) {
    for (const listener of this.listeners.get(type) ?? []) listener()
  }
}

test('embed iframe and token endpoint stay idle until the widget opens', async () => {
  const source = readFileSync(new URL('./public/weknora-widget.js', import.meta.url), 'utf8')
  const body = new FakeElement('body')
  const document = {
    body,
    currentScript: null,
    createElement: (tagName) => new FakeElement(tagName),
  }
  let fetchCalls = 0
  const window = {
    location: { origin: 'https://host.example', href: 'https://host.example/page' },
    addEventListener() {},
    removeEventListener() {},
  }
  const context = vm.createContext({
    window,
    document,
    URL,
    console,
    setTimeout,
    clearTimeout,
    fetch: async () => {
      fetchCalls++
      return { ok: true, json: async () => ({ token: 'ems_test', expiresIn: 1800 }) }
    },
  })
  vm.runInContext(source, context)

  const widget = window.WeKnora.init({
    channel: 'channel-1',
    tokenEndpoint: 'https://host.example/embed-token',
    baseUrl: 'https://weknora.example',
  })
  const panel = body.children[1]
  const iframe = panel.children[0]

  assert.equal(iframe.src, '')
  iframe.dispatch('load')
  await Promise.resolve()
  assert.equal(fetchCalls, 0)

  widget.open()
  assert.equal(iframe.src, 'https://weknora.example/embed/channel-1')
  assert.equal(iframe.srcAssignments, 1)
  iframe.dispatch('load')
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.equal(fetchCalls, 1)
  widget.close()
  widget.open()
  assert.equal(iframe.srcAssignments, 1)
  widget.destroy()
})
