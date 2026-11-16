import { get, put } from '@/utils/request'

// Mirrors internal/types/assistant.go.
export type AssistantAccessLevel = 'read_only' | 'confirm' | 'auto_write'

export const ASSISTANT_ACCESS_LEVELS: AssistantAccessLevel[] = ['read_only', 'confirm', 'auto_write']

export const DEFAULT_ASSISTANT_ACCESS_LEVEL: AssistantAccessLevel = 'confirm'

export const MAX_ASSISTANT_DISPLAY_NAME_LENGTH = 24

export interface AssistantConfig {
  /** Empty keeps the default name. */
  display_name: string
  /** The highest level a member may pick. */
  max_access_level: AssistantAccessLevel
}

export function isAssistantAccessLevel(value: unknown): value is AssistantAccessLevel {
  return typeof value === 'string' && (ASSISTANT_ACCESS_LEVELS as string[]).includes(value)
}

/** The level a run gets: the requested one, never above the ceiling. */
export function capAssistantAccessLevel(
  requested: AssistantAccessLevel,
  ceiling: AssistantAccessLevel,
): AssistantAccessLevel {
  const rank = (level: AssistantAccessLevel) => ASSISTANT_ACCESS_LEVELS.indexOf(level)
  return rank(requested) > rank(ceiling) ? ceiling : requested
}

export function getTenantAssistantConfig() {
  return get<{ success: boolean; data: AssistantConfig }>('/api/v1/tenants/kv/assistant-config')
}

export function updateTenantAssistantConfig(config: AssistantConfig) {
  return put<{ success: boolean; data: AssistantConfig }>('/api/v1/tenants/kv/assistant-config', config)
}
