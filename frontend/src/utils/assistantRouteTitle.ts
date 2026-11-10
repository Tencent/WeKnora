import { TOOLBOX_ITEMS } from '../config/toolbox'

/** The bits of a route that name the page; vue-router's route fits. */
export interface TitledRoute {
  name?: string | symbol | null
  params?: Record<string, unknown>
}

// The sidebar's names for the pages. Settings is left out: it is a dialog
// over the page, and the dialog names itself.
const ROUTE_TITLE_KEYS: Record<string, string> = {
  knowledgeBaseList: 'menu.knowledgeBase',
  knowledgeBaseDetail: 'menu.knowledgeBase',
  home: 'menu.knowledgeBase',
  artifactLibrary: 'menu.artifacts',
  agentList: 'menu.agents',
  automationList: 'menu.automations',
  toolbox: 'toolbox.title',
  globalCreatChat: 'menu.newChat',
  kbCreatChat: 'menu.newChat',
  chat: 'assistant.context.pages.chat',
  organizationList: 'menu.organizations',
}

/** The page the route shows, as the sidebar names it; '' when it has no name. */
export function routeTitle(route: TitledRoute, t: (key: string) => string): string {
  const key = typeof route.name === 'string' ? ROUTE_TITLE_KEYS[route.name] : undefined
  if (!key) return ''
  const parts = [t(key)]
  if (route.name === 'toolbox') {
    const item = TOOLBOX_ITEMS.find(entry => entry.key === route.params?.section)
    if (item) parts.push(t(item.title))
  }
  return parts.join(' / ')
}
