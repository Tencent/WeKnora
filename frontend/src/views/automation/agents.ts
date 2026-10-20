import { BUILTIN_WEKNORA_ASSISTANT_ID, listAgents, type CustomAgent } from '@/api/agent'

export interface AutomationAgentOption {
  id: string
  name: string
}

// Built-in agents that serve one feature and cannot run an automation.
const RESERVED = new Set(['builtin-wiki-fixer', 'builtin-skill-installer'])

/**
 * Agents the server accepts for an automation: the WeKnora assistant, then
 * this workspace's smart-reasoning agents. Agents shared in from other
 * workspaces are left out because a run would be refused.
 */
export function automationAgentOptions(
  agents: CustomAgent[],
  tenantId: number | string | null | undefined,
  assistantName: string,
): AutomationAgentOption[] {
  const tenant = tenantId == null ? null : Number(tenantId)
  const runnable = agents.filter((agent) =>
    agent.id !== BUILTIN_WEKNORA_ASSISTANT_ID
    && !RESERVED.has(agent.id)
    && agent.config?.agent_mode === 'smart-reasoning'
    && (tenant === null || agent.tenant_id == null || Number(agent.tenant_id) === tenant))
  return [
    { id: BUILTIN_WEKNORA_ASSISTANT_ID, name: assistantName },
    ...runnable.map((agent) => ({ id: agent.id, name: agent.name })),
  ]
}

export async function loadAutomationAgents(tenantId: number | string | null | undefined, assistantName: string) {
  const res = await listAgents()
  return automationAgentOptions(res.data ?? [], tenantId, assistantName)
}
