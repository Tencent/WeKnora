import { del, get, put } from '@/utils/request'

/**
 * Which value an execution would actually use for one variable.
 *
 * `unset` means nothing is stored anywhere, `workspace` means an admin filled
 * it in for everyone, `user` means the caller filled in their own — which wins
 * over the workspace one for this caller only.
 */
export type EnvVarSource = 'unset' | 'workspace' | 'user'

/**
 * One variable as its owner may see it. There is deliberately no value field:
 * the endpoint reports what is missing, never what is stored.
 */
export interface EnvVarView {
  name: string
  description?: string
  required?: boolean
  source: EnvVarSource
  /** Present only for the caller's own value; a workspace value has no per-variable timestamp. */
  updated_at?: string
}

/** One skill's declared credentials. */
export interface SkillEnvGroup {
  skill_id: string
  skill_name: string
  vars: EnvVarView[]
}

/**
 * One sandbox config: the caller's own config-wide variables, injected into
 * every execution on it, plus the credentials its skills declared.
 */
export interface ConfigEnvGroup {
  sandbox_config_id: string
  sandbox_config_name: string
  vars: EnvVarView[]
  skills: SkillEnvGroup[]
}

export function listMyEnvVars(): Promise<{ data: ConfigEnvGroup[] }> {
  return get('/api/v1/me/env-vars') as unknown as Promise<{ data: ConfigEnvGroup[] }>
}

export function setMySkillEnv(
  skillId: string,
  name: string,
  value: string,
): Promise<{ success: boolean }> {
  return put('/api/v1/me/env-vars/skill', { skill_id: skillId, name, value }) as unknown as Promise<{
    success: boolean
  }>
}

/**
 * Removes the caller's own value, after which the workspace value (if any)
 * applies again. The scope and name travel in a JSON body rather than the query
 * string, matching the PUT above.
 */
export function deleteMySkillEnv(skillId: string, name: string): Promise<{ success: boolean }> {
  return del('/api/v1/me/env-vars/skill', { skill_id: skillId, name }) as unknown as Promise<{
    success: boolean
  }>
}

export function setMySandboxEnv(
  configId: string,
  name: string,
  value: string,
): Promise<{ success: boolean }> {
  return put('/api/v1/me/env-vars/sandbox', {
    sandbox_config_id: configId,
    name,
    value,
  }) as unknown as Promise<{ success: boolean }>
}

export function deleteMySandboxEnv(configId: string, name: string): Promise<{ success: boolean }> {
  return del('/api/v1/me/env-vars/sandbox', {
    sandbox_config_id: configId,
    name,
  }) as unknown as Promise<{ success: boolean }>
}
