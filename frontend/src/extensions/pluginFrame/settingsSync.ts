import { markRaw } from 'vue'

import i18n from '@/i18n'
import { localizedText } from '@/utils/localizedText'

import { settingsSections } from '../settingsSections'
import PluginFrame from './PluginFrame.vue'
import type { PluginPage } from './pluginPages'

const registered = new Map<string, () => void>()

/**
 * Makes the settings registry hold exactly these plugin sections: new ones
 * are registered under the plugins group, ones no longer enabled removed.
 */
export function syncPluginSettingsSections(pages: readonly PluginPage[]) {
  const wanted = new Map(pages.map((p) => [p.key, p]))
  for (const [key, unregister] of registered) {
    if (!wanted.has(key)) {
      unregister()
      registered.delete(key)
    }
  }
  for (const page of pages) {
    if (registered.has(page.key)) continue
    registered.set(
      page.key,
      settingsSections.register({
        key: page.key,
        group: 'plugins',
        order: 100 + page.order,
        label: () => localizedText(page.name, i18n.global.locale.value),
        icon: { kind: 'tdesign', name: 'app' },
        component: markRaw(PluginFrame),
        props: () => ({ page }),
        pluginId: page.pluginId,
        access: { minRole: page.minRole },
      }),
    )
  }
}
