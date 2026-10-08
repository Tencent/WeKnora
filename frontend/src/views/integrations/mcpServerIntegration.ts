/**
 * Pure helpers for the "WeKnora as MCP server" integration page. Kept free
 * of Vue so the snippet builders can be unit-tested with node:test.
 */

export const MCP_TOKEN_PLACEHOLDER = '<YOUR_TOKEN>'

/**
 * Derive the public MCP URL from the displayed API base (".../api/v1") and
 * the endpoint path ("/mcp/<id>") returned by the backend.
 */
export function buildMcpEndpointUrl(apiBaseUrl: string, endpointPath: string): string {
  const base = (apiBaseUrl || '').trim().replace(/\/+$/, '').replace(/\/api\/v1$/, '')
  const path = endpointPath.startsWith('/') ? endpointPath : `/${endpointPath}`
  return `${base}${path}`
}

/**
 * Sanitize an endpoint name into an mcpServers key. Names that leave no
 * ASCII characters (e.g. Chinese) fall back to the endpoint id so two such
 * endpoints never collide in the client configuration.
 */
export function mcpServerKey(name: string, id = ''): string {
  const slug = (name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  if (slug) return `weknora-${slug}`
  const idSlug = (id || '').replace(/[^a-z0-9]/gi, '').slice(0, 8).toLowerCase()
  return idSlug ? `weknora-${idSlug}` : 'weknora'
}

/** JSON block for clients that speak Streamable HTTP natively (Cursor, VS Code, Claude Desktop connectors). */
export function buildHttpClientSnippet(name: string, url: string, token: string, id = ''): string {
  return JSON.stringify(
    {
      mcpServers: {
        [mcpServerKey(name, id)]: {
          url,
          headers: { Authorization: `Bearer ${token || MCP_TOKEN_PLACEHOLDER}` },
        },
      },
    },
    null,
    2,
  )
}

/** One-liner for Claude Code. */
export function buildClaudeCodeCommand(name: string, url: string, token: string, id = ''): string {
  const auth = `Authorization: Bearer ${token || MCP_TOKEN_PLACEHOLDER}`
  return `claude mcp add --transport http ${mcpServerKey(name, id)} ${url} --header "${auth}"`
}

/** JSON block for stdio-only clients, bridged through mcp-remote. */
export function buildStdioBridgeSnippet(name: string, url: string, token: string, id = ''): string {
  return JSON.stringify(
    {
      mcpServers: {
        [mcpServerKey(name, id)]: {
          command: 'npx',
          args: ['-y', 'mcp-remote', url, '--header', `Authorization: Bearer ${token || MCP_TOKEN_PLACEHOLDER}`],
        },
      },
    },
    null,
    2,
  )
}

export interface ToolCatalogEntry {
  name: string
  group: string
  destructive: boolean
}

export interface GroupedTools<T extends ToolCatalogEntry> {
  group: string
  tools: T[]
}

/** Group catalog entries in the backend's group order, dropping empty groups. */
export function groupTools<T extends ToolCatalogEntry>(groups: string[], tools: T[]): GroupedTools<T>[] {
  const known = new Set(groups)
  const order = [...groups, ...tools.map((t) => t.group).filter((g) => !known.has(g))]
  const out: GroupedTools<T>[] = []
  for (const group of Array.from(new Set(order))) {
    const members = tools.filter((t) => t.group === group)
    if (members.length > 0) out.push({ group, tools: members })
  }
  return out
}

/** Summarize a knowledge-base scope for the card: empty means "all". */
export function summarizeScope(ids: string[] | null | undefined, names: Record<string, string>): string[] {
  return (ids || []).map((id) => names[id] || id)
}
