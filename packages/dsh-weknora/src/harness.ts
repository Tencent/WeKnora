/**
 * Structural types for the slice of the DeepSeek Harness contract this plugin
 * uses. Declaring them here keeps the package free of runtime dependencies on
 * harness internals: the shipped code only ever hands `ctx.tools.register()` a
 * plain object, so a harness release that adds optional definition fields does
 * not require a republish of this plugin.
 *
 * The shapes mirror `@deepseek-ai/dsh-tools` (`ToolDefinition`) and
 * `@deepseek-ai/dsh-llm` (`ContentBlock`, `ToolSchema`) as of dsh 0.1.0-rc.8.
 */

/** Supported subset of JSON Schema that the harness tool registry accepts. */
export interface JsonSchemaNode {
  type?: 'object' | 'array' | 'string' | 'number' | 'integer' | 'boolean' | 'null'
  properties?: Record<string, JsonSchemaNode>
  required?: string[]
  additionalProperties?: boolean
  items?: JsonSchemaNode
  enum?: (string | number | boolean | null)[]
  const?: string | number | boolean | null
  description?: string
  title?: string
}

/** Model-facing content block returned by `output.render`. */
export interface TextContentBlock {
  type: 'text'
  text: string
}

/** Execution context handed to a tool body. */
export interface ToolRunContext {
  readonly signal: AbortSignal
}

/** One registered model-facing tool. */
export interface ToolDefinition {
  readonly name: string
  readonly description: string
  readonly parameters: JsonSchemaNode
  readonly output: {
    readonly schema: JsonSchemaNode
    render(args: unknown, value: unknown): TextContentBlock[]
  }
  execute(args: unknown, exec: ToolRunContext): Promise<unknown>
  readonly timeoutMs?: number
  isConcurrencySafe?(args: unknown): boolean
}

/** The `ctx.tools` service seam. */
export interface ToolRegistry {
  register(definition: ToolDefinition): () => void
}

/** The slice of the Cordis context this plugin injects. */
export interface HarnessContext {
  readonly tools: ToolRegistry
  readonly logger?: {
    info(...args: unknown[]): void
    warn(...args: unknown[]): void
  }
}
