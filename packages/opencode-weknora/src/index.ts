/**
 * opencode-weknora: an opencode plugin that gives the agent retrieval,
 * document reading and composed answers from a WeKnora knowledge base.
 *
 * Ported from WeKnora's `dsh-weknora` (DeepSeek Harness plugin); see
 * https://github.com/Tencent/WeKnora/tree/main/packages/dsh-weknora
 *
 * This module exports ONLY the plugin: opencode's plugin loader treats every
 * function export of the entry module as a plugin and calls it, so the
 * library surface (client, config, tools) stays in sibling modules that are
 * not re-exported here.
 * @module @wxg-prc-cpg/opencode-weknora
 */

import type { Plugin } from '@opencode-ai/plugin'

import { WeknoraClient } from './client.ts'
import { configFromEnv, resolveConfig, type Config } from './config.ts'
import { createTools } from './tools.ts'

export type { Config } from './config.ts'

/**
 * Register the configured tools.
 *
 * Configuration precedence: opencode plugin options over the `WEKNORA_*`
 * environment variables over the defaults — so one environment serves many
 * agents while a single project overrides just what differs:
 *
 * ```json
 * { "plugin": [["opencode-weknora", { "baseUrl": "https://weknora.example.com", "knowledgeBaseIds": ["kb-docs"] }]] }
 * ```
 *
 * The merged configuration is validated here so a typo fails the plugin load
 * with a message naming every violation, rather than failing inside the first
 * tool call.
 */
export const WeknoraPlugin: Plugin = async (input, options) => {
  const config = resolveConfig({ ...configFromEnv(), ...(options as Config | undefined) })
  const client = new WeknoraClient(config)
  const tools = createTools(client, config)
  const registered = Object.keys(tools)
  try {
    await input.client.app.log({
      body: {
        service: 'opencode-weknora',
        level: 'info',
        message: `registered ${registered.join(', ')} against ${config.baseUrl}`,
      },
    })
  } catch {
    // Logging is best-effort; registration does not depend on it.
  }
  return { tool: tools }
}

export default WeknoraPlugin
