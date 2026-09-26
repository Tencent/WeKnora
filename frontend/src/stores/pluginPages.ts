import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import { listContributions, type ContributionListing } from '@/api/plugin'
import { useAuthStore } from '@/stores/auth'
import { pagesOf, type PagePoint, type PluginPage } from '@/extensions/pluginFrame/pluginPages'

import { createCachedResource } from './resourceCache'

/**
 * The plugin pages of the current workspace (toolbox pages, settings
 * sections, knowledge base tabs). No TTL: reloaded when the workspace
 * changes, and invalidated when a plugin is switched on or off.
 */
export const usePluginPagesStore = defineStore('pluginPages', () => {
  const listing = ref<ContributionListing | null>(null)
  const loadedFor = ref<string | number | null>(null)
  const auth = useAuthStore()

  const resource = createCachedResource(
    async () => (await listContributions()).data,
    (value) => {
      listing.value = value
    },
  )

  /** Loads the pages for the current workspace, if not loaded yet. */
  async function ensure(force = false) {
    const tenant = auth.currentTenantId ?? null
    if (tenant !== loadedFor.value) {
      resource.invalidate()
      listing.value = null
      loadedFor.value = tenant
    }
    if (force || !resource.isLoaded()) await resource.ensure(force)
  }

  function invalidate() {
    resource.invalidate()
    loadedFor.value = null
  }

  const visible = (point: PagePoint) =>
    computed<PluginPage[]>(() =>
      pagesOf(listing.value, point).filter((p) => auth.canAccessAllTenants || auth.hasRole(p.minRole)),
    )

  return {
    ensure,
    invalidate,
    pages: visible('pages'),
    settingsSections: visible('settingsSections'),
    kbTabs: visible('kbTabs'),
  }
})
