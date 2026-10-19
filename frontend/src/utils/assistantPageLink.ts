import type {
  AssistantMcpServiceDraft,
  AssistantModelDraft,
  AssistantPageDraft,
} from '@/stores/assistant'

/** The open_page tool's result: a checked link, optionally with a form to fill in. */
export interface AssistantPageLink {
  display_type: 'open_page'
  page:
    | 'knowledge_base' | 'wiki' | 'agent_editor' | 'settings_models' | 'mcp_services'
    | 'settings' | 'toolbox' | 'agents' | 'knowledge_bases' | 'organizations'
  label: string
  detail?: string
  note?: string
  knowledge_base_id?: string
  knowledge_id?: string
  wiki_slug?: string
  agent_id?: string
  section?: string
  /** A tab inside a settings section, e.g. system-global's users. */
  panel?: string
  model_id?: string
  model_draft?: AssistantModelDraft
  mcp_service_id?: string
  mcp_service_draft?: AssistantMcpServiceDraft
  /** The assistant opened the page itself; return_path is where the user was. */
  navigated?: boolean
  return_path?: string
}

/** Where a navigated link's back button goes: an in-app path only. */
export function pageLinkReturnPath(link: AssistantPageLink): string {
  const path = link.navigated ? link.return_path || '' : ''
  return path.startsWith('/') && !path.startsWith('//') && !path.includes('\\') ? path : ''
}

export interface PageLinkLocation {
  path: string
  query: Record<string, string>
}

export function isAssistantPageLink(value: unknown): value is AssistantPageLink {
  const link = value as AssistantPageLink | null
  return !!link && link.display_type === 'open_page' && typeof link.page === 'string' && typeof link.label === 'string'
}

/** Where the link goes, using the deep links the pages already read. */
export function pageLinkLocation(link: AssistantPageLink): PageLinkLocation | null {
  switch (link.page) {
    case 'knowledge_base':
      if (!link.knowledge_base_id) return null
      return {
        path: `/platform/knowledge-bases/${encodeURIComponent(link.knowledge_base_id)}`,
        query: link.knowledge_id ? { knowledge_id: link.knowledge_id } : {},
      }
    case 'wiki':
      if (!link.knowledge_base_id) return null
      return {
        path: `/platform/knowledge-bases/${encodeURIComponent(link.knowledge_base_id)}`,
        query: { tab: 'wiki', ...(link.wiki_slug ? { slug: link.wiki_slug } : {}) },
      }
    case 'agent_editor':
      if (!link.agent_id) return null
      return {
        path: '/platform/agents',
        query: { edit: link.agent_id, ...(link.section ? { section: link.section } : {}) },
      }
    case 'settings_models':
      return {
        path: '/platform/settings',
        query: { section: 'models', ...(link.model_id ? { model_id: link.model_id } : {}) },
      }
    case 'mcp_services':
      return {
        path: '/platform/toolbox/mcp',
        query: link.mcp_service_id ? { service_id: link.mcp_service_id } : {},
      }
    case 'settings':
      return {
        path: '/platform/settings',
        query: {
          ...(link.section ? { section: link.section } : {}),
          ...(link.panel ? { panel: link.panel } : {}),
        },
      }
    case 'toolbox':
      return {
        path: link.section ? `/platform/toolbox/${encodeURIComponent(link.section)}` : '/platform/toolbox',
        query: {},
      }
    case 'agents':
      return { path: '/platform/agents', query: {} }
    case 'knowledge_bases':
      return { path: '/platform/knowledge-bases', query: {} }
    case 'organizations':
      return { path: '/platform/organizations', query: {} }
    default:
      return null
  }
}

/** The form the link's page should open filled in, if any. */
export function pageLinkDraft(link: AssistantPageLink): AssistantPageDraft | null {
  if (link.page === 'settings_models' && link.model_draft && !link.model_id) {
    return { kind: 'model', draft: { ...link.model_draft } }
  }
  if (link.page === 'mcp_services' && link.mcp_service_draft && !link.mcp_service_id) {
    return { kind: 'mcp_service', draft: { ...link.mcp_service_draft } }
  }
  return null
}

/** The endpoint a draft would connect to, shown so the user can check it before typing a key. */
export function pageLinkEndpoint(link: AssistantPageLink): string {
  return link.model_draft?.base_url || link.mcp_service_draft?.url || ''
}
