import assert from 'node:assert/strict'
import test from 'node:test'
import { operationDownload } from './operationDownload.ts'

test('downloads only fixed authenticated resource routes, never URLs in tool output', () => {
  const descriptor = operationDownload('faq.export', {knowledge_base_id:'kb-1',format:'json',url:'https://evil'})
  assert.deepEqual(descriptor,{path:'/api/v1/knowledge-bases/kb-1/faq/entries/export?format=json',fileName:'faq_export.json'})
  assert.equal(operationDownload('faq.export',{knowledge_base_id:'../users',format:'json'}),undefined)
  assert.equal(operationDownload('faq.export',{knowledge_base_id:'kb',format:'json&admin=true'}),undefined)
  assert.equal(operationDownload('external_tool',{url:'/api/v1/settings'}),undefined)
  const attachment=operationDownload('attachment.download',{session_id:'s',attachment_id:'a',file_name:'../../file.txt'})
  assert.deepEqual(attachment,{path:'/api/v1/sessions/s/attachments/a/preview',fileName:'file.txt'})
  assert.equal(operationDownload('attachment.download',{session_id:'foreign/path',attachment_id:'a'}),undefined)
})
