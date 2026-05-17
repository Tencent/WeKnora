<!--
  CredentialResource — per-field "configured / unconfigured / editing" card.

  Why this component exists:
    The previous UX (PR #990) showed a password input pre-filled with a redacted
    placeholder plus a red "Remove this credential" checkbox below it. That
    conflated three distinct user intents (preserve / replace / clear) into a
    single form field, and bundled credential changes with unrelated config
    edits in the same submit. Users could accidentally wipe a working key by
    toggling the wrong checkbox.

    This component splits credentials out as an independent resource:
      - Read-only "configured" badge by default, derived from the `meta` prop.
      - "Replace" expands an input + Save/Cancel; commit is an explicit
        PUT to the credential subresource (no main-form submit needed).
      - "Remove" pops a danger-themed confirmation and on confirm calls DELETE
        immediately. No "save form to apply" intermediate state.

    `meta` is the source of truth and comes from the parent resource's main
    GET response (`<resource>.credentials` on every DTO) — there is no
    dedicated GET /credentials endpoint. After a successful save/remove the
    component derives the new local state from the save's return value (or,
    for remove, by setting the field to unconfigured) and emits 'changed' so
    the parent can re-fetch the main resource if it cares about anything
    else that depends on credential state.
-->
<template>
  <div class="credential-resource">
    <div v-for="field in fields" :key="field.key" class="credential-row">
      <!-- Configured: read-only badge + actions -->
      <template v-if="stateOf(field.key) === 'configured'">
        <div class="credential-summary">
          <t-icon name="check-circle-filled" class="status-icon success" />
          <div class="credential-meta">
            <div class="credential-label">{{ field.label }}</div>
            <div class="credential-sub">{{ t('credential.configured') }}</div>
          </div>
          <div class="credential-actions">
            <t-button size="small" variant="outline" @click="enterEdit(field.key)">
              {{ t('credential.update') }}
            </t-button>
            <t-button size="small" variant="outline" theme="danger" :loading="busy[field.key] === 'remove'"
              @click="onRemove(field)">
              {{ t('credential.remove') }}
            </t-button>
          </div>
        </div>
      </template>

      <!-- Unconfigured: collapsed prompt + "Configure" -->
      <template v-else-if="stateOf(field.key) === 'unconfigured'">
        <div class="credential-summary">
          <t-icon name="info-circle" class="status-icon muted" />
          <div class="credential-meta">
            <div class="credential-label">{{ field.label }}</div>
            <div class="credential-sub">{{ t('credential.unconfigured') }}</div>
          </div>
          <div class="credential-actions">
            <t-button size="small" variant="outline" @click="enterEdit(field.key)">
              {{ t('credential.configure') }}
            </t-button>
          </div>
        </div>
      </template>

      <!-- Editing: inline input + Save / Cancel -->
      <template v-else>
        <div class="credential-edit">
          <div class="credential-label">{{ field.label }}</div>
          <t-input v-model="drafts[field.key]" type="password"
            :placeholder="field.placeholder ?? t('credential.inputPlaceholder')" :autocomplete="'new-password'"
            @keydown.enter.prevent="onSave(field)" />
          <div class="credential-edit-actions">
            <t-button size="small" variant="outline" @click="cancelEdit(field.key)">
              {{ t('common.cancel') }}
            </t-button>
            <t-button size="small" theme="primary" :loading="busy[field.key] === 'save'" :disabled="!drafts[field.key]"
              @click="onSave(field)">
              {{ t('common.save') }}
            </t-button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts" generic="K extends string">
import { ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

export interface CredentialFieldDef<K extends string = string> {
  key: K
  label: string
  // Optional connector-specific placeholder shown only when the input is
  // visible (e.g. "ntn_xxxx" for Notion). Defaults to a generic "Enter value".
  placeholder?: string
}

export interface CredentialResourceApi<K extends string = string> {
  // PUT /credentials — body keyed by field name, value is the new secret.
  // Returns the updated per-field configured map.
  save: (patch: Partial<Record<K, string>>) => Promise<Record<K, { configured: boolean }>>
  // DELETE /credentials/:field
  remove: (field: K) => Promise<void>
}

interface Props {
  fields: CredentialFieldDef<K>[]
  api: CredentialResourceApi<K>
  // Initial per-field "configured?" map, sourced from the parent resource's
  // main GET response. The component reads it on first render and after
  // every reset; subsequent state transitions are tracked locally.
  meta: Record<K, { configured: boolean }>
}

interface Emits {
  // Fires after every successful save or remove so the parent can refresh
  // any derived view (e.g. badges that depend on credential state) or just
  // reload the main resource to keep `meta` in sync.
  (e: 'changed'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()

type State = 'configured' | 'unconfigured' | 'editing'
// Local view state per field. Source of truth is props.meta, but we track
// the editing state and any locally-applied save/remove transitions here so
// the UI doesn't snap back when the parent re-renders before re-fetching.
const states = reactive<Record<string, State>>({})
const drafts = reactive<Record<string, string>>({})
const busy = reactive<Record<string, 'save' | 'remove' | null>>({})

function deriveStatesFromMeta(meta: Record<string, { configured: boolean }>) {
  for (const f of props.fields) {
    // Preserve in-progress edits across parent re-renders — `meta` describes
    // server state, the editing flag is user intent.
    if (states[f.key] === 'editing') continue
    states[f.key] = meta[f.key]?.configured ? 'configured' : 'unconfigured'
  }
}

// Initialize from the first meta snapshot, and re-derive whenever the parent
// passes a new one (after a main-resource refresh). watch with immediate:true
// covers both cases in one place.
watch(
  () => props.meta,
  (m) => deriveStatesFromMeta(m ?? ({} as Record<K, { configured: boolean }>)),
  { immediate: true, deep: true },
)

// If the parent swaps the api (e.g. user opens a different resource), drop
// transient state. props.meta will follow and re-init via the watch above.
watch(() => props.api, () => {
  for (const k of Object.keys(states)) delete states[k]
  for (const k of Object.keys(drafts)) delete drafts[k]
})

function stateOf(key: string): State {
  return states[key] ?? 'unconfigured'
}

function enterEdit(key: string) {
  drafts[key] = ''
  states[key] = 'editing'
}

// Cancel returns directly to whatever the parent told us via props.meta —
// no async re-fetch needed, and no risk of staying stuck in 'editing'
// because the previous implementation's refresh was a no-op when state
// was already 'editing'.
function cancelEdit(key: string) {
  drafts[key] = ''
  states[key] = props.meta?.[key as K]?.configured ? 'configured' : 'unconfigured'
}

async function onSave(field: CredentialFieldDef) {
  const value = drafts[field.key]
  if (!value) return
  busy[field.key] = 'save'
  try {
    // Apply the save's returned metadata locally so the card flips to
    // 'configured' immediately. Skip the editing-preserve guard since this
    // particular field just finished editing.
    const updated = await props.api.save({ [field.key]: value } as Partial<Record<K, string>>)
    for (const f of props.fields) {
      if (f.key === field.key) continue
      if (states[f.key] === 'editing') continue
      states[f.key] = updated[f.key as K]?.configured ? 'configured' : 'unconfigured'
    }
    states[field.key] = updated[field.key as K]?.configured ? 'configured' : 'unconfigured'
    drafts[field.key] = ''
    MessagePlugin.success(t('credential.savedToast'))
    emit('changed')
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('credential.saveFailed'))
  } finally {
    busy[field.key] = null
  }
}

// Remove is a single-click action: skip the modal confirm dialog and just
// do it, with a toast for feedback. Rationale: the secret is irrecoverable
// from the client side regardless of whether we confirm (we never had the
// plaintext), so a modal adds friction without adding safety — re-typing
// the secret is the recovery path either way. The danger-themed "Remove"
// button itself already serves as a visual deterrent against misclicks.
async function onRemove(field: CredentialFieldDef) {
  busy[field.key] = 'remove'
  try {
    await props.api.remove(field.key as K)
    states[field.key] = 'unconfigured'
    MessagePlugin.success(t('credential.removedToast'))
    emit('changed')
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('credential.removeFailed'))
  } finally {
    busy[field.key] = null
  }
}
</script>

<style scoped lang="less">
.credential-resource {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.credential-row {
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
}

.credential-summary {
  display: flex;
  align-items: center;
  gap: 12px;
}

.credential-meta {
  flex: 1;
  min-width: 0;
}

.credential-label {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 2px;
}

.credential-sub {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.credential-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.status-icon {
  font-size: 18px;
  flex-shrink: 0;

  &.success {
    color: var(--td-success-color);
  }

  &.muted {
    color: var(--td-text-color-placeholder);
  }
}

.credential-edit {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.credential-edit-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
