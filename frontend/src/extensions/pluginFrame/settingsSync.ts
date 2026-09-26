import { markRaw } from 'vue'

import i18n from '@/i18n'
import { localizedText } from '@/utils/localizedText'

import { settingsSections, type SettingsNavIcon } from '../settingsSections'
import PluginSettingsSection from './PluginSettingsSection.vue'
import { samePage, type PluginPage } from './pluginPages'

const registered = new Map<string, { page: PluginPage; icon: string | undefined; unregister: () => void }>()

/**
 * Makes the settings registry hold exactly these plugin sections: new ones
 * are registered under the plugins group, ones no longer enabled removed,
 * and ones that changed (a new plugin version serves its files under another
 * path) registered again.
 */
export function syncPluginSettingsSections(pages: readonly PluginPage[], iconOf?: (page: PluginPage) => string | undefined) {
  const wanted = new Map(pages.map((p) => [p.key, p]))
  for (const [key, entry] of registered) {
    const page = wanted.get(key)
    if (!page || !samePage(entry.page, page) || entry.icon !== iconOf?.(page)) {
      entry.unregister()
      registered.delete(key)
    }
  }
  for (const page of pages) {
    if (registered.has(page.key)) continue
    const icon = iconOf?.(page)
    registered.set(page.key, {
      page,
      icon,
      unregister: settingsSections.register({
        key: page.key,
        group: 'plugins',
        order: 100 + page.order,
        label: () => localizedText(page.name, i18n.global.locale.value),
        icon: icon ? { kind: 'image', url: icon } : DEFAULT_ICON,
        component: markRaw(PluginSettingsSection),
        props: () => ({ page }),
        pluginId: page.pluginId,
        access: { minRole: page.minRole },
      }),
    })
  }
}

const DEFAULT_ICON: SettingsNavIcon = { kind: 'tdesign', name: 'app' }
