<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listResources, type Resource } from '@/api/datasource'
import { resourceIconName, resourceTypeLabel, shouldShowResourceType } from '../resourcePresentation'
import {
  DINGTALK_DESCRIBE_PARENT_PREFIX,
  DINGTALK_MANUAL_REFERENCE,
  dingtalkManualKind,
  dingtalkManualReference,
  dingtalkManualReferenceId,
  dingtalkResourceHint,
  extractDingTalkId,
} from './dingtalkResources'

// DingTalk is the only connector whose selectable content is partly invisible to
// the tree: a personal-space document and a 多维表 Base are readable by id but
// nothing enumerates them, so the user pastes a link (or an id) and picks which
// of the two it is. The two link shapes are byte-identical, which is why the
// kind is a control and never inferred.
//
// This component owns that entry and the preview of what the data source will
// sync — manual references and tree picks together. The tree itself belongs to
// the dialog: it lists through the dialog's draft and renders the dialog's
// lazy-load state, and the preview only reads the two facts it needs from it
// (which rows exist, and which children an expanded row has).
const props = defineProps<{
  /** The rows the dialog has listed, so a tree pick keeps its name here. */
  resources: Resource[]
  /** Direct children by parent id, exactly as the dialog indexes them. */
  childrenMap: Map<string, Resource[]>
  /** The expansion state the dialog owns and the tree also renders from. */
  expandedResourceIds: Set<string>
  loadingChildrenIds: Set<string>
  /** The data source a describe request is made against; blank skips it. */
  dataSourceId: string
  /** True while the dialog is listing, so committing cannot race it. */
  loading: boolean
  /** The saved manual entry of the data source being edited, if any. */
  savedReference: string
}>()

// The selection is the dialog's, not this component's: the wizard submits it and
// the tree below adds to it. This component only reads it to render the preview,
// and writes it back through the model. Every write also tells the dialog what
// changed, because the draft the picker lists through is built from its form.
const selectedResourceIds = defineModel<string[]>('selectedResourceIds', { required: true })

// selection is what this component renders from. A write goes out as an emit —
// the dialog owns the selection — so the model's own value only comes back on
// the dialog's next patch, while everything here (the preview, the count, the
// describe of a reference just added) has to see the change at once. The watcher
// is the other direction: a selection changed by the dialog (a tree pick, a
// restored row) reaches this component only through the model.
const selection = ref<string[]>([...selectedResourceIds.value])
watch(selectedResourceIds, (ids) => { selection.value = [...(ids || [])] })

const emit = defineEmits<{
  /** The connector described rows; the dialog merges them into its tree. */
  described: [rows: Record<string, Resource>]
  /** A preview row asked to open; the dialog owns the lazy child listing. */
  expand: [id: string]
}>()

const { t } = useI18n()

const manualId = ref('')
const manualKind = ref<'node' | 'base'>('node')
const manualError = ref('')

// describedRows holds the described row of every manual reference, keyed by the
// reference itself. The preview names its rows from it, so the two never
// disagree, and the dialog merges them into its tree so an expandable reference
// (a Base, whose wiki children the connector lists) can be seen and expanded
// like any other node.
const describedRows = ref<Record<string, Resource>>({})
// References whose description is in flight. Adding two ids in a row must not
// ask for the first one twice: the second call would not yet see it in
// describedRows.
const describePending = new Set<string>()

// A saved manual entry is pre-filled, so the dialog is not the only place the
// selection can be read: the tree never shows a manual reference, and the field
// is where the user sees (and can correct) what was saved.
watch(() => props.savedReference, (reference) => {
  if (!reference || !DINGTALK_MANUAL_REFERENCE.test(reference)) return
  manualKind.value = dingtalkManualKind(reference)
  manualId.value = dingtalkManualReferenceId(reference)
}, { immediate: true })

// manualReferences lists the manually entered selections. They are not in the
// tree, so the tree itself can never show or uncheck them; this is what the
// entry block renders and what the dialog's count adds in.
const manualReferences = computed(() =>
  selection.value.filter(id => DINGTALK_MANUAL_REFERENCE.test(id)),
)

// hasManualSelection is true once at least one manual reference is in the
// selection. Everything about the preview keys off it, so a user who never
// touches the manual block keeps the exact tree-only presentation.
const hasManualSelection = computed(() => manualReferences.value.length > 0)

// applyManualEntry adds the entered id to the selection as the reference the
// backend accepts for the chosen kind, and leaves the field holding that entry.
// Returns false with the inline error set when nothing usable was entered, so a
// caller advancing the wizard can stay on the step.
function applyManualEntry(): boolean {
  const id = extractDingTalkId(manualId.value)
  if (!id) {
    // Nothing usable was entered, so the input is kept exactly as typed: it is
    // the text the user has to correct.
    manualError.value = t('datasource.dingtalk.manualIdRequired')
    return false
  }
  manualError.value = ''
  // The committed entry stays in the field, normalised: a pasted link is
  // replaced by the bare id it carried, so the box shows exactly what was added
  // and can be read or reused without retyping. Adding it again, or leaving the
  // step with it still in the box, does not duplicate anything — the selection
  // is a cover set, so one reference is one entry.
  manualId.value = id
  const cover = new Set(selection.value)
  cover.add(dingtalkManualReference(id, manualKind.value))
  commit([...cover])
  // Ask the connector what the new reference is called; the row shows the id
  // until that answer arrives (or forever, if it never does).
  void resolveManualNames()
  return true
}

function removeManualReference(reference: string) {
  commit(selection.value.filter(id => id !== reference))
  if (!(reference in describedRows.value)) return
  const remaining = { ...describedRows.value }
  delete remaining[reference]
  describedRows.value = remaining
  emit('described', { ...describedRows.value })
}

// removeTreeSelectionRow drops one tree-picked resource from the preview. It
// filters the cover set directly instead of asking the dialog to toggle it: a
// saved node the lazy tree has not loaded has no check state yet, so a toggle
// would check it again rather than remove it.
function removeTreeSelectionRow(id: string) {
  commit(selection.value.filter(x => x !== id))
}

// commit writes the selection back to the dialog. The dialog owns the form, so
// it — not this component — decides what a changed selection means for the draft
// the picker lists through.
function commit(ids: string[]) {
  selection.value = [...ids]
  selectedResourceIds.value = [...ids]
}

// resolveManualNames turns each manual reference into a described row: the
// connector reads the node (or Base) by id and reports its display name, which
// is exactly the name the sync will title the item with. It is the only call the
// collapsed presentation makes — never the tree listing — and every failure
// degrades to the id already on screen, because a name lookup must not be able
// to break the step.
async function resolveManualNames() {
  const known = new Set(Object.keys(describedRows.value))
  const pending = manualReferences.value.filter(id => !known.has(id) && !describePending.has(id))
  if (pending.length === 0) return
  // No draft, no description: the rows keep the ids the user typed, and no
  // listing is attempted (a describe against an empty id is certain to fail).
  const dsId = (props.dataSourceId || '').trim()
  if (!dsId) return
  for (const id of pending) describePending.add(id)
  await Promise.all(pending.map(async (id) => {
    try {
      const res = await listResources(dsId, DINGTALK_DESCRIBE_PARENT_PREFIX + id)
      const rows: Resource[] = res?.data || res || []
      // The connector echoes the reference it described, so an unexpected row
      // is ignored rather than shown under the wrong name; a reference removed
      // while the lookup was in flight is dropped for the same reason.
      const row = rows.find(r => r.external_id === id)
      if (row && manualReferences.value.includes(id)) {
        describedRows.value = { ...describedRows.value, [id]: row }
      }
    } catch {
      // The id stays on screen; nothing else about the step depends on this.
    } finally {
      describePending.delete(id)
    }
  }))
  emit('described', { ...describedRows.value })
}

// The draft the describe call needs is created by the dialog when the resources
// step opens, so the first attempt can legitimately find none — and with a saved
// manual reference the step opens before that draft exists. Describing is
// retried whenever a draft is available, immediate so that a reference restored
// into an already-created data source is named on mount rather than kept as a
// bare id until the user retypes it.
watch(() => props.dataSourceId, (id) => {
  if ((id || '').trim()) void resolveManualNames()
}, { immediate: true })

interface SelectionRow {
  id: string
  manual: boolean
  kind: string
  label: string
}

// resourceById is the lookup behind every per-row fact the preview states about
// a selection — what it syncs, whether it can be expanded, which children it
// has. Rows are keyed by external_id, which is also what a selection stores, so
// a manual reference and a tree pick resolve the same way.
const resourceById = computed(() => new Map(props.resources.map(r => [r.external_id, r])))

// selectionRows is the full selection, in selection order: the preview is what
// this data source will sync, not a view of the tree.
const selectionRows = computed<SelectionRow[]>(() => {
  const byId = resourceById.value
  return selection.value.map((id) => {
    if (DINGTALK_MANUAL_REFERENCE.test(id)) {
      // The described row carries the same name the sync puts on the item; the
      // id stays the fallback whenever the name could not be resolved, so the
      // row is never empty and never wrong.
      const described = describedRows.value[id]
      return {
        id,
        manual: true,
        kind: t(dingtalkManualKind(id) === 'base'
          ? 'datasource.dingtalk.manualKindBase'
          : 'datasource.dingtalk.manualKindNode'),
        label: (described?.name || '').trim() || dingtalkManualReferenceId(id),
      }
    }
    const resource = byId.get(id)
    return {
      id,
      manual: false,
      // A mapped connector type names itself; anything else is just "knowledge
      // base". Before the lazy tree has loaded a saved node, only its id is
      // known — showing the id is still exactly what will be synced.
      kind: resource && shouldShowResourceType(resource.type)
        ? resourceTypeLabel(resource.type, t)
        : t('datasource.dingtalk.selectionTreeKind'),
      label: resource?.name || id,
    }
  })
})

// selectedResource returns the row a preview line stands for: the connector's
// described row for a manual reference (the only answer a Base or a
// personal-space node ever has) or the row a listing delivered. Reading one
// lookup keeps the name, the hint and the expansion from disagreeing.
function selectedResource(id: string): Resource | undefined {
  return describedRows.value[id] || resourceById.value.get(id)
}

// rowExpandable reports whether a row may offer a disclosure control. Only a row
// the connector reports as having children gets one: a leaf has nothing to list,
// so an expander there could only promise content that does not exist.
function rowExpandable(row: SelectionRow): boolean {
  return selectedResource(row.id)?.has_children === true
}

// rowChildren are the direct children of an expanded row. They are the same rows
// the tree renders: children fetched through listResources(parent_id=<ref>) and
// indexed by their parent_id, so a child shown here and the same child shown in
// the tree are one row, not two copies.
function rowChildren(id: string): Resource[] {
  return props.childrenMap.get(id) || []
}

function rowExpanded(id: string): boolean {
  return props.expandedResourceIds.has(id)
}

function rowLoading(id: string): boolean {
  return props.loadingChildrenIds.has(id)
}

// toggleRow hands a preview row to the dialog. Both halves of opening it — the
// expansion state and the lazy child listing — belong to the tree the dialog
// renders, so this component only asks.
function toggleRow(id: string) {
  emit('expand', id)
}

// rowHint is the one-line answer to "what does this row sync", rendered as part
// of the row so it is readable without expanding anything. Only a Base has one:
// its tables are the content a base= reference ingests, while the documents
// listed under it are separate selections.
function rowHint(row: SelectionRow): string {
  const described = selectedResource(row.id)
  return described ? dingtalkResourceHint(described, t) : ''
}

// hasPendingEntry is true while the field holds text that was never committed.
// The dialog asks before advancing the wizard, so a typed id is applied (or
// reported) rather than dropped.
const hasPendingEntry = computed(() => manualId.value.trim() !== '')

defineExpose({
  hasPendingEntry,
  applyManualEntry,
  resolveManualNames,
  removeManualReference,
  removeTreeSelectionRow,
  selectionRows,
  manualReferences,
  hasManualSelection,
  describedRows,
  manualId,
  manualKind,
  manualError,
  rowExpandable,
  rowChildren,
  rowExpanded,
  rowLoading,
  rowHint,
  toggleRow,
})
</script>

<template>
  <!-- Manual entry: shown alongside the tree, not instead of it. Tree picking
       below is unchanged, and a user who only picks from the tree never touches
       this. -->
  <div class="dingtalk-manual-input">
    <label class="dingtalk-manual-input__label">
      {{ t('datasource.dingtalk.manualLabel') }}
      <t-tooltip :content="t('datasource.dingtalk.manualHint')" placement="top">
        <t-icon name="help-circle" class="dingtalk-manual-input__help" />
      </t-tooltip>
    </label>
    <div class="dingtalk-manual-input__row">
      <t-radio-group v-model="manualKind" :disabled="loading">
        <t-radio-button value="node">{{ t('datasource.dingtalk.manualKindNode') }}</t-radio-button>
        <t-radio-button value="base">{{ t('datasource.dingtalk.manualKindBase') }}</t-radio-button>
      </t-radio-group>
      <t-input
        v-model="manualId"
        class="dingtalk-manual-input__field"
        :placeholder="t('datasource.dingtalk.manualPlaceholder')"
        :status="manualError ? 'error' : 'default'"
        clearable
        @enter="applyManualEntry"
        @input="manualError = ''"
      />
      <t-button theme="primary" @click="applyManualEntry">
        {{ t('datasource.dingtalk.manualAdd') }}
      </t-button>
    </div>
    <p v-if="manualError" class="dingtalk-manual-input__error">{{ manualError }}</p>
  </div>

  <!-- What THIS data source will sync, first and prominently: the manual
       references and the tree-checked resources together, in one list. The tree
       cannot show the manual part at all, so the selection area must not depend
       on it. Rendered only once a manual reference exists, so a tree-only user
       keeps the exact pre-existing step. -->
  <div v-if="hasManualSelection" class="ds-selection-preview">
    <div class="ds-selection-preview__title">{{ t('datasource.dingtalk.selectionTitle') }}</div>
    <div v-for="row in selectionRows" :key="row.id" class="ds-selection-preview__row">
      <div class="ds-selection-preview__head">
        <!-- The disclosure control expands with the same lazy call the tree
             uses (listResources(parent_id=<ref>)). It exists only for a row the
             connector reports children for: a leaf has nothing to list, so an
             expander there would promise content that is not there. -->
        <button
          v-if="rowExpandable(row)"
          type="button"
          class="ds-selection-preview__expand"
          :aria-expanded="rowExpanded(row.id)"
          :aria-label="rowExpanded(row.id)
            ? t('knowledgeStages.collapseBranch')
            : t('knowledgeStages.expandBranch')"
          @click="toggleRow(row.id)"
        >
          <t-loading v-if="rowLoading(row.id)" size="12px" />
          <t-icon v-else :name="rowExpanded(row.id) ? 'chevron-down' : 'chevron-right'" size="12px" />
        </button>
        <span v-else class="ds-selection-preview__expand-spacer" aria-hidden="true" />
        <span class="ds-selection-preview__kind">{{ row.kind }}</span>
        <!-- The whole name, wrapped rather than ellipsised, and the id it came
             from on hover: a truncated name cannot be confirmed. -->
        <span class="ds-selection-preview__label" :title="row.id">{{ row.label }}</span>
        <!-- "Added by ID, not in the tree" explains the row, it is not part of
             its name: as an icon tooltip it costs the name no room. -->
        <t-tooltip v-if="row.manual" :content="t('datasource.dingtalk.selectionManualNote')" placement="top">
          <span class="ds-selection-preview__note-icon" role="img" :aria-label="t('datasource.dingtalk.selectionManualNote')">
            <t-icon name="help-circle" />
          </span>
        </t-tooltip>
        <button
          type="button"
          class="ds-selection-preview__remove"
          @click="row.manual ? removeManualReference(row.id) : removeTreeSelectionRow(row.id)"
        >{{ t('datasource.dingtalk.selectionRemove') }}</button>
      </div>
      <!-- What the row syncs, in the row itself: for a Base the tables are the
           content, and that is the single most important fact about the
           selection, so it is never hidden behind an expansion. -->
      <p v-if="rowHint(row)" class="ds-selection-preview__hint">{{ rowHint(row) }}</p>
      <!-- The children the row lists on expansion. They are not selections:
           under a Base they are knowledge-base documents that sync on their own,
           which is exactly what their label says — they are not the tables the
           line above describes. -->
      <div v-if="rowExpanded(row.id)" class="ds-selection-preview__children">
        <div v-for="child in rowChildren(row.id)" :key="child.external_id" class="ds-selection-preview__child">
          <t-icon :name="resourceIconName(child)" size="14px" />
          <span class="ds-selection-preview__child-name" :title="child.name || t('datasource.untitled')">
            {{ child.name || t('datasource.untitled') }}
          </span>
          <span v-if="shouldShowResourceType(child.type)" class="ds-selection-preview__child-kind">
            {{ resourceTypeLabel(child.type, t) }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="less">
@import '../datasource-surface.less';

/* DingTalk manual entry (node / Base id) - shown before the lazy-load tree. */
.dingtalk-manual-input {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
}

.dingtalk-manual-input__label {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.dingtalk-manual-input__help {
  font-size: var(--app-text-lg);
  color: var(--td-text-color-placeholder);
  cursor: help;

  &:hover {
    color: var(--td-text-color-secondary);
  }
}

.dingtalk-manual-input__row {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.dingtalk-manual-input__field {
  flex: 1;
  min-width: 0;
}

.dingtalk-manual-input__error {
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-error-color);
}

/* Selection preview: every resource this data source will sync, manual
   references and tree picks alike. It is the primary content of the step
   whenever a manual reference exists, because the tree can never show the
   manual part. */
.ds-selection-preview {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ds-selection-preview__title {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.ds-selection-preview__row {
  .ds-inset-panel();
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
}

/* The name line: expander | kind | name | note icon | remove. The name is the
   only part that flexes, so it gets the row's spare width instead of the
   ellipsis the old three-part layout forced on it. */
.ds-selection-preview__head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.ds-selection-preview__expand {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  padding: 0;
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background var(--app-motion-instant) ease;
}

.ds-selection-preview__expand:hover,
.ds-selection-preview__expand:focus-visible {
  background: var(--td-bg-color-container-hover);
  outline: none;
}

/* Keeps a leaf's name aligned with an expandable row's name. */
.ds-selection-preview__expand-spacer {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
}

.ds-selection-preview__kind {
  flex-shrink: 0;
  font-size: var(--app-text-sm);
  line-height: 1.4;
  color: var(--td-text-color-secondary);
}

.ds-selection-preview__label {
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-md);
  line-height: 1.5;
  color: var(--td-text-color-primary);
  /* Wrap the whole name instead of truncating it: the name is how the owner
     confirms the row, so an ellipsis here is the defect, not a nicety. */
  overflow-wrap: anywhere;
}

.ds-selection-preview__hint {
  margin: 0;
  /* Aligns with the name above, past the disclosure control. */
  padding-left: 24px;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  /* The table list is the row's most important fact; it wraps rather than
     being clipped to a few names. */
  overflow-wrap: anywhere;
}

.ds-selection-preview__children {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-left: 24px;
  padding-left: 8px;
  border-left: 1px solid var(--td-border-level-1-color);
}

.ds-selection-preview__child {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.ds-selection-preview__child-name {
  min-width: 0;
  overflow-wrap: anywhere;
}

.ds-selection-preview__child-kind {
  flex-shrink: 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.ds-selection-preview__note-icon {
  display: inline-flex;
  flex-shrink: 0;
  font-size: var(--app-text-lg);
  color: var(--td-text-color-placeholder);
  cursor: help;
}

.ds-selection-preview__note-icon:hover {
  color: var(--td-text-color-secondary);
}

.ds-selection-preview__remove {
  flex-shrink: 0;
  padding: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color var(--app-motion-instant) ease;
}

.ds-selection-preview__remove:hover,
.ds-selection-preview__remove:focus-visible {
  color: var(--td-error-color);
  outline: none;
}
</style>
