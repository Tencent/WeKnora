/** Verified by the server; these selectors never grant access by themselves. */
export interface KnowledgeReadOptions {
  agent_id?: string;
  agent_source_tenant_id?: string;
  signal?: AbortSignal;
}

export function withKnowledgeReadContext(url: string, options?: KnowledgeReadOptions): string {
  const query = new URLSearchParams();
  if (options?.agent_id) query.set('agent_id', options.agent_id);
  if (options?.agent_source_tenant_id) query.set('agent_source_tenant_id', options.agent_source_tenant_id);
  const suffix = query.toString();
  return suffix ? `${url}${url.includes('?') ? '&' : '?'}${suffix}` : url;
}
