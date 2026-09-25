import type { SandboxConfigRecord } from '@/api/system'

/** Lite's local skill target. Matches sandbox.HostSkillTargetID on the server. */
export const HOST_SKILL_TARGET_ID = 'host'

export function isHostSkillTarget(id: string | undefined | null): boolean {
  return (id ?? '').trim() === HOST_SKILL_TARGET_ID
}

/** A stand-in config record so config-scoped skill components can target the host. */
export function hostSkillTargetRecord(name: string): SandboxConfigRecord {
  return {
    id: HOST_SKILL_TARGET_ID,
    name,
    sandbox_type: 'host',
    config: {} as SandboxConfigRecord['config'],
    created_at: '',
    updated_at: '',
  }
}

/** Lite desktop: skills install on this computer and remote sandboxes are hidden. */
export function hostSkillsOnly(remoteSupported: boolean, hostSupported: boolean): boolean {
  return !remoteSupported && hostSupported
}

/** Config id the @ picker requests. Lite always asks for the host target. */
export function mentionSkillTargetId(
  hostOnly: boolean,
  sandboxConfigId: string | undefined | null,
): string {
  if (hostOnly) return HOST_SKILL_TARGET_ID
  return (sandboxConfigId ?? '').trim()
}
