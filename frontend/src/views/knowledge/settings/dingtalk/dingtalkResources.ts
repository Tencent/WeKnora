import type { Resource } from '@/api/datasource'

// Two kinds of DingTalk content are readable by id but can never appear in the
// lazy-load tree — a document in the operator's personal space (GET
// /v2.0/wiki/workspaces returns team workspaces only) and a multi-dimensional
// table (no DingTalk API lists Bases at all). The user therefore pastes a link
// or an id, the way the Drive connectors take a folder_token, and the selector
// decides between the two reference forms below.

// DINGTALK_MANUAL_REFERENCE matches exactly what the manual entry writes and
// nothing the tree writes: a node or Base reference carrying no workspace.
// Tree-picked ids always carry workspace=... (and usually ancestor=...), so they
// can never match.
export const DINGTALK_MANUAL_REFERENCE = /^dingtalk:v1\?(?:node|base)=[^&]+$/

// DINGTALK_DESCRIBE_PARENT_PREFIX asks the connector to DESCRIBE one reference
// instead of listing its children. A manual selection is invisible to every
// listing — nothing enumerates Bases, and a personal-space node is absent from
// the workspace listing — so this is the only call that can turn the id the user
// pasted into the name the sync will use. It is a picker-only request form: the
// connector answers with one row for the reference itself, which is why it can
// share the resource endpoint with expansion without ever being confused for it.
export const DINGTALK_DESCRIBE_PARENT_PREFIX = 'dingtalk:v1?describe='

// dingtalkManualReference renders the resource_id for one manual entry. The kind
// is what separates a wiki node (dingtalk:v1?node=<id>) from an independent
// multi-dimensional table (dingtalk:v1?base=<id>); the backend rejects a
// reference that mixes the two forms.
export function dingtalkManualReference(id: string, kind: 'node' | 'base'): string {
  return kind === 'base' ? `dingtalk:v1?base=${id}` : `dingtalk:v1?node=${id}`
}

// dingtalkManualReferenceId reads the stored id back out of a manual reference,
// so a row shows the id the user entered rather than the raw resource_id.
export function dingtalkManualReferenceId(reference: string): string {
  return reference.slice(reference.indexOf('=') + 1)
}

// dingtalkManualKind reads the kind back out of a stored reference without
// decoding the resource id by hand.
export function dingtalkManualKind(reference: string): 'node' | 'base' {
  return reference.startsWith('dingtalk:v1?base=') ? 'base' : 'node'
}

// extractDingTalkId accepts a bare node/Base id or a DingTalk link
// (https://alidocs.dingtalk.com/i/nodes/<ID>, with or without a query string
// such as ?utm_scene=...) and returns the id. Matching is path-based, so any
// host works. Unlike the Drive extractor there is deliberately no "last path
// segment" fallback: a document link and a Base link are byte-identical, so an
// id guessed out of an unrecognised URL would be more likely wrong than right.
// Returns "" when nothing usable is found.
export function extractDingTalkId(input: string): string {
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

// dingtalkTableNames splits the table list the connector puts in a Base row's
// description — notableTableNames joins the names with ", " in listing order.
// The picker re-renders that same list with a separator a table line reads
// better with; it never invents, reorders or drops a name.
export function dingtalkTableNames(description: string): string[] {
  return (description || '').split(',').map(name => name.trim()).filter(Boolean)
}

// dingtalkResourceHint explains, in one line, what selecting a described row
// syncs. It is rendered for a Base both in the tree and — this is the point of
// the line — under the Base's own row in the selection preview, where the tables
// a base= reference ingests must be readable without expanding anything. Without
// it the documents listed under the Base would read as "the Base's contents",
// which they are not: they are separate selections.
export function dingtalkResourceHint(
  r: Resource,
  t: (key: string, params?: Record<string, unknown>) => string,
): string {
  if (r.type !== 'base') return ''
  const tables = dingtalkTableNames(r.description || '')
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
