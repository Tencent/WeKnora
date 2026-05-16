// useListUrlState keeps a list view's filter state (sidebar scope, creator
// filter, free-text query) reflected in the route's query string so the
// URL is shareable, bookmarkable and survives a browser back/forward.
//
// Why a composable instead of inlining: KnowledgeBaseList and AgentList
// share the same three filters and the same URL-encoding rules. Centralising
// here means changing the schema (e.g. renaming `scope` -> `view`) is a
// one-line change for both pages.
//
// Schema:
//   ?scope=<all|mine|shared|orgId>
//   ?creator=<all|mine|others>
//   ?q=<text>
//
// Defaults are omitted from the URL to keep it terse and to avoid producing
// a no-op history entry on first mount. We use router.replace (not push)
// for state changes so the back button still leaves the list view, rather
// than cycling through filter changes the user didn't think of as
// navigation.

import { ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

export type CreatorFilter = 'all' | 'mine' | 'others'

export interface ListUrlState {
  scope: Ref<string>
  creator: Ref<CreatorFilter>
  query: Ref<string>
}

export interface ListUrlStateOptions {
  /** Default scope when no `?scope=` is present. Usually role-derived. */
  defaultScope: string
  /** Default creator filter when no `?creator=` is present. */
  defaultCreator?: CreatorFilter
}

export function useListUrlState(opts: ListUrlStateOptions): ListUrlState {
  const route = useRoute()
  const router = useRouter()

  const initScope = typeof route.query.scope === 'string' && route.query.scope
    ? route.query.scope
    : opts.defaultScope

  const initCreator: CreatorFilter =
    route.query.creator === 'mine' || route.query.creator === 'others' || route.query.creator === 'all'
      ? (route.query.creator as CreatorFilter)
      : (opts.defaultCreator ?? 'all')

  const initQuery = typeof route.query.q === 'string' ? route.query.q : ''

  const scope = ref<string>(initScope)
  const creator = ref<CreatorFilter>(initCreator)
  const query = ref<string>(initQuery)

  // Push URL when any filter changes. We diff against current route.query
  // to avoid a redundant router.replace when nothing actually changed (the
  // route guard would still fire and cause unnecessary watcher churn).
  const sync = () => {
    const next: Record<string, string> = { ...(route.query as Record<string, string>) }
    if (scope.value && scope.value !== opts.defaultScope) {
      next.scope = scope.value
    } else {
      delete next.scope
    }
    const cdef = opts.defaultCreator ?? 'all'
    if (creator.value && creator.value !== cdef) {
      next.creator = creator.value
    } else {
      delete next.creator
    }
    if (query.value) {
      next.q = query.value
    } else {
      delete next.q
    }
    const changed =
      next.scope !== route.query.scope ||
      next.creator !== route.query.creator ||
      next.q !== route.query.q
    if (!changed) return
    router.replace({ path: route.path, query: next }).catch(() => {
      // navigation duplication errors are harmless here; vue-router throws
      // when the user clicks the same scope twice in rapid succession.
    })
  }

  watch([scope, creator, query], sync)

  // When the route changes from elsewhere (e.g. user pastes a link or hits
  // back), pull the new values back into our refs. Guards against re-emit
  // by only updating when the parsed value differs.
  watch(
    () => route.query,
    (q) => {
      const ns = (typeof q.scope === 'string' && q.scope) ? q.scope : opts.defaultScope
      const nc: CreatorFilter =
        q.creator === 'mine' || q.creator === 'others' || q.creator === 'all'
          ? (q.creator as CreatorFilter)
          : (opts.defaultCreator ?? 'all')
      const nq = typeof q.q === 'string' ? q.q : ''
      if (scope.value !== ns) scope.value = ns
      if (creator.value !== nc) creator.value = nc
      if (query.value !== nq) query.value = nq
    }
  )

  return { scope, creator, query }
}
