import assert from 'node:assert/strict'
import test from 'node:test'
import { browserToolTitle, browserToolSummary, browserToolContent } from './browserToolDisplay'
const t = (key: string) => key

test('browser steps expose actions and host without URL credentials/query', () => {
 const title=browserToolTitle(t,{arguments:{method:'navigate',url:'https://user:secret@example.com/cart?token=private'},pending:true})
 assert.equal(title,'localBrowser.local · localBrowser.openPage · example.com…')
 assert.equal(browserToolTitle(t,{arguments:{method:'wait_ms'},success:false}),'localBrowser.local · localBrowser.waitPage · localBrowser.actionFailed')
})
test('browser failures provide recovery information instead of raw protocol messages',()=>{
 assert.equal(browserToolSummary(t,{success:false,output:'timeout: session already has an unfinished command'}),'localBrowser.commandBusy')
 assert.equal(browserToolSummary(t,{success:false,output:'invalid_params: missing field duration_ms'}),'localBrowser.invalidArguments')
 assert.equal(browserToolSummary(t,{success:false,output:'browser command interrupted or timed out'}),'localBrowser.commandInterrupted')
})

test('all displayed steps identify the local browser and distinguish keys from clicks', () => {
 assert.equal(browserToolTitle(t, {arguments: JSON.stringify({method:'press',key:'Enter'})}), 'localBrowser.local · localBrowser.pressKey · Enter')
 assert.equal(browserToolTitle(t, {arguments: {method:'click'}}), 'localBrowser.local · localBrowser.clickPage')
 assert.equal(browserToolTitle(t, {arguments: {method:'wheel'}}), 'localBrowser.local · localBrowser.scrollPage')
 assert.equal(browserToolSummary(t, {pending:true}), 'localBrowser.actionPending')
 assert.equal(browserToolSummary(t, {}), 'localBrowser.actionRecorded')
})
test('browser results render page content and safe destinations without protocol envelopes', () => {
 const result = browserToolContent({output:JSON.stringify({text:'Heading\nButton: Search',url:'https://user:password@example.com/page?token=secret#fragment',tab_id:42,ref_count:10})})
 assert.equal(result.text, 'Heading\nButton: Search')
 assert.equal(result.address, 'https://example.com/page')
 assert.equal(JSON.stringify(result).includes('secret'), false)
 assert.equal(JSON.stringify(result).includes('tab_id'), false)
 assert.equal(browserToolContent({output:'not JSON'}).text, '')
 assert.equal(browserToolContent({output:{html:'<script>alert(1)</script>'}}).text, '<script>alert(1)</script>')
 const bounded = browserToolContent({output:{text:'a'.repeat(13000)}})
 assert.equal(bounded.text.length, 12000)
 assert.equal(bounded.truncated, true)
})
test('tab lists and screenshots have dedicated content instead of object stringification', () => {
 const result = browserToolContent({output:{tabs:[{title:'News',url:'https://example.com/news?secret=x'},null]}})
 assert.deepEqual(result.tabs,[{title:'News',address:'https://example.com/news'},{title:'',address:''}])
 assert.equal(browserToolContent({output:{image_base64:'aGVsbG8=',format:'png'}}).image, 'data:image/png;base64,aGVsbG8=')
 assert.equal(browserToolContent({output:{image_base64:'PHN2Zz4=',format:'svg+xml'}}).image, '')
 assert.equal(browserToolContent({output:{image_base64:'https://example.com/track',format:'jpeg'}}).image, '')
})

test('browser argument validation errors distinguish calls never sent to Chrome', () => {
 const error = "Parameter validation failed: navigate requires url at the top level"
 for (const event of [{success:false,error}, {success:false,output:error}, {success:false,error:{message:error}}]) {
  assert.equal(browserToolSummary(t,event),'localBrowser.invalidArguments')
 }
 assert.equal(browserToolSummary(t,{success:false,error:"Parameter validation failed: required parameter 'method' is missing"}),'localBrowser.invalidArguments')
 assert.equal(browserToolSummary(t,{success:true,output:error}),'localBrowser.actionCompleted')
})

test('flat arguments render destinations and help prompts', () => {
 assert.equal(browserToolContent({arguments:{method:'navigate',url:'https://user:secret@example.com/page?token=private'}}).address,'https://example.com/page')
 assert.equal(browserToolContent({arguments:{method:'request_help',prompt:'Please sign in'}}).prompt,'Please sign in')
})
