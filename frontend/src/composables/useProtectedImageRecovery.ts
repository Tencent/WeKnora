import { nextTick, watch } from 'vue'
import { clearProtectedFileFailureCache, hydrateProtectedFileImages } from '../utils/security.ts'
import type { ProtectedFileAccessContext } from '../utils/protectedFileAccess.ts'

/** Retry after persisted completion/full reveal or a corrected authorization scope. */
export function useProtectedImageRecovery(
  root: () => ParentNode | null | undefined,
  access: () => ProtectedFileAccessContext | undefined,
  ready: () => boolean,
) {
  watch([ready, () => JSON.stringify(access() ?? null)], ([done, scope], [wasDone, previousScope]) => {
    if (!(done && !wasDone) && (!previousScope || scope === previousScope)) return
    clearProtectedFileFailureCache()
    void nextTick(() => hydrateProtectedFileImages(root(), access()))
  }, { immediate: true })
}
