import type { SkillReference } from '@/api/agent'
import type { SkillInfo } from '@/api/skill'

export function skillReferenceKey(skill: SkillInfo): string {
  return `${skill.source ?? 'preloaded'}:${skill.skill_id || skill.name}`
}

export function selectedSkillKeys(refs: SkillReference[] = [], legacyNames: string[] = []): string[] {
  if (refs.length > 0) return refs.map((ref) => `${ref.source}:${ref.skill_id}`)
  return legacyNames.filter(Boolean).map((name) => `preloaded:${name}`)
}

export function skillReferencesFromKeys(keys: string[]): SkillReference[] {
  return [...new Set(keys)].flatMap((key) => {
    const separator = key.indexOf(':')
    if (separator < 1 || separator === key.length - 1) return []
    const source = key.slice(0, separator)
    if (source !== 'preloaded' && source !== 'tenant') return []
    return [{ source, skill_id: key.slice(separator + 1) } as SkillReference]
  })
}
