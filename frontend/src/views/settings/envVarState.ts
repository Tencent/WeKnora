import type { ConfigEnvGroup, EnvVarSource, EnvVarView, SkillEnvGroup } from '@/api/env-vars'

/**
 * Pure state helpers for the environment variable UIs.
 *
 * Everything a reviewer would want to assert lives here rather than in the
 * `.vue` files, because this repo tests plain TypeScript on `node:test` and
 * does not do component testing.
 */

/**
 * Mirror `MaxEnvValueBytes` and `MaxUserEnvVarsPerScope` in
 * `internal/application/service/tenant_skill_env_declare.go`. The server
 * remains authoritative; these browser checks only turn a round trip into
 * immediate feedback.
 */
export const MAX_ENV_VALUE_BYTES = 8192
export const MAX_USER_ENV_VARS_PER_SCOPE = 50

export function isValidEnvValueLength(value: string): boolean {
  return new TextEncoder().encode(value).length <= MAX_ENV_VALUE_BYTES
}

/**
 * Overwriting a name the member already owns remains valid at the limit, so
 * values can still be rotated.
 */
export function canAddEnvVar(existing: EnvVarView[], name: string): boolean {
  const owned = existing.filter((entry) => entry.source === 'user')
  return (
    owned.some((entry) => entry.name === name) || owned.length < MAX_USER_ENV_VARS_PER_SCOPE
  )
}

/** Builds the admin PATCH body from edited drafts intersected with declarations. */
export function editedSkillEnvPayload(
  declaredNames: string[],
  drafts: Record<string, string> | undefined,
): Record<string, string> {
  if (!drafts) return {}
  const declared = new Set(declaredNames)
  return Object.fromEntries(
    Object.entries(drafts).filter(([name]) => declared.has(name)),
  )
}

/** Admin PATCH body that clears one stored value and keeps the declaration. */
export function adminSkillEnvClearPayload(name: string): Record<string, string> {
  return { [name]: '' }
}

/** The clear control is only meaningful when a workspace value is already stored. */
export function canClearAdminSkillEnv(env: { is_set?: boolean }): boolean {
  return env.is_set === true
}

/** The key control only belongs on a skill that declared at least one variable. */
export function skillHasDeclaredEnvs(skill: { envs?: readonly unknown[] | null }): boolean {
  return (skill.envs?.length ?? 0) > 0
}

/**
 * Removes a submitted draft only if it still equals the sent value. New input
 * typed while the request was in flight always wins over response cleanup.
 */
export function clearSubmittedSkillEnvDrafts(
  currentDrafts: Record<string, string>,
  submitted: Record<string, string>,
): Record<string, string> {
  const remaining = { ...currentDrafts }
  for (const [name, value] of Object.entries(submitted)) {
    if (remaining[name] === value) delete remaining[name]
  }
  return remaining
}

export type SkillEnvSavesInFlight = Record<string, true>

function skillEnvSaveKey(configId: string, skillId: string): string {
  return JSON.stringify([configId, skillId])
}

export function isSkillEnvSaveInFlight(
  inFlight: Readonly<SkillEnvSavesInFlight>,
  configId: string,
  skillId: string,
): boolean {
  return inFlight[skillEnvSaveKey(configId, skillId)] === true
}

export function addSkillEnvSaveInFlight(
  inFlight: Readonly<SkillEnvSavesInFlight>,
  configId: string,
  skillId: string,
): SkillEnvSavesInFlight {
  return { ...inFlight, [skillEnvSaveKey(configId, skillId)]: true }
}

export function clearSkillEnvSaveInFlight(
  inFlight: Readonly<SkillEnvSavesInFlight>,
  configId: string,
  skillId: string,
): SkillEnvSavesInFlight {
  const remaining = { ...inFlight }
  delete remaining[skillEnvSaveKey(configId, skillId)]
  return remaining
}

/**
 * Names the sandbox refuses, mirroring `reservedEnvNames` in
 * `internal/application/service/tenant_skill_env_declare.go`. Rejecting them
 * in the browser is not a security boundary — the server checks again — it
 * just turns a 400 into an inline hint before the user hits save.
 */
export const RESERVED_ENV_NAMES: Set<string> = new Set([
  'PATH',
  'HOME',
  'USER',
  'SHELL',
  'LD_PRELOAD',
  'LD_LIBRARY_PATH',
  'PYTHONPATH',
  'PYTHONHOME',
  'NODE_OPTIONS',
])

/** The sandbox hands the skill its artifact directory through this prefix. */
const RESERVED_ENV_PREFIX = 'WEKNORA_'

/** Mirrors `envNamePattern` server-side: UPPER_SNAKE_CASE, at most 128 chars. */
const ENV_NAME_PATTERN = /^[A-Z_][A-Z0-9_]{0,127}$/

export function isValidEnvName(name: string): boolean {
  if (!ENV_NAME_PATTERN.test(name)) return false
  if (RESERVED_ENV_NAMES.has(name)) return false
  return !name.startsWith(RESERVED_ENV_PREFIX)
}

export interface EnvStatus {
  key: EnvVarSource
  /** True only when the skill cannot run without this variable and nothing supplies it. */
  blocking: boolean
}

export function statusOf(v: EnvVarView): EnvStatus {
  const key: EnvVarSource =
    v.source === 'user' || v.source === 'workspace' ? v.source : 'unset'
  return { key, blocking: key === 'unset' && v.required === true }
}

/** The config's display name, falling back to its id so a group is never nameless. */
export function configLabel(group: Pick<ConfigEnvGroup, 'sandbox_config_id' | 'sandbox_config_name'>): string {
  return group.sandbox_config_name?.trim() || group.sandbox_config_id
}

/**
 * Groups ordered by config name, on a copy. The list is re-read after every
 * write, and reordering the array the caller still holds would move rows out
 * from under a half-filled input.
 */
export function sortedConfigGroups(groups: ConfigEnvGroup[]): ConfigEnvGroup[] {
  return [...groups].sort((a, b) => {
    const byName = configLabel(a).localeCompare(configLabel(b))
    if (byName !== 0) return byName
    return a.sandbox_config_id.localeCompare(b.sandbox_config_id)
  })
}

/**
 * Sandbox-wide extras the member already stored. Empty configs stay off the
 * settings page — listing every backend as a blank card is noise.
 */
export function sandboxGroupsWithVars(groups: ConfigEnvGroup[]): ConfigEnvGroup[] {
  return groups.filter((group) => (group.vars?.length ?? 0) > 0)
}

/** How many required declarations still have no user or workspace value. */
export function blockingVarCount(vars: EnvVarView[] | undefined): number {
  return (vars || []).filter((entry) => statusOf(entry).blocking).length
}

/**
 * One skill's credentials, lifted out of the config group it was installed on.
 * The settings page leads with these; sandbox-wide extras stay secondary.
 */
export interface SkillSecretCard {
  sandbox_config_id: string
  sandbox_config_name: string
  skill: SkillEnvGroup
}

export function skillSecretCards(groups: ConfigEnvGroup[]): SkillSecretCard[] {
  const cards: SkillSecretCard[] = []
  for (const group of groups) {
    for (const skill of group.skills || []) {
      if (!skill.vars?.length) continue
      cards.push({
        sandbox_config_id: group.sandbox_config_id,
        sandbox_config_name: configLabel(group),
        skill,
      })
    }
  }
  return cards.sort((a, b) => {
    const bySkill = (a.skill.skill_name || a.skill.skill_id).localeCompare(
      b.skill.skill_name || b.skill.skill_id,
    )
    if (bySkill !== 0) return bySkill
    return a.sandbox_config_name.localeCompare(b.sandbox_config_name)
  })
}
