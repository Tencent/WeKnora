interface EntrySettings {
  selectAgent(id: string, sourceTenantId: string | null): void
  selectKnowledgeBases(ids: string[]): void
}

// Select the agent first: changing it clears stale KB, file and tag selections.
export function applyChatEntrySelection(query: Record<string, unknown>, settings: EntrySettings) {
  if (typeof query.agent_id === 'string' && query.agent_id) {
    const source = typeof query.source_tenant_id === 'string' ? query.source_tenant_id : null
    settings.selectAgent(query.agent_id, source)
  }
  if (typeof query.knowledge_base_id === 'string' && query.knowledge_base_id) {
    settings.selectKnowledgeBases([query.knowledge_base_id])
  }
}
