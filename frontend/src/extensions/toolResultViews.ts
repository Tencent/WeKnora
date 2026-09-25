import type { Component } from 'vue'

import { createRegistry } from './registry'

/** What a tool result view gets to render one tool call. */
export interface ToolResultContext {
  displayType: string
  data: Record<string, any>
  output: string
  arguments: Record<string, any>
  success?: boolean
}

/**
 * Renders the tool results whose display_type equals `key`. Tools that
 * declare no view (or an unknown one) fall back to the raw output.
 */
export interface ToolResultView {
  key: string
  component: Component
  /** Props for the component; defaults to { data }. */
  props?: (ctx: ToolResultContext) => Record<string, unknown>
  pluginId: string
}

export const toolResultViews = createRegistry<ToolResultView>('tool result view')

/** The props a view receives for one tool call. */
export function toolResultProps(view: ToolResultView, ctx: ToolResultContext): Record<string, unknown> {
  return view.props ? view.props(ctx) : { data: ctx.data }
}
