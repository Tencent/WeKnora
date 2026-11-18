import assert from 'node:assert/strict'
import test from 'node:test'
import { folderDropDestination } from './folderDrop'

const paths = ['海底电缆', '海底电缆/10kV', '海底电缆/35kV', '海底电缆/35kV/资料', '档案']
test('reparent preserves the name; dropping on the root moves one folder to the top level', () => {
  assert.equal(folderDropDestination('海底电缆/35kV', '档案', paths), '档案/35kV')
  assert.equal(folderDropDestination('海底电缆/35kV', '', paths), '35kV')
})
test('reject root, self, descendants, current parent and stale/foreign paths', () => {
  for (const [from, parent] of [['', '档案'], ['海底电缆', '海底电缆'], ['海底电缆', '海底电缆/35kV'], ['海底电缆/35kV', '海底电缆'], ['gone', '档案'], ['海底电缆', 'foreign']]) {
    assert.equal(folderDropDestination(from, parent, paths), null, `${from} -> ${parent}`)
  }
})
test('do not silently merge a same-name destination or truncate a deep subtree', () => {
  assert.equal(folderDropDestination('海底电缆/35kV', '档案', [...paths, '档案/35kV']), null)
  const parent = Array.from({length:15}, (_,i)=>`p${i}`).join('/')
  assert.equal(folderDropDestination('海底电缆/35kV', parent, [...paths, parent]), null)
  const long = Array.from({length:9}, ()=> '中'.repeat(40)).join('/')
  assert.equal(folderDropDestination('海底电缆/35kV', long, [...paths, long]), null)
})
