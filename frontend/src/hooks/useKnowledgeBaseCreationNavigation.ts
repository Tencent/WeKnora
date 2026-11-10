import { useRouter } from 'vue-router'

/**
 * Provides a shared navigation helper for knowledge-base creation success.
 * Redirects to the knowledge-base list page and highlights the newly created KB.
 */
export const useKnowledgeBaseCreationNavigation = () => {
  const router = useRouter()

  const navigateToKnowledgeBaseList = (kbId: string) => {
    if (!kbId) return
    router.push({
      path: '/platform/knowledge-bases',
      query: { highlightKbId: kbId },
    })
  }

  return {
    navigateToKnowledgeBaseList,
  }
}

