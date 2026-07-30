import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const componentPath = fileURLToPath(new URL('./RetrievalSettings.vue', import.meta.url))

test('feedback weighting is an explicit workspace opt-in with global kill-switch status', async () => {
  const source = await readFile(componentPath, 'utf8')

  assert.match(source, /v-model="localConfig\.feedback_retrieval_weight_enabled"/)
  assert.match(source, /:disabled="!canEdit \|\| !feedbackRetrievalGloballyEnabled"/)
  assert.match(
    source,
    /cfg\.feedback_retrieval_weight_globally_enabled === true/,
    'an omitted or false server capability must keep the workspace switch disabled',
  )
  assert.match(
    source,
    /cfg\.feedback_retrieval_weight_enabled === true/,
    'an omitted workspace value must remain opted out',
  )
  assert.match(source, /retrievalSettings\.feedbackDisabled/)
  assert.match(source, /retrievalSettings\.feedbackCollectionOnly/)
  assert.match(source, /retrievalSettings\.feedbackWorkspaceOptInRequired/)
  assert.match(source, /retrievalSettings\.feedbackWeightActive/)
})
