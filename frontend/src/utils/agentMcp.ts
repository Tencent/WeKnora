// The backend treats an agent as MCP-enabled only for "all" or "selected"
// with at least one service. An empty mode means none; a "none" agent keeps
// its previously selected service IDs so switching back restores them, but
// those IDs must not make the agent look enabled.
export interface McpCapableConfig {
  mcp_selection_mode?: string | null
  mcp_services?: readonly string[] | null
}

export function agentUsesMcp(config?: McpCapableConfig | null): boolean {
  return config?.mcp_selection_mode === 'all' ||
    (config?.mcp_selection_mode === 'selected' && (config.mcp_services?.length ?? 0) > 0)
}
