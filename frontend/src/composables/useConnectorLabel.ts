import { useI18n } from 'vue-i18n'

/**
 * Format a data source connector type ('dingtalk', 'feishu', …) into the
 * user-facing name declared under datasource.connector.* in the i18n bundle.
 *
 * The type icon's alt text, the card subtitle and the connector picker all render
 * this name, so they resolve it through one function and cannot disagree about
 * what a connector is called. They used to: the icon carried the raw type while
 * the label right next to it showed the translation, which put the internal
 * identifier "dingtalk" into the icon's alt text and into any text copied off the
 * page.
 *
 * The lookup deliberately avoids `t(key) || type`: vue-i18n echoes the key back
 * when the locale carries no message for it, so that expression never falls back
 * and a connector without a translation would render as
 * "datasource.connector.github" — worse than the raw type it was meant to rescue.
 * The connector registry ships several types with no message (github, imap,
 * google_drive, …). `te` tells us a real message exists; the `|| type` after it
 * only covers a message that is present but empty.
 *
 * Same shape as useRoleLabel's formatRole, which solves the same echo problem for
 * tenantMember.role.* by comparing the result with the key.
 */
export function useConnectorLabel() {
  const { t, te } = useI18n()
  const connectorLabel = (type: string | null | undefined): string => {
    if (!type) return ''
    const key = `datasource.connector.${type}`
    return (te(key) ? t(key) : '') || type
  }
  return { connectorLabel }
}
