import { onScopeDispose, watch } from 'vue'
import { releaseProtectedFileImages } from '../utils/security.ts'

/** Pair hydration with root replacement (v-if/drawers) and component teardown. */
export function useProtectedImageLifecycle(root: () => ParentNode | null | undefined): void {
  let current = root()
  watch(root, (value) => {
    if (current !== value) releaseProtectedFileImages(current)
    current = value
  }, { flush: 'post' })
  onScopeDispose(() => {
    releaseProtectedFileImages(current)
    releaseProtectedFileImages(root())
  })
}
