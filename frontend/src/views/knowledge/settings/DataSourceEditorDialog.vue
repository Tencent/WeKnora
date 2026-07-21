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

function enterReplaceCredentials() {
  pendingRemoveCredentials.value = false
  replaceCredentialsMode.value = true
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
  sync_mode: 'incremental' as 'incremental' | 'full' | 'mirror',
  conflict_strategy: 'overwrite' as 'overwrite' | 'skip',
  sync_deletions: true,
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
  const result: { resource: Resource; depth: number }[] = []
  function walk(items: Resource[], depth: number) {
    for (const r of items) {
      result.push({ resource: r, depth })
      if (r.has_children && expandedResourceIds.value.has(r.external_id)) {
        walk(childrenMap.value.get(r.external_id) || [], depth + 1)
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

// Confluence edition: "server" (7.4.9) or "cloud".
// Drives which credential fields are shown and how the backend exports pages.
const confluenceEdition = ref('server')

// Extensible list of Confluence edition options for the dropdown.
// Add new editions here (e.g. "8.x", "Data Center") as needed.
const confluenceEditionOptions = [
  { label: 'Confluence 7.4.9', value: 'server' },
  { label: 'Confluence Cloud', value: 'cloud' },
]

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
    type: 'confluence',
    available: true,
    docUrl: '',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [
      { key: 'base_url', labelKey: 'datasource.field.confluenceBaseUrl', placeholder: 'https://confluence.example.com' },
      { key: 'username', labelKey: 'datasource.field.confluenceUsername', placeholder: '' },
      // password / api_token is injected dynamically via confluenceEffectiveFields
    ],
  },
])


const currentDef = computed(() => connectorDefs.value.find(d => d.type === form.value.type))

// Returns the credential fields to render / validate.
// For Confluence the secret field depends on the selected edition
// (password for Server, api_token for Cloud); for all other connectors
// it simply returns the static definition.
const effectiveCredentialFields = computed(() => {
  const base = currentDef.value?.fields || []
  if (form.value.type !== 'confluence') return base
  const secretField = confluenceEdition.value === 'cloud'
    ? { key: 'api_token', labelKey: 'datasource.field.confluenceApiToken', placeholder: '', secret: true }
    : { key: 'password', labelKey: 'datasource.field.confluencePassword', placeholder: '', secret: true }
  return [...base, secretField]
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
  rssAuthHeaders.value = []

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
    tempDsId.value = props.dataSource.id
    // Restore Confluence edition from settings (credentials are stripped
    // from API responses, but settings survive the round-trip).
    const savedEdition = (editConfig.settings as any)?.confluence_edition
    syncConfluenceEdition(savedEdition === 'cloud' ? 'cloud' : 'server')
  } else {
    replaceCredentialsMode.value = false
    credentialsConfigured.value = false
    syncConfluenceEdition('server')
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
  form.value.config.credentials = {}
  rssAuthHeaders.value = []
  if (def.type === 'confluence') {
    syncConfluenceEdition('server')
  }
  step.value = 1
}

// Keep confluenceEdition ref and form.value.config.credentials.edition in sync.
// All API calls spread credentials, so having edition there means no need for
// scattered `if (type === 'confluence')` checks at every call site.
function syncConfluenceEdition(ed: string) {
  confluenceEdition.value = ed
  form.value.config.credentials.edition = ed
}

// Called by the dropdown @change event (receives the new value).
function onConfluenceEditionChange(ed: string) {
  switchConfluenceEdition(ed)
}

// Switch Confluence edition from the dropdown.
// Clears the secret field that is no longer relevant so stale values
// don't get submitted accidentally.
function switchConfluenceEdition(ed: string) {
  if (confluenceEdition.value === ed) return
  syncConfluenceEdition(ed)
  delete form.value.config.credentials.password
  delete form.value.config.credentials.api_token
  testResult.value = ''
  testErrorMsg.value = ''
}

// --- Test connection (stateless, no DB write) ---
async function testConnection() {
  syncRssAuthHeadersToCredentials()
  if (!validateRssFeedUrls()) return
  if (!isEdit.value || !credentialsConfigured.value || replaceCredentialsMode.value) {
    const fields = effectiveCredentialFields.value
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
    if (isEdit.value && tempDsId.value && !replaceCredentialsMode.value) {
      // Edit mode without credential replacement: test using the stored
      // credentials (which the backend reads from DB).  The update call
      // persists any non-credential config changes (resource_ids, settings)
      // so that validateConnection sees the latest state.
      await updateDataSource(tempDsId.value, {
        ...form.value,
        config: { ...form.value.config, settings: { ...form.value.config.settings, confluence_edition: confluenceEdition.value } },
        knowledge_base_id: props.kbId,
      } as any)
      await validateConnection(tempDsId.value)
    } else {
      // Create mode OR edit mode with credential replacement:
      // Use the stateless validate-credentials endpoint so we test the
      // NEW values the user just typed, not the stale DB row.
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
async function loadResources() {
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
    } else if (!isEdit.value) {
      await updateDataSource(tempDsId.value, {
        ...form.value,
        knowledge_base_id: props.kbId,
      } as any)
    } else if (replaceCredentialsMode.value) {
      // Edit mode with credential replacement: persist the new credentials
      // from the form to the DB via the /credentials subresource BEFORE
      // listing resources.  Without this, listResources reads stale (or
      // possibly incomplete) credentials from the DB and fails with
      // errors like "missing base_url" even though the user filled all
      // fields correctly in the form.
      const filled = Object.entries(form.value.config.credentials).filter(
        ([, v]) => typeof v === 'string' ? v !== '' : v != null,
      )
      if (filled.length > 0) {
        await putDataSourceCredentials(tempDsId.value, Object.fromEntries(filled))
        credentialsConfigured.value = true
        replaceCredentialsMode.value = false
        form.value.config.credentials = {}
      }
    }

    const res = await listResources(tempDsId.value)
    resources.value = res?.data || res || []
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
  }
  loadingResources.value = false
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
  if ((checkStates.value.get(id) || 'unchecked') === 'unchecked') {
    checkResource(id, cover)
  } else {
    uncheckResource(id, cover)
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

  const fields = effectiveCredentialFields.value
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
  step.value++
  if (step.value === 2) {
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
  const settings = { ...form.value.config.settings }
  // Persist the Confluence edition in settings so it survives API round-trips
  // (credentials are stripped from responses, but settings are not).
  if (form.value.type === 'confluence') {
    settings.confluence_edition = confluenceEdition.value
  }
  // credentials already contains `edition` via syncConfluenceEdition,
  // so no extra injection needed here.
  return {
    credentials: isEdit.value ? {} : { ...form.value.config.credentials },
    resource_ids: form.value.config.resource_ids,
    settings,
  }
}

// In edit mode, when the user opted in to Replace credentials and typed at
// least one value, commit it to /credentials before the main PUT. Aborts
// the whole submit on failure so we don't leave the row partially saved.
async function commitCredentialsIfNeeded(dsId: string): Promise<boolean> {
  if (!isEdit.value || !replaceCredentialsMode.value) return true
  syncRssAuthHeadersToCredentials()
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
  return count
})

const hasExpandableNodes = computed(() => resources.value.some(r => r.has_children))

function resourceIconName(r: Resource): string {
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
  confluence_space: 'datasource.resourceType.confluenceSpace',
}

function resourceTypeLabel(type: string): string {
  const key = resourceTypeLabelMap[type]
  if (key) return t(key)
  return ''
}

function shouldShowResourceType(type: string): boolean {
  return !!resourceTypeLabelMap[type]
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
    :class="form.type ? `datasource-editor-drawer datasource-editor-drawer--${form.type}` : 'datasource-editor-drawer'"
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
          <!-- Confluence edition selector (dropdown) -->
          <div v-if="form.type === 'confluence'" class="form-item">
            <label class="form-label">{{ t('datasource.field.confluenceEditionLabel') }}</label>
            <t-select :value="confluenceEdition" @change="onConfluenceEditionChange">
              <t-option
                v-for="opt in confluenceEditionOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </t-select>
          </div>
          <div
            v-for="field in effectiveCredentialFields"
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
      <h4 class="setting-drawer__section-title">{{ t('datasource.step.resources') }}</h4>
      <p class="ds-resource-hint">{{ t('datasource.resourceHint') }}</p>
      <div v-if="loadingResources" class="ds-loading-center"><t-loading /></div>
      <div v-else-if="resources.length > 0" class="resource-picker">
        <div class="resource-picker__toolbar">
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
        <div class="resource-picker__list" role="tree">
          <div
            v-for="{ resource: r, depth } in visibleTree"
            :key="r.external_id"
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
          </div>
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
            <button
              v-if="form.type === 'confluence'"
              type="button"
              class="option-pill"
              :class="{ 'is-active': form.sync_mode === 'mirror' }"
              role="radio"
              :aria-checked="form.sync_mode === 'mirror'"
              @click="form.sync_mode = 'mirror'"
            >
              {{ t('datasource.syncMode.mirror') }}
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
  font-size: 13px;
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
  font-size: 12px;
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
  font-size: 14px;
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
  font-size: 13px;
  font-weight: 600;
}

.ds-type-soon {
  font-size: 10px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-component);
  padding: 1px 6px;
  border-radius: 3px;
}

.ds-type-desc {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

/* --- Step 1: setup guide + credentials (align with ModelEditor / CredentialResource) --- */
.inline-alert {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  flex-wrap: wrap;
}

.inline-alert__icon {
  font-size: 15px;
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
  font-size: 13px;
  font-weight: 500;
  color: var(--td-brand-color);
  white-space: nowrap;
  transition: color 0.15s ease;
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
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  text-align: left;
  cursor: pointer;
  transition: color 0.12s ease;
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
  font-size: 13px;
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
  font-size: 11px;
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
  font-size: 13px;
}

.credential-faux-input {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 4px 0 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border, var(--td-component-stroke));
  border-radius: 6px;
  font-size: 13px;
  transition: border-color 0.15s ease, background-color 0.15s ease;
}

.credential-faux-input:hover {
  border-color: var(--td-brand-color-hover, var(--td-brand-color));
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
  font-size: 16px;
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
  font-size: 12px;
  border-radius: 4px;
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
  font-size: 12px;
}

.form-item {
  margin-bottom: 0;
}

.form-item--flat {
  margin-bottom: 0;
}

.form-item--flat :deep(.t-checkbox__label) {
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.form-label {
  display: block;
  font-size: 13px;
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
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.status-icon {
  font-size: 16px;
  flex-shrink: 0;
}

.status-icon.available {
  color: var(--td-brand-color);
}

.status-icon.unavailable {
  color: var(--td-error-color);
}

.footer-test-message {
  font-size: 12px;
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
  white-space: normal;
  overflow: visible;
  text-overflow: unset;
  word-break: break-all;
}

/* --- Step 2: resource picker (compact flat tree, matches KB selector) --- */
.ds-resource-section {
  gap: 10px !important;
}

.ds-resource-hint {
  margin: -8px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.resource-picker {
  display: flex;
  flex-direction: column;
  gap: 6px;
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
  font-size: 12px;
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
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color 0.12s ease;
}

.resource-picker__action:hover,
.resource-picker__action:focus-visible {
  color: var(--td-text-color-secondary);
  outline: none;
}

.resource-picker__action-sep {
  color: var(--td-text-color-disabled);
  font-size: 12px;
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
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.12s ease;
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
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background 0.12s ease, color 0.12s ease;
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
  border: 1.5px solid var(--td-component-border, var(--td-component-stroke));
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  box-sizing: border-box;
  transition: background 0.12s ease, border-color 0.12s ease;
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
  font-size: 13px;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.resource-picker__type {
  flex-shrink: 0;
  font-size: 10px;
  line-height: 1;
  padding: 2px 5px;
  border-radius: 4px;
  color: var(--td-text-color-placeholder);
  background: color-mix(in srgb, var(--td-text-color-placeholder) 8%, transparent);
}

/* --- Step 2: empty state --- */
.ds-resource-empty {
  text-align: center;
  padding: 24px 0;
}

.ds-empty-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 4px;
}

.ds-empty-desc {
  font-size: 12px;
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
  font-size: 13px;
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
  font-size: 11px;
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
  font-size: 12px;
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
    border-radius: 6px;
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
  font-size: 13px;
  font-weight: 500;
  color: var(--td-brand-color);
  cursor: pointer;
  transition: color 0.12s ease;
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
  border-radius: 8px;
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
  border-radius: 6px;
  background: transparent;
  font: inherit;
  font-size: 12px;
  line-height: 1.3;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.15s ease, color 0.15s ease, border-color 0.15s ease;
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
</style>

<!--
  Drawer header logo — same white badge as list cards / StorageEngineSettings.
-->
<style lang="less">
.datasource-editor-drawer .setting-drawer__header-icon:has(.datasource-header-icon__img) {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.datasource-header-icon__img {
  display: block;
  width: 24px;
  height: 24px;
  object-fit: contain;
}
</style>
