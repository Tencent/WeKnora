import type { WebSearchProviderEntity } from '@/api/web-search-provider';

export type AgentWebSearchConfig = {
  web_search_enabled?: boolean;
  web_search_provider_id?: string;
};

/** 解析智能体实际会使用的搜索引擎 ID（与后端 agent > tenant default 逻辑一致） */
export function resolveAgentWebSearchProviderId(
  config: AgentWebSearchConfig | undefined,
  providers: WebSearchProviderEntity[],
): string | null {
  const explicitId = config?.web_search_provider_id?.trim();
  if (explicitId) {
    return providers.some((p) => p.id === explicitId) ? explicitId : null;
  }
  const defaultProvider = providers.find((p) => p.is_default);
  return defaultProvider?.id ?? null;
}

export function isAgentWebSearchEnabled(config: AgentWebSearchConfig | undefined): boolean {
  return config?.web_search_enabled === true;
}

/** 智能体已启用网络搜索，且能解析到可用搜索引擎 */
export function isAgentWebSearchReady(
  config: AgentWebSearchConfig | undefined,
  providers: WebSearchProviderEntity[],
): boolean {
  if (!isAgentWebSearchEnabled(config)) return false;
  return resolveAgentWebSearchProviderId(config, providers) !== null;
}

/** 空间级默认搜索引擎是否可用（无智能体约束时） */
export function isTenantWebSearchReady(providers: WebSearchProviderEntity[]): boolean {
  return providers.some((p) => p.is_default);
}
