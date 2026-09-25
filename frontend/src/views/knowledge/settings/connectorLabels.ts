// Display names of data source connectors. Builtins have locale keys
// (datasource.connector.<type>); plugin connectors bring localized names in
// their metadata. Kept free of Vue so it runs under node:test.
import { pickLocale } from '../../../utils/localizedText'

export { pickLocale }

/** The parts of connector metadata labels need. */
export interface ConnectorLabelSource {
  type: string
  name?: string
  description?: string
  names?: Record<string, string>
  descriptions?: Record<string, string>
}

/** The subset of vue-i18n the helpers use. */
export interface LabelI18n {
  t: (key: string) => string
  te: (key: string) => boolean
  locale: string
}

/** A connector's name: locale key, plugin variant, plugin default, type. */
export function connectorName(src: ConnectorLabelSource, i18n: LabelI18n): string {
  const key = `datasource.connector.${src.type}`
  if (i18n.te(key)) return i18n.t(key)
  return pickLocale(src.names, i18n.locale) || src.name || src.type
}

/** A connector's description, the same way; empty when there is none. */
export function connectorDescription(src: ConnectorLabelSource, i18n: LabelI18n): string {
  const key = `datasource.connectorDesc.${src.type}`
  if (i18n.te(key)) return i18n.t(key)
  return pickLocale(src.descriptions, i18n.locale) || src.description || ''
}

/** Channels plugin connectors ingest under: their qualified type ID. */
export function isPluginChannel(channel: string | undefined): boolean {
  return !!channel && channel.includes('/')
}
