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
      <!--
        Per-field label, rendered only when there is more than one credential
        (e.g. WeKnora Cloud's api_key + app_secret) so a single-field card
        doesn't double up with the parent's outer .form-label. Single-field
        consumers (ModelEditorDialog API key, WebSearch provider api_key,
        McpService api_key) keep showing the parent label only.
      -->
      <div v-if="fields.length > 1" class="credential-row-label">{{ field.label }}</div>

      <!-- Configured: faux input row (visually identical to t-input) -->
      <template v-if="stateOf(field.key) === 'configured'">
        <!--
          Two possible looks:
          a) default — ✓ 已配置 + [更换 | 移除]
          b) confirm-remove pending — ⚠ 确认移除？此操作不可撤销 + [取消 | 确认移除]

          We use a sub-state instead of a global modal because the modal
          forces the user to context-switch to the screen center, then back
          to this row to see the result. Inline confirm keeps focus on the
          row that's actually changing.
        -->
        <div
          class="credential-faux-input"
          :class="{ 'is-confirm-remove': pendingRemove[field.key] }"
          :title="pendingRemove[field.key] ? '' : t('credential.configured')"
        >
          <template v-if="pendingRemove[field.key]">
            <t-icon name="error-circle-filled" class="status-icon warn" />
            <span class="credential-faux-text danger">{{ t('credential.confirmRemovePrompt') }}</span>
            <div class="credential-actions">
              <t-button size="small" variant="text" @click="cancelPendingRemove(field.key)">
                {{ t('common.cancel') }}
              </t-button>
              <span class="action-divider"></span>
              <t-button size="small" variant="text" theme="danger" :loading="busy[field.key] === 'remove'"
                @click="confirmRemove(field)">
                {{ t('credential.confirmRemove') }}
              </t-button>
            </div>
          </template>
          <template v-else>
            <t-icon name="check-circle-filled" class="status-icon success" />
            <span class="credential-faux-text">{{ t('credential.configured') }}</span>
            <div class="credential-actions">
              <t-button size="small" variant="text" @click="enterEdit(field.key)">
                {{ t('credential.update') }}
              </t-button>
              <span class="action-divider"></span>
              <t-button size="small" variant="text" theme="danger" @click="requestRemove(field.key)">
                {{ t('credential.remove') }}
              </t-button>
            </div>
          </template>
        </div>
      </template>

      <!-- Unconfigured: faux input row with a single "Configure" affordance -->
      <template v-else-if="stateOf(field.key) === 'unconfigured'">
        <div
          class="credential-faux-input is-empty"
          :class="{ 'is-just-removed': inlineToast[field.key]?.kind === 'removed' }"
          @click="enterEdit(field.key)"
        >
          <!--
            Right after a successful remove we hold the row in place but swap
            the icon + placeholder text for a brief success state. After
            ~2.4s the row fades back to its plain "未配置" prompt. This
            keeps feedback anchored to where the user just clicked, instead
            of asking them to glance at a global toast somewhere else.
          -->
          <template v-if="inlineToast[field.key]?.kind === 'removed'">
            <t-icon name="check-circle-filled" class="status-icon success" />
            <span class="credential-faux-text">{{ t('credential.removedToast') }}</span>
          </template>
          <template v-else>
            <t-icon name="lock-on" class="status-icon muted" />
            <span class="credential-faux-text muted">{{ t('credential.unconfigured') }}</span>
            <div class="credential-actions">
              <t-button size="small" variant="text" theme="primary" @click.stop="enterEdit(field.key)">
                {{ t('credential.configure') }}
              </t-button>
            </div>
          </template>
        </div>
      </template>

      <!-- Editing: real input + tiny action row beneath -->
      <template v-else>
        <div class="credential-edit">
          <t-input v-model="drafts[field.key]" type="password"
            :placeholder="field.placeholder ?? t('credential.inputPlaceholder')" :autocomplete="'new-password'"
            class="credential-edit-input" @enter="onSave(field)">
            <template #prefix-icon><t-icon name="lock-on" /></template>
          </t-input>
          <div class="credential-edit-actions">
            <t-button size="small" variant="text" @click="cancelEdit(field.key)">
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
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
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

// Per-field "did the user just press Remove" flag. While true, the
// configured row swaps to an inline confirm prompt instead of immediately
// deleting. Cleared on cancel, on the actual DELETE request firing, and
// any time the field leaves the configured state for any other reason.
const pendingRemove = reactive<Record<string, boolean>>({})

// Inline per-field flash message used as an anchored "toast" instead of the
// global MessagePlugin one for actions whose effect happens at this exact
// row (currently: remove). Cleared after a short delay so the row reverts
// to its normal placeholder. Keyed by field.key.
type InlineToastKind = 'removed'
const inlineToast = reactive<Record<string, { kind: InlineToastKind } | null>>({})
const inlineToastTimers: Record<string, ReturnType<typeof setTimeout> | null> = {}

function flashInlineToast(key: string, kind: InlineToastKind, ms = 2400) {
  inlineToast[key] = { kind }
  if (inlineToastTimers[key]) {
    clearTimeout(inlineToastTimers[key]!)
  }
  inlineToastTimers[key] = setTimeout(() => {
    inlineToast[key] = null
    inlineToastTimers[key] = null
  }, ms)
}

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
  for (const k of Object.keys(pendingRemove)) delete pendingRemove[k]
})

function stateOf(key: string): State {
  return states[key] ?? 'unconfigured'
}

function enterEdit(key: string) {
  drafts[key] = ''
  states[key] = 'editing'
  // If the user was mid-confirm and changed their mind ("update" instead
  // of "remove"), drop the pending flag so we don't bounce back into the
  // confirm UI when they cancel the edit.
  pendingRemove[key] = false
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

// Two-step remove. First click flips the row to a confirm state; second
// click actually fires DELETE. We deliberately do NOT use a global modal
// here — the row is also the place where the result will appear, and a
// modal forces an unnecessary screen-center detour. The danger-themed
// "确认移除" button on a tinted-warning row gives the same protection
// against fat-fingered destructive clicks without that detour.
//
// Errors still surface via global MessagePlugin so the user can't miss them.
function requestRemove(key: string) {
  pendingRemove[key] = true
}

function cancelPendingRemove(key: string) {
  pendingRemove[key] = false
}

async function confirmRemove(field: CredentialFieldDef) {
  busy[field.key] = 'remove'
  try {
    await props.api.remove(field.key as K)
    states[field.key] = 'unconfigured'
    pendingRemove[field.key] = false
    flashInlineToast(field.key, 'removed')
    emit('changed')
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('credential.removeFailed'))
  } finally {
    busy[field.key] = null
  }
}

// Cancel pending inline-toast timers when the component unmounts so we
// don't write to a torn-down reactive object after navigation.
onBeforeUnmount(() => {
  for (const k of Object.keys(inlineToastTimers)) {
    if (inlineToastTimers[k]) {
      clearTimeout(inlineToastTimers[k]!)
      inlineToastTimers[k] = null
    }
  }
})
</script>

<style scoped lang="less">
.credential-resource {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.credential-row {
  /* Empty wrapper now — each state owns its own outer styling so the
     "configured" and "unconfigured" rows can mimic a t-input exactly,
     and the editing state can defer the chrome to t-input itself. */
}

.credential-row-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  margin-bottom: 4px;
  line-height: 1.4;
}

/*
  Faux-input row: visually identical to a TDesign default t-input
    - 32px tall
    - same border (--td-component-border) + radius (6px) + bg
    - 0 12px horizontal padding
  This makes the credential card stop looking like "a card inside a card"
  when it sits between Base URL and 自定义请求头 — it reads as a normal
  field, just one that doesn't accept typed input.
*/
.credential-faux-input {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 4px 0 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
  border-radius: 6px;
  font-size: 13px;
  transition: border-color 0.15s ease, background-color 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color-hover);
  }

  &.is-empty {
    cursor: pointer;
    background: var(--td-bg-color-container);

    &:hover {
      background: var(--td-bg-color-container-hover);
    }
  }

  /*
    Just-removed flash state: brief tinted background + brand-color border
    so the row gives the user clear, anchored confirmation that the remove
    actually happened — no need for them to find a corner toast. Auto-fades
    after ~2.4s back to plain "未配置" via the inlineToast timer.
  */
  &.is-just-removed {
    background: var(--td-success-color-light);
    border-color: var(--td-success-color-focus);
    animation: credential-toast-flash 0.25s ease both;
    cursor: default;

    &:hover {
      background: var(--td-success-color-light);
      border-color: var(--td-success-color-focus);
    }
  }

  /*
    Confirm-remove state: warning-tinted background + danger-color border,
    making it obvious that the row is in a destructive-action standoff. The
    button group changes to [Cancel | Confirm-danger]; user has to make a
    deliberate second click to actually delete.
  */
  &.is-confirm-remove {
    background: var(--td-error-color-light);
    border-color: var(--td-error-color-focus);
    animation: credential-confirm-flash 0.2s ease both;

    &:hover {
      border-color: var(--td-error-color-focus);
    }
  }
}

@keyframes credential-toast-flash {
  from {
    background: var(--td-bg-color-container);
    border-color: var(--td-component-border);
  }
  to {
    background: var(--td-success-color-light);
    border-color: var(--td-success-color-focus);
  }
}

@keyframes credential-confirm-flash {
  from {
    background: var(--td-bg-color-container);
    border-color: var(--td-component-border);
  }
  to {
    background: var(--td-error-color-light);
    border-color: var(--td-error-color-focus);
  }
}

.credential-faux-text {
  flex: 1;
  min-width: 0;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  &.muted {
    color: var(--td-text-color-placeholder);
  }

  &.danger {
    color: var(--td-error-color);
    font-weight: 500;
  }
}

.status-icon {
  flex-shrink: 0;
  font-size: 16px;

  &.success {
    color: var(--td-success-color);
  }

  &.muted {
    color: var(--td-text-color-placeholder);
  }

  &.warn {
    color: var(--td-error-color);
  }
}

/*
  Inline action buttons inside the faux input. We use `variant="text"` so
  they read as inline text affordances — same visual weight as the eye
  toggle on the create-mode API key input. A 1px divider between Update
  and Remove gives them just enough separation without adding boxes.
*/
.credential-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;

  :deep(.t-button--variant-text) {
    height: 24px;
    padding: 0 8px;
    font-size: 12px;
    border-radius: 4px;
  }
}

.action-divider {
  width: 1px;
  height: 14px;
  background: var(--td-component-stroke);
  margin: 0 2px;
}

/*
  Editing state — let t-input own its frame; we just stack a tiny
  end-aligned action row underneath. No border on this wrapper.
*/
.credential-edit {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.credential-edit-actions {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 4px;

  :deep(.t-button) {
    height: 28px;
    padding: 0 12px;
    font-size: 12px;
  }
}
</style>
