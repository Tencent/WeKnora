import assert from 'node:assert/strict'
import test from 'node:test'
import { browserToolTitle, browserToolSummary, browserToolContent, browserToolIncomplete, browserActionLabel } from './browserToolDisplay'
const t = (key: string) => key

test('browser steps expose actions and host without URL credentials/query', () => {
 const title=browserToolTitle(t,{arguments:{method:'navigate',url:'https://user:secret@example.com/cart?token=private'},pending:true})
 assert.equal(title,'localBrowser.local · localBrowser.openPage · example.com…')
 assert.equal(browserToolTitle(t,{arguments:{method:'wait_ms'},success:false}),'localBrowser.local · localBrowser.waitPage · localBrowser.actionFailed')
})

test('navigation timeout is incomplete even in historical success records', () => {
 const event = { arguments:{method:'navigate'}, success:true, output:{reached:'timeout',error_text:'Timed out waiting for load'} }
 assert.equal(browserToolIncomplete(event), true)
 assert.equal(browserToolSummary(t,event), 'localBrowser.navigationIncomplete')
 assert.match(browserToolTitle(t,event), /actionFailed/)
 assert.equal(browserToolContent(event).error, 'Timed out waiting for load')
 assert.equal(browserToolIncomplete({...event,arguments:{method:'evaluate'}}), false)
})

test('diagnostic and evaluation results remain visible and bounded', () => {
 const logs = browserToolContent({arguments:{method:'console'},success:true,output:{entries:[{
  level:'error',text:'API failed <script>',url:'https://user:secret@example.com/app?token=private',stack_trace:[{function_name:'load',url:'https://example.com/app?secret=1',line:7}],
 }]}})
 assert.match(logs.text,/\[error\] API failed <script>/)
 assert.match(logs.text,/load https:\/\/example.com\/app:7/)
 assert.doesNotMatch(logs.text,/secret|private/)
 const network = browserToolContent({arguments:{method:'network'},output:{entries:[{method:'GET',status:500,url:'https://example.com/api?token=private',error_text:'Request failed'}]}})
 assert.equal(network.text,'GET 500 https://example.com/api Request failed')
 for (const value of [false,0,null,'',{count:123}]) {
  assert.equal(browserToolContent({arguments:{method:'evaluate'},output:{ok:true,value}}).text,JSON.stringify(value,null,2))
 }
 assert.equal(browserToolContent({arguments:{method:'console'},success:true,output:{entries:[]}}).empty,true)
 const large = browserToolContent({arguments:{method:'console'},output:{entries:[{text:'x'.repeat(20000)}]}})
 assert.equal(large.text.length,12000)
 assert.equal(large.truncated,true)
})

test('screenshot image comes from structured data while text contains metadata only', () => {
 const content = browserToolContent({arguments:{method:'screenshot'},output:'{"width":1,"height":1,"format":"png"}',tool_data:{image_base64:'aGVsbG8=',format:'png'}})
 assert.equal(content.image,'data:image/png;base64,aGVsbG8=')
 assert.equal(content.text,'')
 assert.equal(browserActionLabel(t,'screenshot'),'localBrowser.captureScreenshot')
 const error = browserToolContent({success:false,output:{error:{message:'Missing ref'},recovery_hint:'Observe again'}})
 assert.equal(error.error,'Missing ref')
 assert.equal(error.recoveryHint,'Observe again')
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
