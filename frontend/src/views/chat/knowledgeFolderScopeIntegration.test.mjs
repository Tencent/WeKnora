import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  buildKnowledgeScopeProjection,
  cloneKnowledgeScopeRequest,
  folderSelectionsFromRequest,
  knowledgeBaseSelectionsFromRequest,
} from '../../utils/knowledgeScope.ts'

const chatView = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const inputField = readFileSync(
  new URL('../../components/Input-field.vue', import.meta.url),
  'utf8',
)
const streamClient = readFileSync(
  new URL('../../api/chat/streame.ts', import.meta.url),
  'utf8',
)
const settingsStore = readFileSync(
  new URL('../../stores/settings.ts', import.meta.url),
  'utf8',
)

function folder(knowledgeBaseId, folderId) {
  return {
    knowledgeBaseId,
    knowledgeBaseName: knowledgeBaseId,
    folderId,
    folderName: folderId,
    folderPath: [folderId],
    ancestorFolderIds: [],
    includeDescendants: true,
  }
}

function sourceBetween(source, startMarker, endMarker) {
  const start = source.indexOf(startMarker)
  const end = source.indexOf(endMarker, start)
  assert.notEqual(start, -1, `missing source marker: ${startMarker}`)
  assert.notEqual(end, -1, `missing source marker: ${endMarker}`)
  return source.slice(start, end)
}

test('aggregates folder payloads by KB and fixes include_descendants to true', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: ['file-a'],
    knowledgeSelections: [{ id: 'file-a', kbId: 'kb-a' }],
    tags: [{ id: 'tag-a', kbId: 'kb-a' }],
    folderSelections: [
      folder('kb-a', 'folder-b'),
      folder('kb-a', 'folder-a'),
      folder('kb-b', 'folder-a'),
    ],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-a', 'kb-b'],
    knowledge_scope: {
      knowledge_ids: ['file-a'],
      tag_scopes: [{
        knowledge_base_id: 'kb-a',
        tag_ids: ['tag-a'],
      }],
      folder_scopes: [
        {
          knowledge_base_id: 'kb-a',
          folder_ids: ['folder-a', 'folder-b'],
          include_descendants: true,
        },
        {
          knowledge_base_id: 'kb-b',
          folder_ids: ['folder-a'],
          include_descendants: true,
        },
      ],
    },
  })
})

test('omits canonical knowledge_scope when no folder selection exists', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: ['kb-a'],
    knowledgeIds: ['file-a'],
    tags: [{ id: 'tag-a', kbId: 'kb-a' }],
    folderSelections: [],
  })

  assert.deepEqual(projection, { knowledge_base_ids: ['kb-a'] })
  assert.equal(Object.hasOwn(projection, 'knowledge_scope'), false)
})

test('restores folder IDs from last_request_state canonical scope', () => {
  const request = {
    folder_scopes: [
      {
        knowledge_base_id: 'kb-a',
        folder_ids: ['folder-a', 'folder-b'],
        include_descendants: true,
      },
      {
        knowledge_base_id: 'kb-whole',
        folder_ids: [''],
        include_descendants: true,
      },
    ],
  }
  const restored = folderSelectionsFromRequest(request)

  assert.deepEqual(
    restored.map(item => [item.knowledgeBaseId, item.folderId]),
    [
      ['kb-a', 'folder-a'],
      ['kb-a', 'folder-b'],
    ],
  )
  assert.ok(restored.every(item => item.includeDescendants))
  assert.deepEqual(
    knowledgeBaseSelectionsFromRequest(request, ['kb-whole']),
    ['kb-whole'],
  )
})

test('wires the shared projection only into ordinary Quick Answer sends', () => {
  assert.match(
    chatView,
    /import\s*\{[\s\S]*buildKnowledgeScopeProjection,[\s\S]*cloneKnowledgeScopeRequest,[\s\S]*\}\s*from '@\/utils\/knowledgeScope'/,
  )
  assert.match(
    chatView,
    /const canProjectFolderScope = \([\s\S]*!props\.embeddedMode[\s\S]*!agentEnabled[\s\S]*useSettingsStoreInstance\.isQuickAnswerMode[\s\S]*!useSettingsStoreInstance\.selectedAgentSourceTenantId/,
  )
  assert.match(
    chatView,
    /folderSelections: useSettingsStoreInstance\.selectedFolderScopes/,
  )
  assert.match(
    chatView,
    /knowledgeSelections,\s*tags: tagSelections/,
  )
  assert.match(
    chatView,
    /knowledge_scope: canonicalScope/,
  )
})

test('passes both attribution IDs with the selected suggestion send action', () => {
  const selectStart = chatView.indexOf('const handleFollowUpSelect =')
  const selectEnd = chatView.indexOf('const dismissSuggestions =', selectStart)
  const selectHandler = chatView.slice(selectStart, selectEnd)

  assert.notEqual(selectStart, -1)
  assert.notEqual(selectEnd, -1)
  assert.match(
    selectHandler,
    /const suggestionAttribution = \{\s*suggestion_set_id: message\.suggestionSet\.id,\s*question_id: item\.id/,
  )
  const restoreStart = selectHandler.indexOf(
    'useSettingsStoreInstance.applyLastRequestState({',
  )
  const triggerStart = selectHandler.indexOf(
    'inputFieldRef.value.triggerSend(item.text, suggestionAttribution)',
  )
  assert.notEqual(restoreStart, -1)
  assert.ok(restoreStart < triggerStart)
  assert.match(
    selectHandler,
    /const restoredScope = item\?\.knowledge_scope[\s\S]*typeof restoredScope === 'object'[\s\S]*!Array\.isArray\(restoredScope\)/,
  )
  assert.match(
    selectHandler,
    /snapshotAsDefaultsIfNeeded\(\)[\s\S]*applyLastRequestState\(\{\s*knowledge_scope: restoredScope/,
  )
  assert.match(
    selectHandler,
    /triggerSend\(item\.text, suggestionAttribution\)/,
  )
  assert.match(
    selectHandler,
    /sendMsg\(item\.text, '', \[\], \[\], \[\], suggestionAttribution\)/,
  )
  assert.match(
    chatView,
    /@send-msg="\(\s*query,\s*modelId,\s*mentionedItems,\s*imageFiles,\s*attachmentFiles,\s*suggestionAttribution\s*\)\s*=>\s*sendMsg\(\s*query,\s*modelId,\s*mentionedItems,\s*imageFiles,\s*attachmentFiles,\s*suggestionAttribution\s*\)"/,
  )
})

test('threads optional attribution through InputField without component state', () => {
  const createSessionStart = inputField.indexOf('const createSession = async')
  const createSessionEnd = inputField.indexOf(
    'const updateAgentModeDropdownPosition',
    createSessionStart,
  )
  const createSession = inputField.slice(createSessionStart, createSessionEnd)
  const exposeStart = inputField.indexOf('defineExpose({')
  const exposeEnd = inputField.indexOf('</script>', exposeStart)
  const exposedSend = inputField.slice(exposeStart, exposeEnd)

  assert.notEqual(createSessionStart, -1)
  assert.notEqual(createSessionEnd, -1)
  assert.notEqual(exposeStart, -1)
  assert.notEqual(exposeEnd, -1)
  assert.match(
    inputField,
    /type SuggestionAttribution = \{\s*suggestion_set_id: string;\s*question_id: string;\s*\}/,
  )
  assert.match(
    inputField,
    /\(\s*e: 'send-msg',[\s\S]*?suggestionAttribution\?: SuggestionAttribution,\s*\): void/,
  )
  assert.match(
    createSession,
    /val: string,\s*suggestionAttribution\?: SuggestionAttribution/,
  )
  assert.match(
    createSession,
    /emit\(\s*'send-msg',\s*val,\s*selectedModelId\.value,\s*mentionedItems,\s*imageFiles,\s*attachmentFiles,\s*suggestionAttribution,\s*\)/,
  )
  assert.match(
    createSession,
    /emit\(\s*'send-msg',\s*val,\s*selectedModelId\.value \|\| '',\s*\[\],\s*\[\],\s*\[\],\s*suggestionAttribution,\s*\)/,
  )
  assert.match(
    exposedSend,
    /triggerSend\(\s*text: string,\s*suggestionAttribution\?: SuggestionAttribution,\s*\)/,
  )
  assert.match(
    exposedSend,
    /nextTick\(\(\) => createSession\(text, suggestionAttribution\)\)/,
  )
  assert.doesNotMatch(
    inputField,
    /\b(?:ref|shallowRef|reactive)\s*(?:<[^>]*SuggestionAttribution[^>]*>\s*\(|\([^)]*SuggestionAttribution)/,
  )
  const ownershipGuard = createSession.indexOf(
    'if (!await ensureFolderScopeFileOwnership())',
  )
  const outgoingEmit = createSession.slice(ownershipGuard).search(
    /emit\(\s*'send-msg',\s*val,\s*selectedModelId\.value,/,
  )
  assert.notEqual(ownershipGuard, -1)
  assert.notEqual(outgoingEmit, -1)
})

test('attributed requests omit current canonical scope and retain legacy selectors', () => {
  const sendStart = chatView.indexOf('const sendMsg = async')
  const streamStart = chatView.indexOf('await startStream({', sendStart)
  const streamEnd = chatView.indexOf('});', streamStart)
  const sendRequest = chatView.slice(sendStart, streamEnd)

  assert.notEqual(sendStart, -1)
  assert.notEqual(streamStart, -1)
  assert.notEqual(streamEnd, -1)
  assert.match(
    sendRequest,
    /attachmentFiles = \[\],\s*suggestionAttribution = undefined/,
  )
  assert.match(
    sendRequest,
    /let canonicalScope = knowledgeScopeProjection\.knowledge_scope[\s\S]*if \(suggestionAttribution\) \{\s*canonicalScope = undefined/,
  )
  assert.match(sendRequest, /knowledge_scope: canonicalScope/)
  assert.match(sendRequest, /knowledge_base_ids: kbIds/)
  assert.match(sendRequest, /knowledge_ids: knowledgeIds/)
  assert.match(sendRequest, /tag_ids: tagIds/)
  assert.match(sendRequest, /mentioned_items: mentionedItems/)
  assert.match(
    sendRequest,
    /suggestion_attribution: suggestionAttribution/,
  )
})

test('does not identify or retain suggestion attribution by question text', () => {
  const sources = `${chatView}\n${inputField}`

  assert.doesNotMatch(sources, /pendingSuggestion/)
  assert.doesNotMatch(
    sources,
    /(?:item\.text|query|text|value)\s*===\s*(?:item\.text|query|text|value)/,
  )
  assert.match(chatView, /question_id: item\.id/)
  assert.match(inputField, /@click="createSession\(query\)"/)
  assert.match(inputField, /createSession\(val\)/)
})

test('stream payload omits knowledge_scope when no projection is provided', () => {
  const postBodyStart = streamClient.indexOf('const postBody: any =')
  const scopeGuardStart = streamClient.indexOf(
    'if (params.knowledge_scope !== undefined)',
    postBodyStart,
  )
  const agentIdStart = streamClient.indexOf(
    '// Include agent_id if provided',
    scopeGuardStart,
  )
  const scopeProjection = streamClient.slice(scopeGuardStart, agentIdStart)

  assert.notEqual(postBodyStart, -1)
  assert.notEqual(scopeGuardStart, -1)
  assert.notEqual(agentIdStart, -1)
  assert.match(
    scopeProjection,
    /postBody\.knowledge_scope = params\.knowledge_scope/,
  )
  assert.doesNotMatch(
    streamClient.slice(postBodyStart, scopeGuardStart),
    /knowledge_scope/,
  )
})

test('session recovery delegates canonical folder restoration to the shared helper', () => {
  assert.match(settingsStore, /knowledge_scope\?: KnowledgeScopeRequest \| null/)
  assert.match(
    settingsStore,
    /knowledgeBaseSelectionsFromRequest\(\s*canonicalScope,\s*knowledgeBaseIdsFromMentions,\s*\)/,
  )
  assert.match(
    settingsStore,
    /folderSelectionsFromRequest\(canonicalScope\)/,
  )
  assert.match(
    settingsStore,
    /normalizeFolderScopeSelections\([\s\S]*folderSelectionsFromRequest\(canonicalScope\),[\s\S]*knowledgeBaseIds/,
  )
})

test('preserves restored canonical shapes and replays a fresh clone while clean', () => {
  const restoredRequests = [
    {
      knowledge_base_ids: ['kb-empty'],
      folder_scopes: [],
    },
    {
      folder_scopes: [{
        knowledge_base_id: 'kb-folder',
        folder_ids: ['folder-a'],
        include_descendants: true,
      }],
    },
    {
      knowledge_base_ids: ['kb-whole'],
      knowledge_ids: ['file-a'],
      tag_scopes: [{
        knowledge_base_id: 'kb-tag',
        tag_ids: ['tag-a'],
      }],
      folder_scopes: [{
        knowledge_base_id: 'kb-folder',
        folder_ids: ['folder-a'],
        include_descendants: true,
      }],
    },
    {
      knowledge_base_ids: ['kb-root'],
      folder_scopes: [{
        knowledge_base_id: 'kb-root',
        folder_ids: [''],
        include_descendants: true,
      }],
    },
  ]

  for (const request of restoredRequests) {
    const cloned = cloneKnowledgeScopeRequest(request)
    assert.deepEqual(cloned, request)
    assert.notEqual(cloned, request)
  }

  const sendScope = sourceBetween(
    chatView,
    'const restoredKnowledgeScope =',
    'await startStream({',
  )
  assert.match(
    sendScope,
    /let canonicalScope = knowledgeScopeProjection\.knowledge_scope/,
  )
  assert.match(
    sendScope,
    /canProjectFolderScope[\s\S]*restoredKnowledgeScope[\s\S]*!useSettingsStoreInstance\._knowledgeScopeDirty/,
  )
  assert.match(
    sendScope,
    /canonicalScope = cloneKnowledgeScopeRequest\(restoredKnowledgeScope\)/,
  )
  assert.doesNotMatch(
    sendScope,
    /_knowledgeScopeDirty\s*=\s*false|_restoredKnowledgeScope\s*=/,
  )
})

test('stores one clean canonical snapshot and clears it across session lifecycles', () => {
  assert.match(
    settingsStore,
    /_restoredKnowledgeScope: null as KnowledgeScopeRequest \| null/,
  )
  assert.match(settingsStore, /_knowledgeScopeDirty: false/)
  assert.match(
    settingsStore,
    /const canonicalScope =[\s\S]*cloneKnowledgeScopeRequest\(state\.knowledge_scope\)[\s\S]*this\._restoredKnowledgeScope = canonicalScope/,
  )
  assert.match(
    settingsStore,
    /applyLastRequestState\([^)]*\) \{\s*this\._restoredKnowledgeScope = null;\s*this\._knowledgeScopeDirty = false;/,
  )
  assert.match(
    settingsStore,
    /restoreDefaultsIfSnapshotted\(\) \{[\s\S]*this\._restoredKnowledgeScope = null;\s*this\._knowledgeScopeDirty = false;/,
  )
  assert.match(
    chatView,
    /const lastState = sessionRes\.data\.last_request_state;[\s\S]*applyLastRequestState\(lastState\)/,
  )
  assert.match(
    settingsStore,
    /selectAgent\(agentId:[\s\S]*if \(agentChanged \|\| knowledgeScopeChanged\) \{\s*this\._restoredKnowledgeScope = null;\s*this\._knowledgeScopeDirty = true;/,
  )
})

test('marks only real KB, file, tag, and folder identity changes dirty', () => {
  const selectKnowledgeBases = sourceBetween(
    settingsStore,
    'selectKnowledgeBases(kbIds: string[])',
    'addKnowledgeBase(kbId: string)',
  )
  const addFile = sourceBetween(
    settingsStore,
    'addFile(fileId: string)',
    'removeFile(fileId: string)',
  )
  const addTag = sourceBetween(
    settingsStore,
    'addTag(tag:',
    'removeTag(tagId:',
  )
  const replaceFolders = sourceBetween(
    settingsStore,
    'replaceFolderScopes(selections:',
    'addFolderScope(selection:',
  )

  assert.match(
    selectKnowledgeBases,
    /!haveSameStringIdentities\([\s\S]*this\._knowledgeScopeDirty = true/,
  )
  assert.match(
    addFile,
    /!haveSameStringIdentities\(previous, this\.settings\.selectedFiles\)[\s\S]*this\._knowledgeScopeDirty = true/,
  )
  assert.match(
    addTag,
    /!haveSameTagIdentities\(previous, this\.settings\.selectedTags\)[\s\S]*this\._knowledgeScopeDirty = true/,
  )
  assert.match(
    replaceFolders,
    /!haveSameFolderIdentities\(this\.selectedFolderScopes, next\)[\s\S]*this\._knowledgeScopeDirty = true/,
  )
  assert.match(
    settingsStore,
    /const\s+haveSameIdentities\s*=\s*\([\s\S]*left\.size === right\.size[\s\S]*right\.has\(identity\)/,
  )
})

test('keeps no-op folder replacement and display or ownership hydration clean', () => {
  const replaceFolders = sourceBetween(
    settingsStore,
    'replaceFolderScopes(selections:',
    'addFolderScope(selection:',
  )
  const enrichFolder = sourceBetween(
    settingsStore,
    'enrichFolderScopeDisplay(',
    'prepareQuickAnswerScope(scope:',
  )
  const ownershipHydration = sourceBetween(
    settingsStore,
    'setFileKbMap(updates:',
    'getSelectedFiles():',
  )

  assert.match(
    replaceFolders,
    /if \(!haveSameFolderIdentities\(this\.selectedFolderScopes, next\)\)/,
  )
  assert.doesNotMatch(enrichFolder, /_knowledgeScopeDirty/)
  assert.doesNotMatch(ownershipHydration, /_knowledgeScopeDirty/)
})

test('keeps stop generation scope-neutral for the next clean replay', () => {
  const stopHandler = sourceBetween(
    chatView,
    'const handleStopGeneration =',
    'const sendMsg = async',
  )

  assert.doesNotMatch(
    stopHandler,
    /_restoredKnowledgeScope|_knowledgeScopeDirty/,
  )
  assert.match(
    chatView,
    /handleStopGeneration[\s\S]*const sendMsg = async[\s\S]*cloneKnowledgeScopeRequest\(restoredKnowledgeScope\)/,
  )
})
