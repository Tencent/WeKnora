import assert from 'node:assert/strict'
import test from 'node:test'
import { browserToolTitle, browserToolSummary } from './browserToolDisplay'
const t = (key: string) => key

test('browser steps expose actions and host without URL credentials/query', () => {
 const title=browserToolTitle(t,{arguments:{method:'navigate',params:{url:'https://user:secret@example.com/cart?token=private'}},pending:true})
 assert.equal(title,'localBrowser.openPage · example.com…')
 assert.equal(browserToolTitle(t,{arguments:{method:'wait_ms'},success:false}),'localBrowser.waitPage · localBrowser.actionFailed')
})
test('browser failures provide recovery information instead of raw protocol messages',()=>{
 assert.equal(browserToolSummary(t,{success:false,output:'timeout: session already has an unfinished command'}),'localBrowser.commandBusy')
 assert.equal(browserToolSummary(t,{success:false,output:'invalid_params: missing field duration_ms'}),'localBrowser.invalidArguments')
 assert.equal(browserToolSummary(t,{success:false,output:'browser command interrupted or timed out'}),'localBrowser.commandInterrupted')
})
