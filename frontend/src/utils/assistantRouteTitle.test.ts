import assert from 'node:assert/strict'
import test from 'node:test'
import { routeTitle } from './assistantRouteTitle'

const t = (key: string) => `<${key}>`

test('routeTitle names the pages the sidebar names', () => {
  assert.equal(routeTitle({ name: 'agentList' }, t), '<menu.agents>')
  assert.equal(routeTitle({ name: 'knowledgeBaseList' }, t), '<menu.knowledgeBase>')
  assert.equal(routeTitle({ name: 'organizationList' }, t), '<menu.organizations>')
  assert.equal(routeTitle({ name: 'chat', params: { chatid: 'x' } }, t), '<assistant.context.pages.chat>')
})

test('routeTitle adds the toolbox section and skips unnamed routes', () => {
  assert.equal(routeTitle({ name: 'toolbox', params: { section: 'skills' } }, t),
    '<toolbox.title> / <settings.skills.title>')
  assert.equal(routeTitle({ name: 'toolbox', params: { section: 'nope' } }, t), '<toolbox.title>')
  assert.equal(routeTitle({ name: 'settings' }, t), '', 'settings names itself as a dialog')
  assert.equal(routeTitle({ name: undefined }, t), '')
  assert.equal(routeTitle({ name: Symbol('x') }, t), '')
})
