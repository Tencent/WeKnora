/**
 * Builds the skill install cards of one assistant message from its tool
 * events. Two tools produce them: search_skills (candidates found or resolved
 * for the user) and shell_exec when a command loaded a skill into the session
 * sandbox only, which is offered as the same card with a notice that it is
 * temporary.
 */

export interface SkillCardCandidate {
  name: string
  display_name?: string
  description?: string
  source: string
  registry?: string
  url?: string
  owner?: string
  version?: string
  downloads?: number
  installs?: number
  official?: boolean
  file_count?: number
  install_status?: string
  in_catalog?: boolean
}

export interface SkillCardGroup {
  key: string
  candidates: SkillCardCandidate[]
  /** Where an install from these cards lands; empty when none can be offered. */
  sandboxConfigId: string
  agentSelectsSkills: boolean
  /** The sandbox keeps running conversations on their image after an install. */
  newSessionsOnly: boolean
  /** The skill was only loaded into this session's sandbox by a command. */
  temporary: boolean
  temporaryName?: string
}

interface ToolEventLike {
  type?: string
  tool_name?: string
  tool_call_id?: string
  pending?: boolean
  success?: boolean
  tool_data?: Record<string, any> | null
}

function asCandidate(raw: any): SkillCardCandidate | null {
  if (!raw || typeof raw !== 'object') return null
  const source = typeof raw.source === 'string' ? raw.source.trim() : ''
  const name = typeof raw.name === 'string' ? raw.name.trim() : ''
  if (!source || !name) return null
  return { ...raw, name, source }
}

/** Best guess at the registry of a locator the backend derived from a command. */
export function registryOfSource(source: string): string {
  const s = source.trim().toLowerCase()
  if (s.startsWith('skills-sh:')) return 'skills-sh'
  if (s.startsWith('@') || /^[a-z0-9._-]+(@[\w.-]+)?$/.test(s)) return 'clawhub'
  try {
    const host = new URL(source).hostname.toLowerCase()
    if (host.endsWith('github.com') || host.endsWith('githubusercontent.com')) return 'github'
    if (host.endsWith('gitlab.com')) return 'gitlab'
    if (host.endsWith('skillhub.cn')) return 'skillhub'
    if (host.endsWith('clawhub.ai') || host.endsWith('clawhub.com')) return 'clawhub'
    if (host.endsWith('skills.sh')) return 'skills-sh'
  } catch {
    // Not a URL.
  }
  return 'url'
}

export function collectSkillCardGroups(events: ToolEventLike[] | null | undefined): SkillCardGroup[] {
  const groups: SkillCardGroup[] = []
  const offered = new Set<string>()
  const fresh = (configId: string, c: SkillCardCandidate) => {
    const key = `${configId}\n${c.source}`
    if (offered.has(key)) return false
    offered.add(key)
    return true
  }

  for (const [index, event] of (events || []).entries()) {
    if (!event || event.type !== 'tool_call' || event.pending || event.success === false) continue
    const data = event.tool_data
    if (!data || typeof data !== 'object') continue
    const key = event.tool_call_id || `skill-card-${index}`

    if (event.tool_name === 'search_skills' && data.display_type === 'skill_candidates') {
      const configId = typeof data.sandbox_config_id === 'string' ? data.sandbox_config_id : ''
      const list = Array.isArray(data.candidates) ? data.candidates : []
      const candidates = list
        .map(asCandidate)
        .filter((c: SkillCardCandidate | null): c is SkillCardCandidate => c !== null && fresh(configId, c))
      if (candidates.length === 0) continue
      groups.push({
        key,
        candidates,
        sandboxConfigId: configId,
        agentSelectsSkills: data.agent_selects_skills === true,
        newSessionsOnly: data.new_sessions_only === true,
        temporary: false,
      })
      continue
    }

    const load = event.tool_name === 'shell_exec' ? data.skill_temporary_load : null
    if (load && typeof load === 'object') {
      const configId = typeof load.sandbox_config_id === 'string' ? load.sandbox_config_id : ''
      const source = typeof load.source === 'string' ? load.source.trim() : ''
      const name = (typeof load.name === 'string' && load.name.trim()) || source
      const candidate: SkillCardCandidate | null = source
        ? { name, source, registry: registryOfSource(source) }
        : null
      // Loading the same skill again adds nothing the first card lacks.
      if (candidate && !fresh(configId, candidate)) continue
      groups.push({
        key,
        // Without a derivable source the notice still shows: it is the part
        // that tells the user the skill is not really installed.
        candidates: candidate ? [candidate] : [],
        sandboxConfigId: configId,
        agentSelectsSkills: load.agent_selects_skills === true,
        newSessionsOnly: load.new_sessions_only === true,
        temporary: true,
        temporaryName: name || undefined,
      })
    }
  }
  return groups
}
