import { INTEGRATION_TABS, type IntegrationTab } from './integrations'

export const INTEGRATION_SECTION_PREFIX = 'integration-'

type QueryValue = string | number | null | undefined | Array<string | number | null>
export type SettingsRouteQuery = Record<string, QueryValue>

export function integrationSectionKey(tab: IntegrationTab): string {
  return `${INTEGRATION_SECTION_PREFIX}${tab}`
}

export function integrationTabFromSection(section: string): IntegrationTab {
  const raw = section.startsWith(INTEGRATION_SECTION_PREFIX)
    ? section.slice(INTEGRATION_SECTION_PREFIX.length)
    : section
  if (INTEGRATION_TABS.includes(raw as IntegrationTab)) {
    return raw as IntegrationTab
  }
  return 'im'
}

export function isIntegrationSection(section: string): boolean {
  if (!section.startsWith(INTEGRATION_SECTION_PREFIX)) return false
  const raw = section.slice(INTEGRATION_SECTION_PREFIX.length)
  return (INTEGRATION_TABS as readonly string[]).includes(raw)
}

function isBareIntegrationTab(section: string): section is IntegrationTab {
  return (INTEGRATION_TABS as readonly string[]).includes(section)
}

/**
 * Map URL `section` (and a leftover `tab` from old bookmarks) onto the
 * settings nav key. Canonical form is `integration-<tab>`; `integrations`,
 * `api`, and bare tab names remain aliases.
 */
export function normalizeSettingsSection(section: string, tab?: string | null): string {
  if (section === 'integrations') {
    return integrationSectionKey(integrationTabFromSection(tab || 'im'))
  }
  if (isBareIntegrationTab(section)) {
    return integrationSectionKey(section)
  }
  return section
}

/**
 * Settings left-nav → URL. Every page, including integrations, is
 * `?section=<navKey>` (e.g. `integration-claw`). `tab` is dropped.
 */
export function buildSettingsRouteQuery(
  sectionKey: string,
  currentQuery: object = {},
): SettingsRouteQuery {
  const query: SettingsRouteQuery = { ...(currentQuery as SettingsRouteQuery) }
  delete query.tab
  if (!isIntegrationSection(sectionKey)) {
    delete query.agentId
  }
  query.section = sectionKey
  return query
}

export function settingsQueryUnchanged(
  currentQuery: object,
  nextQuery: SettingsRouteQuery,
): boolean {
  const current = currentQuery as SettingsRouteQuery
  return String(current.section ?? '') === String(nextQuery.section ?? '')
    && current.tab == null
    && nextQuery.tab == null
    && String(current.agentId ?? '') === String(nextQuery.agentId ?? '')
}
