import { pluginFormOptions, startPluginOAuth, type PluginFormScope } from '@/api/plugin'

import type { ConfigValue } from './schema'
import { valueAt, type SchemaFormSource } from './source'

export interface PluginFormTarget {
  pluginId: string
  scope: PluginFormScope
  /** "<point>/<id>" for instance forms. */
  contribution?: string
  instanceId?: string
}

export interface PluginFormBinding {
  target: () => PluginFormTarget | undefined
  /** What the form holds now, in the shape the plugin's schema has. */
  values: () => ConfigValue
  /** A field's value by its path in the plugin's schema; defaults to a lookup in values(). */
  dependency?: (path: string) => unknown
  /** Maps a form path to the plugin schema's path, for forms that nest fields. */
  field?: (path: string) => string
}

/** A SchemaFormSource that asks the plugin behind a form. */
export function pluginFormSource(b: PluginFormBinding): SchemaFormSource {
  const need = () => {
    const t = b.target()
    if (!t) throw new Error('no plugin behind this form')
    return t
  }
  const field = (path: string) => (b.field ? b.field(path) : path)
  return {
    dependency: path => (b.dependency ? b.dependency(path) : valueAt(b.values(), path)),
    async options(path, source, query) {
      const t = need()
      const res = await pluginFormOptions(t.pluginId, {
        name: source.name, field: field(path), scope: t.scope, contribution: t.contribution,
        instanceId: t.instanceId, values: b.values(), query: query || undefined,
      })
      return res.data?.options ?? []
    },
    async startOAuth(path) {
      const t = need()
      const res = await startPluginOAuth(t.pluginId, {
        scope: t.scope, contribution: t.contribution, field: field(path),
      })
      return res.data
    },
  }
}
