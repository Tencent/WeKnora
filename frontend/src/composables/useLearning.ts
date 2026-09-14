import { onMounted, onScopeDispose } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { learningApi } from '@/api/learning'
import { learningQuizPointers } from './learningHelpers'
import { useLearningState } from './useLearningState'

export function useLearning(context: () => { kbId: string; available: boolean; pageId?: string; quizId?: string; slugs?: string[] }) {
  const auth = useAuthStore()
  let storage: Storage | undefined
  try { storage = window.sessionStorage } catch { /* Private browsing or server rendering. */ }
  const controller = useLearningState(() => ({
    ...context(),
    scope: { kbId: context().kbId, available: context().available && auth.isLoggedIn,
      tenantId: String(auth.effectiveTenantId || ''), userId: auth.currentUserId },
  }), learningApi, { pointers: learningQuizPointers(storage) })
  const refresh = () => { void controller.refreshConsent() }
  onMounted(() => window.addEventListener('focus', refresh))
  onScopeDispose(() => { if (typeof window !== 'undefined') window.removeEventListener('focus', refresh) })
  return controller
}
