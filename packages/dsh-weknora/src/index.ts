/**
 * dsh-weknora: a DeepSeek Harness plugin that gives the agent retrieval,
 * document reading and composed answers from a WeKnora knowledge base.
 * @module dsh-weknora
 */

import { WeknoraClient } from './client.ts'
import { resolveConfig } from './config.ts'
import type { HarnessContext } from './harness.ts'
import { createTools } from './tools.ts'

export const name = 'dsh-weknora'

/** Cordis waits for the tool registry before applying this plugin. */
export const inject = ['tools'] as const

export type { Config } from './config.ts'
export { ConfigError, normalizeBaseUrl, resolveConfig } from './config.ts'
export { WeknoraApiError, WeknoraClient } from './client.ts'
export { createTools } from './tools.ts'

/**
 * Register the configured tools. Each registration is an effect, so unloading
 * or reconfiguring the plugin withdraws the tools without a restart.
 * @param ctx - the Cordis context, with `ctx.tools` injected.
 * @param config - the plugin's `config` row; validated here so a typo fails the load.
 */
export function apply(ctx: HarnessContext, config: unknown): void {
  const resolved = resolveConfig(config as never)
  const client = new WeknoraClient(resolved)
  const registered: string[] = []
  for (const definition of createTools(client, resolved)) {
    ctx.tools.register(definition)
    registered.push(definition.name)
  }
  ctx.logger?.info(`dsh-weknora: registered ${registered.join(', ')} against ${resolved.baseUrl}`)
}
