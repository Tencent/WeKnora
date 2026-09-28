<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  createDataSource,
  updateDataSource,
  triggerSync,
  validateConnection,
  validateCredentials,
  listResources,
  resolveResourceAncestors,
  deleteDataSource,
  putDataSourceCredentials,
  deleteDataSourceCredentials,
  type DataSource,
  type Resource,
} from '@/api/datasource'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import DataSourceTypeIcon from './DataSourceTypeIcon.vue'
import { getDatasourceIconUrl } from './datasourceIcons'

const props = defineProps<{
  kbId: string
  dataSource: DataSource | null
}>()

const visible = defineModel<boolean>('visible', { default: false })
const emit = defineEmits<{ saved: [] }>()
const { t } = useI18n()

const isEdit = computed(() => !!props.dataSource)
const step = ref(0)
const submitting = ref(false)

// In edit mode the credential "configured?" flag travels on the main
// DataSource response (DataSource.credentials.credentials.configured —
// server-side dto.DataSourceResponse.Credentials). True iff a credential
// map is currently stored server-side.
const credentialsConfigured = ref(false)

// "Replace credentials" mode toggle in edit. Defaults to false: a configured
// connector shows a small "Credentials configured ✓" line with Replace /
// Remove actions. Toggling Replace reveals the credential inputs so the
// user can type a new set. Untoggling discards anything typed.
const replaceCredentialsMode = ref(false)

// Whether the credential input section is interactive right now. In create
// mode it's always shown; in edit mode only when the user opted in to
// Replace, OR when nothing is configured yet (degenerate case where the
// data source row exists with no credentials stored).
const credentialsInputVisible = computed(() => {
  if (!isEdit.value) return true
  if (!credentialsConfigured.value) return true
  return replaceCredentialsMode.value
})

function refreshCredentialsStatus() {
  // Re-derive from whatever the parent passed in props.dataSource. Called
  // when the dialog opens or props.dataSource is swapped; the parent is
  // expected to re-fetch the data source list after credential mutations
  // so the new metadata flows in here automatically.
  if (!isEdit.value || !props.dataSource) {
    credentialsConfigured.value = false
    return
  }
  credentialsConfigured.value =
    props.dataSource.credentials?.credentials?.configured === true
}

// Single-click remove with toast feedback. Mirrors the CredentialResource
// component's UX: the secret is irrecoverable client-side either way, so a
// modal confirm just adds friction. The danger-themed button is the deterrent.
const pendingRemoveCredentials = ref(false)
const removingCredentials = ref(false)

function requestRemoveCredentials() {
  pendingRemoveCredentials.value = true
}

function cancelPendingRemoveCredentials() {
  pendingRemoveCredentials.value = false
}

async function confirmRemoveCredentials() {
  if (!props.dataSource?.id) return
  removingCredentials.value = true
  try {
    await deleteDataSourceCredentials(props.dataSource.id)
    credentialsConfigured.value = false
    replaceCredentialsMode.value = false
    pendingRemoveCredentials.value = false
    form.value.config.credentials = {}
    MessagePlugin.success(t('credential.removedToast'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('credential.removeFailed'))
  } finally {
    removingCredentials.value = false
  }
}

function cancelReplaceCredentials() {
  replaceCredentialsMode.value = false
  pendingRemoveCredentials.value = false
  form.value.config.credentials = {}
  rssAuthHeaders.value = []
  testResult.value = credentialsConfigured.value ? 'success' : ''
  testErrorMsg.value = ''
}

interface CustomHeaderItem {
  key: string
  value: string
}

const rssAuthHeaders = ref<CustomHeaderItem[]>([])

function serializeAuthHeaders(items: CustomHeaderItem[]): string {
  return items
    .filter(h => h.key.trim())
    .map(h => `${h.key.trim()}: ${h.value}`)
    .join('\n')
}

function syncRssAuthHeadersToCredentials() {
  if (form.value.type !== 'rss') return
  const serialized = serializeAuthHeaders(rssAuthHeaders.value)
  if (serialized) {
    form.value.config.credentials.auth_headers = serialized
  } else {
    delete form.value.config.credentials.auth_headers
  }
}

// Feed URLs may still live in credentials on older rows (not returned by the
// API). The backend copies them into settings on read; fall back to the
// selected feed resource IDs when settings are still empty.
function hydrateRssFeedUrlsFromConfig(config: { settings?: Record<string, any>; resource_ids?: string[] }) {
  const settings = config.settings || {}
  if (String(settings.feed_urls || '').trim()) {
    return { ...settings }
  }
  const ids = config.resource_ids || []
  if (ids.length === 0) {
    return { ...settings }
  }
  return { ...settings, feed_urls: ids.join('\n') }
}

function addRssAuthHeader() {
  rssAuthHeaders.value.push({ key: '', value: '' })
}

function removeRssAuthHeader(idx: number) {
  rssAuthHeaders.value.splice(idx, 1)
}

function needsConnectionTest(): boolean {
  return !(isEdit.value && credentialsConfigured.value && !replaceCredentialsMode.value)
}

function hydrateConfluenceCredentialsFromSettings() {
  if (form.value.type !== 'confluence') return
  const settings = form.value.config.settings || {}
  const creds = form.value.config.credentials || {}
  form.value.config.credentials = {
    ...creds,
    edition: creds.edition || settings.edition || 'server',
    base_url: creds.base_url || settings.base_url || '',
    username: creds.username || settings.username || '',
  }
}

function syncConfluencePublicFieldsToSettings() {
  if (form.value.type !== 'confluence') return
  const creds = form.value.config.credentials || {}
  form.value.config.settings = {
    ...(form.value.config.settings || {}),
    ...(creds.edition ? { edition: creds.edition } : {}),
    ...(creds.base_url ? { base_url: creds.base_url } : {}),
    ...(creds.username ? { username: creds.username } : {}),
  }
}

function enterReplaceCredentials() {
  pendingRemoveCredentials.value = false
  replaceCredentialsMode.value = true
  hydrateConfluenceCredentialsFromSettings()
  testResult.value = ''
  testErrorMsg.value = ''
}

// Form data
const form = ref({
  name: '',
  type: '',
  config: {
    credentials: {} as Record<string, any>,
    resource_ids: [] as string[],
    settings: {} as Record<string, any>,
  },
  sync_schedule: '0 0 */6 * * *',
  sync_mode: 'incremental' as 'incremental' | 'full',
  conflict_strategy: 'overwrite' as 'overwrite' | 'skip',
  sync_deletions: true,
})

// Yuque folder layout. The key lives in the raw settings bag, and a data source
// created before this control existed carries no folder_mode at all — the
// getter therefore reports the connector's own default (flat) rather than
// rendering the select blank. Writing back is a no-op until the user picks
// something, so opening an existing source can never change its behaviour.
const yuqueFolderMode = computed({
  get: () => (form.value.config.settings?.folder_mode === 'toc' ? 'toc' : 'none'),
  set: (mode: string) => {
    form.value.config.settings = { ...form.value.config.settings, folder_mode: mode }
  },
})

// toc_only is an admission filter, and the connector only reads the table of
// contents when folder_mode is 'toc' — under the flat layout the flag has no
// effect at all, which is why the control is disabled there.
const yuqueTOCOnly = computed({
  get: () => form.value.config.settings?.toc_only === true,
  set: (on: boolean) => {
    form.value.config.settings = { ...form.value.config.settings, toc_only: on }
  },
})

// Step 2: Resources
const resources = ref<Resource[]>([])
const loadingResources = ref(false)
const selectedResourceIds = ref<string[]>([])
const expandedResourceIds = ref(new Set<string>())
// Lazy loading: parents whose children have already been fetched, and parents
// currently being fetched. Used to load hierarchical sources (e.g. Feishu wiki)
// one level at a time instead of traversing the whole tree up front (#1672).
const loadedChildrenIds = ref(new Set<string>())
const loadingChildrenIds = ref(new Set<string>())
// True when the initial listing already returned the whole tree (connectors like
// Notion populate parent_id on the first call). In that case expanding a node
// never needs an extra request.
const treeFullyLoaded = ref(false)

// Drive (云盘) root input: the Drive connectors have no "list spaces" API, so
// the user must supply a root folder_token. We collect it here, write it into
// form.config.resource_ids as the single root, then loadResources lists its
// children. See 飞书云盘数据源设计.md §5.2 / ADR-0004.
const driveFolderToken = ref('')
// 必填校验的内联错误文案：非空时输入框显示 error 状态 + 下方 tips,
// 替代全局 MessagePlugin,与表单字段的就地校验风格一致。
const driveFolderTokenError = ref('')
const driveRootLoaded = ref(false)
const isDriveConnector = (type: string) => type === 'feishu_drive' || type === 'lark_drive'
const isGitLabConnector = (type: string) => type === 'gitlab'
// Seafile resource IDs are "<repo_id>:<path>"; one data source syncs one
// library, so the picker refuses a selection that spans two libraries.
const isSeafileConnector = (type: string) => type === 'seafile'
const seafileLibraryOf = (id: string) => id.split(':')[0]
// Seafile IDs encode the hierarchy, so a saved selection whose node has
// vanished from the tree can still be recognised as living under `parent`.
function seafileWithin(id: string, parent: string): boolean {
  const [repo, path] = [seafileLibraryOf(id), id.slice(id.indexOf(':') + 1)]
  const parentPath = parent.slice(parent.indexOf(':') + 1)
  return repo === seafileLibraryOf(parent) &&
    (parentPath === '/' || path === parentPath || path.startsWith(parentPath + '/'))
}

// DingTalk manual entry: two kinds of DingTalk content are readable by id but
// can never appear in the lazy-load tree — a document in the operator's personal
// space (GET /v2.0/wiki/workspaces returns team workspaces only) and a
// multi-dimensional table (no DingTalk API lists Bases at all). The user
// therefore pastes a link or an id, the way the Drive connectors take a
// folder_token. The kind cannot be inferred from the link: a document link and
// a Base link are byte-identical in shape
// (https://alidocs.dingtalk.com/i/nodes/<ID>), so the selector decides between
// the node= and base= reference forms.
const dingtalkManualId = ref('')
const dingtalkManualKind = ref<'node' | 'base'>('node')
const dingtalkManualError = ref('')
const isDingTalkConnector = (type: string) => type === 'dingtalk'
// Matches exactly what this block writes and nothing the tree writes: a node or
// Base reference carrying no workspace. Tree-picked ids always carry
// workspace=... (and usually ancestor=...), so they can never match.
const DINGTALK_MANUAL_REFERENCE = /^dingtalk:v1\?(?:node|base)=[^&]+$/
// DINGTALK_DESCRIBE_PARENT_PREFIX asks the connector to DESCRIBE one reference
// instead of listing its children. A manual selection is invisible to every
// listing — nothing enumerates Bases, and a personal-space node is absent from
// the workspace listing — so this is the only call that can turn the id the
// user pasted into the name the sync will use. It is a picker-only request form:
// the connector answers with one row for the reference itself, which is why it
// can share the resource endpoint with expansion without ever being confused
// for it.
const DINGTALK_DESCRIBE_PARENT_PREFIX = 'dingtalk:v1?describe='

// dingtalkManualRows holds the described row of every manual reference, keyed by
// the reference itself. The preview names its rows from it, so the two never
// disagree, and the rows are merged into the tree so an expandable reference (a
// Base, whose wiki children the connector lists) can be seen and expanded like
// any other node.
const dingtalkManualRows = ref<Record<string, Resource>>({})
// References whose description is in flight. Adding two ids in a row must not
// ask for the first one twice: the second call would not yet see it in
// dingtalkManualRows.
const dingtalkManualDescribePending = new Set<string>()

interface GitLabProjectInput { project_id: string; ref: string; pathsText: string }
const gitlabProjects = ref<GitLabProjectInput[]>([])
function syncGitLabProjectsToSettings() {
  if (!isGitLabConnector(form.value.type)) return
  form.value.config.settings.projects = gitlabProjects.value
    .filter(project => project.project_id.trim())
    .map(project => ({
      project_id: project.project_id.trim(), ref: project.ref.trim(),
      paths: project.pathsText.split(/[\n,]/).map(path => path.trim()).filter(Boolean),
    }))
}
function addGitLabProject() { gitlabProjects.value.push({ project_id: '', ref: '', pathsText: '' }) }
function removeGitLabProject(index: number) { gitlabProjects.value.splice(index, 1); syncGitLabProjectsToSettings() }

// extractDriveFolderToken accepts either a bare folder_token or a Drive folder
// URL (https://xxx.feishu.cn/drive/folder/<token> or the Lark equivalent
// https://xxx.larksuite.com/drive/folder/<token>) and returns the token.
// Matching is path-based, host-agnostic. Trims surrounding whitespace.
// Returns "" when nothing usable is found.
function extractDriveFolderToken(input: string): string {
  const raw = (input || '').trim()
  if (!raw) return ''
  // Bare token: no scheme, no slash - use as-is.
  if (!raw.includes('://') && !raw.includes('/')) return raw
  // URL form: extract the segment after /drive/folder/.
  const match = raw.match(/\/drive\/folder\/([^/?#]+)/)
  if (match && match[1]) return match[1]
  // Fallback: last path segment of a URL, or the raw string.
  try {
    const u = new URL(raw)
    const segs = u.pathname.split('/').filter(Boolean)
    return segs[segs.length - 1] || raw
  } catch {
    return raw
  }
}

// loadDriveRoot writes the user-supplied folder_token (or the token extracted
// from a pasted URL) as the root resource_id, then lists the root's children
// so the lazy-load tree can populate. On failure it classifies the error so the
// user gets an actionable hint (e.g. share the folder with the app) instead of
// a raw Feishu error body.
async function loadDriveRoot() {
  const token = extractDriveFolderToken(driveFolderToken.value)
  if (!token) {
    driveFolderTokenError.value = t('datasource.drive.folderTokenRequired')
    return
  }
  driveFolderTokenError.value = ''
  // Normalize the input so the user sees the extracted token, not the full URL.
  driveFolderToken.value = token
  form.value.config.resource_ids = [token]
  driveRootLoaded.value = false
  // The create below has the same contract as the picker's draft: without a
  // connector type or a knowledge base it is certain to be rejected, and there
  // is no data source to list the token through. Skipping it keeps the
  // placeholder instead of sending a request that cannot succeed.
  if (!tempDsId.value && !canCreateTempDataSource()) return
  loadingResources.value = true
  try {
    if (!tempDsId.value) {
      const res = await createDataSource({
        ...form.value,
        knowledge_base_id: props.kbId,
        status: 'paused',
      } as any)
      const created = res?.data || res
      tempDsId.value = created.id
    } else {
      // Edit mode OR a previously-created temp row: persist the new folder_token
      // so listResources sees the updated config. Previously this branch skipped
      // updates in edit mode, leaving listResources reading the old folder_token.
      await updateDataSource(tempDsId.value, {
        ...form.value,
        knowledge_base_id: props.kbId,
      } as any)
    }

    const res = await listResources(tempDsId.value)
    resources.value = res?.data || res || []
    if (resources.value.length > 0) {
      // Mirror loadResources' tree initialization: index parents that already
      // arrived with children and auto-expand them.
      const parentsWithChildren = new Set<string>()
      for (const r of resources.value) {
        if (r.parent_id) parentsWithChildren.add(r.parent_id)
      }
      loadedChildrenIds.value = parentsWithChildren
      loadingChildrenIds.value = new Set<string>()
      treeFullyLoaded.value = parentsWithChildren.size > 0
      expandedResourceIds.value = new Set(
        resources.value
          .filter(r => !r.parent_id && r.has_children && parentsWithChildren.has(r.external_id))
          .map(r => r.external_id),
      )
      driveRootLoaded.value = true
      // In edit mode, reveal pre-existing selections that live below the
      // (not-yet-expanded) tree so they are visible and checked - mirrors
      // loadResources' behavior for non-Drive connectors.
      if (isEdit.value && !treeFullyLoaded.value) {
        const loaded = new Set(resources.value.map(r => r.external_id))
        const hidden = selectedResourceIds.value.filter(id => !loaded.has(id))
        if (hidden.length > 0) void revealExistingSelections(hidden)
      }
    }
  } catch (e: any) {
    MessagePlugin.error(classifyDriveLoadError(e))
  }
  loadingResources.value = false
}

// classifyDriveLoadError turns a raw Drive list error into an actionable i18n
// message. The Feishu list API returns 403 with code=1061004 when the app has
// not been shared the target folder; without this the user sees "forbidden"
// and has no idea what to do.
function classifyDriveLoadError(e: any): string {
  const raw = String(e?.message || e?.error || '')
  const lower = raw.toLowerCase()
  // 403 / forbidden / 1061004 -> the app lacks access to this specific folder;
  // the user must share it with the app's group in Feishu Drive.
  if (
    lower.includes('status=403') ||
    lower.includes('forbidden') ||
    lower.includes('"code":1061004') ||
    lower.includes('code=1061004')
  ) {
    return t('datasource.drive.loadForbiddenHint')
  }
  // 401 / auth -> app credentials wrong or app lacks the drive scopes.
  if (lower.includes('status=401') || lower.includes('auth') || lower.includes('1061005')) {
    return t('datasource.drive.loadAuthHint')
  }
  // Invalid / not-found folder_token.
  if (lower.includes('1061003') || lower.includes('not found')) {
    return t('datasource.drive.loadNotFoundHint')
  }
  return raw || t('datasource.resourceLoadFailed')
}

// extractDingTalkId accepts a bare node/Base id or a DingTalk link
// (https://alidocs.dingtalk.com/i/nodes/<ID>, with or without a query string
// such as ?utm_scene=...) and returns the id. Matching is path-based, so any
// host works. Unlike the Drive extractor there is deliberately no "last path
// segment" fallback: a document link and a Base link are byte-identical, so an
// id guessed out of an unrecognised URL would be more likely wrong than right.
// Returns "" when nothing usable is found.
function extractDingTalkId(input: string): string {
  const raw = (input || '').trim()
  if (!raw) return ''
  let id = ''
  if (!raw.includes('://') && !raw.includes('/')) {
    // Bare id: no scheme, no slash - use as-is.
    id = raw
  } else {
    const match = raw.match(/\/i\/nodes\/([^/?#]+)/)
    if (match && match[1]) id = match[1]
  }
  // A character that would split or truncate the resource query cannot be part
  // of a DingTalk id; accepting one would store a reference the backend can
  // never decode, so it is reported as "nothing usable" instead.
  return /[\s&=?#]/.test(id) ? '' : id
}

// dingtalkManualReference renders the resource_id for one manual entry. The
// kind is what separates a wiki node (dingtalk:v1?node=<id>) from an
// independent multi-dimensional table (dingtalk:v1?base=<id>); the backend
// rejects a reference that mixes the two forms.
function dingtalkManualReference(id: string, kind: 'node' | 'base'): string {
  return kind === 'base' ? `dingtalk:v1?base=${id}` : `dingtalk:v1?node=${id}`
}

// dingtalkManualReferences lists the manually entered selections. They are not
// in the tree, so the tree itself can never show or uncheck them; this is what
// the entry block renders and what the selection count adds in.
const dingtalkManualReferences = computed(() =>
  selectedResourceIds.value.filter(id => DINGTALK_MANUAL_REFERENCE.test(id)),
)

// applyDingTalkManualEntry adds the entered id to the selection as the reference
// the backend accepts for the chosen kind, and leaves the field holding that
// entry. Returns false with the inline error set when nothing usable was
// entered, so a caller advancing the wizard can stay on the step.
function applyDingTalkManualEntry(): boolean {
  const id = extractDingTalkId(dingtalkManualId.value)
  if (!id) {
    // Nothing usable was entered, so the input is kept exactly as typed: it is
    // the text the user has to correct.
    dingtalkManualError.value = t('datasource.dingtalk.manualIdRequired')
    return false
  }
  dingtalkManualError.value = ''
  // The committed entry stays in the field, normalised: a pasted link is
  // replaced by the bare id it carried, so the box shows exactly what was added
  // and can be read or reused without retyping. Adding it again, or leaving the
  // step with it still in the box, does not duplicate anything — the selection
  // is a cover set, so one reference is one entry.
  dingtalkManualId.value = id
  const cover = new Set(selectedResourceIds.value)
  cover.add(dingtalkManualReference(id, dingtalkManualKind.value))
  selectedResourceIds.value = [...cover]
  // Keep the form's own copy in step: the paused data source created for the
  // tree is built from it, and it is what the final submit sends.
  form.value.config.resource_ids = [...selectedResourceIds.value]
  // Ask the connector what the new reference is called; the row shows the id
  // until that answer arrives (or forever, if it never does).
  void resolveDingTalkManualNames()
  return true
}

// resolveDingTalkManualNames turns each manual reference into a described row:
// the connector reads the node (or Base) by id and reports its display name,
// which is exactly the name the sync will title the item with. It is the only
// call the collapsed presentation makes — never the tree listing — and every
// failure degrades to the id already on screen, because a name lookup must not
// be able to break the step.
async function resolveDingTalkManualNames() {
  if (!isDingTalkConnector(form.value.type)) return
  const known = new Set(Object.keys(dingtalkManualRows.value))
  const pending = dingtalkManualReferences.value.filter(
    id => !known.has(id) && !dingtalkManualDescribePending.has(id),
  )
  if (pending.length === 0) return
  let dsId = ''
  try {
    // A description is a call against the data source, so the draft the picker
    // lists through has to exist first — the same draft the tree would create.
    dsId = await ensureTempDataSource()
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.resourceLoadFailed'))
    return
  }
  // No draft, no description: the rows keep the ids the user typed, and no
  // listing is attempted (a describe against an empty id is certain to fail).
  if (!dsId) return
  for (const id of pending) dingtalkManualDescribePending.add(id)
  await Promise.all(pending.map(async (id) => {
    try {
      const res = await listResources(dsId, DINGTALK_DESCRIBE_PARENT_PREFIX + id)
      const rows: Resource[] = res?.data || res || []
      // The connector echoes the reference it described, so an unexpected row
      // is ignored rather than shown under the wrong name; a reference removed
      // while the lookup was in flight is dropped for the same reason.
      const row = rows.find(r => r.external_id === id)
      if (row && dingtalkManualReferences.value.includes(id)) {
        dingtalkManualRows.value = { ...dingtalkManualRows.value, [id]: row }
      }
    } catch {
      // The id stays on screen; nothing else about the step depends on this.
    } finally {
      dingtalkManualDescribePending.delete(id)
    }
  }))
  mergeDingTalkManualRowsIntoTree()
}

// mergeDingTalkManualRowsIntoTree adds the described rows to the tree, where
// they act as roots: a manual selection belongs to no listed parent, and a Base
// needs its row in the tree for its children to be reachable at all. The rows
// are merged, not replaced, and re-merged after every listing because
// loadResources swaps the whole array.
function mergeDingTalkManualRowsIntoTree() {
  const rows = Object.values(dingtalkManualRows.value)
  if (rows.length === 0) return
  const existing = new Set(resources.value.map(r => r.external_id))
  const missing = rows.filter(row => !existing.has(row.external_id))
  if (missing.length === 0) return
  resources.value = [...resources.value, ...missing]
}

// removeDingTalkManualReference drops one manual entry from the selection. Only
// ids this block wrote can be passed here, so the tree selections are untouched.
// The described row goes with it: a row left behind would sit in the tree
// unchecked, as if it were still part of the data source.
function removeDingTalkManualReference(reference: string) {
  selectedResourceIds.value = selectedResourceIds.value.filter(id => id !== reference)
  form.value.config.resource_ids = [...selectedResourceIds.value]
  if (!(reference in dingtalkManualRows.value)) return
  const remaining = { ...dingtalkManualRows.value }
  delete remaining[reference]
  dingtalkManualRows.value = remaining
  resources.value = resources.value.filter(r => r.external_id !== reference)
}

// dingtalkManualReferenceId reads the stored id back out of a manual reference,
// so the selection row shows the id the user entered rather than the raw
// resource_id.
function dingtalkManualReferenceId(reference: string): string {
  return reference.slice(reference.indexOf('=') + 1)
}

// dingtalkManualKindLabel names what a stored manual reference points at
// (多维表 / 文档节点) without decoding the resource id by hand.
function dingtalkManualKindLabel(reference: string): string {
  return t(reference.startsWith('dingtalk:v1?base=')
    ? 'datasource.dingtalk.manualKindBase'
    : 'datasource.dingtalk.manualKindNode')
}

// DingTalk is the only connector whose selectable content is partly invisible to
// the tree: a personal-space document and a 多维表 Base are readable by id but
// nothing enumerates them, so `revealExistingSelections` has nothing to reveal
// for those refs and the picker would fall back to the bare root list (every
// workspace the tenant exposes). The selection area therefore previews the
// real selection — manual references and tree picks together — instead of
// relying on the tree to show it.
interface DingTalkSelectionRow {
  id: string
  manual: boolean
  kind: string
  label: string
}

const dingtalkSelectionRows = computed<DingTalkSelectionRow[]>(() => {
  const byId = resourceById.value
  return selectedResourceIds.value.map((id) => {
    if (DINGTALK_MANUAL_REFERENCE.test(id)) {
      // The described row carries the same name the sync puts on the item; the
      // id stays the fallback whenever the name could not be resolved, so the
      // row is never empty and never wrong.
      const described = dingtalkManualRows.value[id]
      return {
        id,
        manual: true,
        kind: dingtalkManualKindLabel(id),
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
        ? resourceTypeLabel(resource.type)
        : t('datasource.dingtalk.selectionTreeKind'),
      label: resource?.name || id,
    }
  })
})

// resourceById is the lookup behind every per-row fact the preview states about
// a selection — what it syncs, whether it can be expanded, which children it
// has. Rows are keyed by external_id, which is also what a selection stores, so
// a manual reference and a tree pick resolve the same way.
const resourceById = computed(() => new Map(resources.value.map(r => [r.external_id, r])))

// dingtalkSelectedResource returns the row a preview line stands for: the
// connector's described row for a manual reference (the only answer a Base or a
// personal-space node ever has) or the row a listing delivered. Reading one
// lookup keeps the name, the hint and the expansion from disagreeing.
function dingtalkSelectedResource(id: string): Resource | undefined {
  return dingtalkManualRows.value[id] || resourceById.value.get(id)
}

// dingtalkSelectionRowExpandable reports whether a row may offer a disclosure
// control. Only a row the connector reports as having children gets one: a leaf
// has nothing to list, so an expander there could only promise content that does
// not exist (and, before the connector answered a leaf with an empty listing,
// could only fail).
function dingtalkSelectionRowExpandable(row: DingTalkSelectionRow): boolean {
  return dingtalkSelectedResource(row.id)?.has_children === true
}

// dingtalkSelectionRowChildren are the direct children of an expanded row. They
// are the same rows the lazy tree renders: children fetched through
// listResources(parent_id=<ref>) and indexed by their parent_id, so a child
// shown here and the same child shown in the tree are one row, not two copies.
function dingtalkSelectionRowChildren(id: string): Resource[] {
  return childrenMap.value.get(id) || []
}

function dingtalkSelectionRowExpanded(id: string): boolean {
  return expandedResourceIds.value.has(id)
}

function dingtalkSelectionRowLoading(id: string): boolean {
  return loadingChildrenIds.value.has(id)
}

// dingtalkSelectionRowHint is the one-line answer to "what does this row sync",
// rendered as part of the row so it is readable without expanding anything. Only
// a Base has one: its tables are the content a base= reference ingests, while
// the documents listed under it are separate selections.
function dingtalkSelectionRowHint(row: DingTalkSelectionRow): string {
  const described = dingtalkSelectedResource(row.id)
  return described ? resourceHint(described) : ''
}

// dingtalkTableNames splits the table list the connector puts in a Base row's
// description — notableTableNames joins the names with ", " in listing order.
// The picker re-renders that same list with a separator a table line reads
// better with; it never invents, reorders or drops a name.
function dingtalkTableNames(description: string): string[] {
  return (description || '').split(',').map(name => name.trim()).filter(Boolean)
}

// removeTreeSelectionRow drops one tree-picked resource from the preview. It
// filters the cover set directly instead of going through toggleResource: a
// saved node the lazy tree has not loaded has no check state yet, so
// toggleResource would check it again rather than remove it.
function removeTreeSelectionRow(id: string) {
  selectedResourceIds.value = selectedResourceIds.value.filter(x => x !== id)
  form.value.config.resource_ids = [...selectedResourceIds.value]
}

// hasDingTalkManualSelection is true once at least one manual reference is in
// the selection. Everything below keys off it, so a user who never touches the
// manual block keeps the exact tree-only presentation.
const hasDingTalkManualSelection = computed(() => dingtalkManualReferences.value.length > 0)

// dingtalkTreeExpanded is the explicit expander state of the knowledge-base
// tree while manual references exist. The picker is collapsed by default then:
// the selection area is the preview of what will sync, so the team's knowledge
// bases must not sit next to one pasted 多维表 as if they were part of it.
// Expanding changes nothing about the selection itself.
const dingtalkTreeExpanded = ref(false)

// dingtalkTreeLoaded records that the knowledge-base tree has really been
// fetched. The tree is listed lazily — the first time the expander is opened —
// so a data source whose only selection is a pasted 多维表 never asks for the
// knowledge bases it will not use. Collapsing and re-expanding must not fetch
// again, which is what this flag answers.
const dingtalkTreeLoaded = ref(false)

// showDingTalkResourceTree gates the tree rows (toolbar + list). Without a
// manual reference it is always true, which is the pre-existing behaviour for
// every connector, DingTalk included: a tree-only user gets the tree open by
// default and never sees the expander row.
const showDingTalkResourceTree = computed(() =>
  !hasDingTalkManualSelection.value || dingtalkTreeExpanded.value,
)

function toggleDingTalkResourceTree() {
  dingtalkTreeExpanded.value = !dingtalkTreeExpanded.value
  // The tree is filled on first use, not when the step opens: until this point
  // there was no tree to fill. The promise is returned so a caller (and a test)
  // can tell when that first listing has settled.
  if (dingtalkTreeExpanded.value && !dingtalkTreeLoaded.value) return loadResources()
}

// Adding the first manual reference collapses the tree again, so "collapsed by
// default whenever a manual reference is selected" also holds after the user
// expanded the tree once before adding the reference. Removing the last manual
// reference restores the tree-only presentation on its own — and with it the
// tree's own listing, which was skipped while the preview replaced it.
watch(
  () => dingtalkManualReferences.value.length,
  (count, previous) => {
    if (count > 0 && previous === 0) dingtalkTreeExpanded.value = false
    if (count === 0 && previous > 0 && !dingtalkTreeLoaded.value) void loadResources()
  },
)

// Shared children/parent indexes — used by tree rendering and selection logic
const childrenMap = computed(() => {
  const map = new Map<string, Resource[]>()
  for (const r of resources.value) {
    if (r.parent_id) {
      const siblings = map.get(r.parent_id)
      if (siblings) siblings.push(r)
      else map.set(r.parent_id, [r])
    }
  }
  return map
})

const parentMap = computed(() => {
  const map = new Map<string, string>()
  for (const r of resources.value) {
    if (r.parent_id) map.set(r.external_id, r.parent_id)
  }
  return map
})

// `selectedResourceIds` is a MINIMAL COVER SET: only the roots of fully-selected
// subtrees. Sending this to the backend gives "sync these IDs and all descendants"
// semantics — including any pages added later under a selected parent.
type CheckState = 'checked' | 'indeterminate' | 'unchecked'

const checkStates = computed(() => {
  const states = new Map<string, CheckState>()
  const cover = new Set(selectedResourceIds.value)

  // Single post-order walk: a node is `checked` if itself or any ancestor is
  // in the cover set; otherwise `indeterminate` if any descendant is checked;
  // otherwise `unchecked`. Returns whether the subtree contains a checked node.
  function walk(node: Resource, ancestorChecked: boolean): boolean {
    const selfChecked = ancestorChecked || cover.has(node.external_id)
    let descendantChecked = false
    for (const c of childrenMap.value.get(node.external_id) || []) {
      if (walk(c, selfChecked)) descendantChecked = true
    }
    if (selfChecked) states.set(node.external_id, 'checked')
    else states.set(node.external_id, descendantChecked ? 'indeterminate' : 'unchecked')
    return selfChecked || descendantChecked
  }
  for (const r of resources.value) {
    if (!r.parent_id) walk(r, false)
  }
  return states
})

function toggleExpand(id: string) {
  const next = new Set(expandedResourceIds.value)
  if (next.has(id)) {
    next.delete(id)
    expandedResourceIds.value = next
    return
  }
  next.add(id)
  expandedResourceIds.value = next
  void ensureChildrenLoaded(id)
}

// ensureChildrenLoaded fetches the direct children of a node on demand. It is a
// no-op when the connector already delivered the whole tree in one call (e.g.
// Notion) or when this node's children have already been fetched.
async function ensureChildrenLoaded(id: string) {
  if (!tempDsId.value) return
  if (loadedChildrenIds.value.has(id) || loadingChildrenIds.value.has(id)) return
  if (treeFullyLoaded.value) {
    loadedChildrenIds.value = new Set(loadedChildrenIds.value).add(id)
    return
  }

  loadingChildrenIds.value = new Set(loadingChildrenIds.value).add(id)
  try {
    const res = await listResources(tempDsId.value, id)
    const children: Resource[] = res?.data || res || []
    if (children.length > 0) {
      const existing = new Set(resources.value.map(r => r.external_id))
      const merged = resources.value.slice()
      for (const c of children) {
        if (!existing.has(c.external_id)) merged.push(c)
      }
      resources.value = merged
    } else {
      // Connectors may conservatively advertise HasChildren to avoid an N+1
      // probe. Once lazy loading proves this is a leaf, collapse it and retain
      // that fact so another expand cannot trigger another empty request.
      resources.value = resources.value.map(r => r.external_id === id
        ? { ...r, has_children: false }
        : r)
      const expanded = new Set(expandedResourceIds.value)
      expanded.delete(id)
      expandedResourceIds.value = expanded
    }
    loadedChildrenIds.value = new Set(loadedChildrenIds.value).add(id)
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.resourceLoadFailed'))
    // Collapse again so the user can retry the expand.
    const next = new Set(expandedResourceIds.value)
    next.delete(id)
    expandedResourceIds.value = next
  } finally {
    const s = new Set(loadingChildrenIds.value)
    s.delete(id)
    loadingChildrenIds.value = s
  }
}

const visibleTree = computed(() => {
  const roots = resources.value.filter(r => !r.parent_id)
  const result: { resource: Resource; depth: number; noticeAfter?: boolean }[] = []
  function walk(items: Resource[], depth: number) {
    for (const r of items) {
      result.push({ resource: r, depth })
      if (r.has_children && expandedResourceIds.value.has(r.external_id)) {
        walk(childrenMap.value.get(r.external_id) || [], depth + 1)
      }
      // Keep this visible after an empty root listing is recognized as a leaf.
      if (
        r.metadata?.hierarchy_limitation === 'cloud_top_level_containers' &&
        (expandedResourceIds.value.has(r.external_id) ||
          (!r.has_children && loadedChildrenIds.value.has(r.external_id)))
      ) {
        result.push({ resource: r, depth: depth + 1, noticeAfter: true })
      }
    }
  }
  walk(roots, 0)
  return result
})

// Connection test
const testing = ref(false)
const testResult = ref<'success' | 'error' | ''>('')
const testErrorMsg = ref('')

// Collapsible prereq in Step 1
const prereqExpanded = ref(false)


// Temp data source for resource listing
const tempDsId = ref('')

// Schedule presets
const schedulePresets = computed(() => [
  { label: t('datasource.schedule30min'), value: '0 */30 * * * *' },
  { label: t('datasource.schedule1h'), value: '0 0 * * * *' },
  { label: t('datasource.schedule6h'), value: '0 0 */6 * * *' },
  { label: t('datasource.schedule12h'), value: '0 0 */12 * * *' },
  { label: t('datasource.schedule24h'), value: '0 0 2 * * *' },
])

// --- Connector definitions ---
interface ConnectorDef {
  type: string
  available: boolean
  docUrl: string
  permissionDocUrl: string
  permissionPageUrl: string
  requiredPermissions: string[]
  fields: {
    key: string
    labelKey: string
    placeholder: string
    secret?: boolean
    optional?: boolean
    hintKey?: string
    multiline?: boolean
    fieldType?: 'custom_headers'
  }[]
}

const connectorDefs = computed<ConnectorDef[]>(() => [
  {
    type: 'feishu',
    available: true,
    docUrl: 'https://open.feishu.cn/app',
    permissionDocUrl: 'https://open.feishu.cn/document/server-docs/docs/wiki-v2/wiki-overview',
    permissionPageUrl: 'https://open.feishu.cn/app',
    requiredPermissions: [
      'wiki:wiki:readonly',
      'drive:drive:readonly',
      'drive:export:readonly',
      'docx:document:readonly',
    ],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      { key: 'base_url', labelKey: 'datasource.field.baseUrl', placeholder: 'https://open.feishu.cn', optional: true, hintKey: 'datasource.field.baseUrlHint' },
    ],
  },
  {
    // Lark is Feishu's international cloud. Same wiki/docx/drive APIs and the
    // same scope identifiers, but a separate console, tenant and app — an app
    // created on open.feishu.cn cannot read a Lark wiki.
    type: 'lark',
    available: true,
    docUrl: 'https://open.larksuite.com/app',
    permissionDocUrl: 'https://open.larksuite.com/document/server-docs/docs/wiki-v2/wiki-overview',
    permissionPageUrl: 'https://open.larksuite.com/app',
    requiredPermissions: [
      'wiki:wiki:readonly',
      'drive:drive:readonly',
      'drive:export:readonly',
      'docx:document:readonly',
    ],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      { key: 'base_url', labelKey: 'datasource.field.baseUrl', placeholder: 'https://open.feishu.cn', optional: true, hintKey: 'datasource.field.baseUrlHint' },
    ],
  },
  {
    // Feishu Drive (云盘) mode: sync documents/files under a user-supplied Drive
    // folder_token. Same auth as the wiki connector but no wiki:wiki:readonly
    // scope - Drive only needs drive + export + docx.
    type: 'feishu_drive',
    available: true,
    docUrl: 'https://open.feishu.cn/app',
    permissionDocUrl: 'https://open.feishu.cn/document/server-docs/docs/drive-v1/file/list',
    permissionPageUrl: 'https://open.feishu.cn/app',
    requiredPermissions: [
      'drive:drive:readonly',
      'drive:export:readonly',
      'docx:document:readonly',
    ],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      { key: 'base_url', labelKey: 'datasource.field.baseUrl', placeholder: 'https://open.feishu.cn', optional: true, hintKey: 'datasource.field.baseUrlHint' },
    ],
  },
  {
    // Lark Drive: international counterpart of feishu_drive.
    type: 'lark_drive',
    available: true,
    docUrl: 'https://open.larksuite.com/app',
    permissionDocUrl: 'https://open.larksuite.com/document/server-docs/docs/drive-v1/file/list',
    permissionPageUrl: 'https://open.larksuite.com/app',
    requiredPermissions: [
      'drive:drive:readonly',
      'drive:export:readonly',
      'docx:document:readonly',
    ],
    fields: [
      { key: 'app_id', labelKey: 'datasource.field.appId', placeholder: 'cli_xxxx' },
      { key: 'app_secret', labelKey: 'datasource.field.appSecret', placeholder: '', secret: true },
      { key: 'base_url', labelKey: 'datasource.field.baseUrl', placeholder: 'https://open.larksuite.com', optional: true, hintKey: 'datasource.field.baseUrlHint' },
    ],
  },
  {
    type: 'notion',
    available: true,
    docUrl: 'https://www.notion.so/my-integrations',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      { key: 'api_key', labelKey: 'datasource.field.integrationToken', placeholder: 'ntn_xxxx', secret: true },
    ],
  },
  {
    type: 'confluence',
    available: true,
    docUrl: 'https://developer.atlassian.com/cloud/confluence/rest/',
    permissionDocUrl: 'https://developer.atlassian.com/cloud/confluence/rest/',
    permissionPageUrl: 'https://id.atlassian.com/manage-profile/security/api-tokens',
    requiredPermissions: [],
    fields: [
      { key: 'base_url', labelKey: 'datasource.field.confluenceBaseUrl', placeholder: 'https://confluence.example.com or https://team.atlassian.net/wiki' },
      { key: 'username', labelKey: 'datasource.field.confluenceUsername', placeholder: 'name or email' },
      { key: 'password', labelKey: 'datasource.field.confluencePassword', placeholder: 'Server/DC password', secret: true },
      { key: 'api_token', labelKey: 'datasource.field.confluenceApiToken', placeholder: 'Cloud API token', secret: true },
    ],
  },
  {
    type: 'yuque',
    available: true,
    docUrl: 'https://www.yuque.com/yuque/developer/api',
    permissionDocUrl: 'https://www.yuque.com/yuque/developer/api',
    permissionPageUrl: 'https://www.yuque.com/settings/tokens',
    requiredPermissions: [
      'repo:read',
      'doc:read',
    ],
    fields: [
      { key: 'api_token', labelKey: 'datasource.field.apiToken', placeholder: '', secret: true },
      { key: 'base_url', labelKey: 'datasource.field.baseUrl', placeholder: 'https://www.yuque.com', optional: true, hintKey: 'datasource.field.baseUrlHint' },
    ],
  },
  {
    type: 'dingtalk',
    available: true,
    docUrl: 'https://open.dingtalk.com/document/development/knowledge-base-overview',
    permissionDocUrl: 'https://open.dingtalk.com/document/development/get-knowledge-base-list',
    permissionPageUrl: 'https://open-dev.dingtalk.com/',
    requiredPermissions: [
      'Wiki.Workspace.Read',
      'Wiki.Node.Read',
      'Storage.File.Read',
    ],
    fields: [
      { key: 'client_id', labelKey: 'datasource.field.clientId', placeholder: 'dingxxxxxxxx' },
      { key: 'client_secret', labelKey: 'datasource.field.clientSecret', placeholder: '', secret: true },
      { key: 'operator_id', labelKey: 'datasource.field.operatorId', placeholder: '', hintKey: 'datasource.field.operatorIdHint' },
    ],
  },
  {
    // Tencent IMA (ima.qq.com). Uses the OpenAPI at /openapi/wiki/v1 with two
    // static headers (ima-openapi-clientid + ima-openapi-apikey); no OAuth.
    type: 'ima',
    available: true,
    docUrl: 'https://ima.qq.com/agent-interface',
    permissionDocUrl: 'https://ima.qq.com/agent-interface',
    permissionPageUrl: 'https://ima.qq.com/agent-interface',
    requiredPermissions: [],
    fields: [
      { key: 'client_id', labelKey: 'datasource.field.imaClientId', placeholder: '', secret: true },
      { key: 'api_key', labelKey: 'datasource.field.imaApiKey', placeholder: '', secret: true },
      { key: 'base_url', labelKey: 'datasource.field.baseUrl', placeholder: 'https://ima.qq.com', optional: true, hintKey: 'datasource.field.baseUrlHint' },
    ],
  },
  {
    type: 'rss',
    available: true,
    docUrl: '',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      { key: 'auth_headers', labelKey: 'datasource.field.authHeaders', placeholder: '', optional: true, hintKey: 'datasource.field.authHeadersHint', fieldType: 'custom_headers' },
    ],
  },
  {
    type: 'gitlab', available: true, docUrl: '', permissionDocUrl: '', permissionPageUrl: '', requiredPermissions: [],
    fields: [
      { key: 'base_url', labelKey: 'datasource.gitlab.baseUrl', placeholder: 'https://gitlab.example.com' },
      { key: 'access_token', labelKey: 'datasource.gitlab.accessToken', placeholder: '', secret: true },
    ],
  },
  {
    type: 'seafile', available: true, docUrl: 'https://help.seafile.com/',
    permissionDocUrl: '', permissionPageUrl: '', requiredPermissions: [],
    fields: [
      { key: 'base_url', labelKey: 'datasource.seafile.baseUrl', placeholder: 'https://seafile.example.com' },
      { key: 'api_token', labelKey: 'datasource.seafile.apiToken', placeholder: '', secret: true, hintKey: 'datasource.seafile.apiTokenHint' },
    ],
  },
])


const currentDef = computed(() => connectorDefs.value.find(d => d.type === form.value.type))

const displayedCredentialFields = computed(() => {
  const fields = currentDef.value?.fields || []
  if (form.value.type !== "confluence") return fields

  return fields.filter((field) => {
    if (field.key === "password") return form.value.config.credentials.edition !== "cloud"
    if (field.key === "api_token") return form.value.config.credentials.edition === "cloud"
    return true
  })
})

// --- Drawer lifecycle ---
watch(visible, async (v) => {
  if (!v) {
    if (!isEdit.value && tempDsId.value) {
      try {
        await deleteDataSource(tempDsId.value)
      } catch {
        // Ignore cleanup errors
      }
      tempDsId.value = ''
    }
    return
  }
  step.value = isEdit.value ? 1 : 0
  testResult.value = ''
  testErrorMsg.value = ''
  tempDsId.value = ''
  prereqExpanded.value = false
  pendingRemoveCredentials.value = false
  resources.value = []
  selectedResourceIds.value = []
  expandedResourceIds.value = new Set()
  loadedChildrenIds.value = new Set()
  loadingChildrenIds.value = new Set()
  treeFullyLoaded.value = false
  driveFolderToken.value = ''
  driveFolderTokenError.value = ''
  driveRootLoaded.value = false
  dingtalkManualId.value = ''
  dingtalkManualKind.value = 'node'
  dingtalkManualError.value = ''
  // Fresh open: the tree starts collapsed again if a saved manual reference is
  // restored below, and is not left expanded from a previous edit session.
  // Nothing about the tree has been fetched yet, and no reference has been
  // described: both are learned lazily, on the step that needs them.
  dingtalkTreeExpanded.value = false
  dingtalkTreeLoaded.value = false
  dingtalkManualRows.value = {}
  rssAuthHeaders.value = []
  gitlabProjects.value = []

  if (isEdit.value && props.dataSource) {
    // Reset edit/replace toggle every open so an aborted replace doesn't
    // carry over. credentialsConfigured will be refreshed from the
    // /credentials subresource (run separately below).
    replaceCredentialsMode.value = false
    credentialsConfigured.value = false
    refreshCredentialsStatus()
    testResult.value = credentialsConfigured.value ? 'success' : ''
    const editConfig = props.dataSource.config || {}
    form.value = {
      name: props.dataSource.name,
      type: props.dataSource.type,
      config: {
        credentials: {},
        resource_ids: editConfig.resource_ids || [],
        settings: props.dataSource.type === 'rss'
          ? hydrateRssFeedUrlsFromConfig(editConfig)
          : (editConfig.settings || {}),
      },
      sync_schedule: props.dataSource.sync_schedule,
      sync_mode: props.dataSource.sync_mode,
      conflict_strategy: props.dataSource.conflict_strategy,
      sync_deletions: props.dataSource.sync_deletions,
    }
    selectedResourceIds.value = form.value.config?.resource_ids || []
    if (isGitLabConnector(form.value.type)) {
      const savedProjects = Array.isArray(form.value.config.settings.projects) ? form.value.config.settings.projects : []
      gitlabProjects.value = savedProjects.map((project: any) => ({
        project_id: String(project.project_id || ''), ref: String(project.ref || ''),
        pathsText: Array.isArray(project.paths) ? project.paths.join('\n') : '',
      }))
    }
    // Pre-fill the Drive root folder_token from the saved resource_ids so the
    // user sees what they previously entered. driveRootLoaded stays false: the
    // tree has not been listed yet, and clicking "load" triggers listResources
    // + revealExistingSelections so pre-existing selections are revealed.
    if (isDriveConnector(form.value.type)) {
      const rids = form.value.config?.resource_ids || []
      if (rids.length > 0) {
        // resource_id is "folderToken" or "folderToken:fileToken"; the root is
        // the first segment.
        driveFolderToken.value = rids[0].split(':')[0]
      }
    }
    // Pre-fill the DingTalk manual entry from a saved manual selection:
    // selectedResourceIds already carries it, so the tree never shows it and
    // this is the only place the user can see (or remove) what was saved.
    if (isDingTalkConnector(form.value.type)) {
      const manual = (form.value.config?.resource_ids || [])
        .find(id => DINGTALK_MANUAL_REFERENCE.test(id))
      if (manual) {
        dingtalkManualKind.value = manual.startsWith('dingtalk:v1?base=') ? 'base' : 'node'
        dingtalkManualId.value = manual.slice(manual.indexOf('=') + 1)
      }
    }
    tempDsId.value = props.dataSource.id
  } else {
    replaceCredentialsMode.value = false
    credentialsConfigured.value = false
    form.value = {
      name: '',
      type: '',
      config: { credentials: {}, resource_ids: [], settings: {} },
      sync_schedule: '0 0 */6 * * *',
      sync_mode: 'incremental',
      conflict_strategy: 'overwrite',
      sync_deletions: true,
    }
  }
})

watch(
  () => form.value.config.credentials,
  () => {
    if (needsConnectionTest()) {
      testResult.value = ''
      testErrorMsg.value = ''
    }
  },
  { deep: true },
)

watch(
  rssAuthHeaders,
  () => {
    syncRssAuthHeadersToCredentials()
    if (needsConnectionTest()) {
      testResult.value = ''
      testErrorMsg.value = ''
    }
  },
  { deep: true },
)

watch(
  () => form.value.config.settings.feed_urls,
  () => {
    if (needsConnectionTest()) {
      testResult.value = ''
      testErrorMsg.value = ''
    }
  },
)

function selectType(def: ConnectorDef) {
  if (!def.available) return
  form.value.type = def.type
  form.value.name = t(`datasource.connector.${def.type}`)
  form.value.config.credentials = def.type === "confluence" ? { edition: "server" } : {}
  if (def.type === 'confluence') {
    form.value.config.settings = { ...form.value.config.settings, edition: 'server' }
  }
  // A new Yuque source opts into the book's folder hierarchy. Only on create:
  // an existing source keeps what it was built with, so the "folder layout is
  // owned by the connector" caveat never applies to sources that predate it.
  if (def.type === 'yuque' && !isEdit.value) {
    form.value.config.settings = { ...form.value.config.settings, folder_mode: 'toc' }
  }
  if (isGitLabConnector(def.type)) addGitLabProject()
  rssAuthHeaders.value = []
  step.value = 1
}

// --- Test connection ---
async function testConnection() {
  syncRssAuthHeadersToCredentials()
  syncConfluencePublicFieldsToSettings()
  if (!validateRssFeedUrls()) return
  if (!isEdit.value || !credentialsConfigured.value || replaceCredentialsMode.value) {
    const fields = displayedCredentialFields.value
    for (const f of fields) {
      if (f.optional || f.fieldType === 'custom_headers') continue
      if (!form.value.config.credentials[f.key]) {
        MessagePlugin.warning(`${t(f.labelKey)} ${t('datasource.isRequired')}`)
        return
      }
    }
  }

  testing.value = true
  testResult.value = ''
  testErrorMsg.value = ''
  try {
    // The main update endpoint ignores credentials. Only use the saved
    // connection when keeping its credentials; test replacements directly
    // without persisting them until the user saves the data source.
    if (isEdit.value && tempDsId.value && !needsConnectionTest()) {
      await updateDataSource(tempDsId.value, {
        ...form.value,
        knowledge_base_id: props.kbId,
      } as any)
      await validateConnection(tempDsId.value)
    } else {
      const creds = { ...form.value.config.credentials }
      if (form.value.type === 'rss') {
        // validate-credentials is credentials-only; feed URLs live in settings.
        creds.feed_urls = form.value.config.settings.feed_urls
      }
      await validateCredentials(form.value.type, creds)
    }
    testResult.value = 'success'
    MessagePlugin.success(t('datasource.testSuccess'))
  } catch (e: any) {
    testResult.value = 'error'
    testErrorMsg.value = e?.message || e?.error || ''
    MessagePlugin.error(t('datasource.testFailed'))
  }
  testing.value = false
}

// --- Load resources ---

// canCreateTempDataSource reports whether the create endpoint could accept a
// request built from the form as it stands. The endpoint rejects a request
// without a knowledge base (ErrKnowledgeBaseNotFound) and one whose type is not
// a registered connector (ErrConnectorNotFound) before the connector is ever
// reached, so a request missing either is certain to fail and must not be sent.
// The one thing left that the endpoint checks — the credentials a connector
// validates against the remote service — can only be answered by making the
// call, so it is deliberately not pre-empted here.
function canCreateTempDataSource(): boolean {
  return (props.kbId || '').trim() !== '' && String(form.value.type || '').trim() !== ''
}

// ensureTempDataSource returns the data source the picker lists through,
// creating the paused draft on first use (a data source must exist before the
// resource endpoint can be called) and keeping it in step with the form while
// the dialog is still a draft. A single in-flight creation is shared, so the
// tree load and a name lookup cannot create two rows for one dialog.
let tempDsPromise: Promise<string> | null = null
async function ensureTempDataSource(): Promise<string> {
  if (tempDsId.value) {
    if (!isEdit.value) {
      await updateDataSource(tempDsId.value, {
        ...form.value,
        knowledge_base_id: props.kbId,
      } as any)
    }
    return tempDsId.value
  }
  // A form that cannot produce a data source yet has nothing to list through,
  // so the create is skipped rather than sent: no request, no error, and — this
  // is the point of returning early — no cached promise, so a later attempt
  // with a real type creates the draft normally.
  if (!canCreateTempDataSource()) return ''
  if (!tempDsPromise) {
    tempDsPromise = (async () => {
      const res = await createDataSource({
        ...form.value,
        knowledge_base_id: props.kbId,
        status: 'paused',
      } as any)
      const created = res?.data || res
      const createdId = typeof created?.id === 'string' ? created.id.trim() : ''
      if (!createdId) {
        // A create that answered without an id cannot be listed through, and
        // /datasource//resources is certain to fail. Failing here instead keeps
        // tempDsId unset, so the next attempt can create a usable draft.
        throw new Error(t('datasource.saveFailed'))
      }
      tempDsId.value = createdId
      return createdId
    })().finally(() => { tempDsPromise = null })
  }
  return tempDsPromise
}

async function loadResources() {
  // DingTalk with a manual selection shows the preview, not the tree: the
  // knowledge-base tree is collapsed behind its expander, so there is nothing
  // to fill and the team's knowledge bases must not be fetched. The tree is
  // listed when the expander is first opened (see toggleDingTalkResourceTree),
  // which is also the only moment its contents become visible.
  if (isDingTalkConnector(form.value.type) && hasDingTalkManualSelection.value
    && !dingtalkTreeExpanded.value) {
    return
  }
  loadingResources.value = true
  try {
    syncConfluencePublicFieldsToSettings()
    // No id means the form cannot be created yet (a blank connector type, or a
    // missing knowledge base): the tree simply does not load, and nothing is
    // reported — the request that would have produced that error is not sent.
    const dsId = await ensureTempDataSource()
    if (!dsId) return

    const res = await listResources(dsId)
    resources.value = res?.data || res || []
    dingtalkTreeLoaded.value = true
    // The described rows of manual selections are roots of their own and are
    // merged back before the reveal step, so a Base already has a row and is
    // never "revealed" by expanding something the tree cannot show.
    mergeDingTalkManualRowsIntoTree()
    // Any parent that already arrived with children (connectors returning the
    // full tree, e.g. Notion) needs no further lazy fetch.
    const parentsWithChildren = new Set<string>()
    for (const r of resources.value) {
      if (r.parent_id) parentsWithChildren.add(r.parent_id)
    }
    loadedChildrenIds.value = parentsWithChildren
    loadingChildrenIds.value = new Set<string>()
    // If any resource already has a parent, the connector returned the whole tree
    // up front, so per-node lazy fetching is unnecessary.
    treeFullyLoaded.value = parentsWithChildren.size > 0
    // Auto-expand top-level nodes whose children are already loaded; lazy nodes
    // (children not yet fetched) stay collapsed until the user expands them.
    expandedResourceIds.value = new Set(
      resources.value
        .filter(r => !r.parent_id && r.has_children && parentsWithChildren.has(r.external_id))
        .map(r => r.external_id),
    )
    // When editing a lazily-loaded source, reveal pre-existing selections that
    // live below the (not-yet-loaded) tree so they are visible and checked.
    if (isEdit.value && !treeFullyLoaded.value) {
      const loaded = new Set(resources.value.map(r => r.external_id))
      const hidden = selectedResourceIds.value.filter(id => !loaded.has(id))
      if (hidden.length > 0) void revealExistingSelections(hidden)
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.resourceLoadFailed'))
  } finally {
    // A skipped load returns from the try block, so the spinner is cleared here
    // rather than after the block.
    loadingResources.value = false
  }
}

// revealExistingSelections asks the backend which ancestors must be expanded to
// surface the current (possibly deeply nested) selection, then loads each level
// so the saved selection becomes visible and correctly checked in the tree.
async function revealExistingSelections(hiddenIds: string[]) {
  if (!tempDsId.value || hiddenIds.length === 0) return
  try {
    const res = await resolveResourceAncestors(tempDsId.value, hiddenIds)
    const ancestors: string[] = res?.data?.ancestors || res?.ancestors || []
    if (ancestors.length === 0) return
    const expanded = new Set(expandedResourceIds.value)
    for (const id of ancestors) expanded.add(id)
    expandedResourceIds.value = expanded
    // Load each ancestor level (children include the next ancestor / the
    // selection itself); calls are independent and dedup on merge.
    await Promise.all(ancestors.map(id => ensureChildrenLoaded(id)))
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.resourceLoadFailed'))
  }
}

function getDescendantIds(id: string): string[] {
  const ids: string[] = []
  const children = childrenMap.value.get(id) || []
  for (const c of children) {
    ids.push(c.external_id)
    ids.push(...getDescendantIds(c.external_id))
  }
  return ids
}

function getAncestorChain(id: string): string[] {
  const chain = [id]
  for (let p = parentMap.value.get(id); p; p = parentMap.value.get(p)) {
    chain.push(p)
  }
  return chain
}

function isCovered(id: string, cover: Set<string>): boolean {
  for (let cur: string | undefined = id; cur; cur = parentMap.value.get(cur)) {
    if (cover.has(cur)) return true
  }
  return false
}

function checkResource(id: string, cover: Set<string>) {
  if (isCovered(id, cover)) return
  const descendants = new Set(getDescendantIds(id))
  for (const d of [...cover]) {
    if (descendants.has(d)) cover.delete(d)
  }
  cover.add(id)
}

// Removes id from the cover set. If id is covered transitively (an ancestor is
// in the cover set), the ancestor is replaced with explicit entries for each
// sibling along the path so the rest of the subtree stays selected.
function uncheckResource(id: string, cover: Set<string>) {
  const chain = getAncestorChain(id) // [id, parent, ..., root]
  let highestIdx = -1
  for (let i = chain.length - 1; i >= 0; i--) {
    if (cover.has(chain[i])) { highestIdx = i; break }
  }
  if (highestIdx > 0) {
    cover.delete(chain[highestIdx])
    for (let i = highestIdx; i > 0; i--) {
      const parent = chain[i]
      const next = chain[i - 1]
      for (const sib of childrenMap.value.get(parent) || []) {
        if (sib.external_id !== next) cover.add(sib.external_id)
      }
    }
  }
  cover.delete(id)
  const descendants = new Set(getDescendantIds(id))
  for (const d of [...cover]) {
    if (descendants.has(d)) cover.delete(d)
  }
}

function toggleResource(id: string) {
  const cover = new Set(selectedResourceIds.value)
  const seafile = isSeafileConnector(form.value.type)
  if ((checkStates.value.get(id) || 'unchecked') === 'unchecked') {
    checkResource(id, cover)
  } else {
    uncheckResource(id, cover)
    // Unchecking also drops saved Seafile selections below this node that
    // the tree no longer lists; otherwise they could never be cleared.
    if (seafile) {
      for (const sel of [...cover]) {
        if (seafileWithin(sel, id)) cover.delete(sel)
      }
    }
  }
  if (seafile && new Set([...cover].map(seafileLibraryOf)).size > 1) {
    MessagePlugin.warning(t('datasource.seafile.singleLibraryOnly'))
    return
  }
  selectedResourceIds.value = [...cover]
}

function validateRssFeedUrls(): boolean {
  if (form.value.type !== 'rss') return true
  if (!String(form.value.config.settings.feed_urls || '').trim()) {
    MessagePlugin.warning(`${t('datasource.field.feedUrls')} ${t('datasource.isRequired')}`)
    return false
  }
  return true
}

function validateStep1Fields(): boolean {
  syncRssAuthHeadersToCredentials()
  if (!validateRssFeedUrls()) return false
  if (isEdit.value && credentialsConfigured.value && !replaceCredentialsMode.value) {
    return true
  }

  const fields = displayedCredentialFields.value
  for (const f of fields) {
    if (f.optional || f.fieldType === 'custom_headers') continue
    if (!form.value.config.credentials[f.key]) {
      MessagePlugin.warning(`${t(f.labelKey)} ${t('datasource.isRequired')}`)
      return false
    }
  }
  return true
}

async function nextStep() {
  if (step.value === 1) {
    if (!validateStep1Fields()) return
    if (needsConnectionTest() && testResult.value !== 'success') {
      await testConnection()
      if ((testResult.value as string) !== 'success') return
    }
  }
  if (step.value === 2 && isDriveConnector(form.value.type)) {
    // folder_token 是 Drive 连接器的必填项：为空就地标错并留在本步,
    // 不允许带着空 token 进入同步策略。
    if (!driveFolderToken.value.trim()) {
      driveFolderTokenError.value = t('datasource.drive.folderTokenRequired')
      return
    }
    driveFolderTokenError.value = ''
  }
  if (step.value === 2 && isDingTalkConnector(form.value.type) && dingtalkManualId.value.trim()) {
    // A typed id the user forgot to add would otherwise be dropped silently on
    // the way to the next step. The box keeps a committed entry, so this is
    // usually a re-application of what was just added: that is a no-op, because
    // the cover set holds one entry per reference.
    if (!applyDingTalkManualEntry()) return
  }
  if (step.value === 2 && isGitLabConnector(form.value.type)) {
    syncGitLabProjectsToSettings()
    if (!gitlabProjects.value.some(project => project.project_id.trim())) {
      MessagePlugin.warning(t('datasource.gitlab.projectRequired'))
      return
    }
  }
  // Seafile has no "whole account" scope: the backend rejects an empty selection.
  if (step.value === 2 && isSeafileConnector(form.value.type) && selectedResourceIds.value.length === 0) {
    MessagePlugin.warning(t('datasource.seafile.selectionRequired'))
    return
  }
  step.value++
  if (step.value === 2) {
    // Drive connectors need a user-supplied folder_token before listing.
    // In edit mode with a saved folder_token, auto-load so the saved tree
    // (and any pre-existing selections) are revealed without an extra click.
    // In create mode (no folder_token yet), just show the placeholder.
    if (isDriveConnector(form.value.type)) {
      if (!driveRootLoaded.value && driveFolderToken.value.trim()) {
        void loadDriveRoot()
      }
      return
    }
    if (isGitLabConnector(form.value.type)) return
    if (isDingTalkConnector(form.value.type)) {
      // A saved manual reference is described as soon as the step opens, so the
      // preview names it without ever touching the (collapsed) tree. With no
      // manual reference this does nothing at all.
      void resolveDingTalkManualNames()
    }
    loadResources()
  }
}

function prevStep() {
  step.value--
}

// Build the config payload for Create / Update requests.
//
// Create mode: credentials flow inline so the initial data source row
// already carries them.
//
// Edit mode: credentials NEVER flow through the main PUT — they go via the
// /credentials subresource, committed before the main submit (see
// commitCredentialsIfNeeded). Sending an empty map keeps the backend
// validator happy.
function buildConfigPayload(): Record<string, unknown> {
  syncGitLabProjectsToSettings()
  syncConfluencePublicFieldsToSettings()
  return {
    credentials: isEdit.value ? {} : { ...form.value.config.credentials },
    resource_ids: form.value.config.resource_ids,
    settings: form.value.config.settings,
  }
}

// In edit mode, when the user opted in to Replace credentials and typed at
// least one value, commit it to /credentials before the main PUT. Aborts
// the whole submit on failure so we don't leave the row partially saved.
async function commitCredentialsIfNeeded(dsId: string): Promise<boolean> {
  if (!isEdit.value || !replaceCredentialsMode.value) return true
  syncRssAuthHeadersToCredentials()
  syncConfluencePublicFieldsToSettings()
  const filled = Object.entries(form.value.config.credentials).filter(
    ([, v]) => typeof v === 'string' ? v !== '' : v != null,
  )
  if (filled.length === 0) return true
  try {
    await putDataSourceCredentials(dsId, Object.fromEntries(filled))
    credentialsConfigured.value = true
    replaceCredentialsMode.value = false
    form.value.config.credentials = {}
    rssAuthHeaders.value = []
    return true
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('credential.saveFailed'))
    return false
  }
}

// --- Final submit ---
async function handleSubmit() {
  form.value.config.resource_ids = selectedResourceIds.value
  submitting.value = true
  try {
    let dataSourceId = tempDsId.value

    if (tempDsId.value) {
      // Commit credential replacement BEFORE the main PUT so a validation
      // failure on credentials doesn't leave us with an updated row that
      // still points at the old broken token.
      const credsOk = await commitCredentialsIfNeeded(tempDsId.value)
      if (!credsOk) {
        submitting.value = false
        return
      }
      await updateDataSource(tempDsId.value, {
        ...form.value,
        config: buildConfigPayload(),
        knowledge_base_id: props.kbId,
        status: 'active',
      } as any)
    } else {
      const res = await createDataSource({
        ...form.value,
        config: buildConfigPayload(),
        knowledge_base_id: props.kbId,
        status: 'active',
      } as any)
      const created = res?.data || res
      dataSourceId = created.id
      tempDsId.value = created.id
    }

    if (isEdit.value) {
      MessagePlugin.warning(t('datasource.updateSuccessSyncHint'))
    } else {
      try {
        await triggerSync(dataSourceId)
        MessagePlugin.success(t('datasource.createAndSyncSuccess'))
      } catch (e: any) {
        MessagePlugin.warning(e?.message || e?.error || t('datasource.createButSyncFailed'))
      }
    }

    emit('saved')
    // Clear before close — otherwise the visible watcher treats the just-saved
    // row as an abandoned temp draft and DELETEs it (loadResources creates the
    // row early at step 2 with tempDsId).
    tempDsId.value = ''
    visible.value = false
  } catch (e: any) {
    MessagePlugin.error(e?.message || e?.error || t('datasource.saveFailed'))
  }
  submitting.value = false
}

function handleClose() {
  visible.value = false
}

async function handleDrawerConfirm() {
  if (step.value === 1 || step.value === 2) {
    await nextStep()
  } else if (step.value === 3) {
    handleSubmit()
  }
}

const selectedResourceCount = computed(() => {
  let count = 0
  for (const state of checkStates.value.values()) {
    if (state === 'checked') count++
  }
  // Manually entered DingTalk ids are not part of the tree, so they carry no
  // check state; they are real selections all the same and must be counted, or
  // a sync of only pasted ids would report "0 selected". A reference the
  // connector has described does have a row in the tree — that row is already
  // counted above — so only the undescribed ones are added here, or the same
  // selection would be counted twice.
  const described = new Set(Object.keys(dingtalkManualRows.value))
  return count + dingtalkManualReferences.value.filter(id => !described.has(id)).length
})

const hasExpandableNodes = computed(() => resources.value.some(r => r.has_children))

function resourceIconName(r: Resource): string {
  // Seafile libraries expand like folders but are the top-level unit a data
  // source binds to, so they keep the root icon.
  if (r.type === 'library') return 'root-list'
  if (r.has_children) return 'folder'
  switch (r.type) {
    case 'wiki_space':
      return 'root-list'
    case 'book':
      return 'book'
    case 'doc_category':
      return 'folder-open'
    default:
      return 'file'
  }
}

function expandAllNodes() {
  const expandable = resources.value.filter(r => r.has_children)
  expandedResourceIds.value = new Set(expandable.map(r => r.external_id))
  // Lazily load children of any expanded node that hasn't been fetched yet.
  for (const r of expandable) {
    void ensureChildrenLoaded(r.external_id)
  }
}

function collapseAllNodes() {
  expandedResourceIds.value = new Set()
}

const resourceTypeLabelMap: Record<string, string> = {
  wiki_space: 'datasource.resourceType.wikiSpace',
  doc_category: 'datasource.resourceType.docCategory',
  book: 'datasource.resourceType.book',
  library: 'datasource.resourceType.library',
  // DingTalk's self-addressing references are described by the connector rather
  // than listed by a workspace. The two types are kept apart because they sync
  // different things: a 多维表 ingests all of its tables as one document, while
  // each wiki document under it is a separate selection.
  base: 'datasource.dingtalk.resourceTypeBase',
  base_child: 'datasource.dingtalk.resourceTypeBaseChild',
}

function resourceTypeLabel(type: string): string {
  const key = resourceTypeLabelMap[type]
  if (key) return t(key)
  return ''
}

function shouldShowResourceType(type: string): boolean {
  return !!resourceTypeLabelMap[type]
}

// resourceHint explains, in one line, what selecting a described row syncs. It
// is rendered for a Base both in the tree and — this is the point of the line —
// under the Base's own row in the selection preview, where the tables a base=
// reference ingests must be readable without expanding anything. Without it the
// documents listed under the Base would read as "the Base's contents", which
// they are not: they are separate selections.
function resourceHint(r: Resource): string {
  if (r.type !== 'base') return ''
  const tables = dingtalkTableNames(r.description)
  return t('datasource.dingtalk.resourceHintBase', {
    // The count and the "as one document" clause sit in the list phrase, so one
    // outer sentence covers both answers: the tables the connector named, and
    // — when it could not list them — "all tables", with no invented count.
    tables: tables.length > 0
      ? t('datasource.dingtalk.resourceHintBaseTableList', {
        count: tables.length,
        tables: tables.join(' · '),
      })
      : t('datasource.dingtalk.resourceHintBaseAllTables'),
  })
}

function resourceRowState(id: string): CheckState {
  return checkStates.value.get(id) || 'unchecked'
}

const stepTitles = computed(() => [
  t('datasource.step.selectType'),
  t('datasource.step.credentials'),
  t('datasource.step.resources'),
  t('datasource.step.strategy'),
])

const drawerTitle = computed(() =>
  isEdit.value ? t('datasource.editTitle') : t('datasource.createTitle'),
)

const drawerDescription = computed(() => stepTitles.value[step.value] ?? '')

const drawerConfirmText = computed(() => {
  if (step.value === 3) {
    return isEdit.value ? t('datasource.save') : t('datasource.createAndSync')
  }
  if (step.value >= 1) return t('datasource.next')
  return t('common.save')
})
</script>

<template>
  <SettingDrawer
    v-model:visible="visible"
    :title="drawerTitle"
    :description="drawerDescription"
    :class="[form.type ? `datasource-editor-drawer datasource-editor-drawer--${form.type}` : 'datasource-editor-drawer', { 'ds-fixed-step': step === 2 && !isGitLabConnector(form.type) }]"
    :hide-footer="step === 0"
    :confirm-text="drawerConfirmText"
    :confirm-loading="submitting || (step === 1 && testing)"
    storage-key="setting-drawer:width:datasource-editor"
    width="640px"
    @confirm="handleDrawerConfirm"
    @cancel="handleClose"
  >
    <template v-if="form.type && getDatasourceIconUrl(form.type)" #headerIcon>
      <img
        :src="getDatasourceIconUrl(form.type)"
        :alt="form.type"
        class="datasource-header-icon__img"
      >
    </template>

    <template v-if="step === 1" #footer-left>
      <t-button v-if="!isEdit" variant="outline" @click="step = 0">
        {{ t('datasource.back') }}
      </t-button>
      <t-button variant="outline" :loading="testing" @click="testConnection">
        <template #icon>
          <t-icon
            v-if="!testing && testResult === 'success'"
            name="check-circle-filled"
            class="status-icon available"
          />
          <t-icon
            v-else-if="!testing && testResult === 'error'"
            name="close-circle-filled"
            class="status-icon unavailable"
          />
        </template>
        {{ testing ? t('model.editor.testing') : t('datasource.testConnection') }}
      </t-button>
      <span
        v-if="testResult"
        :class="['footer-test-message', testResult === 'success' ? 'success' : 'error']"
        :title="testResult === 'error' ? testErrorMsg : t('datasource.connected')"
      >
        {{
          testResult === 'success'
            ? t('datasource.connected')
            : (testErrorMsg || t('datasource.connectionFailed'))
        }}
      </span>
    </template>

    <template v-else-if="step === 2 || step === 3" #footer-left>
      <t-button variant="outline" @click="prevStep">
        {{ t('datasource.back') }}
      </t-button>
    </template>

    <!-- Step indicator -->
    <div class="ds-steps">
      <div
        v-for="(title, i) in stepTitles"
        :key="i"
        :class="['ds-step', { active: step === i, done: step > i }]"
      >
        <span class="ds-step-num">
          <t-icon v-if="step > i" name="check" class="ds-step-check" />
          <template v-else>{{ i + 1 }}</template>
        </span>
        <span class="ds-step-title">{{ title }}</span>
      </div>
    </div>

    <!-- Step 0: Select connector type -->
    <section v-if="step === 0" class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('datasource.step.selectType') }}</h4>
      <div class="ds-type-grid">
        <button
          v-for="def in connectorDefs"
          :key="def.type"
          type="button"
          :class="['ds-type-card', { disabled: !def.available }]"
          :disabled="!def.available"
          @click="selectType(def)"
        >
          <div class="ds-type-header">
            <DataSourceTypeIcon :type="def.type" :size="20" />
            <span class="ds-type-name">{{ t(`datasource.connector.${def.type}`) }}</span>
            <span v-if="!def.available" class="ds-type-soon">{{ t('datasource.comingSoon') }}</span>
          </div>
          <div class="ds-type-desc">{{ t(`datasource.connectorDesc.${def.type}`) }}</div>
        </button>
      </div>
    </section>

    <!-- Step 1: Credentials -->
    <template v-if="step === 1">
      <div
        v-if="currentDef && currentDef.requiredPermissions.length > 0"
        class="ds-setup-guide ds-setup-guide--standalone"
      >
        <button
          type="button"
          class="ds-setup-guide__toggle"
          :aria-expanded="prereqExpanded"
          @click="prereqExpanded = !prereqExpanded"
        >
          <t-icon name="info-circle-filled" size="15px" class="ds-setup-guide__icon" />
          <span class="ds-setup-guide__summary">
            {{ t(`datasource.prereqBarText_${form.type}`, t('datasource.prereqBarText')) }}
          </span>
          <t-icon
            :name="prereqExpanded ? 'chevron-up' : 'chevron-down'"
            size="14px"
            class="ds-setup-guide__chevron"
          />
        </button>
        <div v-if="prereqExpanded" class="ds-setup-guide__body">
          <ol class="ds-setup-steps">
            <li class="ds-setup-step">
              <span class="ds-setup-step__title">{{ t(`datasource.prereqStep1Brief_${form.type}`,
                t('datasource.prereqBotBrief')) }}</span>
              <span class="ds-setup-step__desc">{{ t(`datasource.prereqStep1Desc_${form.type}`,
                t('datasource.prereqBotDesc')) }}</span>
            </li>
            <li class="ds-setup-step">
              <span class="ds-setup-step__title">{{ t(`datasource.prereqStep2Brief_${form.type}`,
                t('datasource.prereqPermBrief')) }}</span>
              <span class="ds-setup-step__desc">
                <template v-if="!t(`datasource.prereqStep2Desc_${form.type}`)">
                  <code
                    v-for="perm in currentDef.requiredPermissions"
                    :key="perm"
                    class="ds-perm-tag"
                  >{{ perm }}</code>
                </template>
                <template v-else>{{ t(`datasource.prereqStep2Desc_${form.type}`) }}</template>
              </span>
            </li>
            <li class="ds-setup-step">
              <span class="ds-setup-step__title">{{ t(`datasource.prereqStep3Brief_${form.type}`,
                t('datasource.prereqMemberBrief')) }}</span>
              <span class="ds-setup-step__desc">{{ t(`datasource.prereqStep3Desc_${form.type}`,
                t('datasource.prereqMemberDesc')) }}</span>
            </li>
          </ol>
          <a
            v-if="currentDef.permissionPageUrl"
            :href="currentDef.permissionPageUrl"
            target="_blank"
            rel="noopener"
            class="doc-link ds-setup-guide__link"
          >
            {{ t(`datasource.prereqOpenConsole_${form.type}`, t('datasource.prereqOpenConsole')) }}
            <t-icon name="link" class="link-icon" />
          </a>
        </div>
      </div>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('datasource.sectionBasic') }}</h4>

        <div v-if="currentDef?.docUrl" class="inline-alert">
          <t-icon name="info-circle-filled" class="inline-alert__icon" />
          <span class="inline-alert__text">{{ t('datasource.docHint') }}</span>
          <a
            :href="currentDef.docUrl"
            target="_blank"
            rel="noopener"
            class="inline-alert__action doc-link"
          >
            {{ t('datasource.openDoc') }}
            <t-icon name="link" class="link-icon" />
          </a>
        </div>

        <div class="form-item">
          <label class="form-label required">{{ t('datasource.nameLabel') }}</label>
          <t-input v-model="form.name" :placeholder="t('datasource.namePlaceholder')" />
        </div>
      </section>

      <section v-if="form.type === 'rss'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('datasource.field.feedUrls') }}</h4>
        <div class="form-item">
          <label class="form-label required">{{ t('datasource.field.feedUrls') }}</label>
          <t-textarea
            v-model="form.config.settings.feed_urls"
            placeholder="https://example.com/feed.xml"
            :autosize="{ minRows: 2, maxRows: 6 }"
            autocomplete="off"
            spellcheck="false"
          />
          <p class="form-desc">{{ t('datasource.field.feedUrlsHint') }}</p>
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('datasource.credentialsLabel') }}</h4>

        <div v-if="isEdit && credentialsConfigured && !replaceCredentialsMode" class="form-item">
          <div
            class="credential-faux-input"
            :class="{ 'is-confirm-remove': pendingRemoveCredentials }"
            :title="pendingRemoveCredentials ? '' : t('credential.configured')"
          >
            <template v-if="pendingRemoveCredentials">
              <t-icon name="error-circle-filled" class="credential-status-icon warn" />
              <span class="credential-faux-text danger">{{ t('credential.confirmRemovePrompt') }}</span>
              <div class="credential-actions">
                <t-button size="small" variant="text" @click="cancelPendingRemoveCredentials">
                  {{ t('common.cancel') }}
                </t-button>
                <span class="action-divider" />
                <t-button
                  size="small"
                  variant="text"
                  theme="danger"
                  :loading="removingCredentials"
                  @click="confirmRemoveCredentials"
                >
                  {{ t('credential.confirmRemove') }}
                </t-button>
              </div>
            </template>
            <template v-else>
              <t-icon name="check-circle-filled" class="credential-status-icon success" />
              <span class="credential-faux-text">{{ t('credential.configured') }}</span>
              <div class="credential-actions">
                <t-button size="small" variant="text" @click="enterReplaceCredentials">
                  {{ t('credential.update') }}
                </t-button>
                <span class="action-divider" />
                <t-button size="small" variant="text" theme="danger" @click="requestRemoveCredentials">
                  {{ t('credential.remove') }}
                </t-button>
              </div>
            </template>
          </div>
        </div>

        <div
          v-else-if="isEdit && !credentialsConfigured && !replaceCredentialsMode"
          class="form-item"
        >
          <div
            class="credential-faux-input is-empty"
            @click="enterReplaceCredentials"
          >
            <t-icon name="lock-on" class="credential-status-icon muted" />
            <span class="credential-faux-text muted">{{ t('credential.unconfigured') }}</span>
            <div class="credential-actions">
              <t-button size="small" variant="text" theme="primary" @click.stop="enterReplaceCredentials">
                {{ t('credential.configure') }}
              </t-button>
            </div>
          </div>
        </div>

        <template v-else-if="credentialsInputVisible">
          <div v-if="form.type === 'confluence'" class="form-item">
            <label class="form-label">{{ t('datasource.field.confluenceEdition') }}</label>
            <t-select v-model="form.config.credentials.edition">
              <t-option value="server" :label="t('datasource.field.confluenceEditionServer')" />
              <t-option value="cloud" :label="t('datasource.field.confluenceEditionCloud')" />
            </t-select>
          </div>
          <div
            v-for="field in displayedCredentialFields"
            :key="field.key"
            class="form-item"
          >
            <template v-if="field.fieldType === 'custom_headers'">
              <div class="custom-headers-header">
                <label class="form-label" style="margin-bottom: 0;">{{ t(field.labelKey) }}</label>
                <t-button variant="text" size="small" theme="primary" @click="addRssAuthHeader">
                  <template #icon><t-icon name="add" /></template>
                  {{ t('model.editor.customHeadersAdd') }}
                </t-button>
              </div>
              <p v-if="field.hintKey" class="form-desc custom-headers-desc">{{ t(field.hintKey) }}</p>
              <div v-if="rssAuthHeaders.length > 0" class="custom-headers-list">
                <div v-for="(item, idx) in rssAuthHeaders" :key="idx" class="custom-header-row">
                  <t-input
                    v-model="item.key"
                    :placeholder="t('model.editor.customHeadersKeyPlaceholder')"
                    class="custom-header-key"
                    autocomplete="off"
                    spellcheck="false"
                  />
                  <t-input
                    v-model="item.value"
                    :placeholder="t('model.editor.customHeadersValuePlaceholder')"
                    class="custom-header-value"
                    autocomplete="off"
                    spellcheck="false"
                  />
                  <t-button
                    variant="text"
                    shape="square"
                    size="small"
                    class="custom-header-remove"
                    :aria-label="t('common.delete')"
                    @click="removeRssAuthHeader(idx)"
                  >
                    <t-icon name="close" />
                  </t-button>
                </div>
              </div>
            </template>
            <template v-else>
              <label class="form-label" :class="{ required: !field.optional }">
                {{ t(field.labelKey) }}
              </label>
              <t-textarea
                v-if="field.multiline"
                v-model="form.config.credentials[field.key]"
                :placeholder="field.placeholder || t('credential.inputPlaceholder')"
                :autosize="{ minRows: 2, maxRows: 6 }"
                autocomplete="off"
                spellcheck="false"
              />
              <t-input
                v-else
                v-model="form.config.credentials[field.key]"
                :placeholder="field.placeholder || t('credential.inputPlaceholder')"
                :type="field.secret ? 'password' : 'text'"
                autocomplete="off"
                spellcheck="false"
              >
                <template v-if="field.secret" #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>
              <p v-if="field.hintKey" class="form-desc">{{ t(field.hintKey) }}</p>
            </template>
          </div>
          <div v-if="isEdit && replaceCredentialsMode" class="credential-edit-actions">
            <t-button size="small" variant="text" @click="cancelReplaceCredentials">
              {{ t('common.cancel') }}
            </t-button>
          </div>
        </template>
      </section>
    </template>

    <!-- Step 2: Select resources -->
    <section v-if="step === 2" class="setting-drawer__section ds-resource-section">
      <template v-if="isGitLabConnector(form.type)">
        <h4 class="setting-drawer__section-title">{{ t('datasource.gitlab.projects') }}</h4>
        <p class="ds-resource-hint">{{ t('datasource.gitlab.projectsHint') }}</p>
        <div class="gitlab-project-list">
          <div v-for="(project, index) in gitlabProjects" :key="index" class="gitlab-project-row">
            <div class="gitlab-project-row__header">
              <strong>{{ t('datasource.gitlab.project') }} {{ index + 1 }}</strong>
              <t-button variant="text" size="small" theme="danger" @click="removeGitLabProject(index)"><t-icon name="delete" /></t-button>
            </div>
            <label class="form-label required">{{ t('datasource.gitlab.projectId') }}</label>
            <t-input v-model="project.project_id" :placeholder="t('datasource.gitlab.projectIdPlaceholder')" />
            <label class="form-label">{{ t('datasource.gitlab.ref') }}</label>
            <t-input v-model="project.ref" :placeholder="t('datasource.gitlab.refPlaceholder')" />
            <label class="form-label">{{ t('datasource.gitlab.paths') }}</label>
            <t-textarea v-model="project.pathsText" :placeholder="t('datasource.gitlab.pathsPlaceholder')" :autosize="{ minRows: 2, maxRows: 5 }" />
          </div>
          <t-button variant="outline" @click="addGitLabProject"><template #icon><t-icon name="add" /></template>{{ t('datasource.gitlab.addProject') }}</t-button>
        </div>
      </template>
      <template v-else>
      <h4 class="setting-drawer__section-title">{{ t('datasource.step.resources') }}</h4>
      <p class="ds-resource-hint">{{ t('datasource.resourceHint') }}</p>

      <!-- DingTalk manual entry: shown alongside the tree, not instead of it.
           A personal-space document and a multi-dimensional table are readable
           by id but never appear in the lazy-load tree, so the user pastes a
           link (or an id) and picks which of the two it is. The selector is
           required: the two link shapes are identical. Tree picking below is
           unchanged, and a user who only picks from the tree never touches
           this. -->
      <div v-if="isDingTalkConnector(form.type)" class="dingtalk-manual-input">
        <label class="dingtalk-manual-input__label">
          {{ t('datasource.dingtalk.manualLabel') }}
          <t-tooltip :content="t('datasource.dingtalk.manualHint')" placement="top">
            <t-icon name="help-circle" class="dingtalk-manual-input__help" />
          </t-tooltip>
        </label>
        <div class="dingtalk-manual-input__row">
          <t-radio-group v-model="dingtalkManualKind" :disabled="loadingResources">
            <t-radio-button value="node">{{ t('datasource.dingtalk.manualKindNode') }}</t-radio-button>
            <t-radio-button value="base">{{ t('datasource.dingtalk.manualKindBase') }}</t-radio-button>
          </t-radio-group>
          <t-input
            v-model="dingtalkManualId"
            class="dingtalk-manual-input__field"
            :placeholder="t('datasource.dingtalk.manualPlaceholder')"
            :status="dingtalkManualError ? 'error' : 'default'"
            clearable
            @enter="applyDingTalkManualEntry"
            @input="dingtalkManualError = ''"
          />
          <t-button theme="primary" @click="applyDingTalkManualEntry">
            {{ t('datasource.dingtalk.manualAdd') }}
          </t-button>
        </div>
        <p v-if="dingtalkManualError" class="dingtalk-manual-input__error">{{ dingtalkManualError }}</p>
      </div>

      <!-- What THIS data source will sync, first and prominently: the manual
           references and the tree-checked resources together, in one list.
           The tree below cannot show the manual part at all, so the selection
           area must not depend on it. This is also why the picker is demoted
           into the explicit expander: it adds to this list, it does not define
           it. Rendered only once a manual reference exists, so a tree-only user
           keeps the exact pre-existing step. -->
      <div v-if="hasDingTalkManualSelection" class="ds-selection-preview">
        <div class="ds-selection-preview__title">{{ t('datasource.dingtalk.selectionTitle') }}</div>
        <div
          v-for="row in dingtalkSelectionRows"
          :key="row.id"
          class="ds-selection-preview__row"
        >
          <div class="ds-selection-preview__head">
            <!-- The disclosure control expands with the same lazy call the tree
                 uses (listResources(parent_id=<ref>)). It exists only for a row
                 the connector reports children for: a leaf has nothing to list,
                 so an expander there would promise content that is not there. -->
            <button
              v-if="dingtalkSelectionRowExpandable(row)"
              type="button"
              class="ds-selection-preview__expand"
              :aria-expanded="dingtalkSelectionRowExpanded(row.id)"
              :aria-label="dingtalkSelectionRowExpanded(row.id)
                ? t('knowledgeStages.collapseBranch')
                : t('knowledgeStages.expandBranch')"
              @click="toggleExpand(row.id)"
            >
              <t-loading v-if="dingtalkSelectionRowLoading(row.id)" size="12px" />
              <t-icon
                v-else
                :name="dingtalkSelectionRowExpanded(row.id) ? 'chevron-down' : 'chevron-right'"
                size="12px"
              />
            </button>
            <span v-else class="ds-selection-preview__expand-spacer" aria-hidden="true" />
            <span class="ds-selection-preview__kind">{{ row.kind }}</span>
            <!-- The whole name, wrapped rather than ellipsised, and the id it
                 came from on hover: a truncated name cannot be confirmed. -->
            <span class="ds-selection-preview__label" :title="row.id">{{ row.label }}</span>
            <!-- "Added by ID, not in the tree" explains the row, it is not part
                 of its name: as an icon tooltip it costs the name no room. -->
            <t-tooltip
              v-if="row.manual"
              :content="t('datasource.dingtalk.selectionManualNote')"
              placement="top"
            >
              <span
                class="ds-selection-preview__note-icon"
                role="img"
                :aria-label="t('datasource.dingtalk.selectionManualNote')"
              >
                <t-icon name="help-circle" />
              </span>
            </t-tooltip>
            <button
              type="button"
              class="ds-selection-preview__remove"
              @click="row.manual ? removeDingTalkManualReference(row.id) : removeTreeSelectionRow(row.id)"
            >{{ t('datasource.dingtalk.selectionRemove') }}</button>
          </div>
          <!-- What the row syncs, in the row itself: for a Base the tables are
               the content, and that is the single most important fact about the
               selection, so it is never hidden behind an expansion. -->
          <p v-if="dingtalkSelectionRowHint(row)" class="ds-selection-preview__hint">
            {{ dingtalkSelectionRowHint(row) }}
          </p>
          <!-- The children the row lists on expansion. They are not selections:
               under a Base they are knowledge-base documents that sync on their
               own, which is exactly what their label says — they are not the
               tables the line above describes. -->
          <div
            v-if="dingtalkSelectionRowExpanded(row.id)"
            class="ds-selection-preview__children"
          >
            <div
              v-for="child in dingtalkSelectionRowChildren(row.id)"
              :key="child.external_id"
              class="ds-selection-preview__child"
            >
              <t-icon :name="resourceIconName(child)" size="14px" />
              <span class="ds-selection-preview__child-name" :title="child.name || t('datasource.untitled')">
                {{ child.name || t('datasource.untitled') }}
              </span>
              <span
                v-if="shouldShowResourceType(child.type)"
                class="ds-selection-preview__child-kind"
              >{{ resourceTypeLabel(child.type) }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Drive (云盘) root input: shown alongside the tree (not as a switch).
           The user supplies a folder_token (or a Drive folder URL) and clicks
           "load"; the tree below stays as a placeholder until load succeeds.
           Other connectors skip this and go straight to the tree. -->
      <div v-if="isDriveConnector(form.type)" class="drive-folder-input">
        <label class="drive-folder-input__label required">
          {{ t('datasource.drive.folderTokenLabel') }}
          <t-tooltip :content="t('datasource.drive.rootNotSupportedHint')" placement="top">
            <t-icon name="help-circle" class="drive-folder-input__help" />
          </t-tooltip>
        </label>
        <div class="drive-folder-input__row">
          <t-input
            v-model="driveFolderToken"
            :placeholder="t('datasource.drive.folderTokenPlaceholder')"
            :status="driveFolderTokenError ? 'error' : 'default'"
            :tips="driveFolderTokenError ? driveFolderTokenError : t('datasource.drive.shareHint')"
            clearable
            @enter="loadDriveRoot"
            @input="driveFolderTokenError = ''"
          />
          <t-button theme="primary" :loading="loadingResources" @click="loadDriveRoot">
            {{ t('datasource.drive.load') }}
          </t-button>
        </div>
      </div>

      <!-- Drive placeholder before the first load: the tree cannot render until
           a folder_token is supplied and loaded. Non-Drive connectors never hit
           this branch. -->
      <div
        v-if="isDriveConnector(form.type) && !driveRootLoaded && !loadingResources"
        class="ds-resource-empty ds-drive-placeholder"
      >
        <p class="ds-empty-title">{{ t('datasource.drive.placeholderTitle') }}</p>
        <p class="ds-empty-desc">{{ t('datasource.drive.placeholderDesc') }}</p>
      </div>

      <div v-else-if="loadingResources" class="ds-loading-center"><t-loading /></div>
      <!-- The expander row must exist before the tree has ever been listed:
           with a manual selection the listing is deliberately deferred until
           the row is used, so this branch renders on the selection alone. -->
      <div
        v-else-if="resources.length > 0 || hasDingTalkManualSelection"
        class="resource-picker"
      >
        <!-- The picker, demoted: this row is the only way in, and it says what
             it does — adding to the preview above, never replacing it. It is
             absent for a tree-only user, who therefore never sees it and gets
             the tree as it always was. -->
        <button
          v-if="hasDingTalkManualSelection"
          type="button"
          class="resource-picker__tree-toggle"
          :aria-expanded="showDingTalkResourceTree"
          @click="toggleDingTalkResourceTree"
        >
          <t-icon :name="showDingTalkResourceTree ? 'chevron-down' : 'chevron-right'" size="12px" />
          {{ t('datasource.dingtalk.treeAddRow') }}
        </button>
        <div v-if="showDingTalkResourceTree" class="resource-picker__toolbar">
          <span class="resource-picker__count">
            {{ t('knowledgeBase.selectedCount', { count: selectedResourceCount }) }}
          </span>
          <div v-if="hasExpandableNodes" class="resource-picker__actions">
            <button type="button" class="resource-picker__action" @click="expandAllNodes">
              {{ t('knowledgeStages.expandBranch') }}
            </button>
            <span class="resource-picker__action-sep" aria-hidden="true">·</span>
            <button type="button" class="resource-picker__action" @click="collapseAllNodes">
              {{ t('knowledgeStages.collapseBranch') }}
            </button>
          </div>
        </div>
        <!-- The listing was attempted but produced nothing (or failed): the
             expander stays usable and says so instead of showing an empty
             tree. -->
        <div
          v-if="showDingTalkResourceTree && resources.length === 0 && !loadingResources"
          class="ds-resource-empty"
        >
          <p class="ds-empty-title">{{ t('datasource.noResources') }}</p>
          <div class="ds-empty-actions">
            <button type="button" class="ds-empty-retry" @click="loadResources">
              {{ t('datasource.retryLoadResources') }}
            </button>
          </div>
        </div>
        <div v-if="showDingTalkResourceTree" class="resource-picker__list" role="tree">
          <template
            v-for="{ resource: r, depth, noticeAfter } in visibleTree"
            :key="noticeAfter ? `${r.external_id}__notice` : r.external_id"
          >
          <div
            v-if="!noticeAfter"
            class="resource-picker__row"
            :class="{
              'is-checked': resourceRowState(r.external_id) === 'checked',
              'is-indeterminate': resourceRowState(r.external_id) === 'indeterminate',
            }"
            :style="{ '--depth': depth }"
            role="treeitem"
            :aria-expanded="r.has_children ? expandedResourceIds.has(r.external_id) : undefined"
            @click="toggleResource(r.external_id)"
          >
            <button
              v-if="r.has_children"
              type="button"
              class="resource-picker__expand"
              :aria-label="expandedResourceIds.has(r.external_id)
                ? t('knowledgeStages.collapseBranch')
                : t('knowledgeStages.expandBranch')"
              @click.stop="toggleExpand(r.external_id)"
            >
              <t-loading v-if="loadingChildrenIds.has(r.external_id)" size="12px" />
              <t-icon
                v-else
                :name="expandedResourceIds.has(r.external_id) ? 'chevron-down' : 'chevron-right'"
                size="12px"
              />
            </button>
            <span v-else class="resource-picker__expand-spacer" aria-hidden="true" />
            <span
              class="resource-picker__check"
              :class="{
                'is-checked': resourceRowState(r.external_id) === 'checked',
                'is-indeterminate': resourceRowState(r.external_id) === 'indeterminate',
              }"
              aria-hidden="true"
            >
              <svg
                v-if="resourceRowState(r.external_id) === 'checked'"
                width="10"
                height="10"
                viewBox="0 0 12 12"
                fill="none"
              >
                <path
                  d="M10 3L4.5 8.5L2 6"
                  stroke="#fff"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
            </span>
            <span class="resource-picker__icon" aria-hidden="true">
              <t-icon :name="resourceIconName(r)" size="16px" />
            </span>
            <span class="resource-picker__label">
              <span class="resource-picker__name" :title="r.name || t('datasource.untitled')">
                {{ r.name || t('datasource.untitled') }}
              </span>
              <span
                v-if="shouldShowResourceType(r.type)"
                class="resource-picker__type"
              >{{ resourceTypeLabel(r.type) }}</span>
            </span>
            <span
              v-if="resourceHint(r)"
              class="resource-picker__hint"
              :title="resourceHint(r)"
            >{{ resourceHint(r) }}</span>
          </div>
          <p
            v-else
            class="resource-picker__notice"
            :style="{ '--depth': depth }"
          >
            {{ t('datasource.confluence.cloudFolderLimitation') }}
          </p>
          </template>
        </div>
      </div>
      <div v-else class="ds-resource-empty">
        <t-icon name="info-circle" size="32px" style="color: var(--td-warning-color); margin-bottom: 8px;" />
        <p class="ds-empty-title">{{ t('datasource.noResources') }}</p>
        <p class="ds-empty-desc">{{ t(`datasource.noResourcesDesc_${form.type}`, t('datasource.noResourcesDesc')) }}</p>
        <div class="ds-guide-steps">
          <div class="ds-guide-step">
            <span class="ds-guide-num">1</span>
            <span>{{ t(`datasource.guideStep1_${form.type}`, t('datasource.guideStep1')) }}</span>
          </div>
          <div class="ds-guide-step">
            <span class="ds-guide-num">2</span>
            <span>{{ t(`datasource.guideStep2_${form.type}`, t('datasource.guideStep2')) }}</span>
          </div>
          <div class="ds-guide-step">
            <span class="ds-guide-num">3</span>
            <span>{{ t(`datasource.guideStep3_${form.type}`, t('datasource.guideStep3')) }}</span>
          </div>
        </div>
        <div class="ds-empty-actions">
          <button type="button" class="ds-empty-retry" @click="loadResources">
            {{ t('datasource.retryLoadResources') }}
          </button>
          <a
            v-if="currentDef?.permissionDocUrl"
            :href="currentDef.permissionDocUrl"
            target="_blank"
            rel="noopener"
            class="doc-link"
          >
            {{ t('datasource.permissionDocLink') }}
            <t-icon name="link" class="link-icon" />
          </a>
        </div>
      </div>
      </template>
    </section>

    <!-- Step 3: Sync strategy -->
    <template v-if="step === 3">
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('datasource.syncScheduleLabel') }}</h4>
        <t-select v-model="form.sync_schedule">
          <t-option v-for="p in schedulePresets" :key="p.value" :value="p.value" :label="p.label" />
        </t-select>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('datasource.syncModeLabel') }}</h4>
        <div class="form-item form-item--flat">
          <div class="option-group" role="radiogroup" :aria-label="t('datasource.syncModeLabel')">
            <button
              type="button"
              class="option-pill"
              :class="{ 'is-active': form.sync_mode === 'incremental' }"
              role="radio"
              :aria-checked="form.sync_mode === 'incremental'"
              @click="form.sync_mode = 'incremental'"
            >
              {{ t('datasource.syncMode.incremental') }}
            </button>
            <button
              type="button"
              class="option-pill"
              :class="{ 'is-active': form.sync_mode === 'full' }"
              role="radio"
              :aria-checked="form.sync_mode === 'full'"
              @click="form.sync_mode = 'full'"
            >
              {{ t('datasource.syncMode.full') }}
            </button>
          </div>
        </div>

        <div class="form-item form-item--flat">
          <label class="form-label">{{ t('datasource.conflictLabel') }}</label>
          <div class="option-group" role="radiogroup" :aria-label="t('datasource.conflictLabel')">
            <button
              type="button"
              class="option-pill"
              :class="{ 'is-active': form.conflict_strategy === 'overwrite' }"
              role="radio"
              :aria-checked="form.conflict_strategy === 'overwrite'"
              @click="form.conflict_strategy = 'overwrite'"
            >
              {{ t('datasource.conflict.overwrite') }}
            </button>
            <button
              type="button"
              class="option-pill"
              :class="{ 'is-active': form.conflict_strategy === 'skip' }"
              role="radio"
              :aria-checked="form.conflict_strategy === 'skip'"
              @click="form.conflict_strategy = 'skip'"
            >
              {{ t('datasource.conflict.skip') }}
            </button>
          </div>
        </div>

        <div class="form-item form-item--flat">
          <t-checkbox v-model="form.sync_deletions">{{ t('datasource.syncDeletions') }}</t-checkbox>
        </div>
      </section>

      <!-- Yuque only: how synced documents are laid out, and what may be admitted. -->
      <section v-if="form.type === 'yuque'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('datasource.yuqueFolderModeLabel') }}</h4>
        <div class="form-item form-item--flat">
          <div
            class="option-group"
            role="radiogroup"
            :aria-label="t('datasource.yuqueFolderModeLabel')"
          >
            <button
              type="button"
              class="option-pill"
              :class="{ 'is-active': yuqueFolderMode === 'toc' }"
              role="radio"
              :aria-checked="yuqueFolderMode === 'toc'"
              @click="yuqueFolderMode = 'toc'"
            >
              {{ t('datasource.yuqueFolderModeToc') }}
            </button>
            <button
              type="button"
              class="option-pill"
              :class="{ 'is-active': yuqueFolderMode === 'none' }"
              role="radio"
              :aria-checked="yuqueFolderMode === 'none'"
              @click="yuqueFolderMode = 'none'"
            >
              {{ t('datasource.yuqueFolderModeNone') }}
            </button>
          </div>
        </div>
        <p class="form-desc">{{ t('datasource.yuqueFolderModeHint') }}</p>

        <div class="form-item form-item--flat">
          <t-checkbox v-model="yuqueTOCOnly" :disabled="yuqueFolderMode !== 'toc'">
            {{ t('datasource.yuqueTOCOnly') }}
          </t-checkbox>
        </div>
        <p class="form-desc">{{ t('datasource.yuqueTOCOnlyHint') }}</p>
      </section>
    </template>
  </SettingDrawer>
</template>

<style scoped lang="less">
@import './datasource-surface.less';
.ds-steps {
  display: flex;
  gap: 8px;
  margin-bottom: 20px;
  border-bottom: 1px solid var(--td-component-stroke);
  padding-bottom: 14px;
}

.ds-step {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.ds-step-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ds-step.active {
  color: var(--td-brand-color);
  font-weight: 500;
}

.ds-step.done {
  color: var(--td-text-color-secondary);
  font-weight: 500;
}

.ds-step-num {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--app-text-sm);
  font-weight: 600;
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  background: transparent;
}

.ds-step.active .ds-step-num {
  background: var(--td-brand-color);
  color: #fff;
  border-color: var(--td-brand-color);
}

.ds-step.done .ds-step-num {
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  color: var(--td-brand-color);
  border-color: transparent;
}

.ds-step-check {
  font-size: var(--app-text-base);
}

.ds-loading-center {
  text-align: center;
  padding: 24px;
}

/* --- Step 0: type cards --- */
.ds-type-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 10px;
}

.ds-type-card {
  .ds-surface-card--interactive();
  padding: 14px;
  cursor: pointer;
  text-align: left;
  font: inherit;
  color: inherit;
}

.ds-type-card.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ds-type-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.ds-type-name {
  font-size: var(--app-text-md);
  font-weight: 600;
}

.ds-type-soon {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-component);
  padding: 1px 6px;
  border-radius: 3px;
}

.ds-type-desc {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

/* --- Step 1: setup guide + credentials (align with ModelEditor / CredentialResource) --- */
.inline-alert {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--app-text-md);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  flex-wrap: wrap;
}

.inline-alert__icon {
  font-size: var(--app-text-lg);
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.inline-alert__text {
  flex: 1 1 auto;
  min-width: 0;
}

.inline-alert__action {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-brand-color);
  white-space: nowrap;
  transition: color var(--app-motion-fast) ease;
}

.inline-alert__action:hover {
  color: var(--td-brand-color-active);
}

.ds-setup-guide {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ds-setup-guide--standalone {
  margin-bottom: 4px;
}

.ds-setup-guide__toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--app-text-md);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  text-align: left;
  cursor: pointer;
  transition: color var(--app-motion-instant) ease;
}

.ds-setup-guide__toggle:hover,
.ds-setup-guide__toggle:focus-visible {
  color: var(--td-text-color-primary);
  outline: none;
}

.ds-setup-guide__icon {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.ds-setup-guide__summary {
  flex: 1;
  min-width: 0;
}

.ds-setup-guide__chevron {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.ds-setup-guide__body {
  padding: 0 0 0 23px;
}

.ds-setup-steps {
  margin: 10px 0 0;
  padding: 0 0 0 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ds-setup-step {
  font-size: var(--app-text-md);
  line-height: 1.5;
  color: var(--td-text-color-primary);
}

.ds-setup-step__title {
  display: block;
  font-weight: 500;
  margin-bottom: 2px;
}

.ds-setup-step__desc {
  display: block;
  color: var(--td-text-color-secondary);
}

.ds-perm-tag {
  display: inline-block;
  font-size: var(--app-text-xs);
  padding: 1px 5px;
  margin: 2px 4px 2px 0;
  border-radius: 3px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family-mono, ui-monospace, monospace);
}

.ds-setup-guide__link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 10px;
  font-size: var(--app-text-md);
}

.credential-faux-input {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 4px 0 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-sm);
  font-size: var(--app-text-md);
  transition: border-color var(--app-motion-fast) ease, background-color var(--app-motion-fast) ease;
}

.credential-faux-input:hover {
  border-color: var(--td-brand-color-hover);
}

.credential-faux-input.is-empty {
  cursor: pointer;
}

.credential-faux-input.is-empty:hover {
  background: var(--td-bg-color-container-hover);
}

.credential-faux-input.is-confirm-remove {
  background: var(--td-error-color-light);
  border-color: var(--td-error-color-focus);
}

.credential-faux-text {
  flex: 1;
  min-width: 0;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.credential-faux-text.muted {
  color: var(--td-text-color-placeholder);
}

.credential-faux-text.danger {
  color: var(--td-error-color);
  font-weight: 500;
}

.credential-status-icon {
  flex-shrink: 0;
  font-size: var(--app-text-xl);
}

.credential-status-icon.success {
  color: var(--td-success-color);
}

.credential-status-icon.muted {
  color: var(--td-text-color-placeholder);
}

.credential-status-icon.warn {
  color: var(--td-error-color);
}

.credential-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.credential-actions :deep(.t-button--variant-text) {
  height: 24px;
  padding: 0 8px;
  font-size: var(--app-text-sm);
  border-radius: var(--app-radius-xs);
}

.action-divider {
  width: 1px;
  height: 14px;
  background: var(--td-component-stroke);
  margin: 0 2px;
}

.credential-edit-actions {
  display: flex;
  justify-content: flex-end;
}

.credential-edit-actions :deep(.t-button) {
  height: 28px;
  padding: 0 12px;
  font-size: var(--app-text-sm);
}

.form-item {
  margin-bottom: 0;
}

.form-item--flat {
  margin-bottom: 0;
}

.form-item--flat :deep(.t-checkbox__label) {
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.form-label {
  display: block;
  font-size: var(--app-text-md);
  font-weight: 500;
  margin-bottom: 6px;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  &.required::before {
    content: '*';
    color: var(--td-error-color);
    margin-right: 4px;
    font-weight: 500;
    line-height: 1;
  }
}

.form-desc {
  margin: 4px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.status-icon {
  font-size: var(--app-text-xl);
  flex-shrink: 0;
}

.status-icon.available {
  color: var(--td-brand-color);
}

.status-icon.unavailable {
  color: var(--td-error-color);
}

.footer-test-message {
  font-size: var(--app-text-sm);
  line-height: 1.4;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.footer-test-message.success {
  color: var(--td-brand-color-active);
}

.footer-test-message.error {
  color: var(--td-error-color);
}

/* --- Step 2: resource picker (compact flat tree, matches KB selector) --- */
.ds-resource-section {
  gap: 10px !important;
}

.ds-resource-hint {
  margin: -8px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

/* Drive (云盘) root folder_token input - shown before the lazy-load tree. */
.drive-folder-input {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--td-border-level-1-color);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
}

.drive-folder-input__label {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);

  /* 与 .form-label.required 一致的红星必填标记 */
  &.required::before {
    content: '*';
    color: var(--td-error-color);
    font-weight: 500;
    line-height: 1;
  }
}

.drive-folder-input__help {
  font-size: var(--app-text-lg);
  color: var(--td-text-color-placeholder);
  cursor: help;

  &:hover {
    color: var(--td-text-color-secondary);
  }
}

.drive-folder-input__row {
  display: flex;
  gap: 8px;
  align-items: center;
  padding-bottom: 20px
}

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

/* Drive tree placeholder: shown before the first successful load. */
.ds-drive-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 120px;
  padding: 24px 12px;
  border: 1px dashed var(--td-border-level-2-color);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-page);
  text-align: center;
}

.ds-drive-placeholder .ds-empty-title {
  margin: 0;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.ds-drive-placeholder .ds-empty-desc {
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.resource-picker {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* Expander row that opens the (default-collapsed) knowledge-base picker while
   a manual reference exists. The label itself says the tree adds to the
   preview instead of replacing it. */
.resource-picker__tree-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  align-self: flex-start;
  padding: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: color var(--app-motion-instant) ease;
}

.resource-picker__tree-toggle:hover,
.resource-picker__tree-toggle:focus-visible {
  color: var(--td-brand-color);
  outline: none;
}

.resource-picker__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 0;
  background: transparent;
}

.resource-picker__count {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.resource-picker__actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.resource-picker__action {
  padding: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color var(--app-motion-instant) ease;
}

.resource-picker__action:hover,
.resource-picker__action:focus-visible {
  color: var(--td-text-color-secondary);
  outline: none;
}

.resource-picker__action-sep {
  color: var(--td-text-color-disabled);
  font-size: var(--app-text-sm);
  user-select: none;
}

.resource-picker__list {
  .ds-inset-panel();
  min-height: 360px;
  max-height: min(calc(100vh - 260px), 600px);
  overflow-y: auto;
  padding: 4px 6px;
  overscroll-behavior: contain;
}

.resource-picker__row {
  --depth: 0;
  position: relative;
  display: grid;
  grid-template-columns: 16px 16px 16px 1fr;
  align-items: center;
  column-gap: 8px;
  min-height: 34px;
  margin-bottom: 2px;
  padding: 5px 8px 5px calc(8px + var(--depth) * 14px);
  border-radius: var(--app-radius-sm);
  cursor: pointer;
  transition: background var(--app-motion-instant) ease;
}

.resource-picker__row:last-child {
  margin-bottom: 0;
}

.resource-picker__row:hover,
.resource-picker__row.is-checked,
.resource-picker__row.is-indeterminate {
  background: var(--td-bg-color-secondarycontainer);
}

.resource-picker__expand,
.resource-picker__expand-spacer {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}

.resource-picker__expand {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background var(--app-motion-instant) ease, color var(--app-motion-instant) ease;
}

.resource-picker__expand:hover,
.resource-picker__expand:focus-visible {
  background: color-mix(in srgb, var(--td-text-color-placeholder) 12%, transparent);
  color: var(--td-text-color-secondary);
  outline: none;
}

.resource-picker__check {
  width: 16px;
  height: 16px;
  border-radius: 3px;
  border: 1.5px solid var(--td-component-border);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  box-sizing: border-box;
  transition: background var(--app-motion-instant) ease, border-color var(--app-motion-instant) ease;
}

.resource-picker__check.is-checked,
.resource-picker__check.is-indeterminate {
  background: var(--td-brand-color);
  border-color: var(--td-brand-color);
}

.resource-picker__check.is-indeterminate::after {
  content: '';
  width: 8px;
  height: 2px;
  border-radius: 1px;
  background: #fff;
}

.resource-picker__icon {
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}

.resource-picker__label {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.resource-picker__name {
  min-width: 0;
  font-size: var(--app-text-md);
  line-height: 1.4;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.resource-picker__type {
  flex-shrink: 0;
  font-size: var(--app-text-2xs);
  line-height: 1;
  padding: 2px 5px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);
  background: color-mix(in srgb, var(--td-text-color-placeholder) 8%, transparent);
}

/* One line saying what a described row syncs. It is what keeps the wiki
   documents listed under a 多维表 from reading as that Base's content. */
.resource-picker__hint {
  flex-shrink: 0;
  margin-left: auto;
  padding-left: 12px;
  font-size: var(--app-text-2xs);
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 45%;
}

.resource-picker__notice {
  --depth: 0;
  margin: 0 0 2px;
  padding: 4px 8px 4px calc(8px + var(--depth) * 14px);
  font-size: var(--app-text-xs);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.resource-picker__notice:last-child {
  margin-bottom: 0;
}

/* --- Step 2: empty state --- */
.ds-resource-empty {
  text-align: center;
  padding: 24px 0;
}

.ds-empty-title {
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 4px;
}

.ds-empty-desc {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  margin: 0 0 16px;
}

.ds-guide-steps {
  display: flex;
  flex-direction: column;
  gap: 8px;
  text-align: left;
  max-width: 440px;
  margin: 0 auto 16px;
}

.ds-guide-step {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  line-height: 1.5;
}

.ds-guide-num {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-top: 1px;
}

.ds-empty-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 16px;
}

.custom-headers-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.custom-headers-desc {
  margin: 0 0 10px 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.custom-headers-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.custom-header-row {
  display: flex;
  align-items: center;
  gap: 8px;

  .custom-header-key {
    flex: 0 0 38%;
  }

  .custom-header-value {
    flex: 1;
  }

  .custom-header-remove {
    flex-shrink: 0;
    width: 32px;
    height: 32px;
    padding: 0;
    color: var(--td-text-color-placeholder);
    border-radius: var(--app-radius-sm);
    transition: all 0.18s ease;

    &:hover {
      background: var(--td-error-color-light);
      color: var(--td-error-color);
    }
  }
}

.ds-empty-retry {
  padding: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-brand-color);
  cursor: pointer;
  transition: color var(--app-motion-instant) ease;
}

.ds-empty-retry:hover,
.ds-empty-retry:focus-visible {
  color: var(--td-brand-color-active);
  outline: none;
}

/* --- Step 3: sync strategy option pills --- */
.option-group {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  width: fit-content;
  max-width: 100%;
}

.option-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 4px 10px;
  min-height: 28px;
  border: 1px solid transparent;
  border-radius: var(--app-radius-sm);
  background: transparent;
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 1.3;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  white-space: nowrap;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;
}

.option-pill:hover {
  color: var(--td-text-color-primary);
}

.option-pill:focus-visible {
  outline: 2px solid var(--td-brand-color);
  outline-offset: 1px;
}

.option-pill.is-active {
  background: var(--td-bg-color-container);
  border-color: var(--td-component-stroke);
  color: var(--td-text-color-primary);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.05);
}

.gitlab-project-list {
  display: grid;
  gap: 12px;
  margin-bottom: 20px;
}

.gitlab-project-row {
  display: grid;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
}

.gitlab-project-row__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>

<!--
  Drawer header logo — same white badge as the data-source list cards.
-->
<style lang="less">
.datasource-editor-drawer .setting-drawer__header-icon:has(.datasource-header-icon__img) {
  background: var(--td-bg-color-container);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.datasource-header-icon__img {
  display: block;
  width: 24px;
  height: 24px;
  object-fit: contain;
}

/* Step 2「选择范围」:整步不滚动 —— token 输入区固定,下方资源区域
   (占位 / 加载 / 空态 / 目录树)撑满抽屉剩余高度,树列表内部滚动。 */
.ds-fixed-step {
  .t-drawer__body {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .setting-drawer__body {
    flex: 1;
    min-height: 0;
  }

  .ds-resource-section {
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .resource-picker,
  .ds-drive-placeholder,
  .ds-loading-center,
  .ds-resource-empty {
    flex: 1;
    min-height: 0;
  }

  .ds-loading-center {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .resource-picker__list {
    flex: 1;
    min-height: 0;
    max-height: none;
  }
}
</style>
